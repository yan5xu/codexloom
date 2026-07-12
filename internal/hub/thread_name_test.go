package hub

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestSyncThreadNamesSendsPersistedNames(t *testing.T) {
	logPath := installFakeCodexNameServer(t)
	h := &Hub{
		agents: map[string]*Agent{
			"a": {ID: "a", Name: "agent-a", ThreadID: "thread-b"},
			"b": {ID: "b", Name: "agent-b", ThreadID: "thread-a"},
			"c": {ID: "c", Name: "no-thread"},
		},
	}

	if err := h.SyncThreadNames(); err != nil {
		t.Fatal(err)
	}
	requests := readThreadNameRequests(t, logPath)
	if len(requests) != 2 {
		t.Fatalf("thread/name/set requests = %d, want 2: %#v", len(requests), requests)
	}
	if requests[0].ThreadID != "thread-a" || requests[0].Name != "agent-b" {
		t.Fatalf("first request = %#v", requests[0])
	}
	if requests[1].ThreadID != "thread-b" || requests[1].Name != "agent-a" {
		t.Fatalf("second request = %#v", requests[1])
	}
}

func TestUpdateConfigSyncsRenamedThread(t *testing.T) {
	logPath := installFakeCodexNameServer(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess"] = &Agent{ID: "sess", Name: "old-name", ThreadID: "thread-1", Status: "idle"}
	name := "new-name"

	view, err := h.UpdateConfig("sess", ConfigParams{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != name {
		t.Fatalf("Agent name = %q, want %q", view.Name, name)
	}
	requests := readThreadNameRequests(t, logPath)
	if len(requests) != 1 || requests[0].ThreadID != "thread-1" || requests[0].Name != name {
		t.Fatalf("thread/name/set requests = %#v", requests)
	}
}

type threadNameRequest struct {
	ThreadID string
	Name     string
}

func installFakeCodexNameServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	logPath := filepath.Join(dir, "requests.ndjson")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_NAME_LOG"
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  if [ -n "$id" ]; then
    printf '{"id":%s,"result":{}}\n' "$id"
  fi
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BIN", binPath)
	t.Setenv("CODEX_NAME_LOG", logPath)
	return logPath
}

func readThreadNameRequests(t *testing.T, path string) []threadNameRequest {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var requests []threadNameRequest
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var message struct {
			Method string `json:"method"`
			Params struct {
				ThreadID string `json:"threadId"`
				Name     string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
		if message.Method == "thread/name/set" {
			requests = append(requests, threadNameRequest{
				ThreadID: message.Params.ThreadID,
				Name:     message.Params.Name,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return requests
}
