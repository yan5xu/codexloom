package hub

import (
	"strings"
	"time"
)

// ThreadCompactResult is the product-facing acknowledgement returned after a
// manual Codex compaction request has been accepted by the shared app-server.
type ThreadCompactResult struct {
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	ThreadID  string `json:"threadId"`
	Started   bool   `json:"started"`
}

// CompactAgentThread asks Codex to compact one Agent's primary Thread. The
// operation is intentionally manual and bounded: an active Turn or active Goal
// owns the next model request, so compaction is rejected until that work is
// paused or completed. Existing epoch coverage will re-deliver Loom durable
// context on the next Turn after Codex writes the compaction marker.
func (h *Hub) CompactAgentThread(key string) (ThreadCompactResult, error) {
	h.mu.Lock()
	if h.providerSwitching {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(409, "Codex compaction is paused during an Agent Provider switch")
	}
	if h.stopping {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(503, "CodexLoom is shutting down")
	}
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(404, "agent not found: %s", key)
	}
	if agent.Status == "running" {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(409, "agent %q is running; stop the active Turn before compacting", agent.Name)
	}
	if rt := h.runtimes[agent.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(409, "agent %q has an active Turn; stop it before compacting", agent.Name)
	}
	if rt := h.runtimes[agent.ID]; rt != nil && len(rt.approvals) > 0 {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(409, "agent %q has a pending approval; resolve it before compacting", agent.Name)
	}
	if goal := h.goals[agent.ID]; goal != nil && goal.Status == GoalStatusActive {
		h.mu.Unlock()
		return ThreadCompactResult{}, errf(409, "agent %q has an active Goal; pause it before compacting", agent.Name)
	}
	agentID, agentName := agent.ID, agent.Name
	threadID, sandbox, cwd := strings.TrimSpace(agent.ThreadID), agent.Sandbox, agent.Cwd
	providerID, model := effectiveProviderBinding(agent)
	disabledSkillPaths := h.disabledSkillPathsLocked(agent.ID)
	h.mu.Unlock()

	if threadID == "" {
		return ThreadCompactResult{}, errf(409, "agent has no Codex Thread binding")
	}
	host, err := h.ensureCodexHost()
	if err != nil {
		return ThreadCompactResult{}, err
	}
	if err := resumeThread(host.client, threadID, sandbox, cwd, providerID, model, disabledSkillPaths); err != nil {
		return ThreadCompactResult{}, errf(500, "resume Thread before compaction: %s", err)
	}
	if _, err := host.client.Request("thread/compact/start", map[string]any{"threadId": threadID}, 60*time.Second); err != nil {
		return ThreadCompactResult{}, errf(500, "start Codex compaction: %s", err)
	}

	h.mu.Lock()
	if agent := h.agents[agentID]; agent != nil {
		h.emitLocked(agentID, "loom/agent-compacted", map[string]any{
			"agentId": agentID, "agentName": agentName, "threadId": threadID,
		})
	}
	h.mu.Unlock()
	return ThreadCompactResult{AgentID: agentID, AgentName: agentName, ThreadID: threadID, Started: true}, nil
}
