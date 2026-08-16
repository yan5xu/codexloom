package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/modelcatalog"
	"github.com/yan5xu/codex-loom/internal/rollout"
)

func (h *Hub) ListAgents() []AgentView {
	h.mu.Lock()
	out := make([]AgentView, 0, len(h.agents))
	for _, meta := range h.agents {
		out = append(out, h.viewLocked(meta))
	}
	h.mu.Unlock()
	for i := range out {
		applyRolloutStatus(&out[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListAgentSummaries returns the live fields needed by the Web workspace
// without copying unbounded task envelopes or Provider audit history into
// every reconciliation snapshot. The canonical Agent detail endpoint remains
// the source for the complete record.
func (h *Hub) ListAgentSummaries() []AgentView {
	views := h.ListAgents()
	for i := range views {
		views[i].CurrentTask = boundedDisplayTask(views[i].CurrentTask, 320)
		views[i].ProviderHistory = nil
		if views[i].LastTurn != nil {
			last := *views[i].LastTurn
			last.Task = boundedDisplayTask(last.Task, 320)
			views[i].LastTurn = &last
		}
		if views[i].Goal != nil {
			goal := *views[i].Goal
			goal.Objective = boundedDisplayTask(goal.Objective, 320)
			views[i].Goal = &goal
		}
	}
	return views
}

func boundedDisplayTask(text string, limit int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = displayUserTask(text)
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// ListSessions is the pre-CodexLoom compatibility method.
func (h *Hub) ListSessions() []SessionView { return h.ListAgents() }

func (h *Hub) GetAgent(key string) (AgentView, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return AgentView{}, errf(404, "agent not found: %s", key)
	}
	view := h.viewLocked(meta)
	h.mu.Unlock()
	applyRolloutStatus(&view)
	return view, nil
}

// GetSession is the pre-CodexLoom compatibility method.
func (h *Hub) GetSession(key string) (SessionView, error) { return h.GetAgent(key) }

func (h *Hub) ActiveAgents() []ActiveAgent {
	views := h.ListAgents()
	out := []ActiveAgent{}
	for _, view := range views {
		if view.Status != "running" {
			continue
		}
		out = append(out, ActiveAgent{ID: view.ID, Name: view.Name, CurrentTask: view.CurrentTask})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RunningSessions is the pre-CodexLoom compatibility method.
func (h *Hub) RunningSessions() []RunningSession { return h.ActiveAgents() }

type CreateParams struct {
	Name           string `json:"name"`
	Cwd            string `json:"cwd"`
	Sandbox        string `json:"sandbox"`
	ApprovalPolicy string `json:"approvalPolicy"`
	ProviderID     string `json:"providerId"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
}

// RestoreAgentParams re-registers a previously archived Agent without
// creating a replacement identity or starting a Turn. Profiles and team
// relationships are stored independently and reconnect through the stable ID.
type RestoreAgentParams struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Cwd            string `json:"cwd"`
	ThreadID       string `json:"threadId"`
	Sandbox        string `json:"sandbox"`
	ApprovalPolicy string `json:"approvalPolicy"`
	ProviderID     string `json:"providerId"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	CreatedAt      string `json:"createdAt"`
}

type ConfigParams struct {
	Name           *string `json:"name"`
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	Sandbox        *string `json:"sandbox"`
	ApprovalPolicy *string `json:"approvalPolicy"`
	ProviderID     *string `json:"providerId"`
}

func (h *Hub) CreateAgent(p CreateParams) (AgentView, error) {
	if p.Name == "" || p.Cwd == "" {
		return AgentView{}, errf(400, "name and cwd are required")
	}
	if !nameRe.MatchString(p.Name) {
		return AgentView{}, errf(400, "name must match [a-zA-Z0-9_-]+")
	}
	if p.Sandbox == "" {
		p.Sandbox = "danger-full-access"
	}
	if p.ApprovalPolicy == "" {
		p.ApprovalPolicy = "never"
	}
	p.Model = strings.TrimSpace(p.Model)
	p.ProviderID = normalizeProviderID(p.ProviderID)
	if p.ProviderID != "" && !nameRe.MatchString(p.ProviderID) {
		return AgentView{}, errf(400, "providerId must match [a-zA-Z0-9_-]+")
	}
	if p.ProviderID == deepSeekProviderID && p.Model == "" {
		p.Model = deepSeekModel
	}
	if p.ProviderID != "" && p.Model == "" {
		return AgentView{}, errf(400, "model is required for a custom Provider")
	}
	if p.ProviderID == deepSeekProviderID && p.Model != deepSeekModel {
		return AgentView{}, errf(400, "DeepSeek Responses currently supports model %s", deepSeekModel)
	}
	if p.ProviderID != "" {
		provider, err := h.GetModelProvider(p.ProviderID)
		if err != nil {
			return AgentView{}, err
		}
		if !provider.Configured || !provider.CredentialConfigured {
			return AgentView{}, errf(409, "Provider %s is not ready; configure and verify it before creating an Agent", p.ProviderID)
		}
	}
	p.Effort = normalizeEffort(strings.TrimSpace(p.Effort))
	if err := validateModelEffort(p.ProviderID, p.Model, p.Effort); err != nil {
		return AgentView{}, err
	}
	idBytes := make([]byte, 4)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	h.mu.Lock()
	if h.resolveLocked(p.Name) != nil {
		h.mu.Unlock()
		return AgentView{}, errf(409, "agent %q already exists", p.Name)
	}
	meta := &Agent{
		ID: id, Name: p.Name, Cwd: p.Cwd,
		Sandbox: p.Sandbox, ApprovalPolicy: p.ApprovalPolicy, ProviderID: p.ProviderID, Model: p.Model, Effort: p.Effort,
		Status: "idle", CreatedAt: now(), UpdatedAt: now(),
	}
	h.agents[id] = meta
	h.seqs[id] = 0
	rt, err := h.getRuntimeLocked(meta)
	if err != nil {
		delete(h.agents, id)
		h.mu.Unlock()
		return AgentView{}, err
	}
	h.mu.Unlock()

	if err := waitReady(rt); err != nil {
		h.mu.Lock()
		delete(h.agents, id)
		delete(h.runtimes, id)
		persistErr := h.persistAgentsLocked()
		h.mu.Unlock()
		if persistErr != nil {
			return AgentView{}, errf(500, "failed to start codex thread: %s; remove failed Agent: %s", err, persistErr)
		}
		return AgentView{}, errf(500, "failed to start codex thread: %s", err)
	}

	h.mu.Lock()
	if err := h.persistAgentsLocked(); err != nil {
		threadID := meta.ThreadID
		delete(h.agents, id)
		delete(h.runtimes, id)
		delete(h.seqs, id)
		h.mu.Unlock()
		if threadID != "" && rt.client != nil && !rt.client.Closed() {
			_, _ = rt.client.Request("thread/archive", map[string]any{"threadId": threadID}, 10*time.Second)
		}
		return AgentView{}, errf(500, "save agent: %s", err)
	}
	h.emitLocked(id, "loom/agent-created", map[string]any{
		"id": id, "name": meta.Name, "cwd": meta.Cwd, "threadId": meta.ThreadID, "providerId": meta.ProviderID,
	})
	h.emitStatusLocked(meta, meta.Status)
	view := h.viewLocked(meta)
	h.mu.Unlock()
	return view, nil
}

func (h *Hub) RestoreAgent(p RestoreAgentParams) (AgentView, error) {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Cwd = strings.TrimSpace(p.Cwd)
	p.ThreadID = strings.TrimSpace(p.ThreadID)
	if p.ID == "" || p.Name == "" || p.Cwd == "" || p.ThreadID == "" {
		return AgentView{}, errf(400, "id, name, cwd, and threadId are required")
	}
	if !nameRe.MatchString(p.Name) {
		return AgentView{}, errf(400, "name must match [a-zA-Z0-9_-]+")
	}
	if p.Sandbox == "" {
		p.Sandbox = "danger-full-access"
	}
	if p.ApprovalPolicy == "" {
		p.ApprovalPolicy = "never"
	}
	p.Effort = normalizeEffort(strings.TrimSpace(p.Effort))
	p.ProviderID = normalizeProviderID(p.ProviderID)
	p.Model = strings.TrimSpace(p.Model)
	if p.ProviderID != "" && !nameRe.MatchString(p.ProviderID) {
		return AgentView{}, errf(400, "providerId must match [a-zA-Z0-9_-]+")
	}
	if p.ProviderID == deepSeekProviderID && p.Model == "" {
		p.Model = deepSeekModel
	}
	if p.ProviderID != "" && p.Model == "" {
		return AgentView{}, errf(400, "model is required for a custom Provider")
	}
	if err := validateModelEffort(p.ProviderID, p.Model, p.Effort); err != nil {
		return AgentView{}, err
	}
	if p.CreatedAt == "" {
		p.CreatedAt = now()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agents[p.ID] != nil {
		return AgentView{}, errf(409, "agent id %q already exists", p.ID)
	}
	if existing := h.resolveLocked(p.Name); existing != nil {
		return AgentView{}, errf(409, "agent %q already exists", p.Name)
	}
	for _, existing := range h.agents {
		if existing.ThreadID == p.ThreadID {
			return AgentView{}, errf(409, "thread %q is already bound to agent %q", p.ThreadID, existing.Name)
		}
	}
	meta := &Agent{
		ID: p.ID, Name: p.Name, Cwd: p.Cwd, ThreadID: p.ThreadID,
		Sandbox: p.Sandbox, ApprovalPolicy: p.ApprovalPolicy,
		ProviderID: p.ProviderID, Model: p.Model, Effort: p.Effort,
		Status: "idle", CreatedAt: p.CreatedAt, UpdatedAt: now(),
	}
	h.agents[p.ID] = meta
	h.seqs[p.ID] = h.st.LastSeq(p.ID)
	if err := h.persistAgentsLocked(); err != nil {
		delete(h.agents, p.ID)
		delete(h.seqs, p.ID)
		return AgentView{}, errf(500, "save restored agent: %s", err)
	}
	h.emitLocked(p.ID, "loom/agent-restored", map[string]any{
		"id": p.ID, "name": p.Name, "cwd": p.Cwd, "threadId": p.ThreadID, "providerId": p.ProviderID,
	})
	h.emitStatusLocked(meta, meta.Status)
	return h.viewLocked(meta), nil
}

// CreateSession is the pre-CodexLoom compatibility method.
func (h *Hub) CreateSession(p CreateParams) (SessionView, error) { return h.CreateAgent(p) }

func (h *Hub) UpdateAgentConfig(key string, p ConfigParams) (AgentView, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return AgentView{}, errf(404, "agent not found: %s", key)
	}
	if meta.Status == "running" {
		h.mu.Unlock()
		return AgentView{}, errf(409, "agent %q is running; config changes apply between Turns", meta.Name)
	}

	nextName := meta.Name
	nextModel := meta.Model
	nextEffort := meta.Effort
	nextSandbox := meta.Sandbox
	nextApprovalPolicy := meta.ApprovalPolicy
	nextProviderID := meta.ProviderID

	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			h.mu.Unlock()
			return AgentView{}, errf(400, "name is required")
		}
		if !nameRe.MatchString(name) {
			h.mu.Unlock()
			return AgentView{}, errf(400, "name must match [a-zA-Z0-9_-]+")
		}
		for _, existing := range h.agents {
			if existing.ID == meta.ID {
				continue
			}
			if existing.ID == name || existing.Name == name {
				h.mu.Unlock()
				return AgentView{}, errf(409, "agent %q already exists", name)
			}
		}
		nextName = name
	}
	if p.Model != nil {
		nextModel = strings.TrimSpace(*p.Model)
	}
	if p.ProviderID != nil {
		nextProviderID = normalizeProviderID(*p.ProviderID)
		if nextProviderID != meta.ProviderID && strings.TrimSpace(meta.ThreadID) != "" {
			h.mu.Unlock()
			return AgentView{}, errf(409, "agent %q already has a primary Thread; use the Provider switch operation", meta.Name)
		}
	}
	if nextProviderID != "" && !nameRe.MatchString(nextProviderID) {
		h.mu.Unlock()
		return AgentView{}, errf(400, "providerId must match [a-zA-Z0-9_-]+")
	}
	if nextProviderID == deepSeekProviderID && nextModel == "" {
		nextModel = deepSeekModel
	}
	if nextProviderID != "" && nextModel == "" {
		h.mu.Unlock()
		return AgentView{}, errf(400, "model is required for a custom Provider")
	}
	if nextProviderID == deepSeekProviderID && nextModel != deepSeekModel {
		h.mu.Unlock()
		return AgentView{}, errf(400, "DeepSeek Responses currently supports model %s", deepSeekModel)
	}
	if p.Effort != nil {
		effort := normalizeEffort(strings.TrimSpace(*p.Effort))
		if effort == "" || validEffort(effort) {
			nextEffort = effort
		} else {
			h.mu.Unlock()
			return AgentView{}, errf(400, "effort must be one of: minimal, low, medium, high, xhigh, max, ultra")
		}
	}
	if err := validateModelEffort(nextProviderID, nextModel, nextEffort); err != nil {
		h.mu.Unlock()
		return AgentView{}, err
	}
	if p.Sandbox != nil {
		nextSandbox = strings.TrimSpace(*p.Sandbox)
	}
	if p.ApprovalPolicy != nil {
		nextApprovalPolicy = strings.TrimSpace(*p.ApprovalPolicy)
	}
	previous := *meta
	nameChanged := meta.Name != nextName
	meta.Source = "" // editing config adopts an edge mirror into CodexLoom's registry
	meta.Name = nextName
	meta.ProviderID = nextProviderID
	meta.Model = nextModel
	meta.Effort = nextEffort
	meta.Sandbox = nextSandbox
	meta.ApprovalPolicy = nextApprovalPolicy
	meta.UpdatedAt = now()
	if err := h.persistAgentsLocked(); err != nil {
		*meta = previous
		h.mu.Unlock()
		return AgentView{}, errf(500, "save agent config: %s", err)
	}
	h.emitStatusLocked(meta, meta.Status)
	view := h.viewLocked(meta)
	threadID := meta.ThreadID
	rt := h.runtimes[meta.ID]
	h.mu.Unlock()

	if nameChanged && threadID != "" {
		if err := h.syncThreadName(rt, threadID, nextName); err != nil {
			// The Hub name remains authoritative. Runtime initialization and the
			// startup backfill retry the Codex-side title later.
			log.Printf("[codex-loom] sync renamed thread %s to %q: %v", threadID, nextName, err)
		}
	}
	return view, nil
}

// UpdateConfig is the pre-CodexLoom compatibility method.
func (h *Hub) UpdateConfig(key string, p ConfigParams) (SessionView, error) {
	return h.UpdateAgentConfig(key, p)
}

func (h *Hub) syncThreadName(rt *runtime, threadID, name string) error {
	if rt != nil && rt.client != nil && !rt.client.Closed() {
		if err := waitReady(rt); err == nil {
			return setThreadName(rt.client, threadID, name)
		}
	}
	host, err := h.ensureCodexHost()
	if err != nil {
		return err
	}
	return setThreadName(host.client, threadID, name)
}

// SyncThreadNames backfills CodexLoom Agent names into Codex's persisted thread
// metadata without resuming or taking ownership of those threads.
func (h *Hub) SyncThreadNames() error {
	type namedThread struct {
		id     string
		name   string
		source string
	}
	h.mu.Lock()
	byThreadID := make(map[string]namedThread, len(h.agents))
	for _, meta := range h.agents {
		if strings.TrimSpace(meta.ThreadID) == "" || strings.TrimSpace(meta.Name) == "" {
			continue
		}
		current, exists := byThreadID[meta.ThreadID]
		if !exists || (current.source == "edge" && meta.Source != "edge") {
			byThreadID[meta.ThreadID] = namedThread{id: meta.ThreadID, name: meta.Name, source: meta.Source}
		}
	}
	h.mu.Unlock()
	threads := make([]namedThread, 0, len(byThreadID))
	for _, thread := range byThreadID {
		threads = append(threads, thread)
	}
	if len(threads) == 0 {
		return nil
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i].id < threads[j].id })

	host, err := h.ensureCodexHost()
	if err != nil {
		return err
	}
	var syncErrs []error
	for _, thread := range threads {
		if err := setThreadName(host.client, thread.id, thread.name); err != nil {
			syncErrs = append(syncErrs, fmt.Errorf("%s (%s): %w", thread.name, thread.id, err))
		}
	}
	return errors.Join(syncErrs...)
}

func normalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "extra-high", "extra_high", "extra high":
		return "xhigh"
	default:
		return strings.ToLower(strings.TrimSpace(effort))
	}
}

func validEffort(effort string) bool {
	switch effort {
	case "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	default:
		return false
	}
}

func validateModelEffort(providerID, model, effort string) error {
	if effort == "" {
		return nil
	}
	if !validEffort(effort) {
		return errf(400, "effort must be one of: minimal, low, medium, high, xhigh, max, ultra")
	}
	providerID = normalizePublicProviderID(providerID)
	snapshot, err := modelcatalog.Describe(os.Getenv("CODEX_LOOM_MODEL_CATALOG"))
	if err != nil {
		return errf(500, "read Codex model catalog: %s", err)
	}
	for _, candidate := range snapshot.PublicModels() {
		if candidate.ProviderID != providerID || candidate.ID != model || len(candidate.ReasoningEfforts) == 0 {
			continue
		}
		for _, supported := range candidate.ReasoningEfforts {
			if effort == supported {
				return nil
			}
		}
		return errf(400, "effort for model %s must be one of: %s", model, strings.Join(candidate.ReasoningEfforts, ", "))
	}
	return nil
}

func normalizeProviderID(providerID string) string {
	providerID = strings.TrimSpace(providerID)
	if providerID == "openai" {
		return ""
	}
	return providerID
}

type SendResult struct {
	Dispatched bool   `json:"dispatched"`
	AgentID    string `json:"agentId"`
	SessionID  string `json:"sessionId"`
	TurnID     string `json:"turnId"`
}

func (h *Hub) SendTask(key, text string, inactivity time.Duration) (SendResult, error) {
	return h.sendTask(key, text, inactivity, "", "", "")
}

func (h *Hub) SendTaskWithArtifacts(key, text string, artifactIDs []string, inactivity time.Duration) (SendResult, error) {
	return h.sendTaskWithArtifacts(key, text, artifactIDs, inactivity, "", "", "", "", "")
}

func (h *Hub) sendTask(key, text string, inactivity time.Duration, inboxItemID, attemptID, agentMessageID string) (SendResult, error) {
	return h.sendTaskWithArtifacts(key, text, nil, inactivity, inboxItemID, attemptID, agentMessageID, "", "")
}

func (h *Hub) sendTaskWithTopic(key, text string, inactivity time.Duration, topicID string) (SendResult, error) {
	return h.sendTaskWithArtifacts(key, text, nil, inactivity, "", "", "", topicID, "")
}

func (h *Hub) sendTaskWithTopicDisplay(key, text, displayTask string, inactivity time.Duration, topicID string) (SendResult, error) {
	return h.sendTaskWithArtifacts(key, text, nil, inactivity, "", "", "", topicID, displayTask)
}

func (h *Hub) sendTaskWithArtifacts(key, text string, artifactIDs []string, inactivity time.Duration, inboxItemID, attemptID, agentMessageID, topicID, displayTask string) (SendResult, error) {
	source := h.inferTurnContextSource(inboxItemID, agentMessageID, topicID, "")
	return h.sendTaskWithContext(key, text, artifactIDs, inactivity, inboxItemID, attemptID, agentMessageID, topicID, displayTask, source)
}

func (h *Hub) sendTaskWithContext(key, text string, artifactIDs []string, inactivity time.Duration, inboxItemID, attemptID, agentMessageID, topicID, displayTask string, source turnContextSource) (SendResult, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(artifactIDs) == 0 {
		return SendResult{}, errf(400, "text or an artifact is required")
	}
	if inactivity <= 0 {
		inactivity = defaultInactivity
	}

	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return SendResult{}, errf(503, "CodexLoom is shutting down")
	}
	if h.isDrainingLocked() {
		h.mu.Unlock()
		return SendResult{}, errf(409, "CodexLoom is draining for restart")
	}
	if h.providerSwitching {
		h.mu.Unlock()
		return SendResult{}, errf(409, "CodexLoom is switching an Agent Provider")
	}
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return SendResult{}, errf(404, "agent not found: %s", key)
	}
	if h.agentCwdUpdatePendingLocked(meta.ID) {
		h.mu.Unlock()
		return SendResult{}, errf(409, "agent %q cwd update is in progress; retry after it completes", meta.Name)
	}
	if topicID != "" {
		topic := h.topics[topicID]
		if topic == nil || !topicHasAgent(topic, meta.ID, meta.ID) {
			h.mu.Unlock()
			return SendResult{}, errf(403, "Agent %s is not part of Topic %s", meta.Name, topicID)
		}
	}
	if rt, ok := h.runtimes[meta.ID]; ok && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return SendResult{}, errf(409, "agent %q is already running a task", meta.Name)
	}
	if meta.Status == "running" {
		// Stale state without a live turn (crash leftovers): repair.
		meta.Status = "idle"
		meta.LastError = "repaired stale running state"
	}
	rt, err := h.getRuntimeLocked(meta)
	if err != nil {
		h.mu.Unlock()
		return SendResult{}, err
	}
	agentID := meta.ID
	providerID := meta.ProviderID
	h.mu.Unlock()
	if providerID == deepSeekProviderID && len(artifactIDs) > 0 {
		return SendResult{}, errf(400, "DeepSeek %s currently supports text input only; remove image and file attachments", deepSeekModel)
	}
	artifacts, err := h.resolveThreadArtifacts(agentID, artifactIDs)
	if err != nil {
		return SendResult{}, err
	}
	taskText := codexTaskText(text, artifacts)
	visibleText := text
	if displayTask = strings.TrimSpace(displayTask); displayTask != "" {
		taskText = displayTask
		visibleText = displayTask
	}
	if displayText := strings.TrimSpace(source.DisplayText); displayText != "" {
		visibleText = displayText
	}

	// Serialize readiness, epoch context injection and turn reservation for one
	// runtime. Concurrent callers must not inject the same context revisions.
	rt.startMu.Lock()
	// getRuntimeLocked runs before startMu. Another caller may have timed out a
	// mutating Thread RPC while this caller was waiting for serialization, so
	// re-check the per-host fence at the actual control boundary.
	if err := h.verifyRuntimeThreadControl(agentID, rt); err != nil {
		rt.startMu.Unlock()
		return SendResult{}, err
	}
	if err := waitReady(rt); err != nil {
		rt.startMu.Unlock()
		return SendResult{}, errf(500, "codex not ready: %s", err)
	}
	// A shared app-server may unload an idle Thread. Resume immediately before
	// every Turn so Web, CLI and queued deliveries do not depend on a stale
	// in-memory binding left by an earlier request.
	if err := h.resumeAgentThread(agentID, rt); err != nil {
		rt.startMu.Unlock()
		return SendResult{}, err
	}
	contextPlan, err := h.prepareTurnContext(agentID, source, artifacts)
	if err != nil {
		rt.startMu.Unlock()
		return SendResult{}, err
	}
	if contextPlan.DeveloperContext != "" {
		if err := h.injectDeveloperContext(agentID, rt, contextPlan.DeveloperContext); err != nil {
			rt.startMu.Unlock()
			return SendResult{}, err
		}
	}
	input := codexArtifactInput(text, contextPlan.InputContext, artifacts)
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(503, "CodexLoom is shutting down")
	}
	if h.isDrainingLocked() {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(409, "CodexLoom is draining for restart")
	}
	if h.providerSwitching {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(409, "CodexLoom is switching an Agent Provider")
	}
	meta = h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(404, "agent vanished")
	}
	if h.agentCwdUpdatePendingLocked(agentID) || h.runtimes[agentID] != rt {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(409, "agent %q runtime changed before Turn start; retry", meta.Name)
	}
	if rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(409, "agent %q is already running a task", meta.Name)
	}
	turn := &turnState{
		task:           taskText,
		source:         turnSource(inboxItemID, agentMessageID),
		inboxItemID:    inboxItemID,
		attemptID:      attemptID,
		agentMessageID: agentMessageID,
		topicID:        topicID,
		contextAttemptID: func() string {
			if contextPlan.Attempt == nil {
				return ""
			}
			return contextPlan.Attempt.ID
		}(),
		contextEpochID: func() string {
			if contextPlan.Attempt == nil {
				return ""
			}
			return contextPlan.Attempt.EpochID
		}(),
		startedAt:    time.Now(),
		lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	previous := *meta
	meta.Source = "" // adopting an edge mirror into CodexLoom's own registry
	meta.Status = "running"
	meta.CurrentTask = taskText
	meta.CurrentTurnID = ""
	meta.LastError = ""
	meta.UpdatedAt = now()
	if err := h.persistAgentsLocked(); err != nil {
		*meta = previous
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(500, "persist Turn start: %s", err)
	}
	rt.activeTurn = turn
	h.emitLocked(agentID, "loom/user-message", map[string]any{"text": visibleText, "attachments": artifacts, "topicId": topicID})
	h.emitStatusLocked(meta, "running")
	threadID, approvalPolicy, sandbox, model, effort := meta.ThreadID, meta.ApprovalPolicy, meta.Sandbox, meta.Model, meta.Effort
	h.mu.Unlock()
	rt.startMu.Unlock()

	h.startWorker(func() { h.watchdog(agentID, turn, inactivity) })

	params := map[string]any{
		"threadId":       threadID,
		"input":          input,
		"approvalPolicy": approvalPolicy,
		"sandboxPolicy":  codexSandboxPolicy(sandbox),
	}
	if model != "" {
		params["model"] = model
	}
	if effort != "" {
		params["effort"] = effort
	}
	startTurn := func() (json.RawMessage, error) {
		return rt.client.Request("turn/start", params, 30*time.Second)
	}
	result, err := startTurn()
	if err != nil && isThreadNotFoundError(err) {
		// The Thread can be evicted between resume and turn/start. Keep the
		// already-reserved Turn and retry this idempotent pre-start sequence once.
		if resumeErr := h.resumeAgentThread(agentID, rt); resumeErr == nil {
			result, err = startTurn()
		} else {
			err = fmt.Errorf("%v; retry %v", err, resumeErr)
		}
	}
	if err != nil {
		h.mu.Lock()
		if m := h.agents[agentID]; m != nil {
			h.finishTurnLocked(m, rt, "failed", "turn/start failed: "+err.Error())
		}
		h.mu.Unlock()
		return SendResult{}, errf(500, "turn/start failed: %s", err)
	}
	var parsed struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
		TurnID string `json:"turnId"`
		ID     string `json:"id"`
	}
	_ = json.Unmarshal(result, &parsed)
	turnID := parsed.Turn.ID
	if turnID == "" {
		turnID = parsed.TurnID
	}
	if turnID == "" {
		turnID = parsed.ID
	}

	h.mu.Lock()
	if turnID != "" && turn.turnID == "" && !turn.finished {
		turn.turnID = turnID
		h.markInboxAttemptRunningLocked(turn)
		if m := h.agents[agentID]; m != nil {
			m.CurrentTurnID = turnID
			h.persistRuntimeProjectionLocked()
		}
	}
	if agentMessageID != "" && turnID != "" && !turn.finished {
		if err := h.markAgentMessageHandlingRunningLocked(turn, agentID); err != nil {
			log.Printf("[codex-loom] save started message handling %s: %v", agentMessageID, err)
		}
	}
	h.emitLocked(agentID, "loom/turn-started", map[string]any{
		"turnId": turn.turnID, "task": taskText, "source": turn.source, "topicId": topicID,
		"providerId": publicProviderID(meta.ProviderID), "model": meta.Model,
	})
	if topicID != "" {
		h.recordTopicWorkEventLocked(topicID, TopicEvent{Type: "turn_started", Actor: meta.Name, AgentID: agentID, Agent: meta.Name, Summary: summarizeTopicText(taskText), Ref: &TopicRef{Type: "turn", ID: turn.turnID, Label: meta.Name}, CreatedAt: now()})
	}
	canonicalTurnID := turn.turnID
	h.mu.Unlock()

	h.markContextSubmitted(threadID, contextPlan.Attempt, canonicalTurnID)
	return SendResult{Dispatched: true, AgentID: agentID, SessionID: agentID, TurnID: canonicalTurnID}, nil
}

func turnSource(inboxItemID, agentMessageID string) string {
	if inboxItemID != "" {
		return "external"
	}
	if agentMessageID != "" {
		return "internal"
	}
	return "owner"
}

func codexTaskText(text string, artifacts []ThreadArtifact) string {
	taskText := strings.TrimSpace(text)
	if taskText == "" {
		names := make([]string, 0, len(artifacts))
		for _, artifact := range artifacts {
			names = append(names, artifact.Name)
		}
		taskText = "Attached: " + strings.Join(names, ", ")
	}
	return taskText
}

func codexArtifactInput(text, loomContext string, artifacts []ThreadArtifact) []map[string]any {
	input := make([]map[string]any, 0, len(artifacts)+2)
	original := strings.TrimSpace(text)
	if original == "" && len(artifacts) > 0 {
		original = "Review the attached files."
	}
	if original != "" {
		input = append(input, map[string]any{"type": "text", "text": original})
	}
	if strings.TrimSpace(loomContext) != "" {
		input = append(input, map[string]any{"type": "text", "text": loomContext})
	}
	for _, artifact := range artifacts {
		if strings.HasPrefix(strings.ToLower(artifact.MimeType), "image/") {
			input = append(input, map[string]any{"type": "localImage", "path": artifact.Path})
		}
	}
	return input
}

func isThreadNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "thread not found") ||
		(strings.Contains(message, "thread") && strings.Contains(message, "not found"))
}

func (h *Hub) watchdog(agentID string, turn *turnState, inactivity time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-turn.stopWatchdog:
			return
		case <-ticker.C:
			h.mu.Lock()
			finished := turn.finished
			idle := time.Since(turn.lastActivity)
			total := time.Since(turn.startedAt)
			h.mu.Unlock()
			if finished {
				return
			}
			if idle > inactivity {
				_, _ = h.Interrupt(agentID, fmt.Sprintf("inactivity timeout (%s)", inactivity))
				return
			}
			if total > absoluteTurnCap {
				_, _ = h.Interrupt(agentID, "absolute turn cap (4h)")
				return
			}
		}
	}
}

type InterruptResult struct {
	Interrupted   bool   `json:"interrupted"`
	Message       string `json:"message,omitempty"`
	Reason        string `json:"reason,omitempty"`
	HeldMessageID string `json:"heldMessageId,omitempty"`
	HeldSubject   string `json:"heldSubject,omitempty"`
}

func activeTurnInterruptMismatch(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	const prefix = "expected active turn id "
	const separator = " but found "
	message := strings.TrimSpace(err.Error())
	rest, ok := strings.CutPrefix(message, prefix)
	if !ok {
		return "", false
	}
	_, actualTurnID, ok := strings.Cut(rest, separator)
	actualTurnID = strings.TrimSpace(actualTurnID)
	if !ok || actualTurnID == "" || strings.ContainsAny(actualTurnID, " \t\r\n") {
		return "", false
	}
	return actualTurnID, true
}

func (h *Hub) Interrupt(key, reason string) (InterruptResult, error) {
	if reason == "" {
		reason = "interrupted by caller"
	}
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return InterruptResult{}, errf(404, "agent not found: %s", key)
	}
	rt := h.runtimes[meta.ID]
	if rt == nil || rt.activeTurn == nil || rt.activeTurn.finished {
		if meta.Status == "running" {
			previous := *meta
			meta.Status = "idle"
			meta.CurrentTask = ""
			meta.CurrentTurnID = ""
			meta.LastError = reason
			meta.UpdatedAt = now()
			if err := h.persistAgentsLocked(); err != nil {
				*meta = previous
				h.mu.Unlock()
				return InterruptResult{}, errf(500, "persist stale Turn repair: %s", err)
			}
			h.emitStatusLocked(meta, "idle")
		}
		h.mu.Unlock()
		return InterruptResult{Interrupted: false, Message: "no active task"}, nil
	}
	turn := rt.activeTurn
	agentID := meta.ID
	threadID := meta.ThreadID
	turnID := turn.turnID
	client := rt.client
	heldMessageID := turn.agentMessageID
	heldSubject := ""
	if message := h.comms[heldMessageID]; message != nil {
		heldSubject = message.Subject
	}
	h.mu.Unlock()

	if turnID == "" {
		return InterruptResult{}, errf(409, "active Turn is still starting; retry shortly")
	}
	interrupt := func(targetTurnID string) error {
		_, err := client.Request("turn/interrupt", map[string]any{
			"threadId": threadID,
			"turnId":   targetTurnID,
		}, 10*time.Second)
		return err
	}
	err := interrupt(turnID)
	if actualTurnID, mismatch := activeTurnInterruptMismatch(err); mismatch && actualTurnID != turnID {
		h.mu.Lock()
		currentMeta := h.agents[agentID]
		currentRuntime := h.runtimes[agentID]
		var retryTurn *turnState
		if currentMeta != nil && currentRuntime == rt && rt.activeTurn != nil && !rt.activeTurn.finished {
			current := rt.activeTurn
			switch {
			case current.turnID == actualTurnID:
				retryTurn = current
			case current.turnID == turnID && !current.startedConfirmed:
				h.rebindActiveTurnIDLocked(currentMeta, current, actualTurnID)
				current.startedConfirmed = true
				retryTurn = current
			case current.turnID == turnID:
				h.finishTurnLocked(currentMeta, rt, "interrupted", "superseded by active Turn "+actualTurnID)
				retryTurn = h.adoptRemoteTurnLocked(currentMeta, rt, actualTurnID)
			}
		}
		h.mu.Unlock()
		if retryTurn != nil {
			turn = retryTurn
			turnID = actualTurnID
			heldMessageID = retryTurn.agentMessageID
			heldSubject = ""
			if heldMessageID != "" {
				h.mu.Lock()
				if message := h.comms[heldMessageID]; message != nil {
					heldSubject = message.Subject
				}
				h.mu.Unlock()
			}
			err = interrupt(actualTurnID)
		}
	}
	if err != nil {
		return InterruptResult{}, errf(500, "turn/interrupt failed: %s", err)
	}
	// codex should follow up with turn/completed(status=interrupted); force
	// the bookkeeping if that doesn't arrive shortly.
	h.startWorker(func() {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-turn.stopWatchdog:
			return
		case <-timer.C:
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if !turn.finished && rt.activeTurn == turn {
			if m := h.agents[agentID]; m != nil {
				h.finishTurnLocked(m, rt, "interrupted", reason)
			}
		}
	})
	return InterruptResult{Interrupted: true, Reason: reason, HeldMessageID: heldMessageID, HeldSubject: heldSubject}, nil
}

func (h *Hub) ArchiveAgent(key string) (map[string]any, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	agentID := meta.ID
	for _, topic := range h.topics {
		if topic != nil && topic.Status != TopicStatusArchived && topicHasAgent(topic, agentID, meta.Name) {
			h.mu.Unlock()
			return nil, errf(409, "agent %s is part of Topic %s (%s); archive the Topic or remove the participant first", meta.Name, topic.ID, topic.Title)
		}
	}
	for _, group := range h.collaborationGroups {
		if group == nil || group.Status != CollaborationGroupStatusActive {
			continue
		}
		for _, memberAgentID := range group.MemberAgentIDs {
			if memberAgentID == agentID {
				h.mu.Unlock()
				return nil, errf(409, "agent %s is part of active Collaboration Group %s (%s); archive or update the Group first", meta.Name, group.ID, group.Name)
			}
		}
	}
	rt := h.runtimes[agentID]
	hasActive := rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished
	threadID := meta.ThreadID
	name := meta.Name
	h.mu.Unlock()

	if hasActive {
		_, _ = h.Interrupt(agentID, "agent archived")
	}
	h.mu.Lock()
	current, ok := h.agents[agentID]
	if !ok {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	killed := *current
	delete(h.agents, agentID)
	if err := h.persistAgentsLocked(); err != nil {
		h.agents[agentID] = current
		h.mu.Unlock()
		return nil, errf(500, "save archived agent: %s", err)
	}
	delete(h.runtimes, agentID)
	delete(h.goals, agentID)
	h.emitLocked(agentID, "loom/agent-archived", map[string]any{"id": agentID, "name": name})
	killed.Status = "killed"
	h.emitStatusLocked(&killed, "killed")
	h.mu.Unlock()

	// Codex Thread archival is a consequence of the committed Loom state. A
	// failure here does not resurrect the governance entity.
	if rt != nil && !rt.client.Closed() && waitReady(rt) == nil {
		_, _ = rt.client.Request("thread/archive", map[string]any{"threadId": threadID}, 10*time.Second)
	}
	return map[string]any{"archived": true, "killed": true, "id": agentID, "name": name}, nil
}

// KillSession is the pre-CodexLoom compatibility method.
func (h *Hub) KillSession(key string) (map[string]any, error) { return h.ArchiveAgent(key) }

// ---- history (read from codex rollout files) ----
//
// History is NOT reconstructed from CodexLoom's own event log. The real,
// complete history of any Agent lives in the Codex rollout file that
// `codex app-server` writes for its thread; we read it directly (see the
// rollout package). This means imported/adopted agents show their full
// history immediately, and no "migration/conversion" step exists. Live events
// (from an Agent CodexLoom is actively driving) still flow through the store
// event log for real-time SSE broadcast — but historical viewing always reads
// the rollout, so a non-driven Agent is fully viewable too.

type HistoryTurn struct {
	ID             string              `json:"id"`
	Status         string              `json:"status"`
	Items          []map[string]any    `json:"items"`
	Model          string              `json:"model,omitempty"`
	Usage          *rollout.TokenUsage `json:"usage,omitempty"`
	UsageUpdatedAt string              `json:"usageUpdatedAt,omitempty"`
}

type History struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Cwd      string        `json:"cwd"`
	ThreadID string        `json:"threadId"`
	Status   string        `json:"status"`
	Total    int           `json:"total"` // total turns in the rollout (for scroll-up paging)
	Turns    []HistoryTurn `json:"turns"`
}

type TurnReference struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	TopicID string `json:"topicId,omitempty"`
}

type TurnDetail struct {
	ID             string              `json:"id"`
	AgentID        string              `json:"agentId"`
	Agent          string              `json:"agent"`
	ThreadID       string              `json:"threadId"`
	Cwd            string              `json:"cwd"`
	Status         string              `json:"status"`
	StartedAt      string              `json:"startedAt,omitempty"`
	CompletedAt    string              `json:"completedAt,omitempty"`
	Model          string              `json:"model,omitempty"`
	Usage          *rollout.TokenUsage `json:"usage,omitempty"`
	UsageUpdatedAt string              `json:"usageUpdatedAt,omitempty"`
	Source         *TurnReference      `json:"source,omitempty"`
	Error          string              `json:"error,omitempty"`
	Items          []map[string]any    `json:"items"`
}

// GetTurn locates a Turn globally by its stable Codex Turn ID. Rollout files
// remain the history source of truth; Loom contributes the owning Agent and
// any durable work reference that started the Turn.
func (h *Hub) GetTurn(turnID string) (TurnDetail, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return TurnDetail{}, errf(400, "turn id is required")
	}

	h.mu.Lock()
	agents := make([]AgentView, 0, len(h.agents))
	for _, agent := range h.agents {
		if agent.ThreadID != "" {
			agents = append(agents, h.viewLocked(agent))
		}
	}
	h.mu.Unlock()
	for i := range agents {
		applyRolloutStatus(&agents[i])
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return agents[i].Name < agents[j].Name
		}
		return agents[i].ID < agents[j].ID
	})

	var lookupErr error
	for _, agent := range agents {
		turn, err := rollout.ReadTurn(agent.ThreadID, turnID)
		if err != nil {
			if !errors.Is(err, rollout.ErrTurnNotFound) && !errors.Is(err, rollout.ErrRolloutNotFound) && lookupErr == nil {
				lookupErr = fmt.Errorf("Agent %s (%s): %w", agent.Name, agent.ThreadID, err)
			}
			continue
		}
		items := turn.Items
		if items == nil {
			items = []map[string]any{}
		}
		detail := TurnDetail{
			ID: turn.ID, AgentID: agent.ID, Agent: agent.Name, ThreadID: agent.ThreadID,
			Cwd: agent.Cwd, Status: turn.Status, Items: items,
		}
		if detail.Status == "running" && (agent.Status != "running" || agent.CurrentTurnID != turnID) {
			detail.Status = "interrupted"
		}
		if report, usageErr := rollout.ReadUsage(agent.ThreadID); usageErr == nil {
			for _, activity := range report.Activity {
				if activity.TurnID != turnID {
					continue
				}
				detail.StartedAt = activity.StartedAt
				detail.CompletedAt = activity.EndedAt
				if detail.Status == "running" && activity.Status != "running" {
					detail.Status = activity.Status
				}
				break
			}
			for _, usage := range report.Turns {
				if usage.TurnID != turnID {
					continue
				}
				copy := usage.Usage
				detail.Model = usage.Model
				detail.Usage = &copy
				detail.UsageUpdatedAt = usage.LastUpdatedAt
				break
			}
		}
		if detail.StartedAt == "" && len(items) > 0 {
			detail.StartedAt, _ = items[0]["timestamp"].(string)
		}
		if detail.CompletedAt == "" && detail.Status != "running" && len(items) > 0 {
			detail.CompletedAt, _ = items[len(items)-1]["timestamp"].(string)
		}
		h.mu.Lock()
		detail.Source, detail.Error = h.turnReferenceLocked(agent.ID, turnID)
		if detail.Error == "" && agent.LastTurn != nil && agent.LastTurn.TurnID == turnID && agent.LastError != "" {
			detail.Error = agent.LastError
		}
		h.mu.Unlock()
		return detail, nil
	}
	if lookupErr != nil {
		return TurnDetail{}, errf(500, "turn lookup failed: %v", lookupErr)
	}
	return TurnDetail{}, errf(404, "turn not found: %s", turnID)
}

func (h *Hub) turnReferenceLocked(agentID, turnID string) (*TurnReference, string) {
	for _, attempt := range h.attempts {
		if attempt == nil || attempt.AgentID != agentID || attempt.TurnID != turnID {
			continue
		}
		return &TurnReference{Kind: "external", ID: attempt.InboxItemID}, attempt.Error
	}
	for _, request := range h.humanRequests {
		if request != nil && request.AgentID == agentID && request.ResumedTurnID == turnID {
			return &TurnReference{Kind: "needs_you", ID: request.ID, TopicID: request.TopicID}, request.LastError
		}
	}
	for _, messageID := range h.commOrder {
		message := h.comms[messageID]
		if message == nil || message.ToAgentID != agentID {
			continue
		}
		matched := message.DeliveryMode == "turn_start" && message.DeliveredTurnID == turnID
		for _, attempt := range message.HandlingAttempts {
			if attempt.TurnID == turnID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		kind := "internal"
		switch {
		case message.TriggerID != "":
			kind = "trigger"
		case message.ScheduleID != "":
			kind = "schedule"
		}
		return &TurnReference{Kind: kind, ID: message.ID, TopicID: message.TopicID}, message.LastHandlingError
	}
	return nil, ""
}

func (h *Hub) History(key string, count, offset int) (History, error) {
	if count <= 0 {
		count = 10
	}
	if offset < 0 {
		offset = 0
	}
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return History{}, errf(404, "agent not found: %s", key)
	}
	view := h.viewLocked(meta)
	h.mu.Unlock()
	applyRolloutStatus(&view)

	threadID := view.ThreadID
	hist := History{ID: view.ID, Name: view.Name, Cwd: view.Cwd, ThreadID: threadID, Status: view.Status}

	if threadID == "" {
		return hist, nil // no thread started yet → no rollout, no history
	}

	tr, total, err := rollout.ReadWindow(threadID, count, offset)
	if err != nil {
		// No rollout on disk (e.g. a new Agent before its first Turn is
		// flushed). Not an error: report empty history for this Agent.
		log.Printf("[codex-loom] history: no rollout for %s (thread %s): %v", view.Name, threadID, err)
		return hist, nil
	}
	all := tr.Turns
	hist.Total = total
	if len(all) > 0 && all[len(all)-1].Status == "running" && hist.Status != "running" {
		if latest, err := rollout.LatestTurn(threadID); err == nil && latest != nil && latest.Status == "running" && externalTurnLooksLive(threadID, latest.UpdatedAt) {
			hist.Status = "running"
		} else {
			all[len(all)-1].Status = "interrupted"
		}
	}
	turns := all
	usageByTurn := map[string]rollout.TurnUsage{}
	if report, usageErr := rollout.ReadUsage(threadID); usageErr == nil {
		for _, turn := range report.Turns {
			usageByTurn[turn.TurnID] = turn
		}
	}
	for _, t := range turns {
		items := t.Items
		if items == nil {
			items = []map[string]any{}
		}
		turn := HistoryTurn{ID: t.ID, Status: t.Status, Items: items}
		if usage, ok := usageByTurn[t.ID]; ok {
			copy := usage.Usage
			turn.Model = usage.Model
			turn.Usage = &copy
			turn.UsageUpdatedAt = usage.LastUpdatedAt
		}
		hist.Turns = append(hist.Turns, turn)
	}
	return hist, nil
}

// Shutdown closes all codex processes. Running agents keep status=running
// on disk so the next startup marks them interrupted.
