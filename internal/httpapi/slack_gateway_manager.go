package httpapi

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
)

type managedSlackGateway struct {
	Managed  bool   `json:"managed"`
	Manager  string `json:"manager"`
	Service  string `json:"service"`
	LogPath  string `json:"logPath"`
	UnitPath string `json:"unitPath"`
}

var installManagedSlackGateway = func(s *Server, connection hub.PlatformConnection, address hub.AgentAddress, appID, teamID, botUserID, hubURL string) (managedSlackGateway, error) {
	return s.installSlackGateway(connection, address, appID, teamID, botUserID, hubURL)
}

func (s *Server) installSlackGateway(connection hub.PlatformConnection, address hub.AgentAddress, appID, teamID, botUserID, hubURL string) (managedSlackGateway, error) {
	wrapper, err := siblingExecutable("loom-slack-gateway")
	if err != nil {
		return managedSlackGateway{}, err
	}
	node, err := findNodeExecutable()
	if err != nil {
		return managedSlackGateway{}, err
	}
	script, err := findGatewaySource("slack.mjs")
	if err != nil {
		return managedSlackGateway{}, err
	}
	logDir := filepath.Join(s.st.Dir(), "gateway")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return managedSlackGateway{}, err
	}
	logPath := filepath.Join(logDir, "slack-"+connection.ID+".log")
	arguments := []string{
		wrapper, "--hub", hubURL, "--connection", connection.ID, "--address", address.ID,
		"--app-id", appID, "--team-id", teamID, "--bot-user-id", botUserID,
		"--node", node, "--script", script,
	}
	switch runtime.GOOS {
	case "darwin":
		return s.installSlackLaunchAgent(connection.ID, arguments, logPath)
	case "linux":
		return s.installSlackSystemdUnit(connection.ID, arguments, logPath)
	default:
		return managedSlackGateway{}, fmt.Errorf("automatic Slack gateway management is not supported on %s", runtime.GOOS)
	}
}

func (s *Server) installSlackLaunchAgent(connectionID string, arguments []string, logPath string) (managedSlackGateway, error) {
	label := "com.codexloom.slack." + safeServicePart(connectionID)
	home, err := os.UserHomeDir()
	if err != nil {
		return managedSlackGateway{}, err
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedSlackGateway{}, err
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
		return managedSlackGateway{}, err
	}
	uid := fmt.Sprint(os.Getuid())
	target := "gui/" + uid + "/" + label
	_ = exec.Command("launchctl", "bootout", target).Run()
	legacyUnits := stopLegacySlackGateways(connectionID)
	if output, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, unitPath).CombinedOutput(); err != nil {
		restoreLaunchAgents(uid, legacyUnits)
		return managedSlackGateway{}, fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("launchctl", "kickstart", "-k", target).CombinedOutput(); err != nil {
		_ = exec.Command("launchctl", "bootout", target).Run()
		restoreLaunchAgents(uid, legacyUnits)
		return managedSlackGateway{}, fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(output)))
	}
	return managedSlackGateway{Managed: true, Manager: "launchd", Service: label, LogPath: logPath, UnitPath: unitPath}, nil
}

func (s *Server) installSlackSystemdUnit(connectionID string, arguments []string, logPath string) (managedSlackGateway, error) {
	service := "codexloom-slack-" + safeServicePart(connectionID) + ".service"
	home, err := os.UserHomeDir()
	if err != nil {
		return managedSlackGateway{}, err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", service)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedSlackGateway{}, err
	}
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, systemdQuote(argument))
	}
	unit := fmt.Sprintf(`[Unit]
Description=CodexLoom Slack gateway (%s)
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
		return managedSlackGateway{}, err
	}
	if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return managedSlackGateway{}, fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "--user", "enable", "--now", service).CombinedOutput(); err != nil {
		return managedSlackGateway{}, fmt.Errorf("systemctl enable: %s", strings.TrimSpace(string(output)))
	}
	return managedSlackGateway{Managed: true, Manager: "systemd", Service: service, LogPath: logPath, UnitPath: unitPath}, nil
}

func findGatewaySource(name string) (string, error) {
	current, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(filepath.Dir(filepath.Dir(current)), "gateway", name),
		filepath.Join("gateway", name),
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("gateway/%s was not found next to the CodexLoom installation", name)
}

func findNodeExecutable() (string, error) {
	if path, err := exec.LookPath("node"); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	candidates := []string{"/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"}
	versions, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "node"))
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	candidates = append(candidates, versions...)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Node.js is required for the Slack Socket Mode gateway")
}

func stopLegacySlackGateways(connectionID string) []string {
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
		if !strings.Contains(text, connectionID) || !strings.Contains(text, "gateway/slack.mjs") {
			continue
		}
		if err := exec.Command("launchctl", "bootout", "gui/"+fmt.Sprint(os.Getuid()), path).Run(); err == nil {
			stopped = append(stopped, path)
		}
	}
	return stopped
}
