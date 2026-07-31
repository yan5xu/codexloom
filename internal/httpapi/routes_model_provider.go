package httpapi

import (
	"net/http"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) registerModelProviderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-providers", func(w http.ResponseWriter, _ *http.Request) {
		providers, err := s.hub.ListModelProviders()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
	})
	mux.HandleFunc("GET /api/model-providers/{id}", func(w http.ResponseWriter, r *http.Request) {
		provider, err := s.hub.GetModelProvider(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": provider})
	})
	mux.HandleFunc("PUT /api/model-providers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !allowModelProviderAdminRequest(w, r) {
			return
		}
		var body hub.ModelProviderUpsertParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		provider, err := s.hub.UpsertModelProvider(r.PathValue("id"), body)
		body.APIKey = ""
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": provider})
	})
	mux.HandleFunc("DELETE /api/model-providers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !allowModelProviderAdminRequest(w, r) {
			return
		}
		if err := s.hub.DeleteModelProvider(r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"disabled": true})
	})
	mux.HandleFunc("POST /api/model-providers/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		if !allowModelProviderAdminRequest(w, r) {
			return
		}
		var body hub.ModelProviderVerifyParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		verification, err := s.hub.VerifyModelProvider(r.PathValue("id"), body.Model)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"verification": verification})
	})
}

func allowModelProviderAdminRequest(w http.ResponseWriter, r *http.Request) bool {
	if allowAdminRequest(r) {
		return true
	}
	writeErr(w, &hub.HubError{
		Status:  http.StatusForbidden,
		Message: "Model Provider changes and verification are only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured",
	})
	return false
}
