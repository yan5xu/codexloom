package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestAgentEventIsMultiplexedToGlobalSubscribers(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	global, cancel := h.SubscribeGlobal()
	defer cancel()

	h.mu.Lock()
	local := h.emitLocked("agent-1", "item/completed", map[string]any{"item": map[string]any{"id": "answer-1"}})
	h.mu.Unlock()

	select {
	case event := <-global:
		if event.Type != "loom/thread-event" {
			t.Fatalf("global event type = %q", event.Type)
		}
		if event.Seq <= 0 {
			t.Fatalf("global event has no durable cursor: %#v", event)
		}
		var payload struct {
			AgentID string      `json:"agentId"`
			Event   store.Event `json:"event"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.AgentID != "agent-1" || payload.Event.Seq != local.Seq || payload.Event.Type != local.Type {
			t.Fatalf("multiplexed payload = %#v, local = %#v", payload, local)
		}
		replayed, err := h.ReadGlobalEvents(event.Seq-1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(replayed) != 1 || replayed[0].Seq != event.Seq || replayed[0].Type != event.Type {
			t.Fatalf("global replay = %#v, want cursor %d", replayed, event.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("global subscriber did not receive Agent event")
	}
}

func TestCompletedNotificationWithFailedTurnStatusProjectsFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", ThreadID: "thread-1", Status: "running",
		CurrentTurnID: "turn-1", CurrentTask: "Do work", CreatedAt: now(), UpdatedAt: now(),
	}
	rt := &runtime{
		agentID:   "agent-1",
		approvals: map[string]*approval{},
		activeTurn: &turnState{
			turnID: "turn-1", task: "Do work", startedAt: time.Now(), stopWatchdog: make(chan struct{}),
		},
	}
	h.onNotification(rt, "turn/completed", json.RawMessage(`{
		"threadId":"thread-1",
		"turn":{"id":"turn-1","status":"failed","error":{"message":"model is unavailable"}}
	}`))

	meta := h.agents["agent-1"]
	if meta.Status != "idle" || meta.LastError != "model is unavailable" {
		t.Fatalf("agent failure projection = %#v", meta)
	}
	if meta.LastTurn == nil || meta.LastTurn.Status != "failed" || meta.LastTurn.TurnID != "turn-1" {
		t.Fatalf("last turn = %#v", meta.LastTurn)
	}
	events, err := st.ReadEvents("agent-1", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "loom/turn-failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events do not contain loom/turn-failed: %#v", events)
	}
}

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

func TestOpenRejectsCorruptRegistryWithoutOverwritingIt(t *testing.T) {
	t.Setenv("PINIX_EDGE_NAMES", filepath.Join(t.TempDir(), "missing.json"))
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{not-json\n")
	if err := os.WriteFile(filepath.Join(dataDir, "agents.json"), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(st); err == nil {
		t.Fatal("Open accepted a corrupt Agent registry")
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("corrupt registry was overwritten: %q", got)
	}
}

func TestUpdateAgentConfigRollsBackWhenRegistryCommitFails(t *testing.T) {
	t.Setenv("PINIX_EDGE_NAMES", filepath.Join(t.TempDir(), "missing.json"))
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(RestoreAgentParams{
		ID: "agent-1", Name: "before", Cwd: "/tmp", ThreadID: "thread-1",
	}); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(dataDir, "agents.json")
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(registry, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(registry)
	}()

	rename := "after"
	if _, err := h.UpdateAgentConfig("agent-1", ConfigParams{Name: &rename}); err == nil {
		t.Fatal("config update succeeded after registry commit failure")
	}
	view, err := h.GetAgent("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "before" {
		t.Fatalf("in-memory Agent name = %q, want rollback to before", view.Name)
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

func TestTurnSource(t *testing.T) {
	tests := []struct {
		name           string
		inboxItemID    string
		agentMessageID string
		want           string
	}{
		{name: "owner", want: "owner"},
		{name: "internal", agentMessageID: "msg_123", want: "internal"},
		{name: "external", inboxItemID: "inb_123", want: "external"},
		{name: "external wins when both identifiers exist", inboxItemID: "inb_123", agentMessageID: "msg_123", want: "external"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := turnSource(test.inboxItemID, test.agentMessageID); got != test.want {
				t.Fatalf("turnSource(%q, %q) = %q, want %q", test.inboxItemID, test.agentMessageID, got, test.want)
			}
		})
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
