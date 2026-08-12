package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PlatformConnection struct {
	ID              string   `json:"id"`
	Provider        string   `json:"provider"`
	AccountRef      string   `json:"accountRef,omitempty"`
	ScopeRef        string   `json:"scopeRef,omitempty"`
	CredentialRef   string   `json:"credentialRef,omitempty"`
	Status          string   `json:"status"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Cursor          string   `json:"cursor,omitempty"`
	LastEventAt     string   `json:"lastEventAt,omitempty"`
	LastHeartbeatAt string   `json:"lastHeartbeatAt,omitempty"`
	LastError       string   `json:"lastError,omitempty"`
	Enabled         bool     `json:"enabled"`
	SupersededBy    string   `json:"supersededBy,omitempty"`
	ArchivedAt      string   `json:"archivedAt,omitempty"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

var loomCLIPath = resolveLoomCLIPath()

func resolveLoomCLIPath() string {
	if configured := strings.TrimSpace(os.Getenv("CODEX_LOOM_CLI")); configured != "" {
		return configured
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "loom")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath("loom"); err == nil {
		return found
	}
	return "loom"
}

func shellCommandArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type AgentAddress struct {
	ID                 string   `json:"id"`
	AgentID            string   `json:"agentId"`
	ConnectionID       string   `json:"connectionId"`
	ExternalIdentity   string   `json:"externalIdentity"`
	DisplayName        string   `json:"displayName,omitempty"`
	TriggerPolicy      string   `json:"triggerPolicy"`
	ReplyPolicy        string   `json:"replyPolicy"`
	DMPolicy           string   `json:"dmPolicy,omitempty"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors,omitempty"`
	AllowConversations []string `json:"allowConversations,omitempty"`
	BlockActors        []string `json:"blockActors,omitempty"`
	BlockConversations []string `json:"blockConversations,omitempty"`
	Enabled            bool     `json:"enabled"`
	SupersededBy       string   `json:"supersededBy,omitempty"`
	ArchivedAt         string   `json:"archivedAt,omitempty"`
	DeletedAt          string   `json:"deletedAt,omitempty"`
	Version            int      `json:"version"`
	CreatedAt          string   `json:"createdAt"`
	UpdatedAt          string   `json:"updatedAt"`
}

// ConversationMembership describes one agent address's durable role in one
// external conversation. Behavioral context is versioned independently from
// the transport identity because one identity can participate in many groups.
type ConversationMembership struct {
	ID               string `json:"id"`
	AddressID        string `json:"addressId"`
	ConversationID   string `json:"conversationId"`
	ConversationType string `json:"conversationType,omitempty"`
	ActorID          string `json:"actorId,omitempty"`
	DisplayName      string `json:"displayName,omitempty"`
	Purpose          string `json:"purpose,omitempty"`
	Role             string `json:"role,omitempty"`
	Guidance         string `json:"guidance,omitempty"`
	TriggerPolicy    string `json:"triggerPolicy"`
	ReplyPolicy      string `json:"replyPolicy"`
	OutboundPolicy   string `json:"outboundPolicy"`
	TrustDomain      string `json:"trustDomain"`
	Enabled          bool   `json:"enabled"`
	SupersededBy     string `json:"supersededBy,omitempty"`
	ArchivedAt       string `json:"archivedAt,omitempty"`
	Version          int    `json:"version"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

// ConversationCandidate is a provider-observed conversation that an external
// identity has already joined. It is discovery state, not permission: ingress
// remains blocked until an operator creates and enables a Membership.
type ConversationCandidate struct {
	ID               string `json:"id"`
	AddressID        string `json:"addressId"`
	ConversationID   string `json:"conversationId"`
	ConversationType string `json:"conversationType"`
	DisplayName      string `json:"displayName,omitempty"`
	Description      string `json:"description,omitempty"`
	Available        bool   `json:"available"`
	FirstSeenAt      string `json:"firstSeenAt"`
	LastSeenAt       string `json:"lastSeenAt"`
	UpdatedAt        string `json:"updatedAt"`
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
	SHA256   string `json:"sha256,omitempty"`
	URL      string `json:"url,omitempty"`
	Path     string `json:"path,omitempty"`
}

type MessageContent struct {
	Text        string          `json:"text,omitempty"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
}

type ThreadContextMessage struct {
	ExternalMessageID string         `json:"externalMessageId"`
	Role              string         `json:"role,omitempty"`
	Sender            ActorRef       `json:"sender"`
	Content           MessageContent `json:"content"`
	OccurredAt        string         `json:"occurredAt,omitempty"`
	TextTruncated     bool           `json:"textTruncated,omitempty"`
}

type ThreadContext struct {
	RootExternalMessageID string                 `json:"rootExternalMessageId"`
	Messages              []ThreadContextMessage `json:"messages,omitempty"`
	Truncated             bool                   `json:"truncated,omitempty"`
	UnavailableReason     string                 `json:"unavailableReason,omitempty"`
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
	ThreadContext       *ThreadContext  `json:"threadContext,omitempty"`
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
	ID                  string                  `json:"id"`
	AgentID             string                  `json:"agentId"`
	AddressID           string                  `json:"addressId"`
	InboxItemID         string                  `json:"inboxItemId,omitempty"`
	MembershipID        string                  `json:"membershipId,omitempty"`
	InReplyTo           string                  `json:"inReplyTo,omitempty"`
	Conversation        ConversationRef         `json:"conversation"`
	Content             MessageContent          `json:"content"`
	ResponseExpectation string                  `json:"responseExpectation,omitempty"`
	IdempotencyKey      string                  `json:"idempotencyKey"`
	State               string                  `json:"state"`
	ExternalMessageID   string                  `json:"externalMessageId,omitempty"`
	ExternalMessageIDs  []string                `json:"externalMessageIds,omitempty"`
	DeliveryReceipts    []OutboxDeliveryReceipt `json:"deliveryReceipts,omitempty"`
	AttemptCount        int                     `json:"attemptCount"`
	AttemptToken        string                  `json:"attemptToken,omitempty"`
	ClaimExpiresAt      string                  `json:"claimExpiresAt,omitempty"`
	LastError           string                  `json:"lastError,omitempty"`
	CreatedAt           string                  `json:"createdAt"`
	UpdatedAt           string                  `json:"updatedAt"`
	SentAt              string                  `json:"sentAt,omitempty"`
}

// OutboxDeliveryReceipt is the provider evidence for one logical part of an
// outbound delivery. Attachment receipts preserve both Loom's stable Artifact
// ID and the provider upload ID so a sent Outbox item is auditable end to end.
type OutboxDeliveryReceipt struct {
	Kind                 string `json:"kind"`
	ArtifactID           string `json:"artifactId,omitempty"`
	ExternalMessageID    string `json:"externalMessageId,omitempty"`
	ExternalAttachmentID string `json:"externalAttachmentId,omitempty"`
}

type integrationConfig struct {
	Connections            map[string]*PlatformConnection        `json:"connections"`
	Addresses              map[string]*AgentAddress              `json:"addresses"`
	Memberships            map[string]*ConversationMembership    `json:"memberships,omitempty"`
	ConversationCandidates map[string]*ConversationCandidate     `json:"conversationCandidates,omitempty"`
	AddressOperations      map[string]*AddressLifecycleOperation `json:"addressOperations,omitempty"`
}

type ConnectionParams struct {
	Provider      string   `json:"provider"`
	AccountRef    string   `json:"accountRef"`
	ScopeRef      string   `json:"scopeRef"`
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
	DMPolicy           string   `json:"dmPolicy"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors"`
	AllowConversations []string `json:"allowConversations"`
	BlockActors        []string `json:"blockActors"`
	BlockConversations []string `json:"blockConversations"`
	Enabled            *bool    `json:"enabled"`
}

type ConversationMembershipParams struct {
	AddressID        string  `json:"addressId"`
	ConversationID   string  `json:"conversationId"`
	ConversationType *string `json:"conversationType"`
	ActorID          *string `json:"actorId"`
	DisplayName      *string `json:"displayName"`
	Purpose          *string `json:"purpose"`
	Role             *string `json:"role"`
	Guidance         *string `json:"guidance"`
	TriggerPolicy    *string `json:"triggerPolicy"`
	ReplyPolicy      *string `json:"replyPolicy"`
	OutboundPolicy   *string `json:"outboundPolicy"`
	TrustDomain      *string `json:"trustDomain"`
	Enabled          *bool   `json:"enabled"`
	ExpectedVersion  *int    `json:"expectedVersion"`
}

type IntegrationConsolidationResult struct {
	Connection             PlatformConnection `json:"connection"`
	Address                AgentAddress       `json:"address"`
	ArchivedConnectionIDs  []string           `json:"archivedConnectionIds,omitempty"`
	ArchivedAddressIDs     []string           `json:"archivedAddressIds,omitempty"`
	ArchivedMembershipIDs  []string           `json:"archivedMembershipIds,omitempty"`
	CanonicalMembershipIDs []string           `json:"canonicalMembershipIds,omitempty"`
}

type ConversationCandidateParams struct {
	ConversationID   string `json:"conversationId"`
	ConversationType string `json:"conversationType"`
	DisplayName      string `json:"displayName"`
	Description      string `json:"description"`
}

type ConversationCandidateSnapshotParams struct {
	Conversations []ConversationCandidateParams `json:"conversations"`
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
	ThreadContext       *ThreadContext  `json:"threadContext,omitempty"`
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
	Agent               string         `json:"agent"`
	Content             MessageContent `json:"content"`
	ResponseExpectation string         `json:"responseExpectation"`
	Reason              string         `json:"reason"`
	Until               string         `json:"until"`
}

type ConnectionHeartbeatParams struct {
	Status         string                         `json:"status"`
	Cursor         string                         `json:"cursor"`
	Capabilities   []string                       `json:"capabilities"`
	Error          string                         `json:"error"`
	GatewayProcess *GatewayProcessHeartbeatParams `json:"_gatewayProcess,omitempty"`
}

// GatewayProcessHeartbeatParams is private connector-to-Runtime proof
// plumbing. It is accepted only on the existing connector-authenticated
// heartbeat route and is never projected into public Connection/SSE output.
// Runtime supplies the observation timestamp on receipt.
type GatewayProcessHeartbeatParams struct {
	AttemptID        string `json:"attemptId"`
	Generation       string `json:"generation"`
	Build            string `json:"build"`
	ExecutableDigest string `json:"executableDigest"`
}

type OutboxResultParams struct {
	AttemptToken       string                  `json:"attemptToken"`
	Success            bool                    `json:"success"`
	ExternalMessageID  string                  `json:"externalMessageId"`
	ExternalMessageIDs []string                `json:"externalMessageIds"`
	DeliveryReceipts   []OutboxDeliveryReceipt `json:"deliveryReceipts"`
	Cursor             string                  `json:"cursor"`
	Error              string                  `json:"error"`
}

type OutboxParams struct {
	Agent               string          `json:"agent"`
	AddressID           string          `json:"addressId"`
	Conversation        ConversationRef `json:"conversation"`
	Content             MessageContent  `json:"content"`
	ResponseExpectation string          `json:"responseExpectation"`
	IdempotencyKey      string          `json:"idempotencyKey"`
}

type ExternalSendParams struct {
	Agent               string               `json:"agent"`
	InboxItemID         string               `json:"inboxItemId"`
	MembershipID        string               `json:"membershipId"`
	ReplyTarget         *ExternalReplyTarget `json:"replyTarget,omitempty"`
	Content             MessageContent       `json:"content"`
	ResponseExpectation string               `json:"responseExpectation"`
	IdempotencyKey      string               `json:"idempotencyKey"`
}

// ExternalReplyTarget preserves a provider-native message or thread location
// inside the Conversation authorized by a Membership. It never selects the
// Conversation, Address, or external identity itself.
type ExternalReplyTarget struct {
	MessageID string `json:"messageId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
}

type ConnectorCommand struct {
	Type              string             `json:"type"`
	Connection        PlatformConnection `json:"connection"`
	Address           AgentAddress       `json:"address"`
	OutboxItem        OutboxItem         `json:"outboxItem,omitempty"`
	ProviderOperation *ProviderOperation `json:"providerOperation,omitempty"`
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
	if cfg.ConversationCandidates != nil {
		h.conversationCandidates = cfg.ConversationCandidates
	}
	if cfg.AddressOperations != nil {
		h.addressOperations = cfg.AddressOperations
	}
	changed := h.normalizeAddressLifecycleLocked()
	changed = h.migrateAllowedConversationsLocked() || changed
	h.integrationNormalizationPending = changed
	return nil
}

func (h *Hub) persistIntegrationsLocked() error {
	return h.st.SaveIntegrations(integrationConfig{
		Connections: h.connections, Addresses: h.addresses, Memberships: h.memberships,
		ConversationCandidates: h.conversationCandidates, AddressOperations: h.addressOperations,
	})
}

func (h *Hub) loadInboxState() error {
	staleInbox := map[string]bool{}
	staleAttempts := map[string]bool{}
	staleOutbox := map[string]bool{}
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
		if previous := h.inbox[item.ID]; previous != nil && inboxStateTerminal(previous.State) && !inboxStateTerminal(item.State) {
			staleInbox[item.ID] = true
			return
		}
		h.inbox[item.ID] = &item
		staleInbox[item.ID] = false
	}); err != nil {
		return err
	}
	// Each NDJSON file is an object history. Recovery applies only after the
	// latest record per ID has been projected; repairing historical handling
	// records would append stale queued work after a later handled record.
	for id, current := range h.inbox {
		item := *current
		repaired := false
		if staleInbox[id] {
			item.UpdatedAt = now()
			repaired = true
		}
		if item.State == "handling" {
			// A process loss makes the old handling attempt uncertain. Hold the
			// durable Inbox item for an explicit Continue instead of replaying an
			// external message automatically after every restart.
			item.State = "interrupted"
			item.ActiveAttemptID = ""
			item.LastError = "CodexLoom restarted during handling"
			item.UpdatedAt = now()
			repaired = true
		}
		if item.Note == "" && item.LastError != "" && (item.State == "deferred" || item.State == "handled" && item.Outcome == "no_reply") {
			item.Note = item.LastError
			item.LastError = ""
			repaired = true
		}
		if repaired {
			if !h.passive {
				if err := h.st.AppendInbox(item); err != nil {
					return fmt.Errorf("persist recovered inbox item %s: %w", item.ID, err)
				}
			}
			copy := item
			h.inbox[id] = &copy
		}
	}
	if err := h.st.ReadAttempts(func(raw json.RawMessage) {
		var attempt HandlingAttempt
		if json.Unmarshal(raw, &attempt) != nil || attempt.ID == "" {
			return
		}
		if previous := h.attempts[attempt.ID]; previous != nil && attemptTerminalRank(previous.Status) > attemptTerminalRank(attempt.Status) {
			staleAttempts[attempt.ID] = true
			return
		}
		h.attempts[attempt.ID] = &attempt
		staleAttempts[attempt.ID] = false
	}); err != nil {
		return err
	}
	for id, current := range h.attempts {
		attempt := *current
		repaired := staleAttempts[id]
		if attempt.AgentID == "" {
			attempt.AgentID = attempt.SessionID
			repaired = true
		}
		if attempt.SessionID == "" {
			attempt.SessionID = attempt.AgentID
			repaired = true
		}
		if attempt.Status == "starting" || attempt.Status == "running" {
			attempt.Status = "interrupted"
			attempt.Error = "CodexLoom restarted during handling"
			attempt.CompletedAt = now()
			repaired = true
		}
		if repaired {
			if !h.passive {
				if err := h.st.AppendAttempt(attempt); err != nil {
					return fmt.Errorf("persist recovered inbox attempt %s: %w", attempt.ID, err)
				}
			}
			copy := attempt
			h.attempts[id] = &copy
		}
	}
	if err := h.st.ReadOutbox(func(raw json.RawMessage) {
		var item OutboxItem
		if json.Unmarshal(raw, &item) != nil || item.ID == "" {
			return
		}
		if _, exists := h.outbox[item.ID]; !exists {
			h.outboxOrder = append(h.outboxOrder, item.ID)
		}
		if previous := h.outbox[item.ID]; previous != nil && previous.State == "sent" && item.State != "sent" {
			staleOutbox[item.ID] = true
			return
		}
		h.outbox[item.ID] = &item
		staleOutbox[item.ID] = false
	}); err != nil {
		return err
	}
	for id, current := range h.outbox {
		item := *current
		if staleOutbox[id] {
			item.UpdatedAt = now()
			if !h.passive {
				if err := h.st.AppendOutbox(item); err != nil {
					return fmt.Errorf("persist terminal outbox item %s: %w", item.ID, err)
				}
			}
			copy := item
			h.outbox[id] = &copy
			continue
		}
		if item.State == "sending" && (item.AttemptToken == "" || item.ClaimExpiresAt == "") {
			item.State = "pending"
			item.AttemptToken = ""
			item.ClaimExpiresAt = ""
			item.LastError = "recovered legacy delivery claim after CodexLoom restart"
			item.UpdatedAt = now()
			if !h.passive {
				if err := h.st.AppendOutbox(item); err != nil {
					return fmt.Errorf("persist recovered outbox item %s: %w", item.ID, err)
				}
			}
			copy := item
			h.outbox[id] = &copy
		}
	}
	var reconciledInbox []InboxItem
	for _, outbox := range h.outbox {
		item := h.inbox[outbox.InboxItemID]
		if item == nil || item.Outcome != "reply" {
			continue
		}
		previousState, previousError := item.State, item.LastError
		switch outbox.State {
		case "sent":
			item.State, item.LastError = "handled", ""
		case "failed":
			item.State, item.LastError = "failed", "delivery_failed: "+strings.TrimSpace(outbox.LastError)
		}
		if item.State != previousState || item.LastError != previousError {
			item.UpdatedAt = now()
			reconciledInbox = append(reconciledInbox, *item)
		}
	}
	for _, item := range reconciledInbox {
		if !h.passive {
			if err := h.st.AppendInbox(item); err != nil {
				return fmt.Errorf("persist reconciled inbox item %s: %w", item.ID, err)
			}
		}
	}
	return nil
}

func inboxStateTerminal(state string) bool {
	return state == "handled" || state == "cancelled"
}

func attemptTerminalRank(status string) int {
	switch status {
	case "completed":
		return 3
	case "failed", "cancelled":
		return 2
	case "interrupted":
		return 1
	default:
		return 0
	}
}

func (h *Hub) CreateConnection(p ConnectionParams) (PlatformConnection, error) {
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	if provider == "" {
		return PlatformConnection{}, errf(400, "provider is required")
	}
	credentialRef := strings.TrimSpace(p.CredentialRef)
	if credentialRef != "" && !validCredentialRef(credentialRef) {
		return PlatformConnection{}, errf(400, "credentialRef must use env:, keychain:, or managed:")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	ts := now()
	connection := PlatformConnection{
		ID: newIntegrationID("conn"), Provider: provider, AccountRef: strings.TrimSpace(p.AccountRef), ScopeRef: strings.TrimSpace(p.ScopeRef),
		CredentialRef: credentialRef, Status: "disconnected", Capabilities: normalizeCapabilities(p.Capabilities),
		Enabled: enabled, CreatedAt: ts, UpdatedAt: ts,
	}
	unlock := h.gatewayCoordinatorForUse().lock(connection.ID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return PlatformConnection{}, err
	}
	h.connections[connection.ID] = &connection
	if err := h.persistIntegrationsLocked(); err != nil {
		delete(h.connections, connection.ID)
		return PlatformConnection{}, errf(500, "save integration: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": connection})
	return clonePlatformConnectionValue(connection), nil
}

func (h *Hub) ListConnections() []PlatformConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]PlatformConnection, 0, len(h.connections))
	for _, connection := range h.connections {
		h.applyGatewayHealthProjectionLocked(connection.ID)
		out = append(out, clonePlatformConnectionValue(*connection))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// RollbackCreatedIntegration removes resources created by a failed provider
// provisioning attempt. Callers must only pass IDs that did not exist before
// that attempt; existing integration state is never modified here.
func (h *Hub) RollbackCreatedIntegration(connectionIDs, addressIDs []string) error {
	unlock := h.lockGatewayMutationScope(connectionIDs, addressIDs)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	lockedConnectionIDs := normalizeGatewayConnectionIDs(append(append([]string(nil), connectionIDs...), h.gatewayConnectionIDsForAddressesLocked(addressIDs)...))
	nextConnections := cloneConnections(h.connections)
	nextAddresses := cloneAddresses(h.addresses)
	nextMemberships := cloneMemberships(h.memberships)
	nextCandidates := cloneConversationCandidates(h.conversationCandidates)
	addressSet := make(map[string]struct{}, len(addressIDs))
	for _, id := range addressIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		addressSet[id] = struct{}{}
		delete(nextAddresses, id)
	}
	for id, membership := range nextMemberships {
		if membership != nil {
			if _, remove := addressSet[membership.AddressID]; remove {
				delete(nextMemberships, id)
			}
		}
	}
	for id, candidate := range nextCandidates {
		if candidate != nil {
			if _, remove := addressSet[candidate.AddressID]; remove {
				delete(nextCandidates, id)
			}
		}
	}
	for _, id := range connectionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		for _, address := range nextAddresses {
			if address != nil && address.ConnectionID == id {
				return errf(409, "cannot roll back connection %s while an address still references it", id)
			}
		}
		if control := h.gatewayState.Controls[id]; control != nil && control.Lifecycle != gatewayBindingProvisioning {
			return errf(409, "cannot roll back adopted Gateway connection %s", id)
		}
		delete(nextConnections, id)
	}
	ticket, err := h.prepareGatewayMutationLocked(lockedConnectionIDs...)
	if err != nil {
		return err
	}
	previousConnections, previousAddresses := h.connections, h.addresses
	previousMemberships, previousCandidates := h.memberships, h.conversationCandidates
	h.connections, h.addresses = nextConnections, nextAddresses
	h.memberships, h.conversationCandidates = nextMemberships, nextCandidates
	if err := h.persistIntegrationsLocked(); err != nil {
		h.connections, h.addresses = previousConnections, previousAddresses
		h.memberships, h.conversationCandidates = previousMemberships, previousCandidates
		return errf(500, "save integration rollback: %s", err)
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return err
	}
	h.emitGlobalLocked("loom/integration-rollback", map[string]any{"connections": connectionIDs, "addresses": addressIDs})
	return nil
}

// RestoreIntegrationResources puts existing resources back to their exact
// pre-provisioning values after a failed provider migration.
func (h *Hub) RestoreIntegrationResources(connections []PlatformConnection, addresses []AgentAddress) error {
	connectionIDs := make([]string, 0, len(connections))
	addressIDs := make([]string, 0, len(addresses))
	for i := range connections {
		connectionIDs = append(connectionIDs, connections[i].ID)
	}
	for i := range addresses {
		addressIDs = append(addressIDs, addresses[i].ID)
		connectionIDs = append(connectionIDs, addresses[i].ConnectionID)
	}
	unlock := h.lockGatewayMutationScope(connectionIDs, addressIDs)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	nextConnections := cloneConnections(h.connections)
	nextAddresses := cloneAddresses(h.addresses)
	for i := range connections {
		connection := connections[i]
		if _, exists := nextConnections[connection.ID]; exists {
			copy := clonePlatformConnectionValue(connection)
			nextConnections[connection.ID] = &copy
		}
	}
	for i := range addresses {
		address := addresses[i]
		if _, exists := nextAddresses[address.ID]; exists {
			copy := cloneAgentAddressValue(address)
			nextAddresses[address.ID] = &copy
		}
	}
	lockedConnectionIDs := normalizeGatewayConnectionIDs(append(connectionIDs, h.gatewayConnectionIDsForAddressesLocked(addressIDs)...))
	ticket, err := h.prepareGatewayMutationLocked(lockedConnectionIDs...)
	if err != nil {
		return err
	}
	previousConnections, previousAddresses := h.connections, h.addresses
	h.connections, h.addresses = nextConnections, nextAddresses
	if err := h.persistIntegrationsLocked(); err != nil {
		h.connections, h.addresses = previousConnections, previousAddresses
		return errf(500, "save integration restore: %s", err)
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return err
	}
	h.emitGlobalLocked("loom/integration-restored", map[string]any{"connections": connections, "addresses": addresses})
	return nil
}

// ConsolidateIntegrationIdentity archives duplicate transport records while
// keeping them available for historical Inbox/Outbox resolution. Missing or
// newer Membership configuration is projected onto the canonical Address.
func (h *Hub) ConsolidateIntegrationIdentity(canonicalConnectionID, canonicalAddressID string, duplicateConnectionIDs, duplicateAddressIDs []string) (IntegrationConsolidationResult, error) {
	lockedConnectionIDs := append([]string{canonicalConnectionID}, duplicateConnectionIDs...)
	lockedAddressIDs := append([]string{canonicalAddressID}, duplicateAddressIDs...)
	unlock := h.lockGatewayMutationScope(lockedConnectionIDs, lockedAddressIDs)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()

	canonicalConnectionID = strings.TrimSpace(canonicalConnectionID)
	canonicalAddressID = strings.TrimSpace(canonicalAddressID)
	connection := h.connections[canonicalConnectionID]
	address := h.addresses[canonicalAddressID]
	if connection == nil || address == nil || address.ConnectionID != connection.ID {
		return IntegrationConsolidationResult{}, errf(404, "canonical integration identity not found")
	}
	if connection.ArchivedAt != "" || address.ArchivedAt != "" {
		return IntegrationConsolidationResult{}, errf(409, "canonical integration identity is archived")
	}

	connectionIDs := normalizeOrderedStrings(duplicateConnectionIDs)
	addressIDs := normalizeOrderedStrings(duplicateAddressIDs)
	retiredConnections := map[string]bool{}
	retiredAddresses := map[string]bool{}
	for _, id := range connectionIDs {
		if id == connection.ID {
			continue
		}
		candidate := h.connections[id]
		if candidate == nil || candidate.Provider != connection.Provider {
			return IntegrationConsolidationResult{}, errf(409, "duplicate connection %s is incompatible", id)
		}
		retiredConnections[id] = true
	}
	for _, id := range addressIDs {
		if id == address.ID {
			continue
		}
		candidate := h.addresses[id]
		if candidate == nil || candidate.AgentID != address.AgentID || candidate.ExternalIdentity != address.ExternalIdentity {
			return IntegrationConsolidationResult{}, errf(409, "duplicate address %s is incompatible", id)
		}
		candidateConnection := h.connections[candidate.ConnectionID]
		if candidateConnection == nil || candidateConnection.Provider != connection.Provider {
			return IntegrationConsolidationResult{}, errf(409, "duplicate address %s uses an incompatible connection", id)
		}
		retiredAddresses[id] = true
		if candidate.ConnectionID != connection.ID {
			retiredConnections[candidate.ConnectionID] = true
		}
	}
	for id := range retiredConnections {
		for _, candidate := range h.addresses {
			if candidate != nil && candidate.ConnectionID == id && candidate.ArchivedAt == "" && !retiredAddresses[candidate.ID] {
				return IntegrationConsolidationResult{}, errf(409, "connection %s also serves address %s", id, candidate.ID)
			}
		}
	}
	affectedConnectionIDs := append([]string{connection.ID}, connectionIDs...)
	for id := range retiredConnections {
		affectedConnectionIDs = append(affectedConnectionIDs, id)
	}
	ticket, err := h.prepareGatewayMutationLocked(affectedConnectionIDs...)
	if err != nil {
		return IntegrationConsolidationResult{}, err
	}

	connections := cloneConnections(h.connections)
	addresses := cloneAddresses(h.addresses)
	memberships := cloneMemberships(h.memberships)
	candidates := cloneConversationCandidates(h.conversationCandidates)
	ts := now()
	result := IntegrationConsolidationResult{Connection: *connections[connection.ID], Address: *addresses[address.ID]}

	canonicalMemberships := map[string]*ConversationMembership{}
	retiredMemberships := make([]*ConversationMembership, 0)
	for _, membership := range memberships {
		if membership != nil && membership.AddressID == address.ID && membership.ArchivedAt == "" {
			canonicalMemberships[membership.ConversationID] = membership
		}
		if membership != nil && retiredAddresses[membership.AddressID] && membership.ArchivedAt == "" {
			retiredMemberships = append(retiredMemberships, membership)
		}
	}
	for _, membership := range retiredMemberships {
		target := canonicalMemberships[membership.ConversationID]
		if target == nil {
			copy := *membership
			copy.ID = stableMembershipID(address.ID, membership.ConversationID)
			copy.AddressID = address.ID
			copy.SupersededBy = ""
			copy.ArchivedAt = ""
			copy.UpdatedAt = ts
			if copy.Version < 1 {
				copy.Version = 1
			}
			memberships[copy.ID] = &copy
			target = &copy
			canonicalMemberships[copy.ConversationID] = target
		} else if membershipPreferredForConsolidation(membership, target) {
			id, addressID, createdAt := target.ID, target.AddressID, target.CreatedAt
			version := maxInt(target.Version, membership.Version) + 1
			*target = *membership
			target.ID, target.AddressID, target.CreatedAt = id, addressID, createdAt
			target.Version, target.UpdatedAt = version, ts
			target.SupersededBy, target.ArchivedAt = "", ""
		}
		membership.Enabled = false
		membership.SupersededBy = target.ID
		membership.ArchivedAt = ts
		membership.UpdatedAt = ts
		membership.Version++
		result.ArchivedMembershipIDs = append(result.ArchivedMembershipIDs, membership.ID)
		result.CanonicalMembershipIDs = append(result.CanonicalMembershipIDs, target.ID)
	}

	canonicalCandidates := map[string]*ConversationCandidate{}
	retiredCandidates := make([]*ConversationCandidate, 0)
	for _, candidate := range candidates {
		if candidate != nil && candidate.AddressID == address.ID {
			canonicalCandidates[candidate.ConversationID] = candidate
		}
		if candidate != nil && retiredAddresses[candidate.AddressID] {
			retiredCandidates = append(retiredCandidates, candidate)
		}
	}
	for _, candidate := range retiredCandidates {
		target := canonicalCandidates[candidate.ConversationID]
		if target == nil {
			copy := *candidate
			copy.ID = stableConversationCandidateID(address.ID, candidate.ConversationID)
			copy.AddressID = address.ID
			candidates[copy.ID] = &copy
			target = &copy
			canonicalCandidates[copy.ConversationID] = target
		} else if candidate.LastSeenAt > target.LastSeenAt {
			target.DisplayName = candidate.DisplayName
			target.Description = candidate.Description
			target.ConversationType = candidate.ConversationType
			target.Available = candidate.Available
			target.LastSeenAt = candidate.LastSeenAt
			target.UpdatedAt = ts
		}
		candidate.Available = false
		candidate.UpdatedAt = ts
	}

	for id := range retiredAddresses {
		candidate := addresses[id]
		candidate.Enabled = false
		candidate.SupersededBy = address.ID
		candidate.ArchivedAt = ts
		candidate.UpdatedAt = ts
		candidate.Version++
		result.ArchivedAddressIDs = append(result.ArchivedAddressIDs, id)
	}
	for id := range retiredConnections {
		candidate := connections[id]
		candidate.Enabled = false
		candidate.Status = "disconnected"
		candidate.SupersededBy = connection.ID
		candidate.ArchivedAt = ts
		candidate.UpdatedAt = ts
		result.ArchivedConnectionIDs = append(result.ArchivedConnectionIDs, id)
	}

	oldConnections, oldAddresses := h.connections, h.addresses
	oldMemberships, oldCandidates := h.memberships, h.conversationCandidates
	h.connections, h.addresses = connections, addresses
	h.memberships, h.conversationCandidates = memberships, candidates
	if err := h.persistIntegrationsLocked(); err != nil {
		h.connections, h.addresses = oldConnections, oldAddresses
		h.memberships, h.conversationCandidates = oldMemberships, oldCandidates
		return IntegrationConsolidationResult{}, errf(500, "save integration consolidation: %s", err)
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return IntegrationConsolidationResult{}, err
	}
	sort.Strings(result.ArchivedConnectionIDs)
	sort.Strings(result.ArchivedAddressIDs)
	sort.Strings(result.ArchivedMembershipIDs)
	result.CanonicalMembershipIDs = normalizeOrderedStrings(result.CanonicalMembershipIDs)
	result.Connection = clonePlatformConnectionValue(*h.connections[connection.ID])
	result.Address = cloneAgentAddressValue(*h.addresses[address.ID])
	h.emitGlobalLocked("loom/integration-consolidated", result)
	return result, nil
}

func membershipPreferredForConsolidation(candidate, current *ConversationMembership) bool {
	if candidate.Enabled != current.Enabled {
		return candidate.Enabled
	}
	if left, right := membershipConfigurationScore(candidate), membershipConfigurationScore(current); left != right {
		return left > right
	}
	return candidate.UpdatedAt > current.UpdatedAt
}

func membershipConfigurationScore(value *ConversationMembership) int {
	if value == nil {
		return 0
	}
	score := 0
	for _, field := range []string{value.Purpose, value.Role, value.Guidance} {
		if strings.TrimSpace(field) != "" {
			score += 4
		}
	}
	for _, field := range []string{value.DisplayName, value.ActorID} {
		if strings.TrimSpace(field) != "" {
			score++
		}
	}
	if normalizeOutboundPolicy(value.OutboundPolicy) != "reply_only" {
		score++
	}
	return score
}

func cloneConnections(values map[string]*PlatformConnection) map[string]*PlatformConnection {
	out := make(map[string]*PlatformConnection, len(values))
	for id, value := range values {
		if value != nil {
			copy := *value
			copy.Capabilities = append([]string(nil), value.Capabilities...)
			out[id] = &copy
		}
	}
	return out
}

func clonePlatformConnectionValue(value PlatformConnection) PlatformConnection {
	value.Capabilities = append([]string(nil), value.Capabilities...)
	return value
}

func cloneAgentAddressValue(value AgentAddress) AgentAddress {
	value.AllowActors = append([]string(nil), value.AllowActors...)
	value.AllowConversations = append([]string(nil), value.AllowConversations...)
	value.BlockActors = append([]string(nil), value.BlockActors...)
	value.BlockConversations = append([]string(nil), value.BlockConversations...)
	return value
}

func cloneAddresses(values map[string]*AgentAddress) map[string]*AgentAddress {
	out := make(map[string]*AgentAddress, len(values))
	for id, value := range values {
		if value != nil {
			copy := *value
			copy.AllowActors = append([]string(nil), value.AllowActors...)
			copy.AllowConversations = append([]string(nil), value.AllowConversations...)
			copy.BlockActors = append([]string(nil), value.BlockActors...)
			copy.BlockConversations = append([]string(nil), value.BlockConversations...)
			out[id] = &copy
		}
	}
	return out
}

func cloneMemberships(values map[string]*ConversationMembership) map[string]*ConversationMembership {
	out := make(map[string]*ConversationMembership, len(values))
	for id, value := range values {
		if value != nil {
			copy := *value
			out[id] = &copy
		}
	}
	return out
}

func cloneConversationCandidates(values map[string]*ConversationCandidate) map[string]*ConversationCandidate {
	out := make(map[string]*ConversationCandidate, len(values))
	for id, value := range values {
		if value != nil {
			copy := *value
			out[id] = &copy
		}
	}
	return out
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (h *Hub) UpdateConnection(id string, p ConnectionParams) (PlatformConnection, error) {
	unlock := h.gatewayCoordinatorForUse().lock(id)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[strings.TrimSpace(id)]
	if connection == nil {
		return PlatformConnection{}, errf(404, "connection not found: %s", id)
	}
	if connection.ArchivedAt != "" {
		return PlatformConnection{}, errf(409, "archived connection is superseded by %s", connection.SupersededBy)
	}
	next := *connection
	next.Capabilities = append([]string(nil), connection.Capabilities...)
	if value := strings.TrimSpace(p.Provider); value != "" {
		next.Provider = strings.ToLower(value)
	}
	if p.AccountRef != "" {
		next.AccountRef = strings.TrimSpace(p.AccountRef)
	}
	if p.ScopeRef != "" {
		next.ScopeRef = strings.TrimSpace(p.ScopeRef)
	}
	if p.CredentialRef != "" {
		value := strings.TrimSpace(p.CredentialRef)
		if !validCredentialRef(value) {
			return PlatformConnection{}, errf(400, "credentialRef must use env:, keychain:, or managed:")
		}
		next.CredentialRef = value
	}
	if p.Capabilities != nil {
		next.Capabilities = normalizeCapabilities(p.Capabilities)
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
		if !next.Enabled {
			next.Status = "disconnected"
		}
	}
	next.UpdatedAt = now()
	ticket, err := h.prepareGatewayMutationLocked(next.ID)
	if err != nil {
		return PlatformConnection{}, err
	}
	h.connections[next.ID] = &next
	if err := h.persistIntegrationsLocked(); err != nil {
		h.connections[next.ID] = connection
		return PlatformConnection{}, errf(500, "save integration: %s", err)
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return PlatformConnection{}, err
	}
	next = *h.connections[next.ID]
	next.Capabilities = append([]string(nil), h.connections[next.ID].Capabilities...)
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": next})
	return clonePlatformConnectionValue(next), nil
}

func (h *Hub) HeartbeatConnection(id string, p ConnectionHeartbeatParams) (PlatformConnection, error) {
	unlock := h.gatewayCoordinatorForUse().lock(id)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[id]
	if connection == nil {
		return PlatformConnection{}, errf(404, "connection not found: %s", id)
	}
	if connection.ArchivedAt != "" {
		return PlatformConnection{}, errf(409, "connection is archived and superseded by %s", connection.SupersededBy)
	}
	next := *connection
	next.Capabilities = append([]string(nil), connection.Capabilities...)
	status := strings.TrimSpace(p.Status)
	if status == "" {
		status = "connected"
	}
	if !oneOf(status, "disconnected", "connecting", "connected", "degraded") {
		return PlatformConnection{}, errf(400, "invalid connection status %q", status)
	}
	ts := now()
	if p.GatewayProcess != nil {
		if status != "connected" || strings.TrimSpace(p.Error) != "" {
			return PlatformConnection{}, errf(409, "Gateway process proof requires a connected heartbeat")
		}
		observedNow, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return PlatformConnection{}, errf(500, "Runtime heartbeat clock is invalid")
		}
		proof := gatewayProcessProof{
			AttemptID: strings.TrimSpace(p.GatewayProcess.AttemptID), Generation: strings.TrimSpace(p.GatewayProcess.Generation),
			Build: strings.TrimSpace(p.GatewayProcess.Build), ExecutableDigest: strings.TrimSpace(p.GatewayProcess.ExecutableDigest), ObservedAt: ts,
		}
		if _, err := h.acceptGatewayProcessProofLocked(next.ID, proof, observedNow, p.Cursor, p.Capabilities); err != nil {
			return PlatformConnection{}, err
		}
		projected := h.connections[next.ID]
		if projected == nil {
			return PlatformConnection{}, errf(404, "connection not found: %s", id)
		}
		copy := *projected
		copy.Capabilities = append([]string(nil), projected.Capabilities...)
		h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": copy})
		return copy, nil
	}
	if handled, err := h.recordGatewayObservationLocked(next.ID, status, p.Error, ts, ts, p.Cursor, "", p.Capabilities); handled {
		if err != nil {
			return PlatformConnection{}, errf(500, "persist Gateway observation: %s", err)
		}
		projected := h.connections[next.ID]
		if projected == nil {
			return PlatformConnection{}, errf(404, "connection not found: %s", id)
		}
		copy := *projected
		copy.Capabilities = append([]string(nil), projected.Capabilities...)
		h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": copy})
		return copy, nil
	}
	next.Status = status
	next.LastHeartbeatAt = ts
	next.UpdatedAt = ts
	if p.Cursor != "" {
		next.Cursor = p.Cursor
	}
	if p.Capabilities != nil {
		next.Capabilities = normalizeCapabilities(p.Capabilities)
	}
	next.LastError = strings.TrimSpace(p.Error)
	h.connections[next.ID] = &next
	if err := h.persistIntegrationsLocked(); err != nil {
		h.connections[next.ID] = connection
		return PlatformConnection{}, errf(500, "save connection heartbeat: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": next})
	return clonePlatformConnectionValue(next), nil
}

func (h *Hub) MarkConnectionDisconnected(id, reason string) {
	unlock := h.gatewayCoordinatorForUse().lock(id)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[id]
	if connection == nil {
		return
	}
	if handled, err := h.recordGatewayObservationLocked(connection.ID, "disconnected", reason, now(), "", "", "", nil); handled {
		if err != nil {
			log.Printf("[codex-loom] persist disconnected Gateway observation %s: %v", id, err)
			return
		}
		h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": *connection})
		return
	}
	previous := *connection
	connection.Status = "disconnected"
	connection.LastError = strings.TrimSpace(reason)
	connection.UpdatedAt = now()
	if err := h.persistIntegrationsLocked(); err != nil {
		*connection = previous
		log.Printf("[codex-loom] persist disconnected connection %s: %v", id, err)
		return
	}
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
	dmPolicy := normalizeDMPolicy(p.DMPolicy)
	if !oneOf(dmPolicy, "open", "managed", "closed") {
		return AgentAddress{}, errf(400, "invalid dmPolicy %q", dmPolicy)
	}
	trustDomain := strings.TrimSpace(p.TrustDomain)
	if trustDomain == "" {
		trustDomain = "local"
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}

	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil {
		return AgentAddress{}, errf(404, "connection not found: %s", connectionID)
	}
	if connection.ArchivedAt != "" {
		return AgentAddress{}, errf(409, "connection is archived and superseded by %s", connection.SupersededBy)
	}
	agent := h.resolveLocked(p.Agent)
	if agent == nil {
		return AgentAddress{}, errf(404, "agent not found: %s", p.Agent)
	}
	for _, address := range h.addresses {
		if address.DeletedAt == "" && address.ConnectionID == connectionID && address.ExternalIdentity == externalIdentity {
			return AgentAddress{}, errf(409, "external identity is already bound to %s", address.AgentID)
		}
		if address.DeletedAt == "" && address.AgentID == agent.ID && address.TrustDomain != trustDomain {
			return AgentAddress{}, errf(409, "agent addresses must share trustDomain %q", address.TrustDomain)
		}
	}
	ts := now()
	address := AgentAddress{
		ID: newIntegrationID("addr"), AgentID: agent.ID, ConnectionID: connectionID,
		ExternalIdentity: externalIdentity, DisplayName: strings.TrimSpace(p.DisplayName),
		TriggerPolicy: trigger, ReplyPolicy: reply, DMPolicy: dmPolicy, TrustDomain: trustDomain,
		AllowActors: normalizeIdentityList(p.AllowActors), AllowConversations: normalizeIdentityList(p.AllowConversations),
		BlockActors: normalizeIdentityList(p.BlockActors), BlockConversations: normalizeIdentityList(p.BlockConversations),
		Enabled: enabled, Version: 1, CreatedAt: ts, UpdatedAt: ts,
	}
	ticket, err := h.prepareGatewayMutationLocked(connectionID)
	if err != nil {
		return AgentAddress{}, err
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
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return AgentAddress{}, err
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": address})
	return cloneAgentAddressValue(address), nil
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
			out = append(out, cloneAgentAddressValue(*address))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (h *Hub) UpdateAddress(id string, p AddressParams) (AgentAddress, error) {
	unlock := h.lockGatewayMutationScope(nil, []string{id})
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	address := h.addresses[id]
	if address == nil {
		return AgentAddress{}, errf(404, "agent address not found: %s", id)
	}
	if address.DeletedAt != "" {
		return AgentAddress{}, errf(409, "address was deleted at %s", address.DeletedAt)
	}
	if address.ArchivedAt != "" {
		return AgentAddress{}, errf(409, "archived address requires an explicit managed restore")
	}
	next := *address
	next.AllowActors = append([]string(nil), address.AllowActors...)
	next.AllowConversations = append([]string(nil), address.AllowConversations...)
	next.BlockActors = append([]string(nil), address.BlockActors...)
	next.BlockConversations = append([]string(nil), address.BlockConversations...)
	if value := strings.TrimSpace(p.ExternalIdentity); value != "" && value != next.ExternalIdentity {
		for otherID, other := range h.addresses {
			if otherID != id && other.DeletedAt == "" && other.ConnectionID == next.ConnectionID && other.ExternalIdentity == value {
				return AgentAddress{}, errf(409, "external identity is already bound to %s", other.AgentID)
			}
		}
		next.ExternalIdentity = value
	}
	if value := strings.TrimSpace(p.DisplayName); value != "" {
		next.DisplayName = value
	}
	if value := strings.TrimSpace(p.TriggerPolicy); value != "" {
		if !oneOf(value, "direct", "mention", "explicit_dispatch", "all", "allowlist") {
			return AgentAddress{}, errf(400, "invalid triggerPolicy %q", value)
		}
		next.TriggerPolicy = value
	}
	if value := strings.TrimSpace(p.ReplyPolicy); value != "" {
		if !oneOf(value, "explicit", "final_answer", "none") {
			return AgentAddress{}, errf(400, "invalid replyPolicy %q", value)
		}
		next.ReplyPolicy = value
	}
	if value := strings.TrimSpace(p.DMPolicy); value != "" {
		value = normalizeDMPolicy(value)
		if !oneOf(value, "open", "managed", "closed") {
			return AgentAddress{}, errf(400, "invalid dmPolicy %q", value)
		}
		next.DMPolicy = value
	}
	trustChanged := false
	if value := strings.TrimSpace(p.TrustDomain); value != "" && value != next.TrustDomain {
		for otherID, other := range h.addresses {
			if otherID != id && other.DeletedAt == "" && other.AgentID == next.AgentID && other.TrustDomain != value {
				return AgentAddress{}, errf(409, "agent addresses must share trustDomain %q", other.TrustDomain)
			}
		}
		next.TrustDomain = value
		trustChanged = true
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if p.AllowActors != nil {
		next.AllowActors = normalizeIdentityList(p.AllowActors)
	}
	if p.AllowConversations != nil {
		next.AllowConversations = normalizeIdentityList(p.AllowConversations)
	}
	if p.BlockActors != nil {
		next.BlockActors = normalizeIdentityList(p.BlockActors)
	}
	if p.BlockConversations != nil {
		next.BlockConversations = normalizeIdentityList(p.BlockConversations)
	}
	next.UpdatedAt = now()
	next.Version++
	ticket, err := h.prepareGatewayMutationLocked(address.ConnectionID)
	if err != nil {
		return AgentAddress{}, err
	}
	previousMemberships := h.memberships
	h.memberships = cloneMemberships(h.memberships)
	h.addresses[id] = &next
	h.ensureAllowedConversationMembershipsLocked(&next)
	updatedMemberships := []ConversationMembership{}
	if trustChanged {
		for _, membership := range h.memberships {
			if membership == nil || membership.AddressID != next.ID {
				continue
			}
			membership.TrustDomain = next.TrustDomain
			membership.Version++
			membership.UpdatedAt = next.UpdatedAt
			updatedMemberships = append(updatedMemberships, *membership)
		}
	}
	if err := h.persistIntegrationsLocked(); err != nil {
		h.addresses[id] = address
		h.memberships = previousMemberships
		return AgentAddress{}, errf(500, "save agent address: %s", err)
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return AgentAddress{}, err
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": next})
	for _, membership := range updatedMemberships {
		h.emitGlobalLocked("loom/conversation-membership", map[string]any{"membership": membership})
	}
	return cloneAgentAddressValue(next), nil
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
	threadContext, err := normalizeInboundThreadContext(p.ThreadContext, p.Conversation.ThreadID, externalID)
	if err != nil {
		return IngressResult{}, err
	}

	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[connectionID]
	if connection == nil || !connection.Enabled {
		return IngressResult{}, errf(404, "enabled connection not found: %s", connectionID)
	}
	address := h.addresses[addressID]
	if address == nil || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" || address.ConnectionID != connectionID {
		return IngressResult{}, errf(404, "enabled address not found for connection: %s", addressID)
	}
	if h.agents[address.AgentID] == nil {
		return IngressResult{}, errf(404, "address target agent not found: %s", address.AgentID)
	}
	externalKey := connectionID + ":" + externalID
	if messageID := h.externalMessages[externalKey]; messageID != "" {
		message := h.messages[messageID]
		item := h.findInboxForMessageAddressLocked(messageID, address.ID)
		if item == nil {
			item = h.findInboxForMessageLocked(messageID, address.AgentID)
		}
		if item == nil {
			item = h.findInboxForMessageLocked(messageID, "")
		}
		if message != nil && item != nil {
			messageCopy, itemCopy := *message, *item
			pendingAccess := item.State == "pending_access"
			return IngressResult{Message: &messageCopy, InboxItem: &itemCopy, Duplicate: true, Ignored: pendingAccess, Reason: item.Note}, nil
		}
	}
	membership := h.membershipForConversationLocked(address.ID, strings.TrimSpace(p.Conversation.ConversationID))
	disabledMembership := membership != nil && !membership.Enabled
	if disabledMembership {
		membership = nil
	}
	direct := conversationIsDirect(p.Conversation, p.Trigger)
	dmPolicy := normalizeDMPolicy(address.DMPolicy)
	if membership != nil && direct && membership.ConversationType != "dm" {
		return h.ignoreIngressLocked(connection, address, externalID, "conversation is not configured as a direct message"), nil
	}
	if membership != nil && conversationNeedsMembership(p.Conversation) && membership.ConversationType == "dm" {
		return h.ignoreIngressLocked(connection, address, externalID, "conversation is not configured as a group"), nil
	}
	if direct && dmPolicy == "closed" {
		return h.ignoreIngressLocked(connection, address, externalID, "direct messages are closed"), nil
	}
	if direct && disabledMembership {
		return h.ignoreIngressLocked(connection, address, externalID, "direct message contact is paused"), nil
	}
	pendingAccess := direct && dmPolicy == "managed" && membership == nil
	if conversationNeedsMembership(p.Conversation) && membership == nil {
		return h.ignoreIngressLocked(connection, address, externalID, "group has no enabled conversation membership"), nil
	}
	if !pendingAccess {
		if membership != nil && membership.ConversationType == "dm" && membership.ActorID != "" && membership.ActorID != strings.TrimSpace(p.Sender.ExternalID) {
			return h.ignoreIngressLocked(connection, address, externalID, "direct message sender does not match configured contact"), nil
		}
		if allowed, reason := addressAllowsIngress(*address, membership, p); !allowed {
			return h.ignoreIngressLocked(connection, address, externalID, reason), nil
		}
	}
	ts := now()
	conversation := p.Conversation
	conversation.Provider = connection.Provider
	conversation.ConnectionID = connectionID
	message := InboxMessage{
		ID: newIntegrationID("imsg"), Origin: connection.Provider, ExternalKey: externalKey,
		ExternalEventID: strings.TrimSpace(p.ExternalEventID), ExternalMessageID: externalID,
		Sender: p.Sender, Conversation: conversation, Content: p.Content, ThreadContext: threadContext,
		ReplyTo: strings.TrimSpace(p.ReplyTo), ResponseExpectation: expectation,
		OccurredAt: strings.TrimSpace(p.OccurredAt), ReceivedAt: ts, ProviderMetadata: p.ProviderMetadata,
	}
	message.Sender.Provider = connection.Provider
	message.Sender.ConnectionID = connectionID
	if message.ThreadContext != nil {
		for i := range message.ThreadContext.Messages {
			message.ThreadContext.Messages[i].Sender.Provider = connection.Provider
			message.ThreadContext.Messages[i].Sender.ConnectionID = connectionID
		}
	}
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
	if pendingAccess {
		item.State = "pending_access"
		item.Note = "Direct message access requires approval"
	}
	if membership != nil {
		item.MembershipID = membership.ID
	}
	if err := h.st.AppendInbox(item); err != nil {
		return IngressResult{}, errf(500, "persist inbox item: %s", err)
	}
	h.inbox[item.ID] = &item
	h.inboxOrder = append(h.inboxOrder, item.ID)
	previousConnection := *connection
	connection.LastEventAt = ts
	connection.UpdatedAt = ts
	rawStatus, rawError := h.rawGatewayObservationLocked(connection.ID, connection.Status, connection.LastError)
	if handled, observationErr := h.recordGatewayObservationLocked(connection.ID, rawStatus, rawError, ts, "", connection.Cursor, ts, nil); handled {
		if observationErr != nil {
			*connection = previousConnection
			log.Printf("[codex-loom] persist Gateway ingress observation %s: %v", item.ID, observationErr)
		}
	} else if err := h.persistIntegrationsLocked(); err != nil {
		*connection = previousConnection
		log.Printf("[codex-loom] persist connection cursor after inbox %s: %v", item.ID, err)
	}
	h.emitGlobalLocked("loom/inbox-message", map[string]any{"message": message, "inboxItem": item})
	if pendingAccess {
		return IngressResult{Message: &message, InboxItem: &item, Ignored: true, Reason: item.Note}, nil
	}
	return IngressResult{Message: &message, InboxItem: &item}, nil
}

func normalizeInboundThreadContext(value *ThreadContext, conversationThreadID, currentExternalMessageID string) (*ThreadContext, error) {
	if value == nil {
		return nil, nil
	}
	rootID := strings.TrimSpace(value.RootExternalMessageID)
	expectedRootID := strings.TrimSpace(conversationThreadID)
	if rootID == "" {
		rootID = expectedRootID
	}
	if expectedRootID == "" || rootID == "" || rootID != expectedRootID {
		return nil, errf(400, "thread context root must match conversation threadId")
	}
	if len(value.Messages) > 16 {
		return nil, errf(400, "thread context supports at most 16 messages")
	}
	out := &ThreadContext{
		RootExternalMessageID: rootID,
		Truncated:             value.Truncated,
		UnavailableReason:     strings.TrimSpace(value.UnavailableReason),
	}
	if len(out.UnavailableReason) > 1024 {
		return nil, errf(400, "thread context unavailableReason is too long")
	}
	totalText := 0
	seen := map[string]bool{}
	for _, input := range value.Messages {
		messageID := strings.TrimSpace(input.ExternalMessageID)
		if messageID == "" {
			return nil, errf(400, "thread context message requires externalMessageId")
		}
		if messageID == strings.TrimSpace(currentExternalMessageID) {
			return nil, errf(400, "thread context must not include the current message")
		}
		if seen[messageID] {
			return nil, errf(400, "thread context contains duplicate message %q", messageID)
		}
		seen[messageID] = true
		role := strings.ToLower(strings.TrimSpace(input.Role))
		if role == "" {
			if messageID == rootID {
				role = "root"
			} else {
				role = "reply"
			}
		}
		if !oneOf(role, "root", "reply") {
			return nil, errf(400, "thread context message role must be root or reply")
		}
		if (role == "root") != (messageID == rootID) {
			return nil, errf(400, "thread context root role must match rootExternalMessageId")
		}
		content := input.Content
		content.Text = strings.TrimSpace(content.Text)
		if content.Text == "" && len(content.Attachments) == 0 {
			continue
		}
		totalText += len(content.Text)
		if totalText > 64<<10 {
			return nil, errf(400, "thread context text exceeds 64 KiB")
		}
		input.ExternalMessageID = messageID
		input.Role = role
		input.Sender.ExternalID = strings.TrimSpace(input.Sender.ExternalID)
		input.Sender.DisplayName = strings.TrimSpace(input.Sender.DisplayName)
		input.Content = content
		input.OccurredAt = strings.TrimSpace(input.OccurredAt)
		out.Messages = append(out.Messages, input)
	}
	return out, nil
}

func (h *Hub) ignoreIngressLocked(connection *PlatformConnection, address *AgentAddress, externalID, reason string) IngressResult {
	previous := *connection
	connection.LastEventAt = now()
	connection.UpdatedAt = connection.LastEventAt
	rawStatus, rawError := h.rawGatewayObservationLocked(connection.ID, connection.Status, connection.LastError)
	if handled, observationErr := h.recordGatewayObservationLocked(connection.ID, rawStatus, rawError, connection.LastEventAt, "", connection.Cursor, connection.LastEventAt, nil); handled {
		if observationErr != nil {
			*connection = previous
			log.Printf("[codex-loom] persist ignored Gateway ingress for connection %s: %v", connection.ID, observationErr)
		}
	} else if err := h.persistIntegrationsLocked(); err != nil {
		*connection = previous
		log.Printf("[codex-loom] persist ignored ingress for connection %s: %v", connection.ID, err)
	}
	h.emitGlobalLocked("loom/inbox-ignored", map[string]any{
		"connectionId": connection.ID, "addressId": address.ID,
		"externalMessageId": externalID, "reason": reason,
	})
	return IngressResult{Ignored: true, Reason: reason}
}

func (h *Hub) findInboxForMessageLocked(messageID, agentID string) *InboxItem {
	for _, id := range h.inboxOrder {
		item := h.inbox[id]
		if item != nil && item.MessageID == messageID && (agentID == "" || item.AgentID == agentID) {
			return item
		}
	}
	return nil
}

func (h *Hub) findInboxForMessageAddressLocked(messageID, addressID string) *InboxItem {
	for _, id := range h.inboxOrder {
		item := h.inbox[id]
		if item != nil && item.MessageID == messageID && item.AddressID == addressID {
			return item
		}
	}
	return nil
}
