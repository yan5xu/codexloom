package main

import (
	"strings"
	"testing"
)

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
