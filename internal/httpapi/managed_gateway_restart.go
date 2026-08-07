package httpapi

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

var restartManagedConnectorService = restartManagedGatewayService
var preflightManagedConnectorCredential = func(s *Server, connection hub.PlatformConnection) error {
	return s.preflightManagedGatewayCredential(connection)
}
var runManagedServiceCommand = func(name string, arguments ...string) ([]byte, error) {
	return exec.Command(name, arguments...).CombinedOutput()
}
var waitManagedServiceRetry = time.Sleep

const (
	managedServiceBootstrapAttempts   = 20
	managedServiceBootstrapRetryDelay = 500 * time.Millisecond
)

// RestartManagedGateways makes a Hub restart an atomic backend update: every
// active managed Connector is restarted against the sibling binaries and
// adapter sources shipped with the new Hub build.
func (s *Server) RestartManagedGateways() {
	for _, connection := range s.hub.ListConnections() {
		if !connection.Enabled {
			continue
		}
		provider := managedGatewayProvider(connection.Provider)
		if provider == "" {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(connection.CredentialRef), credentialstore.ManagedReferencePrefix) {
			log.Printf("[codex-loom] keep %s gateway %s on its current executable: credential is not migrated", provider, connection.ID)
			continue
		}
		if err := preflightManagedConnectorCredential(s, connection); err != nil {
			log.Printf("[codex-loom] keep %s gateway %s on its current executable: managed credential preflight failed: %v", provider, connection.ID, err)
			continue
		}
		restarted, err := restartManagedConnectorService(provider, connection.ID)
		if err != nil {
			log.Printf("[codex-loom] restart %s gateway %s: %v", provider, connection.ID, err)
			continue
		}
		if restarted {
			log.Printf("[codex-loom] restarted managed %s gateway %s", provider, connection.ID)
		}
	}
}

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
			return false, err
		}
		if output, err := runManagedServiceCommand("launchctl", "kickstart", "-k", target); err != nil {
			_, _ = runManagedServiceCommand("launchctl", "bootout", target)
			return false, fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(output)))
		}
		return true, nil
	case "linux":
		service := fmt.Sprintf("codexloom-%s-%s.service", provider, connectionID)
		if err := exec.Command("systemctl", "--user", "is-active", "--quiet", service).Run(); err != nil {
			return false, nil
		}
		if output, err := exec.Command("systemctl", "--user", "restart", service).CombinedOutput(); err != nil {
			return false, fmt.Errorf("systemctl restart: %s", strings.TrimSpace(string(output)))
		}
		return true, nil
	default:
		return false, nil
	}
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
