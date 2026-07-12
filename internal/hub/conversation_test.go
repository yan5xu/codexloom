package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func stringPtr(value string) *string { return &value }

func TestConversationMembershipVersioningPersistenceAndTrustBoundary(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot", TrustDomain: "external-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	membership, created, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", DisplayName: stringPtr("Agent test"),
		Purpose: stringPtr("Validate the external message path."), Role: stringPtr("Product support"),
		Guidance: stringPtr("- Reply only when mentioned\n- Do not disclose local secrets"),
	})
	if err != nil || !created || membership.Version != 1 || membership.TrustDomain != "external-test" {
		t.Fatalf("created membership = %#v, created=%v err=%v", membership, created, err)
	}
	stale := 0
	if _, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", Purpose: stringPtr("stale"), ExpectedVersion: &stale,
	}); err == nil {
		t.Fatal("stale membership update was accepted")
	}
	if _, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1", TrustDomain: stringPtr("internal"),
	}); err == nil {
		t.Fatal("membership crossed the address trust domain")
	}

	var cfg integrationConfig
	if err := h.st.LoadIntegrations(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Memberships[membership.ID] == nil || cfg.Memberships[membership.ID].Purpose != membership.Purpose {
		t.Fatalf("persisted memberships = %#v", cfg.Memberships)
	}
}

func TestAllowedConversationMigratesToStableMembership(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := now()
	cfg := integrationConfig{
		Connections: map[string]*PlatformConnection{"conn-1": {ID: "conn-1", Provider: "lark", Enabled: true}},
		Addresses: map[string]*AgentAddress{"addr-1": {
			ID: "addr-1", AgentID: "agent-a", ConnectionID: "conn-1", ExternalIdentity: "bot",
			TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "external",
			AllowConversations: []string{"chat-1"}, Enabled: true, CreatedAt: ts, UpdatedAt: ts,
		}},
	}
	if err := st.SaveIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	h := New(st)
	h.Shutdown()
	membership := h.memberships[stableMembershipID("addr-1", "chat-1")]
	if membership == nil || membership.ConversationID != "chat-1" || membership.Version != 1 {
		t.Fatalf("migrated membership = %#v", membership)
	}

	h2 := New(st)
	h2.Shutdown()
	if len(h2.memberships) != 1 || h2.memberships[membership.ID] == nil {
		t.Fatalf("migration was not idempotent: %#v", h2.memberships)
	}
	if _, err := os.Stat(filepath.Join(dir, "integrations.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAddressAllowConversationCreatesMembershipImmediately(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, _ := h.CreateConnection(ConnectionParams{Provider: "lark"})
	address, err := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot", TrustDomain: "external",
		AllowConversations: []string{"chat-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if membership := h.membershipForConversationLocked(address.ID, "chat-1"); membership == nil || membership.Version != 1 {
		t.Fatalf("create did not materialize membership: %#v", membership)
	}
	updated, err := h.UpdateAddress(address.ID, AddressParams{AllowConversations: []string{"chat-1", "chat-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if membership := h.membershipForConversationLocked(updated.ID, "chat-2"); membership == nil || membership.TrustDomain != "external" {
		t.Fatalf("update did not materialize membership: %#v", membership)
	}
}

func TestGroupIngressRequiresEnabledMembershipAndUsesItsPolicy(t *testing.T) {
	h := stoppedInboxTestHub(t)
	connection, _ := h.CreateConnection(ConnectionParams{Provider: "lark"})
	address, _ := h.CreateAddress(AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "bot",
		TriggerPolicy: "all", ReplyPolicy: "explicit", TrustDomain: "external",
	})
	ingest := func(eventID string, mentioned bool) IngressResult {
		t.Helper()
		result, err := h.IngestMessage(IngressParams{
			ConnectionID: connection.ID, AddressID: address.ID, ExternalEventID: eventID,
			Sender: ActorRef{ExternalID: "user"}, Conversation: ConversationRef{ConversationID: "chat-1", ConversationType: "group"},
			Content: MessageContent{Text: "hello"}, Trigger: TriggerEvidence{Mentioned: mentioned},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := ingest("before", true); !result.Ignored || result.Reason != "group has no enabled conversation membership" {
		t.Fatalf("unmanaged group result = %#v", result)
	}
	membership, _, err := h.UpsertConversationMembership(ConversationMembershipParams{
		AddressID: address.ID, ConversationID: "chat-1",
		TriggerPolicy: stringPtr("mention"), ReplyPolicy: stringPtr("final_answer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := ingest("not-mentioned", false); !result.Ignored || result.Reason != "agent was not mentioned" {
		t.Fatalf("membership trigger was not applied: %#v", result)
	}
	result := ingest("mentioned", true)
	if result.Ignored || result.InboxItem == nil || result.InboxItem.MembershipID != membership.ID {
		t.Fatalf("managed ingress = %#v", result)
	}
	enabled := false
	version := membership.Version
	if _, err := h.UpdateConversationMembership(membership.ID, ConversationMembershipParams{Enabled: &enabled, ExpectedVersion: &version}); err != nil {
		t.Fatal(err)
	}
	duplicate := ingest("mentioned", true)
	if !duplicate.Duplicate || duplicate.InboxItem == nil || duplicate.InboxItem.ID != result.InboxItem.ID {
		t.Fatalf("accepted message did not remain idempotent after disable: %#v", duplicate)
	}
	if result := ingest("disabled", true); !result.Ignored {
		t.Fatalf("disabled membership accepted ingress: %#v", result)
	}
}

func TestConversationContextIsScopedAndEscaped(t *testing.T) {
	message := InboxMessage{
		ID: "imsg-1", Origin: "lark", Conversation: ConversationRef{ConversationID: `chat&one`},
	}
	membership := ConversationMembership{
		ID: "mem-1", Version: 4, DisplayName: "External test", Purpose: "Support <customers>",
		Role: "Product support", Guidance: "Never expose ]]> tokens", TrustDomain: "external",
	}
	context := renderConversationContext(message, membership)
	for _, fragment := range []string{
		`membership_id="mem-1"`, `membership_version="4"`, `conversation_id="chat&amp;one"`,
		`applies_to_message="imsg-1"`, `<purpose><![CDATA[Support <customers>]]></purpose>`,
		`Never expose ]]]]><![CDATA[> tokens`, `does not grant tools, permissions, or access`,
	} {
		if !strings.Contains(context, fragment) {
			t.Fatalf("context missing %q: %s", fragment, context)
		}
	}
}

func TestInboxEnvelopeCarriesMembershipDisplaySnapshot(t *testing.T) {
	message := InboxMessage{
		ID: "imsg-1", Origin: "lark", ResponseExpectation: "required",
		Sender:       ActorRef{ExternalID: "ou-user", DisplayName: "Tester"},
		Conversation: ConversationRef{ConversationID: "oc-group", ConversationType: "group", ThreadID: "thread-1"},
		Content:      MessageContent{Text: "hello"},
	}
	item := InboxItem{ID: "inb-1", AgentID: "agent-a", MembershipID: "mem-1"}
	address := AgentAddress{ID: "addr-1"}
	membership := ConversationMembership{ID: "mem-1", DisplayName: `Product & support`, Version: 3}
	envelope := formatInboxEnvelope(message, item, address, "final_answer", &membership)
	for _, fragment := range []string{
		`<membership id="mem-1" name="Product &amp; support" version="3" />`,
		`<conversation id="oc-group" thread_id="thread-1" type="group" />`,
	} {
		if !strings.Contains(envelope, fragment) {
			t.Fatalf("envelope missing %q: %s", fragment, envelope)
		}
	}
}

func TestAttemptFreezesMembershipReplyPolicy(t *testing.T) {
	h := stoppedInboxTestHub(t)
	seedInboxHandlingState(h, "explicit")
	h.attempts["att-1"].EffectiveReplyPolicy = "final_answer"
	h.attempts["att-1"].MembershipID = "mem-1"
	h.attempts["att-1"].MembershipVersion = 2
	h.mu.Lock()
	h.finishInboxAttemptLocked(&turnState{
		turnID: "turn-1", inboxItemID: "inb-1", attemptID: "att-1", finalAnswer: "Delivered by frozen policy.",
	}, "completed", "")
	h.mu.Unlock()
	if item := h.inbox["inb-1"]; item.State != "handled" || item.Outcome != "reply" || len(h.outbox) != 1 {
		t.Fatalf("frozen policy result: item=%#v outbox=%#v", item, h.outbox)
	}
}
