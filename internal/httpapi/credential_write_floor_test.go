package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/backup"
	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestManagedCredentialWriteRequiresExactVerifiedRollbackBuild(t *testing.T) {
	previousCommit := buildinfo.Commit
	buildinfo.Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Cleanup(func() { buildinfo.Commit = previousCommit })
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}})
	if err := verifyManagedCredentialWriteFloor(server); err == nil {
		t.Fatal("managed credential write floor accepted a missing rollback snapshot")
	}
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Create(backup.Options{
		Reason: "credential-write-floor", DataDir: dataDir, CodexSessionsDir: sessionsDir,
		Build: buildinfo.Commit,
	}); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedCredentialWriteFloor(server); err != nil {
		t.Fatalf("exact verified rollback build was rejected: %v", err)
	}
	server.build.Commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := verifyManagedCredentialWriteFloor(server); err == nil {
		t.Fatal("rollback snapshot from another build satisfied the write floor")
	}
}

func TestManagedOnboardingStopsBeforeFirstCredentialWriteWithoutRollbackFloor(t *testing.T) {
	previousCommit := buildinfo.Commit
	buildinfo.Commit = "cccccccccccccccccccccccccccccccccccccccc"
	t.Cleanup(func() { buildinfo.Commit = previousCommit })
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}})
	oldDiscover := discoverFeishu
	discoverFeishu = func(context.Context, string, string) (feishu.Discovery, error) { return feishu.Discovery{}, nil }
	t.Cleanup(func() { discoverFeishu = oldDiscover })
	appID := "app-floor-fixture"
	_, err = server.saveLarkCredentials(t.Context(), larkCredentialParams{AppID: appID, AppSecret: randomTestCredential(t)})
	var hubErr *hub.HubError
	if !errors.As(err, &hubErr) || hubErr.Status != http.StatusConflict || hubErr.Message != "credential_rollback_build_floor_unavailable" {
		t.Fatalf("managed onboarding floor error = %v", err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := feishu.ManagedAppSecretReference(credentials, appID); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Fatalf("managed credential was written before rollback floor: %v", err)
	}
}

func allowManagedCredentialWritesForTest(t *testing.T) {
	t.Helper()
	previous := verifyManagedCredentialWriteFloor
	verifyManagedCredentialWriteFloor = func(*Server) error { return nil }
	t.Cleanup(func() { verifyManagedCredentialWriteFloor = previous })
}
