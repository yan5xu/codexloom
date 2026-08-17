package hub

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCommandExecutionDescriptionRemainsInRealtimeRawEvents(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", ThreadID: "thread-1", Status: "running",
		CurrentTurnID: "turn-1", CreatedAt: now(), UpdatedAt: now(),
	}
	rt := &runtime{
		agentID:   "agent-1",
		approvals: map[string]*approval{},
		activeTurn: &turnState{
			turnID: "turn-1", startedAt: time.Now(), stopWatchdog: make(chan struct{}),
		},
	}
	for _, method := range []string{"item/started", "item/completed"} {
		h.onNotification(rt, method, json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"commandExecution","id":"cmd-1","command":"printf probe-ok","description":"Run the isolated command probe","status":"completed"}}`))
	}
	events, err := st.ReadEvents("agent-1", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, event := range events {
		if event.Type != "item/started" && event.Type != "item/completed" {
			continue
		}
		var payload struct {
			Item struct {
				Description string `json:"description"`
			} `json:"item"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Item.Description != "Run the isolated command probe" {
			t.Fatalf("%s description = %q", event.Type, payload.Item.Description)
		}
		seen++
	}
	if seen != 2 {
		t.Fatalf("command lifecycle events = %d, want 2", seen)
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

func TestTurnStartedNotificationRebindsStaleResponseIDAndLinkedWork(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	h.attempts = map[string]*HandlingAttempt{}
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "research", ThreadID: "thread-1", Status: "running",
		CurrentTurnID: "turn-stale", CurrentTask: "Investigate", CreatedAt: now(), UpdatedAt: now(),
	}
	h.attempts["att-1"] = &HandlingAttempt{
		ID: "att-1", InboxItemID: "inb-1", AgentID: "agent-1", Status: "running", TurnID: "turn-stale", StartedAt: now(),
	}
	h.comms["msg-1"] = &AgentMessage{
		ID: "msg-1", ToAgentID: "agent-1", DeliveryStatus: "delivered", DeliveredTurnID: "turn-stale",
		HandlingStatus: "running", ActiveHandlingID: "matt-1", UpdatedAt: now(),
		HandlingAttempts: []AgentMessageHandlingAttempt{{
			ID: "matt-1", TurnID: "turn-stale", Status: "running", StartedAt: now(),
		}},
	}
	turn := &turnState{
		turnID: "turn-stale", task: "Investigate", source: "internal", attemptID: "att-1",
		agentMessageID: "msg-1", handlingAttemptID: "matt-1", startedAt: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	rt := &runtime{agentID: "agent-1", approvals: map[string]*approval{}, activeTurn: turn}
	h.runtimes["agent-1"] = rt

	h.onNotification(rt, "turn/started", json.RawMessage(`{
		"threadId":"thread-1","turn":{"id":"turn-actual","status":"inProgress"}
	}`))

	if rt.activeTurn != turn || turn.turnID != "turn-actual" || !turn.startedConfirmed {
		t.Fatalf("active Turn = %#v, want same confirmed Turn rebound to turn-actual", rt.activeTurn)
	}
	if turn.task != "Investigate" || turn.source != "internal" {
		t.Fatalf("rebind lost local work context: %#v", turn)
	}
	if got := h.agents["agent-1"].CurrentTurnID; got != "turn-actual" {
		t.Fatalf("Agent current Turn = %q", got)
	}
	if got := h.attempts["att-1"].TurnID; got != "turn-actual" {
		t.Fatalf("Inbox attempt Turn = %q", got)
	}
	message := h.comms["msg-1"]
	if message.DeliveredTurnID != "turn-actual" || len(message.HandlingAttempts) != 1 || message.HandlingAttempts[0].TurnID != "turn-actual" {
		t.Fatalf("message handling was not rebound: %#v", message)
	}
}

func TestStaleTerminalNotificationDoesNotFinishCurrentTurn(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "research", ThreadID: "thread-1", Status: "running",
		CurrentTurnID: "turn-current", CurrentTask: "Current work", CreatedAt: now(), UpdatedAt: now(),
	}
	turn := &turnState{
		turnID: "turn-current", startedConfirmed: true, task: "Current work", source: "owner",
		startedAt: time.Now(), stopWatchdog: make(chan struct{}),
	}
	rt := &runtime{agentID: "agent-1", approvals: map[string]*approval{}, activeTurn: turn}
	h.runtimes["agent-1"] = rt

	h.onNotification(rt, "turn/completed", json.RawMessage(`{
		"threadId":"thread-1","turn":{"id":"turn-previous","status":"completed"}
	}`))

	if rt.activeTurn != turn || turn.finished {
		t.Fatalf("stale terminal event finished current Turn: %#v", rt.activeTurn)
	}
	if meta := h.agents["agent-1"]; meta.Status != "running" || meta.CurrentTurnID != "turn-current" || meta.LastTurn != nil {
		t.Fatalf("stale terminal event changed Agent projection: %#v", meta)
	}
}

func TestActiveTurnInterruptMismatch(t *testing.T) {
	actual, ok := activeTurnInterruptMismatch(errors.New("expected active turn id turn-old but found turn-current"))
	if !ok || actual != "turn-current" {
		t.Fatalf("parsed mismatch = %q, %v", actual, ok)
	}
	for _, message := range []string{
		"some other interrupt failure",
		"expected active turn id turn-old",
		"expected active turn id turn-old but found invalid turn",
	} {
		if actual, ok := activeTurnInterruptMismatch(errors.New(message)); ok {
			t.Fatalf("unexpected mismatch parse for %q: %q", message, actual)
		}
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
		Model:    "gpt-5.6-sol", Effort: "high",
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

func TestUpdateAgentConfigRejectsProviderChangeForBoundThread(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: t.TempDir(), ThreadID: "thread-1",
		ProviderID: "deepseek", Model: "deepseek-v4-flash", Status: "idle",
	}

	providerID := "openrouter"
	_, err = h.UpdateAgentConfig("agent-1", ConfigParams{ProviderID: &providerID})
	if err == nil || !strings.Contains(err.Error(), "Provider switch operation") {
		t.Fatalf("UpdateAgentConfig Provider change error = %v", err)
	}
	if h.agents["agent-1"].ProviderID != "deepseek" {
		t.Fatalf("Provider binding changed after rejection: %#v", h.agents["agent-1"])
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

func TestApplyRolloutStatusSummarizesCompletedTopicControlEnvelope(t *testing.T) {
	const threadID = "test-thread-topic-display"
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "20")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := `<loom_topic_context version="1"><brief><summary>Internal state</summary></brief></loom_topic_context>
<owner_topic_input version="1"><message><![CDATA[Verify the visible Topic task.]]></message></owner_topic_input>`
	records := []map[string]any{
		{"timestamp": "2026-07-20T01:00:00Z", "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-topic"}},
		{"timestamp": "2026-07-20T01:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": prompt}},
		{"timestamp": "2026-07-20T01:00:02Z", "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "turn-topic"}},
	}
	var data []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	path := filepath.Join(day, "rollout-2026-07-20T01-00-00-"+threadID+".jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{Agent: Agent{ThreadID: threadID, Status: "idle"}}
	applyRolloutStatus(&view)
	if view.LastTurn == nil || view.LastTurn.Task != "Verify the visible Topic task." {
		t.Fatalf("last Turn = %#v", view.LastTurn)
	}
}

func TestApplyRolloutStatusMarksStaleExternalRunningTurnInterrupted(t *testing.T) {
	const threadID = "test-thread-stale-running"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, "2000-01-01T00:00:00Z")
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{Agent: Agent{ThreadID: threadID, Status: "idle"}}
	applyRolloutStatus(&view)

	if view.Status != "interrupted" {
		t.Fatalf("status = %q, want interrupted", view.Status)
	}
	if view.CurrentTurnID != "" {
		t.Fatalf("current turn = %q, want empty", view.CurrentTurnID)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "interrupted" || view.LastTurn.TurnID != "turn-running" {
		t.Fatalf("last turn = %#v, want stale running turn summarized as interrupted", view.LastTurn)
	}
}

func TestApplyRolloutStatusMarksPersistedStaleRunningTurnInterrupted(t *testing.T) {
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

	if view.Status != "interrupted" {
		t.Fatalf("status = %q, want interrupted", view.Status)
	}
	if view.CurrentTask != "" || view.CurrentTurnID != "" {
		t.Fatalf("current task/turn = %q/%q, want empty", view.CurrentTask, view.CurrentTurnID)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "interrupted" || view.LastTurn.TurnID != "turn-running" {
		t.Fatalf("last turn = %#v, want stale persisted running turn summarized as interrupted", view.LastTurn)
	}
}

func TestApplyRolloutStatusKeepsDismissedInterruptedTurnIdle(t *testing.T) {
	const threadID = "test-thread-dismissed-interruption"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, time.Now().UTC().Format(time.RFC3339Nano))
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	view := AgentView{Agent: Agent{
		ThreadID: threadID, Status: "idle",
		LastTurn: &TurnSummary{TurnID: "turn-running", Task: "keep working", Status: "interrupted", CompletedAt: now()},
	}}
	applyRolloutStatus(&view)

	if view.Status != "idle" || view.CurrentTurnID != "" {
		t.Fatalf("dismissed view = %#v, want idle", view)
	}
}

func TestGetTurnLocatesAgentAndDurableSource(t *testing.T) {
	const threadID = "test-thread-turn-get"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, "2026-07-21T01:00:00Z")
	t.Setenv("CODEX_SESSIONS_DIR", dir)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-0"] = &Agent{ID: "agent-0", Name: "no-rollout", ThreadID: "thread-without-rollout", Status: "idle"}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", ThreadID: threadID, Cwd: "/repo", Status: "idle"}
	h.comms["msg-1"] = &AgentMessage{
		ID: "msg-1", ToAgentID: "agent-1", DeliveryMode: "turn_start", DeliveredTurnID: "turn-running",
		DeliveryStatus: "delivered", HandlingStatus: "interrupted", LastHandlingError: "CodexLoom restarted",
		TopicID: "tpc-1",
	}
	h.commOrder = []string{"msg-1"}

	turn, err := h.GetTurn("turn-running")
	if err != nil {
		t.Fatal(err)
	}
	if turn.AgentID != "agent-1" || turn.Agent != "worker" || turn.ThreadID != threadID || turn.Status != "interrupted" {
		t.Fatalf("Turn identity/status = %#v", turn)
	}
	if turn.Source == nil || turn.Source.Kind != "internal" || turn.Source.ID != "msg-1" || turn.Source.TopicID != "tpc-1" {
		t.Fatalf("Turn source = %#v", turn.Source)
	}
	if turn.Error != "CodexLoom restarted" || len(turn.Items) != 1 || turn.Items[0]["text"] != "keep working" {
		t.Fatalf("Turn detail = %#v", turn)
	}
	if _, err := h.GetTurn("turn-missing"); err == nil {
		t.Fatal("missing Turn did not return an error")
	}
}

func TestGetTurnPreservesRecentExternalRunningStatus(t *testing.T) {
	const threadID = "test-thread-turn-get-live"
	dir := t.TempDir()
	writeTestRollout(t, dir, threadID, time.Now().UTC().Format(time.RFC3339Nano))
	t.Setenv("CODEX_SESSIONS_DIR", dir)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", ThreadID: threadID, Status: "idle"}

	turn, err := h.GetTurn("turn-running")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != "running" {
		t.Fatalf("status = %q, want running for recent external Turn", turn.Status)
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
