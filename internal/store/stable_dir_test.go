package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestS0RejectsFoundationBeforeAnyInDirectoryMutation(t *testing.T) {
	cases := map[string]string{
		"malformed":   `{`,
		"newer":       `{"schemaVersion":2,"minimumWriter":1,"state":{"version":1}}`,
		"higherFloor": `{"schemaVersion":1,"minimumWriter":2,"state":{"version":1}}`,
		"badState":    `{"schemaVersion":1,"minimumWriter":1,"state":{"version":2}}`,
		"unknown":     `{"schemaVersion":1,"minimumWriter":1,"state":{"version":1},"extra":true}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, foundationFileName)
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotDirectory(t, dir)
			if st, err := Open(dir); err == nil {
				_ = st.Close()
				t.Fatal("invalid foundation was accepted")
			}
			after := snapshotDirectory(t, dir)
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("failed open mutated directory: before=%v after=%v", before, after)
			}
		})
	}
}

func TestS0LegacyOpenDoesNotCreateFoundationOrRaiseFloor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agents.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := os.Stat(filepath.Join(dir, foundationFileName)); !os.IsNotExist(err) {
		t.Fatalf("S0 created a foundation/floor record: %v", err)
	}
}

func TestS0LegacyMigrationValidatesSourceBeforeRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_LOOM_DATA", "")
	t.Setenv("CODEX_HUB_DATA", "")
	legacy := filepath.Join(home, ".codex-hub")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, foundationFileName), []byte(`{"schemaVersion":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotDirectory(t, legacy)
	if st, err := Open(DefaultDir()); err == nil {
		_ = st.Close()
		t.Fatal("invalid legacy foundation was migrated")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex-loom")); !os.IsNotExist(err) {
		t.Fatalf("invalid legacy directory was renamed: %v", err)
	}
	after := snapshotDirectory(t, legacy)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("invalid legacy source mutated: %v -> %v", before, after)
	}
}

func TestS0SingleWriterCoversAliasesAndReleases(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(dir), "data-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if second, err := Open(alias); err == nil {
		_ = second.Close()
		t.Fatal("second writer acquired the same filesystem identity")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(alias)
	if err != nil {
		t.Fatalf("writer lease was not released: %v", err)
	}
	_ = reopened.Close()
}

func TestS0FailedWriterClaimReleasesOwnershipForRetry(t *testing.T) {
	parent := t.TempDir()
	firstDir := filepath.Join(parent, "first")
	secondDir := filepath.Join(parent, "second")
	if err := os.Mkdir(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "current")
	if err := os.Symlink(firstDir, alias); err != nil {
		t.Fatal(err)
	}
	secondBefore := snapshotDirectory(t, secondDir)
	opened, err := openStableDataDirWithClaimHook(alias, false, func() {
		if removeErr := os.Remove(alias); removeErr != nil {
			t.Fatal(removeErr)
		}
		if linkErr := os.Symlink(secondDir, alias); linkErr != nil {
			t.Fatal(linkErr)
		}
	})
	if err == nil {
		_ = opened.close()
		t.Fatal("writer claim accepted a retargeted bootstrap path")
	}
	if after := snapshotDirectory(t, secondDir); fmt.Sprint(after) != fmt.Sprint(secondBefore) {
		t.Fatalf("failed claim mutated retarget directory: before=%v after=%v", secondBefore, after)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(firstDir, alias); err != nil {
		t.Fatal(err)
	}
	retried, err := Open(alias)
	if err != nil {
		t.Fatalf("restored path could not retry after failed claim: %v", err)
	}
	defer retried.Close()
	if duplicate, err := Open(firstDir); err == nil {
		_ = duplicate.Close()
		t.Fatal("retry admitted a second writer")
	}
}

func TestS0WriterLeaseIsProcessWide(t *testing.T) {
	if mode := os.Getenv("CODEX_LOOM_S0_LEASE_HELPER"); mode != "" {
		st, err := Open(os.Getenv("CODEX_LOOM_S0_LEASE_DIR"))
		if mode == "locked" && err == nil {
			_ = st.Close()
			os.Exit(9)
		}
		if mode == "abrupt" && err != nil {
			os.Exit(8)
		}
		os.Exit(0) // abrupt mode intentionally skips Close; the OS must release.
	}
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestS0WriterLeaseIsProcessWide$")
	cmd.Env = append(os.Environ(), "CODEX_LOOM_S0_LEASE_HELPER=locked", "CODEX_LOOM_S0_LEASE_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child acquired locked data directory: %v: %s", err, output)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0], "-test.run", "^TestS0WriterLeaseIsProcessWide$")
	cmd.Env = append(os.Environ(), "CODEX_LOOM_S0_LEASE_HELPER=abrupt", "CODEX_LOOM_S0_LEASE_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("abrupt helper: %v: %s", err, output)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("OS did not release abrupt child lease: %v", err)
	}
	_ = reopened.Close()
}

func TestS0SymlinkRetargetAndDirectoryReplacementFailClosed(t *testing.T) {
	t.Run("symlink-retarget", func(t *testing.T) {
		parent := t.TempDir()
		firstDir := filepath.Join(parent, "first")
		secondDir := filepath.Join(parent, "second")
		if err := os.Mkdir(firstDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(secondDir, 0o755); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(parent, "current")
		if err := os.Symlink(firstDir, alias); err != nil {
			t.Fatal(err)
		}
		st, err := Open(alias)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secondDir, alias); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveTopics(map[string]any{"wrong": true}); err == nil {
			t.Fatal("write followed retargeted bootstrap symlink")
		}
		if _, err := os.Stat(filepath.Join(secondDir, "topics.json")); !os.IsNotExist(err) {
			t.Fatalf("retarget received a write: %v", err)
		}
	})
	t.Run("rename-replacement", func(t *testing.T) {
		parent := t.TempDir()
		dir := filepath.Join(parent, "data")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		moved := filepath.Join(parent, "moved")
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveTopics(map[string]any{"wrong": true}); err == nil {
			t.Fatal("write continued after directory identity replacement")
		}
		if _, err := os.Stat(filepath.Join(dir, "topics.json")); !os.IsNotExist(err) {
			t.Fatalf("replacement received a write: %v", err)
		}
	})
}

func TestS0DarwinCaseAliasUsesFilesystemIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin case alias")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "CaseData")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "cASEdATA")
	a, errA := os.Stat(dir)
	b, errB := os.Stat(alias)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Skip("case-sensitive Darwin volume")
	}
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Open(alias); err == nil {
		_ = second.Close()
		t.Fatal("case alias acquired a second writer")
	}
}

func snapshotDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = fmt.Sprintf("%x:%d", sum, len(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestS0FoundationRejectsTrailingJSON(t *testing.T) {
	dir := t.TempDir()
	doc := `{"schemaVersion":1,"minimumWriter":1,"state":{"version":1}} true`
	if err := os.WriteFile(filepath.Join(dir, foundationFileName), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := Open(dir); err == nil {
		_ = st.Close()
		t.Fatal("trailing foundation payload was accepted")
	} else if !strings.Contains(err.Error(), "foundation") {
		t.Fatalf("unexpected error: %v", err)
	}
}
