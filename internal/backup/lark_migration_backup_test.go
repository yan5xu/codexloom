package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The non-secret Lark migration record must survive ordinary backups so a
// restored data directory can still roll back the legacy reference.
func TestLarkMigrationRecordSurvivesBackup(t *testing.T) {
	dataDir := t.TempDir()
	recordDir := filepath.Join(dataDir, "lark-migrations")
	if err := os.MkdirAll(recordDir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := []byte(`{"version":1,"connectionId":"conn_1","previousRef":"keychain:old","currentRef":"managed:abc","phase":"completed"}`)
	if err := os.WriteFile(filepath.Join(recordDir, "conn_1.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), []byte(`{"agent":{"id":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agentsDir := t.TempDir()
	snapshot, err := Create(Options{Reason: "lark-migration-record", DataDir: dataDir, CodexSessionsDir: agentsDir})
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
	found := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "codex-loom/lark-migrations/conn_1.json" {
			content, err := io.ReadAll(tarReader)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != string(record) {
				t.Fatalf("migration record content changed: %q", content)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("Lark migration record was not included in the backup")
	}
}
