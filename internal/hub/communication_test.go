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

func TestSendAgentMessageDoesNotPublishWhenDurableAppendFails(t *testing.T) {
	h := communicationTestHub(t)
	ledger := filepath.Join(h.st.Dir(), "comms.ndjson")
	if err := os.Mkdir(ledger, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := h.SendAgentMessage(CommParams{
		From: "alpha", To: "beta", Subject: "Must persist", Body: "Do not publish this on failure.", Response: "required",
	})
	if err == nil {
		t.Fatal("message send succeeded after durable append failure")
	}
	if messages := h.ListComms("", ""); len(messages) != 0 {
		t.Fatalf("uncommitted message reached the projection: %#v", messages)
	}
}

func TestAgentMessageCapturesSourceTurn(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-a"] = &Agent{ID: "agent-a", Name: "alpha", Status: "running"}
	h.agents["agent-b"] = &Agent{ID: "agent-b", Name: "beta", Status: "running"}
	h.runtimes["agent-a"] = &runtime{activeTurn: &turnState{turnID: "turn-alpha"}}

	result, err := h.SendAgentMessage(CommParams{
		From: "alpha", To: "beta", Subject: "Need context", Body: "Please investigate.", Response: "required",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.SourceTurnID != "turn-alpha" || result.Message.DeliveryStatus != "queued" {
		t.Fatalf("message = %#v, want source Turn and queued delivery", result.Message)
	}
}

func TestAgentMessageEnvelopeCarriesSentAndCurrentTime(t *testing.T) {
	msg := &AgentMessage{
		ID: "msg-time", From: "alpha", To: "beta", Subject: "Timeline", Body: "Check ordering.",
		Response: "required", Status: "open", CreatedAt: "2026-07-14T08:30:00+08:00",
	}
	envelope := formatAgentEnvelopeAt(msg, "2026-07-15T01:45:00+08:00")
	want := `<timing sent_at="2026-07-14T08:30:00+08:00" current_time="2026-07-15T01:45:00+08:00" />`
	if !strings.Contains(envelope, want) {
		t.Fatalf("timing missing from agent envelope:\n%s", envelope)
	}
}

func TestReplySteersExactSourceTurnAndBypassesOrdinaryQueue(t *testing.T) {
	h := communicationTestHub(t)
	root, reply := causalReplyFixture()
	ordinary := &AgentMessage{
		ID: "msg-ordinary", FromAgentID: "agent-c", ToAgentID: "agent-a", From: "gamma", To: "alpha",
		Subject: "Unrelated", Body: "Start something else.", Response: "none", Status: "closed",
		DeliveryStatus: "queued", CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[root.ID], h.comms[ordinary.ID], h.comms[reply.ID] = root, ordinary, reply
	h.commOrder = []string{root.ID, ordinary.ID, reply.ID}

	var steeredInput string
	h.steerTurn = func(threadID, expectedTurnID, input string, timeout time.Duration) (string, error) {
		if threadID != "thread-alpha" || expectedTurnID != "turn-alpha" {
			t.Fatalf("turn/steer target = %s %s", threadID, expectedTurnID)
		}
		steeredInput = input
		return expectedTurnID, nil
	}

	delivered, ok := h.tryDeliverReplyToActiveTurn("agent-a", time.Second)
	if !ok || delivered == nil || delivered.ID != reply.ID {
		t.Fatalf("delivery = %#v, %v", delivered, ok)
	}
	if delivered.DeliveryMode != "turn_steer" || delivered.DeliveredTurnID != "turn-alpha" {
		t.Fatalf("delivery metadata = %#v", delivered)
	}
	if !strings.Contains(steeredInput, `<reply_to>msg-root</reply_to>`) || !strings.Contains(steeredInput, "Here is the result.") {
		t.Fatalf("steered envelope = %s", steeredInput)
	}
	if h.comms[root.ID].Status != "answered" || h.comms[ordinary.ID].DeliveryStatus != "queued" {
		t.Fatalf("root=%#v ordinary=%#v", h.comms[root.ID], h.comms[ordinary.ID])
	}
}

func TestSteerFailureLeavesReplyQueued(t *testing.T) {
	h := communicationTestHub(t)
	root, reply := causalReplyFixture()
	h.comms[root.ID], h.comms[reply.ID] = root, reply
	h.commOrder = []string{root.ID, reply.ID}
	h.steerTurn = func(string, string, string, time.Duration) (string, error) {
		return "", errors.New("active turn changed")
	}

	result, ok := h.tryDeliverReplyToActiveTurn("agent-a", time.Second)
	if ok || result == nil || result.DeliveryStatus != "queued" {
		t.Fatalf("delivery = %#v, %v", result, ok)
	}
	if result.DeliveryMode != "" || !strings.Contains(result.LastDeliveryError, "mid-turn delivery deferred") {
		t.Fatalf("fallback metadata = %#v", result)
	}
	if h.comms[root.ID].Status != "open" {
		t.Fatalf("root was answered before delivery: %#v", h.comms[root.ID])
	}
}

func TestOnlyReplyForCurrentSourceTurnCanSteer(t *testing.T) {
	h := communicationTestHub(t)
	root, reply := causalReplyFixture()
	root.SourceTurnID = "turn-older"
	h.comms[root.ID], h.comms[reply.ID] = root, reply
	h.commOrder = []string{root.ID, reply.ID}
	called := false
	h.steerTurn = func(string, string, string, time.Duration) (string, error) {
		called = true
		return "turn-alpha", nil
	}

	result, ok := h.tryDeliverReplyToActiveTurn("agent-a", time.Second)
	if ok || result != nil || called || h.comms[reply.ID].DeliveryStatus != "queued" {
		t.Fatalf("unrelated reply was steered: result=%#v ok=%v called=%v", result, ok, called)
	}
}

func TestInterruptedAgentMessageTurnIsHeldWithoutRedelivery(t *testing.T) {
	h := communicationTestHub(t)
	msg := &AgentMessage{
		ID: "msg-interrupted", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Continue this work", Response: "required", Status: "open", DeliveryStatus: "delivered",
		DeliveryMode: "turn_start", DeliveredTurnID: "turn-beta", DeliveredAgentID: "agent-b",
		DeliveredSessionID: "agent-b", DeliveredAt: now(), CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}

	h.finishAgentMessageTurnLocked(&turnState{turnID: "turn-beta", agentMessageID: msg.ID}, "interrupted", "stopped for another turn")

	got := h.comms[msg.ID]
	if got.DeliveryStatus != "delivered" || got.DeliveredTurnID != "turn-beta" || got.DeliveryMode != "turn_start" {
		t.Fatalf("interrupted delivery changed = %#v", got)
	}
	if got.HandlingStatus != "interrupted" || got.LastHandlingError != "stopped for another turn" || got.Status != "open" {
		t.Fatalf("held request metadata = %#v", got)
	}
	if len(got.HandlingAttempts) != 1 || got.HandlingAttempts[0].Status != "interrupted" || got.HandlingAttempts[0].TurnID != "turn-beta" {
		t.Fatalf("handling attempts = %#v", got.HandlingAttempts)
	}
	if targets := h.queuedTargets(); len(targets) != 0 {
		t.Fatalf("interrupted request was queued for redelivery: %v", targets)
	}
}

func TestFailedAgentMessageTurnRequiresExplicitRetry(t *testing.T) {
	h := communicationTestHub(t)
	msg := &AgentMessage{
		ID: "msg-failed-turn", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Continue this work", Response: "required", Status: "open", DeliveryStatus: "delivered",
		DeliveryMode: "turn_start", DeliveredTurnID: "turn-beta", CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}

	h.finishAgentMessageTurnLocked(&turnState{turnID: "turn-beta", agentMessageID: msg.ID}, "failed", "model error")

	got := h.comms[msg.ID]
	if got.DeliveryStatus != "delivered" || got.HandlingStatus != "failed" || got.LastHandlingError != "model error" || got.Status != "open" {
		t.Fatalf("failed message = %#v", got)
	}
	if len(got.HandlingAttempts) != 1 || got.HandlingAttempts[0].Status != "failed" {
		t.Fatalf("failed handling attempts = %#v", got.HandlingAttempts)
	}
}

func TestRetryAgentMessagePreservesIdentityAndReplyChain(t *testing.T) {
	h := communicationTestHub(t)
	h.agents["agent-b"].Status = "running" // Keep the async dispatcher from consuming the queue.
	msg := &AgentMessage{
		ID: "msg-stale", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Continue this work", Response: "required", Status: "open", DeliveryStatus: "delivered",
		DeliveryMode: "turn_start", DeliveredTurnID: "turn-old", DeliveredAt: now(), CreatedAt: now(), UpdatedAt: now(),
		HandlingStatus: "interrupted", LastHandlingError: "stopped",
		HandlingAttempts: []AgentMessageHandlingAttempt{{ID: "matt-old", TurnID: "turn-old", Status: "interrupted", StartedAt: now(), CompletedAt: now(), Error: "stopped"}},
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}

	retried, err := h.RetryAgentMessage(msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != msg.ID || retried.DeliveryStatus != "queued" || retried.DeliveredTurnID != "" || retried.Status != "open" {
		t.Fatalf("retried message = %#v", retried)
	}
	if retried.HandlingStatus != "pending" || retried.LastHandlingError != "" || len(retried.HandlingAttempts) != 1 {
		t.Fatalf("retry lost or failed to reset handling history = %#v", retried)
	}
}

func TestStartedAgentMessageCreatesHandlingAttempt(t *testing.T) {
	h := communicationTestHub(t)
	msg := &AgentMessage{
		ID: "msg-start", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Start", Response: "required", Status: "open", DeliveryStatus: "delivering", HandlingStatus: "pending",
		CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}
	turn := &turnState{turnID: "turn-beta", agentMessageID: msg.ID}

	if err := h.markAgentMessageHandlingRunningLocked(turn, "agent-b"); err != nil {
		t.Fatal(err)
	}
	got := h.comms[msg.ID]
	if got.DeliveryStatus != "delivered" || got.HandlingStatus != "running" || got.ActiveHandlingID == "" {
		t.Fatalf("started handling = %#v", got)
	}
	if len(got.HandlingAttempts) != 1 || got.HandlingAttempts[0].TurnID != "turn-beta" || got.HandlingAttempts[0].Status != "running" {
		t.Fatalf("started attempt = %#v", got.HandlingAttempts)
	}
}

func TestLegacyInterruptedRedeliveryIsNormalizedAsHeld(t *testing.T) {
	msg := AgentMessage{
		ID: "msg-loop", Response: "required", Status: "open", DeliveryStatus: "queued",
		LastDeliveryError: "delivery Turn interrupted; queued for redelivery", UpdatedAt: now(),
	}
	if repaired := normalizeAgentMessage(&msg); !repaired {
		t.Fatal("legacy redelivery loop was not repaired")
	}
	if msg.DeliveryStatus != "delivered" || msg.HandlingStatus != "interrupted" || msg.LastHandlingError == "" || msg.LastDeliveryError != "" {
		t.Fatalf("normalized message = %#v", msg)
	}
}

func TestCancelledOpenMessageIsRepairedAsClosed(t *testing.T) {
	msg := AgentMessage{
		ID: "msg-cancelled", From: "alpha", Response: "required", Status: "open",
		DeliveryStatus: "cancelled", UpdatedAt: "2026-07-14T00:00:00Z",
	}
	if repaired := normalizeAgentMessage(&msg); !repaired {
		t.Fatal("legacy cancelled message was not repaired")
	}
	if msg.Status != "closed" || msg.Resolution != "cancelled" || msg.ResolvedBy != "alpha" || msg.ResolvedAt != msg.UpdatedAt {
		t.Fatalf("repaired message = %#v", msg)
	}
}

func TestLoadCommsDoesNotAppendRepairOverNewerResolvedRecord(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := AgentMessage{
		ID: "msg-history", From: "alpha", Response: "required", Status: "open",
		DeliveryStatus: "cancelled", UpdatedAt: "2026-07-14T00:00:00Z",
	}
	resolved := legacy
	resolved.Status = "answered"
	resolved.Resolution = "reply"
	resolved.ResolvedBy = "beta"
	resolved.ResolvedAt = "2026-07-14T00:01:00Z"
	resolved.UpdatedAt = resolved.ResolvedAt
	if err := st.AppendComm(commRecord{Message: legacy}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendComm(commRecord{Message: resolved}); err != nil {
		t.Fatal(err)
	}

	h := testHub(st)
	if err := h.loadComms(); err != nil {
		t.Fatal(err)
	}
	got := h.comms[legacy.ID]
	if got.Status != "answered" || got.Resolution != "reply" {
		t.Fatalf("latest message was overwritten by migration: %#v", got)
	}
	count := 0
	if err := st.ReadComms(func(json.RawMessage) { count++ }); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration appended %d records, want none", count-2)
	}
}

func TestCancelAgentMessageClosesRequiredRequest(t *testing.T) {
	h := communicationTestHub(t)
	msg := &AgentMessage{
		ID: "msg-cancel", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "No longer needed", Response: "required", Status: "open", DeliveryStatus: "queued",
		CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}

	closed, err := h.CancelAgentMessage(msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.Resolution != "cancelled" || closed.DeliveryStatus != "cancelled" || closed.ResolvedBy != "alpha" || closed.ResolvedAt == "" {
		t.Fatalf("cancelled message = %#v", closed)
	}
	if _, err := h.CancelAgentMessage(msg.ID); err != nil {
		t.Fatalf("cancel is not idempotent: %v", err)
	}
}

func TestResolveAgentMessageRequiresSenderAndReason(t *testing.T) {
	h := communicationTestHub(t)
	msg := &AgentMessage{
		ID: "msg-resolve", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Legacy request", Response: "required", Status: "open", DeliveryStatus: "delivered",
		CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[msg.ID] = msg
	h.commOrder = []string{msg.ID}

	if _, err := h.ResolveAgentMessage(msg.ID, ResolveAgentMessageParams{From: "beta", Resolution: "superseded", Reason: "A newer request replaced it."}); err == nil {
		t.Fatal("recipient resolved the sender's request")
	}
	if _, err := h.ResolveAgentMessage(msg.ID, ResolveAgentMessageParams{From: "alpha", Resolution: "superseded"}); err == nil {
		t.Fatal("resolution without a reason was accepted")
	}
	closed, err := h.ResolveAgentMessage(msg.ID, ResolveAgentMessageParams{
		From: "alpha", Resolution: "completed_elsewhere", Reason: "The result was delivered in a separate audited message.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.Resolution != "completed_elsewhere" || closed.ResolutionReason == "" || closed.ResolvedBy != "alpha" || closed.DeliveryStatus != "delivered" {
		t.Fatalf("resolved message = %#v", closed)
	}
	if _, err := h.ResolveAgentMessage(msg.ID, ResolveAgentMessageParams{From: "alpha", Resolution: "completed_elsewhere", Reason: "retry"}); err != nil {
		t.Fatalf("resolve is not idempotent: %v", err)
	}
}

func communicationTestHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-a"] = &Agent{ID: "agent-a", Name: "alpha", ThreadID: "thread-alpha", Status: "running"}
	h.agents["agent-b"] = &Agent{ID: "agent-b", Name: "beta", ThreadID: "thread-beta", Status: "idle"}
	h.agents["agent-c"] = &Agent{ID: "agent-c", Name: "gamma", ThreadID: "thread-gamma", Status: "idle"}
	h.runtimes["agent-a"] = &runtime{activeTurn: &turnState{turnID: "turn-alpha"}}
	return h
}

func causalReplyFixture() (*AgentMessage, *AgentMessage) {
	root := &AgentMessage{
		ID: "msg-root", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Need context", Body: "Please investigate.", Response: "required", Status: "open",
		DeliveryStatus: "delivered", SourceTurnID: "turn-alpha", DeliveredTurnID: "turn-beta",
		CreatedAt: now(), UpdatedAt: now(),
	}
	reply := &AgentMessage{
		ID: "msg-reply", FromAgentID: "agent-b", ToAgentID: "agent-a", From: "beta", To: "alpha",
		Subject: "Re: Need context", Body: "Here is the result.", Response: "none", ReplyTo: root.ID,
		Status: "closed", Resolution: "reply", DeliveryStatus: "queued", CreatedAt: now(), UpdatedAt: now(),
	}
	return root, reply
}
