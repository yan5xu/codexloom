package httpapi

import (
	"net/http"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func inboxItemMatches(item hub.InboxItem, addressIDs map[string]struct{}, activeOnly bool) bool {
	if len(addressIDs) > 0 {
		if _, ok := addressIDs[item.AddressID]; !ok {
			return false
		}
	}
	return !activeOnly || (item.State != "handled" && item.State != "cancelled")
}

func (s *Server) registerIntegrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"connections": s.hub.ListConnections()})
	})
	mux.HandleFunc("GET /api/integrations/providers/lark/discovery", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"discovery": s.discoverLark(r.Context(), r.URL.Query().Get("appId"))})
	})
	mux.HandleFunc("POST /api/integrations/providers/lark/credentials", func(w http.ResponseWriter, r *http.Request) {
		var body larkCredentialParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		discovery, err := s.saveLarkCredentials(r.Context(), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"discovery": discovery})
	})
	mux.HandleFunc("POST /api/integrations/providers/lark/setup", func(w http.ResponseWriter, r *http.Request) {
		var body larkSetupParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.setupLark(r.Context(), body, nativeHubURL(r.Host))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/integrations/providers/slack/discovery", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"discovery": s.discoverSlack(r.Context(), r.URL.Query().Get("connectionId"), r.URL.Query().Get("appId"))})
	})
	mux.HandleFunc("POST /api/integrations/providers/slack/credentials", func(w http.ResponseWriter, r *http.Request) {
		var body slackCredentialParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		discovery, err := s.saveSlackCredentials(r.Context(), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"discovery": discovery})
	})
	mux.HandleFunc("POST /api/integrations/providers/slack/setup", func(w http.ResponseWriter, r *http.Request) {
		var body slackSetupParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.setupSlack(r.Context(), body, nativeHubURL(r.Host))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/integrations/providers/parall/discovery", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"discovery": s.discoverParall(r.Context(), r.URL.Query().Get("connectionId"), r.URL.Query().Get("orgId"), r.URL.Query().Get("agentId"))})
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/credentials", func(w http.ResponseWriter, r *http.Request) {
		var body parallCredentialParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		discovery, err := s.saveParallCredentials(r.Context(), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"discovery": discovery})
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/agent-credentials", func(w http.ResponseWriter, r *http.Request) {
		var body parallAgentCredentialParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		discovery, err := s.saveParallAgentCredential(r.Context(), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"discovery": discovery})
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/import", func(w http.ResponseWriter, r *http.Request) {
		var body parallImportParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.importParallAgent(r.Context(), body, nativeHubURL(r.Host))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/setup", func(w http.ResponseWriter, r *http.Request) {
		var body parallSetupParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.setupParall(r.Context(), body, nativeHubURL(r.Host))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/gateway", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConnectionID string `json:"connectionId"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		result, err := s.repairParallGateway(r.Context(), body.ConnectionID, nativeHubURL(r.Host))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("POST /api/integrations/providers/parall/operations", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ProviderOperationParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		body.Provider = "parall"
		operation, err := s.hub.CreateProviderOperation(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"operation": operation})
	})
	mux.HandleFunc("POST /api/integrations/providers/lark/operations", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ProviderOperationParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		body.Provider = "lark"
		operation, err := s.hub.CreateProviderOperation(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"operation": operation})
	})
	mux.HandleFunc("GET /api/integrations/provider-operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		operation, err := s.hub.GetProviderOperation(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": operation})
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
	mux.HandleFunc("POST /api/integrations/connections/{id}/provider-operations/{operationId}/result", func(w http.ResponseWriter, r *http.Request) {
		if !connectorRequestAllowed(r) {
			writeJSON(w, 403, map[string]any{"error": "connector access denied"})
			return
		}
		var body hub.ProviderOperationResultParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		operation, err := s.hub.CompleteProviderOperation(r.PathValue("id"), r.PathValue("operationId"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": operation})
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
	mux.HandleFunc("GET /api/integrations/conversation-candidates", func(w http.ResponseWriter, r *http.Request) {
		availableOnly := r.URL.Query().Get("available") != "all"
		candidates, err := s.hub.ListConversationCandidates(r.URL.Query().Get("agent"), r.URL.Query().Get("address"), availableOnly)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"candidates": candidates})
	})
	mux.HandleFunc("PUT /api/integrations/addresses/{addressId}/conversation-candidates", func(w http.ResponseWriter, r *http.Request) {
		if !connectorRequestAllowed(r) {
			writeJSON(w, 403, map[string]any{"error": "connector access denied"})
			return
		}
		var body hub.ConversationCandidateSnapshotParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		candidates, err := s.hub.ReplaceConversationCandidates(r.PathValue("addressId"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"candidates": candidates})
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
	mux.HandleFunc("GET /api/human-requests", func(w http.ResponseWriter, r *http.Request) {
		requests, err := s.hub.ListHumanRequests(r.URL.Query().Get("agent"), r.URL.Query().Get("state"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"requests": requests})
	})
	mux.HandleFunc("POST /api/human-requests", func(w http.ResponseWriter, r *http.Request) {
		var body hub.CreateHumanRequestParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		request, err := s.hub.CreateHumanRequest(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"request": request})
	})
	mux.HandleFunc("GET /api/human-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		request, err := s.hub.GetHumanRequest(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"request": request})
	})
	mux.HandleFunc("POST /api/human-requests/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var body hub.AnswerHumanRequestParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		request, err := s.hub.AnswerHumanRequest(r.PathValue("id"), body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"request": request})
	})
	mux.HandleFunc("POST /api/human-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		request, err := s.hub.CancelHumanRequest(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"request": request})
	})
	mux.HandleFunc("POST /api/human-requests/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		request, err := s.hub.RetryHumanRequest(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"request": request})
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
		addressIDs := stringSet(r.URL.Query()["address"])
		activeOnly := r.URL.Query().Get("active") == "true"
		if len(addressIDs) > 0 || activeOnly {
			filteredItems := items[:0]
			for _, item := range items {
				if inboxItemMatches(item, addressIDs, activeOnly) {
					filteredItems = append(filteredItems, item)
				}
			}
			items = filteredItems
			filteredEntries := entries[:0]
			for _, entry := range entries {
				if inboxItemMatches(entry.Item, addressIDs, activeOnly) {
					filteredEntries = append(filteredEntries, entry)
				}
			}
			entries = filteredEntries
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
	mux.HandleFunc("GET /api/outbox/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := s.hub.GetOutbox(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"outboxItem": item})
	})
	mux.HandleFunc("POST /api/integrations/send", func(w http.ResponseWriter, r *http.Request) {
		var body hub.ExternalSendParams
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		item, err := s.hub.SendExternal(body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"outboxItem": item})
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
}
