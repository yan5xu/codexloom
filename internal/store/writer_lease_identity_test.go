package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritableStoreFailsClosedWhenOpeningSymlinkRetargets(t *testing.T) {
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "data")
	if err := os.Symlink(first, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	st, err := Open(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTopics(map[string]any{"must": "fail closed"}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("write after symlink retarget = %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "topics.json")); !os.IsNotExist(err) {
		t.Fatalf("retargeted directory received a write: %v", err)
	}
}

func TestWritableStoreFailsClosedWhenDirectoryIdentityDrifts(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
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
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTopics(map[string]any{"must": "fail closed"}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("write after directory replacement = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "topics.json")); !os.IsNotExist(err) {
		t.Fatalf("replacement directory received a write: %v", err)
	}
}

func TestRootedWriteCannotEscapeWhenDirectoryMovesAfterInitialCheck(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	done, err := st.beginWrite()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(dir, moved); err != nil {
		done()
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		done()
		t.Fatal(err)
	}
	err = st.saveJSONUnlocked(filepath.Join(st.dir, "topics.json"), map[string]any{"rooted": true})
	err = st.finishWrite(err)
	done()
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("mid-write directory drift = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "topics.json")); !os.IsNotExist(err) {
		t.Fatalf("replacement directory received rooted write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "topics.json")); err != nil {
		t.Fatalf("rooted write did not stay bound to original directory handle: %v", err)
	}
}

func TestLinuxFilesystemAllowlistRejectsUnknownTypeMechanically(t *testing.T) {
	if supportedLinuxLocalFilesystemType(0xDEADBEEF) {
		t.Fatal("unknown Linux filesystem type was accepted")
	}
	for _, value := range []uint64{0xEF53, 0x58465342, 0x9123683E, 0x01021994, 0x858458F6, 0x794C7630} {
		if !supportedLinuxLocalFilesystemType(value) {
			t.Fatalf("explicit Linux local filesystem type %#x was rejected", value)
		}
	}
}
