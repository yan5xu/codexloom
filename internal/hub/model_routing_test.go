package hub

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestCustomProviderModelRouteErrorStopsRetryAndReportsConfigurationFailure(t *testing.T) {
	t.Setenv("CODEX_LOOM_MODEL_CATALOG", "")
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		ProviderID: "custom", Model: "Example-Model", CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	events, cancel, err := h.Subscribe("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if _, err := h.SendTask("agent-1", "hello", time.Minute); err != nil {
		t.Fatal(err)
	}
	var failure string
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for failure == "" {
		select {
		case event := <-events:
			if event.Type != "loom/turn-failed" {
				continue
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			failure = payload.Error
		case <-deadline.C:
			t.Fatal("model routing error remained retrying instead of becoming a terminal configuration failure")
		}
	}
	if !strings.Contains(failure, `model ID "Example-Model"`) || !strings.Contains(failure, "case-sensitive") {
		t.Fatalf("terminal error = %q", failure)
	}
	view, err := h.GetAgent("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "failed" || view.LastError != failure {
		t.Fatalf("Agent failure projection = %#v", view.Agent)
	}
	if got := countRequestMethod(t, logPath, "turn/interrupt"); got != 1 {
		t.Fatalf("turn/interrupt requests = %d, want 1", got)
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	if turn["model"] != "Example-Model" {
		t.Fatalf("turn model = %#v, want case-preserved id", turn["model"])
	}
	correctedModel := "example-model"
	view, err = h.UpdateAgentConfig("agent-1", ConfigParams{Model: &correctedModel})
	if err != nil {
		t.Fatal(err)
	}
	if view.Model != correctedModel {
		t.Fatalf("corrected model = %q, want %q", view.Model, correctedModel)
	}
	if failure := sendAndWaitForTerminalEvent(t, h, "loom/turn-completed"); failure != "" {
		t.Fatalf("corrected model Turn error = %q", failure)
	}
	view, err = h.GetAgent("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "completed" || view.LastError != "" || view.Model != correctedModel {
		t.Fatalf("corrected model recovery = %#v", view.Agent)
	}
	turn = lastRequestParams(t, logPath, "turn/start")
	if turn["model"] != correctedModel {
		t.Fatalf("corrected turn model = %#v, want %q", turn["model"], correctedModel)
	}
}

func TestCustomProviderServiceUnavailableRemainsProviderFailure(t *testing.T) {
	logPath, h := modelRoutingTestHub(t, "Vendor/Model-X")
	failure := sendAndWaitForTerminalEvent(t, h, "loom/turn-failed")
	const want = "unexpected status 503 Service Unavailable: upstream temporarily unavailable"
	if failure != want {
		t.Fatalf("terminal error = %q, want %q", failure, want)
	}
	if got := countRequestMethod(t, logPath, "turn/interrupt"); got != 0 {
		t.Fatalf("turn/interrupt requests = %d, want 0 for a genuine Provider outage", got)
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	if turn["model"] != "Vendor/Model-X" {
		t.Fatalf("turn model = %#v, want case-preserved id", turn["model"])
	}
}

func TestCustomProviderSuccessfulCaseSensitiveModelRouteIsUnchanged(t *testing.T) {
	logPath, h := modelRoutingTestHub(t, "Vendor/CaseSensitive-ID")
	if failure := sendAndWaitForTerminalEvent(t, h, "loom/turn-completed"); failure != "" {
		t.Fatalf("completed Turn error = %q", failure)
	}
	view, err := h.GetAgent("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.LastTurn == nil || view.LastTurn.Status != "completed" || view.LastError != "" {
		t.Fatalf("Agent success projection = %#v", view.Agent)
	}
	if got := countRequestMethod(t, logPath, "turn/interrupt"); got != 0 {
		t.Fatalf("turn/interrupt requests = %d, want 0", got)
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	if turn["model"] != "Vendor/CaseSensitive-ID" {
		t.Fatalf("turn model = %#v, want exact case-sensitive id", turn["model"])
	}
}

func TestCustomProviderModelRouteScopeExcludesManagedProviders(t *testing.T) {
	t.Setenv("CODEX_LOOM_MODEL_CATALOG", "")
	for _, providerID := range []string{"", "openai", deepSeekProviderID} {
		if isCustomProviderModelRouteScope(providerID) {
			t.Fatalf("managed Provider %q entered custom model routing scope", normalizePublicProviderID(providerID))
		}
	}
	if !isCustomProviderModelRouteScope("custom") {
		t.Fatal("custom Provider was excluded from custom model routing scope")
	}
}

func TestModelRouteFailureRequiresExactConfiguredModelReference(t *testing.T) {
	cases := []struct {
		name   string
		detail string
		model  string
		want   bool
	}{
		{name: "route message", detail: "No available channel for model Example-Model under group default", model: "Example-Model", want: true},
		{name: "structured model", detail: `{"code":"model_not_found","model":"Vendor/CaseSensitive-ID"}`, model: "Vendor/CaseSensitive-ID", want: true},
		{name: "different model", detail: "No available channel for model Example-Model-Plus", model: "Example-Model", want: false},
		{name: "code only", detail: `{"code":"model_not_found"}`, model: "model", want: false},
		{name: "ordinary outage", detail: "503 Service Unavailable for model Example-Model", model: "Example-Model", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isModelRouteFailure(tc.detail, tc.model); got != tc.want {
				t.Fatalf("isModelRouteFailure(%q, %q) = %t, want %t", tc.detail, tc.model, got, tc.want)
			}
		})
	}
}

func TestTerminalModelRouteFailureIsClassifiedWithoutRetryNotification(t *testing.T) {
	t.Setenv("CODEX_LOOM_MODEL_CATALOG", "")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", ThreadID: "thread-1", ProviderID: "custom", Model: "Display-Only",
		Status: "running", CurrentTurnID: "turn-1", CurrentTask: "Do work", CreatedAt: now(), UpdatedAt: now(),
	}
	rt := &runtime{
		agentID: "agent-1", approvals: map[string]*approval{},
		activeTurn: &turnState{turnID: "turn-1", task: "Do work", startedAt: time.Now(), stopWatchdog: make(chan struct{})},
	}
	h.onNotification(rt, "turn/completed", json.RawMessage(`{
		"threadId":"thread-1",
		"turn":{"id":"turn-1","status":"failed","error":{"message":"503: No available channel for model Display-Only under group default"}}
	}`))
	view := h.agents["agent-1"]
	if view.LastTurn == nil || view.LastTurn.Status != "failed" || !strings.Contains(view.LastError, `model ID "Display-Only"`) {
		t.Fatalf("terminal model route classification = %#v", view)
	}
}

func TestManagedProvidersAreNotClassifiedAsCustomModelRouteFailures(t *testing.T) {
	t.Setenv("CODEX_LOOM_MODEL_CATALOG", "")
	for _, tc := range []struct {
		name       string
		providerID string
		model      string
	}{
		{name: "builtin OpenAI", providerID: "", model: "gpt-5.6-sol"},
		{name: "managed DeepSeek", providerID: deepSeekProviderID, model: deepSeekModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h := testHub(st)
			h.stopping = true
			h.agents["agent-1"] = &Agent{
				ID: "agent-1", Name: "worker", ThreadID: "thread-1", ProviderID: tc.providerID, Model: tc.model,
				Status: "running", CurrentTurnID: "turn-1", CurrentTask: "Do work", CreatedAt: now(), UpdatedAt: now(),
			}
			turn := &turnState{turnID: "turn-1", task: "Do work", startedAt: time.Now(), stopWatchdog: make(chan struct{})}
			rt := &runtime{agentID: "agent-1", approvals: map[string]*approval{}, activeTurn: turn}
			detail := fmt.Sprintf("503: No available channel for model %s under group default", tc.model)
			h.onNotification(rt, "error", json.RawMessage(fmt.Sprintf(`{
				"threadId":"thread-1","turnId":"turn-1","willRetry":true,"error":{"message":%q}
			}`, detail)))
			if turn.forcedFailure != "" {
				t.Fatalf("managed Provider was classified as custom: %q", turn.forcedFailure)
			}
			h.onNotification(rt, "turn/completed", json.RawMessage(fmt.Sprintf(`{
				"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":%q}}
			}`, detail)))
			if got := h.agents["agent-1"].LastError; got != detail {
				t.Fatalf("managed Provider terminal error = %q, want raw error %q", got, detail)
			}
		})
	}
}

func TestDelayedModelRouteStopDoesNotInterruptSuccessorTurn(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	host, err := h.ensureCodexHost()
	if err != nil {
		t.Fatal(err)
	}
	original := &turnState{
		turnID: "turn-original", task: "Original work", startedAt: time.Now(), lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	successor := &turnState{
		turnID: "turn-successor", task: "Successor work", startedAt: time.Now(), lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	rt := &runtime{
		agentID: "agent-1", client: host.client, hostGeneration: host.generation,
		ready: host.ready, approvals: map[string]*approval{}, activeTurn: original,
	}
	h.mu.Lock()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", ThreadID: "thr-model-route-fence", Status: "running",
		CurrentTurnID: original.turnID, CurrentTask: original.task, CreatedAt: now(), UpdatedAt: now(),
	}
	h.runtimes["agent-1"] = rt
	// The worker is admitted while Hub.mu is held, so it cannot inspect runtime
	// state until the original Turn has ended and its successor is active.
	h.scheduleModelRouteInterruptLocked("agent-1", original, "permanent model route failure")
	original.finished = true
	close(original.stopWatchdog)
	rt.activeTurn = successor
	h.agents["agent-1"].CurrentTurnID = successor.turnID
	h.agents["agent-1"].CurrentTask = successor.task
	h.mu.Unlock()

	h.workers.Wait()
	h.mu.Lock()
	active := rt.activeTurn
	successorFinished := successor.finished
	meta := *h.agents["agent-1"]
	h.mu.Unlock()
	if got := countRequestMethod(t, logPath, "turn/interrupt"); got != 0 {
		t.Fatalf("delayed route stop sent %d interrupt request(s) after successor started, want 0", got)
	}
	if active != successor || successorFinished || meta.Status != "running" || meta.CurrentTurnID != successor.turnID {
		t.Fatalf("successor Turn changed by delayed route stop: active=%p successor=%p finished=%t meta=%#v", active, successor, successorFinished, meta)
	}

	h.mu.Lock()
	if rt.activeTurn == successor && !successor.finished {
		successor.finished = true
		close(successor.stopWatchdog)
		rt.activeTurn = nil
	}
	h.mu.Unlock()
}

func modelRoutingTestHub(t *testing.T, model string) (string, *Hub) {
	t.Helper()
	t.Setenv("CODEX_LOOM_MODEL_CATALOG", "")
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	t.Cleanup(h.Shutdown)
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		ProviderID: "custom", Model: model, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	return logPath, h
}

func sendAndWaitForTerminalEvent(t *testing.T, h *Hub, eventType string) string {
	t.Helper()
	events, cancel, err := h.Subscribe("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := h.SendTask("agent-1", "hello", time.Minute); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Type != eventType {
				continue
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			return payload.Error
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}
