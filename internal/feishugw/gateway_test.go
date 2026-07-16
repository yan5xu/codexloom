package feishugw

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func TestGatewayAlwaysConfiguresFeishuEventDispatcher(t *testing.T) {
	gateway, err := New(Config{
		ConnectionID: "conn-1", AddressID: "addr-1", AppID: "cli-test", AppSecret: "secret",
		StateFile: filepath.Join(t.TempDir(), "state.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateway.wsClient == nil || gateway.wsClient.EventHandler() == nil {
		t.Fatal("Feishu WebSocket client must have an event dispatcher")
	}
	for _, eventType := range []string{
		"im.message.reaction.created_v1",
		"im.message.reaction.deleted_v1",
		"im.message.message_read_v1",
	} {
		payload := []byte(fmt.Sprintf(`{"schema":"2.0","header":{"event_id":"evt-1","event_type":%q,"create_time":"1700000000000"},"event":{}}`, eventType))
		if _, err := gateway.wsClient.EventHandler().Do(context.Background(), payload); err != nil {
			t.Fatalf("%s must be acknowledged: %v", eventType, err)
		}
	}
}

func TestIngressParamsPreserveFeishuMessageSemantics(t *testing.T) {
	threadID := "omt_thread"
	message := &channeltypes.NormalizedMessage{
		EventID: "evt-1", MessageID: "om-1", ChatID: "oc-1", ChatType: "group",
		UserID: "ou-human", Content: "hello", RawContentType: "file", MentionedBot: true,
		Mentions:     []channeltypes.Mention{{OpenID: "ou-bot", IsBot: true}},
		Resources:    []channeltypes.Resource{{Type: "file", FileKey: "file-1", FileName: "brief.pdf"}},
		CreateTimeMs: 1_700_000_000_000,
		RawEvent:     &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{ThreadId: &threadID}}},
	}
	got := ingressParams("conn-1", "addr-1", message)
	if got.ExternalEventID != "evt-1" || got.ExternalMessageID != "om-1" || got.Sender.ExternalID != "ou-human" {
		t.Fatalf("identity projection = %#v", got)
	}
	if got.Conversation.ConversationID != "oc-1" || got.Conversation.ThreadID != threadID || got.Conversation.ConversationType != "group" {
		t.Fatalf("conversation projection = %#v", got.Conversation)
	}
	if !got.Trigger.Mentioned || got.Trigger.Direct || len(got.Content.Attachments) != 1 || got.Content.Attachments[0].Name != "brief.pdf" {
		t.Fatalf("content/trigger projection = %#v / %#v", got.Content, got.Trigger)
	}
}

func TestApplyMessageDetailsAddsHumanReadableSender(t *testing.T) {
	senderID := "ou-human"
	senderName := "Xu Changpeng"
	senderType := "user"
	messageLink := "https://applink.feishu.cn/client/chat/open"
	params := hub.IngressParams{
		Sender:           hub.ActorRef{ExternalID: senderID, DisplayName: senderID, Kind: "human"},
		ProviderMetadata: map[string]any{},
	}
	applyMessageDetails(&params, &larkim.Message{
		Sender:         &larkim.Sender{Id: &senderID, SenderName: &senderName, SenderType: &senderType},
		MessageAppLink: &messageLink,
	})
	if params.Sender.DisplayName != senderName || params.Sender.Kind != senderType {
		t.Fatalf("sender = %#v", params.Sender)
	}
	if params.ProviderMetadata["messageAppLink"] != messageLink {
		t.Fatalf("provider metadata = %#v", params.ProviderMetadata)
	}
}

func TestManagedReadChecksChatAndThreadIdentity(t *testing.T) {
	chatID := "oc_team"
	otherChatID := "oc_other"
	messageID := "om_reply"
	message := &larkim.Message{
		MessageId: &messageID, ChatId: &chatID,
	}
	if err := requireFeishuMessageChat(message, chatID); err != nil {
		t.Fatal(err)
	}
	if err := requireFeishuMessageChat(message, otherChatID); err == nil {
		t.Fatal("cross-chat message was accepted")
	}
}

func TestManagedReadUsesNativeThreadContainer(t *testing.T) {
	containerType, containerID := feishuMessageContainer(map[string]any{
		"chatId": "oc_team", "threadId": "omt_topic",
	})
	if containerType != "thread" || containerID != "omt_topic" {
		t.Fatalf("thread container = %s/%s", containerType, containerID)
	}
	containerType, containerID = feishuMessageContainer(map[string]any{"chatId": "oc_team"})
	if containerType != "chat" || containerID != "oc_team" {
		t.Fatalf("chat container = %s/%s", containerType, containerID)
	}
}

func TestManagedReadParsesJSONOperationArguments(t *testing.T) {
	arguments := map[string]any{"limit": float64(40), "threadRootOnly": true, "chatId": " oc_team "}
	limit, err := operationArgumentInt(arguments, "limit", 20)
	if err != nil || limit != 40 {
		t.Fatalf("limit = %d, err=%v", limit, err)
	}
	if value, ok := operationArgumentBool(arguments, "threadRootOnly"); !ok || !value {
		t.Fatalf("threadRootOnly = %v, ok=%v", value, ok)
	}
	if got := operationArgumentString(arguments, "chatId"); got != "oc_team" {
		t.Fatalf("chatId = %q", got)
	}
}

func TestManagedReadRejectsPageSizeAboveFeishuLimit(t *testing.T) {
	_, err := (&Gateway{}).runProviderOperation(context.Background(), hub.ProviderOperation{
		Provider: "lark", Resource: "messages", Action: "list",
		Arguments: map[string]any{"chatId": "oc_team", "limit": float64(51)},
	})
	if err == nil || !strings.Contains(err.Error(), "1 to 50") {
		t.Fatalf("page size error = %v", err)
	}
}

func TestReactionCompletesOnlyAfterTerminalDelivery(t *testing.T) {
	tests := []struct {
		name  string
		entry hub.InboxEntry
		want  bool
	}{
		{name: "queued", entry: hub.InboxEntry{Item: hub.InboxItem{State: "queued"}}},
		{name: "failed", entry: hub.InboxEntry{Item: hub.InboxItem{State: "failed"}}, want: true},
		{name: "no reply", entry: hub.InboxEntry{Item: hub.InboxItem{State: "handled", Outcome: "no_reply"}}, want: true},
		{name: "reply pending", entry: hub.InboxEntry{Item: hub.InboxItem{State: "handled", Outcome: "reply"}, Outbox: &hub.OutboxItem{State: "pending"}}},
		{name: "reply sent", entry: hub.InboxEntry{Item: hub.InboxItem{State: "handled", Outcome: "reply"}, Outbox: &hub.OutboxItem{State: "sent"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reactionComplete(test.entry); got != test.want {
				t.Fatalf("reactionComplete() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMarkdownMessagePayloadUsesFeishuPost(t *testing.T) {
	markdown := "**Result**\n\n- first\n- second"
	msgType, content, err := markdownMessagePayload(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if msgType != "post" {
		t.Fatalf("msg type = %q, want post", msgType)
	}
	var post struct {
		ZhCN struct {
			Content [][]struct {
				Tag  string `json:"tag"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"zh_cn"`
	}
	if err := json.Unmarshal(content, &post); err != nil {
		t.Fatal(err)
	}
	if len(post.ZhCN.Content) != 1 || len(post.ZhCN.Content[0]) != 1 {
		t.Fatalf("post content = %#v", post.ZhCN.Content)
	}
	if element := post.ZhCN.Content[0][0]; element.Tag != "md" || element.Text != markdown {
		t.Fatalf("post element = %#v", element)
	}
}

func TestOutboundPartsPreserveTextAndAttachmentOrder(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(filePath, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	parts, err := outboundParts(hub.MessageContent{
		Text: "summary",
		Attachments: []hub.AttachmentRef{
			{Name: "photo.png", Path: imagePath},
			{Name: "report.pdf", Path: filePath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || parts[0].MsgType != "post" || parts[0].Attachment != nil {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[1].Attachment == nil || parts[1].Attachment.MimeType != "image/png" || parts[1].Attachment.Size != 3 {
		t.Fatalf("image part = %#v", parts[1])
	}
	if parts[2].Attachment == nil || parts[2].Attachment.MimeType != "application/pdf" || parts[2].Attachment.Size != 3 {
		t.Fatalf("file part = %#v", parts[2])
	}
}

func TestFeishuIdempotencyUUIDIsStableAndValid(t *testing.T) {
	first := feishuIdempotencyUUID("reply:inb-1", 0)
	if first != feishuIdempotencyUUID("reply:inb-1", 0) {
		t.Fatal("Feishu idempotency UUID is not stable")
	}
	if first == feishuIdempotencyUUID("reply:inb-1", 1) {
		t.Fatal("different message parts share an idempotency UUID")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first) {
		t.Fatalf("invalid UUID: %s", first)
	}
}

func TestDeliveryJournalSurvivesGatewayRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	newGateway := func() *Gateway {
		gateway, err := New(Config{
			ConnectionID: "conn-1", AddressID: "addr-1", AppID: "cli-test", AppSecret: "secret", StateFile: statePath,
		})
		if err != nil {
			t.Fatal(err)
		}
		return gateway
	}
	item := hub.OutboxItem{ID: "out-1", IdempotencyKey: "delivery-1"}
	part := outboundPart{MsgType: "post", Content: []byte(`{"zh_cn":{}}`)}
	receipt := hub.OutboxDeliveryReceipt{Kind: "text", ExternalMessageID: "om-1"}
	if err := newGateway().rememberDeliveryReceipt(item, 0, receipt); err != nil {
		t.Fatal(err)
	}
	restarted := newGateway()
	got, ok := restarted.deliveryReceipt(item, 0, part)
	if !ok || got.ExternalMessageID != "om-1" {
		t.Fatalf("journal receipt = %#v, ok=%v", got, ok)
	}
	if err := restarted.clearDeliveryRecords(item); err != nil {
		t.Fatal(err)
	}
	if _, ok := newGateway().deliveryReceipt(item, 0, part); ok {
		t.Fatal("completed delivery remained in journal")
	}
}
