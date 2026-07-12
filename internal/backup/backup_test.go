package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateUsesCodexLoomNameAndLayout(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Create(Options{
		DataDir:          dataDir,
		CodexSessionsDir: filepath.Join(t.TempDir(), "sessions"),
		Reason:           "rename-test",
		Agents:           []AgentRef{{ID: "agent-1", Name: "alpha", ThreadID: "thread-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isSnapshotName(snapshot.Name) || snapshot.Name[:11] != "codex-loom-" {
		t.Fatalf("unexpected snapshot name %q", snapshot.Name)
	}

	file, err := os.Open(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "codex-loom/agents.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot did not contain codex-loom/agents.json")
	}
}

func TestListIncludesLegacySnapshots(t *testing.T) {
	dataDir := t.TempDir()
	dir := DefaultDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"codex-hub-20260101T000000Z-manual.tar.gz",
		"codex-loom-20260102T000000Z-manual.tar.gz",
		"unrelated.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	items, err := List(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("List() returned %d snapshots, want 2", len(items))
	}
}
