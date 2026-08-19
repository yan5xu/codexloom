package hub

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

type recordedApprovalResponse struct {
	RPCID    string
	Decision string
}

type recordingApprovalResponder struct {
	mu        sync.Mutex
	err       error
	responses []recordedApprovalResponse
}

func (r *recordingApprovalResponder) Respond(id json.RawMessage, result any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	decision, _ := result.(map[string]any)["decision"].(string)
	r.responses = append(r.responses, recordedApprovalResponse{RPCID: string(id), Decision: decision})
	return r.err
}

func (r *recordingApprovalResponder) setError(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *recordingApprovalResponder) snapshot() []recordedApprovalResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedApprovalResponse(nil), r.responses...)
}

func TestFinishTurnAnswersOutstandingApprovals(t *testing.T) {
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

	turn := &turnState{
		turnID: "turn-approval", task: "needs approval", source: "owner",
		startedAt: time.Now(), lastActivity: time.Now(), stopWatchdog: make(chan struct{}),
	}
	rt := &runtime{
		agentID: "agent-approval", client: host.client, hostGeneration: host.generation,
		ready: host.ready, approvals: map[string]*approval{}, activeTurn: turn,
	}
	h.mu.Lock()
	h.agents[rt.agentID] = &Agent{
		ID: rt.agentID, Name: "approval-agent", ThreadID: "thread-approval", Status: "running",
		CurrentTurnID: turn.turnID, CurrentTask: turn.task, CreatedAt: now(), UpdatedAt: now(),
	}
	h.runtimes[rt.agentID] = rt
	h.mu.Unlock()

	h.onServerRequest(rt, json.RawMessage(`9001`), "item/commandExecution/requestApproval", json.RawMessage(`{"command":"true"}`))
	h.onServerRequest(rt, json.RawMessage(`9002`), "item/fileChange/requestApproval", json.RawMessage(`{"path":"fixture.txt"}`))
	h.onNotification(rt, "turn/completed", json.RawMessage(`{
		"threadId":"thread-approval","turn":{"id":"turn-approval","status":"completed"}
	}`))

	waitForApprovalDecision(t, logPath, "9001", "cancel")
	waitForApprovalDecision(t, logPath, "9002", "cancel")
	events, err := st.ReadEvents(rt.agentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	resolved := map[string]approvalResolvedEvent{}
	for _, event := range events {
		if event.Type != "loom/approval-resolved" {
			continue
		}
		var payload approvalResolvedEvent
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		resolved[payload.ApprovalID] = payload
	}
	want := map[string]approvalResolvedEvent{
		"ap-9001": {ApprovalID: "ap-9001", Decision: "cancel", Method: "item/commandExecution/requestApproval"},
		"ap-9002": {ApprovalID: "ap-9002", Decision: "cancel", Method: "item/fileChange/requestApproval"},
	}
	if len(resolved) != len(want) {
		t.Fatalf("approval-resolved events = %#v, want %#v", resolved, want)
	}
	for approvalID, expected := range want {
		if resolved[approvalID] != expected {
			t.Fatalf("approval-resolved %s = %#v, want %#v", approvalID, resolved[approvalID], expected)
		}
	}
}

func TestResolveApprovalRespondsOnceAndEmitsMatchingEvent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true
	responder := &recordingApprovalResponder{}
	rt := &runtime{
		agentID: "agent-normal", approvalResponder: responder,
		approvals: map[string]*approval{
			"ap-41": {rpcID: json.RawMessage(`41`), method: "item/fileChange/requestApproval", ts: now()},
		},
		activeTurn: &turnState{turnID: "turn-normal", stopWatchdog: make(chan struct{})},
	}
	h.agents[rt.agentID] = &Agent{ID: rt.agentID, Name: "normal", Status: "running"}
	h.runtimes[rt.agentID] = rt

	result, err := h.ResolveApproval("normal", "ap-41", "approve")
	if err != nil {
		t.Fatal(err)
	}
	if result["approvalId"] != "ap-41" || result["decision"] != "accept" {
		t.Fatalf("ResolveApproval result = %#v", result)
	}
	assertApprovalResponses(t, responder.snapshot(), []recordedApprovalResponse{{RPCID: "41", Decision: "accept"}})
	assertApprovalResolvedEvents(t, st, rt.agentID, []approvalResolvedEvent{{
		ApprovalID: "ap-41", Decision: "accept", Method: "item/fileChange/requestApproval",
	}})

	if _, err := h.ResolveApproval("normal", "ap-41", "approve"); err == nil {
		t.Fatal("duplicate ResolveApproval succeeded")
	} else {
		var hubErr *HubError
		if !errors.As(err, &hubErr) || hubErr.Status != 404 {
			t.Fatalf("duplicate ResolveApproval error = %v, want 404", err)
		}
	}
	assertApprovalResponses(t, responder.snapshot(), []recordedApprovalResponse{{RPCID: "41", Decision: "accept"}})
}

func TestFinishTurnCancelsApprovalAcrossTerminalStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status string
		errMsg string
	}{
		{name: "error", status: "failed", errMsg: "model failed"},
		{name: "timeout", status: "interrupted", errMsg: "inactivity timeout (1s)"},
		{name: "cancel", status: "interrupted", errMsg: "cancelled by owner"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h := testHub(st)
			responder := &recordingApprovalResponder{}
			agentID := "agent-" + test.name
			turn := &turnState{
				turnID: "turn-" + test.name, task: test.name, startedAt: time.Now(), lastActivity: time.Now(),
				stopWatchdog: make(chan struct{}),
			}
			rt := &runtime{
				agentID: agentID, approvalResponder: responder, activeTurn: turn,
				approvals: map[string]*approval{
					"ap-7": {rpcID: json.RawMessage(`7`), method: "item/commandExecution/requestApproval", ts: now()},
				},
			}
			h.agents[agentID] = &Agent{
				ID: agentID, Name: test.name, Status: "running", CurrentTurnID: turn.turnID,
				CurrentTask: turn.task, CreatedAt: now(), UpdatedAt: now(),
			}
			h.runtimes[agentID] = rt

			h.mu.Lock()
			h.finishTurnLocked(h.agents[agentID], rt, test.status, test.errMsg)
			h.finishTurnLocked(h.agents[agentID], rt, test.status, test.errMsg)
			// The cancellation worker is already admitted. Suppress unrelated
			// queue workers when it commits the last approval in this unit test.
			h.stopping = true
			h.mu.Unlock()
			h.workers.Wait()

			assertApprovalResponses(t, responder.snapshot(), []recordedApprovalResponse{{RPCID: "7", Decision: "cancel"}})
			assertApprovalResolvedEvents(t, st, agentID, []approvalResolvedEvent{{
				ApprovalID: "ap-7", Decision: "cancel", Method: "item/commandExecution/requestApproval",
			}})
			if len(rt.approvals) != 0 {
				t.Fatalf("pending approvals after %s finish = %#v", test.name, rt.approvals)
			}
			if meta := h.agents[agentID]; meta.Status != "idle" || meta.LastTurn == nil || meta.LastTurn.Status != test.status {
				t.Fatalf("Agent after %s finish = %#v", test.name, meta)
			}
		})
	}
}

func TestFinishTurnApprovalResponseFailureRemainsPendingAndRetryable(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	responder := &recordingApprovalResponder{err: errors.New("forced response failure")}
	turn := &turnState{
		turnID: "turn-failure", task: "fails", startedAt: time.Now(), lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	rt := &runtime{
		agentID: "agent-failure", approvalResponder: responder, activeTurn: turn,
		approvals: map[string]*approval{
			"ap-99": {rpcID: json.RawMessage(`99`), method: "item/commandExecution/requestApproval", ts: now()},
		},
	}
	h.agents[rt.agentID] = &Agent{
		ID: rt.agentID, Name: "failure", Status: "running", CurrentTurnID: turn.turnID,
		CurrentTask: turn.task, CreatedAt: now(), UpdatedAt: now(),
	}
	h.runtimes[rt.agentID] = rt

	h.mu.Lock()
	h.finishTurnLocked(h.agents[rt.agentID], rt, "failed", "turn failed")
	h.stopping = true
	h.mu.Unlock()
	h.workers.Wait()

	if pending := rt.approvals["ap-99"]; pending == nil || pending.resolving {
		t.Fatalf("failed response did not restore retryable approval: %#v", pending)
	}
	assertApprovalResponses(t, responder.snapshot(), []recordedApprovalResponse{{RPCID: "99", Decision: "cancel"}})
	assertApprovalResolvedEvents(t, st, rt.agentID, nil)

	responder.setError(nil)
	result, err := h.ResolveApproval("failure", "ap-99", "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if result["approvalId"] != "ap-99" || result["decision"] != "cancel" || len(rt.approvals) != 0 {
		t.Fatalf("retry result = %#v, approvals = %#v", result, rt.approvals)
	}
	assertApprovalResponses(t, responder.snapshot(), []recordedApprovalResponse{
		{RPCID: "99", Decision: "cancel"},
		{RPCID: "99", Decision: "cancel"},
	})
	assertApprovalResolvedEvents(t, st, rt.agentID, []approvalResolvedEvent{{
		ApprovalID: "ap-99", Decision: "cancel", Method: "item/commandExecution/requestApproval",
	}})
}

type approvalResolvedEvent struct {
	ApprovalID string `json:"approvalId"`
	Decision   string `json:"decision"`
	Method     string `json:"method"`
}

func assertApprovalResolvedEvents(t *testing.T, st *store.Store, agentID string, want []approvalResolvedEvent) {
	t.Helper()
	events, err := st.ReadEvents(agentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]approvalResolvedEvent, 0, len(want))
	for _, event := range events {
		if event.Type != "loom/approval-resolved" {
			continue
		}
		var payload approvalResolvedEvent
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, payload)
	}
	if len(got) != len(want) {
		t.Fatalf("approval-resolved events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("approval-resolved event %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func assertApprovalResponses(t *testing.T, got, want []recordedApprovalResponse) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("approval responses = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("approval response %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func waitForApprovalDecision(t *testing.T, path, rpcID, decision string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if approvalDecisionLogged(t, path, rpcID, decision) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval rpc %s never received decision %q", rpcID, decision)
}

func approvalDecisionLogged(t *testing.T, path, rpcID, decision string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var response struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result struct {
				Decision string `json:"decision"`
			} `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &response); err == nil && response.Method == "" && string(response.ID) == rpcID && response.Result.Decision == decision {
			return true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}
