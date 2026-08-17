package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/launchagent"
)

func TestProxyDoctorStatusVerifiesLaunchAgentHubAndChildWithoutValues(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, inspection, err := launchagent.Render(launchagent.Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/bin",
		NoProxy: "one.invalid,two.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(root, "unit.plist")
	if err := os.WriteFile(plist, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summary := map[string]any{
		"configured": true, "entryCount": float64(inspection.Proxy.EntryCount), "sha256": inspection.Proxy.SHA256,
	}
	response := map[string]any{"proxy": map[string]any{
		"valid": true, "hub": summary, "codexHostLoaded": true,
		"codexHost": summary, "matching": true,
	}}
	previousColor := useColor
	useColor = false
	defer func() { useColor = previousColor }()
	status := proxyDoctorStatus(plist, response)
	if !strings.Contains(status, "verified") || !strings.Contains(status, "2 entries") {
		t.Fatalf("doctor proxy status = %q", status)
	}
	if strings.Contains(status, "one.invalid") || strings.Contains(status, "two.invalid") {
		t.Fatalf("doctor status leaked entries: %q", status)
	}
}

func TestProxyDoctorStatusRejectsRuntimeMismatch(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _, err := launchagent.Render(launchagent.Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/bin", NoProxy: "one.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(root, "unit.plist")
	if err := os.WriteFile(plist, data, 0o600); err != nil {
		t.Fatal(err)
	}
	previousColor := useColor
	useColor = false
	defer func() { useColor = previousColor }()
	status := proxyDoctorStatus(plist, map[string]any{"proxy": map[string]any{
		"valid": true,
		"hub":   map[string]any{"configured": true, "entryCount": float64(1), "sha256": strings.Repeat("a", 64)},
	}})
	if !strings.Contains(status, "mismatch") {
		t.Fatalf("doctor mismatch status = %q", status)
	}
}

func TestFormatBuildIncludesRuntimeIdentity(t *testing.T) {
	text := formatBuild("running", map[string]any{
		"product": "CodexLoom", "version": "1.2.3", "commit": "abc123", "builtAt": "2026-07-15T01:00:00Z",
		"goVersion": "go1.25", "os": "darwin", "arch": "arm64", "pid": 42.0,
		"startedAt": "2026-07-15T02:00:00Z", "mode": "canary", "readOnly": true,
		"dataDir": "/tmp/canary", "webAsset": "assets/index-test.js",
	})
	for _, want := range []string{"CodexLoom 1.2.3 (abc123)", "pid 42", "mode canary", "read-only true", "assets/index-test.js"} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatBuild missing %q:\n%s", want, text)
		}
	}
}

func TestBuildMismatchDetectsDifferentCommit(t *testing.T) {
	got := buildMismatch(map[string]any{"commit": "new"}, map[string]any{"commit": "old"})
	if !strings.Contains(got, "restart required") {
		t.Fatalf("mismatch = %q", got)
	}
}
