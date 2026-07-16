package hub

import (
	"testing"
	"time"
)

func TestDrainHoldsQueuedWorkButAllowsCausalReply(t *testing.T) {
	h := communicationTestHub(t)
	root, reply := causalReplyFixture()
	ordinary := &AgentMessage{
		ID: "msg-after-restart", FromAgentID: "agent-a", ToAgentID: "agent-b", From: "alpha", To: "beta",
		Subject: "Start after restart", Body: "This is durable queued work.", Response: "none", Status: "closed",
		DeliveryStatus: "queued", CreatedAt: now(), UpdatedAt: now(),
	}
	h.comms[root.ID], h.comms[reply.ID], h.comms[ordinary.ID] = root, reply, ordinary
	h.commOrder = []string{root.ID, reply.ID, ordinary.ID}

	h.steerTurn = func(threadID, expectedTurnID, input string, timeout time.Duration) (string, error) {
		return expectedTurnID, nil
	}
	h.BeginDrain()

	if delivered, ok := h.deliverNextQueuedForTarget("agent-b", time.Second); ok || delivered != nil {
		t.Fatalf("ordinary work started during drain: %#v, %v", delivered, ok)
	}
	if got := h.comms[ordinary.ID].DeliveryStatus; got != "queued" {
		t.Fatalf("ordinary message state = %q, want queued", got)
	}
	if _, err := h.SendTask("agent-b", "new user Turn", time.Second); err == nil || !isBusyErr(err) {
		t.Fatalf("new Turn during drain error = %v, want conflict", err)
	}

	delivered, ok := h.deliverNextQueuedForTarget("agent-a", time.Second)
	if !ok || delivered == nil || delivered.ID != reply.ID || delivered.DeliveryMode != "turn_steer" {
		t.Fatalf("causal reply during drain = %#v, %v", delivered, ok)
	}
}

func TestDrainHoldsInboxAndConnectorClaims(t *testing.T) {
	h := communicationTestHub(t)
	h.inbox = map[string]*InboxItem{
		"inb-1": {ID: "inb-1", AgentID: "agent-b", State: "queued"},
	}
	h.inboxOrder = []string{"inb-1"}
	h.connections = map[string]*PlatformConnection{
		"conn-1": {ID: "conn-1", Provider: "parall", Enabled: true},
	}
	h.addresses = map[string]*AgentAddress{
		"addr-1": {ID: "addr-1", AgentID: "agent-b", ConnectionID: "conn-1", Enabled: true},
	}
	h.outbox = map[string]*OutboxItem{
		"out-1": {ID: "out-1", AgentID: "agent-b", AddressID: "addr-1", State: "pending"},
	}
	h.outboxOrder = []string{"out-1"}

	h.BeginDrain()
	h.deliverNextInboxForAgent("agent-b")
	if got := h.inbox["inb-1"].State; got != "queued" {
		t.Fatalf("Inbox state during drain = %q, want queued", got)
	}
	command, err := h.ClaimNextOutbox("conn-1")
	if err != nil || command != nil {
		t.Fatalf("Connector claim during drain = %#v, %v", command, err)
	}
	if got := h.outbox["out-1"].State; got != "pending" {
		t.Fatalf("Outbox state during drain = %q, want pending", got)
	}
}
