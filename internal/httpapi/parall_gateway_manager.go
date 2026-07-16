package httpapi

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
)

type managedParallGateway struct {
	Managed  bool   `json:"managed"`
	Manager  string `json:"manager"`
	Service  string `json:"service"`
	LogPath  string `json:"logPath"`
	UnitPath string `json:"unitPath"`
}

var installManagedParallGateway = func(s *Server, connection hub.PlatformConnection, address hub.AgentAddress, orgID, agentID, hubURL string) (managedParallGateway, error) {
	return s.installParallGateway(connection, address, orgID, agentID, hubURL)
}

var retireManagedParallGateways = func(s *Server, connectionIDs []string) error {
	return s.retireParallGateways(connectionIDs)
}

func (s *Server) installParallGateway(connection hub.PlatformConnection, address hub.AgentAddress, orgID, agentID, hubURL string) (managedParallGateway, error) {
	wrapper, err := siblingExecutable("loom-parall-gateway")
	if err != nil {
		return managedParallGateway{}, err
	}
	node, err := findNodeExecutable()
	if err != nil {
		return managedParallGateway{}, err
	}
	script, err := findGatewaySource("parall.mjs")
	if err != nil {
		return managedParallGateway{}, err
	}
	logDir := filepath.Join(s.st.Dir(), "gateway")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return managedParallGateway{}, err
	}
	logPath := filepath.Join(logDir, "parall-"+connection.ID+".log")
	arguments := []string{
		wrapper, "--hub", hubURL, "--connection", connection.ID, "--address", address.ID,
		"--org-id", orgID, "--agent-id", agentID, "--node", node, "--script", script,
	}
	switch runtime.GOOS {
	case "darwin":
		return s.installParallLaunchAgent(connection.ID, arguments, logPath)
	case "linux":
		return s.installParallSystemdUnit(connection.ID, arguments, logPath)
	default:
		return managedParallGateway{}, fmt.Errorf("automatic Parall gateway management is not supported on %s", runtime.GOOS)
	}
}

func (s *Server) retireParallGateways(connectionIDs []string) error {
	connectionIDs = orderedUnique(connectionIDs)
	if len(connectionIDs) == 0 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		uid := fmt.Sprint(os.Getuid())
		paths, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "*.plist"))
		for _, connectionID := range connectionIDs {
			label := "com.codexloom.parall." + safeServicePart(connectionID)
			_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
			managedPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
			if err := removeIfPresent(managedPath); err != nil {
				return err
			}
			for _, unitPath := range paths {
				payload, readErr := os.ReadFile(unitPath)
				if readErr != nil || !strings.Contains(string(payload), connectionID) || !strings.Contains(string(payload), "parall.mjs") {
					continue
				}
				_ = exec.Command("launchctl", "bootout", "gui/"+uid, unitPath).Run()
				if err := removeIfPresent(unitPath); err != nil {
					return err
				}
			}
		}
	case "linux":
		for _, connectionID := range connectionIDs {
			service := "codexloom-parall-" + safeServicePart(connectionID) + ".service"
			_ = exec.Command("systemctl", "--user", "disable", "--now", service).Run()
			if err := removeIfPresent(filepath.Join(home, ".config", "systemd", "user", service)); err != nil {
				return err
			}
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	default:
		return fmt.Errorf("automatic Parall gateway retirement is not supported on %s", runtime.GOOS)
	}
	return nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Server) installParallLaunchAgent(connectionID string, arguments []string, logPath string) (managedParallGateway, error) {
	label := "com.codexloom.parall." + safeServicePart(connectionID)
	home, err := os.UserHomeDir()
	if err != nil {
		return managedParallGateway{}, err
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedParallGateway{}, err
	}
	var argsXML strings.Builder
	for _, argument := range arguments {
		argsXML.WriteString("      <string>" + html.EscapeString(argument) + "</string>\n")
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array>
%s    </array>
    <key>EnvironmentVariables</key>
    <dict><key>CODEX_LOOM_DATA</key><string>%s</string></dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>ProcessType</key><string>Background</string>
    <key>StandardOutPath</key><string>%s</string>
    <key>StandardErrorPath</key><string>%s</string>
  </dict>
</plist>
`, html.EscapeString(label), argsXML.String(), html.EscapeString(s.st.Dir()), html.EscapeString(logPath), html.EscapeString(logPath))
	if err := writePrivateFile(unitPath, []byte(plist)); err != nil {
		return managedParallGateway{}, err
	}
	uid := fmt.Sprint(os.Getuid())
	target := "gui/" + uid + "/" + label
	_ = exec.Command("launchctl", "bootout", target).Run()
	legacyUnits := stopLegacyParallGateways(connectionID)
	if output, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, unitPath).CombinedOutput(); err != nil {
		restoreLaunchAgents(uid, legacyUnits)
		_ = os.Remove(unitPath)
		return managedParallGateway{}, fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("launchctl", "kickstart", "-k", target).CombinedOutput(); err != nil {
		_ = exec.Command("launchctl", "bootout", target).Run()
		restoreLaunchAgents(uid, legacyUnits)
		_ = os.Remove(unitPath)
		return managedParallGateway{}, fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(output)))
	}
	for _, path := range legacyUnits {
		if filepath.Clean(path) != filepath.Clean(unitPath) {
			_ = os.Remove(path)
		}
	}
	return managedParallGateway{Managed: true, Manager: "launchd", Service: label, LogPath: logPath, UnitPath: unitPath}, nil
}

func (s *Server) installParallSystemdUnit(connectionID string, arguments []string, logPath string) (managedParallGateway, error) {
	service := "codexloom-parall-" + safeServicePart(connectionID) + ".service"
	home, err := os.UserHomeDir()
	if err != nil {
		return managedParallGateway{}, err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", service)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedParallGateway{}, err
	}
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, systemdQuote(argument))
	}
	unit := fmt.Sprintf(`[Unit]
Description=CodexLoom Parall gateway (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=%s
Environment=CODEX_LOOM_DATA=%s
Restart=always
RestartSec=2
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, connectionID, strings.Join(quoted, " "), systemdQuote(s.st.Dir()), logPath, logPath)
	if err := writePrivateFile(unitPath, []byte(unit)); err != nil {
		return managedParallGateway{}, err
	}
	if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		_ = os.Remove(unitPath)
		return managedParallGateway{}, fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "--user", "enable", "--now", service).CombinedOutput(); err != nil {
		_ = exec.Command("systemctl", "--user", "disable", "--now", service).Run()
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return managedParallGateway{}, fmt.Errorf("systemctl enable: %s", strings.TrimSpace(string(output)))
	}
	return managedParallGateway{Managed: true, Manager: "systemd", Service: service, LogPath: logPath, UnitPath: unitPath}, nil
}

func stopLegacyParallGateways(connectionID string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "*.plist"))
	stopped := make([]string, 0)
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(payload)
		if !strings.Contains(text, connectionID) || !strings.Contains(text, "gateway/parall.mjs") {
			continue
		}
		if err := exec.Command("launchctl", "bootout", "gui/"+fmt.Sprint(os.Getuid()), path).Run(); err == nil {
			stopped = append(stopped, path)
		}
	}
	return stopped
}
