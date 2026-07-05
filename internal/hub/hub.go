// Package hub owns codex sessions. A session is a first-class entity: it
// belongs to the hub, not to any caller. Humans and AI agents come and go
// over the same API; the session — its codex subprocess, thread context and
// event log — stays.
//
// Process model: one long-lived `codex app-server` subprocess per session,
// spawned lazily and kept alive across turns. thread/resume restores context
// after a process or hub restart, so sessions survive both.
//
// Event flow: every JSON-RPC notification from codex is wrapped into
// {seq, ts, type, data}, appended to the session's ndjson log, and fanned out
// to all subscribers (SSE observers). Hub lifecycle events use the "hub/"
// type prefix; codex notifications keep their method name as the type.
//
// Locking rule: NEVER call client.Request while holding h.mu — responses are
// delivered by the reader goroutine, which also takes h.mu for notifications.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yan5xu/codex-hub/internal/codex"
	"github.com/yan5xu/codex-hub/internal/store"
)

const (
	defaultInactivity = 30 * time.Minute
	absoluteTurnCap   = 4 * time.Hour
	subscriberBuffer  = 1024
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

type Session struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Cwd            string       `json:"cwd"`
	ThreadID       string       `json:"threadId"`
	Sandbox        string       `json:"sandbox"`
	ApprovalPolicy string       `json:"approvalPolicy"`
	Model          string       `json:"model,omitempty"`
	Status         string       `json:"status"`
	CurrentTask    string       `json:"currentTask"`
	CurrentTurnID  string       `json:"currentTurnId"`
	LastError      string       `json:"lastError"`
	LastTurn       *TurnSummary `json:"lastTurn"`
	CreatedAt      string       `json:"createdAt"`
	UpdatedAt      string       `json:"updatedAt"`
}

// SessionView is what the API returns: metadata + live runtime info.
type SessionView struct {
	Session
	ProcessAlive     bool           `json:"processAlive"`
	PendingApprovals []ApprovalView `json:"pendingApprovals"`
	LastSeq          int64          `json:"lastSeq"`
}

type ApprovalView struct {
	ApprovalID string          `json:"approvalId"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	TS         string          `json:"ts"`
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
	startedAt    time.Time
	lastActivity time.Time
	finished     bool
	stopWatchdog chan struct{}
}

type runtime struct {
	sessionID string
	client    *codex.Client
	ready     chan struct{}
	initErr   error

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

	mu         sync.Mutex
	sessions   map[string]*Session
	seqs       map[string]int64
	runtimes   map[string]*runtime
	subs       map[string]map[*subscriber]struct{}
	globalSubs map[*subscriber]struct{}
}

func New(st *store.Store) *Hub {
	h := &Hub{
		st:         st,
		sessions:   map[string]*Session{},
		seqs:       map[string]int64{},
		runtimes:   map[string]*runtime{},
		subs:       map[string]map[*subscriber]struct{}{},
		globalSubs: map[*subscriber]struct{}{},
	}
	if err := st.LoadSessions(&h.sessions); err != nil {
		log.Printf("[hub] load sessions: %v", err)
	}
	if h.sessions == nil {
		h.sessions = map[string]*Session{}
	}
	// Reconcile: tasks running when the hub last died are interrupted.
	h.mu.Lock()
	for _, meta := range h.sessions {
		h.seqs[meta.ID] = st.LastSeq(meta.ID)
		if meta.Status == "running" {
			h.emitLocked(meta.ID, "hub/turn-interrupted", map[string]any{
				"reason": "hub-restart",
				"task":   meta.CurrentTask,
				"turnId": meta.CurrentTurnID,
			})
			meta.Status = "idle"
			meta.LastError = "interrupted: hub restarted while task was running"
			meta.CurrentTask = ""
			meta.CurrentTurnID = ""
			meta.UpdatedAt = now()
		}
	}
	h.persistLocked()
	h.mu.Unlock()
	return h
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (h *Hub) persistLocked() {
	if err := h.st.SaveSessions(h.sessions); err != nil {
		log.Printf("[hub] persist: %v", err)
	}
}

func (h *Hub) resolveLocked(key string) *Session {
	if s, ok := h.sessions[key]; ok {
		return s
	}
	for _, s := range h.sessions {
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

func (h *Hub) emitLocked(sessionID, typ string, data any) store.Event {
	h.seqs[sessionID]++
	ev := store.Event{Seq: h.seqs[sessionID], TS: now(), Type: typ, Data: toRaw(data)}
	if err := h.st.AppendEvent(sessionID, ev); err != nil {
		log.Printf("[hub] append event: %v", err)
	}
	for sub := range h.subs[sessionID] {
		select {
		case sub.ch <- ev:
		default:
			// Slow observer: drop it; SSE client reconnects and replays by seq.
			delete(h.subs[sessionID], sub)
			sub.close()
		}
	}
	return ev
}

func (h *Hub) emitStatusLocked(meta *Session, status string) {
	data, _ := json.Marshal(map[string]any{
		"id":          meta.ID,
		"name":        meta.Name,
		"status":      status,
		"currentTask": meta.CurrentTask,
		"lastError":   meta.LastError,
	})
	ev := store.Event{TS: now(), Type: "hub/session-status", Data: data}
	for sub := range h.globalSubs {
		select {
		case sub.ch <- ev:
		default:
			delete(h.globalSubs, sub)
			sub.close()
		}
	}
}

// Subscribe returns a channel of live events for a session plus a cancel func.
func (h *Hub) Subscribe(key string) (<-chan store.Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.resolveLocked(key)
	if meta == nil {
		return nil, nil, errf(404, "session not found: %s", key)
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
		return nil, errf(404, "session not found: %s", key)
	}
	return h.st.ReadEvents(meta.ID, since, tail)
}

// ---- runtime management ----

// getRuntimeLocked returns a live runtime, spawning codex if needed.
func (h *Hub) getRuntimeLocked(meta *Session) (*runtime, error) {
	if rt, ok := h.runtimes[meta.ID]; ok && !rt.client.Closed() {
		return rt, nil
	}
	client, err := codex.Spawn()
	if err != nil {
		return nil, errf(500, "spawn codex: %s", err)
	}
	rt := &runtime{
		sessionID: meta.ID,
		client:    client,
		ready:     make(chan struct{}),
		approvals: map[string]*approval{},
	}
	client.OnNotification = func(method string, params json.RawMessage) {
		h.onNotification(rt, method, params)
	}
	client.OnServerRequest = func(id json.RawMessage, method string, params json.RawMessage) {
		h.onServerRequest(rt, id, method, params)
	}
	client.OnClose = func() {
		h.onClientClose(rt)
	}
	h.runtimes[meta.ID] = rt
	go h.initRuntime(meta.ID, rt)
	return rt, nil
}

// initRuntime runs without the hub lock (talks to codex).
func (h *Hub) initRuntime(sessionID string, rt *runtime) {
	defer close(rt.ready)
	h.mu.Lock()
	meta := h.sessions[sessionID]
	if meta == nil {
		h.mu.Unlock()
		rt.initErr = errf(404, "session vanished")
		return
	}
	threadID, sandbox, cwd := meta.ThreadID, meta.Sandbox, meta.Cwd
	h.mu.Unlock()

	if err := rt.client.Initialize(); err != nil {
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
		h.mu.Lock()
		if m := h.sessions[sessionID]; m != nil {
			m.ThreadID = parsed.Thread.ID
			m.UpdatedAt = now()
			h.persistLocked()
		}
		h.mu.Unlock()
		return nil
	}
	if threadID == "" {
		rt.initErr = startThread()
		return
	}
	_, err := rt.client.Request("thread/resume", map[string]any{
		"threadId": threadID, "sandbox": sandbox, "cwd": cwd,
	}, 60*time.Second)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no rollout") || strings.Contains(msg, "not found") {
			rt.initErr = startThread()
		} else {
			rt.initErr = err
		}
	}
}

func waitReady(rt *runtime) error {
	<-rt.ready
	return rt.initErr
}

func (h *Hub) onClientClose(rt *runtime) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runtimes[rt.sessionID] == rt {
		delete(h.runtimes, rt.sessionID)
	}
	meta := h.sessions[rt.sessionID]
	if meta == nil {
		return
	}
	if rt.activeTurn != nil && !rt.activeTurn.finished {
		h.emitLocked(meta.ID, "hub/error", map[string]any{"message": "codex app-server exited mid-turn"})
		h.finishTurnLocked(meta, rt, "interrupted", "codex process exited")
	}
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

func (h *Hub) onNotification(rt *runtime, method string, params json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.sessions[rt.sessionID]
	if meta == nil {
		return
	}
	var tp turnParams
	_ = json.Unmarshal(params, &tp)

	if rt.activeTurn != nil && !rt.activeTurn.finished {
		rt.activeTurn.lastActivity = time.Now()
		turnID := tp.TurnID
		if turnID == "" {
			turnID = tp.Turn.ID
		}
		if turnID != "" && rt.activeTurn.turnID == "" {
			rt.activeTurn.turnID = turnID
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
	meta := h.sessions[rt.sessionID]
	if strings.Contains(strings.ToLower(method), "approval") {
		apID := "ap-" + strings.Trim(string(id), `"`)
		rt.approvals[apID] = &approval{rpcID: id, method: method, params: params, ts: now()}
		if rt.activeTurn != nil && !rt.activeTurn.finished {
			rt.activeTurn.lastActivity = time.Now()
		}
		if meta != nil {
			h.emitLocked(meta.ID, "hub/approval-requested", map[string]any{
				"approvalId": apID,
				"method":     method,
				"params":     params,
			})
		}
		return
	}
	// Unknown server->client request: answer with an error so codex won't hang.
	if meta != nil {
		h.emitLocked(meta.ID, "hub/server-request", map[string]any{"method": method, "params": params})
	}
	_ = rt.client.RespondError(id, -32601, "codex-hub does not handle "+method)
}

func (h *Hub) ResolveApproval(key, approvalID, decision string) (map[string]any, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return nil, errf(404, "session not found: %s", key)
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
	d := "reject"
	if decision == "accept" || decision == "approve" {
		d = "accept"
	}
	h.emitLocked(meta.ID, "hub/approval-resolved", map[string]any{
		"approvalId": approvalID, "decision": d, "method": ap.method,
	})
	client := rt.client
	h.mu.Unlock()

	if err := client.Respond(ap.rpcID, map[string]any{"decision": d}); err != nil {
		return nil, errf(500, "respond approval: %s", err)
	}
	return map[string]any{"approvalId": approvalID, "decision": d}, nil
}

func (h *Hub) finishTurnLocked(meta *Session, rt *runtime, status, errMsg string) {
	turn := rt.activeTurn
	if turn == nil || turn.finished {
		return
	}
	turn.finished = true
	close(turn.stopWatchdog)
	rt.activeTurn = nil
	rt.approvals = map[string]*approval{}

	evType := "hub/turn-completed"
	if status == "failed" {
		evType = "hub/turn-failed"
	} else if status == "interrupted" {
		evType = "hub/turn-interrupted"
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
	h.persistLocked()
	h.emitStatusLocked(meta, "idle")
}

// ---- public API ----

func (h *Hub) viewLocked(meta *Session) SessionView {
	view := SessionView{Session: *meta, PendingApprovals: []ApprovalView{}, LastSeq: h.seqs[meta.ID]}
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

func (h *Hub) ListSessions() []SessionView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SessionView, 0, len(h.sessions))
	for _, meta := range h.sessions {
		out = append(out, h.viewLocked(meta))
	}
	return out
}

func (h *Hub) GetSession(key string) (SessionView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.resolveLocked(key)
	if meta == nil {
		return SessionView{}, errf(404, "session not found: %s", key)
	}
	return h.viewLocked(meta), nil
}

type CreateParams struct {
	Name           string `json:"name"`
	Cwd            string `json:"cwd"`
	Sandbox        string `json:"sandbox"`
	ApprovalPolicy string `json:"approvalPolicy"`
	Model          string `json:"model"`
}

func (h *Hub) CreateSession(p CreateParams) (SessionView, error) {
	if p.Name == "" || p.Cwd == "" {
		return SessionView{}, errf(400, "name and cwd are required")
	}
	if !nameRe.MatchString(p.Name) {
		return SessionView{}, errf(400, "name must match [a-zA-Z0-9_-]+")
	}
	if p.Sandbox == "" {
		p.Sandbox = "danger-full-access"
	}
	if p.ApprovalPolicy == "" {
		p.ApprovalPolicy = "never"
	}
	idBytes := make([]byte, 4)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	h.mu.Lock()
	if h.resolveLocked(p.Name) != nil {
		h.mu.Unlock()
		return SessionView{}, errf(409, "session %q already exists", p.Name)
	}
	meta := &Session{
		ID: id, Name: p.Name, Cwd: p.Cwd,
		Sandbox: p.Sandbox, ApprovalPolicy: p.ApprovalPolicy, Model: p.Model,
		Status: "idle", CreatedAt: now(), UpdatedAt: now(),
	}
	h.sessions[id] = meta
	h.seqs[id] = 0
	rt, err := h.getRuntimeLocked(meta)
	if err != nil {
		delete(h.sessions, id)
		h.mu.Unlock()
		return SessionView{}, err
	}
	h.mu.Unlock()

	if err := waitReady(rt); err != nil {
		h.mu.Lock()
		delete(h.sessions, id)
		delete(h.runtimes, id)
		h.persistLocked()
		h.mu.Unlock()
		rt.client.Close()
		return SessionView{}, errf(500, "failed to start codex thread: %s", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.persistLocked()
	h.emitLocked(id, "hub/session-created", map[string]any{
		"id": id, "name": meta.Name, "cwd": meta.Cwd, "threadId": meta.ThreadID,
	})
	h.emitStatusLocked(meta, meta.Status)
	return h.viewLocked(meta), nil
}

type SendResult struct {
	Dispatched bool   `json:"dispatched"`
	SessionID  string `json:"sessionId"`
	TurnID     string `json:"turnId"`
}

func (h *Hub) SendTask(key, text string, inactivity time.Duration) (SendResult, error) {
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
		return SendResult{}, errf(404, "session not found: %s", key)
	}
	if rt, ok := h.runtimes[meta.ID]; ok && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return SendResult{}, errf(409, "session %q is already running a task", meta.Name)
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
	sessionID := meta.ID
	h.mu.Unlock()

	if err := waitReady(rt); err != nil {
		return SendResult{}, errf(500, "codex not ready: %s", err)
	}

	h.mu.Lock()
	meta = h.sessions[sessionID]
	if meta == nil {
		h.mu.Unlock()
		return SendResult{}, errf(404, "session vanished")
	}
	if rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return SendResult{}, errf(409, "session %q is already running a task", meta.Name)
	}
	turn := &turnState{
		task:         text,
		startedAt:    time.Now(),
		lastActivity: time.Now(),
		stopWatchdog: make(chan struct{}),
	}
	rt.activeTurn = turn
	meta.Status = "running"
	meta.CurrentTask = text
	meta.CurrentTurnID = ""
	meta.LastError = ""
	meta.UpdatedAt = now()
	h.persistLocked()
	h.emitLocked(sessionID, "hub/user-message", map[string]any{"text": text})
	h.emitStatusLocked(meta, "running")
	threadID, approvalPolicy, model := meta.ThreadID, meta.ApprovalPolicy, meta.Model
	h.mu.Unlock()

	go h.watchdog(sessionID, turn, inactivity)

	params := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]any{{"type": "text", "text": text}},
		"approvalPolicy": approvalPolicy,
	}
	if model != "" {
		params["model"] = model
	}
	result, err := rt.client.Request("turn/start", params, 30*time.Second)
	if err != nil {
		h.mu.Lock()
		if m := h.sessions[sessionID]; m != nil {
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
		if m := h.sessions[sessionID]; m != nil {
			m.CurrentTurnID = turnID
			h.persistLocked()
		}
	}
	h.emitLocked(sessionID, "hub/turn-started", map[string]any{"turnId": turn.turnID, "task": text})
	h.mu.Unlock()

	return SendResult{Dispatched: true, SessionID: sessionID, TurnID: turnID}, nil
}

func (h *Hub) watchdog(sessionID string, turn *turnState, inactivity time.Duration) {
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
				_, _ = h.Interrupt(sessionID, fmt.Sprintf("inactivity timeout (%s)", inactivity))
				return
			}
			if total > absoluteTurnCap {
				_, _ = h.Interrupt(sessionID, "absolute turn cap (4h)")
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
		return InterruptResult{}, errf(404, "session not found: %s", key)
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
	sessionID := meta.ID
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
		// Fallback: kill the process. Thread state survives in the rollout;
		// the next turn respawns and thread/resumes.
		client.Close()
		h.mu.Lock()
		if m := h.sessions[sessionID]; m != nil {
			h.finishTurnLocked(m, rt, "interrupted", fmt.Sprintf("%s (process killed: %s)", reason, err))
		}
		h.mu.Unlock()
		return InterruptResult{Interrupted: true, Reason: reason}, nil
	}
	// codex should follow up with turn/completed(status=interrupted); force
	// the bookkeeping if that doesn't arrive shortly.
	go func() {
		time.Sleep(3 * time.Second)
		h.mu.Lock()
		defer h.mu.Unlock()
		if !turn.finished {
			if m := h.sessions[sessionID]; m != nil {
				h.finishTurnLocked(m, rt, "interrupted", reason)
			}
		}
	}()
	return InterruptResult{Interrupted: true, Reason: reason}, nil
}

func (h *Hub) KillSession(key string) (map[string]any, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return nil, errf(404, "session not found: %s", key)
	}
	sessionID := meta.ID
	rt := h.runtimes[sessionID]
	hasActive := rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished
	threadID := meta.ThreadID
	name := meta.Name
	h.mu.Unlock()

	if hasActive {
		_, _ = h.Interrupt(sessionID, "session killed")
	}
	// Best-effort thread archive on a live client.
	if rt != nil && !rt.client.Closed() {
		if waitReady(rt) == nil {
			_, _ = rt.client.Request("thread/archive", map[string]any{"threadId": threadID}, 10*time.Second)
		}
		rt.client.Close()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.runtimes, sessionID)
	if _, ok := h.sessions[sessionID]; !ok {
		return nil, errf(404, "session not found: %s", key)
	}
	h.emitLocked(sessionID, "hub/session-killed", map[string]any{"id": sessionID, "name": name})
	killed := *h.sessions[sessionID]
	delete(h.sessions, sessionID)
	h.persistLocked()
	killed.Status = "killed"
	h.emitStatusLocked(&killed, "killed")
	return map[string]any{"killed": true, "id": sessionID, "name": name}, nil
}

// ---- history (thread/read) ----

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
	Turns    []HistoryTurn `json:"turns"`
}

func (h *Hub) History(key string, count int) (History, error) {
	if count <= 0 {
		count = 10
	}
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return History{}, errf(404, "session not found: %s", key)
	}
	rt, err := h.getRuntimeLocked(meta)
	sessionID := meta.ID
	h.mu.Unlock()
	if err != nil {
		return History{}, err
	}
	if err := waitReady(rt); err != nil {
		return History{}, errf(500, "codex not ready: %s", err)
	}

	h.mu.Lock()
	meta = h.sessions[sessionID]
	if meta == nil {
		h.mu.Unlock()
		return History{}, errf(404, "session vanished")
	}
	threadID := meta.ThreadID
	hist := History{ID: meta.ID, Name: meta.Name, Cwd: meta.Cwd, ThreadID: threadID, Status: meta.Status}
	h.mu.Unlock()

	result, err := rt.client.Request("thread/read", map[string]any{
		"threadId": threadID, "includeTurns": true,
	}, 30*time.Second)
	if err != nil {
		return History{}, errf(500, "thread/read failed: %s", err)
	}
	var parsed struct {
		Thread struct {
			Turns []struct {
				ID     string            `json:"id"`
				Status string            `json:"status"`
				Items  []json.RawMessage `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return History{}, errf(500, "thread/read parse: %s", err)
	}
	turns := parsed.Thread.Turns
	if len(turns) > count {
		turns = turns[len(turns)-count:]
	}
	for _, t := range turns {
		ht := HistoryTurn{ID: t.ID, Status: t.Status, Items: []map[string]any{}}
		for _, raw := range t.Items {
			ht.Items = append(ht.Items, simplifyItem(raw))
		}
		hist.Turns = append(hist.Turns, ht)
	}
	return hist, nil
}

func simplifyItem(raw json.RawMessage) map[string]any {
	var item struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		Text             string `json:"text"`
		Phase            string `json:"phase"`
		Summary          string `json:"summary"`
		Command          string `json:"command"`
		Cwd              string `json:"cwd"`
		Status           string `json:"status"`
		AggregatedOutput string `json:"aggregatedOutput"`
		Output           string `json:"output"`
		ExitCode         *int   `json:"exitCode"`
		DurationMs       *int64 `json:"durationMs"`
		Content          []struct {
			Text string `json:"text"`
		} `json:"content"`
		Changes []struct {
			Path string          `json:"path"`
			Kind json.RawMessage `json:"kind"`
			Diff string          `json:"diff"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return map[string]any{"type": "unknown"}
	}
	switch item.Type {
	case "userMessage":
		text := item.Text
		for _, c := range item.Content {
			text += c.Text
		}
		return map[string]any{"type": "user", "text": text}
	case "agentMessage":
		typ := "thinking"
		if item.Phase == "final_answer" || item.Phase == "" {
			typ = "answer"
		}
		return map[string]any{"type": typ, "text": item.Text}
	case "reasoning":
		text := item.Text
		if text == "" {
			text = item.Summary
		}
		return map[string]any{"type": "reasoning", "text": truncate(text, 2000)}
	case "commandExecution":
		output := item.AggregatedOutput
		if output == "" {
			output = item.Output
		}
		m := map[string]any{
			"type": "command", "id": item.ID, "command": item.Command,
			"cwd": item.Cwd, "status": item.Status, "output": truncate(output, 4000),
		}
		if item.ExitCode != nil {
			m["exitCode"] = *item.ExitCode
		}
		if item.DurationMs != nil {
			m["durationMs"] = *item.DurationMs
		}
		return m
	case "fileChange":
		changes := []map[string]any{}
		for _, c := range item.Changes {
			changes = append(changes, map[string]any{
				"path": c.Path, "kind": kindString(c.Kind), "diff": truncate(c.Diff, 4000),
			})
		}
		return map[string]any{"type": "file_change", "id": item.ID, "status": item.Status, "changes": changes}
	default:
		return map[string]any{"type": item.Type, "raw": truncate(string(raw), 1000)}
	}
}

func kindString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Type
	}
	return ""
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n[... %d chars truncated]", len(s)-limit)
}

// Shutdown closes all codex processes. Running sessions keep status=running
// on disk so the next startup marks them interrupted.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rt := range h.runtimes {
		if rt.activeTurn != nil && !rt.activeTurn.finished {
			rt.activeTurn.finished = true
			close(rt.activeTurn.stopWatchdog)
		}
		rt.client.Close()
	}
	h.persistLocked()
}
