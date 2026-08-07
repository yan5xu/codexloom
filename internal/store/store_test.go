package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyOpenDoesNotCreateOrMutateStore(t *testing.T) {
	dir := t.TempDir()
	agents := []byte(`{"agent":{"id":"agent"}}`)
	if err := os.WriteFile(filepath.Join(dir, "agents.json"), agents, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenWithOptions(dir, OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.ReadOnly() {
		t.Fatal("read-only Store did not retain its mode")
	}
	if _, err := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(err) {
		t.Fatalf("read-only open created events directory: %v", err)
	}
	if err := st.SaveAgents(map[string]any{}); err == nil {
		t.Fatal("read-only Store accepted a durable write")
	}
	after, err := os.ReadFile(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, agents) {
		t.Fatal("read-only Store changed agents.json")
	}
}

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

func TestReadNDJSONRejectsMalformedRecord(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "comms.ndjson")
	if err := os.WriteFile(path, []byte("{\"message\":{\"id\":\"one\"}}\nnot-json\n{\"message\":{\"id\":\"two\"}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var records []json.RawMessage
	err = st.ReadComms(func(raw json.RawMessage) { records = append(records, raw) })
	if err == nil {
		t.Fatal("malformed NDJSON was accepted")
	}
	if len(records) != 1 {
		t.Fatalf("records before corruption = %d, want 1", len(records))
	}
}

func TestReadNDJSONRejectsCompleteMalformedTail(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "comms.ndjson")
	if err := os.WriteFile(path, []byte("{\"message\":{\"id\":\"one\"}}\nnot-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = st.ReadComms(func(json.RawMessage) {})
	if err == nil {
		t.Fatal("complete malformed tail was accepted")
	}
}

func TestReadNDJSONIgnoresTornTrailingRecord(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "comms.ndjson")
	if err := os.WriteFile(path, []byte("{\"message\":{\"id\":\"one\"}}\n{\"message\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	var records []json.RawMessage
	if err := st.ReadComms(func(raw json.RawMessage) { records = append(records, raw) }); err != nil {
		t.Fatalf("torn trailing record: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1 complete record", len(records))
	}
}

func TestSaveAgentsUsesPrivateAtomicFiles(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]any{"agent": map[string]any{"id": "agent"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agents.json", "sessions.json"} {
		info, err := os.Stat(filepath.Join(st.Dir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	matches, err := filepath.Glob(filepath.Join(st.Dir(), ".*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic save left temporary files: %v", matches)
	}
}

func TestDefaultDirPrefersCodexLoomEnv(t *testing.T) {
	t.Setenv("CODEX_HUB_DATA", "/legacy")
	t.Setenv("CODEX_LOOM_DATA", "/loom")
	if got := DefaultDir(); got != "/loom" {
		t.Fatalf("DefaultDir = %q", got)
	}
}
