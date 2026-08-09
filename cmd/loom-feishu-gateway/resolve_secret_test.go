package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

func TestResolveGatewaySecretManagedNoFallback(t *testing.T) {
	dir := t.TempDir()
	credDir := filepath.Join(dir, credentials.DirectoryName)
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := []byte("managed-secret-value")
	ref := "managed:" + strings.Repeat("a", 64)
	id := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(credDir, id), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveGatewaySecret(dir, ref, "cli_app")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(secret) {
		t.Fatalf("resolved secret mismatch: %q", got)
	}
	// Missing file must fail without falling back to env/Keychain.
	missingRef := "managed:" + strings.Repeat("b", 64)
	if _, err := resolveGatewaySecret(dir, missingRef, "cli_app"); err == nil {
		t.Fatal("missing managed credential fell back to env/Keychain")
	}
	// Wrong ref shape must fail.
	if _, err := resolveGatewaySecret(dir, "managed:not-canonical", "cli_app"); err == nil {
		t.Fatal("malformed managed ref was accepted")
	}
	// Secret must never appear in the error.
	if _, err := resolveGatewaySecret(dir, missingRef, "cli_app"); err != nil && strings.Contains(err.Error(), string(secret)) {
		t.Fatal("secret leaked into an error")
	}
}

func TestResolveGatewaySecretLegacyWithoutKeychain(t *testing.T) {
	// Legacy path with no env secret and no app id must fail cleanly and must
	// not touch Keychain; the managed path is independent.
	if err := os.Setenv("FEISHU_APP_SECRET", ""); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("FEISHU_APP_SECRET")
	if _, err := resolveGatewaySecret(t.TempDir(), "", ""); err == nil {
		t.Fatal("legacy resolution succeeded without any secret source")
	}
}
