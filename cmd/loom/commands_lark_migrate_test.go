package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestLarkMigrateCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := st.Dir()
	h, err := hub.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{
		Provider: "lark", AccountRef: "cli_test_app", CredentialRef: "keychain:com.codexloom.lark",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := h.CreateAgent(hub.CreateParams{Name: "lark-test-agent", Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{
		Agent: agent.Name, ConnectionID: connection.ID, ExternalIdentity: "lark://cli_test_app",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "lark:cli_test_app",
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	_ = st.Close()

	executable := filepath.Join(t.TempDir(), "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	larkGatewayBinaryPath = func() (string, error) { return executable, nil }
	if err := writeLarkAnchorUnit(t, home, canonicalDir, connection.ID, address.ID, "cli_test_app", executable); err != nil {
		t.Fatal(err)
	}

	secretPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("cli-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmdLarkMigrate(args{
		positional: []string{"dry-run"},
		flags:      map[string]string{"data": dir, "connection": connection.ID},
	})
	cmdLarkMigrate(args{
		positional: []string{"migrate"},
		flags:      map[string]string{"data": dir, "connection": connection.ID, "source": secretPath},
	})

	migratedRef := openConnectionRef(t, dir, connection.ID)
	if !strings.HasPrefix(migratedRef, "managed:") {
		t.Fatalf("connection not migrated: %q", migratedRef)
	}
	if planRef := openConnectionLaunchPlanRef(t, dir, connection.ID); !strings.HasPrefix(planRef, "managed:") {
		t.Fatalf("migrate did not freeze a typed launch plan: %q", planRef)
	}
	// A completed/plan_pending re-entry must not require the original source.
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	cmdLarkMigrate(args{
		positional: []string{"migrate"},
		flags:      map[string]string{"data": dir, "connection": connection.ID},
	})
	cmdLarkMigrate(args{
		positional: []string{"rollback"},
		flags:      map[string]string{"data": dir, "connection": connection.ID},
	})
	restoredRef := openConnectionRef(t, dir, connection.ID)
	if restoredRef != "keychain:com.codexloom.lark" {
		t.Fatalf("rollback did not restore the previous reference: %q", restoredRef)
	}
	if planRef := openConnectionLaunchPlanRef(t, dir, connection.ID); planRef != "" {
		t.Fatalf("rollback did not revoke the typed launch plan: %q", planRef)
	}
}

// writeLarkAnchorUnit fabricates the currently installed legacy Feishu unit
// under a temporary HOME so the real platform adapter's anchor validation
// matches the frozen maintenance plan.
func writeLarkAnchorUnit(t *testing.T, home, dataDir, connectionID, addressID, appID, executable string) error {
	t.Helper()
	logPath := filepath.Join(dataDir, "gateway", "feishu-"+connectionID+".log")
	hubURL := "http://127.0.0.1:4870"
	switch runtime.GOOS {
	case "darwin":
		label := "com.codexloom.feishu." + connectionID
		unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return err
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>` + label + `</string>
    <key>ProgramArguments</key>
    <array>
      <string>` + executable + `</string>
      <string>--hub</string>
      <string>` + hubURL + `</string>
      <string>--connection</string>
      <string>` + connectionID + `</string>
      <string>--address</string>
      <string>` + addressID + `</string>
      <string>--app-id</string>
      <string>` + appID + `</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict><key>CODEX_LOOM_DATA</key><string>` + dataDir + `</string></dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>ProcessType</key><string>Background</string>
    <key>StandardOutPath</key><string>` + logPath + `</string>
    <key>StandardErrorPath</key><string>` + logPath + `</string>
  </dict>
</plist>
`
		return os.WriteFile(unitPath, []byte(plist), 0o600)
	case "linux":
		service := "codexloom-feishu-" + connectionID + ".service"
		unitPath := filepath.Join(home, ".config", "systemd", "user", service)
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return err
		}
		unit := `[Unit]
Description=CodexLoom native Feishu gateway (` + connectionID + `)
After=network-online.target

[Service]
Type=simple
ExecStart=` + executable + ` --hub ` + hubURL + ` --connection ` + connectionID + ` --address ` + addressID + ` --app-id ` + appID + `
Environment=CODEX_LOOM_DATA=` + dataDir + `
Restart=always
RestartSec=2
StandardOutput=append:` + logPath + `
StandardError=append:` + logPath + `

[Install]
WantedBy=default.target
`
		return os.WriteFile(unitPath, []byte(unit), 0o600)
	default:
		t.Skipf("typed Lark launch anchor rehearsal is unsupported on %s", runtime.GOOS)
		return nil
	}
}

func openConnectionRef(t *testing.T, dir, connectionID string) string {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.Open(st)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	defer func() {
		h.Shutdown()
		_ = st.Close()
	}()
	for _, candidate := range h.ListConnections() {
		if candidate.ID == connectionID {
			return candidate.CredentialRef
		}
	}
	t.Fatalf("connection %s not found", connectionID)
	return ""
}

func openConnectionLaunchPlanRef(t *testing.T, dir, connectionID string) string {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.Open(st)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	defer func() {
		h.Shutdown()
		_ = st.Close()
	}()
	return h.LarkGatewayLaunchPlanRef(connectionID)
}
