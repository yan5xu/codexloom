package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

var restartManagedConnectorService = restartManagedGatewayService
var restartManagedConnector = func(ctx context.Context, s *Server, connection hub.PlatformConnection) (bool, error) {
	return s.restartManagedGatewayVerified(ctx, connection)
}
var prepareManagedConnectorRestart = func(s *Server, connection hub.PlatformConnection) (managedGatewayRestartPlan, error) {
	return s.prepareManagedGatewayRestart(connection)
}
var writeManagedConnectorRestartUnit = writeManagedGatewayRestartUnit
var waitManagedConnectorRestartHeartbeat = func(ctx context.Context, s *Server, connectionID string, after time.Time, expected hub.CredentialMigrationGatewayReceipt) (string, error) {
	return s.waitForManagedGatewayRestartHeartbeat(ctx, connectionID, after, expected)
}
var beforeManagedConnectorRestartLock = func(string) {}
var preflightManagedConnectorCredential = func(s *Server, connection hub.PlatformConnection) error {
	return s.preflightManagedGatewayCredential(connection)
}
var preflightManagedConnectorProcess = func(s *Server, connection hub.PlatformConnection) error {
	return s.preflightMigrationGateway(connection)
}
var runManagedServiceCommand = func(name string, arguments ...string) ([]byte, error) {
	return exec.Command(name, arguments...).CombinedOutput()
}
var waitManagedServiceRetry = time.Sleep

const (
	managedServiceBootstrapAttempts   = 20
	managedServiceBootstrapRetryDelay = 500 * time.Millisecond
	managedGatewayHeartbeatTimeout    = 20 * time.Second
	managedGatewayHeartbeatPoll       = 200 * time.Millisecond
)

type managedGatewayRestartPlan struct {
	Applicable   bool
	UnitPath     string
	OriginalUnit []byte
	TargetUnit   []byte
	RecoveryUnit []byte
	Previous     hub.CredentialMigrationGatewayReceipt
	Target       hub.CredentialMigrationGatewayReceipt
	Recovery     hub.CredentialMigrationGatewayReceipt
}

// RestartManagedGateways makes a Hub restart an atomic backend update: every
// active managed Connector is restarted against the sibling binaries and
// adapter sources shipped with the new Hub build.
func (s *Server) RestartManagedGateways() {
	for _, initial := range s.hub.ListConnections() {
		if !initial.Enabled || initial.ArchivedAt != "" {
			continue
		}
		provider := managedGatewayProvider(initial.Provider)
		if provider == "" {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(initial.CredentialRef), credentialstore.ManagedReferencePrefix) {
			log.Printf("[codex-loom] keep %s gateway %s on its current executable: credential is not migrated", provider, initial.ID)
			continue
		}
		if hub.ConnectionManualRecoveryRequired(initial) {
			log.Printf("[codex-loom] keep managed gateway %s in manual recovery until explicit reconcile", initial.ID)
			continue
		}
		snapshot, err := s.hub.SnapshotConnectionBinding(initial.ID)
		if err != nil {
			log.Printf("[codex-loom] snapshot managed %s gateway %s restart: %v", provider, initial.ID, err)
			continue
		}
		beforeManagedConnectorRestartLock(initial.ID)
		unlock := s.lockCredentialMigration("connection:" + initial.ID)
		func() {
			defer unlock()
			if err := s.hub.RequireCredentialMigrationsIdle(initial.ID); err != nil {
				log.Printf("[codex-loom] keep %s gateway %s on its current registration: %v", provider, initial.ID, err)
				return
			}
			if err := s.hub.MatchConnectionBinding(snapshot); err != nil {
				log.Printf("[codex-loom] skip stale managed %s gateway %s restart: %v", provider, initial.ID, err)
				return
			}
			connection, err := s.migrationConnection(initial.ID)
			if err != nil || !connection.Enabled || connection.ArchivedAt != "" || managedGatewayProvider(connection.Provider) != provider ||
				!strings.HasPrefix(strings.TrimSpace(connection.CredentialRef), credentialstore.ManagedReferencePrefix) || hub.ConnectionManualRecoveryRequired(connection) {
				log.Printf("[codex-loom] skip ineligible managed %s gateway %s restart after lock: %v", provider, initial.ID, err)
				return
			}
			if err := preflightManagedConnectorCredential(s, connection); err != nil {
				log.Printf("[codex-loom] keep %s gateway %s on its current executable: managed credential preflight failed: %v", provider, connection.ID, err)
				return
			}
			if err := preflightManagedConnectorProcess(s, connection); err != nil {
				log.Printf("[codex-loom] keep %s gateway %s on its current registration: process preflight failed: %v", provider, connection.ID, err)
				return
			}
			// Ordinary Connection control writes share this process-local lock;
			// repeat the durable comparison at the last boundary before the
			// service effect so a stale initial scan can never reach the manager.
			if err := s.hub.RequireCredentialMigrationsIdle(connection.ID); err != nil {
				log.Printf("[codex-loom] skip managed %s gateway %s restart before effect: %v", provider, connection.ID, err)
				return
			}
			if err := s.hub.MatchConnectionBinding(snapshot); err != nil {
				log.Printf("[codex-loom] skip stale managed %s gateway %s restart before effect: %v", provider, connection.ID, err)
				return
			}
			if !s.claimManagedGatewayRestart(connection.ID) {
				log.Printf("[codex-loom] skip duplicate managed %s gateway %s restart attempt in this Hub generation", provider, connection.ID)
				return
			}
			restarted, err := restartManagedConnector(context.Background(), s, connection)
			if err != nil {
				var failure *managedGatewayRestartFailure
				if errors.As(err, &failure) && !failure.Recovered {
					if persistErr := s.hub.MarkConnectionManualRecovery(connection.ID, "manual_recovery_required: managed gateway restart failed and previous registration could not be recovered"); persistErr != nil {
						log.Printf("[codex-loom] persist managed gateway manual recovery %s: %v", connection.ID, persistErr)
					}
				}
				log.Printf("[codex-loom] restart %s gateway %s: %v", provider, connection.ID, err)
				return
			}
			if restarted {
				log.Printf("[codex-loom] restarted and verified managed %s gateway %s", provider, connection.ID)
			}
		}()
	}
}

func (s *Server) claimManagedGatewayRestart(connectionID string) bool {
	s.managedGatewayMu.Lock()
	defer s.managedGatewayMu.Unlock()
	connectionID = strings.TrimSpace(connectionID)
	if s.managedGatewayAttempts == nil {
		s.managedGatewayAttempts = map[string]struct{}{}
	}
	if _, exists := s.managedGatewayAttempts[connectionID]; exists {
		return false
	}
	s.managedGatewayAttempts[connectionID] = struct{}{}
	return true
}

func (s *Server) restartManagedGatewayVerified(ctx context.Context, connection hub.PlatformConnection) (bool, error) {
	plan, err := prepareManagedConnectorRestart(s, connection)
	if err != nil || !plan.Applicable {
		return false, err
	}
	if err := s.hub.MarkConnectionManualRecovery(connection.ID, "manual_recovery_required: managed gateway restart effect is not yet reconciled"); err != nil {
		return false, fmt.Errorf("persist managed gateway restart recovery latch: %w", err)
	}
	if err := writeManagedConnectorRestartUnit(plan.UnitPath, plan.TargetUnit); err != nil {
		recovered := s.restoreManagedGatewayRegistration(ctx, connection, plan)
		return false, &managedGatewayRestartFailure{Stage: "write target gateway registration", Recovered: recovered == nil, Cause: errors.Join(err, recovered)}
	}
	started := time.Now().UTC()
	restarted, effectErr := restartManagedConnectorService(connection.Provider, connection.ID)
	if effectErr == nil && !restarted {
		effectErr = errors.New("managed gateway service did not restart")
	}
	if effectErr == nil {
		if _, heartbeatErr := waitManagedConnectorRestartHeartbeat(ctx, s, connection.ID, started, plan.Target); heartbeatErr == nil {
			return true, nil
		} else {
			effectErr = heartbeatErr
		}
	}
	recovered := s.restoreManagedGatewayRegistration(ctx, connection, plan)
	return false, &managedGatewayRestartFailure{Stage: "managed gateway replacement verification", Recovered: recovered == nil, Cause: errors.Join(effectErr, recovered)}
}

func (s *Server) restoreManagedGatewayRegistration(ctx context.Context, connection hub.PlatformConnection, plan managedGatewayRestartPlan) error {
	if err := writeManagedConnectorRestartUnit(plan.UnitPath, plan.RecoveryUnit); err != nil {
		return fmt.Errorf("restore previous gateway unit: %w", err)
	}
	started := time.Now().UTC()
	restarted, err := restartManagedConnectorService(connection.Provider, connection.ID)
	if err != nil {
		var failure *managedGatewayRestartFailure
		if !errors.As(err, &failure) || !failure.Recovered {
			return fmt.Errorf("restart previous gateway registration: %w", err)
		}
		restarted = true
	}
	if !restarted {
		return errors.New("previous gateway registration did not restart")
	}
	if _, err := waitManagedConnectorRestartHeartbeat(ctx, s, connection.ID, started, plan.Recovery); err != nil {
		return fmt.Errorf("verify previous gateway registration: %w", err)
	}
	return nil
}

func (s *Server) waitForManagedGatewayRestartHeartbeat(ctx context.Context, connectionID string, after time.Time, expected hub.CredentialMigrationGatewayReceipt) (string, error) {
	timeout := time.NewTimer(managedGatewayHeartbeatTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(managedGatewayHeartbeatPoll)
	defer ticker.Stop()
	for {
		if heartbeat, ok := managedGatewayHeartbeat(s.hub.ListConnections(), connectionID, after, expected); ok {
			if _, err := s.hub.ClearConnectionManualRecovery(connectionID, after, expected); err != nil {
				return "", err
			}
			return heartbeat, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("managed gateway heartbeat verification timed out")
		case <-ticker.C:
		}
	}
}

func managedGatewayHeartbeat(connections []hub.PlatformConnection, connectionID string, after time.Time, expected hub.CredentialMigrationGatewayReceipt) (string, bool) {
	if !buildinfo.ValidBuildIdentity(expected.Build) || !buildinfo.ValidExecutableSHA256(expected.ExecutableSHA256) || !validOptionalGatewayGeneration(expected.Generation) {
		return "", false
	}
	for _, connection := range connections {
		if connection.ID != connectionID || connection.Status != "connected" && !hub.ConnectionManualRecoveryRequired(connection) || connection.GatewayGeneration != expected.Generation ||
			connection.GatewayBuild != expected.Build || connection.GatewayExecutableSHA256 != expected.ExecutableSHA256 {
			continue
		}
		heartbeat, err := time.Parse(time.RFC3339Nano, connection.LastHeartbeatAt)
		if err == nil && !heartbeat.Before(after) {
			return connection.LastHeartbeatAt, true
		}
	}
	return "", false
}

func validOptionalGatewayGeneration(value string) bool {
	if len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

type managedGatewayRestartFailure struct {
	Stage     string
	Recovered bool
	Cause     error
}

func (e *managedGatewayRestartFailure) Error() string {
	status := "previous registration recovery failed"
	if e.Recovered {
		status = "previous registration recovered"
	}
	return fmt.Sprintf("%s failed (%s): %v", e.Stage, status, e.Cause)
}

func (e *managedGatewayRestartFailure) Unwrap() error { return e.Cause }

func (s *Server) preflightManagedGatewayCredential(connection hub.PlatformConnection) error {
	credentials, err := s.hub.CredentialStore()
	if err != nil {
		return err
	}
	switch managedGatewayProvider(connection.Provider) {
	case "feishu":
		secret, err := feishu.LoadAppSecretReference(credentials, connection.CredentialRef, connection.AccountRef)
		if err != nil || secret == "" {
			return errors.Join(err, errors.New("Feishu credential is unavailable"))
		}
	case "slack":
		_, tokens, err := loomslack.LoadTokensAndAppReference(credentials, connection.CredentialRef, "", connection.AccountRef)
		if err != nil || tokens.Bot == "" || tokens.App == "" {
			return errors.Join(err, errors.New("Slack credential is unavailable"))
		}
	case "parall":
		addresses, err := s.hub.ListAddresses("")
		if err != nil {
			return err
		}
		agentID := ""
		for _, address := range addresses {
			if address.ConnectionID == connection.ID && address.ArchivedAt == "" && address.DeletedAt == "" {
				agentID = strings.TrimPrefix(strings.TrimSpace(address.ExternalIdentity), "prll://")
				break
			}
		}
		loaded, err := parall.LoadAgentCredentialsReference(credentials, connection.CredentialRef, connection.AccountRef, agentID)
		if err != nil || loaded.APIURL == "" || loaded.APIKey == "" {
			return errors.Join(err, errors.New("Parall credential is unavailable"))
		}
	}
	return nil
}

func managedGatewayProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "lark", "feishu":
		return "feishu"
	case "slack":
		return "slack"
	case "parall":
		return "parall"
	default:
		return ""
	}
}

func restartManagedGatewayService(provider, connectionID string) (bool, error) {
	provider = managedGatewayProvider(provider)
	connectionID = safeServicePart(connectionID)
	if provider == "" || connectionID == "" {
		return false, nil
	}
	switch runtime.GOOS {
	case "darwin":
		uid := fmt.Sprint(os.Getuid())
		label := fmt.Sprintf("com.codexloom.%s.%s", provider, connectionID)
		target := "gui/" + uid + "/" + label
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("resolve home directory: %w", err)
		}
		unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		if _, err := os.Stat(unitPath); os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("inspect managed gateway unit: %w", err)
		}
		// Replacing the gateway binary can invalidate launchd's cached lightweight
		// code requirement. A kickstart alone then exits with EX_CONFIG (78), so
		// refresh the job registration before starting the new executable.
		_, _ = runManagedServiceCommand("launchctl", "bootout", target)
		if err := bootstrapManagedLaunchAgent(uid, target, unitPath); err != nil {
			recovered := recoverManagedLaunchAgent(uid, target, unitPath)
			return false, &managedGatewayRestartFailure{Stage: "launchctl bootstrap", Recovered: recovered == nil, Cause: errors.Join(err, recovered)}
		}
		if output, err := runManagedServiceCommand("launchctl", "kickstart", "-k", target); err != nil {
			kickstartErr := fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(output)))
			recovered := recoverManagedLaunchAgent(uid, target, unitPath)
			return false, &managedGatewayRestartFailure{Stage: "launchctl kickstart", Recovered: recovered == nil, Cause: errors.Join(kickstartErr, recovered)}
		}
		return true, nil
	case "linux":
		service := fmt.Sprintf("codexloom-%s-%s.service", provider, connectionID)
		if output, err := runManagedServiceCommand("systemctl", "--user", "daemon-reload"); err != nil {
			return false, &managedGatewayRestartFailure{Stage: "systemctl daemon-reload", Cause: fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(output)))}
		}
		if _, err := runManagedServiceCommand("systemctl", "--user", "is-active", "--quiet", service); err != nil {
			return false, nil
		}
		if output, err := runManagedServiceCommand("systemctl", "--user", "restart", service); err != nil {
			restartErr := fmt.Errorf("systemctl restart: %s", strings.TrimSpace(string(output)))
			_, recovered := runManagedServiceCommand("systemctl", "--user", "start", service)
			return false, &managedGatewayRestartFailure{Stage: "systemctl restart", Recovered: recovered == nil, Cause: errors.Join(restartErr, recovered)}
		}
		return true, nil
	default:
		return false, nil
	}
}

func recoverManagedLaunchAgent(uid, target, unitPath string) error {
	_, _ = runManagedServiceCommand("launchctl", "bootout", target)
	if err := bootstrapManagedLaunchAgent(uid, target, unitPath); err != nil {
		return err
	}
	if output, err := runManagedServiceCommand("launchctl", "kickstart", "-k", target); err != nil {
		return fmt.Errorf("launchctl recovery kickstart: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func bootstrapManagedLaunchAgent(uid, target, unitPath string) error {
	var lastMessage string
	for attempt := 1; attempt <= managedServiceBootstrapAttempts; attempt++ {
		output, err := runManagedServiceCommand("launchctl", "bootstrap", "gui/"+uid, unitPath)
		if err == nil {
			if attempt > 1 {
				log.Printf("[codex-loom] launchctl bootstrap %s succeeded after %d attempts", target, attempt)
			}
			return nil
		}
		lastMessage = strings.TrimSpace(string(output))
		if lastMessage == "" {
			lastMessage = err.Error()
		}
		if !isTransientLaunchctlBootstrapError(lastMessage) {
			return fmt.Errorf("launchctl bootstrap: %s", lastMessage)
		}
		if attempt < managedServiceBootstrapAttempts {
			waitManagedServiceRetry(managedServiceBootstrapRetryDelay)
		}
	}
	return fmt.Errorf("launchctl bootstrap failed after %d attempts: %s", managedServiceBootstrapAttempts, lastMessage)
}

func isTransientLaunchctlBootstrapError(message string) bool {
	return strings.Contains(strings.ToLower(message), "input/output error")
}
