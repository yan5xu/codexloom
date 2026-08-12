package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func TestRemoveLegacyLaunchAgentsPreservesCurrentUnit(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "com.codexloom.feishu.current.plist")
	legacy := filepath.Join(dir, "com.pinix.codex-hub-lark-external.plist")
	for _, path := range []string{current, legacy} {
		if err := os.WriteFile(path, []byte("plist"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removeLegacyLaunchAgents(current, []string{current, legacy})

	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current unit was removed: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy unit still exists: %v", err)
	}
}

func TestFeishuGatewayArgumentsAddOnlyExplicitGlobalDomain(t *testing.T) {
	connection := hub.PlatformConnection{ID: "conn-1"}
	address := hub.AgentAddress{ID: "addr-1"}
	legacy, err := feishuGatewayArguments(connection, address, "cli-test", "http://127.0.0.1:4870", "/opt/loom-feishu-gateway")
	if err != nil {
		t.Fatal(err)
	}
	wantLegacy := []string{
		"/opt/loom-feishu-gateway", "--hub", "http://127.0.0.1:4870", "--connection", "conn-1",
		"--address", "addr-1", "--app-id", "cli-test",
	}
	if !reflect.DeepEqual(legacy, wantLegacy) {
		t.Fatalf("legacy arguments = %v", legacy)
	}

	connection.Domain = "lark"
	global, err := feishuGatewayArguments(connection, address, "cli-test", "http://127.0.0.1:4870", "/opt/loom-feishu-gateway")
	if err != nil {
		t.Fatal(err)
	}
	wantGlobal := append(append([]string(nil), wantLegacy...), "--domain", "lark")
	if !reflect.DeepEqual(global, wantGlobal) {
		t.Fatalf("global arguments = %v", global)
	}
}
