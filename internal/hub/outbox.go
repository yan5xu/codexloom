package hub

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// Outbox owns governed external delivery intent, Connector claims, and
// completion fencing. Inbound handling may create Outbox items, but delivery
// state transitions remain isolated here so a Connector cannot mutate Inbox
// state except through the explicit reconciliation step.
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

func (h *Hub) GetOutbox(id string) (OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.outbox[strings.TrimSpace(id)]
	if item == nil {
		return OutboxItem{}, errf(404, "outbox item not found: %s", id)
	}
	return *item, nil
}

func (h *Hub) SendExternal(p ExternalSendParams) (OutboxItem, error) {
	inboxItemID := strings.TrimSpace(p.InboxItemID)
	membershipID := strings.TrimSpace(p.MembershipID)
	if (inboxItemID == "") == (membershipID == "") {
		return OutboxItem{}, errf(400, "exactly one of inboxItemId or membershipId is required")
	}
	if inboxItemID != "" {
		_, outbox, err := h.ReplyInboxItem(inboxItemID, InboxActionParams{
			Agent: p.Agent, Content: p.Content, ResponseExpectation: p.ResponseExpectation,
		})
		return outbox, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	agent := h.resolveLocked(strings.TrimSpace(p.Agent))
	if agent == nil {
		return OutboxItem{}, errf(404, "agent not found: %s", p.Agent)
	}
	membership := h.memberships[membershipID]
	if membership == nil || !membership.Enabled {
		return OutboxItem{}, errf(404, "enabled conversation membership not found: %s", membershipID)
	}
	if normalizeOutboundPolicy(membership.OutboundPolicy) != "proactive" {
		return OutboxItem{}, errf(403, "conversation membership %s does not allow proactive sends", membershipID)
	}
	address := h.addresses[membership.AddressID]
	if address == nil || !address.Enabled || address.AgentID != agent.ID {
		return OutboxItem{}, errf(403, "agent does not own the enabled address for membership %s", membershipID)
	}
	connection := h.connections[address.ConnectionID]
	if connection == nil || !connection.Enabled {
		return OutboxItem{}, errf(404, "enabled integration connection not found")
	}
	if !hasCapability(connection.Capabilities, "proactive_send") {
		return OutboxItem{}, errf(409, "%s connection does not support proactive sends", connection.Provider)
	}
	if len(p.Content.Attachments) > 0 && !hasCapability(connection.Capabilities, "attachments") {
		return OutboxItem{}, errf(409, "%s connection does not support attachments", connection.Provider)
	}
	key := strings.TrimSpace(p.IdempotencyKey)
	if key == "" {
		return OutboxItem{}, errf(400, "idempotencyKey is required for proactive sends")
	}
	for _, item := range h.outbox {
		if item != nil && item.IdempotencyKey == key {
			if item.AgentID != agent.ID || item.MembershipID != membership.ID {
				return OutboxItem{}, errf(409, "idempotencyKey is already used by another delivery")
			}
			return *item, nil
		}
	}
	content, err := h.normalizeOutboundContentLocked(p.Content)
	if err != nil {
		return OutboxItem{}, err
	}
	expectation := strings.TrimSpace(p.ResponseExpectation)
	if expectation == "" {
		expectation = "none"
	}
	if !oneOf(expectation, "required", "optional", "none") {
		return OutboxItem{}, errf(400, "invalid responseExpectation %q", expectation)
	}
	ts := now()
	item := OutboxItem{
		ID: newIntegrationID("out"), AgentID: agent.ID, AddressID: address.ID, MembershipID: membership.ID,
		Conversation: ConversationRef{
			Provider: connection.Provider, ConnectionID: connection.ID, ConversationID: membership.ConversationID,
			ConversationType: membership.ConversationType,
		},
		Content: content, ResponseExpectation: expectation, IdempotencyKey: key,
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
	content, err := h.normalizeOutboundContentLocked(p.Content)
	if err != nil {
		return OutboxItem{}, err
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
		Content: content, ResponseExpectation: expectation, IdempotencyKey: key,
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
	currentTime := time.Now().UTC()
	for _, id := range h.outboxOrder {
		item := h.outbox[id]
		if item == nil || item.State != "sending" {
			continue
		}
		address := h.addresses[item.AddressID]
		if address == nil || address.ConnectionID != connectionID {
			continue
		}
		if !outboxClaimExpired(item, currentTime) {
			continue
		}
		next := *item
		next.State = "pending"
		next.AttemptToken = ""
		next.ClaimExpiresAt = ""
		next.LastError = "delivery claim expired before result"
		next.UpdatedAt = now()
		if err := h.commitOutboxLocked(next); err != nil {
			log.Printf("[codex-loom] requeue expired outbox %s: %v", next.ID, err)
		}
	}
}

func (h *Hub) ClaimNextOutbox(connectionID string) (*ConnectorCommand, error) {
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
	for _, id := range h.outboxOrder {
		item := h.outbox[id]
		if item != nil && item.State == "sending" {
			address := h.addresses[item.AddressID]
			if address != nil && address.ConnectionID == connectionID && outboxClaimExpired(item, currentTime) {
				next := *item
				next.State = "pending"
				next.AttemptToken = ""
				next.ClaimExpiresAt = ""
				next.LastError = "delivery claim expired before result"
				next.UpdatedAt = now()
				if err := h.commitOutboxLocked(next); err != nil {
					return nil, errf(500, "requeue expired outbox item: %s", err)
				}
				item = h.outbox[id]
			}
		}
		if item == nil || item.State != "pending" {
			continue
		}
		address := h.addresses[item.AddressID]
		if address == nil || !address.Enabled || address.ConnectionID != connectionID {
			continue
		}
		next := *item
		next.State = "sending"
		next.AttemptCount++
		next.AttemptToken = newIntegrationID("claim")
		next.ClaimExpiresAt = currentTime.Add(connectorClaimLease).Format(time.RFC3339Nano)
		next.LastError = ""
		next.UpdatedAt = now()
		if err := h.commitOutboxLocked(next); err != nil {
			return nil, errf(500, "persist outbox claim: %s", err)
		}
		return &ConnectorCommand{Type: "send", Connection: *connection, Address: *address, OutboxItem: next}, nil
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
		if err := h.reconcileInboxDeliveryLocked(item); err != nil {
			return OutboxItem{}, errf(500, "persist delivered inbox state: %s", err)
		}
		return *item, nil
	}
	if item.State != "sending" {
		return OutboxItem{}, errf(409, "outbox item %s is not currently claimed", id)
	}
	if strings.TrimSpace(p.AttemptToken) == "" || p.AttemptToken != item.AttemptToken {
		return OutboxItem{}, errf(409, "stale or missing outbox attempt token")
	}
	if p.Success {
		if err := validateAttachmentReceipts(*item, p.DeliveryReceipts); err != nil {
			p.Success = false
			p.Error = err.Error()
		}
	}
	ts := now()
	next := *item
	next.UpdatedAt = ts
	next.ClaimExpiresAt = ""
	next.DeliveryReceipts = normalizeDeliveryReceipts(append(next.DeliveryReceipts, p.DeliveryReceipts...))
	receiptMessageIDs := make([]string, 0, len(p.DeliveryReceipts))
	for _, receipt := range p.DeliveryReceipts {
		receiptMessageIDs = append(receiptMessageIDs, receipt.ExternalMessageID)
	}
	if p.Success {
		next.State = "sent"
		resultIDs := append(append(p.ExternalMessageIDs, p.ExternalMessageID), receiptMessageIDs...)
		next.ExternalMessageIDs = normalizeOrderedStrings(append(next.ExternalMessageIDs, resultIDs...))
		if len(next.ExternalMessageIDs) > 0 {
			next.ExternalMessageID = next.ExternalMessageIDs[0]
		}
		next.SentAt = ts
		next.LastError = ""
	} else {
		next.State = "failed"
		resultIDs := append(append(p.ExternalMessageIDs, p.ExternalMessageID), receiptMessageIDs...)
		next.ExternalMessageIDs = normalizeOrderedStrings(append(next.ExternalMessageIDs, resultIDs...))
		if next.ExternalMessageID == "" && len(next.ExternalMessageIDs) > 0 {
			next.ExternalMessageID = next.ExternalMessageIDs[0]
		}
		next.LastError = strings.TrimSpace(p.Error)
		if next.LastError == "" {
			next.LastError = "connector send failed"
		}
	}
	if err := h.commitOutboxLocked(next); err != nil {
		return OutboxItem{}, errf(500, "persist outbox result: %s", err)
	}
	if err := h.reconcileInboxDeliveryLocked(&next); err != nil {
		return OutboxItem{}, errf(500, "persist inbox delivery result: %s", err)
	}
	if connection := h.connections[connectionID]; connection != nil {
		previous := *connection
		if p.Cursor != "" {
			connection.Cursor = p.Cursor
		}
		connection.LastEventAt = ts
		connection.UpdatedAt = ts
		if !p.Success {
			connection.Status = "degraded"
			connection.LastError = next.LastError
		}
		if err := h.persistIntegrationsLocked(); err != nil {
			*connection = previous
			log.Printf("[codex-loom] persist connection result for outbox %s: %v", next.ID, err)
		}
	}
	return next, nil
}

func validateAttachmentReceipts(item OutboxItem, receipts []OutboxDeliveryReceipt) error {
	if len(item.Content.Attachments) == 0 {
		return nil
	}
	confirmed := map[string]bool{}
	for _, receipt := range receipts {
		if strings.EqualFold(strings.TrimSpace(receipt.Kind), "attachment") &&
			strings.TrimSpace(receipt.ArtifactID) != "" && strings.TrimSpace(receipt.ExternalAttachmentID) != "" {
			confirmed[strings.TrimSpace(receipt.ArtifactID)] = true
		}
	}
	missing := make([]string, 0)
	for _, attachment := range item.Content.Attachments {
		artifactID := strings.TrimSpace(attachment.ID)
		if artifactID == "" || !confirmed[artifactID] {
			if artifactID == "" {
				artifactID = strings.TrimSpace(attachment.Name)
			}
			missing = append(missing, artifactID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("connector reported success without attachment delivery evidence for %s", strings.Join(missing, ", "))
	}
	return nil
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
	next := *item
	next.State = "pending"
	next.AttemptToken = ""
	next.ClaimExpiresAt = ""
	next.LastError = ""
	next.UpdatedAt = now()
	if err := h.commitOutboxLocked(next); err != nil {
		return OutboxItem{}, errf(500, "persist outbox retry: %s", err)
	}
	if inbox := h.inbox[item.InboxItemID]; inbox != nil {
		nextInbox := *inbox
		nextInbox.State = "awaiting_delivery"
		nextInbox.LastError = ""
		nextInbox.UpdatedAt = now()
		if err := h.commitInboxLocked(nextInbox); err != nil {
			return next, errf(500, "persist inbox delivery retry: %s", err)
		}
	}
	return next, nil
}

func (h *Hub) reconcileInboxDeliveryLocked(outbox *OutboxItem) error {
	if outbox == nil || strings.TrimSpace(outbox.InboxItemID) == "" {
		return nil
	}
	item := h.inbox[outbox.InboxItemID]
	if item == nil || item.Outcome != "reply" {
		return nil
	}
	next := *item
	switch outbox.State {
	case "sent":
		next.State = "handled"
		next.LastError = ""
	case "failed":
		next.State = "failed"
		next.LastError = "delivery_failed: " + strings.TrimSpace(outbox.LastError)
	default:
		if next.State != "handling" {
			next.State = "awaiting_delivery"
		}
		return nil
	}
	next.UpdatedAt = now()
	return h.commitInboxLocked(next)
}
