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
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/rollout"
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
}

func New(h *hub.Hub, st *store.Store, web fs.FS) *Server {
	return &Server{
		hub: h, st: st, web: web, restart: restartState{State: "idle"},
		activeConnectors: map[string]struct{}{},
	}
}

type restartState struct {
	State        string            `json:"state"`
	Message      string            `json:"message,omitempty"`
	Running      []hub.ActiveAgent `json:"running,omitempty"`
	Backup       *backup.Snapshot  `json:"backup,omitempty"`
	PID          int               `json:"pid,omitempty"`
	Reloader     string            `json:"reloader,omitempty"`
	LogPath      string            `json:"logPath,omitempty"`
	ChildLogPath string            `json:"childLogPath,omitempty"`
	UpdatedAt    string            `json:"updatedAt"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		agents := s.hub.ListAgents()
		writeJSON(w, 200, map[string]any{
			"ok": true, "product": "CodexLoom", "dataDir": s.st.Dir(),
			"agents": len(agents), "sessions": len(agents),
		})
	})

	mux.HandleFunc("POST /api/admin/restart", s.adminRestart)
	mux.HandleFunc("GET /api/admin/backups", s.adminListBackups)
	mux.HandleFunc("POST /api/admin/backup", s.adminBackup)

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

	mux.HandleFunc("GET /api/integrations/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"connections": s.hub.ListConnections()})
	})
	mux.HandleFunc("POST /api/integrations/connections", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConnectionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		connection, err := s.hub.CreateConnection(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"connection": connection})
	})
	mux.HandleFunc("PATCH /api/integrations/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConnectionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		connection, err := s.hub.UpdateConnection(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"connection": connection})
	})
	mux.HandleFunc("GET /api/integrations/addresses", func(w http.ResponseWriter, r *http.Request) {
		addresses, err := s.hub.ListAddresses(r.URL.Query().Get("agent"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"addresses": addresses})
	})
	mux.HandleFunc("POST /api/integrations/connections/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if !connectorRequestAllowed(r) {
			writeJSON(w, 403, map[string]any{"error": "connector access denied"})
			return
		}
		var body hub.ConnectionHeartbeatParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		connection, err := s.hub.HeartbeatConnection(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"connection": connection})
	})
	mux.HandleFunc("GET /api/integrations/connections/{id}/commands", s.connectorCommands)
	mux.HandleFunc("POST /api/integrations/connections/{id}/outbox/{outboxId}/result", func(w http.ResponseWriter, r *http.Request) {
		if !connectorRequestAllowed(r) {
			writeJSON(w, 403, map[string]any{"error": "connector access denied"})
			return
		}
		var body hub.OutboxResultParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, err := s.hub.CompleteOutbox(r.PathValue("id"), r.PathValue("outboxId"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"outboxItem": item})
	})
	mux.HandleFunc("GET /api/agents/{agent}/addresses", func(w http.ResponseWriter, r *http.Request) {
		addresses, err := s.hub.ListAddresses(r.PathValue("agent"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"addresses": addresses})
	})
	mux.HandleFunc("POST /api/agents/{agent}/addresses", func(w http.ResponseWriter, r *http.Request) {
		var body hub.AddressParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		body.Agent = r.PathValue("agent")
		address, err := s.hub.CreateAddress(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"address": address})
	})
	mux.HandleFunc("PATCH /api/integrations/addresses/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.AddressParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		address, err := s.hub.UpdateAddress(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"address": address})
	})
	mux.HandleFunc("GET /api/integrations/conversations", func(w http.ResponseWriter, r *http.Request) {
		memberships, err := s.hub.ListConversationMemberships(r.URL.Query().Get("agent"), r.URL.Query().Get("address"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"memberships": memberships})
	})
	mux.HandleFunc("GET /api/integrations/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		membership, err := s.hub.GetConversationMembership(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"membership": membership})
	})
	mux.HandleFunc("PUT /api/integrations/addresses/{addressId}/conversations/{conversationId}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConversationMembershipParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		body.AddressID = r.PathValue("addressId")
		body.ConversationID = r.PathValue("conversationId")
		membership, created, err := s.hub.UpsertConversationMembership(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		writeJSON(w, status, map[string]any{"membership": membership})
	})
	mux.HandleFunc("PATCH /api/integrations/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConversationMembershipParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		membership, err := s.hub.UpdateConversationMembership(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"membership": membership})
	})
	mux.HandleFunc("POST /api/integrations/ingress", func(w http.ResponseWriter, r *http.Request) {
		if !connectorRequestAllowed(r) {
			writeJSON(w, 403, map[string]any{"error": "connector access denied"})
			return
		}
		var body hub.IngressParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.hub.IngestMessage(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		status := 201
		if result.Duplicate {
			status = 200
		} else if result.Ignored {
			status = http.StatusAccepted
		}
		writeJSON(w, status, result)
	})
	mux.HandleFunc("GET /api/inbox", func(w http.ResponseWriter, r *http.Request) {
		agent, state, origin := r.URL.Query().Get("agent"), r.URL.Query().Get("state"), r.URL.Query().Get("origin")
		items, err := s.hub.ListInbox(agent, state, origin)
		if err != nil {
			writeErr(w, err)
			return
		}
		entries, err := s.hub.ListInboxEntries(agent, state, origin)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "entries": entries})
	})
	mux.HandleFunc("GET /api/inbox/{id}", func(w http.ResponseWriter, r *http.Request) {
		entry, err := s.hub.GetInboxEntry(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, entry)
	})
	mux.HandleFunc("POST /api/inbox/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		var body hub.InboxActionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, outbox, err := s.hub.ReplyInboxItem(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"item": item, "outboxItem": outbox})
	})
	mux.HandleFunc("POST /api/inbox/{id}/no-reply", func(w http.ResponseWriter, r *http.Request) {
		var body hub.InboxActionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, err := s.hub.NoReplyInboxItem(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"item": item})
	})
	mux.HandleFunc("POST /api/inbox/{id}/defer", func(w http.ResponseWriter, r *http.Request) {
		var body hub.InboxActionParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, err := s.hub.DeferInboxItem(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"item": item})
	})
	mux.HandleFunc("POST /api/inbox/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		item, err := s.hub.RetryInboxItem(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"item": item})
	})
	mux.HandleFunc("GET /api/outbox", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.hub.ListOutbox(r.URL.Query().Get("agent"), r.URL.Query().Get("state"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /api/outbox", func(w http.ResponseWriter, r *http.Request) {
		var body hub.OutboxParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, err := s.hub.CreateOutbox(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"outboxItem": item})
	})
	mux.HandleFunc("POST /api/outbox/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		item, err := s.hub.RetryOutboxItem(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"outboxItem": item})
	})

	// CodexLoom Agent API. The /api/sessions routes below remain compatibility
	// aliases for older chub binaries and open browser tabs.
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"agents": s.hub.ListAgents()})
	})
	mux.HandleFunc("POST /api/agents", func(w http.ResponseWriter, r *http.Request) {
		var p hub.CreateParams
		if err := readJSON(r, &p); err != nil {
			writeErr(w, err)
			return
		}
		agent, err := s.hub.CreateAgent(p)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"agent": agent})
	})
	mux.HandleFunc("GET /api/agents/{key}", func(w http.ResponseWriter, r *http.Request) {
		agent, err := s.hub.GetAgent(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"agent": agent})
	})
	mux.HandleFunc("PATCH /api/agents/{key}/config", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConfigParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		agent, err := s.hub.UpdateAgentConfig(r.PathValue("key"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"agent": agent})
	})
	mux.HandleFunc("GET /api/agents/{key}/profile", func(w http.ResponseWriter, r *http.Request) {
		profile, err := s.hub.GetProfile(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profile": profile})
	})
	mux.HandleFunc("PUT /api/agents/{key}/profile", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ProfileParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		profile, err := s.hub.UpdateProfile(r.PathValue("key"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profile": profile})
	})
	mux.HandleFunc("DELETE /api/agents/{key}", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.hub.ArchiveAgent(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("POST /api/agents/{key}/turns", func(w http.ResponseWriter, r *http.Request) {
		if s.isRestartPending() {
			writeErr(w, &hub.HubError{Status: 409, Message: "restart pending; wait for CodexLoom to restart before starting a Turn"})
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
	mux.HandleFunc("POST /api/agents/{key}/turns/current/interrupt", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.hub.Interrupt(r.PathValue("key"), "")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/agents/{key}/thread/history", func(w http.ResponseWriter, r *http.Request) {
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		hist, err := s.hub.History(r.PathValue("key"), count, offset)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, hist)
	})
	mux.HandleFunc("POST /api/agents/{key}/thread/approvals/{approvalId}", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("GET /api/agents/{key}/thread/events", s.agentThreadEvents)

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

	mux.HandleFunc("GET /api/sessions/{key}/profile", func(w http.ResponseWriter, r *http.Request) {
		profile, err := s.hub.GetProfile(r.PathValue("key"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profile": profile})
	})

	mux.HandleFunc("PUT /api/sessions/{key}/profile", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ProfileParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		profile, err := s.hub.UpdateProfile(r.PathValue("key"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profile": profile})
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

	mux.HandleFunc("GET /api/comms", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"messages": s.hub.ListComms(r.URL.Query().Get("agent"), r.URL.Query().Get("status")),
		})
	})

	mux.HandleFunc("GET /api/team", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"team": s.hub.Team()})
	})

	mux.HandleFunc("GET /api/team/relationships", func(w http.ResponseWriter, r *http.Request) {
		relationships, err := s.hub.ListRelationships(r.URL.Query().Get("agent"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"relationships": relationships})
	})

	mux.HandleFunc("POST /api/team/relationships", func(w http.ResponseWriter, r *http.Request) {
		var body hub.RelationshipParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		relationship, err := s.hub.CreateRelationship(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"relationship": relationship})
	})

	mux.HandleFunc("PATCH /api/team/relationships/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body hub.RelationshipParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		relationship, err := s.hub.UpdateRelationship(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"relationship": relationship})
	})

	mux.HandleFunc("DELETE /api/team/relationships/{id}", func(w http.ResponseWriter, r *http.Request) {
		relationship, err := s.hub.DeleteRelationship(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"relationship": relationship})
	})

	mux.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"schedules": s.hub.ListSchedules()})
	})

	mux.HandleFunc("POST /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ScheduleParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		schedule, err := s.hub.CreateSchedule(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("GET /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := s.hub.GetSchedule(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		if s.isRestartPending() {
			writeErr(w, &hub.HubError{Status: 409, Message: "restart pending; wait for hub to restart before running schedules"})
			return
		}
		schedule, err := s.hub.RunSchedule(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("POST /api/schedules/{id}/enable", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := s.hub.SetScheduleEnabled(r.PathValue("id"), true)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("POST /api/schedules/{id}/disable", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := s.hub.SetScheduleEnabled(r.PathValue("id"), false)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := s.hub.DeleteSchedule(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schedule": schedule})
	})

	mux.HandleFunc("POST /api/comms/messages", func(w http.ResponseWriter, r *http.Request) {
		if s.isRestartPending() {
			writeErr(w, &hub.HubError{Status: 409, Message: "restart pending; wait for hub to restart before sending new agent messages"})
			return
		}
		var body hub.CommParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.hub.SendAgentMessage(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, result)
	})

	mux.HandleFunc("GET /api/comms/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		msg, err := s.hub.GetAgentMessage(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"message": msg})
	})

	mux.HandleFunc("POST /api/comms/messages/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		msg, err := s.hub.CancelAgentMessage(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"message": msg})
	})

	mux.HandleFunc("POST /api/comms/messages/{id}/no-reply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From string `json:"from"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		msg, err := s.hub.NoReplyAgentMessage(r.PathValue("id"), body.From)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"message": msg})
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

	mux.HandleFunc("GET /api/sessions/{key}/events", s.agentThreadEvents)
	mux.HandleFunc("GET /api/events", s.globalEvents)
	mux.HandleFunc("GET /api/admin/restart/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"restart": s.restartSnapshot()})
	})
	mux.HandleFunc("GET /api/images", s.serveImage)

	mux.HandleFunc("/", s.serveWeb)

	return withCORS(mux)
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

	sseHeaders(w)
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
	ch, cancel := s.hub.SubscribeGlobal()
	defer cancel()
	defer s.hub.MarkConnectionDisconnected(connectionID, "connector command stream closed")

	sseHeaders(w)
	sendPending := func() bool {
		for {
			command, err := s.hub.ClaimNextOutbox(connectionID)
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
		writeErr(w, &hub.HubError{Status: 403, Message: "admin restart is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}

	if state := s.restartSnapshot(); state.State == "waiting" || state.State == "restarting" {
		writeJSON(w, 202, map[string]any{"restart": state})
		return
	}

	running := s.hub.ActiveAgents()
	if len(running) > 0 {
		if state, started := s.markRestartWaiting(running); !started {
			writeJSON(w, 202, map[string]any{"restart": state})
			return
		}
		go s.waitForIdleAndRestart()
		writeJSON(w, 202, map[string]any{"restart": s.restartSnapshot()})
		return
	}

	snap, err := s.createBackup("pre-restart")
	if err != nil {
		writeErr(w, &hub.HubError{Status: 500, Message: "backup before restart failed: " + err.Error()})
		return
	}
	info, err := s.startReloader()
	if err != nil {
		writeErr(w, err)
		return
	}
	info.Backup = snap
	s.setRestartState(info)
	s.emitRestartState()
	writeJSON(w, 202, map[string]any{"restart": info})
}

func (s *Server) adminListBackups(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin backup is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}
	backups, err := backup.List(s.st.Dir())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": backups, "dir": backup.DefaultDir(s.st.Dir())})
}

func (s *Server) adminBackup(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin backup is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Reason == "" {
		body.Reason = "manual"
	}
	snap, err := s.createBackup(body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"backup": snap, "dir": backup.DefaultDir(s.st.Dir())})
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
		running := s.hub.ActiveAgents()
		if len(running) == 0 {
			break
		}
		s.updateRestartWaiting(running)
		<-ticker.C
	}
	snap, err := s.createBackup("pre-restart")
	if err != nil {
		state := restartState{
			State:     "failed",
			Message:   "backup before restart failed: " + err.Error(),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		s.setRestartState(state)
		s.emitRestartState()
		return
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
	info.Backup = snap
	s.setRestartState(info)
	s.emitRestartState()
}

func (s *Server) createBackup(reason string) (*backup.Snapshot, error) {
	agents := s.hub.ListAgents()
	refs := make([]backup.AgentRef, 0, len(agents))
	for _, v := range agents {
		refs = append(refs, backup.AgentRef{
			ID:       v.ID,
			Name:     v.Name,
			ThreadID: v.ThreadID,
			Cwd:      v.Cwd,
			Source:   v.Source,
		})
	}
	return backup.Create(backup.Options{
		Reason:           reason,
		DataDir:          s.st.Dir(),
		CodexSessionsDir: rollout.DefaultSessionsDir(),
		EdgeNamesFile:    store.EdgeNamesFile(),
		Agents:           refs,
		MaxBackups:       backupRetention(),
	})
}

func backupRetention() int {
	if raw := strings.TrimSpace(envCompat("CODEX_LOOM_BACKUP_KEEP", "CODEX_HUB_BACKUP_KEEP")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 25
}

func (s *Server) markRestartWaiting(running []hub.ActiveAgent) (restartState, bool) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restart.State == "waiting" || s.restart.State == "restarting" {
		return s.restart, false
	}
	s.restart = restartState{
		State:     "waiting",
		Message:   "waiting for running Agents to finish their Turns",
		Running:   running,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.emitRestartStateLocked()
	return s.restart, true
}

func (s *Server) updateRestartWaiting(running []hub.ActiveAgent) {
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
