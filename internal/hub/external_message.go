package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (h *Hub) commitOutboxLocked(item OutboxItem) error {
	if err := h.st.AppendOutbox(item); err != nil {
		return err
	}
	cp := item
	h.outbox[item.ID] = &cp
	h.emitGlobalLocked("loom/outbox-item", map[string]any{"item": cp})
	return nil
}

func outboxClaimExpired(item *OutboxItem, currentTime time.Time) bool {
	if item == nil || item.State != "sending" || item.ClaimExpiresAt == "" {
		return true
	}
	return leaseExpired(item.ClaimExpiresAt, currentTime)
}

func formatInboxEnvelope(message InboxMessage, item InboxItem, address AgentAddress, policy string, membership *ConversationMembership) string {
	return formatInboxEnvelopeAt(message, item, address, policy, membership, now())
}

func formatInboxEnvelopeAt(message InboxMessage, item InboxItem, address AgentAddress, policy string, membership *ConversationMembership, currentTime string) string {
	var b strings.Builder
	b.WriteString(`<inbox_message version="1" id="` + xmlEscape(message.ID) + `" inbox_item_id="` + xmlEscape(item.ID) + `" expectation="` + xmlEscape(message.ResponseExpectation) + `">` + "\n")
	b.WriteString("  <timing")
	writeXMLAttribute(&b, "sent_at", message.OccurredAt)
	writeXMLAttribute(&b, "received_at", message.ReceivedAt)
	writeXMLAttribute(&b, "current_time", currentTime)
	b.WriteString(" />\n")
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
	if context := message.ThreadContext; context != nil {
		b.WriteString(`  <thread_context`)
		writeXMLAttribute(&b, "root_message_id", context.RootExternalMessageID)
		writeXMLAttribute(&b, "truncated", fmt.Sprintf("%t", context.Truncated))
		b.WriteString(">\n")
		if context.UnavailableReason != "" {
			writeXMLCDATAIndent(&b, "unavailable_reason", context.UnavailableReason, "    ")
		}
		for _, snapshot := range context.Messages {
			b.WriteString(`    <message`)
			writeXMLAttribute(&b, "id", snapshot.ExternalMessageID)
			writeXMLAttribute(&b, "role", snapshot.Role)
			writeXMLAttribute(&b, "occurred_at", snapshot.OccurredAt)
			writeXMLAttribute(&b, "text_truncated", fmt.Sprintf("%t", snapshot.TextTruncated))
			b.WriteString(">\n")
			b.WriteString(`      <sender`)
			writeXMLAttribute(&b, "id", snapshot.Sender.ExternalID)
			writeXMLAttribute(&b, "kind", snapshot.Sender.Kind)
			b.WriteString(">" + xmlEscape(snapshot.Sender.DisplayName) + "</sender>\n")
			writeXMLCDATAIndent(&b, "body", snapshot.Content.Text, "      ")
			writeInboxAttachmentsXML(&b, "      ", snapshot.Content.Attachments)
			b.WriteString("    </message>\n")
		}
		b.WriteString("  </thread_context>\n")
	}
	writeXMLText(&b, "reply_policy", policy)
	switch policy {
	case "final_answer":
		writeXMLText(&b, "reply_instruction", "Return the response as your final answer. The hub will deliver it to the original conversation automatically; do not call a reply command.")
	case "explicit":
		command := shellCommandArg(loomCLIPath)
		writeXMLText(&b, "reply_command", command+" integration send --from "+shellCommandArg(item.AgentID)+" --reply-to "+shellCommandArg(item.ID)+" --body \"...\"")
		writeXMLText(&b, "reply_with_attachment_command", command+" integration send --from "+shellCommandArg(item.AgentID)+" --reply-to "+shellCommandArg(item.ID)+" --body \"...\" --file \"/absolute/path/to/file\"")
		writeXMLText(&b, "no_reply_command", command+" inbox no-reply "+shellCommandArg(item.ID)+" --agent "+shellCommandArg(item.AgentID))
	}
	writeXMLCDATA(&b, "body", message.Content.Text)
	writeInboxAttachmentsXML(&b, "  ", message.Content.Attachments)
	b.WriteString("</inbox_message>")
	return b.String()
}

func writeXMLCDATAIndent(b *strings.Builder, name, value, indent string) {
	b.WriteString(indent + `<` + name + `><![CDATA[` + strings.ReplaceAll(value, "]]>", "]]]]><![CDATA[>") + `]]></` + name + `>` + "\n")
}

func writeInboxAttachmentsXML(b *strings.Builder, indent string, attachments []AttachmentRef) {
	if len(attachments) == 0 {
		return
	}
	b.WriteString(indent + "<attachments>\n")
	for _, attachment := range attachments {
		b.WriteString(indent + `  <attachment`)
		writeXMLAttribute(b, "id", attachment.ID)
		writeXMLAttribute(b, "name", attachment.Name)
		writeXMLAttribute(b, "mime_type", attachment.MimeType)
		if attachment.Size > 0 {
			writeXMLAttribute(b, "size", fmt.Sprintf("%d", attachment.Size))
		}
		writeXMLAttribute(b, "url", attachment.URL)
		writeXMLAttribute(b, "path", attachment.Path)
		b.WriteString(" />\n")
	}
	b.WriteString(indent + "</attachments>\n")
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

func hasCapability(values []string, expected string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == expected {
			return true
		}
	}
	return false
}

func (h *Hub) normalizeOutboundContentLocked(content MessageContent) (MessageContent, error) {
	if strings.TrimSpace(content.Text) == "" && len(content.Attachments) == 0 {
		return MessageContent{}, errf(400, "outbox content is empty")
	}
	if len(content.Attachments) > 8 {
		return MessageContent{}, errf(400, "an outbound delivery supports at most 8 attachments")
	}
	root := filepath.Join(h.st.Dir(), "attachments", "outbound")
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return MessageContent{}, errf(500, "resolve outbound attachment store: %s", err)
	}
	normalized := content
	normalized.Attachments = make([]AttachmentRef, 0, len(content.Attachments))
	for _, attachment := range content.Attachments {
		path, err := filepath.Abs(strings.TrimSpace(attachment.Path))
		if err != nil || strings.TrimSpace(attachment.Path) == "" {
			return MessageContent{}, errf(400, "outbound attachment requires a managed local path")
		}
		relative, err := filepath.Rel(rootAbs, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return MessageContent{}, errf(400, "outbound attachment is outside the Loom artifact store")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return MessageContent{}, errf(400, "read outbound attachment: %s", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return MessageContent{}, errf(400, "outbound attachment must be a regular managed file")
		}
		if info.Size() <= 0 || info.Size() > 25<<20 {
			return MessageContent{}, errf(400, "outbound attachment must be between 1 byte and 25 MB")
		}
		file, err := os.Open(path)
		if err != nil {
			return MessageContent{}, errf(400, "open outbound attachment: %s", err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return MessageContent{}, errf(400, "hash outbound attachment")
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		if attachment.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(attachment.SHA256), digest) {
			return MessageContent{}, errf(409, "outbound attachment changed after staging")
		}
		attachment.Path = path
		attachment.Size = info.Size()
		attachment.SHA256 = digest
		if strings.TrimSpace(attachment.ID) == "" {
			attachment.ID = "art_" + digest[:16]
		}
		attachment.Name = filepath.Base(strings.TrimSpace(attachment.Name))
		if attachment.Name == "." || attachment.Name == "" {
			attachment.Name = filepath.Base(path)
		}
		if strings.TrimSpace(attachment.MimeType) == "" {
			attachment.MimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Name)))
			if attachment.MimeType == "" {
				attachment.MimeType = "application/octet-stream"
			}
		}
		normalized.Attachments = append(normalized.Attachments, attachment)
	}
	return normalized, nil
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

func normalizeOrderedStrings(values []string) []string {
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
	return out
}

func normalizeDeliveryReceipts(values []OutboxDeliveryReceipt) []OutboxDeliveryReceipt {
	seen := map[string]bool{}
	out := make([]OutboxDeliveryReceipt, 0, len(values))
	for _, value := range values {
		value.Kind = strings.ToLower(strings.TrimSpace(value.Kind))
		value.ArtifactID = strings.TrimSpace(value.ArtifactID)
		value.ExternalMessageID = strings.TrimSpace(value.ExternalMessageID)
		value.ExternalAttachmentID = strings.TrimSpace(value.ExternalAttachmentID)
		if value.Kind == "" || value.ExternalMessageID == "" && value.ExternalAttachmentID == "" {
			continue
		}
		key := strings.Join([]string{value.Kind, value.ArtifactID, value.ExternalMessageID, value.ExternalAttachmentID}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func normalizeDMPolicy(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "open"
	}
	return value
}

func conversationIsDirect(conversation ConversationRef, trigger TriggerEvidence) bool {
	return trigger.Direct || oneOf(strings.ToLower(strings.TrimSpace(conversation.ConversationType)), "dm", "p2p", "direct")
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
	direct := conversationIsDirect(p.Conversation, p.Trigger)
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
