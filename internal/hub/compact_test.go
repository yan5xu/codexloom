package hub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestCompactAgentThreadStartsCodexCompaction(t *testing.T) {
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
		CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	result, err := h.CompactAgentThread("agent-1")
	if err != nil {
		t.Fatalf("CompactAgentThread: %v", err)
	}
	if !result.Started || result.ThreadID != "thr-stale" || result.AgentName != "worker" {
		t.Fatalf("compact result = %#v", result)
	}
	compact := lastRequestParams(t, logPath, "thread/compact/start")
	if compact["threadId"] != "thr-stale" {
		t.Fatalf("thread/compact/start params = %#v", compact)
	}
	if got := countRequestMethod(t, logPath, "thread/compact/start"); got != 1 {
		t.Fatalf("thread/compact/start requests = %d, want 1", got)
	}
}

func TestCompactAgentThreadRejectsActiveTurnAndGoal(t *testing.T) {
	installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", ThreadID: "thr-stale", Status: "idle"}

	h.runtimes["agent-1"] = &runtime{agentID: "agent-1", activeTurn: &turnState{finished: false}}
	_, err = h.CompactAgentThread("agent-1")
	if err == nil || !strings.Contains(err.Error(), "active Turn") {
		t.Fatalf("active Turn compact error = %v", err)
	}
	h.runtimes["agent-1"].activeTurn = nil
	h.runtimes["agent-1"].approvals = map[string]*approval{"ap-1": {rpcID: json.RawMessage(`1`)}}
	_, err = h.CompactAgentThread("agent-1")
	if err == nil || !strings.Contains(err.Error(), "pending approval") {
		t.Fatalf("pending approval compact error = %v", err)
	}
	h.runtimes["agent-1"].approvals = nil

	h.goals["agent-1"] = &ThreadGoal{ThreadID: "thr-stale", Status: GoalStatusActive}
	_, err = h.CompactAgentThread("agent-1")
	if err == nil || !strings.Contains(err.Error(), "active Goal") {
		t.Fatalf("active Goal compact error = %v", err)
	}
}
