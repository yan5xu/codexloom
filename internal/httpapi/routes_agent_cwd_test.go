package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestAgentCwdHTTPContract(t *testing.T) {
	installAgentCwdHTTPFakeCodex(t)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("PINIX_EDGE_NAMES", filepath.Join(t.TempDir(), "missing.json"))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldCwd := t.TempDir()
	newCwd := t.TempDir()
	if err := st.SaveAgents(map[string]*hub.Agent{
		"agent-1": {ID: "agent-1", Name: "worker", Cwd: oldCwd, ThreadID: "thread-1", Status: "idle", UpdatedAt: "2026-08-16T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	handler := New(h, st, fstest.MapFS{"index.html": {Data: []byte("ok")}}).Handler()

	result := agentCwdHTTPRequest(t, handler, http.MethodPatch, "/api/agents/worker/cwd", map[string]any{"cwd": newCwd}, http.StatusOK)
	update := result["update"].(map[string]any)
	agent := result["agent"].(map[string]any)
	inventory := result["inventory"].(map[string]any)
	if update["agentId"] != "agent-1" || update["threadId"] != "thread-1" || update["oldCwd"] != oldCwd || update["newCwd"] != newCwd {
		t.Fatalf("HTTP update receipt = %#v", update)
	}
	if update["effectiveState"] != "next_thread_start_or_resume" || update["skillsState"] != "refreshed" {
		t.Fatalf("HTTP update states = %#v", update)
	}
	if agent["id"] != "agent-1" || agent["threadId"] != "thread-1" || agent["cwd"] != newCwd || inventory["cwd"] != newCwd {
		t.Fatalf("HTTP Agent/inventory = %#v / %#v", agent, inventory)
	}

	defaultCwd := filepath.Join(userHome, "codexloom", "agents", "worker")
	defaultResult := agentCwdHTTPRequest(t, handler, http.MethodPatch, "/api/agents/worker/cwd", map[string]any{}, http.StatusOK)
	defaultUpdate := defaultResult["update"].(map[string]any)
	if defaultUpdate["oldCwd"] != newCwd || defaultUpdate["newCwd"] != defaultCwd || defaultUpdate["agentId"] != "agent-1" || defaultUpdate["threadId"] != "thread-1" {
		t.Fatalf("default HTTP update receipt = %#v", defaultUpdate)
	}
	info, err := os.Stat(defaultCwd)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("default Agent Home info = %#v, %v", info, err)
	}

	errorResult := agentCwdHTTPRequest(t, handler, http.MethodPatch, "/api/agents/worker/cwd", map[string]any{"cwd": "relative/path"}, http.StatusBadRequest)
	if errorResult["error"] != "cwd must be an absolute directory" {
		t.Fatalf("relative cwd error = %#v", errorResult)
	}
	current, err := h.GetAgent("worker")
	if err != nil {
		t.Fatal(err)
	}
	if current.Cwd != defaultCwd {
		t.Fatalf("rejected HTTP update changed cwd to %q", current.Cwd)
	}
}

func agentCwdHTTPRequest(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func installAgentCwdHTTPFakeCodex(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"result":{"userAgent":"fake-agent-cwd"}}\n' "$id" ;;
    *'"method":"skills/list"'*)
      printf '{"id":%s,"result":{"data":[]}}\n' "$id" ;;
    *'"method":"remoteControl/status/read"'*)
      printf '{"id":%s,"result":{"status":"disabled","serverName":"test.local","installationId":"test-install","environmentId":null}}\n' "$id" ;;
    *) printf '{"id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_LOOM_CODEX_BIN", binPath)
	t.Setenv("CODEX_REMOTE_BIN", binPath)
}
