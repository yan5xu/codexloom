package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestConnectorCommandStreamIsExclusivePerConnection(t *testing.T) {
	s := &Server{activeConnectors: map[string]struct{}{}}
	if !s.acquireConnector("conn-a") {
		t.Fatal("first connector lease was rejected")
	}
	if s.acquireConnector("conn-a") {
		t.Fatal("second connector lease was accepted")
	}
	if !s.acquireConnector("conn-b") {
		t.Fatal("different connection was rejected")
	}
	s.releaseConnector("conn-a")
	if !s.acquireConnector("conn-a") {
		t.Fatal("released connector lease was not reusable")
	}
}

func TestThreadSSEProjectsCanonicalAndLegacyNamespaces(t *testing.T) {
	event := store.Event{Seq: 7, Type: "hub/session-created", Data: json.RawMessage(`{"id":"agent-1"}`)}
	var canonical bytes.Buffer
	writeThreadSSE(&canonical, event, true)
	if got := canonical.String(); !strings.Contains(got, `"type":"loom/agent-created"`) || strings.Contains(got, `"type":"hub/session-created"`) {
		t.Fatalf("canonical SSE = %q", got)
	}

	var legacy bytes.Buffer
	writeThreadSSE(&legacy, event, false)
	if got := legacy.String(); !strings.Contains(got, `"type":"hub/session-created"`) || strings.Contains(got, `"type":"loom/agent-created"`) {
		t.Fatalf("legacy SSE = %q", got)
	}
}

func TestGlobalSSEEmitsCanonicalAndCompatibilityAliases(t *testing.T) {
	var output bytes.Buffer
	writeCompatibleGlobalSSE(&output, store.Event{Type: "hub/comms-message", Data: json.RawMessage(`{}`)})
	got := output.String()
	if !strings.Contains(got, `"type":"loom/comms-message"`) || !strings.Contains(got, `"type":"hub/comms-message"`) {
		t.Fatalf("compatible global SSE = %q", got)
	}
}

func TestGlobalThreadEventHasNoLegacyDuplicate(t *testing.T) {
	var output bytes.Buffer
	writeCompatibleGlobalSSE(&output, store.Event{Type: "loom/thread-event", Data: json.RawMessage(`{"agentId":"agent-1"}`)})
	got := output.String()
	if strings.Count(got, `"type":"loom/thread-event"`) != 1 || strings.Contains(got, `"type":"hub/thread-event"`) {
		t.Fatalf("multiplexed global SSE = %q", got)
	}
}

func TestCodexLoomAgentAPIAndLegacySessionAlias(t *testing.T) {
	t.Setenv("PINIX_EDGE_NAMES", t.TempDir()+"/missing.json")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}).Handler()

	for _, path := range []string{"/api/agents", "/api/sessions", "/api/usage", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, res.Code, res.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s JSON: %v", path, err)
		}
	}
}

func TestWebAssetCachingAndSPAFallback(t *testing.T) {
	server := &Server{web: fstest.MapFS{
		"index.html":          {Data: []byte("app-shell")},
		"assets/index-abc.js": {Data: []byte("export const ready = true")},
	}}
	tests := []struct {
		name    string
		path    string
		status  int
		body    string
		cache   string
		content string
	}{
		{name: "index is never cached", path: "/", status: http.StatusOK, body: "app-shell", cache: "no-store", content: "text/html"},
		{name: "spa route uses current index", path: "/team", status: http.StatusOK, body: "app-shell", cache: "no-store", content: "text/html"},
		{name: "hashed asset is immutable", path: "/assets/index-abc.js", status: http.StatusOK, body: "export const ready = true", cache: "public, max-age=31536000, immutable", content: "text/javascript"},
		{name: "missing asset is not html", path: "/assets/missing-old.js", status: http.StatusNotFound, body: "404 page not found", cache: "no-store", content: "text/plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			server.serveWeb(response, request)
			if response.Code != tt.status {
				t.Fatalf("GET %s = %d, want %d", tt.path, response.Code, tt.status)
			}
			if body := strings.TrimSpace(response.Body.String()); body != tt.body {
				t.Fatalf("GET %s body = %q, want %q", tt.path, body, tt.body)
			}
			if cache := response.Header().Get("Cache-Control"); cache != tt.cache {
				t.Fatalf("GET %s Cache-Control = %q, want %q", tt.path, cache, tt.cache)
			}
			if content := response.Header().Get("Content-Type"); !strings.Contains(content, tt.content) {
				t.Fatalf("GET %s Content-Type = %q, want %q", tt.path, content, tt.content)
			}
		})
	}
}

func TestCanonicalEventType(t *testing.T) {
	tests := map[string]string{
		"hub/live":            "loom/live",
		"hub/session-created": "loom/agent-created",
		"hub/session-status":  "loom/agent-status",
		"hub/session-killed":  "loom/agent-archived",
		"hub/turn-started":    "loom/turn-started",
		"item/completed":      "item/completed",
	}
	for input, want := range tests {
		if got := canonicalEventType(input); got != want {
			t.Errorf("canonicalEventType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRestartDrainAllowsCausalReplyButRejectsNewRootWork(t *testing.T) {
	if !isDrainCompletionMessage(hub.CommParams{ReplyTo: "msg_required"}) {
		t.Fatal("causal reply was not treated as drain completion")
	}
	if isDrainCompletionMessage(hub.CommParams{To: "other-agent", Subject: "new work"}) {
		t.Fatal("new root message was treated as drain completion")
	}
}
