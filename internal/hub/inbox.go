package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

type PlatformConnection struct {
	ID              string   `json:"id"`
	Provider        string   `json:"provider"`
	AccountRef      string   `json:"accountRef,omitempty"`
	CredentialRef   string   `json:"credentialRef,omitempty"`
	Status          string   `json:"status"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Cursor          string   `json:"cursor,omitempty"`
	LastEventAt     string   `json:"lastEventAt,omitempty"`
	LastHeartbeatAt string   `json:"lastHeartbeatAt,omitempty"`
	LastError       string   `json:"lastError,omitempty"`
	Enabled         bool     `json:"enabled"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

type AgentAddress struct {
	ID                 string   `json:"id"`
	AgentID            string   `json:"agentId"`
	ConnectionID       string   `json:"connectionId"`
	ExternalIdentity   string   `json:"externalIdentity"`
	DisplayName        string   `json:"displayName,omitempty"`
	TriggerPolicy      string   `json:"triggerPolicy"`
	ReplyPolicy        string   `json:"replyPolicy"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors,omitempty"`
	AllowConversations []string `json:"allowConversations,omitempty"`
	BlockActors        []string `json:"blockActors,omitempty"`
	BlockConversations []string `json:"blockConversations,omitempty"`
	Enabled            bool     `json:"enabled"`
	CreatedAt          string   `json:"createdAt"`
	UpdatedAt          string   `json:"updatedAt"`
}

// ConversationMembership describes one agent address's durable role in one
// external conversation. Behavioral context is versioned independently from
// the transport identity because one identity can participate in many groups.
type ConversationMembership struct {
	ID             string `json:"id"`
	AddressID      string `json:"addressId"`
	ConversationID string `json:"conversationId"`
	DisplayName    string `json:"displayName,omitempty"`
	Purpose        string `json:"purpose,omitempty"`
	Role           string `json:"role,omitempty"`
	Guidance       string `json:"guidance,omitempty"`
	TriggerPolicy  string `json:"triggerPolicy"`
	ReplyPolicy    string `json:"replyPolicy"`
	TrustDomain    string `json:"trustDomain"`
	Enabled        bool   `json:"enabled"`
	Version        int    `json:"version"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type ActorRef struct {
	Provider      string `json:"provider"`
	ConnectionID  string `json:"connectionId,omitempty"`
	ExternalID    string `json:"externalId"`
	DisplayName   string `json:"displayName,omitempty"`
	Kind          string `json:"kind,omitempty"`
	LinkedAgentID string `json:"linkedAgentId,omitempty"`
}

type ConversationRef struct {
	Provider         string `json:"provider"`
	ConnectionID     string `json:"connectionId"`
	ConversationID   string `json:"conversationId"`
	ThreadID         string `json:"threadId,omitempty"`
	MessageID        string `json:"messageId,omitempty"`
	ConversationType string `json:"conversationType,omitempty"`
	Audience         string `json:"audience,omitempty"`
}

type AttachmentRef struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
	URL      string `json:"url,omitempty"`
	Path     string `json:"path,omitempty"`
}

type MessageContent struct {
	Text        string          `json:"text,omitempty"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
}

type InboxMessage struct {
	ID                  string          `json:"id"`
	Origin              string          `json:"origin"`
	ExternalKey         string          `json:"externalKey"`
	ExternalEventID     string          `json:"externalEventId,omitempty"`
	ExternalMessageID   string          `json:"externalMessageId,omitempty"`
	Sender              ActorRef        `json:"sender"`
	Conversation        ConversationRef `json:"conversation"`
	Content             MessageContent  `json:"content"`
	ReplyTo             string          `json:"replyTo,omitempty"`
	ResponseExpectation string          `json:"responseExpectation"`
	OccurredAt          string          `json:"occurredAt,omitempty"`
	ReceivedAt          string          `json:"receivedAt"`
	ProviderMetadata    map[string]any  `json:"providerMetadata,omitempty"`
}

type InboxItem struct {
	ID              string `json:"id"`
	AgentID         string `json:"agentId"`
	MessageID       string `json:"messageId"`
	AddressID       string `json:"addressId"`
	MembershipID    string `json:"membershipId,omitempty"`
	State           string `json:"state"`
	Outcome         string `json:"outcome,omitempty"`
	Priority        int    `json:"priority,omitempty"`
	AvailableAt     string `json:"availableAt,omitempty"`
	AttemptCount    int    `json:"attemptCount"`
	ActiveAttemptID string `json:"activeAttemptId,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	Note            string `json:"note,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type HandlingAttempt struct {
	ID                   string `json:"id"`
	InboxItemID          string `json:"inboxItemId"`
	AgentID              string `json:"agentId"`
	SessionID            string `json:"sessionId,omitempty"` // compatibility
	TurnID               string `json:"turnId,omitempty"`
	MembershipID         string `json:"membershipId,omitempty"`
	MembershipVersion    int    `json:"membershipVersion,omitempty"`
	EffectiveReplyPolicy string `json:"effectiveReplyPolicy,omitempty"`
	Status               string `json:"status"`
	FinalAnswer          string `json:"finalAnswer,omitempty"`
	StartedAt            string `json:"startedAt"`
	CompletedAt          string `json:"completedAt,omitempty"`
	Error                string `json:"error,omitempty"`
}

type OutboxItem struct {
	ID                  string          `json:"id"`
	AgentID             string          `json:"agentId"`
	AddressID           string          `json:"addressId"`
	InReplyTo           string          `json:"inReplyTo,omitempty"`
	Conversation        ConversationRef `json:"conversation"`
	Content             MessageContent  `json:"content"`
	ResponseExpectation string          `json:"responseExpectation,omitempty"`
	IdempotencyKey      string          `json:"idempotencyKey"`
	State               string          `json:"state"`
	ExternalMessageID   string          `json:"externalMessageId,omitempty"`
	AttemptCount        int             `json:"attemptCount"`
	LastError           string          `json:"lastError,omitempty"`
	CreatedAt           string          `json:"createdAt"`
	UpdatedAt           string          `json:"updatedAt"`
	SentAt              string          `json:"sentAt,omitempty"`
}

type integrationConfig struct {
	Connections map[string]*PlatformConnection     `json:"connections"`
	Addresses   map[string]*AgentAddress           `json:"addresses"`
	Memberships map[string]*ConversationMembership `json:"memberships,omitempty"`
}

type ConnectionParams struct {
	Provider      string   `json:"provider"`
	AccountRef    string   `json:"accountRef"`
	CredentialRef string   `json:"credentialRef"`
	Capabilities  []string `json:"capabilities"`
	Enabled       *bool    `json:"enabled"`
}

type AddressParams struct {
	Agent              string   `json:"agent"`
	ConnectionID       string   `json:"connectionId"`
	ExternalIdentity   string   `json:"externalIdentity"`
	DisplayName        string   `json:"displayName"`
	TriggerPolicy      string   `json:"triggerPolicy"`
	ReplyPolicy        string   `json:"replyPolicy"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors"`
	AllowConversations []string `json:"allowConversations"`
	BlockActors        []string `json:"blockActors"`
	BlockConversations []string `json:"blockConversations"`
	Enabled            *bool    `json:"enabled"`
}

type ConversationMembershipParams struct {
	AddressID       string  `json:"addressId"`
	ConversationID  string  `json:"conversationId"`
	DisplayName     *string `json:"displayName"`
	Purpose         *string `json:"purpose"`
	Role            *string `json:"role"`
	Guidance        *string `json:"guidance"`
	TriggerPolicy   *string `json:"triggerPolicy"`
	ReplyPolicy     *string `json:"replyPolicy"`
	TrustDomain     *string `json:"trustDomain"`
	Enabled         *bool   `json:"enabled"`
	ExpectedVersion *int    `json:"expectedVersion"`
}

type TriggerEvidence struct {
	Direct           bool `json:"direct"`
	Mentioned        bool `json:"mentioned"`
	ExplicitDispatch bool `json:"explicitDispatch"`
}

type IngressParams struct {
	ConnectionID        string          `json:"connectionId"`
	AddressID           string          `json:"addressId"`
	ExternalEventID     string          `json:"externalEventId"`
	ExternalMessageID   string          `json:"externalMessageId"`
	Sender              ActorRef        `json:"sender"`
	Conversation        ConversationRef `json:"conversation"`
	Content             MessageContent  `json:"content"`
	ReplyTo             string          `json:"replyTo"`
	ResponseExpectation string          `json:"responseExpectation"`
	OccurredAt          string          `json:"occurredAt"`
	ProviderMetadata    map[string]any  `json:"providerMetadata"`
	Trigger             TriggerEvidence `json:"trigger"`
}

type IngressResult struct {
	Message   *InboxMessage `json:"message,omitempty"`
	InboxItem *InboxItem    `json:"inboxItem,omitempty"`
	Duplicate bool          `json:"duplicate"`
	Ignored   bool          `json:"ignored,omitempty"`
	Reason    string        `json:"reason,omitempty"`
}

type InboxEntry struct {
	Item            InboxItem               `json:"item"`
	Message         InboxMessage            `json:"message"`
	Address         AgentAddress            `json:"address"`
	Membership      *ConversationMembership `json:"membership,omitempty"`
	Attempt         *HandlingAttempt        `json:"attempt,omitempty"`
	AgentName       string                  `json:"agentName"`
	Outbox          *OutboxItem             `json:"outboxItem,omitempty"`
	InternalMessage *AgentMessage           `json:"internalMessage,omitempty"`
}

type InboxActionParams struct {
	Agent   string         `json:"agent"`
	Content MessageContent `json:"content"`
	Reason  string         `json:"reason"`
	Until   string         `json:"until"`
}

type ConnectionHeartbeatParams struct {
	Status       string   `json:"status"`
	Cursor       string   `json:"cursor"`
	Capabilities []string `json:"capabilities"`
	Error        string   `json:"error"`
}

type OutboxResultParams struct {
	Success           bool   `json:"success"`
	ExternalMessageID string `json:"externalMessageId"`
	Cursor            string `json:"cursor"`
	Error             string `json:"error"`
}

type OutboxParams struct {
	Agent               string          `json:"agent"`
	AddressID           string          `json:"addressId"`
	Conversation        ConversationRef `json:"conversation"`
	Content             MessageContent  `json:"content"`
	ResponseExpectation string          `json:"responseExpectation"`
	IdempotencyKey      string          `json:"idempotencyKey"`
}

type ConnectorCommand struct {
	Type       string             `json:"type"`
	Connection PlatformConnection `json:"connection"`
	Address    AgentAddress       `json:"address"`
	OutboxItem OutboxItem         `json:"outboxItem"`
}

func (h *Hub) loadIntegrations() error {
	var cfg integrationConfig
	if err := h.st.LoadIntegrations(&cfg); err != nil {
		return err
	}
	if cfg.Connections != nil {
		h.connections = cfg.Connections
	}
	if cfg.Addresses != nil {
		h.addresses = cfg.Addresses
	}
	if cfg.Memberships != nil {
		h.memberships = cfg.Memberships
	}
	changed := h.migrateAllowedConversationsLocked()
	if changed {
		return h.persistIntegrationsLocked()
	}
	return nil
}

func (h *Hub) persistIntegrationsLocked() error {
	return h.st.SaveIntegrations(integrationConfig{Connections: h.connections, Addresses: h.addresses, Memberships: h.memberships})
}

func (h *Hub) loadInboxState() error {
	if err := h.st.ReadMessages(func(raw json.RawMessage) {
		var msg InboxMessage
		if json.Unmarshal(raw, &msg) != nil || msg.ID == "" {
			return
		}
		if _, exists := h.messages[msg.ID]; !exists {
			h.messageOrder = append(h.messageOrder, msg.ID)
		}
		h.messages[msg.ID] = &msg
		if msg.ExternalKey != "" {
			h.externalMessages[msg.ExternalKey] = msg.ID
		}
	}); err != nil {
		return err
	}
	if err := h.st.ReadInbox(func(raw json.RawMessage) {
		var item InboxItem
		if json.Unmarshal(raw, &item) != nil || item.ID == "" {
			return
		}
		if _, exists := h.inbox[item.ID]; !exists {
			h.inboxOrder = append(h.inboxOrder, item.ID)
		}
		if item.State == "handling" {
			item.State = "queued"
			item.ActiveAttemptID = ""
			item.LastError = "recovered after CodexLoom restart"
			item.UpdatedAt = now()
		}
		if item.Note == "" && item.LastError != "" && (item.State == "deferred" || item.State == "handled" && item.Outcome == "no_reply") {
			item.Note = item.LastError
			item.LastError = ""
		}
		h.inbox[item.ID] = &item
	}); err != nil {
		return err
	}
	if err := h.st.ReadAttempts(func(raw json.RawMessage) {
		var attempt HandlingAttempt
		if json.Unmarshal(raw, &attempt) != nil || attempt.ID == "" {
			return
		}
		if attempt.AgentID == "" {
			attempt.AgentID = attempt.SessionID
		}
		if attempt.SessionID == "" {
			attempt.SessionID = attempt.AgentID
		}
		if attempt.Status == "starting" || attempt.Status == "running" {
			attempt.Status = "interrupted"
			attempt.Error = "CodexLoom restarted during handling"
			attempt.CompletedAt = now()
		}
		h.attempts[attempt.ID] = &attempt
	}); err != nil {
		return err
	}
	return h.st.ReadOutbox(func(raw json.RawMessage) {
		var item OutboxItem
		if json.Unmarshal(raw, &item) != nil || item.ID == "" {
			return
		}
		if _, exists := h.outbox[item.ID]; !exists {
			h.outboxOrder = append(h.outboxOrder, item.ID)
		}
		if item.State == "sending" {
			item.State = "pending"
			item.LastError = "recovered after CodexLoom restart"
			item.UpdatedAt = now()
		}
		h.outbox[item.ID] = &item
	})
}

func (h *Hub) CreateConnection(p ConnectionParams) (PlatformConnection, error) {
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	if provider == "" {
		return PlatformConnection{}, errf(400, "provider is required")
	}
	credentialRef := strings.TrimSpace(p.CredentialRef)
	if credentialRef != "" && !strings.HasPrefix(credentialRef, "env:") && !strings.HasPrefix(credentialRef, "keychain:") {
		return PlatformConnection{}, errf(400, "credentialRef must use env: or keychain:")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	ts := now()
	connection := PlatformConnection{
		ID: newIntegrationID("conn"), Provider: provider, AccountRef: strings.TrimSpace(p.AccountRef),
		CredentialRef: credentialRef, Status: "disconnected", Capabilities: normalizeCapabilities(p.Capabilities),
		Enabled: enabled, CreatedAt: ts, UpdatedAt: ts,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connections[connection.ID] = &connection
	if err := h.persistIntegrationsLocked(); err != nil {
		delete(h.connections, connection.ID)
		return PlatformConnection{}, errf(500, "save integration: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": connection})
	return connection, nil
}

func (h *Hub) ListConnections() []PlatformConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]PlatformConnection, 0, len(h.connections))
	for _, connection := range h.connections {
		out = append(out, *connection)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (h *Hub) UpdateConnection(id string, p ConnectionParams) (PlatformConnection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[strings.TrimSpace(id)]
	if connection == nil {
		return PlatformConnection{}, errf(404, "connection not found: %s", id)
	}
	if value := strings.TrimSpace(p.Provider); value != "" {
		connection.Provider = strings.ToLower(value)
	}
	if p.AccountRef != "" {
		connection.AccountRef = strings.TrimSpace(p.AccountRef)
	}
	if p.CredentialRef != "" {
		value := strings.TrimSpace(p.CredentialRef)
		if !strings.HasPrefix(value, "env:") && !strings.HasPrefix(value, "keychain:") {
			return PlatformConnection{}, errf(400, "credentialRef must use env: or keychain:")
		}
		connection.CredentialRef = value
	}
	if p.Capabilities != nil {
		connection.Capabilities = normalizeCapabilities(p.Capabilities)
	}
	if p.Enabled != nil {
		connection.Enabled = *p.Enabled
		if !connection.Enabled {
			connection.Status = "disconnected"
		}
	}
	connection.UpdatedAt = now()
	if err := h.persistIntegrationsLocked(); err != nil {
		return PlatformConnection{}, errf(500, "save integration: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": *connection})
	return *connection, nil
}

func (h *Hub) HeartbeatConnection(id string, p ConnectionHeartbeatParams) (PlatformConnection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[id]
	if connection == nil {
		return PlatformConnection{}, errf(404, "connection not found: %s", id)
	}
	status := strings.TrimSpace(p.Status)
	if status == "" {
		status = "connected"
	}
	if !oneOf(status, "disconnected", "connecting", "connected", "degraded") {
		return PlatformConnection{}, errf(400, "invalid connection status %q", status)
	}
	ts := now()
	connection.Status = status
	connection.LastHeartbeatAt = ts
	connection.UpdatedAt = ts
	if p.Cursor != "" {
		connection.Cursor = p.Cursor
	}
	if p.Capabilities != nil {
		connection.Capabilities = normalizeCapabilities(p.Capabilities)
	}
	connection.LastError = strings.TrimSpace(p.Error)
	if err := h.persistIntegrationsLocked(); err != nil {
		return PlatformConnection{}, errf(500, "save connection heartbeat: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": *connection})
	return *connection, nil
}

func (h *Hub) MarkConnectionDisconnected(id, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[id]
	if connection == nil {
		return
	}
	connection.Status = "disconnected"
	connection.LastError = strings.TrimSpace(reason)
	connection.UpdatedAt = now()
	_ = h.persistIntegrationsLocked()
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": *connection})
}

func (h *Hub) CreateAddress(p AddressParams) (AgentAddress, error) {
	connectionID := strings.TrimSpace(p.ConnectionID)
	externalIdentity := strings.TrimSpace(p.ExternalIdentity)
	if connectionID == "" || externalIdentity == "" {
		return AgentAddress{}, errf(400, "connectionId and externalIdentity are required")
	}
	trigger := strings.TrimSpace(p.TriggerPolicy)
	if trigger == "" {
		trigger = "mention"
	}
	if !oneOf(trigger, "direct", "mention", "explicit_dispatch", "all", "allowlist") {
		return AgentAddress{}, errf(400, "invalid triggerPolicy %q", trigger)
	}
	reply := strings.TrimSpace(p.ReplyPolicy)
	if reply == "" {
		reply = "final_answer"
	}
	if !oneOf(reply, "explicit", "final_answer", "none") {
		return AgentAddress{}, errf(400, "invalid replyPolicy %q", reply)
	}
	trustDomain := strings.TrimSpace(p.TrustDomain)
	if trustDomain == "" {
		trustDomain = "local"
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil {
		return AgentAddress{}, errf(404, "connection not found: %s", connectionID)
	}
	agent := h.resolveLocked(p.Agent)
	if agent == nil {
		return AgentAddress{}, errf(404, "agent not found: %s", p.Agent)
	}
	for _, address := range h.addresses {
		if address.ConnectionID == connectionID && address.ExternalIdentity == externalIdentity {
			return AgentAddress{}, errf(409, "external identity is already bound to %s", address.AgentID)
		}
		if address.AgentID == agent.ID && address.TrustDomain != trustDomain {
			return AgentAddress{}, errf(409, "agent addresses must share trustDomain %q", address.TrustDomain)
		}
	}
	ts := now()
	address := AgentAddress{
		ID: newIntegrationID("addr"), AgentID: agent.ID, ConnectionID: connectionID,
		ExternalIdentity: externalIdentity, DisplayName: strings.TrimSpace(p.DisplayName),
		TriggerPolicy: trigger, ReplyPolicy: reply, TrustDomain: trustDomain,
		AllowActors: normalizeIdentityList(p.AllowActors), AllowConversations: normalizeIdentityList(p.AllowConversations),
		BlockActors: normalizeIdentityList(p.BlockActors), BlockConversations: normalizeIdentityList(p.BlockConversations),
		Enabled: enabled, CreatedAt: ts, UpdatedAt: ts,
	}
	h.addresses[address.ID] = &address
	createdMemberships := h.ensureAllowedConversationMembershipsLocked(&address)
	if err := h.persistIntegrationsLocked(); err != nil {
		delete(h.addresses, address.ID)
		for _, id := range createdMemberships {
			delete(h.memberships, id)
		}
		return AgentAddress{}, errf(500, "save agent address: %s", err)
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": address})
	return address, nil
}

func (h *Hub) ListAddresses(agentKey string) ([]AgentAddress, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(agentKey)
		if agent == nil {
			return nil, errf(404, "agent not found: %s", agentKey)
		}
		agentID = agent.ID
	}
	out := []AgentAddress{}
	for _, address := range h.addresses {
		if agentID == "" || address.AgentID == agentID {
			out = append(out, *address)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (h *Hub) UpdateAddress(id string, p AddressParams) (AgentAddress, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	address := h.addresses[id]
	if address == nil {
		return AgentAddress{}, errf(404, "agent address not found: %s", id)
	}
	if value := strings.TrimSpace(p.ExternalIdentity); value != "" && value != address.ExternalIdentity {
		for otherID, other := range h.addresses {
			if otherID != id && other.ConnectionID == address.ConnectionID && other.ExternalIdentity == value {
				return AgentAddress{}, errf(409, "external identity is already bound to %s", other.AgentID)
			}
		}
		address.ExternalIdentity = value
	}
	if value := strings.TrimSpace(p.DisplayName); value != "" {
		address.DisplayName = value
	}
	if value := strings.TrimSpace(p.TriggerPolicy); value != "" {
		if !oneOf(value, "direct", "mention", "explicit_dispatch", "all", "allowlist") {
			return AgentAddress{}, errf(400, "invalid triggerPolicy %q", value)
		}
		address.TriggerPolicy = value
	}
	if value := strings.TrimSpace(p.ReplyPolicy); value != "" {
		if !oneOf(value, "explicit", "final_answer", "none") {
			return AgentAddress{}, errf(400, "invalid replyPolicy %q", value)
		}
		address.ReplyPolicy = value
	}
	trustChanged := false
	if value := strings.TrimSpace(p.TrustDomain); value != "" && value != address.TrustDomain {
		for otherID, other := range h.addresses {
			if otherID != id && other.AgentID == address.AgentID && other.TrustDomain != value {
				return AgentAddress{}, errf(409, "agent addresses must share trustDomain %q", other.TrustDomain)
			}
		}
		address.TrustDomain = value
		trustChanged = true
	}
	if p.Enabled != nil {
		address.Enabled = *p.Enabled
	}
	if p.AllowActors != nil {
		address.AllowActors = normalizeIdentityList(p.AllowActors)
	}
	if p.AllowConversations != nil {
		address.AllowConversations = normalizeIdentityList(p.AllowConversations)
	}
	if p.BlockActors != nil {
		address.BlockActors = normalizeIdentityList(p.BlockActors)
	}
	if p.BlockConversations != nil {
		address.BlockConversations = normalizeIdentityList(p.BlockConversations)
	}
	address.UpdatedAt = now()
	createdMemberships := h.ensureAllowedConversationMembershipsLocked(address)
	updatedMemberships := []ConversationMembership{}
	if trustChanged {
		for _, membership := range h.memberships {
			if membership == nil || membership.AddressID != address.ID {
				continue
			}
			membership.TrustDomain = address.TrustDomain
			membership.Version++
			membership.UpdatedAt = address.UpdatedAt
			updatedMemberships = append(updatedMemberships, *membership)
		}
	}
	if err := h.persistIntegrationsLocked(); err != nil {
		for _, id := range createdMemberships {
			delete(h.memberships, id)
		}
		return AgentAddress{}, errf(500, "save agent address: %s", err)
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": *address})
	for _, membership := range updatedMemberships {
		h.emitGlobalLocked("loom/conversation-membership", map[string]any{"membership": membership})
	}
	return *address, nil
}

func (h *Hub) IngestMessage(p IngressParams) (IngressResult, error) {
	connectionID := strings.TrimSpace(p.ConnectionID)
	addressID := strings.TrimSpace(p.AddressID)
	externalID := strings.TrimSpace(p.ExternalMessageID)
	if externalID == "" {
		externalID = strings.TrimSpace(p.ExternalEventID)
	}
	if connectionID == "" || addressID == "" || externalID == "" {
		return IngressResult{}, errf(400, "connectionId, addressId and external message/event id are required")
	}
	if strings.TrimSpace(p.Content.Text) == "" && len(p.Content.Attachments) == 0 {
		return IngressResult{}, errf(400, "message content is empty")
	}
	expectation := strings.TrimSpace(p.ResponseExpectation)
	if expectation == "" {
		expectation = "optional"
	}
	if !oneOf(expectation, "required", "optional", "none") {
		return IngressResult{}, errf(400, "invalid responseExpectation %q", expectation)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil || !connection.Enabled {
		return IngressResult{}, errf(404, "enabled connection not found: %s", connectionID)
	}
	address := h.addresses[addressID]
	if address == nil || !address.Enabled || address.ConnectionID != connectionID {
		return IngressResult{}, errf(404, "enabled address not found for connection: %s", addressID)
	}
	if h.agents[address.AgentID] == nil {
		return IngressResult{}, errf(404, "address target agent not found: %s", address.AgentID)
	}
	externalKey := connectionID + ":" + externalID
	if messageID := h.externalMessages[externalKey]; messageID != "" {
		message := h.messages[messageID]
		item := h.findInboxForMessageLocked(messageID, address.AgentID)
		if message != nil && item != nil {
			messageCopy, itemCopy := *message, *item
			return IngressResult{Message: &messageCopy, InboxItem: &itemCopy, Duplicate: true}, nil
		}
	}
	membership := h.membershipForConversationLocked(address.ID, strings.TrimSpace(p.Conversation.ConversationID))
	if membership != nil && !membership.Enabled {
		membership = nil
	}
	if conversationNeedsMembership(p.Conversation) && membership == nil {
		connection.LastEventAt = now()
		connection.UpdatedAt = connection.LastEventAt
		_ = h.persistIntegrationsLocked()
		h.emitGlobalLocked("loom/inbox-ignored", map[string]any{
			"connectionId": connection.ID, "addressId": address.ID,
			"externalMessageId": externalID, "reason": "group has no enabled conversation membership",
		})
		return IngressResult{Ignored: true, Reason: "group has no enabled conversation membership"}, nil
	}
	if allowed, reason := addressAllowsIngress(*address, membership, p); !allowed {
		connection.LastEventAt = now()
		connection.UpdatedAt = connection.LastEventAt
		_ = h.persistIntegrationsLocked()
		h.emitGlobalLocked("loom/inbox-ignored", map[string]any{
			"connectionId": connection.ID, "addressId": address.ID,
			"externalMessageId": externalID, "reason": reason,
		})
		return IngressResult{Ignored: true, Reason: reason}, nil
	}
	ts := now()
	conversation := p.Conversation
	conversation.Provider = connection.Provider
	conversation.ConnectionID = connectionID
	message := InboxMessage{
		ID: newIntegrationID("imsg"), Origin: connection.Provider, ExternalKey: externalKey,
		ExternalEventID: strings.TrimSpace(p.ExternalEventID), ExternalMessageID: externalID,
		Sender: p.Sender, Conversation: conversation, Content: p.Content,
		ReplyTo: strings.TrimSpace(p.ReplyTo), ResponseExpectation: expectation,
		OccurredAt: strings.TrimSpace(p.OccurredAt), ReceivedAt: ts, ProviderMetadata: p.ProviderMetadata,
	}
	message.Sender.Provider = connection.Provider
	message.Sender.ConnectionID = connectionID
	if err := h.st.AppendMessage(message); err != nil {
		return IngressResult{}, errf(500, "persist message: %s", err)
	}
	h.messages[message.ID] = &message
	h.messageOrder = append(h.messageOrder, message.ID)
	h.externalMessages[externalKey] = message.ID

	item := InboxItem{
		ID: newIntegrationID("inb"), AgentID: address.AgentID, MessageID: message.ID, AddressID: address.ID,
		State: "queued", CreatedAt: ts, UpdatedAt: ts,
	}
	if membership != nil {
		item.MembershipID = membership.ID
	}
	if err := h.st.AppendInbox(item); err != nil {
		return IngressResult{}, errf(500, "persist inbox item: %s", err)
	}
	h.inbox[item.ID] = &item
	h.inboxOrder = append(h.inboxOrder, item.ID)
	connection.LastEventAt = ts
	connection.UpdatedAt = ts
	_ = h.persistIntegrationsLocked()
	h.emitGlobalLocked("loom/inbox-message", map[string]any{"message": message, "inboxItem": item})
	return IngressResult{Message: &message, InboxItem: &item}, nil
}

func (h *Hub) findInboxForMessageLocked(messageID, agentID string) *InboxItem {
	for _, id := range h.inboxOrder {
		item := h.inbox[id]
		if item != nil && item.MessageID == messageID && item.AgentID == agentID {
			return item
		}
	}
	return nil
}

func (h *Hub) ListInbox(agentKey, state, origin string) ([]InboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(agentKey)
		if agent == nil {
			return nil, errf(404, "agent not found: %s", agentKey)
		}
		agentID = agent.ID
	}
	out := []InboxItem{}
	for i := len(h.inboxOrder) - 1; i >= 0; i-- {
		item := h.inbox[h.inboxOrder[i]]
		if item == nil || (agentID != "" && item.AgentID != agentID) || (state != "" && item.State != state) {
			continue
		}
		if origin != "" {
			message := h.messages[item.MessageID]
			if message == nil || message.Origin != origin {
				continue
			}
		}
		out = append(out, *item)
	}
	return out, nil
}

func (h *Hub) GetInboxItem(id string) (InboxItem, InboxMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.inbox[id]
	if item == nil {
		return InboxItem{}, InboxMessage{}, errf(404, "inbox item not found: %s", id)
	}
	message := h.messages[item.MessageID]
	if message == nil {
		return InboxItem{}, InboxMessage{}, errf(500, "inbox message missing: %s", item.MessageID)
	}
	return *item, *message, nil
}

func (h *Hub) ListInboxEntries(agentKey, state, origin string) ([]InboxEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(agentKey)
		if agent == nil {
			return nil, errf(404, "agent not found: %s", agentKey)
		}
		agentID = agent.ID
	}
	out := []InboxEntry{}
	for i := len(h.inboxOrder) - 1; i >= 0; i-- {
		item := h.inbox[h.inboxOrder[i]]
		if item == nil || (agentID != "" && item.AgentID != agentID) || (state != "" && item.State != state) {
			continue
		}
		message := h.messages[item.MessageID]
		if message == nil || (origin != "" && message.Origin != origin) {
			continue
		}
		address := h.addresses[item.AddressID]
		if address == nil {
			address = &AgentAddress{ID: item.AddressID, AgentID: item.AgentID}
		}
		agentName := item.AgentID
		if agent := h.agents[item.AgentID]; agent != nil {
			agentName = agent.Name
		}
		entry := InboxEntry{Item: *item, Message: *message, Address: *address, AgentName: agentName}
		if membership := h.memberships[item.MembershipID]; membership != nil {
			cp := *membership
			entry.Membership = &cp
		}
		if attempt := h.latestAttemptForInboxLocked(item.ID); attempt != nil {
			cp := *attempt
			entry.Attempt = &cp
		}
		if reply := h.replyOutboxLocked(item.ID); reply != nil {
			cp := *reply
			entry.Outbox = &cp
		}
		out = append(out, entry)
	}
	if origin == "" || origin == "loom" || origin == "chub" {
		for i := len(h.commOrder) - 1; i >= 0; i-- {
			msg := h.comms[h.commOrder[i]]
			if msg == nil || msg.ReplyTo != "" || (agentID != "" && msg.ToAgentID != agentID) {
				continue
			}
			itemState, outcome := internalInboxState(msg)
			if state != "" && itemState != state {
				continue
			}
			createdAt := msg.CreatedAt
			item := InboxItem{
				ID: "loom:" + msg.ID, AgentID: msg.ToAgentID, MessageID: msg.ID,
				AddressID: "loom", State: itemState, Outcome: outcome,
				LastError: msg.LastDeliveryError, CreatedAt: createdAt, UpdatedAt: msg.UpdatedAt,
			}
			inboxMessage := InboxMessage{
				ID: msg.ID, Origin: "loom", ExternalKey: msg.ID,
				Sender:       ActorRef{Provider: "loom", ExternalID: msg.FromAgentID, DisplayName: msg.From, Kind: "agent", LinkedAgentID: msg.FromAgentID},
				Conversation: ConversationRef{Provider: "loom", ConnectionID: "loom", ConversationID: msg.ID, MessageID: msg.ID, ConversationType: "agent"},
				Content:      MessageContent{Text: msg.Body}, ReplyTo: msg.ReplyTo,
				ResponseExpectation: msg.Response, OccurredAt: msg.CreatedAt, ReceivedAt: msg.CreatedAt,
				ProviderMetadata: map[string]any{"subject": msg.Subject},
			}
			agentName := msg.To
			if target := h.agents[msg.ToAgentID]; target != nil {
				agentName = target.Name
			}
			cp := *msg
			out = append(out, InboxEntry{
				Item: item, Message: inboxMessage,
				Address:   AgentAddress{ID: "loom", AgentID: msg.ToAgentID, ConnectionID: "loom", ExternalIdentity: msg.To, DisplayName: msg.To, TriggerPolicy: "direct", ReplyPolicy: "explicit", TrustDomain: "local", Enabled: true},
				AgentName: agentName, InternalMessage: &cp,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Item.CreatedAt > out[j].Item.CreatedAt })
	return out, nil
}

func (h *Hub) GetInboxEntry(id string) (InboxEntry, error) {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "chub:") {
		id = "loom:" + strings.TrimPrefix(id, "chub:")
	}
	entries, err := h.ListInboxEntries("", "", "")
	if err != nil {
		return InboxEntry{}, err
	}
	for _, entry := range entries {
		if entry.Item.ID == id {
			return entry, nil
		}
	}
	return InboxEntry{}, errf(404, "inbox item not found: %s", id)
}

func (h *Hub) latestAttemptForInboxLocked(inboxItemID string) *HandlingAttempt {
	var latest *HandlingAttempt
	for _, attempt := range h.attempts {
		if attempt == nil || attempt.InboxItemID != inboxItemID {
			continue
		}
		if latest == nil || attempt.StartedAt > latest.StartedAt {
			latest = attempt
		}
	}
	return latest
}

func internalInboxState(msg *AgentMessage) (string, string) {
	if msg.DeliveryStatus == "failed" || msg.DeliveryStatus == "cancelled" {
		return "failed", ""
	}
	if msg.Resolution == "reply" || msg.Status == "answered" {
		return "handled", "reply"
	}
	if msg.Resolution == "no_reply" || msg.Response == "none" {
		return "handled", "no_reply"
	}
	if msg.DeliveryStatus == "queued" || msg.DeliveryStatus == "delivering" {
		return "queued", ""
	}
	return "handling", ""
}

func (h *Hub) ListOutbox(agentKey, state string) ([]OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(agentKey)
		if agent == nil {
			return nil, errf(404, "agent not found: %s", agentKey)
		}
		agentID = agent.ID
	}
	out := []OutboxItem{}
	for i := len(h.outboxOrder) - 1; i >= 0; i-- {
		item := h.outbox[h.outboxOrder[i]]
		if item == nil || (agentID != "" && item.AgentID != agentID) || (state != "" && item.State != state) {
			continue
		}
		out = append(out, *item)
	}
	return out, nil
}

func (h *Hub) CreateOutbox(p OutboxParams) (OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.resolveLocked(strings.TrimSpace(p.Agent))
	if agent == nil {
		return OutboxItem{}, errf(404, "agent not found: %s", p.Agent)
	}
	address := h.addresses[strings.TrimSpace(p.AddressID)]
	if address == nil || !address.Enabled || address.AgentID != agent.ID {
		return OutboxItem{}, errf(404, "enabled agent address not found: %s", p.AddressID)
	}
	connection := h.connections[address.ConnectionID]
	if connection == nil || !connection.Enabled {
		return OutboxItem{}, errf(404, "enabled address connection not found")
	}
	if strings.TrimSpace(p.Conversation.ConversationID) == "" {
		return OutboxItem{}, errf(400, "conversationId is required")
	}
	if strings.TrimSpace(p.Content.Text) == "" && len(p.Content.Attachments) == 0 {
		return OutboxItem{}, errf(400, "outbox content is empty")
	}
	expectation := strings.TrimSpace(p.ResponseExpectation)
	if expectation == "" {
		expectation = "optional"
	}
	if !oneOf(expectation, "required", "optional", "none") {
		return OutboxItem{}, errf(400, "invalid responseExpectation %q", expectation)
	}
	if key := strings.TrimSpace(p.IdempotencyKey); key != "" {
		for _, item := range h.outbox {
			if item.IdempotencyKey == key {
				return *item, nil
			}
		}
	}
	ts := now()
	id := newIntegrationID("out")
	key := strings.TrimSpace(p.IdempotencyKey)
	if key == "" {
		key = "send:" + id
	}
	conversation := p.Conversation
	conversation.Provider = connection.Provider
	conversation.ConnectionID = connection.ID
	item := OutboxItem{
		ID: id, AgentID: agent.ID, AddressID: address.ID, Conversation: conversation,
		Content: p.Content, ResponseExpectation: expectation, IdempotencyKey: key,
		State: "pending", CreatedAt: ts, UpdatedAt: ts,
	}
	if err := h.st.AppendOutbox(item); err != nil {
		return OutboxItem{}, errf(500, "persist outbox item: %s", err)
	}
	h.outbox[item.ID] = &item
	h.outboxOrder = append(h.outboxOrder, item.ID)
	h.emitGlobalLocked("loom/outbox-item", map[string]any{"item": item})
	return item, nil
}

func (h *Hub) RequeueSendingForConnection(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range h.outboxOrder {
		item := h.outbox[id]
		if item == nil || item.State != "sending" {
			continue
		}
		address := h.addresses[item.AddressID]
		if address == nil || address.ConnectionID != connectionID {
			continue
		}
		item.State = "pending"
		item.LastError = "connector reconnected before delivery result"
		item.UpdatedAt = now()
		h.appendOutboxLocked(item)
	}
}

func (h *Hub) ClaimNextOutbox(connectionID string) (*ConnectorCommand, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil || !connection.Enabled {
		return nil, errf(404, "enabled connection not found: %s", connectionID)
	}
	for _, id := range h.outboxOrder {
		item := h.outbox[id]
		if item == nil || item.State != "pending" {
			continue
		}
		address := h.addresses[item.AddressID]
		if address == nil || !address.Enabled || address.ConnectionID != connectionID {
			continue
		}
		item.State = "sending"
		item.AttemptCount++
		item.LastError = ""
		item.UpdatedAt = now()
		h.appendOutboxLocked(item)
		return &ConnectorCommand{Type: "send", Connection: *connection, Address: *address, OutboxItem: *item}, nil
	}
	return nil, nil
}

func (h *Hub) CompleteOutbox(connectionID, id string, p OutboxResultParams) (OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.outbox[id]
	if item == nil {
		return OutboxItem{}, errf(404, "outbox item not found: %s", id)
	}
	address := h.addresses[item.AddressID]
	if address == nil || address.ConnectionID != connectionID {
		return OutboxItem{}, errf(403, "outbox item does not belong to connection")
	}
	if item.State == "sent" {
		return *item, nil
	}
	ts := now()
	item.UpdatedAt = ts
	if p.Success {
		item.State = "sent"
		item.ExternalMessageID = strings.TrimSpace(p.ExternalMessageID)
		item.SentAt = ts
		item.LastError = ""
	} else {
		item.State = "failed"
		item.LastError = strings.TrimSpace(p.Error)
		if item.LastError == "" {
			item.LastError = "connector send failed"
		}
	}
	h.appendOutboxLocked(item)
	if connection := h.connections[connectionID]; connection != nil {
		if p.Cursor != "" {
			connection.Cursor = p.Cursor
		}
		connection.LastEventAt = ts
		connection.UpdatedAt = ts
		if !p.Success {
			connection.Status = "degraded"
			connection.LastError = item.LastError
		}
		_ = h.persistIntegrationsLocked()
	}
	return *item, nil
}

func (h *Hub) RetryOutboxItem(id string) (OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.outbox[id]
	if item == nil {
		return OutboxItem{}, errf(404, "outbox item not found: %s", id)
	}
	if item.State != "failed" {
		return OutboxItem{}, errf(409, "only failed outbox items can be retried")
	}
	item.State = "pending"
	item.LastError = ""
	item.UpdatedAt = now()
	h.appendOutboxLocked(item)
	return *item, nil
}

func (h *Hub) inboxLoop() {
	h.drainInbox()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.drainInbox()
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) drainInbox() {
	for _, agentID := range h.queuedInboxAgents() {
		h.deliverNextInboxForAgent(agentID)
	}
}

func (h *Hub) queuedInboxAgents() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, id := range h.inboxOrder {
		item := h.inbox[id]
		if item != nil && item.State == "deferred" && inboxAvailable(item.AvailableAt) {
			item.State = "queued"
			item.AvailableAt = ""
			item.UpdatedAt = now()
			h.appendInboxLocked(item)
			item = h.inbox[id]
		}
		if item == nil || item.State != "queued" || seen[item.AgentID] || !inboxAvailable(item.AvailableAt) {
			continue
		}
		seen[item.AgentID] = true
		out = append(out, item.AgentID)
	}
	return out
}

func (h *Hub) deliverNextInboxForAgent(agentID string) {
	h.mu.Lock()
	meta := h.agents[agentID]
	if meta == nil || meta.Status == "running" {
		h.mu.Unlock()
		return
	}
	if rt := h.runtimes[agentID]; rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return
	}
	var item *InboxItem
	for _, id := range h.inboxOrder {
		candidate := h.inbox[id]
		if candidate != nil && candidate.AgentID == agentID && candidate.State == "queued" && inboxAvailable(candidate.AvailableAt) {
			item = candidate
			break
		}
	}
	if item == nil {
		h.mu.Unlock()
		return
	}
	message := h.messages[item.MessageID]
	address := h.addresses[item.AddressID]
	if message == nil || address == nil || !address.Enabled {
		item.State = "failed"
		item.LastError = "message or enabled address is unavailable"
		item.UpdatedAt = now()
		h.appendInboxLocked(item)
		h.mu.Unlock()
		return
	}
	membership := h.memberships[item.MembershipID]
	if item.MembershipID == "" {
		membership = h.membershipForConversationLocked(address.ID, message.Conversation.ConversationID)
		if membership != nil {
			item.MembershipID = membership.ID
		}
	}
	if conversationNeedsMembership(message.Conversation) && (membership == nil || !membership.Enabled) {
		item.State = "failed"
		item.LastError = "conversation membership is unavailable or disabled"
		item.UpdatedAt = now()
		h.appendInboxLocked(item)
		h.mu.Unlock()
		return
	}
	policy := effectiveReplyPolicy(message, address, membership)
	ts := now()
	attempt := &HandlingAttempt{
		ID: newIntegrationID("att"), InboxItemID: item.ID, AgentID: agentID, SessionID: agentID,
		Status: "starting", StartedAt: ts, EffectiveReplyPolicy: policy,
	}
	var context string
	if membership != nil {
		attempt.MembershipID = membership.ID
		attempt.MembershipVersion = membership.Version
		context = renderConversationContext(*message, *membership)
	}
	item.State = "handling"
	item.AttemptCount++
	item.ActiveAttemptID = attempt.ID
	item.LastError = ""
	item.Note = ""
	item.UpdatedAt = ts
	if err := h.st.AppendAttempt(*attempt); err != nil {
		item.State = "failed"
		item.ActiveAttemptID = ""
		item.LastError = "persist handling attempt: " + err.Error()
		h.appendInboxLocked(item)
		h.mu.Unlock()
		return
	}
	h.attempts[attempt.ID] = attempt
	h.appendInboxLocked(item)
	h.emitGlobalLocked("loom/inbox-item", map[string]any{"item": *item, "attempt": *attempt})
	envelope := formatInboxEnvelope(*message, *item, *address, policy, membership)
	itemID, attemptID := item.ID, attempt.ID
	h.mu.Unlock()

	_, err := h.sendTask(agentID, envelope, defaultInactivity, itemID, attemptID, context)
	if err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.inbox[itemID]
	currentAttempt := h.attempts[attemptID]
	if current == nil || current.State != "handling" {
		return
	}
	if isBusyErr(err) {
		current.State = "queued"
		current.ActiveAttemptID = ""
		current.LastError = "delivery raced with another turn"
	} else {
		current.State = "failed"
		current.ActiveAttemptID = ""
		current.LastError = err.Error()
	}
	current.UpdatedAt = now()
	h.appendInboxLocked(current)
	if currentAttempt != nil && (currentAttempt.Status == "starting" || currentAttempt.Status == "running") {
		currentAttempt.Status = "failed"
		currentAttempt.Error = err.Error()
		currentAttempt.CompletedAt = now()
		h.appendAttemptLocked(currentAttempt)
	}
}

func inboxAvailable(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !time.Now().Before(t)
}

func completedFinalAnswer(method string, raw json.RawMessage) string {
	if method != "item/completed" {
		return ""
	}
	var params struct {
		Item struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Phase string `json:"phase"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &params) != nil || params.Item.Type != "agentMessage" {
		return ""
	}
	if params.Item.Phase != "" && params.Item.Phase != "final_answer" {
		return ""
	}
	return strings.TrimSpace(params.Item.Text)
}

func (h *Hub) markInboxAttemptRunningLocked(turn *turnState) {
	if turn == nil || turn.attemptID == "" {
		return
	}
	attempt := h.attempts[turn.attemptID]
	if attempt == nil || attempt.Status != "starting" {
		return
	}
	attempt.Status = "running"
	attempt.TurnID = turn.turnID
	h.appendAttemptLocked(attempt)
}

func (h *Hub) finishInboxAttemptLocked(turn *turnState, status, errMsg string) {
	if turn == nil || turn.inboxItemID == "" {
		return
	}
	ts := now()
	attempt := h.attempts[turn.attemptID]
	if attempt != nil {
		attempt.Status = status
		attempt.TurnID = turn.turnID
		attempt.FinalAnswer = turn.finalAnswer
		attempt.CompletedAt = ts
		attempt.Error = errMsg
		h.appendAttemptLocked(attempt)
	}
	item := h.inbox[turn.inboxItemID]
	if item == nil || item.State != "handling" {
		return
	}
	item.ActiveAttemptID = ""
	item.UpdatedAt = ts
	if status != "completed" {
		item.State = "failed"
		item.LastError = errMsg
		if item.LastError == "" {
			item.LastError = "agent turn " + status
		}
		h.appendInboxLocked(item)
		return
	}
	address := h.addresses[item.AddressID]
	if address == nil {
		item.State = "failed"
		item.LastError = "agent address not found"
		h.appendInboxLocked(item)
		return
	}
	message := h.messages[item.MessageID]
	if message == nil {
		item.State = "failed"
		item.LastError = "inbox message not found"
		h.appendInboxLocked(item)
		return
	}
	policy := ""
	if attempt != nil {
		policy = attempt.EffectiveReplyPolicy
	}
	if policy == "" {
		policy = effectiveReplyPolicy(message, address, h.memberships[item.MembershipID])
	}
	if policy == "none" {
		item.State = "handled"
		item.Outcome = "no_reply"
		item.LastError = ""
		item.Note = ""
		h.appendInboxLocked(item)
		return
	}
	switch policy {
	case "none":
		item.State = "handled"
		item.Outcome = "no_reply"
		item.LastError = ""
		item.Note = ""
	case "final_answer":
		if strings.TrimSpace(turn.finalAnswer) == "" {
			item.State = "failed"
			item.LastError = "agent completed without a final answer"
			break
		}
		if _, err := h.createReplyOutboxLocked(item, MessageContent{Text: turn.finalAnswer}); err != nil {
			item.State = "failed"
			item.LastError = err.Error()
			break
		}
		item.State = "handled"
		item.Outcome = "reply"
		item.LastError = ""
		item.Note = ""
	default:
		item.State = "failed"
		item.LastError = "decision_missing: replyPolicy explicit requires reply or no-reply"
	}
	h.appendInboxLocked(item)
}

func (h *Hub) ReplyInboxItem(id string, p InboxActionParams) (InboxItem, OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item, err := h.inboxActionTargetLocked(id, p.Agent)
	if err != nil {
		return InboxItem{}, OutboxItem{}, err
	}
	if strings.TrimSpace(p.Content.Text) == "" && len(p.Content.Attachments) == 0 {
		return InboxItem{}, OutboxItem{}, errf(400, "reply content is empty")
	}
	if item.State == "handled" && item.Outcome == "reply" {
		if existing := h.replyOutboxLocked(item.ID); existing != nil {
			return *item, *existing, nil
		}
	}
	if item.State == "cancelled" || item.State == "handled" {
		return InboxItem{}, OutboxItem{}, errf(409, "inbox item is already %s", item.State)
	}
	outbox, err := h.createReplyOutboxLocked(item, p.Content)
	if err != nil {
		return InboxItem{}, OutboxItem{}, err
	}
	item.State = "handled"
	item.Outcome = "reply"
	item.ActiveAttemptID = ""
	item.LastError = ""
	item.Note = ""
	item.UpdatedAt = now()
	h.appendInboxLocked(item)
	return *item, outbox, nil
}

func (h *Hub) NoReplyInboxItem(id string, p InboxActionParams) (InboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item, err := h.inboxActionTargetLocked(id, p.Agent)
	if err != nil {
		return InboxItem{}, err
	}
	if h.replyOutboxLocked(item.ID) != nil {
		return InboxItem{}, errf(409, "reply already exists for inbox item")
	}
	if item.State == "handled" && item.Outcome == "no_reply" {
		return *item, nil
	}
	if item.State == "cancelled" || item.State == "handled" {
		return InboxItem{}, errf(409, "inbox item is already %s", item.State)
	}
	item.State = "handled"
	item.Outcome = "no_reply"
	item.ActiveAttemptID = ""
	item.LastError = ""
	item.Note = strings.TrimSpace(p.Reason)
	item.UpdatedAt = now()
	h.appendInboxLocked(item)
	return *item, nil
}

func (h *Hub) DeferInboxItem(id string, p InboxActionParams) (InboxItem, error) {
	until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(p.Until))
	if err != nil || !until.After(time.Now()) {
		return InboxItem{}, errf(400, "until must be a future RFC3339 timestamp")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	item, targetErr := h.inboxActionTargetLocked(id, p.Agent)
	if targetErr != nil {
		return InboxItem{}, targetErr
	}
	if item.State == "handled" || item.State == "cancelled" {
		return InboxItem{}, errf(409, "inbox item is already %s", item.State)
	}
	item.State = "deferred"
	item.Outcome = ""
	item.AvailableAt = until.UTC().Format(time.RFC3339Nano)
	item.ActiveAttemptID = ""
	item.LastError = ""
	item.Note = strings.TrimSpace(p.Reason)
	item.UpdatedAt = now()
	h.appendInboxLocked(item)
	return *item, nil
}

func (h *Hub) RetryInboxItem(id string) (InboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.inbox[id]
	if item == nil {
		return InboxItem{}, errf(404, "inbox item not found: %s", id)
	}
	if item.State != "failed" && item.State != "deferred" {
		return InboxItem{}, errf(409, "only failed or deferred inbox items can be retried")
	}
	item.State = "queued"
	item.AvailableAt = ""
	item.ActiveAttemptID = ""
	item.LastError = ""
	item.Note = ""
	item.UpdatedAt = now()
	h.appendInboxLocked(item)
	go h.deliverNextInboxForAgent(item.AgentID)
	return *item, nil
}

func (h *Hub) inboxActionTargetLocked(id, agentKey string) (*InboxItem, error) {
	item := h.inbox[id]
	if item == nil {
		return nil, errf(404, "inbox item not found: %s", id)
	}
	if strings.TrimSpace(agentKey) != "" {
		agent := h.resolveLocked(agentKey)
		if agent == nil || agent.ID != item.AgentID {
			return nil, errf(403, "agent cannot act on this inbox item")
		}
	}
	return item, nil
}

func (h *Hub) createReplyOutboxLocked(item *InboxItem, content MessageContent) (OutboxItem, error) {
	if existing := h.replyOutboxLocked(item.ID); existing != nil {
		return *existing, nil
	}
	message := h.messages[item.MessageID]
	if message == nil {
		return OutboxItem{}, errf(500, "inbox message not found: %s", item.MessageID)
	}
	address := h.addresses[item.AddressID]
	if address == nil || effectiveReplyPolicy(message, address, h.memberships[item.MembershipID]) == "none" {
		return OutboxItem{}, errf(409, "inbox item does not allow a reply")
	}
	ts := now()
	outbox := OutboxItem{
		ID: newIntegrationID("out"), AgentID: item.AgentID, AddressID: item.AddressID,
		InReplyTo: message.ID, Conversation: message.Conversation, Content: content, ResponseExpectation: "optional",
		IdempotencyKey: "reply:" + item.ID, State: "pending", CreatedAt: ts, UpdatedAt: ts,
	}
	if err := h.st.AppendOutbox(outbox); err != nil {
		return OutboxItem{}, errf(500, "persist outbox item: %s", err)
	}
	h.outbox[outbox.ID] = &outbox
	h.outboxOrder = append(h.outboxOrder, outbox.ID)
	h.emitGlobalLocked("loom/outbox-item", map[string]any{"item": outbox})
	return outbox, nil
}

func (h *Hub) replyOutboxLocked(inboxItemID string) *OutboxItem {
	key := "reply:" + inboxItemID
	for _, id := range h.outboxOrder {
		item := h.outbox[id]
		if item != nil && item.IdempotencyKey == key {
			return item
		}
	}
	return nil
}

func (h *Hub) appendInboxLocked(item *InboxItem) {
	if err := h.st.AppendInbox(*item); err != nil {
		log.Printf("[codex-loom] append inbox item: %v", err)
	}
	cp := *item
	h.inbox[item.ID] = &cp
	h.emitGlobalLocked("loom/inbox-item", map[string]any{"item": cp})
}

func (h *Hub) appendAttemptLocked(attempt *HandlingAttempt) {
	if err := h.st.AppendAttempt(*attempt); err != nil {
		log.Printf("[codex-loom] append handling attempt: %v", err)
	}
	cp := *attempt
	h.attempts[attempt.ID] = &cp
	h.emitGlobalLocked("loom/inbox-attempt", map[string]any{"attempt": cp})
}

func (h *Hub) appendOutboxLocked(item *OutboxItem) {
	if err := h.st.AppendOutbox(*item); err != nil {
		log.Printf("[codex-loom] append outbox item: %v", err)
	}
	cp := *item
	h.outbox[item.ID] = &cp
	h.emitGlobalLocked("loom/outbox-item", map[string]any{"item": cp})
}

func formatInboxEnvelope(message InboxMessage, item InboxItem, address AgentAddress, policy string, membership *ConversationMembership) string {
	var b strings.Builder
	b.WriteString(`<inbox_message version="1" id="` + xmlEscape(message.ID) + `" inbox_item_id="` + xmlEscape(item.ID) + `" expectation="` + xmlEscape(message.ResponseExpectation) + `">` + "\n")
	b.WriteString(`  <origin provider="` + xmlEscape(message.Origin) + `" address_id="` + xmlEscape(address.ID) + `" />` + "\n")
	if item.MembershipID != "" {
		b.WriteString(`  <membership id="` + xmlEscape(item.MembershipID) + `"`)
		if membership != nil {
			writeXMLAttribute(&b, "name", membership.DisplayName)
			if membership.Version > 0 {
				writeXMLAttribute(&b, "version", fmt.Sprintf("%d", membership.Version))
			}
		}
		b.WriteString(" />\n")
	}
	b.WriteString(`  <sender id="` + xmlEscape(message.Sender.ExternalID) + `">` + xmlEscape(message.Sender.DisplayName) + `</sender>` + "\n")
	b.WriteString(`  <conversation id="` + xmlEscape(message.Conversation.ConversationID) + `"`)
	if message.Conversation.ThreadID != "" {
		b.WriteString(` thread_id="` + xmlEscape(message.Conversation.ThreadID) + `"`)
	}
	writeXMLAttribute(&b, "type", message.Conversation.ConversationType)
	b.WriteString(" />\n")
	writeXMLText(&b, "reply_policy", policy)
	switch policy {
	case "final_answer":
		writeXMLText(&b, "reply_instruction", "Return the response as your final answer. The hub will deliver it to the original conversation automatically; do not call a reply command.")
	case "explicit":
		writeXMLText(&b, "reply_command", "loom inbox reply "+item.ID+" --agent "+item.AgentID+" --body \"...\"")
		writeXMLText(&b, "no_reply_command", "loom inbox no-reply "+item.ID+" --agent "+item.AgentID)
	}
	writeXMLCDATA(&b, "body", message.Content.Text)
	if len(message.Content.Attachments) > 0 {
		b.WriteString("  <attachments>\n")
		for _, attachment := range message.Content.Attachments {
			b.WriteString(`    <attachment`)
			writeXMLAttribute(&b, "id", attachment.ID)
			writeXMLAttribute(&b, "name", attachment.Name)
			writeXMLAttribute(&b, "mime_type", attachment.MimeType)
			if attachment.Size > 0 {
				writeXMLAttribute(&b, "size", fmt.Sprintf("%d", attachment.Size))
			}
			writeXMLAttribute(&b, "url", attachment.URL)
			writeXMLAttribute(&b, "path", attachment.Path)
			b.WriteString(" />\n")
		}
		b.WriteString("  </attachments>\n")
	}
	b.WriteString("</inbox_message>")
	return b.String()
}

func writeXMLAttribute(b *strings.Builder, name, value string) {
	if value != "" {
		b.WriteString(` ` + name + `="` + xmlEscape(value) + `"`)
	}
}

func effectiveReplyPolicy(message *InboxMessage, address *AgentAddress, membership *ConversationMembership) string {
	if message.ResponseExpectation == "none" || resolvedReplyPolicy(*address, membership) == "none" {
		return "none"
	}
	return resolvedReplyPolicy(*address, membership)
}

func normalizeCapabilities(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeIdentityList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func addressAllowsIngress(address AgentAddress, membership *ConversationMembership, p IngressParams) (bool, string) {
	actor := strings.TrimSpace(p.Sender.ExternalID)
	conversation := strings.TrimSpace(p.Conversation.ConversationID)
	if listContains(address.BlockActors, actor) {
		return false, "sender is blocked"
	}
	if listContains(address.BlockConversations, conversation) {
		return false, "conversation is blocked"
	}
	if len(address.AllowActors) > 0 && !listContains(address.AllowActors, actor) {
		return false, "sender is not allowlisted"
	}
	if membership == nil && len(address.AllowConversations) > 0 && !listContains(address.AllowConversations, conversation) {
		return false, "conversation is not allowlisted"
	}
	direct := p.Trigger.Direct || oneOf(strings.ToLower(strings.TrimSpace(p.Conversation.ConversationType)), "dm", "p2p", "direct")
	switch resolvedTriggerPolicy(address, membership) {
	case "all":
		return true, ""
	case "direct":
		if direct {
			return true, ""
		}
		return false, "message is not direct"
	case "mention":
		if direct || p.Trigger.Mentioned || p.Trigger.ExplicitDispatch {
			return true, ""
		}
		return false, "agent was not mentioned"
	case "explicit_dispatch":
		if p.Trigger.ExplicitDispatch {
			return true, ""
		}
		return false, "message was not explicitly dispatched"
	case "allowlist":
		if len(address.AllowActors) == 0 && len(address.AllowConversations) == 0 {
			return false, "allowlist policy has no entries"
		}
		return true, ""
	default:
		return false, "unsupported trigger policy"
	}
}

func listContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func newIntegrationID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("random id: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}
