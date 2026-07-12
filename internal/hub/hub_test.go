package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRestoreAgentKeepsStableIdentityAndDoesNotStartRuntime(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()

	view, err := h.RestoreAgent(RestoreAgentParams{
		ID: "a07193ea", Name: "parall-edge-dev", Cwd: "/tmp/parall-edge",
		ThreadID: "019f53a7-5485-7733-87f8-5b513420f62a",
		Model:    "gpt-5.6-sol", Effort: "high", ProfileVersionSeen: 3,
		CreatedAt: "2026-07-12T00:08:21Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != "a07193ea" || view.ThreadID != "019f53a7-5485-7733-87f8-5b513420f62a" {
		t.Fatalf("restored identity = %#v", view.Agent)
	}
	if view.Status != "idle" || view.ProcessAlive || view.CurrentTurnID != "" || view.CurrentTask != "" {
		t.Fatalf("restored runtime state = %#v", view)
	}

	var persisted map[string]*Agent
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["a07193ea"] == nil || persisted["a07193ea"].Name != "parall-edge-dev" {
		t.Fatalf("persisted agents = %#v", persisted)
	}
	if _, err := h.RestoreAgent(RestoreAgentParams{
		ID: "a07193ea", Name: "duplicate", Cwd: "/tmp/duplicate", ThreadID: "thread-duplicate",
	}); err == nil {
		t.Fatal("duplicate stable id restore succeeded")
	}
}

func TestImportEdgeSkipsAliasForOwnedThread(t *testing.T) {
	edgeFile := filepath.Join(t.TempDir(), "names.json")
	if err := os.WriteFile(edgeFile, []byte(`{
  "old-edge-name": {"threadId":"thread-shared","cwd":"/edge"},
  "other-edge-name": {"threadId":"thread-other","cwd":"/other"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PINIX_EDGE_NAMES", edgeFile)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*Agent{
		"owned": {
			ID: "owned", Name: "renamed-in-loom", ThreadID: "thread-shared", Cwd: "/owned",
			Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		},
	}); err != nil {
		t.Fatal(err)
	}

	h := New(st)
	defer h.Shutdown()
	agents := h.ListAgents()
	if len(agents) != 2 {
		t.Fatalf("agents = %#v, want owned Agent plus one distinct edge Agent", agents)
	}
	for _, agent := range agents {
		if agent.Name == "old-edge-name" {
			t.Fatalf("edge alias for owned Thread was imported: %#v", agent)
		}
	}
}

func TestApplyRolloutStatusShowsRecentExternalRunningTurn(t *testing.T) {
	const threadID = "test-thread-recent-running"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, time.Now().UTC().Format(time.RFC3339Nano))
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{Agent: Agent{ThreadID: threadID, Status: "idle"}}
	applyRolloutStatus(&view)

	if view.Status != "running" {
		t.Fatalf("status = %q, want running", view.Status)
	}
	if view.CurrentTurnID != "turn-running" || view.CurrentTask != "keep working" {
		t.Fatalf("view = %#v, want current running turn", view)
	}
}

func TestApplyRolloutStatusIgnoresStaleExternalRunningTurn(t *testing.T) {
	const threadID = "test-thread-stale-running"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, "2000-01-01T00:00:00Z")
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{Agent: Agent{ThreadID: threadID, Status: "idle"}}
	applyRolloutStatus(&view)

	if view.Status != "idle" {
		t.Fatalf("status = %q, want idle", view.Status)
	}
	if view.CurrentTurnID != "" {
		t.Fatalf("current turn = %q, want empty", view.CurrentTurnID)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "interrupted" || view.LastTurn.TurnID != "turn-running" {
		t.Fatalf("last turn = %#v, want stale running turn summarized as interrupted", view.LastTurn)
	}
}

func TestApplyRolloutStatusClearsPersistedStaleRunningTurn(t *testing.T) {
	const threadID = "test-thread-persisted-stale-running"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, "2000-01-01T00:00:00Z")
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{
		Agent: Agent{
			ThreadID:      threadID,
			Status:        "running",
			CurrentTask:   "old task",
			CurrentTurnID: "turn-running",
		},
		ProcessAlive: false,
	}
	applyRolloutStatus(&view)

	if view.Status != "idle" {
		t.Fatalf("status = %q, want idle", view.Status)
	}
	if view.CurrentTask != "" || view.CurrentTurnID != "" {
		t.Fatalf("current task/turn = %q/%q, want empty", view.CurrentTask, view.CurrentTurnID)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "interrupted" || view.LastTurn.TurnID != "turn-running" {
		t.Fatalf("last turn = %#v, want stale persisted running turn summarized as interrupted", view.LastTurn)
	}
}

func writeTestRollout(t *testing.T, dir, threadID, ts string) {
	t.Helper()
	day := filepath.Join(dir, "2026", "07", "08")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-07-08T10-00-00-"+threadID+".jsonl")
	data := `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-running"}}
{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"user_message","message":"keep working"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
