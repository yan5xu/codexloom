package hub

import (
	"encoding/json"
	"log"
	"strings"
	"time"
)

const maxProviderOperationResultBytes = 768 << 10

// ProviderOperation is a durable, credential-mediated call executed by a
// managed Connector. It records provider-native arguments and results without
// exposing the Connector credential to the caller.
type ProviderOperation struct {
	ID             string          `json:"id"`
	Provider       string          `json:"provider"`
	AgentID        string          `json:"agentId"`
	AddressID      string          `json:"addressId"`
	ConnectionID   string          `json:"connectionId"`
	Resource       string          `json:"resource"`
	Action         string          `json:"action"`
	Arguments      map[string]any  `json:"arguments,omitempty"`
	State          string          `json:"state"`
	Result         json.RawMessage `json:"result,omitempty"`
	AttemptCount   int             `json:"attemptCount"`
	AttemptToken   string          `json:"attemptToken,omitempty"`
	ClaimExpiresAt string          `json:"claimExpiresAt,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
	CompletedAt    string          `json:"completedAt,omitempty"`
}

type ProviderOperationParams struct {
	Provider  string         `json:"provider"`
	AddressID string         `json:"addressId"`
	Resource  string         `json:"resource"`
	Action    string         `json:"action"`
	Arguments map[string]any `json:"arguments"`
}

type ProviderOperationResultParams struct {
	AttemptToken string          `json:"attemptToken"`
	Success      bool            `json:"success"`
	Result       json.RawMessage `json:"result"`
	Error        string          `json:"error"`
}

func (h *Hub) loadProviderOperations() error {
	stale := map[string]bool{}
	if err := h.st.ReadProviderOperations(func(raw json.RawMessage) {
		var operation ProviderOperation
		if json.Unmarshal(raw, &operation) != nil || operation.ID == "" {
			return
		}
		if _, exists := h.providerOperations[operation.ID]; !exists {
			h.providerOperationOrder = append(h.providerOperationOrder, operation.ID)
		}
		if previous := h.providerOperations[operation.ID]; previous != nil && providerOperationTerminal(previous.State) && !providerOperationTerminal(operation.State) {
			stale[operation.ID] = true
			return
		}
		h.providerOperations[operation.ID] = &operation
		stale[operation.ID] = false
	}); err != nil {
		return err
	}
	for id, current := range h.providerOperations {
		operation := *current
		repaired := stale[id]
		if operation.State == "running" && (operation.AttemptToken == "" || operation.ClaimExpiresAt == "") {
			operation.State = "pending"
			operation.AttemptToken = ""
			operation.ClaimExpiresAt = ""
			operation.LastError = "recovered legacy provider operation claim after CodexLoom restart"
			operation.UpdatedAt = now()
			repaired = true
		}
		if repaired {
			if err := h.st.AppendProviderOperation(operation); err != nil {
				return err
			}
			copy := operation
			h.providerOperations[id] = &copy
		}
	}
	return nil
}

func providerOperationTerminal(state string) bool {
	return state == "succeeded" || state == "failed"
}

func (h *Hub) CreateProviderOperation(p ProviderOperationParams) (ProviderOperation, error) {
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	addressID := strings.TrimSpace(p.AddressID)
	resource := strings.ToLower(strings.TrimSpace(p.Resource))
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if provider == "" || addressID == "" || resource == "" || action == "" {
		return ProviderOperation{}, errf(400, "provider, addressId, resource and action are required")
	}
	if !validProviderReadOperation(provider, resource, action) {
		return ProviderOperation{}, errf(400, "unsupported %s provider operation: %s %s", provider, resource, action)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	address := h.addresses[addressID]
	if address == nil {
		return ProviderOperation{}, errf(404, "address not found: %s", addressID)
	}
	if !address.Enabled || address.ArchivedAt != "" {
		return ProviderOperation{}, errf(409, "address is not enabled: %s", addressID)
	}
	connection := h.connections[address.ConnectionID]
	if connection == nil {
		return ProviderOperation{}, errf(404, "connection not found: %s", address.ConnectionID)
	}
	if !connection.Enabled || connection.ArchivedAt != "" {
		return ProviderOperation{}, errf(409, "connection is not enabled: %s", connection.ID)
	}
	if connection.Provider != provider {
		return ProviderOperation{}, errf(400, "address %s belongs to %s, not %s", addressID, connection.Provider, provider)
	}
	arguments := map[string]any{}
	for key, value := range p.Arguments {
		arguments[key] = value
	}
	if provider == "lark" {
		chatID := providerOperationString(arguments, "chatId")
		if chatID == "" {
			return ProviderOperation{}, errf(400, "chatId is required for managed Lark reads")
		}
		if (action == "get" || action == "replies") && providerOperationString(arguments, "messageId") == "" {
			return ProviderOperation{}, errf(400, "messageId is required for Lark messages %s", action)
		}
		if !hasEnabledConversationMembershipLocked(h.memberships, address.ID, chatID) {
			return ProviderOperation{}, errf(403, "address %s has no enabled Membership for Lark chat %s", address.ID, chatID)
		}
	}
	ts := now()
	operation := ProviderOperation{
		ID: newIntegrationID("pop"), Provider: provider, AgentID: address.AgentID,
		AddressID: address.ID, ConnectionID: connection.ID, Resource: resource,
		Action: action, Arguments: arguments, State: "pending", CreatedAt: ts, UpdatedAt: ts,
	}
	if err := h.st.AppendProviderOperation(operation); err != nil {
		return ProviderOperation{}, errf(500, "persist provider operation: %s", err)
	}
	h.providerOperations[operation.ID] = &operation
	h.providerOperationOrder = append(h.providerOperationOrder, operation.ID)
	h.emitGlobalLocked("loom/provider-operation", map[string]any{"operation": operation})
	return operation, nil
}

func validParallReadOperation(resource, action string) bool {
	switch resource + "/" + action {
	case "chats/list", "chats/get", "chats/discoverable", "chats/members-list",
		"messages/list", "messages/get", "messages/replies":
		return true
	default:
		return false
	}
}

func validProviderReadOperation(provider, resource, action string) bool {
	switch provider {
	case "parall":
		return validParallReadOperation(resource, action)
	case "lark":
		return resource == "messages" && (action == "list" || action == "get" || action == "replies")
	default:
		return false
	}
}

func providerOperationString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

func hasEnabledConversationMembershipLocked(memberships map[string]*ConversationMembership, addressID, conversationID string) bool {
	for _, membership := range memberships {
		if membership != nil && membership.AddressID == addressID && membership.ConversationID == conversationID &&
			membership.Enabled && membership.ArchivedAt == "" {
			return true
		}
	}
	return false
}

func (h *Hub) GetProviderOperation(id string) (ProviderOperation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	operation := h.providerOperations[strings.TrimSpace(id)]
	if operation == nil {
		return ProviderOperation{}, errf(404, "provider operation not found: %s", id)
	}
	return *operation, nil
}

func (h *Hub) ClaimNextProviderOperation(connectionID string) (*ConnectorCommand, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.isDrainingLocked() {
		return nil, nil
	}
	connection := h.connections[connectionID]
	if connection == nil || !connection.Enabled {
		return nil, errf(404, "enabled connection not found: %s", connectionID)
	}
	currentTime := time.Now().UTC()
	for _, id := range h.providerOperationOrder {
		operation := h.providerOperations[id]
		if operation != nil && operation.State == "running" && operation.ConnectionID == connectionID &&
			providerOperationClaimExpired(operation, currentTime) {
			next := *operation
			next.State = "pending"
			next.AttemptToken = ""
			next.ClaimExpiresAt = ""
			next.LastError = "provider operation claim expired before result"
			next.UpdatedAt = now()
			if err := h.commitProviderOperationLocked(next); err != nil {
				return nil, errf(500, "requeue expired provider operation: %s", err)
			}
			operation = h.providerOperations[id]
		}
		if operation == nil || operation.State != "pending" || operation.ConnectionID != connectionID {
			continue
		}
		address := h.addresses[operation.AddressID]
		if address == nil || !address.Enabled || address.ConnectionID != connectionID {
			continue
		}
		next := *operation
		next.State = "running"
		next.AttemptCount++
		next.AttemptToken = newIntegrationID("claim")
		next.ClaimExpiresAt = currentTime.Add(connectorClaimLease).Format(time.RFC3339Nano)
		next.LastError = ""
		next.UpdatedAt = now()
		if err := h.commitProviderOperationLocked(next); err != nil {
			return nil, errf(500, "persist provider operation claim: %s", err)
		}
		return &ConnectorCommand{
			Type: "provider_operation", Connection: *connection, Address: *address,
			ProviderOperation: &next,
		}, nil
	}
	return nil, nil
}

func (h *Hub) ClaimNextConnectorCommand(connectionID string) (*ConnectorCommand, error) {
	h.RequeueSendingForConnection(connectionID)
	h.RequeueProviderOperationsForConnection(connectionID)
	if h.connectorCommandInFlight(connectionID, time.Now().UTC()) {
		return nil, nil
	}
	command, err := h.ClaimNextOutbox(connectionID)
	if err != nil || command != nil {
		return command, err
	}
	return h.ClaimNextProviderOperation(connectionID)
}

func (h *Hub) connectorCommandInFlight(connectionID string, currentTime time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range h.outbox {
		if item == nil || item.State != "sending" || outboxClaimExpired(item, currentTime) {
			continue
		}
		address := h.addresses[item.AddressID]
		if address != nil && address.ConnectionID == connectionID {
			return true
		}
	}
	for _, operation := range h.providerOperations {
		if operation != nil && operation.ConnectionID == connectionID && operation.State == "running" &&
			!providerOperationClaimExpired(operation, currentTime) {
			return true
		}
	}
	return false
}

func (h *Hub) RequeueProviderOperationsForConnection(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	currentTime := time.Now().UTC()
	for _, id := range h.providerOperationOrder {
		operation := h.providerOperations[id]
		if operation == nil || operation.State != "running" || operation.ConnectionID != connectionID {
			continue
		}
		if !providerOperationClaimExpired(operation, currentTime) {
			continue
		}
		next := *operation
		next.State = "pending"
		next.AttemptToken = ""
		next.ClaimExpiresAt = ""
		next.LastError = "provider operation claim expired before result"
		next.UpdatedAt = now()
		if err := h.commitProviderOperationLocked(next); err != nil {
			log.Printf("[codex-loom] requeue expired provider operation %s: %v", next.ID, err)
		}
	}
}

func (h *Hub) CompleteProviderOperation(connectionID, id string, p ProviderOperationResultParams) (ProviderOperation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	operation := h.providerOperations[strings.TrimSpace(id)]
	if operation == nil {
		return ProviderOperation{}, errf(404, "provider operation not found: %s", id)
	}
	if operation.ConnectionID != connectionID {
		return ProviderOperation{}, errf(403, "provider operation does not belong to connection")
	}
	if operation.State == "succeeded" || operation.State == "failed" {
		return *operation, nil
	}
	if operation.State != "running" {
		return ProviderOperation{}, errf(409, "provider operation is %s, not running", operation.State)
	}
	if strings.TrimSpace(p.AttemptToken) == "" || p.AttemptToken != operation.AttemptToken {
		return ProviderOperation{}, errf(409, "stale or missing provider operation attempt token")
	}
	ts := now()
	next := *operation
	next.UpdatedAt = ts
	next.CompletedAt = ts
	next.ClaimExpiresAt = ""
	if p.Success {
		result := json.RawMessage(append([]byte(nil), p.Result...))
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		if len(result) > maxProviderOperationResultBytes {
			p.Success = false
			p.Error = "provider result exceeds 768 KiB"
		} else if !json.Valid(result) {
			p.Success = false
			p.Error = "provider returned invalid JSON"
		} else {
			next.State = "succeeded"
			next.Result = result
			next.LastError = ""
		}
	}
	if !p.Success {
		next.State = "failed"
		next.Result = nil
		next.LastError = strings.TrimSpace(p.Error)
		if next.LastError == "" {
			next.LastError = "provider operation failed"
		}
	}
	if err := h.commitProviderOperationLocked(next); err != nil {
		return ProviderOperation{}, errf(500, "persist provider operation result: %s", err)
	}
	return next, nil
}

func (h *Hub) commitProviderOperationLocked(operation ProviderOperation) error {
	if err := h.st.AppendProviderOperation(operation); err != nil {
		return err
	}
	copy := operation
	h.providerOperations[operation.ID] = &copy
	h.emitGlobalLocked("loom/provider-operation", map[string]any{"operation": copy})
	return nil
}

func providerOperationClaimExpired(operation *ProviderOperation, currentTime time.Time) bool {
	return operation == nil || operation.State != "running" || leaseExpired(operation.ClaimExpiresAt, currentTime)
}
