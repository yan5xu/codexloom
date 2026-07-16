package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) registerSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"build": s.build})
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		agents := s.hub.ListAgents()
		writeJSON(w, 200, map[string]any{
			"ok": true, "product": "CodexLoom", "dataDir": s.st.Dir(),
			"agents": len(agents), "sessions": len(agents), "build": s.build,
		})
	})

	mux.HandleFunc("POST /api/admin/restart", s.adminRestart)
	mux.HandleFunc("GET /api/admin/backups", s.adminListBackups)
	mux.HandleFunc("POST /api/admin/backup", s.adminBackup)
	mux.HandleFunc("POST /api/admin/backups/prune", s.adminPruneBackups)
	mux.HandleFunc("POST /api/skills/reload", func(w http.ResponseWriter, r *http.Request) {
		inventory, err := s.hub.ReloadSkills()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"inventory": inventory})
	})
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		start, endExclusive, explicit, err := calendarWindowFromRequest(r, time.Now())
		if err != nil {
			writeErr(w, &hub.HubError{Status: 400, Message: err.Error()})
			return
		}
		if explicit {
			writeJSON(w, 200, map[string]any{"usage": s.hub.TokenUsageOverviewRange(start, endExclusive)})
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		writeJSON(w, 200, map[string]any{"usage": s.hub.TokenUsageOverview(days)})
	})
	mux.HandleFunc("GET /api/workload", func(w http.ResponseWriter, r *http.Request) {
		start, endExclusive, explicit, err := calendarWindowFromRequest(r, time.Now())
		if err != nil {
			writeErr(w, &hub.HubError{Status: 400, Message: err.Error()})
			return
		}
		if explicit {
			writeJSON(w, 200, map[string]any{"workload": s.hub.WorkloadOverviewRange(start, endExclusive)})
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		writeJSON(w, 200, map[string]any{"workload": s.hub.WorkloadOverview(days)})
	})

	mux.HandleFunc("GET /api/remote", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"remote": s.hub.RemoteSnapshot()})
	})
	mux.HandleFunc("POST /api/remote/enable", func(w http.ResponseWriter, r *http.Request) {
		remote, err := s.hub.EnableRemote()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"remote": remote})
	})
	mux.HandleFunc("POST /api/remote/disable", func(w http.ResponseWriter, r *http.Request) {
		remote, err := s.hub.DisableRemote()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"remote": remote})
	})
	mux.HandleFunc("POST /api/remote/pairing", func(w http.ResponseWriter, r *http.Request) {
		pairing, err := s.hub.StartRemotePairing()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"pairing": pairing})
	})
	mux.HandleFunc("GET /api/remote/pairing", func(w http.ResponseWriter, r *http.Request) {
		pairing, err := s.hub.ReadRemotePairing()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"pairing": pairing})
	})
	mux.HandleFunc("GET /api/remote/devices", func(w http.ResponseWriter, r *http.Request) {
		devices, err := s.hub.ListRemoteDevices()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"devices": devices})
	})
	mux.HandleFunc("DELETE /api/remote/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.hub.RevokeRemoteDevice(r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"revoked": true})
	})

}
