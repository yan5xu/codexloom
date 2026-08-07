//go:build unix

package httpapi

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestPrepareManagedGatewayRestartGeneratesExactTargetProof(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.Open(filepath.Join(home, "data"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: "keychain:restart-proof"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := migrationGatewayServiceFor("feishu", connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(home, "bin", "loom-feishu-gateway")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("test managed gateway executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(service.UnitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	unit := ""
	switch service.Manager {
	case "launchd":
		unit = fmt.Sprintf(`<plist><dict><key>ProgramArguments</key><array><string>%s</string></array></dict></plist>`, html.EscapeString(wrapper))
	case "systemd":
		unit = "[Service]\nExecStart=" + systemdQuote(wrapper) + "\n"
	default:
		t.Fatalf("unexpected manager %s", service.Manager)
	}
	if err := os.WriteFile(service.UnitPath, []byte(unit), 0o600); err != nil {
		t.Fatal(err)
	}
	server := New(h, st, nil)
	server.build.Commit = "build-restart-proof"
	plan, err := server.prepareManagedGatewayRestart(connection)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applicable || plan.UnitPath != service.UnitPath || plan.Previous.Generation != "" ||
		plan.Target.Generation == "" || plan.Target.Build != server.build.Commit || !buildinfo.ValidExecutableSHA256(plan.Target.ExecutableSHA256) {
		t.Fatalf("restart plan = %#v", plan)
	}
	arguments, err := gatewayUnitArguments(string(plan.TargetUnit), service.Manager)
	if err != nil {
		t.Fatal(err)
	}
	if got := findArgumentValue(arguments, "--generation"); got != plan.Target.Generation {
		t.Fatalf("target unit generation = %q, want %q; unit=%s", got, plan.Target.Generation, strings.TrimSpace(string(plan.TargetUnit)))
	}
}
