package httpapi

import (
	"mime"
	"net/http"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) registerTopicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/topics", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("view") == "summary" {
			writeJSON(w, 200, map[string]any{"topics": s.hub.ListTopicSummaries(r.URL.Query().Get("status"), r.URL.Query().Get("agent"))})
			return
		}
		writeJSON(w, 200, map[string]any{"topics": s.hub.ListTopics(r.URL.Query().Get("status"), r.URL.Query().Get("agent"))})
	})
	mux.HandleFunc("POST /api/topics", func(w http.ResponseWriter, r *http.Request) {
		var body hub.CreateTopicParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		topic, err := s.hub.CreateTopic(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"topic": topic})
	})
	mux.HandleFunc("GET /api/topics/{id}", func(w http.ResponseWriter, r *http.Request) {
		topic, err := s.hub.GetTopic(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("GET /api/topics/{id}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		artifacts, err := s.hub.TopicArtifacts(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		for index := range artifacts {
			artifacts[index].Path = ""
			artifacts[index].URL = "/api/topics/" + r.PathValue("id") + "/artifacts/" + artifacts[index].ID
		}
		writeJSON(w, 200, map[string]any{"artifacts": artifacts})
	})
	mux.HandleFunc("GET /api/topics/{id}/artifacts/{artifactId}", func(w http.ResponseWriter, r *http.Request) {
		artifact, file, err := s.hub.OpenTopicArtifact(r.PathValue("id"), r.PathValue("artifactId"))
		if err != nil {
			writeErr(w, err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			writeErr(w, err)
			return
		}
		disposition := threadArtifactDisposition(
			artifact.MimeType,
			r.URL.Query().Get("preview") == "1",
			r.URL.Query().Get("download") == "1",
		)
		w.Header().Set("Content-Type", artifact.MimeType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": artifact.Name}))
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("Cross-Origin-Resource-Policy", threadArtifactResourcePolicy(artifact.MimeType))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, artifact.Name, info.ModTime(), file)
	})
	mux.HandleFunc("PATCH /api/topics/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.UpdateTopicParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		topic, err := s.hub.UpdateTopic(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("POST /api/topics/{id}/participants", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Actor string `json:"actor"`
			hub.TopicParticipantParams
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		topic, err := s.hub.AddTopicParticipant(r.PathValue("id"), body.Actor, body.TopicParticipantParams)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("DELETE /api/topics/{id}/participants/{agent}", func(w http.ResponseWriter, r *http.Request) {
		topic, err := s.hub.RemoveTopicParticipant(r.PathValue("id"), r.PathValue("agent"), r.URL.Query().Get("actor"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("POST /api/topics/{id}/links", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Actor         string `json:"actor"`
			Type          string `json:"type"`
			ID            string `json:"id"`
			Relation      string `json:"relation"`
			Label         string `json:"label"`
			InheritedFrom string `json:"inheritedFrom"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		topic, err := s.hub.LinkTopic(r.PathValue("id"), body.Actor, hub.TopicLink{Type: body.Type, ID: body.ID, Relation: body.Relation, Label: body.Label, InheritedFrom: body.InheritedFrom})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("POST /api/topics/{id}/send", func(w http.ResponseWriter, r *http.Request) {
		if s.isRestartPending() {
			writeErr(w, &hub.HubError{Status: 409, Message: "restart pending; wait for CodexLoom to restart before sending Topic work"})
			return
		}
		var body hub.TopicInputParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.hub.SendTopicInput(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /api/topics/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		topic, err := s.hub.MarkTopicRead(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"topic": topic})
	})
	mux.HandleFunc("POST /api/topics/{id}/interventions", func(w http.ResponseWriter, r *http.Request) {
		var body hub.TopicInterventionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		if strings.TrimSpace(body.Agent) == "" {
			writeErr(w, &hub.HubError{Status: 400, Message: "agent is required"})
			return
		}
		result, err := s.hub.InterveneTopicTurn(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"intervention": result})
	})
}
