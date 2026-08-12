package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

func TestCBackupExcludesFixedCredentialDirectory(t *testing.T) {
	dataDir := t.TempDir()
	credDir := filepath.Join(dataDir, credentials.DirectoryName)
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := []byte("secret-must-not-be-backed-up")
	if err := os.WriteFile(filepath.Join(credDir, strings.Repeat("a", 64)), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), []byte(`{"agent":{"id":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoCredentialEntries(t, dataDir, secret, false)
}

func TestCBackupExcludesCaseAliasedCredentialDirectoryByIdentity(t *testing.T) {
	dataDir := t.TempDir()
	aliased := filepath.Join(dataDir, strings.ToUpper(credentials.DirectoryName))
	if err := os.Mkdir(aliased, 0o700); err != nil {
		t.Skipf("cannot create case alias fixture: %v", err)
	}
	probe, err := os.Stat(aliased)
	if err != nil {
		t.Skipf("case alias unavailable: %v", err)
	}
	exact, err := os.Stat(filepath.Join(dataDir, credentials.DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(probe, exact) {
		t.Skip("test volume is case-sensitive")
	}
	secret := []byte("aliased-secret-must-not-be-backed-up")
	if err := os.WriteFile(filepath.Join(aliased, strings.Repeat("b", 64)), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), []byte(`{"agent":{"id":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoCredentialEntries(t, dataDir, secret, true)
}

func assertNoCredentialEntries(t *testing.T, dataDir string, secret []byte, aliasFixture bool) {
	t.Helper()
	agentsDir := t.TempDir()
	snapshot, err := Create(Options{Reason: "credential-exclusion", DataDir: dataDir, CodexSessionsDir: agentsDir})
	if err != nil {
		t.Fatal(err)
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
	tarReader := tar.NewReader(gz)
	manifestFound := false
	manifestMarker := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := header.Name
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "codex-loom/"+strings.ToLower(credentials.DirectoryName)+"/") {
			t.Fatalf("backup archive contains credential entry: %s", name)
		}
		content, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), string(secret)) {
			t.Fatalf("backup archive contains credential material in entry %s", name)
		}
		if name == "manifest.json" {
			manifestFound = true
			if strings.Contains(string(content), credentials.DirectoryName+"/**") {
				manifestMarker = true
			}
		}
	}
	if !manifestFound || !manifestMarker {
		t.Fatal("backup manifest does not declare the credential directory exclusion")
	}
	_ = aliasFixture
}
