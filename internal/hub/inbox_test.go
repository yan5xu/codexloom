package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestIngressIsIdempotentAcrossRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	seedInboxAgent(t, h, "agent-a", "alpha")
	connection, err := h.CreateConnection(ConnectionParams{Provider: "parall"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "prll://usr_alpha",
		TrustDomain: "workspace-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	params := IngressParams{
		ConnectionID: connection.ID, AddressID: address.ID,
		ExternalEventID: "dsp_1", ExternalMessageID: "msg_1",
		Sender:              ActorRef{ExternalID: "usr_sender", DisplayName: "Sender", Kind: "human"},
		Conversation:        ConversationRef{ConversationID: "chat_1", ThreadID: "thread_1"},
		Content:             MessageContent{Text: "Please inspect the failure."},
		ResponseExpectation: "required",
		Trigger:             TriggerEvidence{ExplicitDispatch: true},
	}
	first, err := h.IngestMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.IngestMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate || !second.Duplicate {
		t.Fatalf("duplicate flags = %v/%v, want false/true", first.Duplicate, second.Duplicate)
	}
	if first.Message == nil || second.Message == nil || first.InboxItem == nil || second.InboxItem == nil || first.Message.ID != second.Message.ID || first.InboxItem.ID != second.InboxItem.ID {
		t.Fatalf("duplicate created new objects: first=%#v second=%#v", first, second)
	}

	h.Shutdown()
	reloaded := New(st)
	defer reloaded.Shutdown()
	third, err := reloaded.IngestMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Duplicate || third.Message == nil || third.InboxItem == nil || third.Message.ID != first.Message.ID || third.InboxItem.ID != first.InboxItem.ID {
		t.Fatalf("restart lost idempotency: %#v", third)
	}
	items, err := reloaded.ListInbox("alpha", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("inbox items = %d, want 1", len(items))
	}
}

func TestIngressEnforcesAddressTriggerPolicy(t *testing.T) {
	tests := []struct {
		name             string
		policy           string
		conversationType string
		trigger          TriggerEvidence
		wantIgnored      bool
	}{
		{name: "group without mention", policy: "mention", conversationType: "group", wantIgnored: true},
		{name: "group mention", policy: "mention", conversationType: "group", trigger: TriggerEvidence{Mentioned: true}},
		{name: "direct message", policy: "mention", conversationType: "dm", trigger: TriggerEvidence{Direct: true}},
		{name: "explicit dispatch", policy: "explicit_dispatch", conversationType: "channel", trigger: TriggerEvidence{ExplicitDispatch: true}},
		{name: "ordinary message is not dispatch", policy: "explicit_dispatch", conversationType: "group", trigger: TriggerEvidence{Mentioned: true}, wantIgnored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := stoppedInboxTestHub(t)
			connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
			if err != nil {
				t.Fatal(err)
			}
			address, err := h.CreateAddress(AddressParams{
				Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot", TriggerPolicy: tt.policy,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tt.conversationType == "group" {
				if _, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
					AddressID: address.ID, ConversationID: "chat-1",
				}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := h.IngestMessage(IngressParams{
				ConnectionID: connection.ID, AddressID: address.ID, ExternalEventID: "evt-1", ExternalMessageID: "msg-1",
				Sender: ActorRef{ExternalID: "user-1"}, Conversation: ConversationRef{ConversationID: "chat-1", ConversationType: tt.conversationType},
				Content: MessageContent{Text: "hello"}, Trigger: tt.trigger,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Ignored != tt.wantIgnored {
				t.Fatalf("ignored = %v (%s), want %v", result.Ignored, result.Reason, tt.wantIgnored)
			}
			items, err := h.ListInbox("alpha", "", "")
			if err != nil {
				t.Fatal(err)
			}
			wantItems := 1
			if tt.wantIgnored {
				wantItems = 0
			}
			if len(items) != wantItems {
				t.Fatalf("inbox items = %d, want %d", len(items), wantItems)
			}
		})
	}
}

func TestManagedDirectMessagesRequirePerContactMembership(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", DMPolicy: "managed",
	})
	if err != nil {
		t.Fatal(err)
	}
	ingest := func(messageID, actorID string) IngressResult {
		result, err := h.IngestMessage(IngressParams{
			ConnectionID: connection.ID, AddressID: address.ID,
			ExternalEventID: "evt-" + messageID, ExternalMessageID: messageID,
			Sender:       ActorRef{ExternalID: actorID, DisplayName: "Direct Contact"},
			Conversation: ConversationRef{ConversationID: "dm-chat", ConversationType: "dm"},
			Content:      MessageContent{Text: "hello"}, Trigger: TriggerEvidence{Direct: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	pending := ingest("msg-pending", "ou-contact")
	if !pending.Ignored || pending.InboxItem == nil || pending.InboxItem.State != "pending_access" || pending.Message == nil {
		t.Fatalf("pending ingress = %#v", pending)
	}
	duplicate := ingest("msg-pending", "ou-contact")
	if !duplicate.Duplicate || !duplicate.Ignored || duplicate.InboxItem == nil || duplicate.InboxItem.ID != pending.InboxItem.ID {
		t.Fatalf("pending duplicate = %#v", duplicate)
	}

	conversationType, actorID := "dm", "ou-contact"
	displayName, purpose, role, guidance := "Direct Contact", "Coordinate privately", "Answer account questions", "Do not discuss other accounts"
	membership, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "dm-chat", ConversationType: &conversationType, ActorID: &actorID,
		DisplayName: &displayName, Purpose: &purpose, Role: &role, Guidance: &guidance,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted := ingest("msg-accepted", "ou-contact")
	if accepted.Ignored || accepted.InboxItem == nil || accepted.InboxItem.MembershipID != membership.ID {
		t.Fatalf("accepted ingress = %#v", accepted)
	}
	wrongActor := ingest("msg-wrong-actor", "ou-other")
	if !wrongActor.Ignored || wrongActor.Reason != "direct message sender does not match configured contact" {
		t.Fatalf("wrong actor ingress = %#v", wrongActor)
	}
}

func TestIngressAddressAllowAndBlockLists(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot", TriggerPolicy: "allowlist",
		AllowActors: []string{"trusted"}, AllowConversations: []string{"team-chat"},
		BlockActors: []string{"blocked"}, BlockConversations: []string{"blocked-chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ingest := func(eventID, actor, conversation string) IngressResult {
		t.Helper()
		result, err := h.IngestMessage(IngressParams{
			ConnectionID: connection.ID, AddressID: address.ID, ExternalEventID: eventID,
			Sender: ActorRef{ExternalID: actor}, Conversation: ConversationRef{ConversationID: conversation},
			Content: MessageContent{Text: "hello"}, Trigger: TriggerEvidence{Mentioned: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := ingest("event-ok", "trusted", "team-chat"); result.Ignored {
		t.Fatalf("allowlisted message ignored: %s", result.Reason)
	}
	if result := ingest("event-actor", "stranger", "team-chat"); !result.Ignored {
		t.Fatal("non-allowlisted actor was accepted")
	}
	if result := ingest("event-chat", "trusted", "other-chat"); !result.Ignored {
		t.Fatal("non-allowlisted conversation was accepted")
	}
	if result := ingest("event-block", "blocked", "team-chat"); !result.Ignored || result.Reason != "sender is blocked" {
		t.Fatalf("blocklist did not take precedence: %#v", result)
	}
}

func TestIngressAcceptsAttachmentOnlyAndExposesReference(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "alpha", TriggerPolicy: "all"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.IngestMessage(IngressParams{
		ConnectionID: connection.ID, AddressID: address.ID, ExternalEventID: "attachment-event",
		Sender: ActorRef{ExternalID: "sender"}, Conversation: ConversationRef{ConversationID: "chat"},
		Content: MessageContent{Attachments: []AttachmentRef{{ID: "file-1", Name: `report & notes.pdf`, MimeType: "application/pdf", Size: 2048, URL: `https://example.test/file?a=1&b=2`}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message == nil || result.InboxItem == nil {
		t.Fatalf("accepted ingress returned no message/item: %#v", result)
	}
	envelope := formatInboxEnvelope(*result.Message, *result.InboxItem, address, effectiveReplyPolicy(result.Message, &address, nil), nil)
	for _, fragment := range []string{`<attachments>`, `id="file-1"`, `name="report &amp; notes.pdf"`, `mime_type="application/pdf"`, `size="2048"`, `url="https://example.test/file?a=1&amp;b=2"`} {
		if !strings.Contains(envelope, fragment) {
			t.Fatalf("envelope missing %q: %s", fragment, envelope)
		}
	}
}

func TestIngressPersistsThreadContextAndRendersItAsUserContent(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "parall"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "parall-alpha",
		TriggerPolicy: "explicit_dispatch", TrustDomain: "team-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationType, trigger := "group", "explicit_dispatch"
	if _, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", ConversationType: &conversationType,
		TriggerPolicy: &trigger,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := h.IngestMessage(IngressParams{
		ConnectionID: connection.ID, AddressID: address.ID,
		ExternalEventID: "dispatch-1", ExternalMessageID: "message-current",
		Sender:       ActorRef{ExternalID: "user-current", DisplayName: "Current sender"},
		Conversation: ConversationRef{ConversationID: "chat-1", ConversationType: "thread", ThreadID: "message-root"},
		Content:      MessageContent{Text: "follow up"}, Trigger: TriggerEvidence{ExplicitDispatch: true},
		ThreadContext: &ThreadContext{
			RootExternalMessageID: "message-root", Truncated: true,
			Messages: []ThreadContextMessage{
				{
					ExternalMessageID: "message-root", Role: "root", Sender: ActorRef{ExternalID: "user-root", DisplayName: "Root <sender>"},
					Content: MessageContent{Text: "Original ]]> question", Attachments: []AttachmentRef{{ID: "att-1", Name: "context.png"}}}, OccurredAt: "2026-07-14T10:00:00Z",
				},
				{
					ExternalMessageID: "message-reply", Sender: ActorRef{ExternalID: "user-reply", DisplayName: "Responder"},
					Content: MessageContent{Text: "Earlier reply"}, OccurredAt: "2026-07-14T10:01:00Z", TextTruncated: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message == nil || result.Message.ThreadContext == nil || len(result.Message.ThreadContext.Messages) != 2 {
		t.Fatalf("thread context was not persisted: %#v", result.Message)
	}
	contextMessage := result.Message.ThreadContext.Messages[0]
	if contextMessage.Sender.Provider != "parall" || contextMessage.Sender.ConnectionID != connection.ID {
		t.Fatalf("context sender route was not normalized: %#v", contextMessage.Sender)
	}
	envelope := formatInboxEnvelope(*result.Message, *result.InboxItem, address, "final_answer", nil)
	for _, fragment := range []string{
		`<thread_context root_message_id="message-root" truncated="true">`,
		`<message id="message-root" role="root" occurred_at="2026-07-14T10:00:00Z" text_truncated="false">`,
		`<sender id="user-root">Root &lt;sender&gt;</sender>`,
		`Original ]]]]><![CDATA[> question`, `id="att-1"`, `name="context.png"`,
		`<message id="message-reply" role="reply" occurred_at="2026-07-14T10:01:00Z" text_truncated="true">`,
	} {
		if !strings.Contains(envelope, fragment) {
			t.Fatalf("thread envelope missing %q: %s", fragment, envelope)
		}
	}
}

func TestIngressRejectsThreadContextForAnotherRoot(t *testing.T) {
	_, err := normalizeInboundThreadContext(&ThreadContext{RootExternalMessageID: "other-root"}, "expected-root", "current")
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatched thread context error = %v", err)
	}
}

func TestIngressRejectsCurrentOrDuplicateThreadContextMessages(t *testing.T) {
	for name, context := range map[string]*ThreadContext{
		"current": {
			RootExternalMessageID: "root",
			Messages:              []ThreadContextMessage{{ExternalMessageID: "current", Role: "reply", Content: MessageContent{Text: "current"}}},
		},
		"duplicate": {
			RootExternalMessageID: "root",
			Messages: []ThreadContextMessage{
				{ExternalMessageID: "root", Role: "root", Content: MessageContent{Text: "root"}},
				{ExternalMessageID: "root", Role: "root", Content: MessageContent{Text: "again"}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeInboundThreadContext(context, "root", "current"); err == nil {
				t.Fatal("invalid thread context was accepted")
			}
		})
	}
}

func TestCrossPlatformInboxPreservesFIFOAndReplyRoute(t *testing.T) {
	h := stoppedInboxTestHub(t)
	parallConnection, err := h.CreateConnection(ConnectionParams{Provider: "parall"})
	if err != nil {
		t.Fatal(err)
	}
	larkConnection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	parallAddress, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: parallConnection.ID, ExternalIdentity: "parall-alpha",
		TriggerPolicy: "explicit_dispatch", TrustDomain: "team-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	larkAddress, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: larkConnection.ID, ExternalIdentity: "lark-alpha",
		TriggerPolicy: "mention", TrustDomain: "team-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: larkAddress.ID, ConversationID: "lark-chat",
	}); err != nil {
		t.Fatal(err)
	}
	parallResult, err := h.IngestMessage(IngressParams{
		ConnectionID: parallConnection.ID, AddressID: parallAddress.ID, ExternalEventID: "parall-event",
		ExternalMessageID: "parall-message", Sender: ActorRef{ExternalID: "parall-sender"},
		Conversation: ConversationRef{ConversationID: "parall-chat", ThreadID: "parall-thread", MessageID: "parall-message"},
		Content:      MessageContent{Text: "first"}, Trigger: TriggerEvidence{ExplicitDispatch: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	larkResult, err := h.IngestMessage(IngressParams{
		ConnectionID: larkConnection.ID, AddressID: larkAddress.ID, ExternalEventID: "lark-event",
		ExternalMessageID: "lark-message", Sender: ActorRef{ExternalID: "lark-sender"},
		Conversation: ConversationRef{ConversationID: "lark-chat", MessageID: "lark-message", ConversationType: "group"},
		Content:      MessageContent{Text: "second"}, Trigger: TriggerEvidence{Mentioned: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if parallResult.InboxItem == nil || larkResult.InboxItem == nil || len(h.inboxOrder) != 2 ||
		h.inboxOrder[0] != parallResult.InboxItem.ID || h.inboxOrder[1] != larkResult.InboxItem.ID {
		t.Fatalf("inbox order = %#v, parall=%#v lark=%#v", h.inboxOrder, parallResult.InboxItem, larkResult.InboxItem)
	}
	_, parallOutbox, err := h.ReplyInboxItem(parallResult.InboxItem.ID, InboxActionParams{Agent: "alpha", Content: MessageContent{Text: "parall reply"}})
	if err != nil {
		t.Fatal(err)
	}
	_, larkOutbox, err := h.ReplyInboxItem(larkResult.InboxItem.ID, InboxActionParams{Agent: "alpha", Content: MessageContent{Text: "lark reply"}})
	if err != nil {
		t.Fatal(err)
	}
	if parallOutbox.AddressID != parallAddress.ID || parallOutbox.Conversation.ConnectionID != parallConnection.ID ||
		parallOutbox.Conversation.ConversationID != "parall-chat" || parallOutbox.Conversation.ThreadID != "parall-thread" {
		t.Fatalf("Parall reply route crossed platforms: %#v", parallOutbox)
	}
	if larkOutbox.AddressID != larkAddress.ID || larkOutbox.Conversation.ConnectionID != larkConnection.ID ||
		larkOutbox.Conversation.ConversationID != "lark-chat" || larkOutbox.Conversation.MessageID != "lark-message" {
		t.Fatalf("Lark reply route crossed platforms: %#v", larkOutbox)
	}
}

func TestAddressEnforcesIdentityAndTrustDomain(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	seedInboxAgent(t, h, "agent-a", "alpha")
	seedInboxAgent(t, h, "agent-b", "beta")
	first, _ := h.CreateConnection(ConnectionParams{Provider: "parall"})
	second, _ := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if _, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: first.ID, ExternalIdentity: "external-alpha", TrustDomain: "org-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateAddress(AddressParams{
		Agent: "beta", ConnectionID: first.ID, ExternalIdentity: "external-alpha", TrustDomain: "org-a",
	}); err == nil {
		t.Fatal("same external identity was bound to two agents")
	}
	if _, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: second.ID, ExternalIdentity: "lark-alpha", TrustDomain: "org-b",
	}); err == nil {
		t.Fatal("one agent was allowed to cross trust domains")
	}
}

func TestInboxStateRecoversInterruptedWork(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	message := InboxMessage{ID: "imsg_1", ExternalKey: "conn_1:msg_1", Origin: "fake", ReceivedAt: now()}
	inbox := InboxItem{
		ID: "inb_1", AgentID: "agent-a", MessageID: message.ID, AddressID: "addr_1",
		State: "handling", ActiveAttemptID: "att_1", CreatedAt: now(), UpdatedAt: now(),
	}
	attempt := HandlingAttempt{
		ID: "att_1", InboxItemID: inbox.ID, SessionID: "agent-a", Status: "running", StartedAt: now(),
	}
	outbox := OutboxItem{
		ID: "out_1", AgentID: "agent-a", AddressID: "addr_1", State: "sending",
		IdempotencyKey: "reply:inb_1", CreatedAt: now(), UpdatedAt: now(),
	}
	if err := st.AppendMessage(message); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendInbox(inbox); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendOutbox(outbox); err != nil {
		t.Fatal(err)
	}

	h := New(st)
	defer h.Shutdown()
	if got := h.inbox[inbox.ID]; got == nil || got.State != "queued" || got.ActiveAttemptID != "" {
		t.Fatalf("recovered inbox = %#v", got)
	}
	if got := h.attempts[attempt.ID]; got == nil || got.Status != "interrupted" {
		t.Fatalf("recovered attempt = %#v", got)
	}
	if got := h.outbox[outbox.ID]; got == nil || got.State != "pending" {
		t.Fatalf("recovered outbox = %#v", got)
	}
}

func TestFinishInboxAttemptCreatesDurableReply(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "final_answer")
	turn := &turnState{
		turnID: "turn-1", inboxItemID: "inb-1", attemptID: "att-1", finalAnswer: "Verified and fixed.",
	}
	h.mu.Lock()
	h.finishInboxAttemptLocked(turn, "completed", "")
	h.mu.Unlock()

	item := h.inbox["inb-1"]
	if item == nil || item.State != "awaiting_delivery" || item.Outcome != "reply" {
		t.Fatalf("inbox item = %#v", item)
	}
	if len(h.outbox) != 1 {
		t.Fatalf("outbox count = %d, want 1", len(h.outbox))
	}
	for _, outbox := range h.outbox {
		if outbox.State != "pending" || outbox.Content.Text != "Verified and fixed." || outbox.IdempotencyKey != "reply:inb-1" || outbox.ResponseExpectation != "optional" {
			t.Fatalf("outbox = %#v", outbox)
		}
	}
	attempt := h.attempts["att-1"]
	if attempt.Status != "completed" || attempt.FinalAnswer != "Verified and fixed." || attempt.TurnID != "turn-1" {
		t.Fatalf("attempt = %#v", attempt)
	}
}

func TestFinishInboxAttemptRequiresExplicitDecision(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.mu.Lock()
	h.finishInboxAttemptLocked(&turnState{
		turnID: "turn-1", inboxItemID: "inb-1", attemptID: "att-1", finalAnswer: "Local summary only.",
	}, "completed", "")
	h.mu.Unlock()
	item := h.inbox["inb-1"]
	if item.State != "failed" || !stringsContains(item.LastError, "decision_missing") || len(h.outbox) != 0 {
		t.Fatalf("item/outbox = %#v / %#v", item, h.outbox)
	}
}

func TestNoResponseExpectationOverridesAddressReplyPolicy(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "final_answer")
	h.messages["imsg-1"].ResponseExpectation = "none"
	h.mu.Lock()
	h.finishInboxAttemptLocked(&turnState{
		turnID: "turn-1", inboxItemID: "inb-1", attemptID: "att-1", finalAnswer: "Local acknowledgement.",
	}, "completed", "")
	h.mu.Unlock()
	item := h.inbox["inb-1"]
	if item.State != "handled" || item.Outcome != "no_reply" || len(h.outbox) != 0 {
		t.Fatalf("none expectation created a reply: item=%#v outbox=%#v", item, h.outbox)
	}
	if _, _, err := h.ReplyInboxItem("inb-1", InboxActionParams{Agent: "alpha", Content: MessageContent{Text: "late"}}); err == nil {
		t.Fatal("manual reply was allowed for expectation=none")
	}
	envelope := formatInboxEnvelope(*h.messages["imsg-1"], *item, *h.addresses["addr-1"], "none", nil)
	if strings.Contains(envelope, "reply_command") || strings.Contains(envelope, "no_reply_command") || !strings.Contains(envelope, "<reply_policy>none</reply_policy>") {
		t.Fatalf("none expectation envelope exposes reply actions: %s", envelope)
	}
}

func TestInboxEnvelopeReplyInstructionsMatchPolicy(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "final_answer")
	message, item, address := *h.messages["imsg-1"], *h.inbox["inb-1"], *h.addresses["addr-1"]

	finalEnvelope := formatInboxEnvelope(message, item, address, "final_answer", nil)
	if !strings.Contains(finalEnvelope, "<reply_instruction>") || strings.Contains(finalEnvelope, "reply_command") {
		t.Fatalf("final_answer envelope exposes the wrong reply contract: %s", finalEnvelope)
	}

	address.ReplyPolicy = "explicit"
	previousCLIPath := loomCLIPath
	loomCLIPath = "/tmp/Codex Loom/bin/loom"
	defer func() { loomCLIPath = previousCLIPath }()
	explicitEnvelope := formatInboxEnvelope(message, item, address, "explicit", nil)
	if !strings.Contains(explicitEnvelope, "<reply_command>") || !strings.Contains(explicitEnvelope, "<reply_with_attachment_command>") || !strings.Contains(explicitEnvelope, "<no_reply_command>") || strings.Contains(explicitEnvelope, "reply_instruction") {
		t.Fatalf("explicit envelope exposes the wrong reply contract: %s", explicitEnvelope)
	}
	if !strings.Contains(explicitEnvelope, "&#39;/tmp/Codex Loom/bin/loom&#39; integration send") || !strings.Contains(explicitEnvelope, "--from &#39;agent-a&#39;") || !strings.Contains(explicitEnvelope, "--reply-to &#39;inb-1&#39;") {
		t.Fatalf("explicit envelope does not carry a cwd-independent command: %s", explicitEnvelope)
	}
	if !strings.Contains(explicitEnvelope, "--file &#34;/absolute/path/to/file&#34;") {
		t.Fatalf("explicit envelope does not describe attachment replies: %s", explicitEnvelope)
	}
}

func TestInboxEnvelopeCarriesProviderAndDeliveryTimeline(t *testing.T) {
	message := InboxMessage{
		ID: "imsg-time", Origin: "parall", OccurredAt: "2026-07-14T08:30:00Z", ReceivedAt: "2026-07-14T08:31:00Z",
		Sender:       ActorRef{ExternalID: "usr_sender", DisplayName: "Sender"},
		Conversation: ConversationRef{ConversationID: "chat-1"}, Content: MessageContent{Text: "Timeline"},
	}
	item := InboxItem{ID: "inb-time", AgentID: "agent-a"}
	address := AgentAddress{ID: "addr-1"}
	envelope := formatInboxEnvelopeAt(message, item, address, "none", nil, "2026-07-15T01:45:00+08:00")
	want := `<timing sent_at="2026-07-14T08:30:00Z" received_at="2026-07-14T08:31:00Z" current_time="2026-07-15T01:45:00+08:00" />`
	if !strings.Contains(envelope, want) {
		t.Fatalf("timing missing from inbox envelope:\n%s", envelope)
	}
}

func TestResolveLoomCLIPathHonorsEnvironment(t *testing.T) {
	t.Setenv("CODEX_LOOM_CLI", "/custom/bin/loom")
	if got := resolveLoomCLIPath(); got != "/custom/bin/loom" {
		t.Fatalf("resolveLoomCLIPath() = %q", got)
	}
}

func TestInboxReplyAndNoReplyAreIdempotentAndExclusive(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	firstItem, firstOutbox, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "Reply once."},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondItem, secondOutbox, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "Reply once."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstItem.Outcome != "reply" || secondItem.Outcome != "reply" || firstOutbox.ID != secondOutbox.ID || len(h.outbox) != 1 {
		t.Fatalf("reply idempotency failed: %#v %#v", firstOutbox, secondOutbox)
	}
	if _, err := h.NoReplyInboxItem("inb-1", InboxActionParams{Agent: "alpha"}); err == nil {
		t.Fatal("no-reply was allowed after a reply existed")
	}
}

func TestNoReplyReasonIsANoteNotAnError(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	item, err := h.NoReplyInboxItem("inb-1", InboxActionParams{Agent: "alpha", Reason: "FYI only"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Note != "FYI only" || item.LastError != "" || item.Outcome != "no_reply" {
		t.Fatalf("no-reply item = %#v", item)
	}
}

func TestNoReplyKeepsProjectionUnchangedWhenInboxCommitFails(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	ledger := filepath.Join(h.st.Dir(), "inbox.ndjson")
	if err := os.Mkdir(ledger, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.NoReplyInboxItem("inb-1", InboxActionParams{Agent: "alpha"}); err == nil {
		t.Fatal("no-reply succeeded after durable append failed")
	}
	item := h.inbox["inb-1"]
	if item.State != "handling" || item.Outcome != "" || item.ActiveAttemptID != "att-1" {
		t.Fatalf("uncommitted no-reply reached projection: %#v", item)
	}
}

func TestCompletedFinalAnswerOnlyCapturesFinalAgentMessage(t *testing.T) {
	final := json.RawMessage(`{"item":{"type":"agentMessage","phase":"final_answer","text":"Final answer"}}`)
	commentary := json.RawMessage(`{"item":{"type":"agentMessage","phase":"commentary","text":"Working"}}`)
	if got := completedFinalAnswer("item/completed", final); got != "Final answer" {
		t.Fatalf("final answer = %q", got)
	}
	if got := completedFinalAnswer("item/completed", commentary); got != "" {
		t.Fatalf("commentary captured as final = %q", got)
	}
}

func TestConnectorReplaysAndCompletesOutboxIdempotently(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.connections["conn-1"] = &PlatformConnection{ID: "conn-1", Provider: "fake", Enabled: true}
	h.addresses["addr-1"].ConnectionID = "conn-1"
	_, outbox, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "Deliver once."},
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := h.ClaimNextOutbox("conn-1")
	if err != nil || command == nil || command.OutboxItem.ID != outbox.ID || command.OutboxItem.State != "sending" {
		t.Fatalf("first claim = %#v, err=%v", command, err)
	}
	firstToken := command.OutboxItem.AttemptToken
	h.RequeueSendingForConnection("conn-1")
	if command, err = h.ClaimNextOutbox("conn-1"); err != nil || command != nil {
		t.Fatalf("unexpired claim was replayed: %#v, err=%v", command, err)
	}
	h.outbox[outbox.ID].ClaimExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	h.RequeueSendingForConnection("conn-1")
	command, err = h.ClaimNextOutbox("conn-1")
	if err != nil || command == nil || command.OutboxItem.AttemptCount != 2 {
		t.Fatalf("replayed claim = %#v, err=%v", command, err)
	}
	if _, err := h.CompleteOutbox("conn-1", outbox.ID, OutboxResultParams{
		AttemptToken: firstToken, Success: true, ExternalMessageID: "stale-result",
	}); err == nil {
		t.Fatal("stale delivery result was accepted")
	}
	completed, err := h.CompleteOutbox("conn-1", outbox.ID, OutboxResultParams{
		AttemptToken: command.OutboxItem.AttemptToken,
		Success:      true, ExternalMessageID: "external-reply-1", Cursor: "42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "sent" || completed.ExternalMessageID != "external-reply-1" || h.connections["conn-1"].Cursor != "42" {
		t.Fatalf("completed = %#v connection=%#v", completed, h.connections["conn-1"])
	}
	if item := h.inbox["inb-1"]; item.State != "handled" || item.Outcome != "reply" {
		t.Fatalf("completed delivery did not complete inbox item: %#v", item)
	}
	again, err := h.CompleteOutbox("conn-1", outbox.ID, OutboxResultParams{Success: true, ExternalMessageID: "different"})
	if err != nil || again.ExternalMessageID != "external-reply-1" {
		t.Fatalf("idempotent completion = %#v, err=%v", again, err)
	}
	if command, err := h.ClaimNextOutbox("conn-1"); err != nil || command != nil {
		t.Fatalf("sent item was claimed again: %#v, err=%v", command, err)
	}
}

func TestConnectorClaimsOnlyOneCommandAtATime(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.connections["conn-1"] = &PlatformConnection{ID: "conn-1", Provider: "fake", Enabled: true}
	h.addresses["addr-1"].ConnectionID = "conn-1"
	_, first, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "First"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.CreateOutbox(OutboxParams{
		Agent: "alpha", AddressID: "addr-1", Conversation: ConversationRef{ConversationID: "chat-1"},
		Content: MessageContent{Text: "Second"}, IdempotencyKey: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := h.ClaimNextConnectorCommand("conn-1")
	if err != nil || command == nil || command.OutboxItem.ID != first.ID {
		t.Fatalf("first command = %#v, err=%v", command, err)
	}
	if command, err = h.ClaimNextConnectorCommand("conn-1"); err != nil || command != nil {
		t.Fatalf("second command was claimed while first was in flight: %#v, err=%v", command, err)
	}
	if _, err := h.CompleteOutbox("conn-1", first.ID, OutboxResultParams{
		AttemptToken: h.outbox[first.ID].AttemptToken, Success: true, ExternalMessageID: "external-first",
	}); err != nil {
		t.Fatal(err)
	}
	command, err = h.ClaimNextConnectorCommand("conn-1")
	if err != nil || command == nil || command.OutboxItem.ID != second.ID {
		t.Fatalf("second command = %#v, err=%v", command, err)
	}
}

func TestConnectorCannotCompleteAttachmentWithoutProviderReceipt(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.connections["conn-1"] = &PlatformConnection{ID: "conn-1", Provider: "fake", Enabled: true, Capabilities: []string{"attachments"}}
	h.addresses["addr-1"].ConnectionID = "conn-1"
	_, outbox, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "Report"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.outbox[outbox.ID].Content.Attachments = []AttachmentRef{{ID: "art-report", Name: "report.png"}}
	command, err := h.ClaimNextOutbox("conn-1")
	if err != nil || command == nil {
		t.Fatalf("claim attachment outbox = %#v, err=%v", command, err)
	}
	completed, err := h.CompleteOutbox("conn-1", outbox.ID, OutboxResultParams{
		AttemptToken: command.OutboxItem.AttemptToken, Success: true, ExternalMessageID: "message-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "failed" || !strings.Contains(completed.LastError, "art-report") {
		t.Fatalf("attachment delivery without evidence = %#v", completed)
	}
	if _, err := h.RetryOutboxItem(outbox.ID); err != nil {
		t.Fatal(err)
	}
	command, err = h.ClaimNextOutbox("conn-1")
	if err != nil || command == nil {
		t.Fatalf("claim retried attachment outbox = %#v, err=%v", command, err)
	}
	completed, err = h.CompleteOutbox("conn-1", outbox.ID, OutboxResultParams{
		AttemptToken: command.OutboxItem.AttemptToken,
		Success:      true,
		DeliveryReceipts: []OutboxDeliveryReceipt{
			{Kind: "text", ExternalMessageID: "message-2"},
			{Kind: "attachment", ArtifactID: "art-report", ExternalMessageID: "image-message", ExternalAttachmentID: "image-key"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "sent" || len(completed.DeliveryReceipts) != 2 || len(completed.ExternalMessageIDs) != 3 {
		t.Fatalf("attachment delivery with evidence = %#v", completed)
	}
}

func TestExternalSendUsesGovernedMembershipAndManagedAttachments(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "parall", Capabilities: []string{"proactive_send", "attachments"}})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "prll://usr-alpha", TrustDomain: "external",
	})
	if err != nil {
		t.Fatal(err)
	}
	membership, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", DisplayName: stringPtr("Research group"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SendExternal(ExternalSendParams{
		Agent: "alpha", MembershipID: membership.ID, Content: MessageContent{Text: "blocked"}, IdempotencyKey: "delivery-1",
	}); err == nil {
		t.Fatal("reply-only membership allowed a proactive send")
	}
	proactive := "proactive"
	membership, err = h.UpdateConversationMembership(membership.ID, ConversationMembershipParams{OutboundPolicy: &proactive})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(h.st.Dir(), "attachments", "outbound")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(artifactDir, "report.txt")
	if err := os.WriteFile(artifactPath, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	outbox, err := h.SendExternal(ExternalSendParams{
		Agent: "alpha", MembershipID: membership.ID, IdempotencyKey: "delivery-1", ResponseExpectation: "none",
		Content: MessageContent{Text: "summary", Attachments: []AttachmentRef{{Name: "report.txt", Path: artifactPath}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outbox.MembershipID != membership.ID || outbox.AddressID != address.ID || outbox.Conversation.ConversationID != "chat-1" || outbox.ResponseExpectation != "none" {
		t.Fatalf("governed route = %#v", outbox)
	}
	if len(outbox.Content.Attachments) != 1 || outbox.Content.Attachments[0].ID == "" || len(outbox.Content.Attachments[0].SHA256) != 64 {
		t.Fatalf("managed attachment = %#v", outbox.Content.Attachments)
	}
	again, err := h.SendExternal(ExternalSendParams{
		Agent: "alpha", MembershipID: membership.ID, IdempotencyKey: "delivery-1", Content: MessageContent{Text: "different"},
	})
	if err != nil || again.ID != outbox.ID {
		t.Fatalf("idempotent send = %#v, err=%v", again, err)
	}
}

func TestMembershipOutboundNoneBlocksInboxReplies(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.memberships["mem-1"] = &ConversationMembership{
		ID: "mem-1", AddressID: "addr-1", ConversationID: "chat-1", ReplyPolicy: "explicit",
		OutboundPolicy: "none", Enabled: true,
	}
	h.inbox["inb-1"].MembershipID = "mem-1"
	if _, _, err := h.ReplyInboxItem("inb-1", InboxActionParams{
		Agent: "alpha", Content: MessageContent{Text: "blocked"},
	}); err == nil {
		t.Fatal("outboundPolicy=none allowed an Inbox reply")
	}
}

func TestInternalMessagesProjectIntoInboxAndNoReplyIsExclusive(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxAgent(t, h, "agent-b", "beta")
	ts := now()
	message := &AgentMessage{
		ID: "msg_internal", FromAgentID: "agent-b", ToAgentID: "agent-a",
		From: "beta", To: "alpha", Subject: "Graph UI", Body: "Review the graph.",
		Response: "required", Status: "open", DeliveryStatus: "delivered",
		CreatedAt: ts, UpdatedAt: ts,
	}
	h.mu.Lock()
	if err := h.commitAgentMessageLocked(*message); err != nil {
		t.Fatal(err)
	}
	h.mu.Unlock()

	entries, err := h.ListInboxEntries("alpha", "", "chub")
	if err != nil || len(entries) != 1 {
		t.Fatalf("internal inbox entries = %#v, err=%v", entries, err)
	}
	entry := entries[0]
	if entry.Item.ID != "loom:msg_internal" || entry.Message.Origin != "loom" || entry.Item.State != "handling" || entry.AgentName != "alpha" || entry.InternalMessage == nil {
		t.Fatalf("internal inbox projection = %#v", entry)
	}

	closed, err := h.NoReplyAgentMessage(message.ID, "alpha")
	if err != nil || closed.Status != "closed" || closed.Resolution != "no_reply" {
		t.Fatalf("no reply = %#v, err=%v", closed, err)
	}
	if _, err := h.NoReplyAgentMessage(message.ID, "alpha"); err != nil {
		t.Fatalf("no reply is not idempotent: %v", err)
	}
	if _, err := h.SendAgentMessage(CommParams{From: "alpha", ReplyTo: message.ID, Body: "late reply"}); err == nil {
		t.Fatal("reply was allowed after no-reply")
	}

	entries, err = h.ListInboxEntries("alpha", "handled", "chub")
	if err != nil || len(entries) != 1 || entries[0].Item.Outcome != "no_reply" {
		t.Fatalf("resolved projection = %#v, err=%v", entries, err)
	}
}

func TestInterruptedInternalMessageProjectsAsHeldInboxWork(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxAgent(t, h, "agent-b", "beta")
	ts := now()
	message := &AgentMessage{
		ID: "msg_held", FromAgentID: "agent-b", ToAgentID: "agent-a",
		From: "beta", To: "alpha", Subject: "Held work", Body: "Continue only when asked.",
		Response: "required", Status: "open", DeliveryStatus: "delivered", HandlingStatus: "interrupted",
		LastHandlingError: "interrupted by caller",
		HandlingAttempts:  []AgentMessageHandlingAttempt{{ID: "matt-1", TurnID: "turn-1", Status: "interrupted", StartedAt: ts, CompletedAt: ts, Error: "interrupted by caller"}},
		CreatedAt:         ts, UpdatedAt: ts,
	}
	h.mu.Lock()
	if err := h.commitAgentMessageLocked(*message); err != nil {
		t.Fatal(err)
	}
	h.mu.Unlock()

	entries, err := h.ListInboxEntries("alpha", "interrupted", "loom")
	if err != nil || len(entries) != 1 {
		t.Fatalf("held inbox entries = %#v, err=%v", entries, err)
	}
	if entries[0].Item.State != "interrupted" || entries[0].Item.AttemptCount != 1 || entries[0].Item.LastError != "interrupted by caller" {
		t.Fatalf("held inbox projection = %#v", entries[0])
	}
}

func TestConnectionCanBeDisabledWithoutDeletingAddress(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "ou_alpha"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	updated, err := h.UpdateConnection(connection.ID, ConnectionParams{Enabled: &enabled})
	if err != nil || updated.Enabled || updated.Status != "disconnected" {
		t.Fatalf("updated connection = %#v, err=%v", updated, err)
	}
	addresses, err := h.ListAddresses("alpha")
	if err != nil || len(addresses) != 1 || addresses[0].ID != address.ID {
		t.Fatalf("address was lost after disabling connection: %#v, err=%v", addresses, err)
	}
}

func TestUpdateConnectionRollsBackWhenIntegrationCommitFails(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.st.Dir(), "integrations.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{Enabled: &enabled}); err == nil {
		t.Fatal("connection update succeeded after durable replace failed")
	}
	if got := h.connections[connection.ID]; got == nil || !got.Enabled || got.Status != connection.Status {
		t.Fatalf("uncommitted connection update reached projection: %#v", got)
	}
}

func TestInboxRecoveryOnlyUsesLatestProjection(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := "2026-07-14T08:01:27Z"
	completed := "2026-07-14T08:01:39Z"
	for _, item := range []InboxItem{
		{ID: "inb-1", AgentID: "agent-a", State: "handling", ActiveAttemptID: "att-1", AttemptCount: 1, CreatedAt: started, UpdatedAt: started},
		{ID: "inb-1", AgentID: "agent-a", State: "handled", Outcome: "reply", AttemptCount: 1, CreatedAt: started, UpdatedAt: completed},
		{ID: "inb-1", AgentID: "agent-a", State: "queued", AttemptCount: 1, LastError: "stale historical recovery", CreatedAt: started, UpdatedAt: completed},
	} {
		if err := st.AppendInbox(item); err != nil {
			t.Fatal(err)
		}
	}
	for _, attempt := range []HandlingAttempt{
		{ID: "att-1", InboxItemID: "inb-1", AgentID: "agent-a", SessionID: "agent-a", Status: "running", StartedAt: started},
		{ID: "att-1", InboxItemID: "inb-1", AgentID: "agent-a", SessionID: "agent-a", Status: "completed", StartedAt: started, CompletedAt: completed},
		{ID: "att-1", InboxItemID: "inb-1", AgentID: "agent-a", SessionID: "agent-a", Status: "interrupted", StartedAt: started, CompletedAt: completed},
	} {
		if err := st.AppendAttempt(attempt); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []OutboxItem{
		{ID: "out-1", AgentID: "agent-a", InboxItemID: "inb-1", IdempotencyKey: "reply:inb-1", State: "sending", CreatedAt: started, UpdatedAt: started},
		{ID: "out-1", AgentID: "agent-a", InboxItemID: "inb-1", IdempotencyKey: "reply:inb-1", State: "sent", ExternalMessageID: "external-1", CreatedAt: started, UpdatedAt: completed, SentAt: completed},
		{ID: "out-1", AgentID: "agent-a", InboxItemID: "inb-1", IdempotencyKey: "reply:inb-1", State: "pending", LastError: "stale historical recovery", CreatedAt: started, UpdatedAt: completed},
	} {
		if err := st.AppendOutbox(item); err != nil {
			t.Fatal(err)
		}
	}

	for restart := 1; restart <= 2; restart++ {
		h, err := OpenWithOptions(st, OpenOptions{Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := h.inbox["inb-1"]; got == nil || got.State != "handled" || got.Outcome != "reply" {
			t.Fatalf("restart %d inbox = %#v", restart, got)
		}
		if got := h.outbox["out-1"]; got == nil || got.State != "sent" || got.ExternalMessageID != "external-1" {
			t.Fatalf("restart %d outbox = %#v", restart, got)
		}
		if got := h.attempts["att-1"]; got == nil || got.Status != "completed" {
			t.Fatalf("restart %d attempt = %#v", restart, got)
		}
		h.Shutdown()
	}
}

func stoppedInboxTestHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	h.Shutdown()
	seedInboxAgent(t, h, "agent-a", "alpha")
	return h
}

func seedInboxHandlingState(h *Hub, replyPolicy string) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	h.addresses["addr-1"] = &AgentAddress{ID: "addr-1", AgentID: "agent-a", ReplyPolicy: replyPolicy, Enabled: true}
	h.messages["imsg-1"] = &InboxMessage{
		ID: "imsg-1", Origin: "fake", Content: MessageContent{Text: "Question"},
		Conversation: ConversationRef{Provider: "fake", ConversationID: "chat-1"}, ReceivedAt: ts,
	}
	h.inbox["inb-1"] = &InboxItem{
		ID: "inb-1", AgentID: "agent-a", AddressID: "addr-1", MessageID: "imsg-1",
		State: "handling", ActiveAttemptID: "att-1", CreatedAt: ts, UpdatedAt: ts,
	}
	h.inboxOrder = append(h.inboxOrder, "inb-1")
	h.attempts["att-1"] = &HandlingAttempt{
		ID: "att-1", InboxItemID: "inb-1", SessionID: "agent-a", Status: "running", StartedAt: ts,
	}
}

func stringsContains(value, fragment string) bool {
	return len(fragment) == 0 || len(value) >= len(fragment) && strings.Index(value, fragment) >= 0
}

func seedInboxAgent(t *testing.T, h *Hub, id, name string) {
	t.Helper()
	h.mu.Lock()
	h.agents[id] = &Agent{ID: id, Name: name, Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.persistRuntimeProjectionLocked()
	h.mu.Unlock()
}
