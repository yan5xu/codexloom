package httpapi

import (
	"crypto/sha256"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestPassiveStoreServeAndShutdownAreByteForByteReadOnly(t *testing.T) {
	dir := t.TempDir()
	fixtures := map[string]string{
		"agents.json": `{"agent":{"id":"agent","name":"agent","status":"idle","pendingProviderSwitch":{"providerId":"fixture","model":"fixture"},"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}`,
		"topics.json": `{"legacy":{"status":"invalid","currentBrief":{}}}`,
	}
	for name, value := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := passiveTreeSnapshot(t, dir)
	st, err := store.OpenWithOptions(dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.CredentialStore(); err == nil {
		t.Fatal("passive read-only Hub opened the managed credential backend")
	}
	handler := NewWithOptions(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}, Options{Mode: "canary", ReadOnly: true}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/integrations/connections", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("passive GET = %d: %s", response.Code, response.Body.String())
	}
	h.Shutdown()
	after := passiveTreeSnapshot(t, dir)
	if len(before) != len(after) {
		t.Fatalf("passive HTTP fileset changed: before=%v after=%v", before, after)
	}
	for path, value := range before {
		if after[path] != value {
			t.Fatalf("passive HTTP durable bytes changed for %s", path)
		}
	}
}

func passiveTreeSnapshot(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := map[string][sha256.Size]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative+"/"] = sha256.Sum256(nil)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
