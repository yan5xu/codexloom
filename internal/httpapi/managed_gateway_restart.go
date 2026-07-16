package httpapi

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var restartManagedConnectorService = restartManagedGatewayService
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
