package launchagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderInspectInjectsCanonicalManagedProxy(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, rendered, err := Render(Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/local/bin:/usr/bin:/bin",
		NoProxy: "localhost, EXAMPLE.invalid,example.invalid,.service.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "example.invalid,example.invalid") {
		t.Fatalf("rendered plist did not deduplicate: %s", data)
	}
	if !strings.Contains(string(data), "<key>CODEX_LOOM_NO_PROXY</key>") {
		t.Fatal("rendered plist is missing CODEX_LOOM_NO_PROXY")
	}
	inspection, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if inspection != rendered || inspection.Proxy.EntryCount != 3 || len(inspection.Proxy.SHA256) != 64 {
		t.Fatalf("inspection = %#v, rendered = %#v", inspection, rendered)
	}
}

func TestWriteIsAtomicOwnerOnlyAndRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unit.plist")
	if err := Write(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" || info.Mode().Perm() != 0o600 {
		t.Fatalf("installed plist data=%q mode=%#o", data, info.Mode().Perm())
	}
	link := filepath.Join(root, "linked.plist")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("unsafe")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink write error = %v", err)
	}
}

func TestUpdateProxyPreservesUnrelatedEnvironmentAndCollapsesLegacySpellings(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _, err := Render(Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/bin", NoProxy: "managed-old.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(data), "    <key>PATH</key>", `    <key>NO_PROXY</key>
    <string>legacy.invalid,shared.invalid</string>
    <key>no_proxy</key>
    <string>SHARED.invalid</string>
    <key>CODEX_LOOM_CODEX_BIN</key>
    <string>/opt/codex/bin/codex</string>
    <key>PATH</key>`, 1)
	updated, inspection, err := UpdateProxy([]byte(legacy), "new.invalid,MANAGED-OLD.invalid")
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.Contains(text, "<key>CODEX_LOOM_CODEX_BIN</key>") || !strings.Contains(text, "/opt/codex/bin/codex") {
		t.Fatalf("unrelated environment was not preserved: %s", text)
	}
	if strings.Contains(text, "<key>NO_PROXY</key>") || strings.Contains(text, "<key>no_proxy</key>") {
		t.Fatalf("legacy spellings were not collapsed: %s", text)
	}
	if !strings.Contains(text, "<string>legacy.invalid,shared.invalid,managed-old.invalid,new.invalid</string>") {
		t.Fatalf("canonical managed value missing: %s", text)
	}
	if inspection.Proxy.EntryCount != 4 {
		t.Fatalf("updated inspection = %#v", inspection)
	}
}

func TestRenderAndInspectFailClosed(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Render(Config{Executable: executable, WorkingDirectory: root, Path: "/usr/bin"}); err == nil {
		t.Fatal("Render accepted a non-executable binary")
	}
	if _, _, err := Render(Config{Executable: "relative", WorkingDirectory: root, Path: "/usr/bin"}); err == nil {
		t.Fatal("Render accepted a relative executable")
	}

	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _, err := Render(Config{Executable: executable, WorkingDirectory: root, Path: "/usr/bin", NoProxy: "safe.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	missingManaged := strings.Replace(string(data), "    <key>CODEX_LOOM_NO_PROXY</key>\n    <string>safe.invalid</string>\n", "", 1)
	if _, err := Inspect([]byte(missingManaged)); err == nil || !strings.Contains(err.Error(), "missing CODEX_LOOM_NO_PROXY") {
		t.Fatalf("missing managed key error = %v", err)
	}
	nonCanonical := strings.Replace(string(data), "<string>safe.invalid</string>", "<string>safe.invalid,SAFE.invalid</string>", 1)
	if _, err := Inspect([]byte(nonCanonical)); err == nil || !strings.Contains(err.Error(), "not the canonical") {
		t.Fatalf("non-canonical error = %v", err)
	}
	duplicateLabel := strings.Replace(string(data), "  <key>Label</key>", "  <key>Label</key><string>duplicate</string>\n  <key>Label</key>", 1)
	if _, err := Inspect([]byte(duplicateLabel)); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("duplicate key error = %v", err)
	}
}
