package feishugw

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	loomfeishu "github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
)

func TestGatewayNormalizesLarkDomainForBothSDKClients(t *testing.T) {
	gateway, err := New(Config{
		ConnectionID: "conn-1", AddressID: "addr-1", AppID: "cli-test", AppSecret: "secret",
		Domain: loomfeishu.DomainLark, StateFile: filepath.Join(t.TempDir(), "state.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateway.cfg.Domain != loomfeishu.DomainLark || gateway.client == nil || gateway.wsClient == nil {
		t.Fatalf("gateway domain/client configuration = %q / %v / %v", gateway.cfg.Domain, gateway.client != nil, gateway.wsClient != nil)
	}
	if _, err := New(Config{
		ConnectionID: "conn-1", AddressID: "addr-1", AppID: "cli-test", AppSecret: "secret",
		Domain: "https://example.com", StateFile: filepath.Join(t.TempDir(), "state.json"),
	}); err == nil {
		t.Fatal("gateway accepted an arbitrary provider domain")
	}
}

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

func TestApplyMessageDetailsNormalizesDirectFeishuPost(t *testing.T) {
	msgType := "post"
	content := `{"title":"","content":[[{"tag":"at","user_id":"@_user_1","user_name":"大菠萝"},{"tag":"text","text":" 详细介绍："}],[],[{"tag":"text","text":"- 看好软件接管劳动预算。"},{"tag":"a","href":"https://example.com/source","text":"来源"}],[{"tag":"img","image_key":"img-key"}]],"content_v2":[[{"tag":"at","user_id":"@_user_1","user_name":"大菠萝"},{"tag":"text","text":" 详细介绍："}],[],[{"tag":"text","text":"- 看好软件接管劳动预算。"},{"tag":"a","href":"https://example.com/source","text":"来源"}],[{"tag":"img","image_key":"img-key"}]]}`
	params := hub.IngressParams{
		Content: hub.MessageContent{Text: feishuRichTextPlaceholder}, ProviderMetadata: map[string]any{},
	}
	applyMessageDetails(&params, &larkim.Message{MsgType: &msgType, Body: &larkim.MessageBody{Content: &content}})
	for _, want := range []string{"@大菠萝 详细介绍：", "- 看好软件接管劳动预算。", "[来源](https://example.com/source)", "![image](img-key)"} {
		if !strings.Contains(params.Content.Text, want) {
			t.Fatalf("normalized text %q does not contain %q", params.Content.Text, want)
		}
	}
	if len(params.Content.Attachments) != 1 || params.Content.Attachments[0].ID != "img-key" || params.Content.Attachments[0].MimeType != "image/*" {
		t.Fatalf("attachments = %#v", params.Content.Attachments)
	}
	metadata, _ := params.ProviderMetadata["contentNormalization"].(map[string]any)
	if metadata["status"] != "normalized" || metadata["format"] != "direct-post" || metadata["source"] != "message-details" {
		t.Fatalf("content normalization metadata = %#v", metadata)
	}
}

func TestIngressParamsNormalizesLocalizedFeishuPostFromEvent(t *testing.T) {
	msgType := "post"
	content := `{"zh_cn":{"title":"标题","content":[[{"tag":"text","text":"正文"}]]}}`
	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{
		MessageType: &msgType, Content: &content,
	}}}
	params := ingressParams("conn-1", "addr-1", &channeltypes.NormalizedMessage{
		MessageID: "om-1", Content: feishuRichTextPlaceholder, RawContentType: "post", RawEvent: event,
	})
	if params.Content.Text != "**标题**\n\n正文" {
		t.Fatalf("normalized localized post = %q", params.Content.Text)
	}
	metadata, _ := params.ProviderMetadata["contentNormalization"].(map[string]any)
	if metadata["format"] != "localized-post:zh_cn" || metadata["source"] != "receive-event" {
		t.Fatalf("content normalization metadata = %#v", metadata)
	}
}

func TestNormalizeFeishuPostSupportsContentV2Only(t *testing.T) {
	text, _, format, err := normalizeFeishuPostContent(`{"title":"新版","content_v2":[[{"tag":"text","text":"只有 content_v2"}]]}`)
	if err != nil {
		t.Fatal(err)
	}
	if text != "**新版**\n\n只有 content_v2" || format != "direct-post" {
		t.Fatalf("content_v2 normalization = %q / %q", text, format)
	}
}

func TestApplyMessageDetailsPreservesMalformedPostAsStructuredFallback(t *testing.T) {
	msgType := "post"
	content := `{"title":`
	params := hub.IngressParams{
		Content: hub.MessageContent{Text: feishuRichTextPlaceholder}, ProviderMetadata: map[string]any{},
	}
	applyMessageDetails(&params, &larkim.Message{MsgType: &msgType, Body: &larkim.MessageBody{Content: &content}})
	for _, want := range []string{"Feishu post normalization failed", "native content is invalid JSON", "<feishu_native_content", content} {
		if !strings.Contains(params.Content.Text, want) {
			t.Fatalf("fallback %q does not contain %q", params.Content.Text, want)
		}
	}
	metadata, _ := params.ProviderMetadata["contentNormalization"].(map[string]any)
	if metadata["status"] != "fallback" || metadata["error"] == "" {
		t.Fatalf("content normalization metadata = %#v", metadata)
	}
}

func TestFailedMessageDetailsDoNotDegradeNormalizedEventContent(t *testing.T) {
	msgType := "post"
	eventContent := `{"title":"","content":[[{"tag":"text","text":"事件正文"}]]}`
	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{
		MessageType: &msgType, Content: &eventContent,
	}}}
	params := ingressParams("conn-1", "addr-1", &channeltypes.NormalizedMessage{
		MessageID: "om-1", Content: feishuRichTextPlaceholder, RawContentType: "post", RawEvent: event,
	})
	beforeText := params.Content.Text
	beforeMetadata := params.ProviderMetadata["contentNormalization"]
	invalidDetails := `{"title":`
	applyMessageDetails(&params, &larkim.Message{MsgType: &msgType, Body: &larkim.MessageBody{Content: &invalidDetails}})
	if params.Content.Text != beforeText {
		t.Fatalf("useful event content was degraded: before=%q after=%q", beforeText, params.Content.Text)
	}
	if !reflect.DeepEqual(params.ProviderMetadata["contentNormalization"], beforeMetadata) {
		t.Fatalf("normalization metadata was degraded: before=%#v after=%#v", beforeMetadata, params.ProviderMetadata["contentNormalization"])
	}
}

func TestMergeFeishuAttachmentsDeduplicatesFileKey(t *testing.T) {
	got := mergeFeishuAttachments(
		[]hub.AttachmentRef{{ID: "img-key", Name: "screenshot.png", MimeType: "application/octet-stream"}},
		[]hub.AttachmentRef{{ID: "img-key", MimeType: "image/*"}},
	)
	if len(got) != 1 || got[0].Name != "screenshot.png" || got[0].MimeType != "image/*" {
		t.Fatalf("merged attachments = %#v", got)
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

func TestGovernedReplyTargetChecksThreadIdentity(t *testing.T) {
	threadID := "omt_topic"
	otherThreadID := "omt_other"
	message := &larkim.Message{ThreadId: &threadID}
	if err := requireFeishuReplyThread(message, threadID); err != nil {
		t.Fatal(err)
	}
	if err := requireFeishuReplyThread(message, otherThreadID); err == nil {
		t.Fatal("cross-thread reply target was accepted")
	}
	if err := requireFeishuReplyThread(&larkim.Message{}, threadID); err == nil {
		t.Fatal("reply target without thread identity was accepted")
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
