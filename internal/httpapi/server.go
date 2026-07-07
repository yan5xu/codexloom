// Package httpapi exposes the hub over HTTP: REST for operations, SSE for
// real-time observation (per-session event stream with seq replay, plus a
// hub-level status stream), and the embedded React console.
package httpapi

import (
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

	"github.com/yan5xu/codex-hub/internal/hub"
	"github.com/yan5xu/codex-hub/internal/store"
)

type Server struct {
	hub       *hub.Hub
	st        *store.Store
	web       fs.FS
	restartMu sync.Mutex
	restart   restartState
}

func New(h *hub.Hub, st *store.Store, web fs.FS) *Server {
	return &Server{hub: h, st: st, web: web, restart: restartState{State: "idle"}}
}

type restartState struct {
	State        string               `json:"state"`
	Message      string               `json:"message,omitempty"`
	Running      []hub.RunningSession `json:"running,omitempty"`
	PID          int                  `json:"pid,omitempty"`
	Reloader     string               `json:"reloader,omitempty"`
	LogPath      string               `json:"logPath,omitempty"`
	ChildLogPath string               `json:"childLogPath,omitempty"`
	UpdatedAt    string               `json:"updatedAt"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"ok": true, "dataDir": s.st.Dir(), "sessions": len(s.hub.ListSessions()),
		})
	})

	mux.HandleFunc("POST /api/admin/restart", s.adminRestart)

	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"sessions": s.hub.ListSessions()})
	})

	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var p hub.CreateParams
		if err := readJSON(r, &p); err != nil {
			writeErr(w, err)
			return
		}
		session, err := s.hub.CreateSession(p)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"session": session})
	})

	mux.HandleFunc("GET /api/sessions/{key}", func(w http.ResponseWriter, r *http.Request) {
		session, err := s.hub.GetSession(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"session": session})
	})

	mux.HandleFunc("PATCH /api/sessions/{key}/config", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConfigParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		session, err := s.hub.UpdateConfig(r.PathValue("key"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"session": session})
	})

	mux.HandleFunc("DELETE /api/sessions/{key}", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.hub.KillSession(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})

	mux.HandleFunc("POST /api/sessions/{key}/messages", func(w http.ResponseWriter, r *http.Request) {
		if s.isRestartPending() {
			writeErr(w, &hub.HubError{Status: 409, Message: "restart pending; wait for hub to restart before sending new tasks"})
			return
		}
		var body struct {
			Text       string `json:"text"`
			TimeoutSec int    `json:"timeoutSec"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.hub.SendTask(r.PathValue("key"), body.Text, time.Duration(body.TimeoutSec)*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, result)
	})

	mux.HandleFunc("POST /api/sessions/{key}/interrupt", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.hub.Interrupt(r.PathValue("key"), "")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})

	mux.HandleFunc("GET /api/sessions/{key}/history", func(w http.ResponseWriter, r *http.Request) {
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		hist, err := s.hub.History(r.PathValue("key"), count, offset)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, hist)
	})

	mux.HandleFunc("POST /api/sessions/{key}/approvals/{approvalId}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Decision string `json:"decision"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.hub.ResolveApproval(r.PathValue("key"), r.PathValue("approvalId"), body.Decision)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})

	mux.HandleFunc("GET /api/sessions/{key}/events", s.sessionEvents)
	mux.HandleFunc("GET /api/events", s.globalEvents)
	mux.HandleFunc("GET /api/admin/restart/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"restart": s.restartSnapshot()})
	})
	mux.HandleFunc("GET /api/images", s.serveImage)

	mux.HandleFunc("/", s.serveWeb)

	return withCORS(mux)
}

// sessionEvents streams a session's event log over SSE: subscribe first,
// replay persisted events, then drain live ones (skipping replay overlap) —
// no gap, no duplicates.
func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
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
		writeSSE(w, ev)
		if ev.Seq > replayMax {
			replayMax = ev.Seq
		}
	}
	session, _ := s.hub.GetSession(key)
	liveData, _ := json.Marshal(map[string]any{"session": session})
	writeSSE(w, store.Event{Seq: replayMax, TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/live", Data: liveData})
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
			writeSSE(w, ev)
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

	sseHeaders(w)
	snapshot, _ := json.Marshal(map[string]any{"sessions": s.hub.ListSessions()})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/sessions", Data: snapshot})
	restartSnapshot, _ := json.Marshal(map[string]any{"restart": s.restartSnapshot()})
	writeSSE(w, store.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: "hub/restart-status", Data: restartSnapshot})
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
			writeSSE(w, ev)
			flusher.Flush()
		}
	}
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
		path = "index.html" // SPA fallback
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
		writeErr(w, &hub.HubError{Status: 403, Message: "admin restart is only allowed from localhost unless CODEX_HUB_ADMIN_TOKEN is configured"})
		return
	}

	if state := s.restartSnapshot(); state.State == "waiting" || state.State == "restarting" {
		writeJSON(w, 202, map[string]any{"restart": state})
		return
	}

	running := s.hub.RunningSessions()
	if len(running) > 0 {
		if state, started := s.markRestartWaiting(running); !started {
			writeJSON(w, 202, map[string]any{"restart": state})
			return
		}
		go s.waitForIdleAndRestart()
		writeJSON(w, 202, map[string]any{"restart": s.restartSnapshot()})
		return
	}

	info, err := s.startReloader()
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setRestartState(info)
	s.emitRestartState()
	writeJSON(w, 202, map[string]any{"restart": info})
}

func (s *Server) startReloader() (restartState, error) {
	logPath := strings.TrimSpace(os.Getenv("CODEX_HUB_RESTART_LOG"))
	if logPath == "" {
		logPath = "/tmp/codex-hub-reloader.log"
	}
	childLogPath := strings.TrimSpace(os.Getenv("CODEX_HUB_LOG"))
	if childLogPath == "" {
		childLogPath = "/tmp/codex-hub.log"
	}

	exe, err := os.Executable()
	if err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "resolve executable: " + err.Error()}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return restartState{}, &hub.HubError{Status: 500, Message: "resolve working directory: " + err.Error()}
	}
	reloader := strings.TrimSpace(os.Getenv("CODEX_HUB_RELOADER"))
	if reloader == "" {
		reloader = filepath.Join(filepath.Dir(exe), "codex-hub-reloader")
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
		running := s.hub.RunningSessions()
		if len(running) == 0 {
			break
		}
		s.updateRestartWaiting(running)
		<-ticker.C
	}
	info, err := s.startReloader()
	if err != nil {
		state := restartState{
			State:     "failed",
			Message:   err.Error(),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		s.setRestartState(state)
		s.emitRestartState()
		return
	}
	s.setRestartState(info)
	s.emitRestartState()
}

func (s *Server) markRestartWaiting(running []hub.RunningSession) (restartState, bool) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restart.State == "waiting" || s.restart.State == "restarting" {
		return s.restart, false
	}
	s.restart = restartState{
		State:     "waiting",
		Message:   "waiting for running sessions to finish",
		Running:   running,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.emitRestartStateLocked()
	return s.restart, true
}

func (s *Server) updateRestartWaiting(running []hub.RunningSession) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restart.State != "waiting" {
		return
	}
	s.restart.Running = running
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
	s.hub.EmitGlobal("hub/restart-status", map[string]any{"restart": s.restart})
}

// ---- helpers ----

func allowAdminRequest(r *http.Request) bool {
	token := os.Getenv("CODEX_HUB_ADMIN_TOKEN")
	if token != "" {
		header := r.Header.Get("X-Codex-Hub-Admin-Token")
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
