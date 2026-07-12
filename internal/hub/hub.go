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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
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
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Cwd                string       `json:"cwd"`
	ThreadID           string       `json:"threadId"`
	Sandbox            string       `json:"sandbox"`
	ApprovalPolicy     string       `json:"approvalPolicy"`
	Model              string       `json:"model,omitempty"`
	Effort             string       `json:"effort,omitempty"`
	ProfileVersionSeen int          `json:"profileVersionSeen,omitempty"`
	Status             string       `json:"status"`
	CurrentTask        string       `json:"currentTask"`
	CurrentTurnID      string       `json:"currentTurnId"`
	LastError          string       `json:"lastError"`
	LastTurn           *TurnSummary `json:"lastTurn"`
	CreatedAt          string       `json:"createdAt"`
	UpdatedAt          string       `json:"updatedAt"`
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
	ID                 string `json:"id"`
	FromAgentID        string `json:"fromAgentId"`
	ToAgentID          string `json:"toAgentId"`
	From               string `json:"from"`
	To                 string `json:"to"`
	Subject            string `json:"subject"`
	Body               string `json:"body"`
	Response           string `json:"response"`
	ReplyTo            string `json:"replyTo,omitempty"`
	Status             string `json:"status"`               // open, answered, closed
	Resolution         string `json:"resolution,omitempty"` // reply, no_reply
	DeliveryStatus     string `json:"deliveryStatus"`       // queued, delivering, delivered, failed, cancelled
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	DeliveredAt        string `json:"deliveredAt,omitempty"`
	LastDeliveryError  string `json:"lastDeliveryError,omitempty"`
	DeliveredAgentID   string `json:"deliveredAgentId,omitempty"`
	DeliveredSessionID string `json:"deliveredSessionId,omitempty"` // compatibility
	DeliveredTurnID    string `json:"deliveredTurnId,omitempty"`
}

type CommParams struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Subject    string        `json:"subject"`
	Body       string        `json:"body"`
	Response   string        `json:"response"`
	ReplyTo    string        `json:"replyTo"`
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
	turnID       string
	task         string
	inboxItemID  string
	attemptID    string
	finalAnswer  string
	startedAt    time.Time
	lastActivity time.Time
	finished     bool
	stopWatchdog chan struct{}
}

type runtime struct {
	agentID        string
	client         *codex.Client
	hostGeneration uint64
	ready          chan struct{}
	initErr        error
	startMu        sync.Mutex

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
	st *store.Store

	mu                      sync.Mutex
	agents                  map[string]*Agent
	comms                   map[string]*AgentMessage
	commOrder               []string
	schedules               map[string]*Schedule
	profiles                map[string]*AgentProfile
	teamLinks               map[string]*TeamRelationship
	connections             map[string]*PlatformConnection
	addresses               map[string]*AgentAddress
	memberships             map[string]*ConversationMembership
	messages                map[string]*InboxMessage
	messageOrder            []string
	externalMessages        map[string]string
	inbox                   map[string]*InboxItem
	inboxOrder              []string
	attempts                map[string]*HandlingAttempt
	outbox                  map[string]*OutboxItem
	outboxOrder             []string
	seqs                    map[string]int64
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
	background              sync.WaitGroup
}

func New(st *store.Store) *Hub {
	h := &Hub{
		st:               st,
		agents:           map[string]*Agent{},
		comms:            map[string]*AgentMessage{},
		schedules:        map[string]*Schedule{},
		profiles:         map[string]*AgentProfile{},
		teamLinks:        map[string]*TeamRelationship{},
		connections:      map[string]*PlatformConnection{},
		addresses:        map[string]*AgentAddress{},
		memberships:      map[string]*ConversationMembership{},
		messages:         map[string]*InboxMessage{},
		externalMessages: map[string]string{},
		inbox:            map[string]*InboxItem{},
		attempts:         map[string]*HandlingAttempt{},
		outbox:           map[string]*OutboxItem{},
		seqs:             map[string]int64{},
		runtimes:         map[string]*runtime{},
		subs:             map[string]map[*subscriber]struct{}{},
		globalSubs:       map[*subscriber]struct{}{},
		stop:             make(chan struct{}),
	}
	if err := st.LoadAgents(&h.agents); err != nil {
		log.Printf("[codex-loom] load agents: %v", err)
	}
	if h.agents == nil {
		h.agents = map[string]*Agent{}
	}
	if err := h.st.LoadProfiles(&h.profiles); err != nil {
		log.Printf("[codex-loom] load profiles: %v", err)
	}
	if h.profiles == nil {
		h.profiles = map[string]*AgentProfile{}
	}
	if err := h.st.LoadTeamLinks(&h.teamLinks); err != nil {
		log.Printf("[codex-loom] load team links: %v", err)
	}
	if h.teamLinks == nil {
		h.teamLinks = map[string]*TeamRelationship{}
	}
	if err := h.loadIntegrations(); err != nil {
		log.Printf("[codex-loom] load integrations: %v", err)
	}
	if err := h.loadInboxState(); err != nil {
		log.Printf("[codex-loom] load inbox state: %v", err)
	}
	if err := h.loadComms(); err != nil {
		log.Printf("[codex-loom] load comms: %v", err)
	}
	if err := h.st.LoadSchedules(&h.schedules); err != nil {
		log.Printf("[codex-loom] load schedules: %v", err)
	}
	if h.schedules == nil {
		h.schedules = map[string]*Schedule{}
	}
	h.loadRemoteLocked()
	// Mirror pinix-edge's registry: edge-created agents become visible here
	// (read-only) and their rollout history is immediately viewable.
	h.importEdgeLocked()
	if err := h.migrateCommAgentIDsLocked(); err != nil {
		log.Printf("[codex-loom] migrate comm agent ids: %v", err)
	}
	// Reconcile: tasks running when the hub last died are interrupted.
	h.mu.Lock()
	for _, meta := range h.agents {
		h.seqs[meta.ID] = st.LastSeq(meta.ID)
		if meta.Source == "edge" {
			continue // edge mirrors carry no CodexLoom-driven turn state
		}
		if meta.Status == "running" {
			h.emitLocked(meta.ID, "loom/turn-interrupted", map[string]any{
				"reason": "loom-restart",
				"task":   meta.CurrentTask,
				"turnId": meta.CurrentTurnID,
			})
			meta.Status = "idle"
			meta.LastError = "interrupted: CodexLoom restarted while task was running"
			meta.CurrentTask = ""
			meta.CurrentTurnID = ""
			meta.UpdatedAt = now()
		}
	}
	h.persistLocked()
	h.mu.Unlock()
	h.background.Add(4)
	go func() { defer h.background.Done(); h.deliveryLoop() }()
	go func() { defer h.background.Done(); h.schedulerLoop() }()
	go func() { defer h.background.Done(); h.inboxLoop() }()
	go func() { defer h.background.Done(); h.remoteLoop() }()
	return h
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (h *Hub) loadComms() error {
	return h.st.ReadComms(func(raw json.RawMessage) {
		var rec commRecord
		if err := json.Unmarshal(raw, &rec); err != nil || rec.Message.ID == "" {
			return
		}
		msg := rec.Message
		if _, exists := h.comms[msg.ID]; !exists {
			h.commOrder = append(h.commOrder, msg.ID)
		}
		normalizeAgentMessage(&msg)
		h.comms[msg.ID] = &msg
	})
}

func normalizeAgentMessage(msg *AgentMessage) {
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
}

func (h *Hub) appendCommLocked(msg *AgentMessage) {
	rec := commRecord{Message: *msg}
	if err := h.st.AppendComm(rec); err != nil {
		log.Printf("[codex-loom] append comm: %v", err)
	}
	if _, exists := h.comms[msg.ID]; !exists {
		h.commOrder = append(h.commOrder, msg.ID)
	}
	cp := *msg
	h.comms[msg.ID] = &cp
	h.emitGlobalLocked("loom/comms-message", map[string]any{"message": cp})
}

func (h *Hub) emitGlobalLocked(typ string, data any) {
	ev := store.Event{TS: now(), Type: typ, Data: toRaw(data)}
	for sub := range h.globalSubs {
		select {
		case sub.ch <- ev:
		default:
			delete(h.globalSubs, sub)
			sub.close()
		}
	}
}

func (h *Hub) persistLocked() {
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
	if err := h.st.SaveAgents(own); err != nil {
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
		"lastError":      meta.LastError,
		"model":          meta.Model,
		"effort":         meta.Effort,
		"sandbox":        meta.Sandbox,
		"approvalPolicy": meta.ApprovalPolicy,
		"updatedAt":      meta.UpdatedAt,
	}
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
	go h.initRuntime(meta.ID, rt)
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
		result, err := rt.client.Request("thread/start", map[string]any{
			"sandbox": sandbox, "cwd": cwd,
		}, 30*time.Second)
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
			m.ThreadID = threadID
			m.UpdatedAt = now()
			h.persistLocked()
		}
		h.mu.Unlock()
		if err := setThreadName(rt.client, threadID, threadName); err != nil {
			log.Printf("[codex-loom] set thread name %s to %q: %v", threadID, threadName, err)
		}
		return nil
	}
	if threadID == "" {
		rt.initErr = startThread()
		return
	}
	err := resumeThread(rt.client, threadID, sandbox, cwd)
	if err != nil {
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
}

func resumeThread(client *codex.Client, threadID, sandbox, cwd string) error {
	_, err := client.Request("thread/resume", map[string]any{
		"threadId": threadID, "sandbox": sandbox, "cwd": cwd,
	}, 60*time.Second)
	return err
}

func (h *Hub) resumeAgentThread(agentID string, rt *runtime) error {
	h.mu.Lock()
	meta := h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		return errf(404, "agent vanished")
	}
	threadID, sandbox, cwd := meta.ThreadID, meta.Sandbox, meta.Cwd
	h.mu.Unlock()
	if strings.TrimSpace(threadID) == "" {
		return errf(409, "agent has no Codex Thread binding")
	}
	if err := resumeThread(rt.client, threadID, sandbox, cwd); err != nil {
		return errf(500, "resume Codex Thread: %s", err)
	}
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

	if method == "turn/started" && (rt.activeTurn == nil || rt.activeTurn.finished) {
		rt.activeTurn = &turnState{
			turnID: turnID, task: "Remote turn", startedAt: time.Now(), lastActivity: time.Now(),
			stopWatchdog: make(chan struct{}),
		}
		meta.Status = "running"
		meta.CurrentTask = rt.activeTurn.task
		meta.CurrentTurnID = turnID
		meta.LastError = ""
		meta.UpdatedAt = now()
		h.persistLocked()
		h.emitStatusLocked(meta, "running")
	}
	if text := notificationUserText(params); text != "" && rt.activeTurn != nil && !rt.activeTurn.finished {
		rt.activeTurn.task = text
		meta.CurrentTask = text
		meta.UpdatedAt = now()
		h.persistLocked()
		h.emitStatusLocked(meta, "running")
	}

	if rt.activeTurn != nil && !rt.activeTurn.finished {
		rt.activeTurn.lastActivity = time.Now()
		if text := completedFinalAnswer(method, params); text != "" {
			rt.activeTurn.finalAnswer = text
		}
		if turnID != "" && rt.activeTurn.turnID == "" {
			rt.activeTurn.turnID = turnID
			h.markInboxAttemptRunningLocked(rt.activeTurn)
			meta.CurrentTurnID = turnID
			h.persistLocked()
		}
	}

	h.emitLocked(meta.ID, method, params)

	switch method {
	case "turn/completed", "turn/failed", "turn/aborted":
		status := "completed"
		errMsg := ""
		if tp.Turn.Error != nil {
			errMsg = tp.Turn.Error.Message
		} else if tp.Error != nil {
			errMsg = tp.Error.Message
		}
		switch {
		case method == "turn/failed":
			status = "failed"
		case method == "turn/aborted", tp.Turn.Status == "interrupted":
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
		"durationMs": time.Since(turn.startedAt).Milliseconds(),
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	h.emitLocked(meta.ID, evType, payload)

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
	h.persistLocked()
	h.emitStatusLocked(meta, "idle")
	go h.deliverNextQueuedForTarget(meta.ID, defaultInactivity)
	go h.deliverNextInboxForAgent(meta.ID)
}

// ---- public API ----

func (h *Hub) viewLocked(meta *Agent) AgentView {
	view := AgentView{Agent: *meta, PendingApprovals: []ApprovalView{}, LastSeq: h.seqs[meta.ID]}
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
	switch latest.Status {
	case "running":
		if !externalTurnLooksLive(view.ThreadID, latest.UpdatedAt) {
			view.Status = "idle"
			view.CurrentTask = ""
			view.CurrentTurnID = ""
			view.LastError = ""
			view.LastTurn = &TurnSummary{
				TurnID:      latest.ID,
				Task:        latest.Task,
				Status:      "interrupted",
				CompletedAt: latest.UpdatedAt,
			}
			if latest.UpdatedAt != "" {
				view.UpdatedAt = latest.UpdatedAt
			}
			return
		}
		task := strings.TrimSpace(latest.Task)
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
			Task:        latest.Task,
			Status:      latest.Status,
			CompletedAt: latest.UpdatedAt,
		}
	}
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

// ListSessions is the pre-CodexLoom compatibility method.
func (h *Hub) ListSessions() []SessionView { return h.ListAgents() }

func (h *Hub) ListComms(agent, status string) []AgentMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent = strings.TrimSpace(agent)
	agentID := ""
	if meta := h.resolveLocked(agent); meta != nil {
		agentID = meta.ID
	}
	status = strings.TrimSpace(status)
	out := []AgentMessage{}
	for i := len(h.commOrder) - 1; i >= 0; i-- {
		msg := h.comms[h.commOrder[i]]
		if msg == nil {
			continue
		}
		if agent != "" && msg.From != agent && msg.To != agent && msg.FromAgentID != agent && msg.ToAgentID != agent && msg.FromAgentID != agentID && msg.ToAgentID != agentID {
			continue
		}
		if status != "" && msg.Status != status {
			continue
		}
		out = append(out, *msg)
	}
	return out
}

func (h *Hub) GetAgentMessage(id string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	return *msg, nil
}

func (h *Hub) CancelAgentMessage(id string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	if msg.DeliveryStatus != "queued" {
		return AgentMessage{}, errf(409, "message %s is %s; only queued messages can be cancelled", msg.ID, msg.DeliveryStatus)
	}
	msg.DeliveryStatus = "cancelled"
	msg.UpdatedAt = now()
	h.appendCommLocked(msg)
	return *msg, nil
}

// NoReplyAgentMessage explicitly closes a response-required internal message.
// It is idempotent for the same recipient and mutually exclusive with a reply.
func (h *Hub) NoReplyAgentMessage(id, fromKey string) (AgentMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.comms[strings.TrimSpace(id)]
	if msg == nil {
		return AgentMessage{}, errf(404, "message not found: %s", id)
	}
	from := h.resolveLocked(strings.TrimSpace(fromKey))
	if from == nil || from.ID != msg.ToAgentID {
		return AgentMessage{}, errf(403, "only %s can close message %s", msg.To, msg.ID)
	}
	if msg.Resolution == "no_reply" {
		return *msg, nil
	}
	if msg.Status == "answered" || msg.Resolution == "reply" {
		return AgentMessage{}, errf(409, "message %s already has a reply", msg.ID)
	}
	if msg.Response != "required" {
		return AgentMessage{}, errf(409, "message %s does not require a response", msg.ID)
	}
	msg.Status = "closed"
	msg.Resolution = "no_reply"
	msg.UpdatedAt = now()
	h.appendCommLocked(msg)
	return *msg, nil
}

func (h *Hub) SendAgentMessage(p CommParams) (CommResult, error) {
	if p.Timeout == 0 && p.TimeoutSec > 0 {
		p.Timeout = time.Duration(p.TimeoutSec) * time.Second
	}
	if p.ReplyTo != "" {
		return h.replyAgentMessage(p)
	}
	return h.createAgentMessage(p)
}

func (h *Hub) createAgentMessage(p CommParams) (CommResult, error) {
	from, to, err := h.validateCommEndpoints(p.From, p.To, p.System && p.From == schedulerIdentity)
	if err != nil {
		return CommResult{}, err
	}
	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	if subject == "" {
		return CommResult{}, errf(400, "subject is required")
	}
	if body == "" {
		return CommResult{}, errf(400, "body is required")
	}
	response := strings.TrimSpace(p.Response)
	if response == "" {
		response = "required"
	}
	if response != "required" && response != "none" {
		return CommResult{}, errf(400, "response must be required or none")
	}
	id := newMessageID()
	status := "closed"
	if response == "required" {
		status = "open"
	}
	msg := &AgentMessage{
		ID:             id,
		FromAgentID:    endpointID(from, p.From),
		ToAgentID:      to.ID,
		From:           endpointName(from, p.From),
		To:             to.Name,
		Subject:        subject,
		Body:           body,
		Response:       response,
		Status:         status,
		DeliveryStatus: "queued",
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	h.mu.Lock()
	h.appendCommLocked(msg)
	h.mu.Unlock()

	delivered, _ := h.deliverNextQueuedForTarget(to.ID, p.Timeout)
	current, err := h.GetAgentMessage(id)
	if err != nil {
		return CommResult{}, err
	}
	result := CommResult{Message: &current}
	if delivered != nil && delivered.ID == id {
		result.TurnID = delivered.DeliveredTurnID
	}
	return result, nil
}

func (h *Hub) replyAgentMessage(p CommParams) (CommResult, error) {
	fromName := strings.TrimSpace(p.From)
	body := strings.TrimSpace(p.Body)
	if fromName == "" {
		return CommResult{}, errf(400, "from is required")
	}
	if body == "" {
		return CommResult{}, errf(400, "body is required")
	}

	h.mu.Lock()
	orig := h.comms[p.ReplyTo]
	if orig == nil {
		h.mu.Unlock()
		return CommResult{}, errf(404, "message not found: %s", p.ReplyTo)
	}
	if orig.Response != "required" {
		h.mu.Unlock()
		return CommResult{}, errf(409, "message %s does not require a response", orig.ID)
	}
	if orig.Status != "open" || orig.Resolution == "no_reply" {
		h.mu.Unlock()
		return CommResult{}, errf(409, "message %s is already resolved", orig.ID)
	}
	if orig.FromAgentID == "" {
		if orig.From == schedulerIdentity {
			orig.FromAgentID = schedulerAgentID
		} else if sender := h.resolveLocked(orig.From); sender != nil {
			orig.FromAgentID = sender.ID
		}
	}
	if orig.ToAgentID == "" {
		if target := h.resolveLocked(orig.To); target != nil {
			orig.ToAgentID = target.ID
		}
	}
	origID := orig.ID
	origFrom := orig.From
	origSubject := orig.Subject
	from := h.resolveLocked(fromName)
	if from == nil {
		h.mu.Unlock()
		return CommResult{}, errf(404, "from agent not found: %s", fromName)
	}
	if from.ID != orig.ToAgentID {
		h.mu.Unlock()
		return CommResult{}, errf(400, "message %s expects replies from %s", origID, orig.To)
	}
	subject := strings.TrimSpace(p.Subject)
	if subject == "" {
		subject = "Re: " + origSubject
	}
	toName := origFrom
	var to *Agent
	if orig.FromAgentID != schedulerAgentID {
		to = h.resolveLocked(orig.FromAgentID)
		if to == nil {
			h.mu.Unlock()
			return CommResult{}, errf(404, "original sender agent not found: %s", origFrom)
		}
		toName = to.Name
	}
	msg := &AgentMessage{
		ID:             newMessageID(),
		FromAgentID:    from.ID,
		ToAgentID:      orig.FromAgentID,
		From:           from.Name,
		To:             toName,
		Subject:        subject,
		Body:           body,
		Response:       "none",
		ReplyTo:        origID,
		Status:         "closed",
		Resolution:     "reply",
		DeliveryStatus: "queued",
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if orig.FromAgentID == schedulerAgentID {
		msg.DeliveryStatus = "delivered"
		msg.DeliveredAt = msg.CreatedAt
		msg.UpdatedAt = msg.CreatedAt
		h.appendCommLocked(msg)
		h.markOriginalAnsweredLocked(msg)
		cp := *msg
		h.mu.Unlock()
		return CommResult{Message: &cp}, nil
	}
	h.appendCommLocked(msg)
	h.mu.Unlock()

	delivered, _ := h.deliverNextQueuedForTarget(to.ID, p.Timeout)
	current, err := h.GetAgentMessage(msg.ID)
	if err != nil {
		return CommResult{}, err
	}
	result := CommResult{Message: &current}
	if delivered != nil && delivered.ID == msg.ID {
		result.TurnID = delivered.DeliveredTurnID
	}
	return result, nil
}

func (h *Hub) validateCommEndpoints(fromKey, toKey string, allowSystemFrom bool) (*Agent, *Agent, error) {
	fromKey = strings.TrimSpace(fromKey)
	toKey = strings.TrimSpace(toKey)
	if fromKey == "" {
		return nil, nil, errf(400, "from is required")
	}
	if toKey == "" {
		return nil, nil, errf(400, "to is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	from := h.resolveLocked(fromKey)
	if from == nil && !(allowSystemFrom && fromKey == schedulerIdentity) {
		return nil, nil, errf(404, "from agent not found: %s", fromKey)
	}
	to := h.resolveLocked(toKey)
	if to == nil {
		return nil, nil, errf(404, "to agent not found: %s", toKey)
	}
	return from, to, nil
}

func endpointName(s *Agent, fallback string) string {
	if s != nil {
		return s.Name
	}
	return strings.TrimSpace(fallback)
}

func endpointID(s *Agent, fallback string) string {
	if s != nil {
		return s.ID
	}
	if strings.TrimSpace(fallback) == schedulerIdentity {
		return schedulerAgentID
	}
	return legacyAgentID(fallback)
}

func (h *Hub) deliveryLoop() {
	h.drainQueuedAll()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.drainQueuedAll()
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) drainQueuedAll() {
	targets := h.queuedTargets()
	for _, target := range targets {
		h.deliverNextQueuedForTarget(target, defaultInactivity)
	}
}

func (h *Hub) queuedTargets() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.DeliveryStatus != "queued" || seen[msg.ToAgentID] {
			continue
		}
		seen[msg.ToAgentID] = true
		out = append(out, msg.ToAgentID)
	}
	return out
}

func (h *Hub) deliverNextQueuedForTarget(target string, timeout time.Duration) (*AgentMessage, bool) {
	view, err := h.GetAgent(target)
	if err != nil {
		if isNotFoundErr(err) {
			h.failQueuedForTarget(target, err)
		}
		return nil, false
	}
	if view.Status == "running" {
		return nil, false
	}

	h.mu.Lock()
	targetMeta := h.resolveLocked(target)
	if targetMeta == nil {
		h.mu.Unlock()
		h.failQueuedForTarget(target, errf(404, "agent not found: %s", target))
		return nil, false
	}
	if rt := h.runtimes[targetMeta.ID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return nil, false
	}
	var snapshot *AgentMessage
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.ToAgentID != targetMeta.ID || msg.DeliveryStatus != "queued" {
			continue
		}
		msg.DeliveryStatus = "delivering"
		msg.LastDeliveryError = ""
		msg.UpdatedAt = now()
		h.appendCommLocked(msg)
		cp := *msg
		snapshot = &cp
		break
	}
	h.mu.Unlock()
	if snapshot == nil {
		return nil, false
	}

	result, err := h.SendTask(snapshot.ToAgentID, formatAgentEnvelope(snapshot), timeout)
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.comms[snapshot.ID]
	if current == nil {
		return nil, false
	}
	if err != nil {
		current.UpdatedAt = now()
		current.LastDeliveryError = err.Error()
		if isBusyErr(err) {
			current.DeliveryStatus = "queued"
		} else {
			current.DeliveryStatus = "failed"
		}
		h.appendCommLocked(current)
		cp := *current
		return &cp, false
	}
	current.DeliveryStatus = "delivered"
	current.DeliveredAgentID = result.AgentID
	current.DeliveredSessionID = result.AgentID
	current.DeliveredTurnID = result.TurnID
	current.DeliveredAt = now()
	current.UpdatedAt = current.DeliveredAt
	current.LastDeliveryError = ""
	h.appendCommLocked(current)
	h.markOriginalAnsweredLocked(current)
	cp := *current
	return &cp, true
}

func (h *Hub) failQueuedForTarget(target string, cause error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil || msg.ToAgentID != target || msg.DeliveryStatus != "queued" {
			continue
		}
		msg.DeliveryStatus = "failed"
		msg.LastDeliveryError = cause.Error()
		msg.UpdatedAt = now()
		h.appendCommLocked(msg)
	}
}

func (h *Hub) markOriginalAnsweredLocked(reply *AgentMessage) {
	if reply.ReplyTo == "" || reply.DeliveryStatus != "delivered" {
		return
	}
	orig := h.comms[reply.ReplyTo]
	if orig == nil || orig.Status != "open" {
		return
	}
	orig.Status = "answered"
	orig.Resolution = "reply"
	orig.UpdatedAt = now()
	h.appendCommLocked(orig)
}

func isBusyErr(err error) bool {
	var hubErr *HubError
	if ok := errors.As(err, &hubErr); ok && hubErr.Status == 409 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already running") || strings.Contains(msg, " is running")
}

func isNotFoundErr(err error) bool {
	var hubErr *HubError
	return errors.As(err, &hubErr) && hubErr.Status == 404
}

func newMessageID() string {
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	return "msg_" + hex.EncodeToString(idBytes)
}

func formatAgentEnvelope(msg *AgentMessage) string {
	var b strings.Builder
	b.WriteString(`<agent_message version="1" id="`)
	b.WriteString(xmlEscape(msg.ID))
	b.WriteString(`" response="`)
	b.WriteString(xmlEscape(msg.Response))
	b.WriteString(`" status="`)
	b.WriteString(xmlEscape(msg.Status))
	b.WriteString(`">` + "\n")
	writeXMLText(&b, "from", msg.From)
	writeXMLText(&b, "to", msg.To)
	writeXMLText(&b, "subject", msg.Subject)
	if msg.ReplyTo != "" {
		writeXMLText(&b, "reply_to", msg.ReplyTo)
	}
	if msg.Response == "required" {
		writeXMLText(&b, "reply_command", "loom msg --reply-to "+msg.ID+" --from "+msg.To+" --body \"...\"")
	}
	writeXMLCDATA(&b, "body", msg.Body)
	b.WriteString("</agent_message>")
	return b.String()
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func writeXMLText(b *strings.Builder, tag, value string) {
	b.WriteString("  <")
	b.WriteString(tag)
	b.WriteString(">")
	b.WriteString(xmlEscape(value))
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">\n")
}

func writeXMLCDATA(b *strings.Builder, tag, value string) {
	b.WriteString("  <")
	b.WriteString(tag)
	b.WriteString("><![CDATA[")
	b.WriteString(strings.ReplaceAll(value, "]]>", "]]]]><![CDATA[>"))
	b.WriteString("]]></")
	b.WriteString(tag)
	b.WriteString(">\n")
}

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
	Model          string `json:"model"`
	Effort         string `json:"effort"`
}

// RestoreAgentParams re-registers a previously archived Agent without
// creating a replacement identity or starting a Turn. Profiles and team
// relationships are stored independently and reconnect through the stable ID.
type RestoreAgentParams struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Cwd                string `json:"cwd"`
	ThreadID           string `json:"threadId"`
	Sandbox            string `json:"sandbox"`
	ApprovalPolicy     string `json:"approvalPolicy"`
	Model              string `json:"model"`
	Effort             string `json:"effort"`
	ProfileVersionSeen int    `json:"profileVersionSeen"`
	CreatedAt          string `json:"createdAt"`
}

type ConfigParams struct {
	Name           *string `json:"name"`
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	Sandbox        *string `json:"sandbox"`
	ApprovalPolicy *string `json:"approvalPolicy"`
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
	p.Effort = normalizeEffort(strings.TrimSpace(p.Effort))
	if p.Effort != "" {
		if !validEffort(p.Effort) {
			return AgentView{}, errf(400, "effort must be one of: minimal, low, medium, high, xhigh")
		}
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
		Sandbox: p.Sandbox, ApprovalPolicy: p.ApprovalPolicy, Model: p.Model, Effort: p.Effort,
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
		h.persistLocked()
		h.mu.Unlock()
		return AgentView{}, errf(500, "failed to start codex thread: %s", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.persistLocked()
	h.emitLocked(id, "loom/agent-created", map[string]any{
		"id": id, "name": meta.Name, "cwd": meta.Cwd, "threadId": meta.ThreadID,
	})
	h.emitStatusLocked(meta, meta.Status)
	return h.viewLocked(meta), nil
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
	if p.Effort != "" && !validEffort(p.Effort) {
		return AgentView{}, errf(400, "effort must be one of: minimal, low, medium, high, xhigh")
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
		Model: p.Model, Effort: p.Effort, ProfileVersionSeen: p.ProfileVersionSeen,
		Status: "idle", CreatedAt: p.CreatedAt, UpdatedAt: now(),
	}
	h.agents[p.ID] = meta
	h.seqs[p.ID] = h.st.LastSeq(p.ID)
	h.persistLocked()
	h.emitLocked(p.ID, "loom/agent-restored", map[string]any{
		"id": p.ID, "name": p.Name, "cwd": p.Cwd, "threadId": p.ThreadID,
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
	if p.Effort != nil {
		effort := normalizeEffort(strings.TrimSpace(*p.Effort))
		if effort == "" || validEffort(effort) {
			nextEffort = effort
		} else {
			h.mu.Unlock()
			return AgentView{}, errf(400, "effort must be one of: minimal, low, medium, high, xhigh")
		}
	}
	if p.Sandbox != nil {
		nextSandbox = strings.TrimSpace(*p.Sandbox)
	}
	if p.ApprovalPolicy != nil {
		nextApprovalPolicy = strings.TrimSpace(*p.ApprovalPolicy)
	}
	nameChanged := meta.Name != nextName
	meta.Source = "" // editing config adopts an edge mirror into CodexLoom's registry
	meta.Name = nextName
	meta.Model = nextModel
	meta.Effort = nextEffort
	meta.Sandbox = nextSandbox
	meta.ApprovalPolicy = nextApprovalPolicy
	meta.UpdatedAt = now()
	h.persistLocked()
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
	case "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
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

func (h *Hub) sendTask(key, text string, inactivity time.Duration, inboxItemID, attemptID, developerContext string) (SendResult, error) {
	if strings.TrimSpace(text) == "" {
		return SendResult{}, errf(400, "text is required")
	}
	if inactivity <= 0 {
		inactivity = defaultInactivity
	}

	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return SendResult{}, errf(404, "agent not found: %s", key)
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
	h.mu.Unlock()

	// Serialize readiness, profile injection and turn reservation for one
	// runtime. Concurrent callers must not inject the same profile version.
	rt.startMu.Lock()
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
	if err := h.injectProfileIfNeeded(agentID, rt); err != nil {
		rt.startMu.Unlock()
		return SendResult{}, err
	}
	if strings.TrimSpace(developerContext) != "" {
		if err := h.injectDeveloperContext(agentID, rt, developerContext); err != nil {
			rt.startMu.Unlock()
			return SendResult{}, err
		}
	}

	h.mu.Lock()
	meta = h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(404, "agent vanished")
	}
	if rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		rt.startMu.Unlock()
		return SendResult{}, errf(409, "agent %q is already running a task", meta.Name)
	}
	turn := &turnState{
		task:         text,
		inboxItemID:  inboxItemID,
		attemptID:    attemptID,
		startedAt:    time.Now(),
		lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	rt.activeTurn = turn
	meta.Source = "" // adopting an edge mirror into CodexLoom's own registry
	meta.Status = "running"
	meta.CurrentTask = text
	meta.CurrentTurnID = ""
	meta.LastError = ""
	meta.UpdatedAt = now()
	h.persistLocked()
	h.emitLocked(agentID, "loom/user-message", map[string]any{"text": text})
	h.emitStatusLocked(meta, "running")
	threadID, approvalPolicy, model, effort := meta.ThreadID, meta.ApprovalPolicy, meta.Model, meta.Effort
	h.mu.Unlock()
	rt.startMu.Unlock()

	go h.watchdog(agentID, turn, inactivity)

	params := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]any{{"type": "text", "text": text}},
		"approvalPolicy": approvalPolicy,
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
			h.persistLocked()
		}
	}
	h.emitLocked(agentID, "loom/turn-started", map[string]any{"turnId": turn.turnID, "task": text})
	h.mu.Unlock()

	return SendResult{Dispatched: true, AgentID: agentID, SessionID: agentID, TurnID: turnID}, nil
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
	Interrupted bool   `json:"interrupted"`
	Message     string `json:"message,omitempty"`
	Reason      string `json:"reason,omitempty"`
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
			meta.Status = "idle"
			meta.CurrentTask = ""
			meta.CurrentTurnID = ""
			meta.LastError = reason
			meta.UpdatedAt = now()
			h.persistLocked()
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
	h.mu.Unlock()

	params := map[string]any{"threadId": threadID}
	if turnID != "" {
		params["turnId"] = turnID
	}
	_, err := client.Request("turn/interrupt", params, 10*time.Second)
	if err != nil {
		return InterruptResult{}, errf(500, "turn/interrupt failed: %s", err)
	}
	// codex should follow up with turn/completed(status=interrupted); force
	// the bookkeeping if that doesn't arrive shortly.
	go func() {
		time.Sleep(3 * time.Second)
		h.mu.Lock()
		defer h.mu.Unlock()
		if !turn.finished {
			if m := h.agents[agentID]; m != nil {
				h.finishTurnLocked(m, rt, "interrupted", reason)
			}
		}
	}()
	return InterruptResult{Interrupted: true, Reason: reason}, nil
}

func (h *Hub) ArchiveAgent(key string) (map[string]any, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return nil, errf(404, "agent not found: %s", key)
	}
	agentID := meta.ID
	rt := h.runtimes[agentID]
	hasActive := rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished
	threadID := meta.ThreadID
	name := meta.Name
	h.mu.Unlock()

	if hasActive {
		_, _ = h.Interrupt(agentID, "agent archived")
	}
	// Best-effort thread archive on the shared CodexHost connection.
	if rt != nil && !rt.client.Closed() {
		if waitReady(rt) == nil {
			_, _ = rt.client.Request("thread/archive", map[string]any{"threadId": threadID}, 10*time.Second)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.runtimes, agentID)
	if _, ok := h.agents[agentID]; !ok {
		return nil, errf(404, "agent not found: %s", key)
	}
	h.emitLocked(agentID, "loom/agent-archived", map[string]any{"id": agentID, "name": name})
	killed := *h.agents[agentID]
	delete(h.agents, agentID)
	h.persistLocked()
	killed.Status = "killed"
	h.emitStatusLocked(&killed, "killed")
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
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Items  []map[string]any `json:"items"`
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

	tr, err := rollout.Read(threadID)
	if err != nil {
		// No rollout on disk (e.g. a new Agent before its first Turn is
		// flushed). Not an error: report empty history for this Agent.
		log.Printf("[codex-loom] history: no rollout for %s (thread %s): %v", view.Name, threadID, err)
		return hist, nil
	}
	all := tr.Turns
	hist.Total = len(all)
	if len(all) > 0 && all[len(all)-1].Status == "running" && hist.Status != "running" {
		if latest, err := rollout.LatestTurn(threadID); err == nil && latest != nil && latest.Status == "running" && externalTurnLooksLive(threadID, latest.UpdatedAt) {
			hist.Status = "running"
		} else {
			all[len(all)-1].Status = "interrupted"
		}
	}
	// Window from the end: skip the newest `offset` turns, take `count` before them.
	end := len(all) - offset
	if end < 0 {
		end = 0
	}
	start := end - count
	if start < 0 {
		start = 0
	}
	turns := all[start:end]
	for _, t := range turns {
		items := t.Items
		if items == nil {
			items = []map[string]any{}
		}
		hist.Turns = append(hist.Turns, HistoryTurn{ID: t.ID, Status: t.Status, Items: items})
	}
	return hist, nil
}

// Shutdown closes all codex processes. Running agents keep status=running
// on disk so the next startup marks them interrupted.
func (h *Hub) Shutdown() {
	h.stopOnce.Do(func() {
		if h.stop != nil {
			close(h.stop)
		}
	})
	h.background.Wait()
	h.mu.Lock()
	host := h.codexHost
	h.codexHost = nil
	h.remoteRuntime = nil
	for _, rt := range h.runtimes {
		if rt.activeTurn != nil && !rt.activeTurn.finished {
			rt.activeTurn.finished = true
			if rt.activeTurn.stopWatchdog != nil {
				close(rt.activeTurn.stopWatchdog)
			}
		}
	}
	h.persistLocked()
	h.mu.Unlock()
	if host != nil {
		host.client.Close()
	}
}
