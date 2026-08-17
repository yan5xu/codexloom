package httpapi

import (
	"encoding/json"
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

func TestTurnGetRouteFindsTurnWithoutAgentKey(t *testing.T) {
	sessions := t.TempDir()
	threadID := "thread-api-turn"
	day := filepath.Join(sessions, "2026", "07", "21")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := `{"timestamp":"2026-07-21T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-api"}}
{"timestamp":"2026-07-21T01:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"inspect me"}}
{"timestamp":"2026-07-21T01:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-api"}}
`
	path := filepath.Join(day, "rollout-2026-07-21T01-00-00-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", sessions)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*hub.Agent{
		"agent-1": {ID: "agent-1", Name: "worker", ThreadID: threadID, Status: "idle", CreatedAt: nowForTest(), UpdatedAt: nowForTest()},
	}); err != nil {
		t.Fatal(err)
	}
	ro, err := st.OpenReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	h, err := hub.OpenWithOptions(ro, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o644)}}
	server := httptest.NewServer(New(h, nil, web).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/api/turns/turn-api")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var body struct {
		Turn hub.TurnDetail `json:"turn"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Turn.ID != "turn-api" || body.Turn.Agent != "worker" || body.Turn.Status != "completed" {
		t.Fatalf("Turn = %#v", body.Turn)
	}

	missing, err := http.Get(server.URL + "/api/turns/turn-missing")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", missing.StatusCode, http.StatusNotFound)
	}
}
