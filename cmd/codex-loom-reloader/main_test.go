package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalExecutableMigratesLegacyBinaryName(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "codex-hub")
	canonical := filepath.Join(dir, "codex-loom")
	if err := os.WriteFile(canonical, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := canonicalExecutable(legacy); got != canonical {
		t.Fatalf("canonicalExecutable() = %q, want %q", got, canonical)
	}
}

func TestCanonicalExecutableKeepsLegacyWhenReplacementMissing(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "codex-hub")
	if got := canonicalExecutable(legacy); got != legacy {
		t.Fatalf("canonicalExecutable() = %q, want %q", got, legacy)
	}
}
