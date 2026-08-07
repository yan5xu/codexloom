package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddFileExcludesTornNDJSONTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comms.ndjson")
	complete := []byte("{\"message\":{\"id\":\"one\"}}\n")
	if err := os.WriteFile(path, append(append([]byte(nil), complete...), []byte("{\"message\":")...), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := addFile(tw, path, "codex-loom/comms.ndjson"); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(bytes.NewReader(archive.Bytes()))
	header, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if header.Size != int64(len(complete)) || !bytes.Equal(data, complete) {
		t.Fatalf("archived NDJSON size=%d data=%q", header.Size, data)
	}
}

func TestCreateUsesCodexLoomNameAndLayout(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "collaboration-groups.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "credential-migrations.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "events"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "events", "agent-1.ndjson"), []byte("derived\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "credentials"), 0o700); err != nil {
		t.Fatal(err)
	}
	secretBytes := make([]byte, 64)
	if _, err := rand.Read(secretBytes); err != nil {
		t.Fatal("random test value generation failed")
	}
	if err := os.WriteFile(filepath.Join(dataDir, "credentials", "managed.json"), secretBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Create(Options{
		DataDir:          dataDir,
		CodexSessionsDir: filepath.Join(t.TempDir(), "sessions"),
		Reason:           "rename-test",
		Agents:           []AgentRef{{ID: "agent-1", Name: "alpha", ThreadID: "thread-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isSnapshotName(snapshot.Name) || snapshot.Name[:11] != "codex-loom-" {
		t.Fatalf("unexpected snapshot name %q", snapshot.Name)
	}
	if snapshot.CredentialsIncluded || snapshot.RunnableRestore || snapshot.BackupStatus != "credentials_excluded" {
		t.Fatalf("ordinary snapshot credential status = included %v runnable %v status %q", snapshot.CredentialsIncluded, snapshot.RunnableRestore, snapshot.BackupStatus)
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
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	foundCollaborationGroups := false
	foundMigrationReceipts := false
	foundDerivedEvent := false
	foundCredential := false
	manifestReportsExcludedCredentials := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "codex-loom/agents.json" {
			found = true
		}
		if header.Name == "codex-loom/collaboration-groups.json" {
			foundCollaborationGroups = true
		}
		if header.Name == "codex-loom/credential-migrations.json" {
			foundMigrationReceipts = true
		}
		if header.Name == "codex-loom/events/agent-1.ndjson" {
			foundDerivedEvent = true
		}
		if strings.HasPrefix(header.Name, "codex-loom/credentials/") {
			foundCredential = true
		}
		if header.Name == "manifest.json" {
			manifestBytes, readErr := io.ReadAll(tr)
			if readErr != nil {
				t.Fatal(readErr)
			}
			manifestReportsExcludedCredentials = bytes.Contains(manifestBytes, []byte("credentials/**")) && bytes.Contains(manifestBytes, []byte("not a complete runnable restore"))
		}
	}
	if !found {
		t.Fatal("snapshot did not contain codex-loom/agents.json")
	}
	if !foundCollaborationGroups {
		t.Fatal("snapshot did not contain codex-loom/collaboration-groups.json")
	}
	if !foundMigrationReceipts {
		t.Fatal("snapshot did not contain non-secret credential migration receipts")
	}
	if foundDerivedEvent {
		t.Fatal("snapshot contained derived SSE replay events")
	}
	if foundCredential {
		t.Fatal("ordinary snapshot contained managed credentials")
	}
	if !manifestReportsExcludedCredentials {
		t.Fatal("snapshot manifest did not report that managed credentials were excluded")
	}
}

func TestListIncludesLegacySnapshots(t *testing.T) {
	dataDir := t.TempDir()
	dir := DefaultDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"codex-hub-20260101T000000Z-manual.tar.gz",
		"codex-loom-20260102T000000Z-manual.tar.gz",
		"unrelated.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	items, err := List(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("List() returned %d snapshots, want 2", len(items))
	}
}

func TestApplyRetentionBoundsBytesAndPreservesRecoveryFloor(t *testing.T) {
	dataDir := t.TempDir()
	dir := DefaultDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "codex-loom-20260715T12000"+string(rune('0'+i))+"Z-test.tar.gz")
		if err := os.WriteFile(path, make([]byte, 100), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := current.Add(-time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	report, err := applyRetentionAt(dir, RetentionPolicy{
		MinCount: 2,
		MaxCount: 5,
		MaxBytes: 250,
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if report.BeforeCount != 5 || report.AfterCount != 2 || report.RemovedCount != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.AfterBytes != 200 || report.RemovedBytes != 300 {
		t.Fatalf("unexpected byte accounting: %+v", report)
	}
	items, err := List(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].CreatedAt.After(items[1].CreatedAt) {
		t.Fatalf("expected two newest snapshots, got %+v", items)
	}
}

func TestApplyRetentionRemovesExpiredSnapshotsAfterMinimum(t *testing.T) {
	dataDir := t.TempDir()
	dir := DefaultDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	ages := []time.Duration{time.Hour, 48 * time.Hour, 72 * time.Hour}
	for i, age := range ages {
		path := filepath.Join(dir, "codex-loom-20260715T12000"+string(rune('0'+i))+"Z-test.tar.gz")
		if err := os.WriteFile(path, []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := current.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	report, err := applyRetentionAt(dir, RetentionPolicy{MinCount: 1, MaxAge: 24 * time.Hour}, current)
	if err != nil {
		t.Fatal(err)
	}
	if report.AfterCount != 1 || report.RemovedCount != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRetentionPolicyNormalizesMinimumAgainstMaximum(t *testing.T) {
	policy := (RetentionPolicy{MinCount: 8, MaxCount: 3, MaxBytes: -1, MaxAge: -1}).Normalize()
	if policy.MinCount != 3 || policy.MaxBytes != 0 || policy.MaxAge != 0 {
		t.Fatalf("unexpected normalized policy: %+v", policy)
	}
}
