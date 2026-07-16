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

type managedFeishuGateway struct {
	Managed  bool   `json:"managed"`
	Manager  string `json:"manager"`
	Service  string `json:"service"`
	LogPath  string `json:"logPath"`
	UnitPath string `json:"unitPath"`
}

var installNativeFeishuGateway = func(s *Server, connection hub.PlatformConnection, address hub.AgentAddress, appID, hubURL string) (managedFeishuGateway, error) {
	return s.installFeishuGateway(connection, address, appID, hubURL)
}

func (s *Server) installFeishuGateway(connection hub.PlatformConnection, address hub.AgentAddress, appID, hubURL string) (managedFeishuGateway, error) {
	binary, err := siblingExecutable("loom-feishu-gateway")
	if err != nil {
		return managedFeishuGateway{}, err
	}
	logDir := filepath.Join(s.st.Dir(), "gateway")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return managedFeishuGateway{}, err
	}
	logPath := filepath.Join(logDir, "feishu-"+connection.ID+".log")
	arguments := []string{
		binary, "--hub", hubURL, "--connection", connection.ID,
		"--address", address.ID, "--app-id", appID,
	}
	switch runtime.GOOS {
	case "darwin":
		return s.installFeishuLaunchAgent(connection.ID, arguments, logPath)
	case "linux":
		return s.installFeishuSystemdUnit(connection.ID, arguments, logPath)
	default:
		return managedFeishuGateway{}, fmt.Errorf("automatic Feishu gateway management is not supported on %s", runtime.GOOS)
	}
}

func (s *Server) installFeishuLaunchAgent(connectionID string, arguments []string, logPath string) (managedFeishuGateway, error) {
	label := "com.codexloom.feishu." + safeServicePart(connectionID)
	home, err := os.UserHomeDir()
	if err != nil {
		return managedFeishuGateway{}, err
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedFeishuGateway{}, err
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
		return managedFeishuGateway{}, err
	}
	uid := fmt.Sprint(os.Getuid())
	serviceTarget := "gui/" + uid + "/" + label
	_ = exec.Command("launchctl", "bootout", serviceTarget).Run()
	legacyUnits := stopLegacyLarkGateways(connectionID)
	if output, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, unitPath).CombinedOutput(); err != nil {
		restoreLaunchAgents(uid, legacyUnits)
		return managedFeishuGateway{}, fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("launchctl", "kickstart", "-k", serviceTarget).CombinedOutput(); err != nil {
		_ = exec.Command("launchctl", "bootout", serviceTarget).Run()
		restoreLaunchAgents(uid, legacyUnits)
		return managedFeishuGateway{}, fmt.Errorf("launchctl kickstart: %s", strings.TrimSpace(string(output)))
	}
	removeLegacyLaunchAgents(unitPath, legacyUnits)
	return managedFeishuGateway{Managed: true, Manager: "launchd", Service: label, LogPath: logPath, UnitPath: unitPath}, nil
}

func (s *Server) installFeishuSystemdUnit(connectionID string, arguments []string, logPath string) (managedFeishuGateway, error) {
	service := "codexloom-feishu-" + safeServicePart(connectionID) + ".service"
	home, err := os.UserHomeDir()
	if err != nil {
		return managedFeishuGateway{}, err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", service)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return managedFeishuGateway{}, err
	}
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, systemdQuote(argument))
	}
	unit := fmt.Sprintf(`[Unit]
Description=CodexLoom native Feishu gateway (%s)
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
		return managedFeishuGateway{}, err
	}
	if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return managedFeishuGateway{}, fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "--user", "enable", "--now", service).CombinedOutput(); err != nil {
		return managedFeishuGateway{}, fmt.Errorf("systemctl enable: %s", strings.TrimSpace(string(output)))
	}
	return managedFeishuGateway{Managed: true, Manager: "systemd", Service: service, LogPath: logPath, UnitPath: unitPath}, nil
}

func siblingExecutable(name string) (string, error) {
	current, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join(filepath.Dir(current), name)}
	if path, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s is not built next to %s", name, filepath.Base(current))
}

func stopLegacyLarkGateways(connectionID string) []string {
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
		if !strings.Contains(text, connectionID) || !strings.Contains(text, "gateway/lark.mjs") {
			continue
		}
		if err := exec.Command("launchctl", "bootout", "gui/"+fmt.Sprint(os.Getuid()), path).Run(); err == nil {
			stopped = append(stopped, path)
		}
	}
	return stopped
}

func restoreLaunchAgents(uid string, unitPaths []string) {
	for _, path := range unitPaths {
		_ = exec.Command("launchctl", "bootstrap", "gui/"+uid, path).Run()
	}
}

func removeLegacyLaunchAgents(currentUnitPath string, unitPaths []string) {
	currentUnitPath = filepath.Clean(currentUnitPath)
	for _, path := range unitPaths {
		if filepath.Clean(path) != currentUnitPath {
			_ = os.Remove(path)
		}
	}
}

func writePrivateFile(path string, payload []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func safeServicePart(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteByte('-')
		}
	}
	return strings.Trim(result.String(), "-")
}

func systemdQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
