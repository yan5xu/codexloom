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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yan5xu/codex-hub/internal/hub"
	"github.com/yan5xu/codex-hub/internal/store"
)

type Server struct {
	hub *hub.Hub
	st  *store.Store
	web fs.FS
}

func New(h *hub.Hub, st *store.Store, web fs.FS) *Server {
	return &Server{hub: h, st: st, web: web}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"ok": true, "dataDir": s.st.Dir(), "sessions": len(s.hub.ListSessions()),
		})
	})

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

	mux.HandleFunc("DELETE /api/sessions/{key}", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.hub.KillSession(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})

	mux.HandleFunc("POST /api/sessions/{key}/messages", func(w http.ResponseWriter, r *http.Request) {
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
		hist, err := s.hub.History(r.PathValue("key"), count)
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

// ---- helpers ----

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
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
