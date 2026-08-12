package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

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
		entry := InboxEntry{Item: *item, Message: *message, Address: cloneAgentAddressValue(*address), AgentName: agentName}
		membership := h.memberships[item.MembershipID]
		if membership == nil {
			membership = h.membershipForConversationLocked(address.ID, message.Conversation.ConversationID)
		}
		if membership != nil {
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
			lastError := msg.LastDeliveryError
			if msg.LastHandlingError != "" {
				lastError = msg.LastHandlingError
			}
			item := InboxItem{
				ID: "loom:" + msg.ID, AgentID: msg.ToAgentID, MessageID: msg.ID,
				AddressID: "loom", State: itemState, Outcome: outcome,
				AttemptCount: len(msg.HandlingAttempts), LastError: lastError, CreatedAt: createdAt, UpdatedAt: msg.UpdatedAt,
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
	if msg.Resolution == "cancelled" || msg.Resolution == "completed_elsewhere" || msg.Resolution == "superseded" {
		return "handled", msg.Resolution
	}
	if msg.DeliveryStatus == "failed" {
		return "failed", ""
	}
	if msg.DeliveryStatus == "cancelled" {
		return "handled", "cancelled"
	}
	if msg.DeliveryStatus == "queued" || msg.DeliveryStatus == "delivering" {
		return "queued", ""
	}
	if msg.Resolution == "reply" || msg.Status == "answered" {
		return "handled", "reply"
	}
	if msg.Resolution == "no_reply" || msg.Response == "none" {
		return "handled", "no_reply"
	}
	if msg.HandlingStatus == "interrupted" {
		return "interrupted", ""
	}
	if msg.HandlingStatus == "failed" {
		return "failed", ""
	}
	return "handling", ""
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
		if item != nil && item.State == "pending_delegation" {
			next := h.reconcilePendingDelegationSnapshotLocked(*item)
			if err := h.commitInboxLocked(next); err != nil {
				log.Printf("[codex-loom] reconcile pending delegation %s: %v", next.ID, err)
				continue
			}
			item = h.inbox[id]
		}
		if item != nil && item.State == "deferred" && inboxAvailable(item.AvailableAt) {
			next := *item
			next.State = "queued"
			next.AvailableAt = ""
			next.UpdatedAt = now()
			if err := h.commitInboxLocked(next); err != nil {
				log.Printf("[codex-loom] activate deferred inbox item %s: %v", next.ID, err)
				continue
			}
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

func (h *Hub) reconcilePendingDelegationSnapshotLocked(item InboxItem) InboxItem {
	source := h.inbox[item.DelegatedFromInboxItemID]
	if source != nil && source.State == "handled" && source.Outcome == "delegated" && source.DelegatedToInboxItemID == item.ID {
		item.State = "queued"
		item.LastError = ""
	} else {
		item.State = "cancelled"
		item.Note = "incomplete delegation was cancelled during recovery"
	}
	item.ActiveAttemptID = ""
	item.UpdatedAt = now()
	return item
}

func (h *Hub) deliverNextInboxForAgent(agentID string) {
	h.mu.Lock()
	if h.isDrainingLocked() || h.providerSwitching {
		h.mu.Unlock()
		return
	}
	meta := h.agents[agentID]
	if meta == nil || meta.Status == "running" || h.activeGoalReservesThreadLocked(agentID) {
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
	if message == nil || address == nil || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" || address.AgentID != agentID {
		next := *item
		next.State = "failed"
		next.LastError = "message or enabled address is unavailable"
		next.UpdatedAt = now()
		if err := h.commitInboxLocked(next); err != nil {
			log.Printf("[codex-loom] fail unavailable inbox item %s: %v", next.ID, err)
		}
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
	if conversationIsDirect(message.Conversation, TriggerEvidence{}) && normalizeDMPolicy(address.DMPolicy) == "closed" {
		next := *item
		next.State = "failed"
		next.LastError = "direct messages are closed"
		next.UpdatedAt = now()
		if err := h.commitInboxLocked(next); err != nil {
			log.Printf("[codex-loom] fail closed direct inbox item %s: %v", next.ID, err)
		}
		h.mu.Unlock()
		return
	}
	if conversationRequiresMembership(*address, message.Conversation) && (membership == nil || !membership.Enabled) {
		next := *item
		next.State = "failed"
		next.LastError = "conversation membership is unavailable or disabled"
		next.UpdatedAt = now()
		if err := h.commitInboxLocked(next); err != nil {
			log.Printf("[codex-loom] fail unconfigured inbox item %s: %v", next.ID, err)
		}
		h.mu.Unlock()
		return
	}
	policy := effectiveReplyPolicy(message, address, membership)
	delegationTargets := []string(nil)
	if policy == "explicit" {
		delegationTargets = h.availableDelegationTargetsLocked(item, message)
	}
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
	nextItem := *item
	nextItem.State = "handling"
	nextItem.AttemptCount++
	nextItem.ActiveAttemptID = attempt.ID
	nextItem.LastError = ""
	nextItem.Note = ""
	nextItem.UpdatedAt = ts
	if err := h.st.AppendAttempt(*attempt); err != nil {
		failed := *item
		failed.State = "failed"
		failed.ActiveAttemptID = ""
		failed.LastError = "persist handling attempt: " + err.Error()
		failed.UpdatedAt = now()
		if commitErr := h.commitInboxLocked(failed); commitErr != nil {
			log.Printf("[codex-loom] persist inbox failure after attempt write failure: %v", commitErr)
		}
		h.mu.Unlock()
		return
	}
	attemptCopy := *attempt
	h.attempts[attempt.ID] = &attemptCopy
	h.emitGlobalLocked("loom/inbox-attempt", map[string]any{"attempt": attemptCopy})
	if err := h.commitInboxLocked(nextItem); err != nil {
		failedAttempt := attemptCopy
		failedAttempt.Status = "failed"
		failedAttempt.Error = "persist handling inbox state: " + err.Error()
		failedAttempt.CompletedAt = now()
		if commitErr := h.commitAttemptLocked(failedAttempt); commitErr != nil {
			log.Printf("[codex-loom] persist failed handling attempt %s: %v", failedAttempt.ID, commitErr)
		}
		h.mu.Unlock()
		return
	}
	currentTime := now()
	envelope := formatInboxEnvelopeContextWithDelegationTargetsAt(*message, nextItem, *address, policy, membership, currentTime, delegationTargets)
	displayEnvelope := formatInboxEnvelopeWithDelegationTargetsAt(*message, nextItem, *address, policy, membership, currentTime, delegationTargets)
	originalInput := message.Content.Text
	if strings.TrimSpace(originalInput) == "" && len(message.Content.Attachments) > 0 {
		originalInput = "Review the attached external message files."
	}
	itemID, attemptID := nextItem.ID, attempt.ID
	h.mu.Unlock()

	workContext := envelope
	if strings.TrimSpace(context) != "" {
		workContext = context + "\n" + envelope
	}
	source := externalBusinessContext("inbox_message", itemID, workContext)
	source.DisplayText = displayEnvelope
	_, err := h.sendTaskWithContext(agentID, originalInput, nil, defaultInactivity, itemID, attemptID, "", "", "", source)
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
	nextCurrent := *current
	if isBusyErr(err) {
		nextCurrent.State = "queued"
		nextCurrent.ActiveAttemptID = ""
		nextCurrent.LastError = "delivery raced with another turn"
	} else {
		nextCurrent.State = "failed"
		nextCurrent.ActiveAttemptID = ""
		nextCurrent.LastError = err.Error()
	}
	nextCurrent.UpdatedAt = now()
	if commitErr := h.commitInboxLocked(nextCurrent); commitErr != nil {
		log.Printf("[codex-loom] persist inbox delivery failure %s: %v", nextCurrent.ID, commitErr)
	}
	if currentAttempt != nil && (currentAttempt.Status == "starting" || currentAttempt.Status == "running") {
		nextAttempt := *currentAttempt
		nextAttempt.Status = "failed"
		nextAttempt.Error = err.Error()
		nextAttempt.CompletedAt = now()
		if commitErr := h.commitAttemptLocked(nextAttempt); commitErr != nil {
			log.Printf("[codex-loom] persist handling attempt failure %s: %v", nextAttempt.ID, commitErr)
		}
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
	next := *attempt
	next.Status = "running"
	next.TurnID = turn.turnID
	if err := h.commitAttemptLocked(next); err != nil {
		log.Printf("[codex-loom] persist running inbox attempt %s: %v", next.ID, err)
	}
}

func (h *Hub) finishInboxAttemptLocked(turn *turnState, status, errMsg string) {
	if turn == nil || turn.inboxItemID == "" {
		return
	}
	ts := now()
	attempt := h.attempts[turn.attemptID]
	if attempt != nil {
		nextAttempt := *attempt
		nextAttempt.Status = status
		nextAttempt.TurnID = turn.turnID
		nextAttempt.FinalAnswer = turn.finalAnswer
		nextAttempt.CompletedAt = ts
		nextAttempt.Error = errMsg
		if err := h.commitAttemptLocked(nextAttempt); err != nil {
			log.Printf("[codex-loom] persist completed inbox attempt %s: %v", nextAttempt.ID, err)
		}
	}
	item := h.inbox[turn.inboxItemID]
	if item == nil || item.State != "handling" {
		return
	}
	nextItem := *item
	nextItem.ActiveAttemptID = ""
	nextItem.UpdatedAt = ts
	if status != "completed" {
		nextItem.State = "failed"
		nextItem.LastError = errMsg
		if nextItem.LastError == "" {
			nextItem.LastError = "agent turn " + status
		}
		if err := h.commitInboxLocked(nextItem); err != nil {
			log.Printf("[codex-loom] persist failed inbox turn %s: %v", nextItem.ID, err)
		}
		return
	}
	address := h.addresses[item.AddressID]
	if address == nil {
		nextItem.State = "failed"
		nextItem.LastError = "agent address not found"
		if err := h.commitInboxLocked(nextItem); err != nil {
			log.Printf("[codex-loom] persist missing-address inbox %s: %v", nextItem.ID, err)
		}
		return
	}
	message := h.messages[item.MessageID]
	if message == nil {
		nextItem.State = "failed"
		nextItem.LastError = "inbox message not found"
		if err := h.commitInboxLocked(nextItem); err != nil {
			log.Printf("[codex-loom] persist missing-message inbox %s: %v", nextItem.ID, err)
		}
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
		nextItem.State = "handled"
		nextItem.Outcome = "no_reply"
		nextItem.LastError = ""
		nextItem.Note = ""
		if err := h.commitInboxLocked(nextItem); err != nil {
			log.Printf("[codex-loom] persist no-reply inbox %s: %v", nextItem.ID, err)
		}
		return
	}
	switch policy {
	case "none":
		nextItem.State = "handled"
		nextItem.Outcome = "no_reply"
		nextItem.LastError = ""
		nextItem.Note = ""
	case "final_answer":
		if strings.TrimSpace(turn.finalAnswer) == "" {
			nextItem.State = "failed"
			nextItem.LastError = "agent completed without a final answer"
			break
		}
		if _, err := h.createReplyOutboxLocked(item, MessageContent{Text: turn.finalAnswer}, "optional"); err != nil {
			nextItem.State = "failed"
			nextItem.LastError = err.Error()
			break
		}
		nextItem.State = "awaiting_delivery"
		nextItem.Outcome = "reply"
		nextItem.LastError = ""
		nextItem.Note = ""
	default:
		nextItem.State = "failed"
		nextItem.LastError = "decision_missing: replyPolicy explicit requires reply or no-reply"
	}
	if err := h.commitInboxLocked(nextItem); err != nil {
		log.Printf("[codex-loom] persist finished inbox item %s: %v", nextItem.ID, err)
	}
}

func (h *Hub) ReplyInboxItem(id string, p InboxActionParams) (InboxItem, OutboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item, err := h.inboxActionTargetLocked(id, p.Agent)
	if err != nil {
		return InboxItem{}, OutboxItem{}, err
	}
	if (item.State == "awaiting_delivery" || item.State == "handled") && item.Outcome == "reply" {
		if existing := h.replyOutboxLocked(item.ID); existing != nil {
			return *item, *existing, nil
		}
	}
	if item.State == "cancelled" || item.State == "handled" {
		return InboxItem{}, OutboxItem{}, errf(409, "inbox item is already %s", item.State)
	}
	content, err := h.normalizeOutboundContentLocked(p.Content)
	if err != nil {
		return InboxItem{}, OutboxItem{}, err
	}
	outbox, err := h.createReplyOutboxLocked(item, content, p.ResponseExpectation)
	if err != nil {
		return InboxItem{}, OutboxItem{}, err
	}
	next := *item
	next.State = "awaiting_delivery"
	next.Outcome = "reply"
	next.ActiveAttemptID = ""
	next.LastError = ""
	next.Note = ""
	next.UpdatedAt = now()
	if err := h.commitInboxLocked(next); err != nil {
		return InboxItem{}, OutboxItem{}, errf(500, "persist inbox reply decision: %s", err)
	}
	return next, outbox, nil
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
	next := *item
	next.State = "handled"
	next.Outcome = "no_reply"
	next.ActiveAttemptID = ""
	next.LastError = ""
	next.Note = strings.TrimSpace(p.Reason)
	next.UpdatedAt = now()
	if err := h.commitInboxLocked(next); err != nil {
		return InboxItem{}, errf(500, "persist no-reply decision: %s", err)
	}
	return next, nil
}

func (h *Hub) DelegateInboxItem(id string, p InboxDelegationParams) (InboxItem, InboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	source, err := h.inboxActionTargetLocked(id, p.Agent)
	if err != nil {
		return InboxItem{}, InboxItem{}, err
	}
	targetAgent := h.resolveLocked(strings.TrimSpace(p.To))
	if targetAgent == nil {
		return InboxItem{}, InboxItem{}, errf(404, "delegation target agent not found: %s", p.To)
	}
	if source.AgentID == targetAgent.ID {
		return InboxItem{}, InboxItem{}, errf(400, "delegation target must be a different agent")
	}
	if source.DelegatedFromInboxItemID != "" {
		return InboxItem{}, InboxItem{}, errf(409, "delegated inbox items cannot be delegated again")
	}
	if source.State == "handled" && source.Outcome == "delegated" && source.DelegatedToInboxItemID != "" {
		target := h.inbox[source.DelegatedToInboxItemID]
		if target == nil {
			return InboxItem{}, InboxItem{}, errf(500, "delegated inbox target is missing: %s", source.DelegatedToInboxItemID)
		}
		if target.AgentID != targetAgent.ID {
			return InboxItem{}, InboxItem{}, errf(409, "inbox item was already delegated to another agent")
		}
		if target.State == "pending_delegation" {
			nextTarget := *target
			nextTarget.State = "queued"
			nextTarget.LastError = ""
			nextTarget.UpdatedAt = now()
			if err := h.commitInboxLocked(nextTarget); err != nil {
				return InboxItem{}, InboxItem{}, errf(500, "finish pending inbox delegation: %s", err)
			}
			target = h.inbox[target.ID]
		}
		return *source, *target, nil
	}
	if source.State == "handled" || source.State == "cancelled" || source.State == "awaiting_delivery" {
		return InboxItem{}, InboxItem{}, errf(409, "inbox item is already %s", source.State)
	}
	if source.State == "pending_access" || source.State == "pending_delegation" {
		return InboxItem{}, InboxItem{}, errf(409, "inbox item is not available for delegation")
	}
	if h.replyOutboxLocked(source.ID) != nil {
		return InboxItem{}, InboxItem{}, errf(409, "reply already exists for inbox item")
	}
	if !h.hasDelegationRelationshipLocked(source.AgentID, targetAgent.ID) {
		return InboxItem{}, InboxItem{}, errf(403, "no directed relationship authorizes delegation to %s", targetAgent.Name)
	}
	message := h.messages[source.MessageID]
	if message == nil {
		return InboxItem{}, InboxItem{}, errf(500, "inbox message not found: %s", source.MessageID)
	}
	targetAddress, targetMembership, routeErr := h.delegationRouteLocked(message, targetAgent.ID)
	if routeErr != nil {
		return InboxItem{}, InboxItem{}, routeErr
	}
	for _, candidate := range h.inbox {
		if candidate == nil || candidate.DelegatedFromInboxItemID != source.ID || candidate.State == "cancelled" {
			continue
		}
		return InboxItem{}, InboxItem{}, errf(409, "inbox item already has an incomplete delegation target: %s", candidate.ID)
	}
	ts := now()
	target := InboxItem{
		ID: newIntegrationID("inb"), AgentID: targetAgent.ID, MessageID: source.MessageID,
		AddressID: targetAddress.ID, MembershipID: targetMembership.ID,
		DelegatedFromInboxItemID: source.ID, State: "pending_delegation",
		Note: strings.TrimSpace(p.Reason), CreatedAt: ts, UpdatedAt: ts,
	}
	if err := h.appendNewInboxLocked(target); err != nil {
		return InboxItem{}, InboxItem{}, errf(500, "persist pending inbox delegation: %s", err)
	}
	nextSource := *source
	nextSource.State = "handled"
	nextSource.Outcome = "delegated"
	nextSource.DelegatedToInboxItemID = target.ID
	nextSource.ActiveAttemptID = ""
	nextSource.LastError = ""
	nextSource.Note = strings.TrimSpace(p.Reason)
	nextSource.UpdatedAt = now()
	if err := h.commitInboxLocked(nextSource); err != nil {
		cancelled := target
		cancelled.State = "cancelled"
		cancelled.Note = "source delegation decision was not persisted"
		cancelled.UpdatedAt = now()
		if cancelErr := h.commitInboxLocked(cancelled); cancelErr != nil {
			log.Printf("[codex-loom] cancel incomplete delegation target %s: %v", cancelled.ID, cancelErr)
		}
		return InboxItem{}, InboxItem{}, errf(500, "persist source inbox delegation: %s", err)
	}
	queued := target
	queued.State = "queued"
	queued.UpdatedAt = now()
	if err := h.commitInboxLocked(queued); err != nil {
		return nextSource, target, errf(500, "queue delegated inbox target: %s", err)
	}
	return nextSource, queued, nil
}

func (h *Hub) hasDelegationRelationshipLocked(fromAgentID, toAgentID string) bool {
	for _, relationship := range h.teamLinks {
		if relationship != nil && relationship.FromAgentID == fromAgentID && relationship.ToAgentID == toAgentID {
			return true
		}
	}
	return false
}

func (h *Hub) delegationRouteLocked(message *InboxMessage, targetAgentID string) (*AgentAddress, *ConversationMembership, error) {
	type route struct {
		address    *AgentAddress
		membership *ConversationMembership
	}
	routes := []route{}
	for _, address := range h.addresses {
		if address == nil || address.AgentID != targetAgentID || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" {
			continue
		}
		connection := h.connections[address.ConnectionID]
		if connection == nil || !connection.Enabled || connection.ArchivedAt != "" || connection.Provider != message.Origin {
			continue
		}
		membership := h.membershipForConversationLocked(address.ID, message.Conversation.ConversationID)
		if membership == nil || !membership.Enabled || membership.ArchivedAt != "" {
			continue
		}
		routes = append(routes, route{address: address, membership: membership})
	}
	if len(routes) == 0 {
		return nil, nil, errf(409, "delegation target has no enabled explicit-dispatch route for this provider and conversation")
	}
	if len(routes) != 1 {
		return nil, nil, errf(409, "delegation target has %d enabled routes for this provider and conversation; exactly one is required", len(routes))
	}
	selected := routes[0]
	direct := conversationIsDirect(message.Conversation, TriggerEvidence{})
	if direct && (normalizeDMPolicy(selected.address.DMPolicy) == "closed" || selected.membership.ConversationType != "dm") {
		return nil, nil, errf(409, "delegation target route does not allow this direct conversation")
	}
	if direct && selected.membership.ActorID != "" && selected.membership.ActorID != strings.TrimSpace(message.Sender.ExternalID) {
		return nil, nil, errf(409, "delegation target direct-message contact does not match the sender")
	}
	if conversationNeedsMembership(message.Conversation) && selected.membership.ConversationType == "dm" {
		return nil, nil, errf(409, "delegation target route is not configured as a group")
	}
	if allowed, reason := addressAllowsIngress(*selected.address, selected.membership, IngressParams{
		Sender: message.Sender, Conversation: message.Conversation, Trigger: TriggerEvidence{ExplicitDispatch: true},
	}); !allowed {
		return nil, nil, errf(409, "delegation target route rejected the message: %s", reason)
	}
	if resolvedTriggerPolicy(*selected.address, selected.membership) != "explicit_dispatch" {
		return nil, nil, errf(409, "delegation target route does not require explicit dispatch")
	}
	if resolvedReplyPolicy(*selected.address, selected.membership) == "none" || normalizeOutboundPolicy(selected.membership.OutboundPolicy) == "none" {
		return nil, nil, errf(409, "delegation target route does not allow a reply")
	}
	return selected.address, selected.membership, nil
}

func (h *Hub) availableDelegationTargetsLocked(item *InboxItem, message *InboxMessage) []string {
	if item == nil || message == nil || item.DelegatedFromInboxItemID != "" {
		return nil
	}
	targets := []string{}
	seen := map[string]bool{}
	for _, relationship := range h.teamLinks {
		if relationship == nil || relationship.FromAgentID != item.AgentID || seen[relationship.ToAgentID] {
			continue
		}
		if _, _, err := h.delegationRouteLocked(message, relationship.ToAgentID); err != nil {
			continue
		}
		seen[relationship.ToAgentID] = true
		targets = append(targets, h.agentNameLocked(relationship.ToAgentID, relationship.To))
	}
	sort.Strings(targets)
	return targets
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
	address := h.addresses[item.AddressID]
	if address == nil || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" || address.AgentID != item.AgentID {
		return InboxItem{}, errf(409, "inbox item address is no longer active for its original Agent")
	}
	next := *item
	next.State = "deferred"
	next.Outcome = ""
	next.AvailableAt = until.UTC().Format(time.RFC3339Nano)
	next.ActiveAttemptID = ""
	next.LastError = ""
	next.Note = strings.TrimSpace(p.Reason)
	next.UpdatedAt = now()
	if err := h.commitInboxLocked(next); err != nil {
		return InboxItem{}, errf(500, "persist inbox deferral: %s", err)
	}
	return next, nil
}

func (h *Hub) RetryInboxItem(id string) (InboxItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.inbox[id]
	if item == nil {
		return InboxItem{}, errf(404, "inbox item not found: %s", id)
	}
	if item.State != "failed" && item.State != "deferred" && item.State != "pending_access" && item.State != "interrupted" {
		return InboxItem{}, errf(409, "only interrupted, failed, deferred, or pending-access inbox items can be retried")
	}
	address := h.addresses[item.AddressID]
	if address == nil || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" || address.AgentID != item.AgentID {
		return InboxItem{}, errf(409, "inbox item address is no longer active for its original Agent")
	}
	next := *item
	if item.State == "pending_access" {
		message := h.messages[item.MessageID]
		if message == nil || address == nil {
			return InboxItem{}, errf(409, "pending direct message is unavailable")
		}
		membership := h.membershipForConversationLocked(address.ID, message.Conversation.ConversationID)
		if membership == nil || !membership.Enabled {
			return InboxItem{}, errf(409, "configure and enable this direct-message contact before approving it")
		}
		if membership.ActorID != "" && membership.ActorID != message.Sender.ExternalID {
			return InboxItem{}, errf(409, "configured contact does not match the pending sender")
		}
		next.MembershipID = membership.ID
	}
	next.State = "queued"
	next.AvailableAt = ""
	next.ActiveAttemptID = ""
	next.LastError = ""
	next.Note = ""
	next.UpdatedAt = now()
	if err := h.commitInboxLocked(next); err != nil {
		return InboxItem{}, errf(500, "persist inbox retry: %s", err)
	}
	return next, nil
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

func (h *Hub) createReplyOutboxLocked(item *InboxItem, content MessageContent, responseExpectation string) (OutboxItem, error) {
	if existing := h.replyOutboxLocked(item.ID); existing != nil {
		return *existing, nil
	}
	message := h.messages[item.MessageID]
	if message == nil {
		return OutboxItem{}, errf(500, "inbox message not found: %s", item.MessageID)
	}
	address := h.addresses[item.AddressID]
	membership := h.memberships[item.MembershipID]
	if address == nil || !address.Enabled || address.ArchivedAt != "" || address.DeletedAt != "" || address.AgentID != item.AgentID || effectiveReplyPolicy(message, address, membership) == "none" ||
		(membership != nil && (!membership.Enabled || normalizeOutboundPolicy(membership.OutboundPolicy) == "none")) {
		return OutboxItem{}, errf(409, "inbox item does not allow a reply")
	}
	if connection := h.connections[address.ConnectionID]; len(content.Attachments) > 0 &&
		(connection == nil || !hasCapability(connection.Capabilities, "attachments")) {
		return OutboxItem{}, errf(409, "address connection does not support attachments")
	}
	expectation := strings.TrimSpace(responseExpectation)
	if expectation == "" {
		expectation = "optional"
	}
	if !oneOf(expectation, "required", "optional", "none") {
		return OutboxItem{}, errf(400, "invalid responseExpectation %q", expectation)
	}
	ts := now()
	conversation := message.Conversation
	if connection := h.connections[address.ConnectionID]; connection != nil {
		conversation.Provider = connection.Provider
		conversation.ConnectionID = connection.ID
	}
	outbox := OutboxItem{
		ID: newIntegrationID("out"), AgentID: item.AgentID, AddressID: item.AddressID,
		InboxItemID: item.ID, MembershipID: item.MembershipID,
		InReplyTo: message.ID, Conversation: conversation, Content: content, ResponseExpectation: expectation,
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

func (h *Hub) commitInboxLocked(item InboxItem) error {
	if err := h.st.AppendInbox(item); err != nil {
		return err
	}
	cp := item
	h.inbox[item.ID] = &cp
	h.emitGlobalLocked("loom/inbox-item", map[string]any{"item": cp})
	return nil
}

func (h *Hub) appendNewInboxLocked(item InboxItem) error {
	if h.inbox[item.ID] != nil {
		return fmt.Errorf("inbox item already exists: %s", item.ID)
	}
	if err := h.st.AppendInbox(item); err != nil {
		return err
	}
	cp := item
	h.inbox[item.ID] = &cp
	h.inboxOrder = append(h.inboxOrder, item.ID)
	h.emitGlobalLocked("loom/inbox-item", map[string]any{"item": cp})
	return nil
}

func (h *Hub) commitAttemptLocked(attempt HandlingAttempt) error {
	if err := h.st.AppendAttempt(attempt); err != nil {
		return err
	}
	cp := attempt
	h.attempts[attempt.ID] = &cp
	h.emitGlobalLocked("loom/inbox-attempt", map[string]any{"attempt": cp})
	return nil
}
