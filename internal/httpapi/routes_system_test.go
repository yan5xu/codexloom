package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestVersionReportsRunningArtifact(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	started := time.Date(2026, 7, 15, 2, 3, 4, 0, time.UTC)
	web := fstest.MapFS{"index.html": {Data: []byte(`<script src="/assets/index-test.js"></script>`)}}
	server := NewWithOptions(h, st, web, Options{StartedAt: started, Mode: "canary", ReadOnly: true})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Build buildinfo.Info `json:"build"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Build.Mode != "canary" || !response.Build.ReadOnly || response.Build.WebAsset != "assets/index-test.js" {
		t.Fatalf("build = %#v", response.Build)
	}
	if response.Build.StartedAt != "2026-07-15T02:03:04Z" {
		t.Fatalf("startedAt = %s", response.Build.StartedAt)
	}
}

func TestReadOnlyCanaryRejectsWritesAndExternalReads(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	server := NewWithOptions(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}, Options{Mode: "canary", ReadOnly: true}).Handler()

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/agents"},
		{http.MethodDelete, "/api/remote/devices/example"},
		{http.MethodGet, "/api/integrations/providers/lark/discovery"},
		{http.MethodGet, "/api/remote/devices"},
	} {
		request := httptest.NewRequest(test.method, test.path, bytes.NewReader([]byte(`{}`)))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", test.method, test.path, response.Code)
		}
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/agents = %d: %s", response.Code, response.Body.String())
	}
}
