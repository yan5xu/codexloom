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

func (h *Hub) migrateAllowedConversationsLocked() bool {
	changed := false
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
			ConversationID: conversationID, DisplayName: conversationID,
			TriggerPolicy: address.TriggerPolicy, ReplyPolicy: address.ReplyPolicy,
			TrustDomain: address.TrustDomain, Enabled: true, Version: 1,
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
			ReplyPolicy: address.ReplyPolicy, TrustDomain: address.TrustDomain,
			Enabled: true, CreatedAt: ts,
		}
	} else if p.ExpectedVersion != nil && *p.ExpectedVersion != current.Version {
		return ConversationMembership{}, false, errf(409, "conversation membership version changed: expected %d, current %d", *p.ExpectedVersion, current.Version)
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
	next.TrustDomain = trust
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	return nil
}

func membershipContentEqual(a, b ConversationMembership) bool {
	return a.DisplayName == b.DisplayName && a.Purpose == b.Purpose && a.Role == b.Role &&
		a.Guidance == b.Guidance && a.TriggerPolicy == b.TriggerPolicy && a.ReplyPolicy == b.ReplyPolicy &&
		a.TrustDomain == b.TrustDomain && a.Enabled == b.Enabled
}

func renderConversationContext(message InboxMessage, membership ConversationMembership) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<conversation_context version="1" membership_id="%s" membership_version="%d" provider="%s" conversation_id="%s" applies_to_message="%s">`+"\n",
		xmlEscape(membership.ID), membership.Version, xmlEscape(message.Origin), xmlEscape(message.Conversation.ConversationID), xmlEscape(message.ID))
	writeXMLCDATA(&b, "display_name", membership.DisplayName)
	writeXMLCDATA(&b, "purpose", membership.Purpose)
	writeXMLCDATA(&b, "role", membership.Role)
	writeXMLCDATA(&b, "guidance", membership.Guidance)
	writeXMLText(&b, "trust_domain", membership.TrustDomain)
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

func conversationNeedsMembership(conversation ConversationRef) bool {
	return strings.EqualFold(strings.TrimSpace(conversation.ConversationType), "group")
}
