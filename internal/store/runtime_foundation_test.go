package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriterLeaseRejectsSecondStoreBeforeDataWriteAndReleases(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "already has a writable CodexLoom process") {
		t.Fatalf("second writer error = %v", err)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected writer changed data files: before=%d after=%d", len(before), len(after))
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Open(dir)
	if err != nil {
		t.Fatalf("writer lease was not released: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriterLeaseRejectsSecondProcessAndReleasesForNextProcess(t *testing.T) {
	dir := t.TempDir()
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	runWriterLeaseHelper(t, dir, "blocked")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	runWriterLeaseHelper(t, dir, "open")
}

func TestWriterLeaseProcessHelper(t *testing.T) {
	mode := os.Getenv("CODEX_LOOM_TEST_WRITER_LEASE_MODE")
	if mode == "" {
		return
	}
	dir := os.Getenv("CODEX_LOOM_TEST_WRITER_LEASE_DIR")
	st, err := Open(dir)
	switch mode {
	case "blocked":
		if err == nil {
			_ = st.Close()
			t.Fatal("second process acquired writer lease")
		}
		if !strings.Contains(err.Error(), "already has a writable CodexLoom process") {
			t.Fatalf("second process error = %v", err)
		}
	case "open":
		if err != nil {
			t.Fatalf("released writer lease remained held: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown writer lease helper mode %q", mode)
	}
}

func runWriterLeaseHelper(t *testing.T, dir, mode string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWriterLeaseProcessHelper$")
	command.Env = append(os.Environ(),
		"CODEX_LOOM_TEST_WRITER_LEASE_MODE="+mode,
		"CODEX_LOOM_TEST_WRITER_LEASE_DIR="+dir,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("writer lease helper %s: %v\n%s", mode, err, output)
	}
}

func TestWriterLeaseReleasesWhenOpenFailsAfterAcquisition(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "data")
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("opening a non-directory store succeeded")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("failed open leaked writer lease: %v", err)
	}
	_ = st.Close()
}

func TestWriterLeaseSupportsNestedMissingDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "nested", "data")
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events")); err != nil {
		t.Fatalf("nested store was not created: %v", err)
	}
	_ = st.Close()
}

func TestWriterLeaseCanonicalizesSymlinkAliases(t *testing.T) {
	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlink aliases are unavailable: %v", err)
	}
	st, err := Open(realDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := Open(alias); err == nil || !strings.Contains(err.Error(), "already has a writable CodexLoom process") {
		t.Fatalf("symlink alias writer error = %v", err)
	}
}

func TestWriterLeaseCloseWaitsForInFlightWriteAndClosedStoreRejectsWrites(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	doneWrite, err := st.beginWrite()
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- st.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("writer lease closed during an in-flight write: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	doneWrite()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTopics(map[string]any{"after": "close"}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed store write error = %v", err)
	}
	next, err := Open(dir)
	if err != nil {
		t.Fatalf("writer lease not released after in-flight write: %v", err)
	}
	_ = next.Close()
}

func TestReadOnlyStoreDoesNotCompeteForWriterOrCreateFiles(t *testing.T) {
	dir := t.TempDir()
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before := snapshotStoreTree(t, dir)
	reader, err := OpenWithOptions(dir, OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reader.ReadOnly() {
		t.Fatal("read-only store is writable")
	}
	if err := reader.SaveTopics(map[string]any{"unexpected": true}); err == nil {
		t.Fatal("read-only store accepted a write")
	}
	_ = reader.Close()
	after := snapshotStoreTree(t, dir)
	assertStoreTreeEqual(t, before, after)
}

func TestRuntimeFoundationIsAbsentOnLegacyOpenAndRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var envelope RuntimeFoundationEnvelope
	exists, err := st.LoadRuntimeFoundation(&envelope)
	if err != nil || exists {
		t.Fatalf("legacy foundation = exists %v err %v", exists, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime-foundation.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy open created runtime foundation: %v", err)
	}
	_ = st.Close()

	newer := `{"schemaVersion":2,"minimumWriter":2,"gatewayLifecycle":{}}`
	if err := os.WriteFile(filepath.Join(dir, "runtime-foundation.json"), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "newer runtime foundation") {
		t.Fatalf("newer schema error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "runtime-foundation.json"))
	if err != nil || string(data) != newer {
		t.Fatalf("failed open rewrote newer foundation: %q %v", data, err)
	}
}

func TestRuntimeFoundationRejectsInvalidAndTrailingCandidateStateWithoutRewrite(t *testing.T) {
	for name, content := range map[string]string{
		"old candidate schema": `{"schemaVersion":0,"minimumWriter":0,"gatewayLifecycle":{}}`,
		"missing lifecycle":    `{"schemaVersion":1,"minimumWriter":1,"gatewayLifecycle":null}`,
		"trailing JSON":        `{"schemaVersion":1,"minimumWriter":1,"gatewayLifecycle":{}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "runtime-foundation.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotStoreTree(t, dir)
			if _, err := Open(dir); err == nil {
				t.Fatal("invalid runtime foundation opened as writable")
			}
			after := snapshotStoreTree(t, dir)
			assertStoreTreeEqual(t, before, after)
		})
	}
}

func snapshotStoreTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative+"/"] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertStoreTreeEqual(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("store fileset changed: before=%v after=%v", before, after)
	}
	for path, value := range before {
		if after[path] != value {
			t.Fatalf("store bytes changed for %s", path)
		}
	}
}
