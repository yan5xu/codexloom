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

func TestCodexLoomAgentAPIAndLegacySessionAlias(t *testing.T) {
	t.Setenv("PINIX_EDGE_NAMES", t.TempDir()+"/missing.json")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}).Handler()

	for _, path := range []string{"/api/agents", "/api/sessions", "/api/health"} {
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
