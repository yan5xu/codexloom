package hub

import (
	"encoding/json"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestGoalNotificationProjectsNativeGoalIntoAgentView(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stopping = true // terminal transitions must not launch background delivery in this unit test
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "research", ThreadID: "thread-1", Status: "idle"}
	runtime := &runtime{agentID: "agent-1", approvals: map[string]*approval{}}

	h.onNotification(runtime, "thread/goal/updated", json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":null,
		"goal":{"threadId":"thread-1","objective":"Complete the audit","status":"active","tokenBudget":120000,"tokensUsed":4300,"timeUsedSeconds":92,"createdAt":100,"updatedAt":200}
	}`))

	h.mu.Lock()
	view := h.viewLocked(h.agents["agent-1"])
	reserved := h.activeGoalReservesThreadLocked("agent-1")
	h.mu.Unlock()
	if view.Goal == nil || view.Goal.Objective != "Complete the audit" || view.Goal.Status != GoalStatusActive {
		t.Fatalf("projected Goal = %#v", view.Goal)
	}
	if view.Goal.TokenBudget == nil || *view.Goal.TokenBudget != 120000 || view.Goal.TokensUsed != 4300 || view.Goal.TimeUsedSeconds != 92 {
		t.Fatalf("projected Goal usage = %#v", view.Goal)
	}
	if !reserved {
		t.Fatal("active Goal did not reserve its Thread")
	}

	h.onNotification(runtime, "thread/goal/cleared", json.RawMessage(`{"threadId":"thread-1"}`))
	h.mu.Lock()
	view = h.viewLocked(h.agents["agent-1"])
	reserved = h.activeGoalReservesThreadLocked("agent-1")
	h.mu.Unlock()
	if view.Goal != nil || reserved {
		t.Fatalf("cleared Goal still projected: %#v, reserved=%v", view.Goal, reserved)
	}
}

func TestOnlyActiveGoalReservesThreadButCausalReplyRemainsEligible(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "research", ThreadID: "thread-1", Status: "idle"}
	h.goals["agent-1"] = &ThreadGoal{ThreadID: "thread-1", Objective: "Audit", Status: GoalStatusActive}
	if !h.activeGoalReservesThreadLocked("agent-1") {
		t.Fatal("active Goal did not reserve Thread")
	}
	for _, status := range []string{GoalStatusPaused, GoalStatusBlocked, GoalStatusUsageLimited, GoalStatusBudgetLimited, GoalStatusComplete} {
		h.goals["agent-1"] = &ThreadGoal{ThreadID: "thread-1", Objective: "Audit", Status: status}
		if h.activeGoalReservesThreadLocked("agent-1") {
			t.Fatalf("non-running Goal status %s reserved Thread", status)
		}
	}

	root := &AgentMessage{ID: "root", FromAgentID: "agent-1", ToAgentID: "agent-2", SourceTurnID: "turn-goal"}
	reply := &AgentMessage{ID: "reply", FromAgentID: "agent-2", ToAgentID: "agent-1", ReplyTo: "root"}
	unrelated := &AgentMessage{ID: "unrelated", FromAgentID: "agent-2", ToAgentID: "agent-1"}
	h.comms[root.ID] = root
	if !h.isCausalReplyForAgentLocked(reply, "agent-1") {
		t.Fatal("causal reply was not eligible for unfinished Goal")
	}
	if h.isCausalReplyForAgentLocked(unrelated, "agent-1") {
		t.Fatal("unrelated message was treated as causal Goal input")
	}
}

func TestActiveGoalKeepsExternalInboxQueued(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "research", ThreadID: "thread-1", Status: "idle"}
	h.goals["agent-1"] = &ThreadGoal{ThreadID: "thread-1", Objective: "Audit", Status: GoalStatusActive}
	h.inbox = map[string]*InboxItem{"inbox-1": {ID: "inbox-1", AgentID: "agent-1", State: "queued"}}
	h.inboxOrder = []string{"inbox-1"}

	h.deliverNextInboxForAgent("agent-1")
	if h.inbox["inbox-1"].State != "queued" {
		t.Fatalf("Inbox state = %q, want queued", h.inbox["inbox-1"].State)
	}
}
