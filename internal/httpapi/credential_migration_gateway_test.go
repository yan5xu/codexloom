//go:build unix

package httpapi

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestMigrationGatewayAnchorIsPrivateAndIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}})
	connection := hub.PlatformConnection{ID: "conn_anchor", Provider: "slack"}
	service, err := migrationGatewayServiceFor(connection.Provider, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	wrapper := filepath.Join(sourceDir, "loom-slack-gateway")
	script := filepath.Join(sourceDir, "slack.mjs")
	protocol := filepath.Join(sourceDir, "slack-protocol.mjs")
	if err := os.WriteFile(wrapper, []byte("gateway-wrapper-fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	observed, err := buildinfo.ObserveExecutable(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	connection.GatewayBuild = "build-anchor"
	connection.GatewayExecutableSHA256 = observed.SHA256
	if err := os.WriteFile(script, []byte("gateway-adapter-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protocol, []byte("gateway-protocol-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(service.UnitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	unit := migrationGatewayUnitFixture(service.Manager, wrapper, script)
	if err := os.WriteFile(service.UnitPath, []byte(unit), 0o600); err != nil {
		t.Fatal(err)
	}
	receiptID := "cmig_anchor_fixture"
	anchorID, err := server.captureMigrationGatewayAnchor(connection, receiptID)
	if err != nil || anchorID != receiptID {
		t.Fatalf("capture anchor id = %q, err = %v", anchorID, err)
	}
	anchorDir := filepath.Join(st.Dir(), credentialstore.DirectoryName, "gateway-rollback", receiptID)
	for _, name := range []string{"unit", "evidence.json", "loom-slack-gateway", "slack.mjs", "slack-protocol.mjs"} {
		info, err := os.Lstat(filepath.Join(anchorDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("anchor file %s is unsafe: mode=%v err=%v", name, infoMode(info), err)
		}
	}
	anchoredUnit, err := os.ReadFile(filepath.Join(anchorDir, "unit"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(anchoredUnit), filepath.Join(anchorDir, "loom-slack-gateway")) || !strings.Contains(string(anchoredUnit), filepath.Join(anchorDir, "slack.mjs")) || strings.Contains(string(anchoredUnit), sourceDir) {
		t.Fatal("rollback unit does not point exclusively at private anchored files")
	}
	if err := os.Remove(service.UnitPath); err != nil {
		t.Fatal(err)
	}
	anchorID, err = server.captureMigrationGatewayAnchor(connection, receiptID)
	if err != nil || anchorID != receiptID {
		t.Fatalf("idempotent anchor reuse id = %q, err = %v", anchorID, err)
	}
	anchorEvidence, verified, err := readMigrationGatewayAnchorEvidence(anchorDir, filepath.Join(anchorDir, "loom-slack-gateway"))
	if err != nil || !verified || anchorEvidence.Build != connection.GatewayBuild || anchorEvidence.ExecutableSHA256 != observed.SHA256 {
		t.Fatalf("anchor executable evidence = %#v, verified=%v, err=%v", anchorEvidence, verified, err)
	}
	if err := os.WriteFile(filepath.Join(anchorDir, "loom-slack-gateway"), []byte("tampered-wrapper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readMigrationGatewayAnchorEvidence(anchorDir, filepath.Join(anchorDir, "loom-slack-gateway")); err == nil {
		t.Fatal("rollback anchor digest drift was accepted")
	}
}

func TestMigrationGatewayAnchorRejectsExistingSymlinkDirectory(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}})
	if _, err := h.CredentialStore(); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	rollbackRoot := filepath.Join(st.Dir(), credentialstore.DirectoryName, "gateway-rollback")
	if err := os.Symlink(target, rollbackRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := server.migrationGatewayAnchorDir("cmig_symlink_fixture", true); err == nil {
		t.Fatal("symlinked rollback root was accepted")
	}
}

func TestMigrationGatewayRejectsUnanchoredLegacyLaunchAgent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd-only migration fence")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(directory, "com.codexloom.slack.conn_fence.plist")
	extra := filepath.Join(directory, "legacy-slack.plist")
	legacyUnit := `<plist><string>conn_fence</string><string>/old/gateway/slack.mjs</string></plist>`
	if err := os.WriteFile(extra, []byte(legacyUnit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rejectUnanchoredLegacyGatewayUnits("slack", "conn_fence", current); err == nil {
		t.Fatal("unanchored legacy launch agent was accepted")
	}
}

func migrationGatewayUnitFixture(manager, wrapper, script string) string {
	switch manager {
	case "launchd":
		return fmt.Sprintf(`<plist><dict><key>ProgramArguments</key><array><string>%s</string><string>--script</string><string>%s</string></array></dict></plist>`, html.EscapeString(wrapper), html.EscapeString(script))
	case "systemd":
		return "[Service]\nExecStart=" + systemdQuote(wrapper) + " \"--script\" " + systemdQuote(script) + "\n"
	default:
		return ""
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}
