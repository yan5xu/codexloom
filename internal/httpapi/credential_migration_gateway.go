//go:build unix

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/hub"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

const migrationGatewayHeartbeatTimeout = 20 * time.Second

var (
	plistStringPattern  = regexp.MustCompile(`<string>([^<]*)</string>`)
	systemdQuotePattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
)

type migrationGatewayService struct {
	Provider string
	Manager  string
	Service  string
	UnitPath string
	Target   string
}

func (s *Server) preflightMigrationGateway(connection hub.PlatformConnection) error {
	provider := managedGatewayProvider(connection.Provider)
	if provider == "" {
		if strings.EqualFold(connection.Provider, "github") {
			return nil
		}
		return errors.New("provider has no managed credential migration gateway")
	}
	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		return err
	}
	activeAddresses := 0
	for _, address := range addresses {
		if address.ConnectionID == connection.ID && address.Enabled && address.ArchivedAt == "" && address.DeletedAt == "" {
			activeAddresses++
		}
	}
	if activeAddresses != 1 {
		return errors.New("connection must have exactly one active Address")
	}
	service, err := migrationGatewayServiceFor(provider, connection.ID)
	if err != nil {
		return err
	}
	if err := rejectUnanchoredLegacyGatewayUnits(provider, connection.ID, service.UnitPath); err != nil {
		return err
	}
	unit, err := readBoundedPrivateFile(service.UnitPath, 1<<20, false)
	if err != nil {
		return err
	}
	arguments, err := gatewayUnitArguments(string(unit), service.Manager)
	if err != nil {
		return err
	}
	wrapperName := "loom-" + provider + "-gateway"
	if provider == "feishu" {
		wrapperName = "loom-feishu-gateway"
	}
	wrapperPath := findArgumentByBase(arguments, wrapperName)
	if wrapperPath == "" {
		return errors.New("gateway wrapper path is missing from the unit")
	}
	if _, err := readBoundedPrivateFile(wrapperPath, 128<<20, true); err != nil {
		return err
	}
	if provider == "slack" || provider == "parall" {
		scriptPath := findArgumentByBase(arguments, provider+".mjs")
		if scriptPath == "" {
			return errors.New("gateway adapter path is missing from the unit")
		}
		if _, err := readBoundedPrivateFile(scriptPath, 128<<20, false); err != nil {
			return err
		}
		if _, err := readBoundedPrivateFile(filepath.Join(filepath.Dir(scriptPath), provider+"-protocol.mjs"), 128<<20, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) activateMigrationGateway(ctx context.Context, connection hub.PlatformConnection, targetRef, receiptID, hubURL string) (hub.CredentialMigrationGatewayReceipt, error) {
	provider := managedGatewayProvider(connection.Provider)
	if provider == "" {
		if strings.EqualFold(connection.Provider, "github") {
			return hub.CredentialMigrationGatewayReceipt{Status: "not_applicable", Build: s.build.Commit}, nil
		}
		return hub.CredentialMigrationGatewayReceipt{Status: "unsupported", Build: s.build.Commit}, errors.New("provider has no managed gateway")
	}
	anchorID, err := s.captureMigrationGatewayAnchor(connection, receiptID)
	receipt := hub.CredentialMigrationGatewayReceipt{Status: "activating", AnchorID: anchorID, Build: s.build.Commit}
	if err != nil {
		receipt.Status = "anchor_failed"
		return receipt, err
	}
	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		receipt.Status = "activation_failed"
		return receipt, err
	}
	var address hub.AgentAddress
	for _, candidate := range addresses {
		if candidate.ConnectionID == connection.ID && candidate.ArchivedAt == "" && candidate.DeletedAt == "" && candidate.Enabled {
			if address.ID != "" {
				receipt.Status = "activation_failed"
				return receipt, errors.New("connection has multiple active addresses")
			}
			address = candidate
		}
	}
	if address.ID == "" {
		receipt.Status = "activation_failed"
		return receipt, errors.New("connection has no active address")
	}
	target := connection
	target.CredentialRef = targetRef
	started := time.Now().UTC()
	switch provider {
	case "feishu":
		gateway, installErr := s.installFeishuGatewayForMigration(target, address, target.AccountRef, hubURL)
		receipt.Manager, receipt.Service = gateway.Manager, gateway.Service
		err = installErr
	case "slack":
		credentials, storeErr := s.hub.CredentialStore()
		appID := ""
		if storeErr == nil {
			appID, _, storeErr = loomslack.LoadTokensAndAppReference(credentials, targetRef, "", target.AccountRef)
		}
		teamID, botUserID := slackAddressIdentity(address.ExternalIdentity)
		if teamID == "" {
			teamID = target.AccountRef
		}
		if storeErr != nil || appID == "" || botUserID == "" {
			err = errors.Join(storeErr, errors.New("Slack gateway identity is incomplete"))
			break
		}
		gateway, installErr := s.installSlackGatewayForMigration(target, address, appID, teamID, botUserID, hubURL)
		receipt.Manager, receipt.Service = gateway.Manager, gateway.Service
		err = installErr
	case "parall":
		agentID := strings.TrimPrefix(strings.TrimSpace(address.ExternalIdentity), "prll://")
		if target.AccountRef == "" || agentID == "" {
			err = errors.New("Parall gateway identity is incomplete")
			break
		}
		gateway, installErr := s.installParallGatewayForMigration(target, address, target.AccountRef, agentID, hubURL)
		receipt.Manager, receipt.Service = gateway.Manager, gateway.Service
		err = installErr
	}
	if err != nil {
		receipt.Status = "activation_failed"
		return receipt, err
	}
	heartbeat, err := s.waitForMigrationGatewayHeartbeat(ctx, connection.ID, started)
	if err != nil {
		receipt.Status = "heartbeat_failed"
		return receipt, err
	}
	receipt.Status = "verified"
	receipt.HeartbeatAt = heartbeat
	return receipt, nil
}

func (s *Server) rollbackMigrationGateway(ctx context.Context, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	provider := managedGatewayProvider(connection.Provider)
	result := hub.CredentialMigrationGatewayReceipt{Status: "restoring", Build: s.build.Commit}
	if provider == "" {
		result.Status = "not_applicable"
		return result, nil
	}
	if receipt.GatewayReceipt == nil || !validMigrationObjectID(receipt.GatewayReceipt.AnchorID, "cmig_") {
		result.Status = "restore_failed"
		return result, errors.New("gateway rollback anchor is unavailable")
	}
	service, err := migrationGatewayServiceFor(provider, connection.ID)
	if err != nil {
		result.Status = "restore_failed"
		return result, err
	}
	anchorDir, err := s.migrationGatewayAnchorDir(receipt.GatewayReceipt.AnchorID, false)
	if err != nil {
		result.Status = "restore_failed"
		return result, err
	}
	unit, err := readBoundedPrivateFile(filepath.Join(anchorDir, "unit"), 1<<20, false)
	if err != nil {
		result.Status = "restore_failed"
		return result, err
	}
	if err := writeSyncedPrivateFile(service.UnitPath, unit, 0o600); err != nil {
		result.Status = "restore_failed"
		return result, err
	}
	started := time.Now().UTC()
	restarted, err := restartManagedConnectorService(provider, connection.ID)
	if err != nil || !restarted {
		result.Status = "restore_failed"
		return result, errors.Join(err, errors.New("previous gateway unit did not restart"))
	}
	heartbeat, err := s.waitForMigrationGatewayHeartbeat(ctx, connection.ID, started)
	if err != nil {
		result.Status = "heartbeat_failed"
		return result, err
	}
	result.Status = "restored"
	result.Manager, result.Service = service.Manager, service.Service
	result.AnchorID = receipt.GatewayReceipt.AnchorID
	result.HeartbeatAt = heartbeat
	return result, nil
}

func (s *Server) captureMigrationGatewayAnchor(connection hub.PlatformConnection, receiptID string) (string, error) {
	provider := managedGatewayProvider(connection.Provider)
	service, err := migrationGatewayServiceFor(provider, connection.ID)
	if err != nil {
		return "", err
	}
	if err := rejectUnanchoredLegacyGatewayUnits(provider, connection.ID, service.UnitPath); err != nil {
		return "", err
	}
	anchorDir, err := s.migrationGatewayAnchorDir(receiptID, true)
	if err != nil {
		return "", err
	}
	if exists, err := validateExistingMigrationGatewayAnchor(anchorDir, provider, service.Manager); err != nil {
		return "", err
	} else if exists {
		return receiptID, nil
	}
	unit, err := readBoundedPrivateFile(service.UnitPath, 1<<20, false)
	if err != nil {
		return "", err
	}
	arguments, err := gatewayUnitArguments(string(unit), service.Manager)
	if err != nil {
		return "", err
	}
	wrapperName := "loom-" + provider + "-gateway"
	if provider == "feishu" {
		wrapperName = "loom-feishu-gateway"
	}
	wrapperPath := findArgumentByBase(arguments, wrapperName)
	if wrapperPath == "" {
		return "", errors.New("gateway wrapper path is missing from the unit")
	}
	anchoredWrapper := filepath.Join(anchorDir, wrapperName)
	if err := copyPrivateFile(wrapperPath, anchoredWrapper, 0o700); err != nil {
		return "", err
	}
	anchoredUnit := replaceUnitArgument(string(unit), wrapperPath, anchoredWrapper, service.Manager)
	if provider == "slack" || provider == "parall" {
		scriptName := provider + ".mjs"
		scriptPath := findArgumentByBase(arguments, scriptName)
		if scriptPath == "" {
			return "", errors.New("gateway adapter path is missing from the unit")
		}
		anchoredScript := filepath.Join(anchorDir, scriptName)
		if err := copyPrivateFile(scriptPath, anchoredScript, 0o600); err != nil {
			return "", err
		}
		protocolName := provider + "-protocol.mjs"
		if err := copyPrivateFile(filepath.Join(filepath.Dir(scriptPath), protocolName), filepath.Join(anchorDir, protocolName), 0o600); err != nil {
			return "", err
		}
		anchoredUnit = replaceUnitArgument(anchoredUnit, scriptPath, anchoredScript, service.Manager)
	}
	if err := writeSyncedPrivateFile(filepath.Join(anchorDir, "unit"), []byte(anchoredUnit), 0o600); err != nil {
		return "", err
	}
	return receiptID, nil
}

func rejectUnanchoredLegacyGatewayUnits(provider, connectionID, currentUnitPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	marker := map[string]string{
		"feishu": "gateway/lark.mjs", "slack": "gateway/slack.mjs", "parall": "gateway/parall.mjs",
	}[provider]
	if marker == "" {
		return errors.New("unsupported gateway rollback provider")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "*.plist"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(currentUnitPath) {
			continue
		}
		payload, readErr := readBoundedPrivateFile(path, 1<<20, false)
		if readErr != nil {
			continue
		}
		text := string(payload)
		if strings.Contains(text, connectionID) && strings.Contains(text, marker) {
			return errors.New("additional legacy gateway unit must be retired or separately anchored before migration")
		}
	}
	return nil
}

func validateExistingMigrationGatewayAnchor(anchorDir, provider, manager string) (bool, error) {
	unitPath := filepath.Join(anchorDir, "unit")
	if _, err := os.Lstat(unitPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	unit, err := readBoundedPrivateFile(unitPath, 1<<20, false)
	if err != nil {
		return false, err
	}
	arguments, err := gatewayUnitArguments(string(unit), manager)
	if err != nil {
		return false, err
	}
	wrapperName := "loom-" + provider + "-gateway"
	if provider == "feishu" {
		wrapperName = "loom-feishu-gateway"
	}
	wrapperPath := findArgumentByBase(arguments, wrapperName)
	if filepath.Dir(wrapperPath) != anchorDir {
		return false, errors.New("gateway rollback anchor does not own its wrapper")
	}
	if _, err := readBoundedPrivateFile(wrapperPath, 128<<20, true); err != nil {
		return false, err
	}
	if provider == "slack" || provider == "parall" {
		scriptPath := findArgumentByBase(arguments, provider+".mjs")
		if filepath.Dir(scriptPath) != anchorDir {
			return false, errors.New("gateway rollback anchor does not own its adapter")
		}
		if _, err := readBoundedPrivateFile(scriptPath, 128<<20, false); err != nil {
			return false, err
		}
		if _, err := readBoundedPrivateFile(filepath.Join(anchorDir, provider+"-protocol.mjs"), 128<<20, false); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Server) migrationGatewayAnchorDir(anchorID string, create bool) (string, error) {
	if !validMigrationObjectID(anchorID, "cmig_") {
		return "", errors.New("invalid gateway rollback anchor")
	}
	if _, err := s.hub.CredentialStore(); err != nil {
		return "", err
	}
	root := filepath.Join(s.st.Dir(), credentialstore.DirectoryName)
	rollbackRoot := filepath.Join(root, "gateway-rollback")
	anchorDir := filepath.Join(rollbackRoot, anchorID)
	if create {
		for _, directory := range []string{rollbackRoot, anchorDir} {
			if err := os.Mkdir(directory, 0o700); err != nil {
				if !os.IsExist(err) {
					return "", err
				}
			} else if err := os.Chmod(directory, 0o700); err != nil {
				return "", err
			}
			if err := validatePrivateMigrationDirectory(directory); err != nil {
				return "", err
			}
		}
	}
	if err := validatePrivateMigrationDirectory(rollbackRoot); err != nil {
		return "", err
	}
	if err := validatePrivateMigrationDirectory(anchorDir); err != nil {
		return "", err
	}
	return anchorDir, nil
}

func migrationGatewayServiceFor(provider, connectionID string) (migrationGatewayService, error) {
	provider, connectionID = managedGatewayProvider(provider), safeServicePart(connectionID)
	if provider == "" || connectionID == "" {
		return migrationGatewayService{}, errors.New("invalid managed gateway identity")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return migrationGatewayService{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		service := "com.codexloom." + provider + "." + connectionID
		return migrationGatewayService{
			Provider: provider, Manager: "launchd", Service: service,
			UnitPath: filepath.Join(home, "Library", "LaunchAgents", service+".plist"),
			Target:   "gui/" + fmt.Sprint(os.Getuid()) + "/" + service,
		}, nil
	case "linux":
		service := "codexloom-" + provider + "-" + connectionID + ".service"
		return migrationGatewayService{
			Provider: provider, Manager: "systemd", Service: service,
			UnitPath: filepath.Join(home, ".config", "systemd", "user", service), Target: service,
		}, nil
	default:
		return migrationGatewayService{}, fmt.Errorf("managed gateway rollback is unsupported on %s", runtime.GOOS)
	}
}

func (s *Server) waitForMigrationGatewayHeartbeat(ctx context.Context, connectionID string, after time.Time) (string, error) {
	timeout := time.NewTimer(migrationGatewayHeartbeatTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, connection := range s.hub.ListConnections() {
			if connection.ID != connectionID || connection.Status != "connected" || connection.LastHeartbeatAt == "" {
				continue
			}
			heartbeat, err := time.Parse(time.RFC3339Nano, connection.LastHeartbeatAt)
			if err == nil && !heartbeat.Before(after) {
				return connection.LastHeartbeatAt, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("gateway heartbeat verification timed out")
		case <-ticker.C:
		}
	}
}

func gatewayUnitArguments(unit, manager string) ([]string, error) {
	result := []string{}
	switch manager {
	case "launchd":
		for _, match := range plistStringPattern.FindAllStringSubmatch(unit, -1) {
			if len(match) == 2 {
				result = append(result, html.UnescapeString(match[1]))
			}
		}
	case "systemd":
		line := ""
		for _, candidate := range strings.Split(unit, "\n") {
			if strings.HasPrefix(strings.TrimSpace(candidate), "ExecStart=") {
				line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "ExecStart="))
				break
			}
		}
		for _, token := range systemdQuotePattern.FindAllString(line, -1) {
			value, err := strconv.Unquote(token)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	default:
		return nil, errors.New("unsupported gateway manager")
	}
	if len(result) == 0 {
		return nil, errors.New("gateway unit has no executable arguments")
	}
	return result, nil
}

func findArgumentByBase(arguments []string, base string) string {
	for _, argument := range arguments {
		if filepath.Base(argument) == base {
			return argument
		}
	}
	return ""
}

func replaceUnitArgument(unit, previous, target, manager string) string {
	switch manager {
	case "launchd":
		return strings.ReplaceAll(unit, html.EscapeString(previous), html.EscapeString(target))
	case "systemd":
		return strings.ReplaceAll(unit, systemdQuote(previous), systemdQuote(target))
	default:
		return unit
	}
}

func slackAddressIdentity(value string) (string, string) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "slack://")
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func validMigrationObjectID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) > 100 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validatePrivateMigrationDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("gateway rollback directory is unsafe")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("gateway rollback directory is not owned by the current user")
	}
	return nil
}

func readBoundedPrivateFile(path string, limit int64, executable bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("gateway rollback source file is unsafe")
	}
	if executable && info.Mode().Perm()&0o100 == 0 {
		return nil, errors.New("gateway rollback executable is not executable")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("gateway rollback source is not owned by the current user")
	}
	if info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("gateway rollback source size is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit+1))
}

func copyPrivateFile(source, target string, mode os.FileMode) error {
	data, err := readBoundedPrivateFile(source, 128<<20, mode&0o100 != 0)
	if err != nil {
		return err
	}
	return writeSyncedPrivateFile(target, data, mode)
}

func writeSyncedPrivateFile(path string, payload []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".credential-migration-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := writeAllPrivate(temporary, payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func writeAllPrivate(file *os.File, payload []byte) error {
	for len(payload) > 0 {
		written, err := file.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return errors.New("private file write made no progress")
		}
		payload = payload[written:]
	}
	return nil
}
