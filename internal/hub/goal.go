package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

const (
	GoalStatusActive        = "active"
	GoalStatusPaused        = "paused"
	GoalStatusBlocked       = "blocked"
	GoalStatusUsageLimited  = "usageLimited"
	GoalStatusBudgetLimited = "budgetLimited"
	GoalStatusComplete      = "complete"
)

// ThreadGoal is Codex's native persisted Goal projected into Loom. Loom never
// persists a second copy; this value is hydrated from thread/goal/get and kept
// current by thread/goal/updated and thread/goal/cleared notifications.
type ThreadGoal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int64 `json:"tokenBudget"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type GoalUpdateParams struct {
	Objective        *string `json:"objective"`
	Status           *string `json:"status"`
	TokenBudget      *int64  `json:"tokenBudget"`
	ClearTokenBudget bool    `json:"clearTokenBudget"`
}

type threadGoalGetResponse struct {
	Goal *ThreadGoal `json:"goal"`
}

type threadGoalSetResponse struct {
	Goal ThreadGoal `json:"goal"`
}

func validGoalStatus(status string) bool {
	switch status {
	case GoalStatusActive, GoalStatusPaused, GoalStatusBlocked, GoalStatusUsageLimited, GoalStatusBudgetLimited, GoalStatusComplete:
		return true
	default:
		return false
	}
}

// activeGoalReservesThreadLocked reports whether automatic Goal continuation
// currently owns the next Turn. A paused, blocked, or limited Goal remains
// durable and visible, but it must not starve the Agent's Inbox.
func (h *Hub) activeGoalReservesThreadLocked(agentID string) bool {
	goal := h.goals[agentID]
	return goal != nil && goal.Status == GoalStatusActive
}

// ActiveGoalAgentIDs returns the stable identities whose native Codex Goals
// currently own automatic continuation. It is used by graceful restart to
// stop at a Turn boundary instead of waiting forever for an active Goal.
func (h *Hub) ActiveGoalAgentIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0)
	for agentID, goal := range h.goals {
		if goal != nil && goal.Status == GoalStatusActive {
			ids = append(ids, agentID)
		}
	}
	sort.Strings(ids)
	return ids
}

// PauseGoalsForRestart pauses only Goals that are still active when each
// update reaches Codex. The active Turn is not interrupted; changing the Goal
// status only prevents Codex from immediately starting its next continuation.
func (h *Hub) PauseGoalsForRestart(agentIDs []string) ([]string, error) {
	paused := make([]string, 0, len(agentIDs))
	for _, agentID := range uniqueSortedStrings(agentIDs) {
		goal, err := h.GetGoal(agentID)
		if err != nil {
			return paused, fmt.Errorf("read Goal for %s: %w", agentID, err)
		}
		if goal == nil || goal.Status != GoalStatusActive {
			continue
		}
		status := GoalStatusPaused
		updated, err := h.UpdateGoal(agentID, GoalUpdateParams{Status: &status})
		if err != nil {
			return paused, fmt.Errorf("pause Goal for %s: %w", agentID, err)
		}
		if updated == nil || updated.Status != GoalStatusPaused {
			return paused, fmt.Errorf("pause Goal for %s returned status %q", agentID, goalStatus(updated))
		}
		paused = append(paused, agentID)
	}
	return paused, nil
}

// ResumeGoalsAfterRestart resumes only Goals that remain paused. A Goal that
// completed, was cleared, or was otherwise changed while its final Turn
// drained is left untouched. Repeating this operation is therefore safe.
func (h *Hub) ResumeGoalsAfterRestart(agentIDs []string) error {
	var errs []error
	for _, agentID := range uniqueSortedStrings(agentIDs) {
		goal, err := h.GetGoal(agentID)
		if err != nil {
			var hubErr *HubError
			if errors.As(err, &hubErr) && hubErr.Status == 404 {
				continue
			}
			errs = append(errs, fmt.Errorf("read Goal for %s: %w", agentID, err))
			continue
		}
		if goal == nil || goal.Status != GoalStatusPaused {
			continue
		}
		status := GoalStatusActive
		updated, err := h.UpdateGoal(agentID, GoalUpdateParams{Status: &status})
		if err != nil {
			errs = append(errs, fmt.Errorf("resume Goal for %s: %w", agentID, err))
			continue
		}
		if updated == nil || updated.Status != GoalStatusActive {
			errs = append(errs, fmt.Errorf("resume Goal for %s returned status %q", agentID, goalStatus(updated)))
		}
	}
	return errors.Join(errs...)
}

func goalStatus(goal *ThreadGoal) string {
	if goal == nil {
		return ""
	}
	return goal.Status
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (h *Hub) applyGoalLocked(agentID string, goal *ThreadGoal, emit bool) {
	if h.goals == nil {
		h.goals = map[string]*ThreadGoal{}
	}
	if goal == nil {
		delete(h.goals, agentID)
	} else {
		copy := *goal
		h.goals[agentID] = &copy
	}
	if !emit {
		return
	}
	if agent := h.agents[agentID]; agent != nil {
		h.emitStatusLocked(agent, agent.Status)
	}
}

func (h *Hub) hydrateGoals(host *codexHostRuntime) {
	type target struct {
		agentID  string
		threadID string
		sandbox  string
		cwd      string
	}
	h.mu.Lock()
	targets := make([]target, 0, len(h.agents))
	for _, agent := range h.agents {
		if strings.TrimSpace(agent.ThreadID) == "" {
			continue
		}
		targets = append(targets, target{agentID: agent.ID, threadID: agent.ThreadID, sandbox: agent.Sandbox, cwd: agent.Cwd})
	}
	h.mu.Unlock()

	active := make([]target, 0)
	for _, target := range targets {
		raw, err := host.client.Request("thread/goal/get", map[string]any{"threadId": target.threadID}, 15*time.Second)
		if err != nil {
			log.Printf("[codex-loom] hydrate Goal for %s: %v", target.threadID, err)
			continue
		}
		var response threadGoalGetResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			log.Printf("[codex-loom] decode Goal for %s: %v", target.threadID, err)
			continue
		}
		h.mu.Lock()
		h.applyGoalLocked(target.agentID, response.Goal, false)
		if response.Goal != nil {
			if agent := h.agents[target.agentID]; agent != nil {
				h.emitStatusLocked(agent, agent.Status)
			}
		}
		h.mu.Unlock()
		if response.Goal != nil && response.Goal.Status == GoalStatusActive {
			active = append(active, target)
		}
	}

	// Resuming an active Goal hands continuation back to Codex. Paused,
	// blocked, limited, and complete Goals remain visible without starting work.
	for _, target := range active {
		if err := resumeThread(host.client, target.threadID, target.sandbox, target.cwd); err != nil {
			log.Printf("[codex-loom] resume active Goal for %s: %v", target.threadID, err)
		}
	}
}

func (h *Hub) GetGoal(key string) (*ThreadGoal, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	agentID, threadID := agent.ID, agent.ThreadID
	h.mu.Unlock()
	if strings.TrimSpace(threadID) == "" {
		return nil, errf(409, "agent has no Codex Thread binding")
	}
	host, err := h.ensureCodexHost()
	if err != nil {
		return nil, err
	}
	raw, err := host.client.Request("thread/goal/get", map[string]any{"threadId": threadID}, 15*time.Second)
	if err != nil {
		return nil, errf(500, "read Codex Goal: %s", err)
	}
	var response threadGoalGetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, errf(500, "decode Codex Goal: %s", err)
	}
	h.mu.Lock()
	h.applyGoalLocked(agentID, response.Goal, false)
	h.mu.Unlock()
	return cloneGoal(response.Goal), nil
}

func (h *Hub) UpdateGoal(key string, update GoalUpdateParams) (*ThreadGoal, error) {
	params := map[string]any{}
	if update.Objective != nil {
		objective := strings.TrimSpace(*update.Objective)
		if objective == "" {
			return nil, errf(400, "goal objective is required")
		}
		if len(objective) > 4000 {
			return nil, errf(400, "goal objective must be at most 4000 characters")
		}
		params["objective"] = objective
	}
	if update.Status != nil {
		status := strings.TrimSpace(*update.Status)
		if !validGoalStatus(status) {
			return nil, errf(400, "invalid goal status: %s", status)
		}
		params["status"] = status
	}
	if update.TokenBudget != nil {
		if *update.TokenBudget <= 0 {
			return nil, errf(400, "goal token budget must be positive")
		}
		params["tokenBudget"] = *update.TokenBudget
	} else if update.ClearTokenBudget {
		params["tokenBudget"] = nil
	}
	if len(params) == 0 {
		return nil, errf(400, "goal objective, status, or token budget is required")
	}

	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	agentID, threadID := agent.ID, agent.ThreadID
	h.mu.Unlock()
	if strings.TrimSpace(threadID) == "" {
		return nil, errf(409, "agent has no Codex Thread binding")
	}
	params["threadId"] = threadID
	host, err := h.ensureCodexHost()
	if err != nil {
		return nil, err
	}
	raw, err := host.client.Request("thread/goal/set", params, 20*time.Second)
	if err != nil {
		return nil, errf(500, "update Codex Goal: %s", err)
	}
	var response threadGoalSetResponse
	if err := json.Unmarshal(raw, &response); err != nil || strings.TrimSpace(response.Goal.ThreadID) == "" {
		return nil, errf(500, "decode Codex Goal: %s", decodeError(err))
	}
	h.mu.Lock()
	wasReserved := h.activeGoalReservesThreadLocked(agentID)
	h.applyGoalLocked(agentID, &response.Goal, true)
	if wasReserved && !h.activeGoalReservesThreadLocked(agentID) {
		h.startPendingWorkersLocked(agentID)
	}
	h.mu.Unlock()

	if response.Goal.Status == GoalStatusActive {
		h.startWorker(func() { h.resumeGoalThread(agentID, host.generation) })
	}
	return cloneGoal(&response.Goal), nil
}

func (h *Hub) ClearGoal(key string) (bool, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return false, errf(404, "agent not found: %s", key)
	}
	agentID, threadID := agent.ID, agent.ThreadID
	h.mu.Unlock()
	if strings.TrimSpace(threadID) == "" {
		return false, errf(409, "agent has no Codex Thread binding")
	}
	host, err := h.ensureCodexHost()
	if err != nil {
		return false, err
	}
	raw, err := host.client.Request("thread/goal/clear", map[string]any{"threadId": threadID}, 20*time.Second)
	if err != nil {
		return false, errf(500, "clear Codex Goal: %s", err)
	}
	var response struct {
		Cleared bool `json:"cleared"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, errf(500, "decode clear Goal response: %s", err)
	}
	if response.Cleared {
		h.mu.Lock()
		h.applyGoalLocked(agentID, nil, true)
		h.startPendingWorkersLocked(agentID)
		h.mu.Unlock()
	}
	return response.Cleared, nil
}

func (h *Hub) resumeGoalThread(agentID string, generation uint64) {
	h.mu.Lock()
	host := h.codexHost
	agent := h.agents[agentID]
	if host == nil || host.generation != generation || agent == nil || h.goals[agentID] == nil || h.goals[agentID].Status != GoalStatusActive {
		h.mu.Unlock()
		return
	}
	if rt := h.runtimes[agentID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return
	}
	threadID, sandbox, cwd := agent.ThreadID, agent.Sandbox, agent.Cwd
	h.mu.Unlock()
	if err := resumeThread(host.client, threadID, sandbox, cwd); err != nil {
		log.Printf("[codex-loom] resume Goal for %s: %v", threadID, err)
	}
}

func (h *Hub) onGoalNotificationLocked(agentID, method string, params json.RawMessage) {
	wasReserved := h.activeGoalReservesThreadLocked(agentID)
	switch method {
	case "thread/goal/updated":
		var notification struct {
			Goal ThreadGoal `json:"goal"`
		}
		if json.Unmarshal(params, &notification) != nil || notification.Goal.ThreadID == "" {
			return
		}
		h.applyGoalLocked(agentID, &notification.Goal, true)
	case "thread/goal/cleared":
		h.applyGoalLocked(agentID, nil, true)
	}
	if wasReserved && !h.activeGoalReservesThreadLocked(agentID) {
		h.startPendingWorkersLocked(agentID)
	}
}

func (h *Hub) startPendingWorkersLocked(agentID string) {
	if h.isDrainingLocked() {
		return
	}
	h.startWorkerLocked(func() { h.deliverNextQueuedForTarget(agentID, defaultInactivity) })
	h.startWorkerLocked(func() { h.deliverNextInboxForAgent(agentID) })
	h.startWorkerLocked(func() { h.deliverAnsweredHumanRequest(agentID) })
}

func cloneGoal(goal *ThreadGoal) *ThreadGoal {
	if goal == nil {
		return nil
	}
	copy := *goal
	return &copy
}

func decodeError(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("response did not include a Goal")
}
