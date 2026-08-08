// Package hub governs durable Agents. An Agent is a stable governance entity
// bound to one long-lived Codex Thread; callers and interfaces come and go
// while the Agent identity, Profile, relationships and Thread remain.
//
// Process model: one shared CodexHost owns a long-lived `codex app-server`.
// All Agent Threads and Remote clients use that process; thread/resume restores
// bindings after a CodexLoom restart.
//
// Event flow: every JSON-RPC notification from codex is wrapped into
// {seq, ts, type, data}, appended to the Agent event log, and fanned out to
// subscribers. HTTP projects legacy hub/* lifecycle names into canonical
// loom/* names; Codex notifications retain their method name.
//
// Locking rule: NEVER call client.Request while holding h.mu — responses are
// delivered by the reader goroutine, which also takes h.mu for notifications.
package hub

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yan5xu/codex-loom/internal/codex"
	"github.com/yan5xu/codex-loom/internal/rollout"
	"github.com/yan5xu/codex-loom/internal/store"
)

const (
	defaultInactivity         = 30 * time.Minute
	absoluteTurnCap           = 4 * time.Hour
	externalRunningStaleAfter = 2 * time.Minute
	schedulerIdentity         = "scheduler"
	schedulerDefaultTZ        = "Asia/Shanghai"
	subscriberBuffer          = 1024
	edgeCreatedAt             = "1970-01-01T00:00:00Z"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type HubError struct {
	Status  int
	Message string
}

func (e *HubError) Error() string { return e.Message }

func errf(status int, format string, args ...any) *HubError {
	return &HubError{Status: status, Message: fmt.Sprintf(format, args...)}
}

type TurnSummary struct {
	TurnID      string `json:"turnId"`
	Task        string `json:"task"`
	Status      string `json:"status"`
	CompletedAt string `json:"completedAt"`
}

// Agent is CodexLoom's stable governance entity. ThreadID is its primary Codex
// execution binding; Profile, links and external addresses remain attached to
// the Agent even if that binding is migrated later.
type Agent struct {
	ID                    string                  `json:"id"`
	Name                  string                  `json:"name"`
	Cwd                   string                  `json:"cwd"`
	ThreadID              string                  `json:"threadId"`
	Sandbox               string                  `json:"sandbox"`
	ApprovalPolicy        string                  `json:"approvalPolicy"`
	ProviderID            string                  `json:"providerId,omitempty"`
	Model                 string                  `json:"model,omitempty"`
	Effort                string                  `json:"effort,omitempty"`
	Status                string                  `json:"status"`
	CurrentTask           string                  `json:"currentTask"`
	CurrentTurnID         string                  `json:"currentTurnId"`
	LastError             string                  `json:"lastError"`
	LastTurn              *TurnSummary            `json:"lastTurn"`
	CreatedAt             string                  `json:"createdAt"`
	UpdatedAt             string                  `json:"updatedAt"`
	PendingProviderSwitch *ProviderSwitchBinding  `json:"pendingProviderSwitch,omitempty"`
	ProviderHistory       []ProviderBindingChange `json:"providerHistory,omitempty"`
	// Source is "edge" for Agents mirrored read-only from pinix-edge's
	// registry (they are re-imported each startup and never persisted here);
	// empty for Agents CodexLoom owns. Starting a Turn promotes an edge mirror
	// to a native Agent (Source cleared, then persisted).
	Source string `json:"source,omitempty"`
}

// Session is the pre-CodexLoom compatibility name.
type Session = Agent

// AgentView is what the canonical API returns: governance metadata plus live
// Codex Thread runtime state.
type AgentView struct {
	Agent
	ProcessAlive     bool           `json:"processAlive"`
	PendingApprovals []ApprovalView `json:"pendingApprovals"`
	Goal             *ThreadGoal    `json:"goal,omitempty"`
	LastSeq          int64          `json:"lastSeq"`
}

// SessionView is the pre-CodexLoom compatibility name.
type SessionView = AgentView

type ApprovalView struct {
	ApprovalID string          `json:"approvalId"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	TS         string          `json:"ts"`
}

type ActiveAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CurrentTask string `json:"currentTask"`
}

// RunningSession is the pre-CodexLoom compatibility name.
type RunningSession = ActiveAgent

type AgentMessage struct {
	ID                 string                        `json:"id"`
	FromAgentID        string                        `json:"fromAgentId"`
	ToAgentID          string                        `json:"toAgentId"`
	From               string                        `json:"from"`
	To                 string                        `json:"to"`
	Subject            string                        `json:"subject"`
	Body               string                        `json:"body"`
	Response           string                        `json:"response"`
	ReplyTo            string                        `json:"replyTo,omitempty"`
	SourceTurnID       string                        `json:"sourceTurnId,omitempty"`
	ScheduleID         string                        `json:"scheduleId,omitempty"`
	ScheduledAt        string                        `json:"scheduledAt,omitempty"`
	TriggerID          string                        `json:"triggerId,omitempty"`
	TopicID            string                        `json:"topicId,omitempty"`
	TriggerEvent       *TriggerEvent                 `json:"triggerEvent,omitempty"`
	Status             string                        `json:"status"`               // open, answered, closed
	Resolution         string                        `json:"resolution,omitempty"` // reply, no_reply, cancelled, completed_elsewhere, superseded
	ResolutionReason   string                        `json:"resolutionReason,omitempty"`
	ResolvedBy         string                        `json:"resolvedBy,omitempty"`
	ResolvedAt         string                        `json:"resolvedAt,omitempty"`
	DeliveryStatus     string                        `json:"deliveryStatus"`         // queued, delivering, delivered, failed, cancelled
	DeliveryMode       string                        `json:"deliveryMode,omitempty"` // turn_start, turn_steer
	CreatedAt          string                        `json:"createdAt"`
	UpdatedAt          string                        `json:"updatedAt"`
	DeliveredAt        string                        `json:"deliveredAt,omitempty"`
	LastDeliveryError  string                        `json:"lastDeliveryError,omitempty"`
	DeliveredAgentID   string                        `json:"deliveredAgentId,omitempty"`
	DeliveredSessionID string                        `json:"deliveredSessionId,omitempty"` // compatibility
	DeliveredTurnID    string                        `json:"deliveredTurnId,omitempty"`
	HandlingStatus     string                        `json:"handlingStatus,omitempty"` // pending, running, completed, interrupted, failed
	ActiveHandlingID   string                        `json:"activeHandlingAttemptId,omitempty"`
	LastHandlingError  string                        `json:"lastHandlingError,omitempty"`
	HandlingAttempts   []AgentMessageHandlingAttempt `json:"handlingAttempts,omitempty"`
}

// AgentMessageHandlingAttempt records one Turn that handled an already
// delivered internal message. Delivery and handling are separate lifecycles:
// interrupting an attempt never makes the original delivery pending again.
type AgentMessageHandlingAttempt struct {
	ID          string `json:"id"`
	TurnID      string `json:"turnId,omitempty"`
	Status      string `json:"status"` // running, completed, interrupted, failed
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt,omitempty"`
	Error       string `json:"error,omitempty"`
}

type CommParams struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Subject    string        `json:"subject"`
	Body       string        `json:"body"`
	Response   string        `json:"response"`
	ReplyTo    string        `json:"replyTo"`
	TopicID    string        `json:"topicId,omitempty"`
	System     bool          `json:"-"`
	Timeout    time.Duration `json:"-"`
	TimeoutSec int           `json:"timeoutSec"`
}

type CommResult struct {
	Message *AgentMessage `json:"message"`
	TurnID  string        `json:"turnId,omitempty"`
}

type Schedule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	To            string `json:"to"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	Response      string `json:"response"`
	At            string `json:"at,omitempty"`
	Cron          string `json:"cron,omitempty"`
	Timezone      string `json:"timezone"`
	Enabled       bool   `json:"enabled"`
	LastRunAt     string `json:"lastRunAt,omitempty"`
	NextRunAt     string `json:"nextRunAt,omitempty"`
	LastMessageID string `json:"lastMessageId,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type ScheduleParams struct {
	Name     string `json:"name"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Response string `json:"response"`
	At       string `json:"at"`
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type commRecord struct {
	Message AgentMessage `json:"message"`
}

type approval struct {
	rpcID  json.RawMessage
	method string
	params json.RawMessage
	ts     string
}

type turnState struct {
	turnID            string
	startedConfirmed  bool
	task              string
	source            string
	inboxItemID       string
	attemptID         string
	agentMessageID    string
	topicID           string
	handlingAttemptID string
	contextAttemptID  string
	contextEpochID    string
	finalAnswer       string
	forcedFailure     string
	startedAt         time.Time
	lastActivity      time.Time
	finished          bool
	stopWatchdog      chan struct{}
}

type runtime struct {
	agentID           string
	client            *codex.Client
	hostGeneration    uint64
	skillConfigHash   string
	skillConfigLoaded bool
	ready             chan struct{}
	initErr           error
	startMu           sync.Mutex

	activeTurn *turnState           // guarded by Hub.mu
	approvals  map[string]*approval // guarded by Hub.mu
}

type subscriber struct {
	ch     chan store.Event
	once   sync.Once
	global bool
}

func (s *subscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

type Hub struct {
	st      *store.Store
	passive bool

	// Persistence seams keep the R0 crash boundaries deterministic in tests.
	// Production leaves them nil and writes through Store.
	saveIntegrations         func(integrationConfig) error
	saveRuntimeFoundation    func(gatewayFoundationDocument) error
	gatewayLifecycle         gatewayLifecycleState
	gatewayFoundationPresent bool
	gatewayCoordinator       *gatewayConnectionCoordinator

	mu                      sync.Mutex
	contextCoverageMu       sync.Mutex
	modelProviderMu         sync.Mutex
	agents                  map[string]*Agent
	agentSkillConfigs       map[string]*AgentSkillConfig
	comms                   map[string]*AgentMessage
	commOrder               []string
	schedules               map[string]*Schedule
	triggers                map[string]*Trigger
	topics                  map[string]*Topic
	profiles                map[string]*AgentProfile
	teamLinks               map[string]*TeamRelationship
	loomAgentPrompt         *LoomAgentPrompt
	collaborationGroups     map[string]*CollaborationGroup
	organizationLinks       map[string]*OrganizationRelationship
	connections             map[string]*PlatformConnection
	addresses               map[string]*AgentAddress
	addressOperations       map[string]*AddressLifecycleOperation
	memberships             map[string]*ConversationMembership
	conversationCandidates  map[string]*ConversationCandidate
	messages                map[string]*InboxMessage
	messageOrder            []string
	externalMessages        map[string]string
	inbox                   map[string]*InboxItem
	inboxOrder              []string
	attempts                map[string]*HandlingAttempt
	outbox                  map[string]*OutboxItem
	outboxOrder             []string
	providerOperations      map[string]*ProviderOperation
	providerOperationOrder  []string
	humanRequests           map[string]*HumanRequest
	humanRequestOrder       []string
	goals                   map[string]*ThreadGoal
	seqs                    map[string]int64
	globalSeq               int64
	runtimes                map[string]*runtime
	subs                    map[string]map[*subscriber]struct{}
	globalSubs              map[*subscriber]struct{}
	remoteConfig            RemoteConfig
	remoteStatus            RemoteStatus
	remotePairing           *RemotePairing
	remoteRuntime           *remoteRuntime
	remoteGeneration        uint64
	remoteStartMu           sync.Mutex
	remoteEnabledGeneration uint64
	codexHost               *codexHostRuntime
	codexHostGeneration     uint64
	stop                    chan struct{}
	stopOnce                sync.Once
	stopping                bool
	draining                bool
	providerSwitching       bool
	triggerObservations     map[string]struct{}
	background              sync.WaitGroup
	workers                 sync.WaitGroup
	steerTurn               func(threadID, expectedTurnID, input string, timeout time.Duration) (string, error)
	dispatchHumanAnswer     func(key, text string) (SendResult, error)
	observeTrigger          triggerObserver
	contextHistoryProbe     contextHistoryProbeFunc
	threadResumeTimeout     time.Duration
	developerContextTimeout time.Duration
}

// New is retained for in-process callers that cannot recover from an invalid
// store. The service entry point uses Open so it can report the startup error.
func New(st *store.Store) *Hub {
	h, err := Open(st)
	if err != nil {
		panic(err)
	}
	return h
}

// OpenOptions controls process-level behavior that is intentionally separate
// from the durable Hub model. Passive mode is used by read-only development
// canaries: it loads projections without importing external registries,
// reconciling live runtime state, or starting workers.
type OpenOptions struct {
	Passive bool
}

// Open loads all durable projections before starting background work. Required
// state is fail-closed: malformed data is never replaced with an empty map.
func Open(st *store.Store) (*Hub, error) {
	return OpenWithOptions(st, OpenOptions{})
}

func OpenWithOptions(st *store.Store, options OpenOptions) (*Hub, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if options.Passive {
		st = st.ReadOnlyView()
	} else if st.ReadOnly() {
		return nil, fmt.Errorf("writable Hub requires a writable Store")
	}
	h := &Hub{
		st:                     st,
		passive:                options.Passive,
		gatewayLifecycle:       emptyGatewayLifecycleState(),
		gatewayCoordinator:     newGatewayConnectionCoordinator(),
		agents:                 map[string]*Agent{},
		agentSkillConfigs:      map[string]*AgentSkillConfig{},
		comms:                  map[string]*AgentMessage{},
		schedules:              map[string]*Schedule{},
		triggers:               map[string]*Trigger{},
		topics:                 map[string]*Topic{},
		profiles:               map[string]*AgentProfile{},
		teamLinks:              map[string]*TeamRelationship{},
		collaborationGroups:    map[string]*CollaborationGroup{},
		organizationLinks:      map[string]*OrganizationRelationship{},
		connections:            map[string]*PlatformConnection{},
		addresses:              map[string]*AgentAddress{},
		addressOperations:      map[string]*AddressLifecycleOperation{},
		memberships:            map[string]*ConversationMembership{},
		conversationCandidates: map[string]*ConversationCandidate{},
		messages:               map[string]*InboxMessage{},
		externalMessages:       map[string]string{},
		inbox:                  map[string]*InboxItem{},
		attempts:               map[string]*HandlingAttempt{},
		outbox:                 map[string]*OutboxItem{},
		providerOperations:     map[string]*ProviderOperation{},
		humanRequests:          map[string]*HumanRequest{},
		goals:                  map[string]*ThreadGoal{},
		seqs:                   map[string]int64{},
		runtimes:               map[string]*runtime{},
		subs:                   map[string]map[*subscriber]struct{}{},
		globalSubs:             map[*subscriber]struct{}{},
		triggerObservations:    map[string]struct{}{},
		stop:                   make(chan struct{}),
	}
	h.globalSeq = st.LastSeq(globalEventLogID)
	if err := st.LoadAgents(&h.agents); err != nil {
		return nil, fmt.Errorf("load agents: %w", err)
	}
	if h.agents == nil {
		h.agents = map[string]*Agent{}
	}
	providerSwitchRecovered := false
	for _, meta := range h.agents {
		if meta.PendingProviderSwitch == nil {
			continue
		}
		meta.PendingProviderSwitch = nil
		meta.LastError = "an interrupted Provider switch was rolled back to the previous binding"
		meta.UpdatedAt = now()
		providerSwitchRecovered = true
	}
	if providerSwitchRecovered && !options.Passive {
		if err := h.persistAgentsLocked(); err != nil {
			return nil, fmt.Errorf("recover interrupted Provider switch: %w", err)
		}
	}
	if err := h.st.LoadAgentSkillConfigs(&h.agentSkillConfigs); err != nil {
		return nil, fmt.Errorf("load Agent Skill config: %w", err)
	}
	if h.agentSkillConfigs == nil {
		h.agentSkillConfigs = map[string]*AgentSkillConfig{}
	}
	if err := h.st.LoadProfiles(&h.profiles); err != nil {
		return nil, fmt.Errorf("load profiles: %w", err)
	}
	if h.profiles == nil {
		h.profiles = map[string]*AgentProfile{}
	}
	if err := h.loadLoomAgentPrompt(); err != nil {
		return nil, fmt.Errorf("load Loom Agent Prompt: %w", err)
	}
	if err := h.st.LoadTeamLinks(&h.teamLinks); err != nil {
		return nil, fmt.Errorf("load team links: %w", err)
	}
	if h.teamLinks == nil {
		h.teamLinks = map[string]*TeamRelationship{}
	}
	if err := h.st.LoadCollaborationGroups(&h.collaborationGroups); err != nil {
		return nil, fmt.Errorf("load collaboration groups: %w", err)
	}
	if h.collaborationGroups == nil {
		h.collaborationGroups = map[string]*CollaborationGroup{}
	}
	if err := h.st.LoadOrganizationLinks(&h.organizationLinks); err != nil {
		return nil, fmt.Errorf("load organization links: %w", err)
	}
	if h.organizationLinks == nil {
		h.organizationLinks = map[string]*OrganizationRelationship{}
	}
	if err := h.validateLoadedCollaborationGroupsLocked(); err != nil {
		return nil, fmt.Errorf("validate collaboration groups: %w", err)
	}
	integrationsNormalized, err := h.loadIntegrations()
	if err != nil {
		return nil, fmt.Errorf("load integrations: %w", err)
	}
	if err := h.loadGatewayLifecycle(); err != nil {
		return nil, fmt.Errorf("load gateway lifecycle: %w", err)
	}
	if integrationsNormalized && !options.Passive {
		if h.gatewayFoundationPresent {
			return nil, fmt.Errorf("integration normalization requires explicit gateway lifecycle reconciliation")
		}
		if err := h.persistIntegrationsLocked(); err != nil {
			return nil, fmt.Errorf("persist normalized integrations: %w", err)
		}
	}
	projectionChanged := false
	for connectionID := range h.gatewayLifecycle.Controls {
		projectionChanged = h.applyGatewayHealthProjectionLocked(connectionID) || projectionChanged
	}
	if projectionChanged && !options.Passive {
		if err := h.persistIntegrationsLocked(); err != nil {
			return nil, fmt.Errorf("persist gateway health projection: %w", err)
		}
	}
	if err := h.loadInboxStateWithPersistence(!options.Passive); err != nil {
		return nil, fmt.Errorf("load inbox state: %w", err)
	}
	if err := h.loadProviderOperationsWithPersistence(!options.Passive); err != nil {
		return nil, fmt.Errorf("load provider operations: %w", err)
	}
	if err := h.loadCommsWithPersistence(!options.Passive); err != nil {
		return nil, fmt.Errorf("load communications: %w", err)
	}
	if err := h.loadHumanRequests(); err != nil {
		return nil, fmt.Errorf("load human requests: %w", err)
	}
	if err := h.st.LoadSchedules(&h.schedules); err != nil {
		return nil, fmt.Errorf("load schedules: %w", err)
	}
	if h.schedules == nil {
		h.schedules = map[string]*Schedule{}
	}
	if err := h.st.LoadTriggers(&h.triggers); err != nil {
		return nil, fmt.Errorf("load triggers: %w", err)
	}
	if h.triggers == nil {
		h.triggers = map[string]*Trigger{}
	}
	if err := h.st.LoadTopics(&h.topics); err != nil {
		return nil, fmt.Errorf("load Topics: %w", err)
	}
	if h.topics == nil {
		h.topics = map[string]*Topic{}
	}
	if h.normalizeTopicsLocked() && !options.Passive {
		if err := h.persistTopicsLocked(); err != nil {
			return nil, fmt.Errorf("persist normalized Topics: %w", err)
		}
	}
	if err := h.loadRemoteLocked(); err != nil {
		return nil, fmt.Errorf("load Remote config: %w", err)
	}
	if !options.Passive {
		// Mirror pinix-edge's registry: edge-created agents become visible here
		// (read-only) and their rollout history is immediately viewable.
		h.importEdgeLocked()
	}
	if err := h.migrateCommAgentIDsLockedWithPersistence(!options.Passive); err != nil {
		return nil, fmt.Errorf("migrate communication agent ids: %w", err)
	}
	if err := h.reconcileTriggersLockedWithPersistence(!options.Passive); err != nil {
		return nil, fmt.Errorf("reconcile triggers: %w", err)
	}
	for _, meta := range h.agents {
		h.seqs[meta.ID] = st.LastSeq(meta.ID)
	}
	if options.Passive {
		return h, nil
	}

	// Reconcile: tasks running when the Hub last died are interrupted. Keep the
	// interrupted projection visible until the Owner continues or dismisses it;
	// otherwise a restart silently turns unfinished work into an idle Agent.
	h.mu.Lock()
	for _, meta := range h.agents {
		if meta.Source == "edge" {
			continue // edge mirrors carry no CodexLoom-driven turn state
		}
		if meta.Status == "running" {
			interrupted, missingTerminal := reconcileInterruptedTurn(meta)
			interruptedTurnID := interrupted.TurnID
			if !missingTerminal {
				if interruptedTurnID != "" {
					for _, messageID := range h.commOrder {
						msg := h.comms[messageID]
						if msg == nil || msg.ToAgentID != meta.ID || msg.DeliveryMode != "turn_start" || msg.DeliveredTurnID != interruptedTurnID {
							continue
						}
						h.finishAgentMessageTurnLocked(&turnState{turnID: interruptedTurnID, agentMessageID: msg.ID}, interrupted.Status, "")
					}
				}
				meta.Status = "idle"
				meta.LastError = ""
				meta.LastTurn = interrupted
				meta.CurrentTask = ""
				meta.CurrentTurnID = ""
				meta.UpdatedAt = now()
				continue
			}
			if interruptedTurnID != "" {
				for _, messageID := range h.commOrder {
					msg := h.comms[messageID]
					if msg == nil || msg.ToAgentID != meta.ID || msg.DeliveryMode != "turn_start" || msg.DeliveredTurnID != interruptedTurnID {
						continue
					}
					h.finishAgentMessageTurnLocked(&turnState{turnID: interruptedTurnID, agentMessageID: msg.ID}, "interrupted", "CodexLoom restarted while delivery Turn was running")
				}
			}
			h.emitLocked(meta.ID, "loom/turn-interrupted", map[string]any{
				"reason": "loom-restart",
				"task":   interrupted.Task,
				"turnId": interrupted.TurnID,
			})
			meta.Status = "interrupted"
			meta.LastError = "interrupted: CodexLoom restarted while task was running"
			meta.LastTurn = interrupted
			meta.CurrentTask = ""
			meta.CurrentTurnID = ""
			meta.UpdatedAt = now()
		}
	}
	if err := h.persistAgentsLocked(); err != nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("persist startup recovery: %w", err)
	}
	h.mu.Unlock()
	h.background.Add(6)
	go func() { defer h.background.Done(); h.deliveryLoop() }()
	go func() { defer h.background.Done(); h.schedulerLoop() }()
	go func() { defer h.background.Done(); h.inboxLoop() }()
	go func() { defer h.background.Done(); h.remoteLoop() }()
	go func() { defer h.background.Done(); h.eventMaintenanceLoop() }()
	go func() { defer h.background.Done(); h.triggerLoop() }()
	return h, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (h *Hub) loadComms() error {
	return h.loadCommsWithPersistence(true)
}

func (h *Hub) loadCommsWithPersistence(persistRecovery bool) error {
	repairLatest := map[string]bool{}
	if err := h.st.ReadComms(func(raw json.RawMessage) {
		var rec commRecord
		if err := json.Unmarshal(raw, &rec); err != nil || rec.Message.ID == "" {
			return
		}
		msg := rec.Message
		if _, exists := h.comms[msg.ID]; !exists {
			h.commOrder = append(h.commOrder, msg.ID)
		}
		repairLatest[msg.ID] = normalizeAgentMessage(&msg)
		h.comms[msg.ID] = &msg
	}); err != nil {
		return err
	}
	h.reconcileDeliveredReplyRoots(repairLatest)
	for _, id := range h.commOrder {
		if !repairLatest[id] {
			continue
		}
		msg := h.comms[id]
		if persistRecovery {
			if err := h.st.AppendComm(commRecord{Message: *msg}); err != nil {
				return fmt.Errorf("persist repaired message %s: %w", msg.ID, err)
			}
		}
	}
	return nil
}

func (h *Hub) reconcileDeliveredReplyRoots(repairLatest map[string]bool) {
	for _, id := range h.commOrder {
		reply := h.comms[id]
		if reply == nil || reply.ReplyTo == "" || reply.Resolution != "reply" || reply.DeliveryStatus != "delivered" {
			continue
		}
		root := h.comms[reply.ReplyTo]
		if root == nil || root.Status != "open" || root.Response != "required" {
			continue
		}
		resolvedAt := reply.DeliveredAt
		if resolvedAt == "" {
			resolvedAt = reply.UpdatedAt
		}
		if resolvedAt == "" {
			resolvedAt = reply.CreatedAt
		}
		if resolvedAt == "" {
			resolvedAt = now()
		}
		next := *root
		next.Status = "answered"
		next.Resolution = "reply"
		next.ResolvedBy = reply.From
		next.ResolvedAt = resolvedAt
		next.UpdatedAt = resolvedAt
		h.comms[root.ID] = &next
		repairLatest[root.ID] = true
	}
}

func normalizeAgentMessage(msg *AgentMessage) bool {
	repaired := false
	if msg.DeliveredAgentID == "" {
		msg.DeliveredAgentID = msg.DeliveredSessionID
	}
	if msg.DeliveredSessionID == "" {
		msg.DeliveredSessionID = msg.DeliveredAgentID
	}
	if msg.Status == "queued" || msg.Status == "delivering" || msg.Status == "failed" {
		msg.Status = "open"
		if msg.Response == "none" {
			msg.Status = "closed"
		}
	}
	if msg.Status == "" {
		msg.Status = "open"
		if msg.Response == "none" {
			msg.Status = "closed"
		}
	}
	if msg.DeliveryStatus == "" {
		if msg.DeliveredTurnID != "" {
			msg.DeliveryStatus = "delivered"
		} else {
			msg.DeliveryStatus = "queued"
		}
	}
	if msg.DeliveryStatus == "delivering" {
		msg.DeliveryStatus = "queued"
		msg.LastDeliveryError = "recovered from interrupted delivery"
	}
	// Older Loom versions turned an interrupted handling Turn back into a
	// queued delivery. That creates an infinite stop/redeliver loop. Recover
	// those exact records as an already-delivered but held request.
	if msg.DeliveryStatus == "queued" && strings.Contains(msg.LastDeliveryError, "delivery Turn interrupted") {
		msg.DeliveryStatus = "delivered"
		msg.HandlingStatus = "interrupted"
		msg.LastHandlingError = msg.LastDeliveryError
		msg.LastDeliveryError = ""
		repaired = true
	}
	if msg.DeliveryMode == "" && msg.DeliveryStatus == "delivered" && msg.DeliveredTurnID != "" {
		msg.DeliveryMode = "turn_start"
	}
	if msg.HandlingStatus == "" {
		switch msg.DeliveryStatus {
		case "queued", "delivering", "failed":
			msg.HandlingStatus = "pending"
		case "delivered":
			msg.HandlingStatus = "completed"
		}
	}
	if msg.DeliveryStatus == "cancelled" && msg.Status == "open" {
		msg.Status = "closed"
		msg.Resolution = "cancelled"
		msg.ResolvedBy = msg.From
		msg.ResolvedAt = msg.UpdatedAt
		repaired = true
	}
	return repaired
}

func (h *Hub) emitGlobalLocked(typ string, data any) {
	h.globalSeq++
	ev := store.Event{Seq: h.globalSeq, TS: now(), Type: typ, Data: toRaw(data)}
	if err := h.st.AppendEvent(globalEventLogID, ev); err != nil {
		log.Printf("[codex-loom] append global event: %v", err)
	}
	for sub := range h.globalSubs {
		select {
		case sub.ch <- ev:
		default:
			delete(h.globalSubs, sub)
			sub.close()
		}
	}
}

const globalEventLogID = "__global__"

func (h *Hub) LastGlobalSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.globalSeq
}

func (h *Hub) ReadGlobalEvents(since int64, tail int) ([]store.Event, error) {
	return h.st.ReadEvents(globalEventLogID, since, tail)
}

func (h *Hub) persistAgentsLocked() error {
	// Persist only agents CodexLoom owns. Edge mirrors are re-imported from
	// pinix-edge's registry on every startup, so writing them here would only
	// let them drift out of sync.
	own := make(map[string]*Agent, len(h.agents))
	for id, meta := range h.agents {
		if meta.Source == "edge" {
			continue
		}
		own[id] = meta
	}
	return h.st.SaveAgents(own)
}

// persistRuntimeProjectionLocked checkpoints observed Codex runtime state. The
// rollout remains authoritative and Open reconciles this projection after a
// crash, so notification callbacks log checkpoint failures instead of blocking
// the shared app-server read loop. User-authored state uses explicit commits.
func (h *Hub) persistRuntimeProjectionLocked() {
	if err := h.persistAgentsLocked(); err != nil {
		log.Printf("[codex-loom] persist: %v", err)
	}
}

// importEdgeLocked merges pinix-edge's name registry into the Agent map as
// read-only mirrors. Existing Agents win by either name or Thread binding. A
// Hub-side rename must not make the old edge name reappear as a second Agent
// for the same Thread.
func (h *Hub) importEdgeLocked() {
	agents, err := store.LoadEdgeAgents()
	if err != nil {
		log.Printf("[codex-loom] load edge registry: %v", err)
		return
	}
	taken := map[string]bool{}
	takenThreads := map[string]bool{}
	for _, meta := range h.agents {
		taken[meta.Name] = true
		if threadID := strings.TrimSpace(meta.ThreadID); threadID != "" {
			takenThreads[threadID] = true
		}
	}
	for _, a := range agents {
		threadID := strings.TrimSpace(a.ThreadID)
		if taken[a.Name] || takenThreads[threadID] {
			continue
		}
		id := stableEdgeAgentID(threadID)
		if _, clash := h.agents[id]; clash {
			continue
		}
		h.agents[id] = &Agent{
			ID: id, Name: a.Name, Cwd: a.Cwd, ThreadID: threadID,
			Sandbox: "danger-full-access", ApprovalPolicy: "never",
			Status: "idle", Source: "edge",
			CreatedAt: edgeCreatedAt, UpdatedAt: now(),
		}
		taken[a.Name] = true
		takenThreads[threadID] = true
	}
}

func (h *Hub) resolveLocked(key string) *Agent {
	if s, ok := h.agents[key]; ok {
		return s
	}
	for _, s := range h.agents {
		if s.Name == key {
			return s
		}
	}
	return nil
}

// ---- events ----

func toRaw(data any) json.RawMessage {
	if raw, ok := data.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage("{}")
		}
		return raw
	}
	b, err := json.Marshal(data)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func (h *Hub) emitLocked(agentID, typ string, data any) store.Event {
	h.seqs[agentID]++
	ev := store.Event{Seq: h.seqs[agentID], TS: now(), Type: typ, Data: toRaw(data)}
	if err := h.st.AppendEvent(agentID, ev); err != nil {
		log.Printf("[codex-loom] append event: %v", err)
	}
	for sub := range h.subs[agentID] {
		select {
		case sub.ch <- ev:
		default:
			// Slow observer: drop it; SSE client reconnects and replays by seq.
			delete(h.subs[agentID], sub)
			sub.close()
		}
	}
	// The workbench keeps multiple Agent tabs live over its single global SSE
	// connection. Preserve the Agent-local event unchanged inside the envelope
	// so each mounted Thread view can reduce its own stream independently.
	h.emitGlobalLocked("loom/thread-event", map[string]any{"agentId": agentID, "event": ev})
	return ev
}

func (h *Hub) emitStatusLocked(meta *Agent, status string) {
	data := map[string]any{
		"id":             meta.ID,
		"name":           meta.Name,
		"cwd":            meta.Cwd,
		"threadId":       meta.ThreadID,
		"source":         meta.Source,
		"status":         status,
		"currentTask":    meta.CurrentTask,
		"currentTurnId":  meta.CurrentTurnID,
		"lastError":      meta.LastError,
		"lastTurn":       meta.LastTurn,
		"model":          meta.Model,
		"effort":         meta.Effort,
		"sandbox":        meta.Sandbox,
		"approvalPolicy": meta.ApprovalPolicy,
		"providerId":     meta.ProviderID,
		"updatedAt":      meta.UpdatedAt,
	}
	data["goal"] = h.goals[meta.ID]
	h.emitGlobalLocked("loom/agent-status", data)
}

func (h *Hub) EmitGlobal(typ string, data map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitGlobalLocked(typ, data)
}

// Subscribe returns a channel of live events for an Agent plus a cancel func.
func (h *Hub) Subscribe(key string) (<-chan store.Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.resolveLocked(key)
	if meta == nil {
		return nil, nil, errf(404, "agent not found: %s", key)
	}
	sub := &subscriber{ch: make(chan store.Event, subscriberBuffer)}
	if h.subs[meta.ID] == nil {
		h.subs[meta.ID] = map[*subscriber]struct{}{}
	}
	h.subs[meta.ID][sub] = struct{}{}
	cancel := func() {
		h.mu.Lock()
		delete(h.subs[meta.ID], sub)
		h.mu.Unlock()
		sub.close()
	}
	return sub.ch, cancel, nil
}

func (h *Hub) SubscribeGlobal() (<-chan store.Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := &subscriber{ch: make(chan store.Event, subscriberBuffer), global: true}
	h.globalSubs[sub] = struct{}{}
	cancel := func() {
		h.mu.Lock()
		delete(h.globalSubs, sub)
		h.mu.Unlock()
		sub.close()
	}
	return sub.ch, cancel
}

func (h *Hub) ReadEvents(key string, since int64, tail int) ([]store.Event, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	h.mu.Unlock()
	if meta == nil {
		return nil, errf(404, "agent not found: %s", key)
	}
	return h.st.ReadEvents(meta.ID, since, tail)
}

// LastSeq returns the highest event seq for the Agent (0 if none). Used by
// the SSE handler to skip replay (live-only) when history is served separately.
func (h *Hub) LastSeq(key string) int64 {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	h.mu.Unlock()
	if meta == nil {
		return 0
	}
	return h.st.LastSeq(meta.ID)
}

// ---- runtime management ----

// getRuntimeLocked returns an Agent binding to the shared CodexHost.
func (h *Hub) getRuntimeLocked(meta *Agent) (*runtime, error) {
	if err := h.threadControlFailureLocked(meta.ThreadID); err != nil {
		return nil, err
	}
	if rt, ok := h.runtimes[meta.ID]; ok && !rt.client.Closed() {
		select {
		case <-rt.ready:
			if rt.initErr == nil {
				return rt, nil
			}
			// A transient resume/initialize failure must not poison this Agent
			// until the entire shared Host is restarted.
			delete(h.runtimes, meta.ID)
		default:
			return rt, nil
		}
	}
	host, err := h.ensureCodexHostLocked()
	if err != nil {
		return nil, err
	}
	rt := &runtime{
		agentID: meta.ID, client: host.client, hostGeneration: host.generation,
		ready: make(chan struct{}), approvals: map[string]*approval{},
	}
	h.runtimes[meta.ID] = rt
	if !h.startWorkerLocked(func() { h.initRuntime(meta.ID, rt) }) {
		delete(h.runtimes, meta.ID)
		return nil, errf(503, "CodexLoom is shutting down")
	}
	return rt, nil
}

// initRuntime runs without the hub lock (talks to codex).
func (h *Hub) initRuntime(agentID string, rt *runtime) {
	defer close(rt.ready)
	h.mu.Lock()
	meta := h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		rt.initErr = errf(404, "agent vanished")
		return
	}
	threadID, threadName, sandbox, cwd := meta.ThreadID, meta.Name, meta.Sandbox, meta.Cwd
	providerID, model := effectiveProviderBinding(meta)
	disabledSkillPaths := h.disabledSkillPathsLocked(meta.ID)
	skillConfigHash := agentSkillConfigHash(disabledSkillPaths)
	h.mu.Unlock()

	h.mu.Lock()
	host := h.codexHost
	h.mu.Unlock()
	if host == nil || host.generation != rt.hostGeneration {
		rt.initErr = fmt.Errorf("CodexHost changed while thread was initializing")
		return
	}
	if err := waitCodexHost(host); err != nil {
		rt.initErr = err
		return
	}
	startThread := func() error {
		params := threadBindingParams(sandbox, cwd, providerID, model, disabledSkillPaths)
		result, err := rt.client.Request("thread/start", params, 30*time.Second)
		if err != nil {
			return err
		}
		var parsed struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(result, &parsed); err != nil || parsed.Thread.ID == "" {
			return fmt.Errorf("thread/start returned no thread id")
		}
		threadID := parsed.Thread.ID
		h.mu.Lock()
		if m := h.agents[agentID]; m != nil {
			previous := *m
			m.ThreadID = threadID
			m.UpdatedAt = now()
			if err := h.persistAgentsLocked(); err != nil {
				*m = previous
				h.mu.Unlock()
				return fmt.Errorf("persist started Thread binding: %w", err)
			}
		}
		h.mu.Unlock()
		if err := setThreadName(rt.client, threadID, threadName); err != nil {
			log.Printf("[codex-loom] set thread name %s to %q: %v", threadID, threadName, err)
		}
		h.mu.Lock()
		if h.runtimes[agentID] == rt {
			rt.skillConfigHash = skillConfigHash
			rt.skillConfigLoaded = true
		}
		h.mu.Unlock()
		return nil
	}
	if threadID == "" {
		rt.initErr = startThread()
		return
	}
	err := resumeThreadWithTimeout(rt.client, threadID, sandbox, cwd, providerID, model, disabledSkillPaths, h.effectiveThreadResumeTimeout())
	if err != nil {
		if codex.IsRequestTimeout(err) {
			h.markThreadControlIndeterminate(rt, threadID, "thread/resume")
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no rollout") || strings.Contains(msg, "not found") {
			rt.initErr = startThread()
		} else {
			rt.initErr = err
		}
		return
	}
	if err := setThreadName(rt.client, threadID, threadName); err != nil {
		log.Printf("[codex-loom] set thread name %s to %q: %v", threadID, threadName, err)
	}
	h.mu.Lock()
	if h.runtimes[agentID] == rt {
		rt.skillConfigHash = skillConfigHash
		rt.skillConfigLoaded = true
	}
	h.mu.Unlock()
}

func threadBindingParams(sandbox, cwd, providerID, model string, disabledSkillPaths []string) map[string]any {
	params := map[string]any{"sandbox": sandbox, "cwd": cwd}
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		params["modelProvider"] = providerID
	}
	if model = strings.TrimSpace(model); model != "" {
		params["model"] = model
	}
	params["config"] = codexAgentSkillConfig(disabledSkillPaths)
	return params
}

func resumeThread(client *codex.Client, threadID, sandbox, cwd, providerID, model string, disabledSkillPaths []string) error {
	return resumeThreadWithTimeout(client, threadID, sandbox, cwd, providerID, model, disabledSkillPaths, 60*time.Second)
}

func resumeThreadWithTimeout(client *codex.Client, threadID, sandbox, cwd, providerID, model string, disabledSkillPaths []string, timeout time.Duration) error {
	params := threadBindingParams(sandbox, cwd, providerID, model, disabledSkillPaths)
	params["threadId"] = threadID
	_, err := client.Request("thread/resume", params, timeout)
	return err
}

func (h *Hub) effectiveThreadResumeTimeout() time.Duration {
	if h.threadResumeTimeout > 0 {
		return h.threadResumeTimeout
	}
	return 60 * time.Second
}

func codexSandboxMode(sandbox string) string {
	switch strings.TrimSpace(sandbox) {
	case "danger-full-access", "dangerFullAccess":
		return "dangerFullAccess"
	case "workspace-write", "workspaceWrite":
		return "workspaceWrite"
	case "read-only", "readOnly":
		return "readOnly"
	default:
		return strings.TrimSpace(sandbox)
	}
}

func codexSandboxPolicy(sandbox string) map[string]any {
	return map[string]any{"type": codexSandboxMode(sandbox)}
}

func (h *Hub) resumeAgentThread(agentID string, rt *runtime) error {
	h.mu.Lock()
	meta := h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		return errf(404, "agent vanished")
	}
	threadID, sandbox, cwd := meta.ThreadID, meta.Sandbox, meta.Cwd
	providerID, model := effectiveProviderBinding(meta)
	disabledSkillPaths := h.disabledSkillPathsLocked(meta.ID)
	skillConfigHash := agentSkillConfigHash(disabledSkillPaths)
	if rt.skillConfigLoaded && rt.skillConfigHash != skillConfigHash {
		h.mu.Unlock()
		return errf(409, "Agent Skill policy changed for a loaded Codex Thread; restart CodexLoom before starting the next Turn")
	}
	h.mu.Unlock()
	if strings.TrimSpace(threadID) == "" {
		return errf(409, "agent has no Codex Thread binding")
	}
	if err := resumeThreadWithTimeout(rt.client, threadID, sandbox, cwd, providerID, model, disabledSkillPaths, h.effectiveThreadResumeTimeout()); err != nil {
		if codex.IsRequestTimeout(err) {
			h.markThreadControlIndeterminate(rt, threadID, "thread/resume")
		}
		return errf(500, "resume Codex Thread: %s", err)
	}
	h.mu.Lock()
	if h.runtimes[agentID] == rt {
		rt.skillConfigHash = skillConfigHash
		rt.skillConfigLoaded = true
	}
	h.mu.Unlock()
	return nil
}

func setThreadName(client *codex.Client, threadID, name string) error {
	threadID = strings.TrimSpace(threadID)
	name = strings.TrimSpace(name)
	if threadID == "" || name == "" {
		return nil
	}
	_, err := client.Request("thread/name/set", map[string]any{
		"threadId": threadID,
		"name":     name,
	}, 10*time.Second)
	return err
}

func waitReady(rt *runtime) error {
	<-rt.ready
	return rt.initErr
}

// ---- codex message handling ----

type turnParams struct {
	TurnID string `json:"turnId"`
	Turn   struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func notificationUserText(params json.RawMessage) string {
	var event struct {
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	}
	if json.Unmarshal(params, &event) != nil || event.Item.Type != "userMessage" {
		return ""
	}
	if text := strings.TrimSpace(event.Item.Text); text != "" {
		return text
	}
	var parts []string
	for _, content := range event.Item.Content {
		if text := strings.TrimSpace(content.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func displayUserTask(text string) string {
	text = strings.TrimSpace(text)
	if task, ok := displayLoomControlTask(text); ok {
		return task
	}
	managedContext := false
	if index := strings.Index(text, "<loom_context"); index >= 0 {
		managedContext = true
		text = strings.TrimSpace(text[:index])
	}
	if index := strings.Index(text, "\n\n<loom_attachments"); index >= 0 {
		text = strings.TrimSpace(text[:index])
	} else if strings.HasPrefix(text, "<loom_attachments") {
		text = ""
	}
	if text == "" {
		if managedContext {
			return "CodexLoom managed work"
		}
		return "Attached files"
	}
	return text
}

func displayLoomControlTask(text string) (string, bool) {
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "owner_topic_input":
			var payload struct {
				Message string `xml:"message"`
			}
			if err := decoder.DecodeElement(&payload, &start); err == nil {
				if message := summarizeTopicText(payload.Message); message != "" {
					return message, true
				}
			}
		case "owner_topic_intervention":
			var payload struct {
				Guidance string `xml:"guidance"`
			}
			if err := decoder.DecodeElement(&payload, &start); err == nil {
				if guidance := summarizeTopicText(payload.Guidance); guidance != "" {
					return "Owner guidance: " + guidance, true
				}
			}
		}
	}
}

func (h *Hub) adoptRemoteTurnLocked(meta *Agent, rt *runtime, turnID string) *turnState {
	turn := &turnState{
		turnID: turnID, startedConfirmed: true, task: "Remote turn", source: "remote",
		startedAt: time.Now(), lastActivity: time.Now(), stopWatchdog: make(chan struct{}),
	}
	rt.activeTurn = turn
	meta.Status = "running"
	meta.CurrentTask = turn.task
	meta.CurrentTurnID = turnID
	meta.LastError = ""
	meta.UpdatedAt = now()
	h.persistRuntimeProjectionLocked()
	h.emitStatusLocked(meta, "running")
	return turn
}

// rebindActiveTurnIDLocked reconciles a locally reserved Turn with the
// authoritative id emitted by app-server. The associated Inbox/Message
// records must move with it or completion would leave the work item hanging.
func (h *Hub) rebindActiveTurnIDLocked(meta *Agent, turn *turnState, turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turn == nil || turn.finished || turnID == "" || turn.turnID == turnID {
		return
	}
	previousTurnID := turn.turnID
	turn.turnID = turnID
	h.rebindContextAttemptTurn(meta.ThreadID, turn.contextAttemptID, previousTurnID, turnID)

	// Establish records that are still at the pre-start boundary first.
	h.markInboxAttemptRunningLocked(turn)
	if err := h.markAgentMessageHandlingRunningLocked(turn, meta.ID); err != nil {
		log.Printf("[codex-loom] establish reconciled message handling %s: %v", turn.agentMessageID, err)
	}

	if attempt := h.attempts[turn.attemptID]; attempt != nil &&
		(attempt.TurnID == "" || attempt.TurnID == previousTurnID) {
		next := *attempt
		next.TurnID = turnID
		if err := h.commitAttemptLocked(next); err != nil {
			log.Printf("[codex-loom] persist reconciled Inbox attempt %s: %v", next.ID, err)
		}
	}
	if message := h.comms[turn.agentMessageID]; message != nil {
		next := *message
		next.HandlingAttempts = cloneAgentMessageHandlingAttempts(message.HandlingAttempts)
		changed := false
		if next.DeliveredTurnID == "" || next.DeliveredTurnID == previousTurnID {
			next.DeliveredTurnID = turnID
			changed = true
		}
		for i := range next.HandlingAttempts {
			attempt := &next.HandlingAttempts[i]
			if (turn.handlingAttemptID != "" && attempt.ID == turn.handlingAttemptID) ||
				(turn.handlingAttemptID == "" && next.ActiveHandlingID != "" && attempt.ID == next.ActiveHandlingID) {
				if attempt.TurnID == "" || attempt.TurnID == previousTurnID {
					attempt.TurnID = turnID
					changed = true
				}
				break
			}
		}
		if changed {
			next.UpdatedAt = now()
			if err := h.commitAgentMessageLocked(next); err != nil {
				log.Printf("[codex-loom] persist reconciled message handling %s: %v", next.ID, err)
			}
		}
	}

	meta.Status = "running"
	meta.CurrentTurnID = turnID
	meta.UpdatedAt = now()
	h.persistRuntimeProjectionLocked()
	if previousTurnID != "" {
		log.Printf("[codex-loom] reconciled active Turn for %s: %s -> %s", meta.Name, previousTurnID, turnID)
		h.emitLocked(meta.ID, "loom/turn-reconciled", map[string]any{
			"previousTurnId": previousTurnID, "turnId": turnID, "source": turn.source,
		})
	}
}

func (h *Hub) onNotification(rt *runtime, method string, params json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.agents[rt.agentID]
	if meta == nil {
		return
	}
	var tp turnParams
	_ = json.Unmarshal(params, &tp)
	turnID := tp.TurnID
	if turnID == "" {
		turnID = tp.Turn.ID
	}

	if method == "turn/started" && turnID != "" {
		switch turn := rt.activeTurn; {
		case turn == nil || turn.finished:
			h.adoptRemoteTurnLocked(meta, rt, turnID)
		case turn.turnID == "":
			h.rebindActiveTurnIDLocked(meta, turn, turnID)
			turn.startedConfirmed = true
		case turn.turnID == turnID:
			turn.startedConfirmed = true
		case !turn.startedConfirmed:
			// turn/start may answer with a stale id immediately before the
			// authoritative notification arrives. Preserve the local work
			// context and move every linked record to the notified id.
			h.rebindActiveTurnIDLocked(meta, turn, turnID)
			turn.startedConfirmed = true
		default:
			// A confirmed Turn was superseded without its terminal event being
			// observed. Close only that stale projection, then adopt the actual
			// app-server Turn. Pending workers will see the new active Turn.
			previousTurnID := turn.turnID
			h.finishTurnLocked(meta, rt, "interrupted", "superseded by active Turn "+turnID)
			h.adoptRemoteTurnLocked(meta, rt, turnID)
			log.Printf("[codex-loom] adopted successor Turn for %s: %s -> %s", meta.Name, previousTurnID, turnID)
		}
	}
	activeEvent := rt.activeTurn != nil && !rt.activeTurn.finished &&
		(turnID == "" || rt.activeTurn.turnID == "" || rt.activeTurn.turnID == turnID)
	if text := notificationUserText(params); text != "" && activeEvent {
		task := displayUserTask(text)
		rt.activeTurn.task = task
		meta.CurrentTask = task
		meta.UpdatedAt = now()
		h.persistRuntimeProjectionLocked()
		h.emitStatusLocked(meta, "running")
	}
	if method == "thread/goal/updated" || method == "thread/goal/cleared" {
		h.onGoalNotificationLocked(meta.ID, method, params)
	}

	if activeEvent {
		rt.activeTurn.lastActivity = time.Now()
		if text := completedFinalAnswer(method, params); text != "" {
			rt.activeTurn.finalAnswer = text
		}
		if turnID != "" && rt.activeTurn.turnID == "" {
			h.rebindActiveTurnIDLocked(meta, rt.activeTurn, turnID)
		}
		if isModelProducedNotification(method) {
			h.observeContextModelEventLocked(meta, rt.activeTurn)
		}
		if method == "error" && rt.activeTurn.forcedFailure == "" {
			if failure, interrupt := customProviderModelRouteFailure(meta.ProviderID, meta.Model, params); failure != "" {
				rt.activeTurn.forcedFailure = failure
				if interrupt {
					h.scheduleModelRouteInterruptLocked(meta.ID, rt.activeTurn, failure)
				}
			}
		}
	}

	h.emitLocked(meta.ID, method, params)

	switch method {
	case "turn/completed", "turn/failed", "turn/aborted":
		if rt.activeTurn == nil || rt.activeTurn.finished {
			return
		}
		if turnID != "" && rt.activeTurn.turnID == "" {
			h.rebindActiveTurnIDLocked(meta, rt.activeTurn, turnID)
		}
		if turnID != "" && rt.activeTurn.turnID != "" && rt.activeTurn.turnID != turnID {
			log.Printf("[codex-loom] ignored terminal event for stale Turn on %s: active=%s event=%s", meta.Name, rt.activeTurn.turnID, turnID)
			return
		}
		status := "completed"
		errMsg := ""
		if tp.Turn.Error != nil {
			errMsg = tp.Turn.Error.Message
		} else if tp.Error != nil {
			errMsg = tp.Error.Message
		}
		if rt.activeTurn.forcedFailure == "" {
			rt.activeTurn.forcedFailure = customProviderModelRouteFailureDetail(meta.ProviderID, meta.Model, errMsg)
		}
		switch {
		case method == "turn/failed", tp.Turn.Status == "failed":
			status = "failed"
		case method == "turn/aborted", tp.Turn.Status == "interrupted", tp.Turn.Status == "aborted", tp.Turn.Status == "cancelled", tp.Turn.Status == "canceled":
			status = "interrupted"
		}
		h.finishTurnLocked(meta, rt, status, errMsg)
	}
}

func (h *Hub) onServerRequest(rt *runtime, id json.RawMessage, method string, params json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.agents[rt.agentID]
	if strings.Contains(strings.ToLower(method), "approval") {
		apID := "ap-" + strings.Trim(string(id), `"`)
		rt.approvals[apID] = &approval{rpcID: id, method: method, params: params, ts: now()}
		if rt.activeTurn != nil && !rt.activeTurn.finished {
			rt.activeTurn.lastActivity = time.Now()
		}
		if meta != nil {
			h.emitLocked(meta.ID, "loom/approval-requested", map[string]any{
				"approvalId": apID,
				"method":     method,
				"params":     params,
			})
		}
		return
	}
	// Unknown server->client request: answer with an error so codex won't hang.
	if meta != nil {
		h.emitLocked(meta.ID, "loom/server-request", map[string]any{"method": method, "params": params})
	}
	_ = rt.client.RespondError(id, -32601, "CodexLoom does not handle "+method)
}

func (h *Hub) ResolveApproval(key, approvalID, decision string) (map[string]any, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	rt := h.runtimes[meta.ID]
	if rt == nil {
		h.mu.Unlock()
		return nil, errf(404, "no pending approval %s", approvalID)
	}
	ap, ok := rt.approvals[approvalID]
	if !ok {
		h.mu.Unlock()
		return nil, errf(404, "no pending approval %s", approvalID)
	}
	delete(rt.approvals, approvalID)
	// codex 0.142.5 availableDecisions are "accept" / "cancel" (see protocol doc).
	d := "cancel"
	if decision == "accept" || decision == "approve" {
		d = "accept"
	}
	h.emitLocked(meta.ID, "loom/approval-resolved", map[string]any{
		"approvalId": approvalID, "decision": d, "method": ap.method,
	})
	client := rt.client
	h.mu.Unlock()

	if err := client.Respond(ap.rpcID, map[string]any{"decision": d}); err != nil {
		return nil, errf(500, "respond approval: %s", err)
	}
	return map[string]any{"approvalId": approvalID, "decision": d}, nil
}

func (h *Hub) finishTurnLocked(meta *Agent, rt *runtime, status, errMsg string) {
	turn := rt.activeTurn
	if turn == nil || turn.finished {
		return
	}
	if turn.forcedFailure != "" {
		status = "failed"
		errMsg = turn.forcedFailure
	}
	turn.finished = true
	close(turn.stopWatchdog)
	rt.activeTurn = nil
	rt.approvals = map[string]*approval{}

	evType := "loom/turn-completed"
	if status == "failed" {
		evType = "loom/turn-failed"
	} else if status == "interrupted" {
		evType = "loom/turn-interrupted"
	}
	payload := map[string]any{
		"turnId":     turn.turnID,
		"task":       turn.task,
		"source":     turn.source,
		"durationMs": time.Since(turn.startedAt).Milliseconds(),
		"topicId":    turn.topicID,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	h.emitLocked(meta.ID, evType, payload)
	if turn.topicID != "" {
		h.recordTopicWorkEventLocked(turn.topicID, TopicEvent{
			Type: "turn_" + status, Actor: meta.Name, AgentID: meta.ID, Agent: meta.Name,
			Summary: status + ": " + summarizeTopicText(turn.task), Ref: &TopicRef{Type: "turn", ID: turn.turnID, Label: meta.Name}, CreatedAt: now(),
		})
	}

	meta.Status = "idle"
	meta.CurrentTask = ""
	meta.CurrentTurnID = ""
	if status == "completed" {
		meta.LastError = ""
	} else if errMsg != "" {
		meta.LastError = errMsg
	} else {
		meta.LastError = status
	}
	meta.LastTurn = &TurnSummary{TurnID: turn.turnID, Task: turn.task, Status: status, CompletedAt: now()}
	meta.UpdatedAt = now()
	h.finishInboxAttemptLocked(turn, status, errMsg)
	h.finishAgentMessageTurnLocked(turn, status, errMsg)
	h.persistRuntimeProjectionLocked()
	h.emitStatusLocked(meta, "idle")
	h.startPendingWorkersLocked(meta.ID)
}

// finishAgentMessageTurnLocked completes the handling attempt associated with
// this Turn. Once turn/start succeeded, delivery remains delivered forever;
// an interrupted or failed attempt is held until an explicit retry.
func (h *Hub) finishAgentMessageTurnLocked(turn *turnState, status, errMsg string) {
	if turn == nil || turn.agentMessageID == "" {
		return
	}
	msg := h.comms[turn.agentMessageID]
	if msg == nil {
		return
	}
	if msg.DeliveryStatus == "delivering" && turn.turnID != "" {
		if err := h.markAgentMessageHandlingRunningLocked(turn, msg.ToAgentID); err != nil {
			log.Printf("[codex-loom] establish message handling before finish %s: %v", msg.ID, err)
			return
		}
		msg = h.comms[turn.agentMessageID]
	}
	if msg == nil || (msg.DeliveryStatus != "delivering" && msg.DeliveryStatus != "delivered") {
		return
	}
	if msg.DeliveredTurnID != "" && turn.turnID != "" && msg.DeliveredTurnID != turn.turnID {
		return
	}

	next := *msg
	next.HandlingAttempts = cloneAgentMessageHandlingAttempts(msg.HandlingAttempts)
	next.UpdatedAt = now()

	// A failure before Codex confirms a Turn is still a delivery failure. It
	// requires an explicit retry and must never enter the automatic queue.
	if next.DeliveryStatus == "delivering" {
		next.DeliveryStatus = "failed"
		next.LastDeliveryError = strings.TrimSpace(errMsg)
		if next.LastDeliveryError == "" {
			next.LastDeliveryError = "turn did not start"
		}
		next.HandlingStatus = "pending"
		if err := h.commitAgentMessageLocked(next); err != nil {
			log.Printf("[codex-loom] save pre-start message failure: %v", err)
		}
		return
	}

	attemptIndex := -1
	for i := range next.HandlingAttempts {
		attempt := &next.HandlingAttempts[i]
		if (turn.handlingAttemptID != "" && attempt.ID == turn.handlingAttemptID) ||
			(turn.handlingAttemptID == "" && next.ActiveHandlingID != "" && attempt.ID == next.ActiveHandlingID) ||
			(turn.handlingAttemptID == "" && next.ActiveHandlingID == "" && turn.turnID != "" && attempt.TurnID == turn.turnID) {
			attemptIndex = i
			break
		}
	}
	if attemptIndex < 0 {
		startedAt := next.DeliveredAt
		if startedAt == "" {
			startedAt = next.UpdatedAt
		}
		attempt := AgentMessageHandlingAttempt{
			ID: newIntegrationID("matt"), TurnID: turn.turnID, Status: "running", StartedAt: startedAt,
		}
		next.HandlingAttempts = append(next.HandlingAttempts, attempt)
		attemptIndex = len(next.HandlingAttempts) - 1
	}

	attempt := &next.HandlingAttempts[attemptIndex]
	attempt.CompletedAt = next.UpdatedAt
	attempt.Error = strings.TrimSpace(errMsg)
	next.ActiveHandlingID = ""
	next.LastHandlingError = ""
	switch status {
	case "completed":
		attempt.Status = "completed"
		attempt.Error = ""
		next.HandlingStatus = "completed"
	case "interrupted":
		attempt.Status = "interrupted"
		next.HandlingStatus = "interrupted"
		next.LastHandlingError = attempt.Error
		if next.LastHandlingError == "" {
			next.LastHandlingError = "handling Turn interrupted"
			attempt.Error = next.LastHandlingError
		}
	default:
		attempt.Status = "failed"
		next.HandlingStatus = "failed"
		next.LastHandlingError = attempt.Error
		if next.LastHandlingError == "" {
			next.LastHandlingError = "handling Turn failed"
			attempt.Error = next.LastHandlingError
		}
	}
	if err := h.commitAgentMessageLocked(next); err != nil {
		log.Printf("[codex-loom] save message handling result: %v", err)
	}
}

// ---- public API ----

func (h *Hub) viewLocked(meta *Agent) AgentView {
	view := AgentView{Agent: *meta, PendingApprovals: []ApprovalView{}, LastSeq: h.seqs[meta.ID]}
	if goal := h.goals[meta.ID]; goal != nil {
		copy := *goal
		view.Goal = &copy
	}
	if rt, ok := h.runtimes[meta.ID]; ok && !rt.client.Closed() {
		view.ProcessAlive = true
		for id, ap := range rt.approvals {
			view.PendingApprovals = append(view.PendingApprovals, ApprovalView{
				ApprovalID: id, Method: ap.method, Params: ap.params, TS: ap.ts,
			})
		}
	}
	return view
}

func applyRolloutStatus(view *AgentView) {
	if view.ThreadID == "" {
		return
	}
	if view.Status == "running" && view.ProcessAlive {
		return
	}
	latest, err := rollout.LatestTurn(view.ThreadID)
	if err != nil || latest == nil || latest.ID == "" {
		return
	}
	// A restart-interrupted Turn remains visible until explicitly continued or
	// dismissed. Its rollout has no terminal event by definition, so do not
	// mistake a recently-written orphan for a live external Turn.
	if view.LastTurn != nil && view.LastTurn.TurnID == latest.ID && view.LastTurn.Status == "interrupted" {
		return
	}
	switch latest.Status {
	case "running":
		if !externalTurnLooksLive(view.ThreadID, latest.UpdatedAt) {
			view.Status = "interrupted"
			view.CurrentTask = ""
			view.CurrentTurnID = ""
			view.LastError = "interrupted: active Turn ended without a terminal event"
			view.LastTurn = &TurnSummary{
				TurnID:      latest.ID,
				Task:        displayRolloutTask(latest.Task),
				Status:      "interrupted",
				CompletedAt: latest.UpdatedAt,
			}
			if latest.UpdatedAt != "" {
				view.UpdatedAt = latest.UpdatedAt
			}
			return
		}
		task := displayRolloutTask(latest.Task)
		if task == "" {
			task = "external turn " + latest.ID
		}
		view.Status = "running"
		view.CurrentTask = task
		view.CurrentTurnID = latest.ID
		view.LastError = ""
		if latest.UpdatedAt != "" {
			view.UpdatedAt = latest.UpdatedAt
		}
	case "completed", "interrupted":
		if !view.ProcessAlive {
			view.Status = "idle"
			view.CurrentTask = ""
			view.CurrentTurnID = ""
		}
		view.LastTurn = &TurnSummary{
			TurnID:      latest.ID,
			Task:        displayRolloutTask(latest.Task),
			Status:      latest.Status,
			CompletedAt: latest.UpdatedAt,
		}
	}
}

func displayRolloutTask(task string) string {
	if strings.TrimSpace(task) == "" {
		return ""
	}
	return displayUserTask(task)
}

func externalTurnLooksLive(threadID, updatedAt string) bool {
	return timestampWithin(updatedAt, externalRunningStaleAfter)
}

func timestampWithin(ts string, d time.Duration) bool {
	if strings.TrimSpace(ts) == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return false
	}
	age := time.Since(t)
	return age >= 0 && age <= d
}
