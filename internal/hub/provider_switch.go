package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/rollout"
)

// ProviderSwitchBinding is persisted before the shared CodexHost is restarted.
// The committed ProviderID and Model remain unchanged until the same primary
// Thread has been cold-resumed successfully with this binding.
type ProviderSwitchBinding struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model,omitempty"`
	StartedAt  string `json:"startedAt"`
}

type ProviderBindingChange struct {
	PreviousProviderID string `json:"previousProviderId"`
	PreviousModel      string `json:"previousModel,omitempty"`
	ProviderID         string `json:"providerId"`
	Model              string `json:"model,omitempty"`
	SwitchedAt         string `json:"switchedAt"`
}

type ProviderSwitchParams struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
}

type providerSwitchRolloutBackup struct {
	originalPath string
	backupPath   string
}

func publicProviderID(providerID string) string {
	return normalizePublicProviderID(providerID)
}

func effectiveProviderBinding(agent *Agent) (string, string) {
	if agent != nil && agent.PendingProviderSwitch != nil {
		return agent.PendingProviderSwitch.ProviderID, agent.PendingProviderSwitch.Model
	}
	if agent == nil {
		return "", ""
	}
	return agent.ProviderID, agent.Model
}

func normalizeProviderSwitchBinding(params ProviderSwitchParams) (string, string, error) {
	providerID := normalizeProviderID(params.ProviderID)
	model := strings.TrimSpace(params.Model)
	if providerID != "" && !nameRe.MatchString(providerID) {
		return "", "", errf(400, "providerId must match [a-zA-Z0-9_-]+")
	}
	if providerID == deepSeekProviderID && model == "" {
		model = deepSeekModel
	}
	if providerID != "" && model == "" {
		return "", "", errf(400, "model is required for a custom Provider")
	}
	if providerID == deepSeekProviderID && model != deepSeekModel {
		return "", "", errf(400, "DeepSeek Responses currently supports model %s", deepSeekModel)
	}
	return providerID, model, nil
}

func (h *Hub) providerSwitchBusyLocked() error {
	for _, agent := range h.agents {
		if agent != nil && agent.Status == "running" {
			return errf(409, "agent %q is running; wait for all Turns to finish", agent.Name)
		}
	}
	for agentID, runtime := range h.runtimes {
		if runtime == nil {
			continue
		}
		if runtime.activeTurn != nil && !runtime.activeTurn.finished {
			return errf(409, "agent %q has an active Turn", h.agentNameLocked(agentID, agentID))
		}
		if len(runtime.approvals) > 0 {
			return errf(409, "agent %q has a pending approval", h.agentNameLocked(agentID, agentID))
		}
	}
	for agentID, goal := range h.goals {
		if goal != nil && goal.Status == GoalStatusActive {
			return errf(409, "agent %q has an active Goal; pause it before switching Provider", h.agentNameLocked(agentID, agentID))
		}
	}
	for _, message := range h.comms {
		if message != nil && message.DeliveryStatus == "delivering" {
			return errf(409, "an Agent Message delivery is starting; retry the Provider switch after it settles")
		}
	}
	for _, item := range h.inbox {
		if item != nil && item.State == "handling" {
			return errf(409, "an External Inbox delivery is starting; retry the Provider switch after it settles")
		}
	}
	for _, request := range h.humanRequests {
		if request != nil && request.DeliveryStatus == "delivering" {
			return errf(409, "a Needs You answer is being delivered; retry the Provider switch after it settles")
		}
	}
	return nil
}

// detachCodexHostLocked removes idle runtime projections before deliberately
// closing a shared host. The old OnClose callback becomes a generation no-op.
func (h *Hub) detachCodexHostLocked() *codexHostRuntime {
	host := h.codexHost
	if host == nil {
		return nil
	}
	h.codexHost = nil
	for agentID, runtime := range h.runtimes {
		if runtime != nil && runtime.hostGeneration == host.generation {
			delete(h.runtimes, agentID)
		}
	}
	h.remoteRuntime = nil
	h.remoteEnabledGeneration = 0
	h.remotePairing = nil
	if h.remoteConfig.Enabled {
		h.remoteStatus.State = "starting"
		h.remoteStatus.LastError = ""
		h.remoteStatus.UpdatedAt = now()
		h.emitRemoteLocked()
	}
	return host
}

func closeCodexHost(host *codexHostRuntime) error {
	if host == nil {
		return nil
	}
	host.client.Close()
	if !host.client.WaitClosed(4 * time.Second) {
		return fmt.Errorf("CodexHost did not exit within 4s")
	}
	return nil
}

func (h *Hub) startProviderSwitchHost() (*codexHostRuntime, error) {
	h.mu.Lock()
	host, err := h.startCodexHostLocked()
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := waitCodexHost(host); err != nil {
		return nil, errf(500, "CodexHost is not ready: %s", err)
	}
	return host, nil
}

func (h *Hub) verifyProviderSwitchRuntime(agentID string, host *codexHostRuntime) error {
	h.mu.Lock()
	agent := h.agents[agentID]
	if agent == nil {
		h.mu.Unlock()
		return errf(404, "agent vanished during Provider switch")
	}
	runtime, err := h.getRuntimeLocked(agent)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := waitReady(runtime); err != nil {
		return errf(500, "cold-resume primary Thread: %s", err)
	}
	if runtime.hostGeneration != host.generation {
		return errf(500, "CodexHost changed during Provider switch")
	}
	return nil
}

func (h *Hub) finishProviderSwitchMaintenance() {
	h.mu.Lock()
	h.providerSwitching = false
	remoteEnabled := h.remoteConfig.Enabled
	h.mu.Unlock()
	if remoteEnabled {
		h.startWorker(func() { _ = h.ensureRemoteRuntime() })
	}
	h.startWorker(func() {
		h.drainQueuedAll()
		h.drainInbox()
		h.drainHumanAnswers()
	})
}

func (h *Hub) rollbackProviderSwitch(agentID string, previous Agent, cause error, rolloutBackup *providerSwitchRolloutBackup) error {
	if rolloutBackup != nil {
		if err := rollout.RestoreRolloutBackup(rolloutBackup.backupPath, rolloutBackup.originalPath); err != nil {
			cause = fmt.Errorf("%v; restore rollout backup: %w", cause, err)
		}
	}
	h.mu.Lock()
	if agent := h.agents[agentID]; agent != nil {
		*agent = previous
		if err := h.persistAgentsLocked(); err != nil {
			h.mu.Unlock()
			h.finishProviderSwitchMaintenance()
			return fmt.Errorf("%v; rollback Agent binding: %w", cause, err)
		}
	}
	failedHost := h.detachCodexHostLocked()
	h.mu.Unlock()
	if err := closeCodexHost(failedHost); err != nil {
		cause = fmt.Errorf("%v; close failed switch host: %w", cause, err)
	}
	if host, err := h.startProviderSwitchHost(); err != nil {
		cause = fmt.Errorf("%v; restore previous CodexHost: %w", cause, err)
	} else if err := h.verifyProviderSwitchRuntime(agentID, host); err != nil {
		cause = fmt.Errorf("%v; restore previous Thread binding: %w", cause, err)
	}
	h.finishProviderSwitchMaintenance()
	return cause
}

// SwitchAgentProvider cold-resumes the same primary Thread with a new Provider
// binding. The shared CodexHost is restarted, so all Agent Turns, approvals and
// active Goals must be quiescent before the operation begins.
func (h *Hub) SwitchAgentProvider(key string, params ProviderSwitchParams) (AgentView, error) {
	providerID, model, err := normalizeProviderSwitchBinding(params)
	if err != nil {
		return AgentView{}, err
	}

	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return AgentView{}, errf(503, "CodexLoom is shutting down")
	}
	if h.isDrainingLocked() {
		h.mu.Unlock()
		return AgentView{}, errf(409, "CodexLoom is draining for restart")
	}
	if h.providerSwitching {
		h.mu.Unlock()
		return AgentView{}, errf(409, "another Agent Provider switch is in progress")
	}
	agent := h.resolveLocked(key)
	if agent == nil {
		h.mu.Unlock()
		return AgentView{}, errf(404, "agent not found: %s", key)
	}
	if strings.TrimSpace(agent.ThreadID) == "" {
		h.mu.Unlock()
		return AgentView{}, errf(409, "agent has no primary Thread binding")
	}
	if agent.ProviderID == providerID && agent.Model == model {
		view := h.viewLocked(agent)
		h.mu.Unlock()
		return view, nil
	}
	if err := h.providerSwitchBusyLocked(); err != nil {
		h.mu.Unlock()
		return AgentView{}, err
	}
	h.providerSwitching = true
	agentID := agent.ID
	h.mu.Unlock()
	if _, err := h.startProviderSwitchHost(); err != nil {
		h.finishProviderSwitchMaintenance()
		return AgentView{}, err
	}

	providerLookupID := publicProviderID(providerID)
	provider, providerErr := h.GetModelProvider(providerLookupID)
	if providerErr != nil {
		h.finishProviderSwitchMaintenance()
		return AgentView{}, providerErr
	}
	if !provider.Configured || !provider.CredentialConfigured {
		h.finishProviderSwitchMaintenance()
		return AgentView{}, errf(409, "Provider %s is not ready; configure and verify it before switching", providerLookupID)
	}

	h.mu.Lock()
	agent = h.agents[agentID]
	if agent == nil {
		h.mu.Unlock()
		h.finishProviderSwitchMaintenance()
		return AgentView{}, errf(404, "agent vanished during Provider switch")
	}
	if err := h.providerSwitchBusyLocked(); err != nil {
		h.mu.Unlock()
		h.finishProviderSwitchMaintenance()
		return AgentView{}, err
	}
	previous := *agent
	previous.ProviderHistory = append([]ProviderBindingChange(nil), agent.ProviderHistory...)
	var rolloutBackup *providerSwitchRolloutBackup
	agent.Source = ""
	agent.PendingProviderSwitch = &ProviderSwitchBinding{ProviderID: providerID, Model: model, StartedAt: now()}
	agent.UpdatedAt = now()
	if err := h.persistAgentsLocked(); err != nil {
		*agent = previous
		h.mu.Unlock()
		h.finishProviderSwitchMaintenance()
		return AgentView{}, errf(500, "save pending Provider switch: %s", err)
	}
	oldHost := h.detachCodexHostLocked()
	h.mu.Unlock()

	if err := closeCodexHost(oldHost); err != nil {
		return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, err, rolloutBackup))
	}
	if providerID == "" && previous.ProviderID != "" {
		backupDir := filepath.Join(os.TempDir(), "codexloom-provider-switch-backups")
		if h.st != nil {
			backupDir = filepath.Join(h.st.Dir(), "backups", "provider-switch")
		}
		result, sanitizeErr := rollout.SanitizeReasoningContent(previous.ThreadID, backupDir)
		if sanitizeErr != nil {
			return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, sanitizeErr, rolloutBackup))
		}
		if result.Changed > 0 && result.BackupPath != "" {
			rolloutBackup = &providerSwitchRolloutBackup{originalPath: result.OriginalPath, backupPath: result.BackupPath}
		}
	}
	host, err := h.startProviderSwitchHost()
	if err != nil {
		return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, err, rolloutBackup))
	}
	if err := h.verifyProviderSwitchRuntime(agentID, host); err != nil {
		return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, err, rolloutBackup))
	}

	h.mu.Lock()
	agent = h.agents[agentID]
	if agent == nil {
		h.mu.Unlock()
		return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, fmt.Errorf("agent vanished before commit"), rolloutBackup))
	}
	agent.ProviderID = providerID
	agent.Model = model
	agent.PendingProviderSwitch = nil
	agent.ProviderHistory = append(append([]ProviderBindingChange(nil), previous.ProviderHistory...), ProviderBindingChange{
		PreviousProviderID: publicProviderID(previous.ProviderID), PreviousModel: previous.Model,
		ProviderID: publicProviderID(providerID), Model: model, SwitchedAt: now(),
	})
	agent.LastError = ""
	agent.UpdatedAt = now()
	if err := h.persistAgentsLocked(); err != nil {
		*agent = previous
		h.mu.Unlock()
		return AgentView{}, errf(500, "switch Provider: %s", h.rollbackProviderSwitch(agentID, previous, fmt.Errorf("commit Agent binding: %w", err), rolloutBackup))
	}
	switchedAt := agent.ProviderHistory[len(agent.ProviderHistory)-1].SwitchedAt
	h.emitLocked(agentID, "loom/agent-provider-switched", map[string]any{
		"threadId":           agent.ThreadID,
		"previousProviderId": publicProviderID(previous.ProviderID), "previousModel": previous.Model,
		"providerId": publicProviderID(providerID), "model": model, "switchedAt": switchedAt,
	})
	h.emitStatusLocked(agent, agent.Status)
	view := h.viewLocked(agent)
	h.mu.Unlock()
	h.finishProviderSwitchMaintenance()
	return view, nil
}
