// Package httpapi exposes CodexLoom over HTTP: REST for operations, SSE for
// real-time Agent and Thread observation, and the embedded React console.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yan5xu/codex-loom/internal/backup"
	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

type Server struct {
	hub              *hub.Hub
	st               *store.Store
	web              fs.FS
	restartMu        sync.Mutex
	restart          restartState
	connectorMu      sync.Mutex
	activeConnectors map[string]struct{}
	build            buildinfo.Info
	readOnly         bool
}

func New(h *hub.Hub, st *store.Store, web fs.FS) *Server {
	return NewWithOptions(h, st, web, Options{})
}

type Options struct {
	StartedAt time.Time
	Mode      string
	ReadOnly  bool
}

func NewWithOptions(h *hub.Hub, st *store.Store, web fs.FS, options Options) *Server {
	dataDir := ""
	if st != nil {
		dataDir = st.Dir()
	}
	return &Server{
		hub: h, st: st, web: web, restart: restartState{State: "idle"},
		activeConnectors: map[string]struct{}{},
		build: buildinfo.Current(web, buildinfo.Runtime{
			StartedAt: options.StartedAt, DataDir: dataDir, Mode: options.Mode, ReadOnly: options.ReadOnly,
		}),
		readOnly: options.ReadOnly,
	}
}

type restartState struct {
	State        string                `json:"state"`
	Message      string                `json:"message,omitempty"`
	Running      []hub.ActiveAgent     `json:"running,omitempty"`
	Operations   []hub.ActiveOperation `json:"operations,omitempty"`
	Backup       *backup.Snapshot      `json:"backup,omitempty"`
	PID          int                   `json:"pid,omitempty"`
	Reloader     string                `json:"reloader,omitempty"`
	LogPath      string                `json:"logPath,omitempty"`
	ChildLogPath string                `json:"childLogPath,omitempty"`
	UpdatedAt    string                `json:"updatedAt"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.registerSystemRoutes(mux)
	s.registerIntegrationRoutes(mux)
	s.registerAgentRoutes(mux)
	s.registerOrganizationRoutes(mux)
	s.registerCompatibilityRoutes(mux)

	mux.HandleFunc("/", s.serveWeb)

	var handler http.Handler = mux
	if s.readOnly {
		handler = readOnly(handler)
	}
	return withCORS(handler)
}

// agentThreadEvents streams an Agent's Thread event log over SSE: subscribe first,
// replay persisted events, then drain live ones (skipping replay overlap) —
// no gap, no duplicates.
func (s *Server) agentThreadEvents(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	canonical := strings.HasPrefix(r.URL.Path, "/api/agents/")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming unsupported"))
		return
	}

	since := int64(0)
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if v, err := strconv.ParseInt(lastID, 10, 64); err == nil {
			since = v
		}
	} else if q := r.URL.Query().Get("since"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			since = v
		}
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))

	// replay=0 → history is served separately via /history (from rollout);
	// skip event-log replay and stream only new activity from now on.
	if r.URL.Query().Get("replay") == "0" {
		since = s.hub.LastSeq(key)
	}

	// Subscribe BEFORE replay so nothing emitted during replay is lost.
	ch, cancel, err := s.hub.Subscribe(key)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer cancel()

	events, err := s.hub.ReadEvents(key, since, tail)
	if err != nil {
		writeErr(w, err)
		return
	}

	sseHeaders(w)
	replayMax := since
	for _, ev := range events {
		writeThreadSSE(w, ev, canonical)
		if ev.Seq > replayMax {
			replayMax = ev.Seq
		}
	}
	agent, _ := s.hub.GetAgent(key)
	livePayload := map[string]any{"agent": agent}
	if !canonical {
		livePayload = map[string]any{"session": agent}
	}
	liveData, _ := json.Marshal(livePayload)
	writeThreadSSE(w, store.Event{Seq: replayMax, TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/live", Data: liveData}, canonical)
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return // dropped as slow observer; client reconnects with Last-Event-ID
			}
			if ev.Seq <= replayMax {
				continue // overlap raced during replay
			}
			writeThreadSSE(w, ev, canonical)
			flusher.Flush()
		}
	}
}

func (s *Server) globalEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming unsupported"))
		return
	}
	ch, cancel := s.hub.SubscribeGlobal()
	defer cancel()

	since := s.hub.LastGlobalSeq()
	reconnecting := false
	if lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastID != "" {
		if value, err := strconv.ParseInt(lastID, 10, 64); err == nil {
			since = value
			reconnecting = true
		}
	} else if cursor := strings.TrimSpace(r.URL.Query().Get("since")); cursor != "" {
		if value, err := strconv.ParseInt(cursor, 10, 64); err == nil {
			since = value
			reconnecting = true
		}
	}
	events, err := s.hub.ReadGlobalEvents(since, 10_000)
	if err != nil {
		writeErr(w, err)
		return
	}
	currentSeq := s.hub.LastGlobalSeq()
	gap := reconnecting && currentSeq > since && (len(events) == 0 || events[0].Seq > since+1)

	sseHeaders(w)
	replayMax := since
	if gap {
		availableFrom := currentSeq + 1
		if len(events) > 0 {
			availableFrom = events[0].Seq
		}
		data, _ := json.Marshal(map[string]any{
			"reason": "global event replay window compacted", "since": since, "availableFrom": availableFrom,
		})
		writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "loom/reconcile", Data: data})
	}
	for _, event := range events {
		writeCompatibleGlobalSSE(w, event)
		if event.Seq > replayMax {
			replayMax = event.Seq
		}
	}
	agents := s.hub.ListAgents()
	loomSnapshot, _ := json.Marshal(map[string]any{"agents": agents})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "loom/agents", Data: loomSnapshot})
	legacySnapshot, _ := json.Marshal(map[string]any{"sessions": agents})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/sessions", Data: legacySnapshot})
	restartSnapshot, _ := json.Marshal(map[string]any{"restart": s.restartSnapshot()})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "loom/restart-status", Data: restartSnapshot})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/restart-status", Data: restartSnapshot})
	remoteSnapshot, _ := json.Marshal(map[string]any{"remote": s.hub.RemoteSnapshot()})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "loom/remote-status", Data: remoteSnapshot})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/remote-status", Data: remoteSnapshot})
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			if ev.Seq <= replayMax {
				continue
			}
			writeCompatibleGlobalSSE(w, ev)
			flusher.Flush()
		}
	}
}

func (s *Server) connectorCommands(w http.ResponseWriter, r *http.Request) {
	if !connectorRequestAllowed(r) {
		writeJSON(w, 403, map[string]any{"error": "connector access denied"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming unsupported"))
		return
	}
	connectionID := r.PathValue("id")
	if !s.acquireConnector(connectionID) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "connection already has an active command stream"})
		return
	}
	defer s.releaseConnector(connectionID)
	if _, err := s.hub.HeartbeatConnection(connectionID, hub.ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.RequeueSendingForConnection(connectionID)
	s.hub.RequeueProviderOperationsForConnection(connectionID)
	ch, cancel := s.hub.SubscribeGlobal()
	defer cancel()
	defer s.hub.MarkConnectionDisconnected(connectionID, "connector command stream closed")

	sseHeaders(w)
	sendPending := func() bool {
		if s.isRestartPending() {
			return true
		}
		for {
			command, err := s.hub.ClaimNextConnectorCommand(connectionID)
			if err != nil {
				return false
			}
			if command == nil {
				return true
			}
			writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "connector/command", Data: toJSONRaw(command)})
			flusher.Flush()
		}
	}
	if !sendPending() {
		return
	}

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
			if !sendPending() {
				return
			}
		case _, open := <-ch:
			if !open || !sendPending() {
				return
			}
		}
	}
}

func (s *Server) acquireConnector(connectionID string) bool {
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	if _, exists := s.activeConnectors[connectionID]; exists {
		return false
	}
	s.activeConnectors[connectionID] = struct{}{}
	return true
}

func (s *Server) releaseConnector(connectionID string) {
	s.connectorMu.Lock()
	delete(s.activeConnectors, connectionID)
	s.connectorMu.Unlock()
}

func connectorRequestAllowed(r *http.Request) bool {
	if want := envCompat("CODEX_LOOM_CONNECTOR_TOKEN", "CODEX_HUB_CONNECTOR_TOKEN"); want != "" {
		got := r.Header.Get("X-Codex-Loom-Connector-Token")
		if got == "" {
			got = r.Header.Get("X-Codex-Hub-Connector-Token")
		}
		return len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func toJSONRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if s.web == nil {
		http.Error(w, "web console not built (run: make web)", 404)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.web, path); err != nil {
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.NotFound(w, r)
			return
		}
		path = "index.html" // SPA fallback
	}
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.web, path)
}

func (s *Server) serveImage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, &hub.HubError{Status: 400, Message: "path is required"})
		return
	}
	if !filepath.IsAbs(path) {
		writeErr(w, &hub.HubError{Status: 400, Message: "path must be absolute"})
		return
	}
	clean := filepath.Clean(path)
	f, err := os.Open(clean)
	if err != nil {
		writeErr(w, &hub.HubError{Status: 404, Message: "image not found"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeErr(w, &hub.HubError{Status: 404, Message: "image not found"})
		return
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		writeErr(w, err)
		return
	}
	contentType := http.DetectContentType(head[:n])
	if !strings.HasPrefix(contentType, "image/") {
		writeErr(w, &hub.HubError{Status: 415, Message: "path is not an image"})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, filepath.Base(clean), info.ModTime(), f)
}

func (s *Server) adminRestart(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin restart is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}

	if state := s.restartSnapshot(); state.State == "waiting" || state.State == "restarting" {
		writeJSON(w, 202, map[string]any{"restart": state})
		return
	}

	s.hub.BeginDrain()
	drain := s.hub.DrainStatus()
	if state, started := s.markRestartWaiting(drain); !started {
		writeJSON(w, 202, map[string]any{"restart": state})
		return
	}
	if drain.Busy() {
		go s.waitForIdleAndRestart()
		writeJSON(w, 202, map[string]any{"restart": s.restartSnapshot()})
		return
	}

	snap, err := s.createBackup("pre-restart")
	if err != nil {
		s.failRestart("backup before restart failed: " + err.Error())
		writeErr(w, &hub.HubError{Status: 500, Message: "backup before restart failed: " + err.Error()})
		return
	}
	info, err := s.startReloader()
	if err != nil {
		s.failRestart(err.Error())
		writeErr(w, err)
		return
	}
	info.Backup = snap
	s.setRestartState(info)
	s.emitRestartState()
	writeJSON(w, 202, map[string]any{"restart": info})
}

func (s *Server) startReloader() (restartState, error) {
	logPath := strings.TrimSpace(envCompat("CODEX_LOOM_RESTART_LOG", "CODEX_HUB_RESTART_LOG"))
	if logPath == "" {
		logPath = "/tmp/codex-loom-reloader.log"
	}
	childLogPath := strings.TrimSpace(envCompat("CODEX_LOOM_LOG", "CODEX_HUB_LOG"))
	if childLogPath == "" {
		childLogPath = "/tmp/codex-loom.log"
	}

	exe, err := os.Executable()
	if err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "resolve executable: " + err.Error()}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "resolve working directory: " + err.Error()}
	}
	reloader := strings.TrimSpace(envCompat("CODEX_LOOM_RELOADER", "CODEX_HUB_RELOADER"))
	if reloader == "" {
		reloader = filepath.Join(filepath.Dir(exe), "codex-loom-reloader")
		if _, err := os.Stat(reloader); err != nil {
			reloader = filepath.Join(filepath.Dir(exe), "codex-hub-reloader")
		}
	}
	if _, err := os.Stat(reloader); err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "reloader not found: " + reloader}
	}

	args := []string{
		"-pid", strconv.Itoa(os.Getpid()),
		"-exe", exe,
		"-cwd", cwd,
		"-log", logPath,
		"-child-log", childLogPath,
		"--",
	}
	args = append(args, os.Args[1:]...)
	cmd := exec.Command(reloader, args...)
	cmd.Env = os.Environ()
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "start reloader: " + err.Error()}
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	return restartState{
		State:        "restarting",
		Message:      "reloader process started",
		PID:          pid,
		Reloader:     reloader,
		LogPath:      logPath,
		ChildLogPath: childLogPath,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *Server) waitForIdleAndRestart() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		drain := s.hub.DrainStatus()
		if !drain.Busy() {
			break
		}
		s.updateRestartWaiting(drain)
		<-ticker.C
	}
	snap, err := s.createBackup("pre-restart")
	if err != nil {
		s.failRestart("backup before restart failed: " + err.Error())
		return
	}
	info, err := s.startReloader()
	if err != nil {
		s.failRestart(err.Error())
		return
	}
	info.Backup = snap
	s.setRestartState(info)
	s.emitRestartState()
}

func (s *Server) failRestart(message string) {
	s.hub.CancelDrain()
	s.setRestartState(restartState{
		State:     "failed",
		Message:   message,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	s.emitRestartState()
}

func (s *Server) markRestartWaiting(drain hub.DrainStatus) (restartState, bool) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restart.State == "waiting" || s.restart.State == "restarting" {
		return s.restart, false
	}
	s.restart = restartState{
		State:      "waiting",
		Message:    "draining active work and preparing restart",
		Running:    drain.Agents,
		Operations: drain.Operations,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.emitRestartStateLocked()
	return s.restart, true
}

func (s *Server) updateRestartWaiting(drain hub.DrainStatus) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restart.State != "waiting" {
		return
	}
	s.restart.Running = drain.Agents
	s.restart.Operations = drain.Operations
	s.restart.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.emitRestartStateLocked()
}

func (s *Server) setRestartState(state restartState) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	s.restart = state
	if s.restart.UpdatedAt == "" {
		s.restart.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
}

func (s *Server) restartSnapshot() restartState {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	return s.restart
}

func (s *Server) isRestartPending() bool {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	return s.restart.State == "waiting" || s.restart.State == "restarting"
}

func (s *Server) emitRestartState() {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	s.emitRestartStateLocked()
}

func (s *Server) emitRestartStateLocked() {
	s.hub.EmitGlobal("loom/restart-status", map[string]any{"restart": s.restart})
}

// ---- helpers ----

func allowAdminRequest(r *http.Request) bool {
	token := envCompat("CODEX_LOOM_ADMIN_TOKEN", "CODEX_HUB_ADMIN_TOKEN")
	if token != "" {
		header := r.Header.Get("X-Codex-Loom-Admin-Token")
		if header == "" {
			header = r.Header.Get("X-Codex-Hub-Admin-Token")
		}
		if header == "" {
			header = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		return header == token
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envCompat(primary, legacy string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	return os.Getenv(legacy)
}

func canonicalEventType(typ string) string {
	switch typ {
	case "hub/sessions":
		return "loom/agents"
	case "hub/session-status":
		return "loom/agent-status"
	case "hub/session-created":
		return "loom/agent-created"
	case "hub/session-killed":
		return "loom/agent-archived"
	}
	if strings.HasPrefix(typ, "hub/") {
		return "loom/" + strings.TrimPrefix(typ, "hub/")
	}
	return typ
}

func writeThreadSSE(w io.Writer, ev store.Event, canonical bool) {
	if canonical {
		ev.Type = canonicalEventType(ev.Type)
	} else {
		ev.Type = legacyEventType(ev.Type)
	}
	writeSSE(w, ev)
}

func writeCompatibleGlobalSSE(w io.Writer, ev store.Event) {
	canonical := ev
	canonical.Type = canonicalEventType(ev.Type)
	writeSSE(w, canonical)
	legacyType := legacyEventType(canonical.Type)
	if legacyType != canonical.Type {
		legacy := canonical
		legacy.Type = legacyType
		writeSSE(w, legacy)
	}
}

func legacyEventType(typ string) string {
	switch typ {
	case "loom/thread-event":
		// This workbench-only multiplexed event has no legacy session alias.
		return typ
	case "loom/agents":
		return "hub/sessions"
	case "loom/agent-status":
		return "hub/session-status"
	case "loom/agent-created":
		return "hub/session-created"
	case "loom/agent-archived":
		return "hub/session-killed"
	}
	if strings.HasPrefix(typ, "loom/") {
		return "hub/" + strings.TrimPrefix(typ, "loom/")
	}
	return typ
}

func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fmt.Fprint(w, ": connected\n\n")
}

func writeSSE(w io.Writer, ev store.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if ev.Seq > 0 {
		fmt.Fprintf(w, "id: %d\n", ev.Seq)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return &hub.HubError{Status: 400, Message: "read body: " + err.Error()}
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return &hub.HubError{Status: 400, Message: "invalid JSON body"}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[http] encode: %v", err)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	status := 500
	var he *hub.HubError
	if errors.As(err, &he) {
		status = he.Status
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Codex-Hub-Admin-Token")
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "this CodexLoom canary is read-only"})
			return
		}
		if readOnlyExternalRead(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "external provider access is disabled in a CodexLoom canary"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOnlyExternalRead(path string) bool {
	return strings.HasPrefix(path, "/api/integrations/providers/") ||
		strings.HasSuffix(path, "/commands") ||
		path == "/api/remote/pairing" ||
		path == "/api/remote/devices"
}
