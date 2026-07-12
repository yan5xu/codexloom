package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMigratesLegacyCodexHubDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_LOOM_DATA", "")
	t.Setenv("CODEX_HUB_DATA", "")
	legacy := filepath.Join(home, ".codex-hub")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "sessions.json"), []byte(`{"agent":{"id":"agent"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Open(DefaultDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Dir() != filepath.Join(home, ".codex-loom") {
		t.Fatalf("store dir = %q", st.Dir())
	}
	info, err := os.Lstat(legacy)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy path is not a compatibility symlink: info=%v err=%v", info, err)
	}
	var agents map[string]map[string]any
	if err := st.LoadAgents(&agents); err != nil {
		t.Fatal(err)
	}
	if agents["agent"]["id"] != "agent" {
		t.Fatalf("agents = %#v", agents)
	}
	if err := st.SaveAgents(agents); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agents.json", "sessions.json"} {
		if _, err := os.Stat(filepath.Join(st.Dir(), name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
}

func TestDefaultDirPrefersCodexLoomEnv(t *testing.T) {
	t.Setenv("CODEX_HUB_DATA", "/legacy")
	t.Setenv("CODEX_LOOM_DATA", "/loom")
	if got := DefaultDir(); got != "/loom" {
		t.Fatalf("DefaultDir = %q", got)
	}
}
