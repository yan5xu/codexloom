package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func stableMembershipID(addressID, conversationID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(addressID) + "\x00" + strings.TrimSpace(conversationID)))
	return "mem_" + hex.EncodeToString(sum[:8])
}

func stableConversationCandidateID(addressID, conversationID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(addressID) + "\x00" + strings.TrimSpace(conversationID)))
	return "conv_" + hex.EncodeToString(sum[:8])
}

func (h *Hub) ListConversationCandidates(agentKey, addressID string, availableOnly bool) ([]ConversationCandidate, error) {
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
	addressID = strings.TrimSpace(addressID)
	out := []ConversationCandidate{}
	for _, candidate := range h.conversationCandidates {
		if candidate == nil || addressID != "" && candidate.AddressID != addressID || availableOnly && !candidate.Available {
			continue
		}
		address := h.addresses[candidate.AddressID]
		if address == nil || agentID != "" && address.AgentID != agentID {
			continue
		}
		out = append(out, *candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddressID != out[j].AddressID {
			return out[i].AddressID < out[j].AddressID
		}
		if out[i].Available != out[j].Available {
			return out[i].Available
		}
		left, right := strings.ToLower(out[i].DisplayName), strings.ToLower(out[j].DisplayName)
		if left != right {
			return left < right
		}
		return out[i].ConversationID < out[j].ConversationID
	})
	return out, nil
}

// ReplaceConversationCandidates applies a provider's complete snapshot for one
// external identity. Missing entries become unavailable but remain persisted so
// operators keep evidence and existing Memberships are never silently removed.
func (h *Hub) ReplaceConversationCandidates(addressID string, p ConversationCandidateSnapshotParams) ([]ConversationCandidate, error) {
	addressID = strings.TrimSpace(addressID)
	if addressID == "" {
		return nil, errf(400, "address id is required")
	}
	if len(p.Conversations) > 5000 {
		return nil, errf(400, "conversation snapshot is too large")
	}

	reported := make(map[string]ConversationCandidateParams, len(p.Conversations))
	for _, item := range p.Conversations {
		item.ConversationID = strings.TrimSpace(item.ConversationID)
		item.ConversationType = strings.ToLower(strings.TrimSpace(item.ConversationType))
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		item.Description = strings.TrimSpace(item.Description)
		if item.ConversationID == "" {
			return nil, errf(400, "conversationId is required")
		}
		if item.ConversationType == "" {
			item.ConversationType = "group"
		}
		if !oneOf(item.ConversationType, "group", "dm") {
			return nil, errf(400, "invalid conversationType %q", item.ConversationType)
		}
		if len(item.DisplayName) > 1000 || len(item.Description) > 16000 {
			return nil, errf(400, "conversation candidate metadata is too large")
		}
		if item.DisplayName == "" {
			item.DisplayName = item.ConversationID
		}
		reported[item.ConversationID] = item
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	address := h.addresses[addressID]
	if address == nil {
		return nil, errf(404, "agent address not found: %s", addressID)
	}
	if address.ArchivedAt != "" {
		return nil, errf(409, "address is archived and superseded by %s", address.SupersededBy)
	}
	previous := h.conversationCandidates
	next := make(map[string]*ConversationCandidate, len(previous)+len(reported))
	for id, candidate := range previous {
		if candidate == nil {
			continue
		}
		clone := *candidate
		next[id] = &clone
	}
	ts := now()
	changed := false
	for _, candidate := range next {
		if candidate.AddressID == addressID && candidate.Available {
			if _, ok := reported[candidate.ConversationID]; !ok {
				candidate.Available = false
				candidate.UpdatedAt = ts
				changed = true
			}
		}
	}
	for conversationID, item := range reported {
		id := stableConversationCandidateID(addressID, conversationID)
		candidate := next[id]
		if candidate == nil {
			candidate = &ConversationCandidate{ID: id, AddressID: addressID, ConversationID: conversationID, FirstSeenAt: ts}
			next[id] = candidate
			changed = true
		}
		metadataChanged := candidate.ConversationType != item.ConversationType || candidate.DisplayName != item.DisplayName ||
			candidate.Description != item.Description || !candidate.Available
		candidate.ConversationType = item.ConversationType
		candidate.DisplayName = item.DisplayName
		candidate.Description = item.Description
		candidate.Available = true
		if metadataChanged {
			candidate.UpdatedAt = ts
			changed = true
		}
		candidate.LastSeenAt = ts
	}
	if changed {
		h.conversationCandidates = next
		if err := h.persistIntegrationsLocked(); err != nil {
			h.conversationCandidates = previous
			return nil, errf(500, "save conversation candidates: %s", err)
		}
	}
	out := make([]ConversationCandidate, 0, len(reported))
	for _, candidate := range h.conversationCandidates {
		if candidate != nil && candidate.AddressID == addressID {
			out = append(out, *candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Available != out[j].Available {
			return out[i].Available
		}
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	if changed {
		h.emitGlobalLocked("loom/conversation-candidates", map[string]any{"addressId": addressID, "candidates": out})
	}
	return out, nil
}

func (h *Hub) migrateAllowedConversationsLocked() bool {
	changed := false
	for _, address := range h.addresses {
		if address != nil && strings.TrimSpace(address.DMPolicy) == "" {
			address.DMPolicy = "open"
			changed = true
		}
	}
	for _, membership := range h.memberships {
		if membership != nil && strings.TrimSpace(membership.ConversationType) == "" {
			membership.ConversationType = "group"
			changed = true
		}
		if membership != nil && strings.TrimSpace(membership.OutboundPolicy) == "" {
			membership.OutboundPolicy = "reply_only"
			changed = true
		}
	}
	for _, address := range h.addresses {
		changed = len(h.ensureAllowedConversationMembershipsLocked(address)) > 0 || changed
	}
	return changed
}

func (h *Hub) ensureAllowedConversationMembershipsLocked(address *AgentAddress) []string {
	if address == nil {
		return nil
	}
	created := []string{}
	for _, conversationID := range address.AllowConversations {
		if h.membershipForConversationLocked(address.ID, conversationID) != nil {
			continue
		}
		ts := address.UpdatedAt
		if ts == "" {
			ts = now()
		}
		membership := &ConversationMembership{
			ID: stableMembershipID(address.ID, conversationID), AddressID: address.ID,
			ConversationID: conversationID, ConversationType: "group", DisplayName: conversationID,
			TriggerPolicy: address.TriggerPolicy, ReplyPolicy: address.ReplyPolicy,
			OutboundPolicy: "reply_only",
			TrustDomain:    address.TrustDomain, Enabled: true, Version: 1,
			CreatedAt: ts, UpdatedAt: ts,
		}
		h.memberships[membership.ID] = membership
		created = append(created, membership.ID)
	}
	return created
}

func (h *Hub) membershipForConversationLocked(addressID, conversationID string) *ConversationMembership {
	for _, membership := range h.memberships {
		if membership != nil && membership.AddressID == addressID && membership.ConversationID == conversationID {
			return membership
		}
	}
	return nil
}

func (h *Hub) ListConversationMemberships(agentKey, addressID string) ([]ConversationMembership, error) {
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
	addressID = strings.TrimSpace(addressID)
	out := []ConversationMembership{}
	for _, membership := range h.memberships {
		if membership == nil || addressID != "" && membership.AddressID != addressID {
			continue
		}
		address := h.addresses[membership.AddressID]
		if address == nil || agentID != "" && address.AgentID != agentID {
			continue
		}
		out = append(out, *membership)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddressID != out[j].AddressID {
			return out[i].AddressID < out[j].AddressID
		}
		return out[i].ConversationID < out[j].ConversationID
	})
	return out, nil
}

func (h *Hub) GetConversationMembership(id string) (ConversationMembership, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	membership := h.memberships[strings.TrimSpace(id)]
	if membership == nil {
		return ConversationMembership{}, errf(404, "conversation membership not found: %s", id)
	}
	return *membership, nil
}

func (h *Hub) UpsertConversationMembership(p ConversationMembershipParams) (ConversationMembership, bool, error) {
	addressID := strings.TrimSpace(p.AddressID)
	conversationID := strings.TrimSpace(p.ConversationID)
	if addressID == "" || conversationID == "" {
		return ConversationMembership{}, false, errf(400, "addressId and conversationId are required")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	address := h.addresses[addressID]
	if address == nil {
		return ConversationMembership{}, false, errf(404, "agent address not found: %s", addressID)
	}
	if address.ArchivedAt != "" {
		return ConversationMembership{}, false, errf(409, "address is archived and superseded by %s", address.SupersededBy)
	}
	current := h.membershipForConversationLocked(addressID, conversationID)
	created := current == nil
	if current == nil {
		if p.ExpectedVersion != nil && *p.ExpectedVersion != 0 {
			return ConversationMembership{}, false, errf(409, "conversation membership version changed: expected %d, current 0", *p.ExpectedVersion)
		}
		ts := now()
		current = &ConversationMembership{
			ID: stableMembershipID(addressID, conversationID), AddressID: addressID,
			ConversationID: conversationID, TriggerPolicy: address.TriggerPolicy,
			ReplyPolicy: address.ReplyPolicy, OutboundPolicy: "reply_only", TrustDomain: address.TrustDomain,
			Enabled: true, CreatedAt: ts,
		}
	} else if p.ExpectedVersion != nil && *p.ExpectedVersion != current.Version {
		return ConversationMembership{}, false, errf(409, "conversation membership version changed: expected %d, current %d", *p.ExpectedVersion, current.Version)
	}
	if current.ArchivedAt != "" {
		return ConversationMembership{}, false, errf(409, "conversation membership is archived and superseded by %s", current.SupersededBy)
	}

	next := *current
	if err := applyMembershipParams(&next, p); err != nil {
		return ConversationMembership{}, false, err
	}
	if !created && membershipContentEqual(*current, next) {
		return *current, false, nil
	}
	next.Version = current.Version + 1
	next.UpdatedAt = now()
	h.memberships[next.ID] = &next
	if err := h.persistIntegrationsLocked(); err != nil {
		if created {
			delete(h.memberships, next.ID)
		} else {
			h.memberships[current.ID] = current
		}
		return ConversationMembership{}, false, errf(500, "save conversation membership: %s", err)
	}
	h.emitGlobalLocked("loom/conversation-membership", map[string]any{"membership": next})
	return next, created, nil
}

func (h *Hub) UpdateConversationMembership(id string, p ConversationMembershipParams) (ConversationMembership, error) {
	h.mu.Lock()
	current := h.memberships[strings.TrimSpace(id)]
	if current == nil {
		h.mu.Unlock()
		return ConversationMembership{}, errf(404, "conversation membership not found: %s", id)
	}
	p.AddressID = current.AddressID
	p.ConversationID = current.ConversationID
	h.mu.Unlock()
	updated, _, err := h.UpsertConversationMembership(p)
	return updated, err
}

func applyMembershipParams(next *ConversationMembership, p ConversationMembershipParams) error {
	if p.ConversationType != nil {
		value := strings.ToLower(strings.TrimSpace(*p.ConversationType))
		if !oneOf(value, "group", "dm") {
			return errf(400, "invalid conversationType %q", value)
		}
		if next.ConversationType != "" && next.ConversationType != value {
			return errf(409, "conversation type cannot change from %q to %q", next.ConversationType, value)
		}
		next.ConversationType = value
	}
	if next.ConversationType == "" {
		next.ConversationType = "group"
	}
	if p.ActorID != nil {
		value := strings.TrimSpace(*p.ActorID)
		if next.ActorID != "" && value != "" && next.ActorID != value {
			return errf(409, "conversation actor cannot change")
		}
		if value != "" {
			next.ActorID = value
		}
	}
	for name, value := range map[string]*string{"displayName": p.DisplayName, "purpose": p.Purpose, "role": p.Role, "guidance": p.Guidance} {
		if value != nil && len(*value) > 16_000 {
			return errf(400, "%s must be at most 16000 bytes", name)
		}
	}
	if p.DisplayName != nil {
		next.DisplayName = strings.TrimSpace(*p.DisplayName)
	}
	if p.Purpose != nil {
		next.Purpose = strings.TrimSpace(*p.Purpose)
	}
	if p.Role != nil {
		next.Role = strings.TrimSpace(*p.Role)
	}
	if p.Guidance != nil {
		next.Guidance = strings.TrimSpace(*p.Guidance)
	}
	trigger := next.TriggerPolicy
	if p.TriggerPolicy != nil && strings.TrimSpace(*p.TriggerPolicy) != "" {
		trigger = strings.TrimSpace(*p.TriggerPolicy)
	}
	if !oneOf(trigger, "direct", "mention", "explicit_dispatch", "all", "allowlist") {
		return errf(400, "invalid triggerPolicy %q", trigger)
	}
	reply := next.ReplyPolicy
	if p.ReplyPolicy != nil && strings.TrimSpace(*p.ReplyPolicy) != "" {
		reply = strings.TrimSpace(*p.ReplyPolicy)
	}
	if !oneOf(reply, "explicit", "final_answer", "none") {
		return errf(400, "invalid replyPolicy %q", reply)
	}
	outbound := normalizeOutboundPolicy(next.OutboundPolicy)
	if p.OutboundPolicy != nil && strings.TrimSpace(*p.OutboundPolicy) != "" {
		outbound = normalizeOutboundPolicy(*p.OutboundPolicy)
	}
	if !oneOf(outbound, "reply_only", "proactive", "none") {
		return errf(400, "invalid outboundPolicy %q", outbound)
	}
	trust := next.TrustDomain
	if p.TrustDomain != nil && strings.TrimSpace(*p.TrustDomain) != "" {
		trust = strings.TrimSpace(*p.TrustDomain)
	}
	if trust == "" {
		trust = "local"
	}
	// A shared long-lived Agent is not a trust isolation boundary. Until an
	// agent can use separate provider threads, every membership must stay in
	// the address's existing trust domain.
	if trust != next.TrustDomain {
		return errf(409, "conversation membership must use address trustDomain %q", next.TrustDomain)
	}
	next.TriggerPolicy = trigger
	next.ReplyPolicy = reply
	next.OutboundPolicy = outbound
	next.TrustDomain = trust
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	return nil
}

func membershipContentEqual(a, b ConversationMembership) bool {
	return a.ConversationType == b.ConversationType && a.ActorID == b.ActorID &&
		a.DisplayName == b.DisplayName && a.Purpose == b.Purpose && a.Role == b.Role &&
		a.Guidance == b.Guidance && a.TriggerPolicy == b.TriggerPolicy && a.ReplyPolicy == b.ReplyPolicy &&
		a.OutboundPolicy == b.OutboundPolicy && a.TrustDomain == b.TrustDomain && a.Enabled == b.Enabled
}

func renderConversationContext(message InboxMessage, membership ConversationMembership) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<conversation_context version="1" membership_id="%s" membership_version="%d" provider="%s" conversation_id="%s" conversation_type="%s" actor_id="%s" applies_to_message="%s">`+"\n",
		xmlEscape(membership.ID), membership.Version, xmlEscape(message.Origin), xmlEscape(message.Conversation.ConversationID),
		xmlEscape(membership.ConversationType), xmlEscape(membership.ActorID), xmlEscape(message.ID))
	writeXMLCDATA(&b, "display_name", membership.DisplayName)
	writeXMLCDATA(&b, "purpose", membership.Purpose)
	writeXMLCDATA(&b, "role", membership.Role)
	writeXMLCDATA(&b, "guidance", membership.Guidance)
	writeXMLText(&b, "trust_domain", membership.TrustDomain)
	writeXMLText(&b, "outbound_policy", normalizeOutboundPolicy(membership.OutboundPolicy))
	b.WriteString("</conversation_context>\n")
	b.WriteString("This trusted context applies only to the immediately following inbox message with the matching message id. It supersedes earlier conversation_context instructions for that message, but does not grant tools, permissions, or access.")
	return b.String()
}

func resolvedTriggerPolicy(address AgentAddress, membership *ConversationMembership) string {
	if membership != nil {
		return membership.TriggerPolicy
	}
	return address.TriggerPolicy
}

func resolvedReplyPolicy(address AgentAddress, membership *ConversationMembership) string {
	if membership != nil {
		return membership.ReplyPolicy
	}
	return address.ReplyPolicy
}

func normalizeOutboundPolicy(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "reply_only"
	}
	return value
}

func conversationNeedsMembership(conversation ConversationRef) bool {
	return strings.EqualFold(strings.TrimSpace(conversation.ConversationType), "group")
}

func conversationRequiresMembership(address AgentAddress, conversation ConversationRef) bool {
	if conversationNeedsMembership(conversation) {
		return true
	}
	return conversationIsDirect(conversation, TriggerEvidence{}) && normalizeDMPolicy(address.DMPolicy) == "managed"
}
