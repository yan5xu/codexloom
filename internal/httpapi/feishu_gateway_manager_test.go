package httpapi

import (
	"os"
	"path/filepath"
	"testing"
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
