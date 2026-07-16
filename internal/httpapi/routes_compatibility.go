package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) registerCompatibilityRoutes(mux *http.ServeMux) {
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

	mux.HandleFunc("GET /api/sessions/{key}/events", s.agentThreadEvents)
	mux.HandleFunc("GET /api/events", s.globalEvents)
	mux.HandleFunc("GET /api/admin/restart/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"restart": s.restartSnapshot()})
	})
	mux.HandleFunc("GET /api/images", s.serveImage)

}
