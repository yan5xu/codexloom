package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	githubapi "github.com/yan5xu/codex-loom/internal/github"
)

const (
	triggerIdentity        = "trigger"
	triggerAgentID         = "system:trigger"
	triggerPollConcurrency = 4
)

type Trigger struct {
	ID                string             `json:"id"`
	AgentID           string             `json:"agentId"`
	Agent             string             `json:"agent"`
	ConnectionID      string             `json:"connectionId"`
	Provider          string             `json:"provider"`
	ResourceKind      string             `json:"resourceKind"`
	Subject           map[string]string  `json:"subject"`
	Conditions        []TriggerCondition `json:"conditions"`
	ResumeInstruction string             `json:"resumeInstruction"`
	Work              TriggerWorkAnchor  `json:"work"`
	State             string             `json:"state"`
	ExpiresAt         string             `json:"expiresAt,omitempty"`
	LastObservedAt    string             `json:"lastObservedAt,omitempty"`
	LastEventKey      string             `json:"lastEventKey,omitempty"`
	LastMessageID     string             `json:"lastMessageId,omitempty"`
	LastError         string             `json:"lastError,omitempty"`
	Version           int                `json:"version"`
	CreatedAt         string             `json:"createdAt"`
	UpdatedAt         string             `json:"updatedAt"`
}

type TriggerCondition struct {
	Event      string            `json:"event"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type TriggerWorkAnchor struct {
	AgentID            string `json:"agentId"`
	ThreadIDAtCreation string `json:"threadIdAtCreation,omitempty"`
	SourceTurnID       string `json:"sourceTurnId,omitempty"`
	GoalCreatedAt      int64  `json:"goalCreatedAt,omitempty"`
	TopicID            string `json:"topicId,omitempty"`
}

type TriggerParams struct {
	Agent             string             `json:"agent"`
	ConnectionID      string             `json:"connectionId"`
	Provider          string             `json:"provider"`
	ResourceKind      string             `json:"resourceKind"`
	Subject           map[string]string  `json:"subject"`
	Conditions        []TriggerCondition `json:"conditions"`
	ResumeInstruction string             `json:"resumeInstruction"`
	ExpiresAt         string             `json:"expiresAt"`
	TopicID           string             `json:"topicId,omitempty"`
}

type TriggerEvent struct {
	Provider          string            `json:"provider"`
	ConnectionID      string            `json:"connectionId"`
	ResourceKind      string            `json:"resourceKind"`
	SubjectKey        string            `json:"subjectKey"`
	Event             string            `json:"event"`
	EventKey          string            `json:"eventKey"`
	OccurredAt        string            `json:"occurredAt,omitempty"`
	ObservedAt        string            `json:"observedAt"`
	Summary           string            `json:"summary"`
	ResumeInstruction string            `json:"resumeInstruction"`
	Snapshot          map[string]any    `json:"snapshot,omitempty"`
	Work              TriggerWorkAnchor `json:"work,omitempty"`
}

type TriggerObservation struct {
	Matched    bool
	Permanent  bool
	ObservedAt string
	Event      TriggerEvent
}

type triggerObserver func(context.Context, PlatformConnection, Trigger) (TriggerObservation, error)

func (h *Hub) persistTriggersLocked() error {
	return h.st.SaveTriggers(h.triggers)
}

func (h *Hub) ListTriggers(agent, state string) []Trigger {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if value := h.resolveLocked(strings.TrimSpace(agent)); value != nil {
		agentID = value.ID
	}
	state = strings.TrimSpace(state)
	out := make([]Trigger, 0, len(h.triggers))
	for _, trigger := range h.triggers {
		if trigger == nil || agent != "" && trigger.AgentID != agentID && trigger.AgentID != agent && trigger.Agent != agent {
			continue
		}
		if state != "" && trigger.State != state {
			continue
		}
		out = append(out, cloneTrigger(*trigger))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return triggerStateOrder(out[i].State) < triggerStateOrder(out[j].State)
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func triggerStateOrder(state string) int {
	switch state {
	case "pending", "armed":
		return 0
	case "paused":
		return 1
	default:
		return 2
	}
}

func (h *Hub) GetTrigger(id string) (Trigger, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	trigger := h.triggers[strings.TrimSpace(id)]
	if trigger == nil {
		return Trigger{}, errf(404, "trigger not found: %s", id)
	}
	return cloneTrigger(*trigger), nil
}

func (h *Hub) CreateTrigger(p TriggerParams) (Trigger, error) {
	agentKey := strings.TrimSpace(p.Agent)
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	resourceKind := strings.ToLower(strings.TrimSpace(p.ResourceKind))
	resume := strings.TrimSpace(p.ResumeInstruction)
	if agentKey == "" || provider == "" || resourceKind == "" || resume == "" {
		return Trigger{}, errf(400, "agent, provider, resourceKind, and resumeInstruction are required")
	}
	conditions, err := normalizeTriggerConditions(provider, resourceKind, p.Conditions)
	if err != nil {
		return Trigger{}, err
	}
	subject, err := normalizeTriggerSubject(provider, resourceKind, p.Subject)
	if err != nil {
		return Trigger{}, err
	}
	if err := validateTriggerDefinition(provider, resourceKind, subject, conditions); err != nil {
		return Trigger{}, err
	}
	expiresAt, err := normalizeTriggerExpiry(p.ExpiresAt)
	if err != nil {
		return Trigger{}, errf(400, "invalid expiresAt: %s", err)
	}

	h.mu.Lock()
	agent := h.resolveLocked(agentKey)
	if agent == nil {
		h.mu.Unlock()
		return Trigger{}, errf(404, "agent not found: %s", agentKey)
	}
	connection, err := h.resolveTriggerConnectionLocked(provider, strings.TrimSpace(p.ConnectionID), subject)
	if err != nil {
		h.mu.Unlock()
		return Trigger{}, err
	}
	timestamp := now()
	trigger := Trigger{
		ID: newTriggerID(), AgentID: agent.ID, Agent: agent.Name,
		ConnectionID: connection.ID, Provider: provider, ResourceKind: resourceKind,
		Subject: subject, Conditions: conditions, ResumeInstruction: resume,
		Work:  TriggerWorkAnchor{AgentID: agent.ID, ThreadIDAtCreation: agent.ThreadID},
		State: "pending", ExpiresAt: expiresAt, Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if p.TopicID != "" {
		topic := h.topics[strings.TrimSpace(p.TopicID)]
		if topic == nil || !topicHasAgent(topic, agent.ID, agent.ID) {
			h.mu.Unlock()
			return Trigger{}, errf(403, "Agent %s is not part of Topic %s", agent.Name, p.TopicID)
		}
		trigger.Work.TopicID = topic.ID
	}
	if rt := h.runtimes[agent.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		trigger.Work.SourceTurnID = rt.activeTurn.turnID
		if trigger.Work.TopicID == "" {
			trigger.Work.TopicID = rt.activeTurn.topicID
		}
	}
	if goal := h.goals[agent.ID]; goal != nil {
		trigger.Work.GoalCreatedAt = goal.CreatedAt
	}
	id := trigger.ID
	h.triggers[id] = &trigger
	if err := h.persistTriggersLocked(); err != nil {
		delete(h.triggers, id)
		h.mu.Unlock()
		return Trigger{}, errf(500, "persist trigger: %s", err)
	}
	if trigger.Work.TopicID != "" {
		h.recordTopicWorkEventLocked(trigger.Work.TopicID, TopicEvent{Type: "trigger_created", Actor: agent.Name, AgentID: agent.ID, Agent: agent.Name, Summary: trigger.ResumeInstruction, Ref: &TopicRef{Type: "trigger", ID: trigger.ID, Label: trigger.Provider + " " + trigger.ResourceKind}, CreatedAt: trigger.CreatedAt})
	}
	h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": trigger})
	h.mu.Unlock()

	h.startWorker(func() { h.observeTriggerNow(id) })
	return h.GetTrigger(id)
}

func (h *Hub) resolveTriggerConnectionLocked(provider, requested string, subject map[string]string) (*PlatformConnection, error) {
	if requested != "" {
		connection := h.connections[requested]
		if connection == nil || connection.ArchivedAt != "" {
			return nil, errf(404, "connection not found: %s", requested)
		}
		if connection.Provider != provider {
			return nil, errf(400, "connection %s belongs to %s, not %s", requested, connection.Provider, provider)
		}
		if !connection.Enabled {
			return nil, errf(409, "connection %s is disabled", requested)
		}
		return connection, nil
	}
	matches := make([]*PlatformConnection, 0)
	for _, connection := range h.connections {
		if connection.Provider != provider || !connection.Enabled || connection.ArchivedAt != "" {
			continue
		}
		matches = append(matches, connection)
	}
	if len(matches) == 0 {
		return nil, errf(409, "no enabled %s connection; connect the provider first", provider)
	}
	if provider == "github" {
		owner := strings.ToLower(strings.TrimSpace(subject["owner"]))
		exact := make([]*PlatformConnection, 0)
		broad := make([]*PlatformConnection, 0)
		legacy := make([]*PlatformConnection, 0)
		for _, connection := range matches {
			scope := strings.ToLower(strings.TrimSpace(connection.ScopeRef))
			switch {
			case scope == owner:
				exact = append(exact, connection)
			case scope == "*":
				broad = append(broad, connection)
			case scope == "":
				legacy = append(legacy, connection)
			}
		}
		if len(exact) == 1 {
			return exact[0], nil
		}
		if len(exact) > 1 {
			return nil, errf(409, "multiple GitHub connections cover resource owner %s; specify connectionId", owner)
		}
		if len(broad) == 1 {
			return broad[0], nil
		}
		if len(broad) > 1 {
			return nil, errf(409, "multiple broad GitHub connections exist; specify connectionId")
		}
		if len(legacy) == 1 {
			return legacy[0], nil
		}
		return nil, errf(409, "no enabled GitHub connection covers resource owner %s", owner)
	}
	if len(matches) > 1 {
		return nil, errf(409, "multiple %s connections exist; specify connectionId", provider)
	}
	return matches[0], nil
}

func normalizeTriggerConditions(provider, resourceKind string, input []TriggerCondition) ([]TriggerCondition, error) {
	if len(input) == 0 {
		return nil, errf(400, "at least one trigger condition is required")
	}
	allowed := map[string]bool{}
	if provider == "github" && resourceKind == "pull-request" {
		allowed = map[string]bool{"merged": true, "closed": true, "head-changed": true}
	} else if provider == "github" && resourceKind == "workflow-run" {
		allowed = map[string]bool{"completed": true, "success": true, "failure": true, "cancelled": true}
	} else {
		return nil, errf(400, "unsupported trigger resource: %s %s", provider, resourceKind)
	}
	seen := map[string]bool{}
	out := make([]TriggerCondition, 0, len(input))
	for _, condition := range input {
		event := strings.ToLower(strings.TrimSpace(condition.Event))
		if !allowed[event] {
			return nil, errf(400, "unsupported %s %s event: %s", provider, resourceKind, event)
		}
		if seen[event] {
			continue
		}
		seen[event] = true
		params := map[string]string{}
		for key, value := range condition.Parameters {
			if key = strings.TrimSpace(key); key != "" {
				params[key] = strings.TrimSpace(value)
			}
		}
		out = append(out, TriggerCondition{Event: event, Parameters: params})
	}
	return out, nil
}

func normalizeTriggerSubject(provider, resourceKind string, input map[string]string) (map[string]string, error) {
	if provider != "github" {
		return nil, errf(400, "unsupported trigger provider: %s", provider)
	}
	owner := strings.TrimSpace(input["owner"])
	repo := strings.TrimSpace(input["repo"])
	if owner == "" || repo == "" {
		return nil, errf(400, "GitHub trigger subject requires owner and repo")
	}
	if strings.ContainsAny(owner, "/: \t\r\n") || strings.ContainsAny(repo, "/: \t\r\n") {
		return nil, errf(400, "invalid GitHub trigger owner or repo")
	}
	result := map[string]string{"owner": owner, "repo": repo}
	switch resourceKind {
	case "pull-request":
		number, err := strconv.Atoi(strings.TrimSpace(input["number"]))
		if err != nil || number <= 0 {
			return nil, errf(400, "GitHub pull-request subject requires a positive number")
		}
		result["number"] = strconv.Itoa(number)
		if expected := strings.TrimSpace(input["expectedHead"]); expected != "" {
			result["expectedHead"] = expected
		}
	case "workflow-run":
		runID, err := strconv.ParseInt(strings.TrimSpace(input["runId"]), 10, 64)
		if err != nil || runID <= 0 {
			return nil, errf(400, "GitHub workflow-run subject requires a positive runId")
		}
		result["runId"] = strconv.FormatInt(runID, 10)
	default:
		return nil, errf(400, "unsupported GitHub resource: %s", resourceKind)
	}
	return result, nil
}

func validateTriggerDefinition(provider, resourceKind string, subject map[string]string, conditions []TriggerCondition) error {
	if provider != "github" || resourceKind != "pull-request" {
		return nil
	}
	expectedHead := strings.TrimSpace(subject["expectedHead"])
	for _, condition := range conditions {
		if condition.Event != "head-changed" {
			continue
		}
		if expectedHead == "" {
			expectedHead = strings.TrimSpace(condition.Parameters["expectedHead"])
		}
		if expectedHead == "" {
			return errf(400, "GitHub head-changed trigger requires expectedHead")
		}
		subject["expectedHead"] = expectedHead
	}
	return nil
}

func normalizeTriggerExpiry(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var value time.Time
	var err error
	if strings.HasSuffix(raw, "d") {
		days, parseErr := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if parseErr != nil || days <= 0 {
			return "", fmt.Errorf("duration must be a positive number of days")
		}
		value = time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	} else if duration, parseErr := time.ParseDuration(raw); parseErr == nil {
		if duration <= 0 {
			return "", fmt.Errorf("duration must be positive")
		}
		value = time.Now().UTC().Add(duration)
	} else {
		value, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return "", err
		}
	}
	if !value.After(time.Now().UTC()) {
		return "", fmt.Errorf("expiry must be in the future")
	}
	return value.UTC().Format(time.RFC3339Nano), nil
}

func (h *Hub) PauseTrigger(id string) (Trigger, error) {
	return h.setTriggerState(id, "paused")
}

func (h *Hub) ResumeTrigger(id string) (Trigger, error) {
	trigger, err := h.setTriggerState(id, "armed")
	if err != nil {
		return Trigger{}, err
	}
	h.startWorker(func() { h.observeTriggerNow(trigger.ID) })
	return h.GetTrigger(trigger.ID)
}

func (h *Hub) CancelTrigger(id string) (Trigger, error) {
	return h.setTriggerState(id, "cancelled")
}

func (h *Hub) setTriggerState(id, state string) (Trigger, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	trigger := h.triggers[strings.TrimSpace(id)]
	if trigger == nil {
		return Trigger{}, errf(404, "trigger not found: %s", id)
	}
	if state == "armed" && triggerExpiredAt(trigger, time.Now().UTC()) {
		previous := cloneTrigger(*trigger)
		trigger.State = "expired"
		trigger.Version++
		trigger.UpdatedAt = now()
		if err := h.persistTriggersLocked(); err != nil {
			*trigger = previous
			return Trigger{}, errf(500, "persist expired trigger: %s", err)
		}
		h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": cloneTrigger(*trigger)})
		return Trigger{}, errf(409, "cannot resume expired trigger")
	}
	if trigger.State == state {
		return cloneTrigger(*trigger), nil
	}
	switch state {
	case "paused":
		if trigger.State != "pending" && trigger.State != "armed" {
			return Trigger{}, errf(409, "cannot pause trigger in state %s", trigger.State)
		}
	case "armed":
		if trigger.State != "paused" && trigger.State != "failed" {
			return Trigger{}, errf(409, "cannot resume trigger in state %s", trigger.State)
		}
	case "cancelled":
		if trigger.State == "triggered" || trigger.State == "cancelled" || trigger.State == "expired" {
			return Trigger{}, errf(409, "cannot cancel trigger in state %s", trigger.State)
		}
	}
	previous := *trigger
	if state == "armed" {
		h.rerouteLegacyGitHubTriggerLocked(trigger)
	}
	trigger.State = state
	trigger.LastError = ""
	trigger.Version++
	trigger.UpdatedAt = now()
	if err := h.persistTriggersLocked(); err != nil {
		*trigger = previous
		return Trigger{}, errf(500, "persist trigger: %s", err)
	}
	copy := cloneTrigger(*trigger)
	h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": copy})
	return copy, nil
}

func (h *Hub) rerouteLegacyGitHubTriggerLocked(trigger *Trigger) {
	if trigger == nil || trigger.Provider != "github" {
		return
	}
	current := h.connections[trigger.ConnectionID]
	if current != nil && strings.TrimSpace(current.ScopeRef) != "" {
		return
	}
	owner := strings.ToLower(strings.TrimSpace(trigger.Subject["owner"]))
	if owner == "" {
		return
	}
	var replacement *PlatformConnection
	for _, connection := range h.connections {
		if connection.Provider == "github" && connection.Enabled && connection.ArchivedAt == "" && strings.EqualFold(connection.ScopeRef, owner) {
			if replacement != nil {
				return
			}
			replacement = connection
		}
	}
	if replacement != nil {
		trigger.ConnectionID = replacement.ID
	}
}

func (h *Hub) triggerLoop() {
	interval := triggerPollInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	h.PollTriggersNow()
	for {
		select {
		case <-ticker.C:
			h.PollTriggersNow()
		case <-h.stop:
			return
		}
	}
}

func triggerPollInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("CODEX_LOOM_TRIGGER_POLL_INTERVAL")); raw != "" {
		if value, err := time.ParseDuration(raw); err == nil && value >= time.Second {
			return value
		}
	}
	return 30 * time.Second
}

func (h *Hub) PollTriggersNow() {
	h.mu.Lock()
	ids := make([]string, 0)
	previous := map[string]Trigger{}
	expired := make([]Trigger, 0)
	current := time.Now().UTC()
	for _, trigger := range h.triggers {
		if trigger == nil || trigger.State != "pending" && trigger.State != "armed" {
			continue
		}
		if trigger.ExpiresAt != "" {
			expires, err := time.Parse(time.RFC3339Nano, trigger.ExpiresAt)
			if err == nil && !expires.After(current) {
				previous[trigger.ID] = cloneTrigger(*trigger)
				trigger.State = "expired"
				trigger.Version++
				trigger.UpdatedAt = current.Format(time.RFC3339Nano)
				expired = append(expired, cloneTrigger(*trigger))
				continue
			}
		}
		ids = append(ids, trigger.ID)
	}
	if len(expired) > 0 {
		if err := h.persistTriggersLocked(); err != nil {
			log.Printf("[codex-loom] persist expired triggers: %v", err)
			for id, value := range previous {
				copy := value
				h.triggers[id] = &copy
			}
			expired = nil
		} else {
			for _, trigger := range expired {
				h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": trigger})
			}
		}
	}
	h.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	workers := triggerPollConcurrency
	if len(ids) < workers {
		workers = len(ids)
	}
	jobs := make(chan string)
	var group sync.WaitGroup
	group.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer group.Done()
			for id := range jobs {
				h.observeTriggerNow(id)
			}
		}()
	}
	for _, id := range ids {
		if h.stop == nil {
			jobs <- id
			continue
		}
		select {
		case jobs <- id:
		case <-h.stop:
			close(jobs)
			group.Wait()
			return
		}
	}
	close(jobs)
	group.Wait()
}

func (h *Hub) observeTriggerNow(id string) {
	h.mu.Lock()
	stored := h.triggers[id]
	if stored == nil || stored.State != "pending" && stored.State != "armed" {
		h.mu.Unlock()
		return
	}
	if h.triggerObservations == nil {
		h.triggerObservations = map[string]struct{}{}
	}
	if _, observing := h.triggerObservations[id]; observing {
		h.mu.Unlock()
		return
	}
	h.triggerObservations[id] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.triggerObservations, id)
		h.mu.Unlock()
	}()

	h.mu.Lock()
	stored = h.triggers[id]
	if stored == nil || stored.State != "pending" && stored.State != "armed" {
		h.mu.Unlock()
		return
	}
	if triggerExpiredAt(stored, time.Now().UTC()) {
		previous := cloneTrigger(*stored)
		stored.State = "expired"
		stored.Version++
		stored.UpdatedAt = now()
		if err := h.persistTriggersLocked(); err != nil {
			*stored = previous
			log.Printf("[codex-loom] persist expired trigger %s: %v", id, err)
		} else {
			h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": cloneTrigger(*stored)})
		}
		h.mu.Unlock()
		return
	}
	trigger := cloneTrigger(*stored)
	connection := h.connections[trigger.ConnectionID]
	if connection == nil || !connection.Enabled || connection.ArchivedAt != "" {
		h.mu.Unlock()
		h.recordTriggerError(id, fmt.Errorf("connection is unavailable"), false)
		return
	}
	connectionCopy := *connection
	observer := h.observeTrigger
	h.mu.Unlock()
	if observer == nil {
		observer = observeProviderTrigger
	}
	ctx, cancel := h.triggerObservationContext()
	observation, err := observer(ctx, connectionCopy, trigger)
	cancel()
	if err != nil {
		h.recordTriggerError(id, err, observation.Permanent)
		return
	}
	h.applyTriggerObservation(id, observation)
}

func (h *Hub) triggerObservationContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	if h.stop != nil {
		go func() {
			select {
			case <-h.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}

func (h *Hub) recordTriggerError(id string, cause error, permanent bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	trigger := h.triggers[id]
	if trigger == nil || trigger.State != "pending" && trigger.State != "armed" {
		return
	}
	message := cause.Error()
	if trigger.LastError == message && !permanent {
		return
	}
	previous := cloneTrigger(*trigger)
	trigger.LastError = message
	if permanent {
		trigger.State = "failed"
	}
	trigger.Version++
	trigger.UpdatedAt = now()
	if err := h.persistTriggersLocked(); err != nil {
		*trigger = previous
		return
	}
	h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": cloneTrigger(*trigger)})
}

func (h *Hub) applyTriggerObservation(id string, observation TriggerObservation) {
	// Resolve the current Connection before entering its lifecycle domain, then
	// re-read the Trigger under Hub.mu after the deterministic lock is held.
	h.mu.Lock()
	connectionID := ""
	if trigger := h.triggers[id]; trigger != nil {
		connectionID = trigger.ConnectionID
	}
	h.mu.Unlock()
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	trigger := h.triggers[id]
	if trigger == nil || trigger.State != "pending" && trigger.State != "armed" {
		h.mu.Unlock()
		return
	}
	if triggerExpiredAt(trigger, time.Now().UTC()) {
		previous := cloneTrigger(*trigger)
		trigger.State = "expired"
		trigger.Version++
		trigger.UpdatedAt = now()
		if err := h.persistTriggersLocked(); err != nil {
			*trigger = previous
			log.Printf("[codex-loom] persist expired trigger %s after observation: %v", id, err)
		} else {
			h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": cloneTrigger(*trigger)})
		}
		h.mu.Unlock()
		return
	}
	observedAt := strings.TrimSpace(observation.ObservedAt)
	if observedAt == "" {
		observedAt = now()
	}
	previous := cloneTrigger(*trigger)
	changed := trigger.LastError != ""
	trigger.LastError = ""
	targetID := ""
	if observation.Matched {
		observation.Event.ObservedAt = observedAt
		observation.Event.Provider = trigger.Provider
		observation.Event.ConnectionID = trigger.ConnectionID
		observation.Event.ResourceKind = trigger.ResourceKind
		observation.Event.ResumeInstruction = trigger.ResumeInstruction
		observation.Event.Work = trigger.Work
		message, err := h.ensureTriggerMessageLocked(trigger, observation.Event)
		if err != nil {
			*trigger = previous
			h.mu.Unlock()
			log.Printf("[codex-loom] persist Trigger occurrence %s: %v", id, err)
			return
		}
		trigger.State = "triggered"
		trigger.LastEventKey = observation.Event.EventKey
		trigger.LastMessageID = message.ID
		targetID = message.ToAgentID
		changed = true
	} else if trigger.State == "pending" {
		trigger.State = "armed"
		changed = true
	}
	checkpoint := changed || shouldCheckpointTriggerObservation(trigger, observedAt)
	if checkpoint {
		trigger.LastObservedAt = observedAt
	}
	if changed {
		trigger.UpdatedAt = observedAt
		trigger.Version++
	}
	if checkpoint {
		if err := h.persistTriggersLocked(); err != nil {
			*trigger = previous
			h.mu.Unlock()
			return
		}
	}
	copy := cloneTrigger(*trigger)
	if changed {
		h.emitGlobalLocked("loom/trigger", map[string]any{"trigger": copy})
	}
	if connection := h.connections[trigger.ConnectionID]; connection != nil && shouldCheckpointTriggerConnection(connection, observedAt) {
		previousConnection := *connection
		if handled, observationErr := h.recordGatewayObservationLocked(connection.ID, "connected", "", observedAt, observedAt, connection.Cursor, observedAt, nil); handled {
			if observationErr != nil {
				*connection = previousConnection
			}
			h.mu.Unlock()
			if targetID != "" {
				h.deliverNextQueuedForTarget(targetID, defaultInactivity)
			}
			return
		}
		connection.Status = "connected"
		connection.LastHeartbeatAt = observedAt
		connection.LastError = ""
		connection.UpdatedAt = observedAt
		if err := h.persistIntegrationsLocked(); err != nil {
			*connection = previousConnection
		}
	}
	h.mu.Unlock()
	if targetID != "" {
		h.deliverNextQueuedForTarget(targetID, defaultInactivity)
	}
}

func shouldCheckpointTriggerConnection(connection *PlatformConnection, observedAt string) bool {
	if connection.Status != "connected" || connection.LastError != "" || connection.LastHeartbeatAt == "" {
		return true
	}
	current, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return false
	}
	previous, err := time.Parse(time.RFC3339Nano, connection.LastHeartbeatAt)
	return err != nil || current.Sub(previous) >= time.Minute
}

func shouldCheckpointTriggerObservation(trigger *Trigger, observedAt string) bool {
	if trigger == nil || trigger.LastObservedAt == "" {
		return true
	}
	current, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return false
	}
	previous, err := time.Parse(time.RFC3339Nano, trigger.LastObservedAt)
	return err != nil || current.Sub(previous) >= time.Minute
}

func triggerExpiredAt(trigger *Trigger, current time.Time) bool {
	if trigger == nil || trigger.ExpiresAt == "" {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, trigger.ExpiresAt)
	return err == nil && !expires.After(current)
}

func (h *Hub) ensureTriggerMessageLocked(trigger *Trigger, event TriggerEvent) (*AgentMessage, error) {
	for _, id := range h.commOrder {
		message := h.comms[id]
		if message != nil && message.TriggerID == trigger.ID {
			return message, nil
		}
	}
	target := h.agents[trigger.AgentID]
	if target == nil {
		return nil, errf(404, "target agent not found: %s", trigger.AgentID)
	}
	timestamp := now()
	eventCopy := event
	message := AgentMessage{
		ID: newMessageID(), FromAgentID: triggerAgentID, ToAgentID: target.ID,
		From: triggerIdentity, To: target.Name, Subject: event.Summary, Body: trigger.ResumeInstruction,
		Response: "none", Status: "closed", DeliveryStatus: "queued", HandlingStatus: "pending",
		TriggerID: trigger.ID, TriggerEvent: &eventCopy, SourceTurnID: trigger.Work.SourceTurnID, TopicID: trigger.Work.TopicID, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := h.commitAgentMessageLocked(message); err != nil {
		return nil, errf(500, "persist Trigger message: %s", err)
	}
	copy := message
	return &copy, nil
}

func (h *Hub) reconcileTriggersLocked() error {
	messages := map[string]*AgentMessage{}
	for _, id := range h.commOrder {
		message := h.comms[id]
		if message == nil || message.TriggerID == "" {
			continue
		}
		if current := messages[message.TriggerID]; current == nil || message.CreatedAt > current.CreatedAt {
			messages[message.TriggerID] = message
		}
	}
	changed := false
	for id, trigger := range h.triggers {
		message := messages[id]
		if trigger == nil || message == nil || trigger.State != "pending" && trigger.State != "armed" {
			continue
		}
		trigger.State = "triggered"
		trigger.LastMessageID = message.ID
		if message.TriggerEvent != nil {
			trigger.LastEventKey = message.TriggerEvent.EventKey
			trigger.LastObservedAt = message.TriggerEvent.ObservedAt
		}
		trigger.LastError = ""
		trigger.Version++
		trigger.UpdatedAt = message.UpdatedAt
		changed = true
	}
	if !changed {
		return nil
	}
	if h.passive {
		return nil
	}
	if err := h.persistTriggersLocked(); err != nil {
		return fmt.Errorf("persist reconciled triggers: %w", err)
	}
	return nil
}

func observeProviderTrigger(ctx context.Context, connection PlatformConnection, trigger Trigger) (TriggerObservation, error) {
	if trigger.Provider != "github" {
		return TriggerObservation{Permanent: true}, fmt.Errorf("unsupported Trigger provider: %s", trigger.Provider)
	}
	token, err := githubapi.LoadCredential(connection.CredentialRef)
	if err != nil {
		return TriggerObservation{}, err
	}
	client := githubapi.NewClient(token)
	if value := strings.TrimSpace(os.Getenv("CODEX_LOOM_GITHUB_API_URL")); value != "" {
		client.APIURL = value
	}
	return observeGitHubTrigger(ctx, client, trigger)
}

func observeGitHubTrigger(ctx context.Context, client *githubapi.Client, trigger Trigger) (TriggerObservation, error) {
	observedAt := now()
	owner, repo := trigger.Subject["owner"], trigger.Subject["repo"]
	switch trigger.ResourceKind {
	case "pull-request":
		number, _ := strconv.Atoi(trigger.Subject["number"])
		pull, err := client.GetPullRequest(ctx, owner, repo, number)
		if err != nil {
			return githubObservationError(err)
		}
		subjectKey := fmt.Sprintf("%s/%s#%d", owner, repo, number)
		snapshot := map[string]any{
			"state": pull.State, "merged": pull.Merged, "headSha": pull.Head.SHA,
			"mergeCommitSha": pull.MergeCommitSHA, "updatedAt": pull.UpdatedAt,
		}
		for _, condition := range trigger.Conditions {
			event := TriggerEvent{SubjectKey: subjectKey, Snapshot: snapshot, ObservedAt: observedAt}
			switch condition.Event {
			case "merged":
				if !pull.Merged {
					continue
				}
				event.Event = "merged"
				event.EventKey = "github:pr:" + subjectKey + ":merged:" + pull.MergeCommitSHA
				event.OccurredAt = pull.MergedAt
				event.Summary = "Pull request " + subjectKey + " is merged."
			case "closed":
				if pull.State != "closed" || pull.Merged {
					continue
				}
				event.Event = "closed"
				event.EventKey = "github:pr:" + subjectKey + ":closed:" + pull.UpdatedAt
				event.OccurredAt = pull.ClosedAt
				event.Summary = "Pull request " + subjectKey + " was closed without merge."
			case "head-changed":
				expected := trigger.Subject["expectedHead"]
				if expected == "" {
					expected = condition.Parameters["expectedHead"]
				}
				if expected == "" || pull.Head.SHA == "" || pull.Head.SHA == expected {
					continue
				}
				event.Event = "head-changed"
				event.EventKey = "github:pr:" + subjectKey + ":head:" + pull.Head.SHA
				event.OccurredAt = pull.UpdatedAt
				event.Summary = "Pull request " + subjectKey + " moved from " + expected + " to " + pull.Head.SHA + "."
			}
			return TriggerObservation{Matched: true, ObservedAt: observedAt, Event: event}, nil
		}
		return TriggerObservation{ObservedAt: observedAt, Event: TriggerEvent{SubjectKey: subjectKey, Snapshot: snapshot}}, nil
	case "workflow-run":
		runID, _ := strconv.ParseInt(trigger.Subject["runId"], 10, 64)
		run, err := client.GetWorkflowRun(ctx, owner, repo, runID)
		if err != nil {
			return githubObservationError(err)
		}
		subjectKey := fmt.Sprintf("%s/%s/actions/runs/%d", owner, repo, runID)
		snapshot := map[string]any{"status": run.Status, "conclusion": run.Conclusion, "headSha": run.HeadSHA, "updatedAt": run.UpdatedAt, "name": run.Name}
		for _, condition := range trigger.Conditions {
			matched := condition.Event == "completed" && run.Status == "completed" || condition.Event == run.Conclusion && run.Status == "completed"
			if !matched {
				continue
			}
			event := TriggerEvent{
				SubjectKey: subjectKey, Event: condition.Event,
				EventKey:   fmt.Sprintf("github:workflow:%d:%s:%s", runID, run.Status, run.Conclusion),
				OccurredAt: run.UpdatedAt, ObservedAt: observedAt,
				Summary: fmt.Sprintf("Workflow run %s is %s (%s).", subjectKey, run.Status, run.Conclusion), Snapshot: snapshot,
			}
			return TriggerObservation{Matched: true, ObservedAt: observedAt, Event: event}, nil
		}
		return TriggerObservation{ObservedAt: observedAt, Event: TriggerEvent{SubjectKey: subjectKey, Snapshot: snapshot}}, nil
	default:
		return TriggerObservation{Permanent: true}, fmt.Errorf("unsupported GitHub Trigger resource: %s", trigger.ResourceKind)
	}
}

func githubObservationError(err error) (TriggerObservation, error) {
	var apiErr *githubapi.APIError
	permanent := errors.As(err, &apiErr) && apiErr.Status == 404
	if permanent {
		return TriggerObservation{Permanent: true}, fmt.Errorf("GitHub resource was not found or this Connection cannot access its Resource Owner/repository: %w", err)
	}
	return TriggerObservation{Permanent: permanent}, err
}

func cloneTrigger(trigger Trigger) Trigger {
	trigger.Subject = cloneStringMap(trigger.Subject)
	trigger.Conditions = append([]TriggerCondition(nil), trigger.Conditions...)
	for index := range trigger.Conditions {
		trigger.Conditions[index].Parameters = cloneStringMap(trigger.Conditions[index].Parameters)
	}
	return trigger
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func newTriggerID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("trg_%d", time.Now().UnixNano())
	}
	return "trg_" + hex.EncodeToString(buffer)
}
