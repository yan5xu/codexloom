package feishugw

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	channelnormalize "github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/yan5xu/codex-loom/internal/hub"
)

type Config struct {
	HubURL         string
	ConnectionID   string
	AddressID      string
	AppID          string
	AppSecret      string
	ConnectorToken string
	StateFile      string
	HTTPClient     *http.Client
}

type reactionRecord struct {
	MessageID   string `json:"messageId"`
	InboxItemID string `json:"inboxItemId"`
	ReactionID  string `json:"reactionId,omitempty"`
}

type deliveryRecord struct {
	OutboxID       string                    `json:"outboxId"`
	IdempotencyKey string                    `json:"idempotencyKey"`
	Receipt        hub.OutboxDeliveryReceipt `json:"receipt"`
	UpdatedAt      string                    `json:"updatedAt"`
}

type stateFile struct {
	Reactions  map[string]*reactionRecord `json:"reactions"`
	Deliveries map[string]*deliveryRecord `json:"deliveries,omitempty"`
}

type cachedName struct {
	Value     string
	ExpiresAt time.Time
}

type Gateway struct {
	cfg       Config
	client    *lark.Client
	wsClient  *larkws.Client
	channel   channeltypes.Channel
	http      *http.Client
	connected atomic.Bool
	lastErrMu sync.RWMutex
	lastErr   string
	nameMu    sync.Mutex
	names     map[string]cachedName
	stateMu   sync.Mutex
	state     stateFile
}

func New(cfg Config) (*Gateway, error) {
	cfg.HubURL = strings.TrimRight(strings.TrimSpace(cfg.HubURL), "/")
	if cfg.HubURL == "" {
		cfg.HubURL = "http://127.0.0.1:4870"
	}
	for name, value := range map[string]string{
		"connection id": cfg.ConnectionID, "address id": cfg.AddressID,
		"app id": cfg.AppID, "app secret": cfg.AppSecret,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if cfg.StateFile == "" {
		cfg.StateFile = filepath.Join(defaultDataDir(), "gateway", "feishu-"+cfg.ConnectionID+".json")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateFile), 0o700); err != nil {
		return nil, fmt.Errorf("create gateway state directory: %w", err)
	}
	apiClient := lark.NewClient(cfg.AppID, cfg.AppSecret, lark.WithLogLevel(larkcore.LogLevelInfo))
	eventDispatcher := dispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)
	ch := channel.NewChannel(apiClient, wsClient)
	ch.OnReaction(func(context.Context, *channeltypes.ReactionEvent) error { return nil })
	eventDispatcher.OnP2MessageReadV1(func(context.Context, *larkim.P2MessageReadV1) error { return nil })
	g := &Gateway{cfg: cfg, client: apiClient, wsClient: wsClient, channel: ch, http: cfg.HTTPClient, names: map[string]cachedName{}, state: stateFile{Reactions: map[string]*reactionRecord{}, Deliveries: map[string]*deliveryRecord{}}}
	if g.http == nil {
		g.http = &http.Client{}
	}
	_ = g.loadState()
	return g, nil
}

func (g *Gateway) Run(ctx context.Context) error {
	g.channel.OnReady(func() {
		g.connected.Store(true)
		g.setError("")
		log.Printf("[feishu] WebSocket connected for %s", g.cfg.AppID)
	})
	g.channel.OnReconnecting(func() { g.connected.Store(false) })
	g.channel.OnReconnected(func() {
		g.connected.Store(true)
		g.setError("")
	})
	g.channel.OnDisconnected(func() { g.connected.Store(false) })
	g.channel.OnError(func(err error) {
		g.connected.Store(false)
		g.setError(err.Error())
		log.Printf("[feishu] WebSocket: %v", err)
	})
	g.channel.OnMessage(func(_ context.Context, msg *channeltypes.NormalizedMessage) error {
		// Acknowledge the platform event immediately; durable processing happens in Hub.
		go g.ingestMessage(ctx, msg)
		return nil
	})

	g.restoreReactionMonitors(ctx)
	go g.commandLoop(ctx)
	go g.heartbeatLoop(ctx)

	startErr := make(chan error, 1)
	go func() { startErr <- g.channel.Start(ctx) }()
	select {
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.channel.Stop(stopCtx)
		return nil
	case err := <-startErr:
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func (g *Gateway) ingestMessage(ctx context.Context, msg *channeltypes.NormalizedMessage) {
	params := ingressParams(g.cfg.ConnectionID, g.cfg.AddressID, msg)
	if params.ExternalMessageID == "" || (strings.TrimSpace(params.Content.Text) == "" && len(params.Content.Attachments) == 0) {
		return
	}
	if err := g.enrichMessageDetails(ctx, &params); err != nil {
		log.Printf("[feishu] message details %s: %v", params.ExternalMessageID, err)
	}
	var result hub.IngressResult
	if err := g.hubJSON(ctx, http.MethodPost, "/api/integrations/ingress", params, &result); err != nil {
		log.Printf("[feishu] ingest %s: %v", params.ExternalMessageID, err)
		return
	}
	if result.Ignored {
		log.Printf("[feishu] ignored %s: %s", params.ExternalMessageID, result.Reason)
		return
	}
	if result.InboxItem == nil || result.InboxItem.ID == "" {
		log.Printf("[feishu] Hub accepted %s without an inbox item", params.ExternalMessageID)
		return
	}
	record := &reactionRecord{MessageID: params.ExternalMessageID, InboxItemID: result.InboxItem.ID}
	g.stateMu.Lock()
	g.state.Reactions[record.MessageID] = record
	_ = g.saveStateLocked()
	g.stateMu.Unlock()
	go g.monitorReaction(ctx, record)
	log.Printf("[feishu] accepted %s from %s", params.ExternalMessageID, params.Sender.ExternalID)
}

func (g *Gateway) enrichMessageDetails(ctx context.Context, params *hub.IngressParams) error {
	var detailErr error
	response, err := g.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(params.ExternalMessageID).UserIdType("open_id").Build())
	if err != nil {
		detailErr = err
	} else if !response.Success() {
		detailErr = fmt.Errorf("Feishu message details failed (%d): %s", response.Code, response.Msg)
	} else if response.Data != nil && len(response.Data.Items) > 0 {
		applyMessageDetails(params, response.Data.Items[0])
	}
	if params.Sender.ExternalID != "" && (params.Sender.DisplayName == "" || params.Sender.DisplayName == params.Sender.ExternalID) {
		name, nameErr := g.resolveSenderName(ctx, params.Conversation.ConversationID, params.Sender.ExternalID)
		if name != "" {
			params.Sender.DisplayName = name
		}
		if detailErr == nil {
			detailErr = nameErr
		}
	}
	return detailErr
}

func (g *Gateway) resolveSenderName(ctx context.Context, chatID, userID string) (string, error) {
	key := chatID + "\x00" + userID
	g.nameMu.Lock()
	entry, ok := g.names[key]
	if ok && time.Now().Before(entry.ExpiresAt) {
		g.nameMu.Unlock()
		return entry.Value, nil
	}
	g.nameMu.Unlock()

	name := ""
	var resolutionErr error
	user, err := g.client.Contact.V3.User.Get(ctx, larkcontact.NewGetUserReqBuilder().UserId(userID).UserIdType("open_id").Build())
	if err != nil {
		resolutionErr = err
	} else if user.Success() && user.Data != nil && user.Data.User != nil {
		name = pointerString(user.Data.User.Name)
	} else if !user.Success() {
		resolutionErr = fmt.Errorf("Feishu user lookup failed (%d): %s", user.Code, user.Msg)
	}

	if name == "" && chatID != "" {
		iterator, iteratorErr := g.client.Im.V1.ChatMembers.GetByIterator(ctx, larkim.NewGetChatMembersReqBuilder().ChatId(chatID).MemberIdType("open_id").Limit(5000).Build())
		if iteratorErr != nil {
			resolutionErr = iteratorErr
		} else {
			for {
				hasNext, member, nextErr := iterator.Next()
				if nextErr != nil {
					resolutionErr = nextErr
					break
				}
				if !hasNext {
					break
				}
				if member != nil && pointerString(member.MemberId) == userID {
					name = pointerString(member.Name)
					break
				}
			}
		}
	}

	ttl := 6 * time.Hour
	if name == "" {
		ttl = 10 * time.Minute
	}
	g.nameMu.Lock()
	g.names[key] = cachedName{Value: name, ExpiresAt: time.Now().Add(ttl)}
	g.nameMu.Unlock()
	return name, resolutionErr
}

func applyMessageDetails(params *hub.IngressParams, message *larkim.Message) {
	if params == nil || message == nil {
		return
	}
	if message.Sender != nil {
		if value := pointerString(message.Sender.Id); value != "" {
			params.Sender.ExternalID = value
		}
		if value := pointerString(message.Sender.SenderName); value != "" {
			params.Sender.DisplayName = value
		}
		if value := pointerString(message.Sender.SenderType); value != "" {
			params.Sender.Kind = value
		}
	}
	if value := pointerString(message.MessageAppLink); value != "" {
		if params.ProviderMetadata == nil {
			params.ProviderMetadata = map[string]any{}
		}
		params.ProviderMetadata["messageAppLink"] = value
	}
}

func ingressParams(connectionID, addressID string, msg *channeltypes.NormalizedMessage) hub.IngressParams {
	params := hub.IngressParams{ConnectionID: connectionID, AddressID: addressID, ResponseExpectation: "optional"}
	if msg == nil {
		return params
	}
	params.ExternalEventID = msg.EventID
	params.ExternalMessageID = msg.MessageID
	params.Sender = hub.ActorRef{ExternalID: msg.UserID, DisplayName: msg.UserID, Kind: "human"}
	conversationType := "group"
	if msg.ChatType == "p2p" {
		conversationType = "dm"
	}
	params.Conversation = hub.ConversationRef{
		ConversationID: msg.ChatID, MessageID: msg.MessageID, ConversationType: conversationType,
	}
	params.Content.Text = strings.TrimSpace(msg.Content)
	for _, resource := range msg.Resources {
		params.Content.Attachments = append(params.Content.Attachments, hub.AttachmentRef{
			ID: resource.FileKey, Name: resource.FileName, MimeType: resourceMimeType(resource.Type),
		})
	}
	mentionIDs := make([]string, 0, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		id := mention.OpenID
		if id == "" {
			id = mention.UserID
		}
		if id != "" {
			mentionIDs = append(mentionIDs, id)
		}
	}
	params.Trigger = hub.TriggerEvidence{Direct: msg.ChatType == "p2p", Mentioned: msg.MentionedBot, ExplicitDispatch: false}
	params.ProviderMetadata = map[string]any{
		"eventType": "im.message.receive_v1", "messageType": msg.RawContentType,
		"chatType": msg.ChatType, "mentionIds": mentionIDs,
	}
	if msg.CreateTimeMs > 0 {
		params.OccurredAt = time.UnixMilli(msg.CreateTimeMs).UTC().Format(time.RFC3339Nano)
	}
	if event, ok := msg.RawEvent.(*larkim.P2MessageReceiveV1); ok && event.Event != nil && event.Event.Message != nil {
		params.Conversation.ThreadID = pointerString(event.Event.Message.ThreadId)
		if params.Conversation.ThreadID == "" {
			params.Conversation.ThreadID = pointerString(event.Event.Message.RootId)
		}
	}
	return params
}

func resourceMimeType(kind string) string {
	switch strings.ToLower(kind) {
	case "image", "sticker":
		return "image/*"
	case "audio":
		return "audio/*"
	case "video", "media":
		return "video/*"
	default:
		return "application/octet-stream"
	}
}

func (g *Gateway) commandLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := g.consumeCommands(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[hub] command stream: %v; reconnecting", err)
		}
		if !sleepContext(ctx, time.Second) {
			return
		}
	}
}

func (g *Gateway) consumeCommands(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.HubURL+"/api/integrations/connections/"+g.cfg.ConnectionID+"/commands", nil)
	if err != nil {
		return err
	}
	g.addHeaders(request)
	response, err := g.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				if err := g.handleCommand(ctx, []byte(data.String())); err != nil {
					log.Printf("[feishu] command: %v", err)
				}
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	return scanner.Err()
}

func (g *Gateway) handleCommand(ctx context.Context, data []byte) error {
	var envelope struct {
		Type string `json:"type"`
		Data struct {
			Type              string                 `json:"type"`
			OutboxItem        hub.OutboxItem         `json:"outboxItem"`
			ProviderOperation *hub.ProviderOperation `json:"providerOperation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Type != "connector/command" {
		return nil
	}
	if envelope.Data.Type == "provider_operation" && envelope.Data.ProviderOperation != nil {
		return g.handleProviderOperation(ctx, *envelope.Data.ProviderOperation)
	}
	if envelope.Data.OutboxItem.ID == "" {
		return nil
	}
	item := envelope.Data.OutboxItem
	receipts, err := g.sendOutbox(ctx, item)
	externalIDs := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		externalIDs = append(externalIDs, receipt.ExternalMessageID)
	}
	result := hub.OutboxResultParams{AttemptToken: item.AttemptToken, Success: err == nil, ExternalMessageIDs: externalIDs, DeliveryReceipts: receipts}
	if len(externalIDs) > 0 {
		result.ExternalMessageID = externalIDs[0]
	}
	if err != nil {
		result.Error = err.Error()
	}
	var completed hub.OutboxItem
	if reportErr := g.hubJSON(ctx, http.MethodPost, "/api/integrations/connections/"+g.cfg.ConnectionID+"/outbox/"+item.ID+"/result", result, &struct {
		OutboxItem *hub.OutboxItem `json:"outboxItem"`
	}{OutboxItem: &completed}); reportErr != nil {
		return fmt.Errorf("report outbox %s: %w", item.ID, reportErr)
	}
	if err != nil {
		log.Printf("[feishu] send %s: %v", item.ID, err)
		return nil
	}
	if clearErr := g.clearDeliveryRecords(item); clearErr != nil {
		log.Printf("[feishu] clear delivery journal %s: %v", item.ID, clearErr)
	}
	log.Printf("[feishu] sent %s as %s", item.ID, strings.Join(externalIDs, ","))
	return nil
}

const (
	maxFeishuMessagePageSize = 50
	maxFeishuThreadScanPages = 10
)

func (g *Gateway) handleProviderOperation(ctx context.Context, operation hub.ProviderOperation) error {
	resultValue, operationErr := g.runProviderOperation(ctx, operation)
	result := hub.ProviderOperationResultParams{
		AttemptToken: operation.AttemptToken,
		Success:      operationErr == nil,
	}
	if operationErr != nil {
		result.Error = operationErr.Error()
	} else {
		encoded, err := json.Marshal(resultValue)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode Lark operation result: %v", err)
		} else if len(encoded) > 700<<10 {
			result.Success = false
			result.Error = "Lark operation result exceeds the managed result limit; retry with a smaller --limit or narrower time range"
		} else {
			result.Result = encoded
		}
	}
	var completed hub.ProviderOperation
	if err := g.hubJSON(ctx, http.MethodPost,
		"/api/integrations/connections/"+g.cfg.ConnectionID+"/provider-operations/"+operation.ID+"/result",
		result, &struct {
			Operation *hub.ProviderOperation `json:"operation"`
		}{Operation: &completed}); err != nil {
		return fmt.Errorf("report Lark operation %s: %w", operation.ID, err)
	}
	if !result.Success {
		log.Printf("[feishu] provider operation %s failed: %s", operation.ID, result.Error)
		return nil
	}
	log.Printf("[feishu] completed provider operation %s: %s/%s", operation.ID, operation.Resource, operation.Action)
	return nil
}

func (g *Gateway) runProviderOperation(ctx context.Context, operation hub.ProviderOperation) (any, error) {
	if operation.Provider != "lark" || operation.Resource != "messages" {
		return nil, fmt.Errorf("unsupported managed Lark operation: %s %s/%s", operation.Provider, operation.Resource, operation.Action)
	}
	chatID := operationArgumentString(operation.Arguments, "chatId")
	if chatID == "" {
		return nil, fmt.Errorf("chatId is required")
	}
	limit, err := operationArgumentInt(operation.Arguments, "limit", 20)
	if err != nil || limit < 1 || limit > maxFeishuMessagePageSize {
		return nil, fmt.Errorf("limit must be an integer from 1 to %d", maxFeishuMessagePageSize)
	}

	switch operation.Action {
	case "get":
		message, err := g.getFeishuMessage(ctx, operationArgumentString(operation.Arguments, "messageId"), chatID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": []*larkim.Message{message}}, nil
	case "list":
		threadID := operationArgumentString(operation.Arguments, "threadId")
		page, err := g.listFeishuMessagePage(ctx, operation.Arguments, operationArgumentString(operation.Arguments, "pageToken"), limit)
		if err != nil {
			return nil, err
		}
		if threadID == "" {
			return page, nil
		}
		return feishuMessagePageResult(page, map[string]any{
			"mode": "native_thread", "chat_id": chatID, "thread_id": threadID,
			"pages": 1, "max_pages": 1, "truncated": pointerBool(page.HasMore),
		}), nil
	case "replies":
		return g.readFeishuReplies(ctx, operation.Arguments, chatID, limit)
	default:
		return nil, fmt.Errorf("unsupported managed Lark operation: messages/%s", operation.Action)
	}
}

func (g *Gateway) getFeishuMessage(ctx context.Context, messageID, chatID string) (*larkim.Message, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("messageId is required")
	}
	response, err := g.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(messageID).UserIdType("open_id").Build())
	if err != nil {
		return nil, err
	}
	if !response.Success() {
		return nil, fmt.Errorf("Lark message get failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil || len(response.Data.Items) == 0 || response.Data.Items[0] == nil {
		return nil, fmt.Errorf("Lark message get returned no message")
	}
	message := response.Data.Items[0]
	if err := requireFeishuMessageChat(message, chatID); err != nil {
		return nil, err
	}
	return message, nil
}

func (g *Gateway) listFeishuMessagePage(ctx context.Context, arguments map[string]any, pageToken string, pageSize int) (*larkim.ListMessageRespData, error) {
	chatID := operationArgumentString(arguments, "chatId")
	containerType, containerID := feishuMessageContainer(arguments)
	builder := larkim.NewListMessageReqBuilder().ContainerIdType(containerType).ContainerId(containerID).PageSize(pageSize)
	if value := operationArgumentString(arguments, "startTime"); value != "" {
		builder.StartTime(value)
	}
	if value := operationArgumentString(arguments, "endTime"); value != "" {
		builder.EndTime(value)
	}
	if value := operationArgumentString(arguments, "sort"); value != "" {
		builder.SortType(value)
	}
	if pageToken != "" {
		builder.PageToken(pageToken)
	}
	if value, ok := operationArgumentBool(arguments, "threadRootOnly"); ok && containerType == "chat" {
		builder.OnlyThreadRootMessages(value)
	}
	response, err := g.client.Im.V1.Message.List(ctx, builder.Build())
	if err != nil {
		return nil, err
	}
	if !response.Success() {
		return nil, fmt.Errorf("Lark message list failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil {
		return &larkim.ListMessageRespData{}, nil
	}
	for _, message := range response.Data.Items {
		if err := requireFeishuMessageChat(message, chatID); err != nil {
			return nil, err
		}
	}
	return response.Data, nil
}

func (g *Gateway) readFeishuReplies(ctx context.Context, arguments map[string]any, chatID string, limit int) (any, error) {
	target, err := g.getFeishuMessage(ctx, operationArgumentString(arguments, "messageId"), chatID)
	if err != nil {
		return nil, err
	}
	rootID := pointerString(target.RootId)
	if rootID == "" {
		rootID = pointerString(target.MessageId)
	}
	root := target
	if rootID != pointerString(target.MessageId) {
		root, err = g.getFeishuMessage(ctx, rootID, chatID)
		if err != nil {
			return nil, fmt.Errorf("read Lark thread root %s: %w", rootID, err)
		}
	}
	threadID := pointerString(target.ThreadId)
	if threadID == "" {
		threadID = pointerString(root.ThreadId)
	}
	if threadID == "" {
		return nil, fmt.Errorf("Lark message %s has no thread_id", pointerString(target.MessageId))
	}
	scanArguments := map[string]any{
		"chatId": chatID, "threadId": threadID, "sort": "ByCreateTimeAsc",
	}
	result, err := g.readFeishuThreadReplies(ctx, scanArguments, chatID, threadID, rootID, limit)
	if err != nil {
		return nil, err
	}
	result["root"] = root
	return result, nil
}

func (g *Gateway) readFeishuThreadReplies(ctx context.Context, arguments map[string]any, chatID, threadID, rootID string, limit int) (map[string]any, error) {
	readArguments := make(map[string]any, len(arguments)+1)
	for key, value := range arguments {
		readArguments[key] = value
	}
	pageToken := operationArgumentString(readArguments, "pageToken")
	messages := make([]*larkim.Message, 0, limit)
	hasMore := false
	omitted := false
	pages := 0
	for pages < maxFeishuThreadScanPages && len(messages) < limit {
		page, err := g.listFeishuMessagePage(ctx, readArguments, pageToken, maxFeishuMessagePageSize)
		if err != nil {
			return nil, err
		}
		pages++
		for _, message := range page.Items {
			if rootID != "" && pointerString(message.MessageId) == rootID {
				continue
			}
			if len(messages) < limit {
				messages = append(messages, message)
			} else {
				omitted = true
			}
		}
		hasMore = pointerBool(page.HasMore)
		pageToken = pointerString(page.PageToken)
		if !hasMore || pageToken == "" {
			break
		}
	}
	result := map[string]any{
		"items":      messages,
		"has_more":   hasMore,
		"page_token": pageToken,
		"loom_scan": map[string]any{
			"mode":    "native_thread",
			"chat_id": chatID, "thread_id": threadID, "root_id": rootID,
			"pages": pages, "max_pages": maxFeishuThreadScanPages,
			"truncated": hasMore || omitted,
		},
	}
	return result, nil
}

func feishuMessageContainer(arguments map[string]any) (string, string) {
	if threadID := operationArgumentString(arguments, "threadId"); threadID != "" {
		return "thread", threadID
	}
	return "chat", operationArgumentString(arguments, "chatId")
}

func feishuMessagePageResult(page *larkim.ListMessageRespData, loomScan map[string]any) map[string]any {
	result := map[string]any{"items": []*larkim.Message{}}
	if page != nil {
		result["items"] = page.Items
		if page.HasMore != nil {
			result["has_more"] = *page.HasMore
		}
		if page.PageToken != nil {
			result["page_token"] = *page.PageToken
		}
	}
	if loomScan != nil {
		result["loom_scan"] = loomScan
	}
	return result
}

func requireFeishuMessageChat(message *larkim.Message, chatID string) error {
	if message == nil || pointerString(message.ChatId) == "" {
		return fmt.Errorf("Lark returned a message without chat_id")
	}
	if pointerString(message.ChatId) != chatID {
		return fmt.Errorf("Lark message belongs to chat %s, not authorized chat %s", pointerString(message.ChatId), chatID)
	}
	return nil
}

func operationArgumentString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

func operationArgumentInt(arguments map[string]any, key string, fallback int) (int, error) {
	value, ok := arguments[key]
	if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return fallback, nil
	}
	switch typed := value.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s is not an integer", key)
		}
		return int(typed), nil
	case int:
		return typed, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	}
}

func operationArgumentBool(arguments map[string]any, key string) (bool, bool) {
	value, ok := arguments[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func pointerBool(value *bool) bool {
	return value != nil && *value
}

func (g *Gateway) sendOutbox(ctx context.Context, item hub.OutboxItem) ([]hub.OutboxDeliveryReceipt, error) {
	parts, err := outboundParts(item.Content)
	if err != nil {
		return nil, err
	}
	receipts := make([]hub.OutboxDeliveryReceipt, 0, len(parts))
	for index, part := range parts {
		if receipt, ok := g.deliveryReceipt(item, index, part); ok {
			receipts = append(receipts, receipt)
			continue
		}
		msgType, content := part.MsgType, part.Content
		receipt := hub.OutboxDeliveryReceipt{Kind: "text"}
		if part.Attachment != nil {
			receipt.Kind = "attachment"
			receipt.ArtifactID = part.Attachment.ID
			if strings.HasPrefix(part.Attachment.MimeType, "image/") {
				imageKey, err := g.uploadImage(ctx, part.Attachment.Path)
				if err != nil {
					return receipts, err
				}
				receipt.ExternalAttachmentID = imageKey
				msgType = "image"
				content, err = json.Marshal(map[string]string{"image_key": imageKey})
				if err != nil {
					return receipts, err
				}
			} else {
				fileKey, err := g.uploadFile(ctx, *part.Attachment)
				if err != nil {
					return receipts, err
				}
				receipt.ExternalAttachmentID = fileKey
				msgType = "file"
				content, err = json.Marshal(map[string]string{"file_key": fileKey})
				if err != nil {
					return receipts, err
				}
			}
		}
		externalID, err := g.sendMessage(ctx, item, msgType, content, feishuIdempotencyUUID(item.IdempotencyKey, index))
		if err != nil {
			return receipts, err
		}
		receipt.ExternalMessageID = externalID
		receipts = append(receipts, receipt)
		if err := g.rememberDeliveryReceipt(item, index, receipt); err != nil {
			return receipts, fmt.Errorf("persist Feishu delivery receipt: %w", err)
		}
	}
	return receipts, nil
}

func feishuIdempotencyUUID(idempotencyKey string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("codexloom:feishu:%s:%d", idempotencyKey, index)))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func deliveryPartKey(item hub.OutboxItem, index int) string {
	return fmt.Sprintf("%s:%d", item.ID, index)
}

func (g *Gateway) deliveryReceipt(item hub.OutboxItem, index int, part outboundPart) (hub.OutboxDeliveryReceipt, bool) {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	record := g.state.Deliveries[deliveryPartKey(item, index)]
	if record == nil || record.OutboxID != item.ID || record.IdempotencyKey != item.IdempotencyKey || record.Receipt.ExternalMessageID == "" {
		return hub.OutboxDeliveryReceipt{}, false
	}
	if part.Attachment == nil {
		if record.Receipt.Kind != "text" {
			return hub.OutboxDeliveryReceipt{}, false
		}
	} else if record.Receipt.Kind != "attachment" || record.Receipt.ArtifactID != part.Attachment.ID {
		return hub.OutboxDeliveryReceipt{}, false
	}
	return record.Receipt, true
}

func (g *Gateway) rememberDeliveryReceipt(item hub.OutboxItem, index int, receipt hub.OutboxDeliveryReceipt) error {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	if g.state.Deliveries == nil {
		g.state.Deliveries = map[string]*deliveryRecord{}
	}
	g.state.Deliveries[deliveryPartKey(item, index)] = &deliveryRecord{
		OutboxID: item.ID, IdempotencyKey: item.IdempotencyKey, Receipt: receipt,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	return g.saveStateLocked()
}

func (g *Gateway) clearDeliveryRecords(item hub.OutboxItem) error {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	prefix := item.ID + ":"
	for key := range g.state.Deliveries {
		if strings.HasPrefix(key, prefix) {
			delete(g.state.Deliveries, key)
		}
	}
	return g.saveStateLocked()
}

func (g *Gateway) sendMessage(ctx context.Context, item hub.OutboxItem, msgType string, content []byte, idempotencyKey string) (string, error) {
	if item.Conversation.MessageID != "" {
		if item.InboxItemID == "" && item.MembershipID != "" {
			target, err := g.getFeishuMessage(ctx, item.Conversation.MessageID, item.Conversation.ConversationID)
			if err != nil {
				return "", fmt.Errorf("validate governed Feishu reply target: %w", err)
			}
			if err := requireFeishuReplyThread(target, item.Conversation.ThreadID); err != nil {
				return "", err
			}
		}
		body := larkim.NewReplyMessageReqBodyBuilder().MsgType(msgType).Content(string(content)).Uuid(idempotencyKey)
		if item.Conversation.ThreadID != "" {
			body.ReplyInThread(true)
		}
		response, err := g.client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().MessageId(item.Conversation.MessageID).Body(body.Build()).Build())
		if err != nil {
			return "", err
		}
		if !response.Success() {
			return "", fmt.Errorf("Feishu reply failed (%d): %s", response.Code, response.Msg)
		}
		if response.Data == nil || response.Data.MessageId == nil {
			return "", fmt.Errorf("Feishu reply returned no message id")
		}
		return *response.Data.MessageId, nil
	}
	response, err := g.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(
		larkim.NewCreateMessageReqBodyBuilder().ReceiveId(item.Conversation.ConversationID).MsgType(msgType).Content(string(content)).Uuid(idempotencyKey).Build(),
	).Build())
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("Feishu send failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil || response.Data.MessageId == nil {
		return "", fmt.Errorf("Feishu send returned no message id")
	}
	return *response.Data.MessageId, nil
}

func requireFeishuReplyThread(message *larkim.Message, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	actual := pointerString(message.ThreadId)
	if actual == "" {
		return fmt.Errorf("Feishu reply target has no thread_id; expected %s", threadID)
	}
	if actual != threadID {
		return fmt.Errorf("Feishu reply target belongs to thread %s, not requested thread %s", actual, threadID)
	}
	return nil
}

type outboundPart struct {
	MsgType    string
	Content    []byte
	Attachment *hub.AttachmentRef
}

func outboundParts(content hub.MessageContent) ([]outboundPart, error) {
	parts := make([]outboundPart, 0, 1+len(content.Attachments))
	if strings.TrimSpace(content.Text) != "" {
		msgType, payload, err := markdownMessagePayload(content.Text)
		if err != nil {
			return nil, err
		}
		parts = append(parts, outboundPart{MsgType: msgType, Content: payload})
	}
	for _, value := range content.Attachments {
		attachment, err := localAttachment(value)
		if err != nil {
			return nil, err
		}
		parts = append(parts, outboundPart{Attachment: attachment})
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("Feishu outbox content is empty")
	}
	return parts, nil
}

func markdownMessagePayload(markdown string) (string, []byte, error) {
	content, err := channelnormalize.SimpleMarkdownToPost("", markdown, nil)
	if err != nil {
		return "", nil, err
	}
	return "post", []byte(content), nil
}

func localAttachment(value hub.AttachmentRef) (*hub.AttachmentRef, error) {
	attachment := value
	attachment.Path = strings.TrimSpace(attachment.Path)
	if attachment.Path == "" {
		return nil, fmt.Errorf("Feishu attachment requires a local path")
	}
	mimeType := strings.ToLower(strings.TrimSpace(attachment.MimeType))
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Path)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	info, err := os.Stat(attachment.Path)
	if err != nil {
		return nil, fmt.Errorf("read Feishu attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Feishu attachment is not a regular file")
	}
	limit := int64(25 << 20)
	if strings.HasPrefix(mimeType, "image/") {
		limit = 10 << 20
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("Feishu attachment %s exceeds the %d MB limit", attachment.Name, limit>>20)
	}
	attachment.MimeType = mimeType
	attachment.Size = info.Size()
	return &attachment, nil
}

func (g *Gateway) uploadImage(ctx context.Context, imagePath string) (string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	response, err := g.client.Im.V1.Image.Create(ctx, larkim.NewCreateImageReqBuilder().Body(
		larkim.NewCreateImageReqBodyBuilder().ImageType("message").Image(file).Build(),
	).Build())
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("Feishu image upload failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil || response.Data.ImageKey == nil || strings.TrimSpace(*response.Data.ImageKey) == "" {
		return "", fmt.Errorf("Feishu image upload returned no image key")
	}
	return *response.Data.ImageKey, nil
}

func (g *Gateway) uploadFile(ctx context.Context, attachment hub.AttachmentRef) (string, error) {
	file, err := os.Open(attachment.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	name := strings.TrimSpace(attachment.Name)
	if name == "" {
		name = filepath.Base(attachment.Path)
	}
	response, err := g.client.Im.V1.File.Create(ctx, larkim.NewCreateFileReqBuilder().Body(
		larkim.NewCreateFileReqBodyBuilder().FileType(feishuFileType(name)).FileName(name).File(file).Build(),
	).Build())
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("Feishu file upload failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil || response.Data.FileKey == nil {
		return "", fmt.Errorf("Feishu file upload returned no file key")
	}
	return *response.Data.FileKey, nil
}

func feishuFileType(name string) string {
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	switch extension {
	case "opus", "mp4", "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx":
		return extension
	default:
		return "stream"
	}
}

func (g *Gateway) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		g.sendHeartbeat(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (g *Gateway) sendHeartbeat(ctx context.Context) {
	status := "connecting"
	if g.connected.Load() {
		status = "connected"
	} else if g.errorText() != "" {
		status = "degraded"
	}
	body := hub.ConnectionHeartbeatParams{
		Status: status, Error: g.errorText(),
		Capabilities: []string{"receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"},
	}
	var ignored map[string]any
	if err := g.hubJSON(ctx, http.MethodPost, "/api/integrations/connections/"+g.cfg.ConnectionID+"/heartbeat", body, &ignored); err != nil && ctx.Err() == nil {
		log.Printf("[hub] heartbeat: %v", err)
	}
}

func (g *Gateway) monitorReaction(ctx context.Context, record *reactionRecord) {
	if err := g.ensureReaction(ctx, record); err != nil {
		log.Printf("[feishu] reaction %s: %v", record.MessageID, err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var entry hub.InboxEntry
		if err := g.hubJSON(ctx, http.MethodGet, "/api/inbox/"+record.InboxItemID, nil, &entry); err == nil && reactionComplete(entry) {
			if err := g.removeReaction(ctx, record); err != nil {
				log.Printf("[feishu] remove reaction %s: %v", record.MessageID, err)
			} else {
				log.Printf("[feishu] removed eyes from %s", record.MessageID)
			}
			g.stateMu.Lock()
			delete(g.state.Reactions, record.MessageID)
			_ = g.saveStateLocked()
			g.stateMu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func reactionComplete(entry hub.InboxEntry) bool {
	if entry.Item.State == "failed" || entry.Item.State == "cancelled" {
		return true
	}
	if entry.Item.State != "handled" {
		return false
	}
	if entry.Item.Outcome == "no_reply" {
		return true
	}
	return entry.Item.Outcome == "reply" && entry.Outbox != nil && entry.Outbox.State == "sent"
}

func (g *Gateway) ensureReaction(ctx context.Context, record *reactionRecord) error {
	if record.ReactionID != "" {
		return nil
	}
	response, err := g.client.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().MessageId(record.MessageID).Body(
		larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType("OnIt").Build()).Build(),
	).Build())
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("Feishu reaction failed (%d): %s", response.Code, response.Msg)
	}
	if response.Data == nil || response.Data.ReactionId == nil {
		return fmt.Errorf("Feishu reaction returned no reaction id")
	}
	record.ReactionID = *response.Data.ReactionId
	g.stateMu.Lock()
	_ = g.saveStateLocked()
	g.stateMu.Unlock()
	log.Printf("[feishu] added eyes to %s", record.MessageID)
	return nil
}

func (g *Gateway) removeReaction(ctx context.Context, record *reactionRecord) error {
	if record.ReactionID == "" {
		return nil
	}
	response, err := g.client.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().MessageId(record.MessageID).ReactionId(record.ReactionID).Build())
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("Feishu remove reaction failed (%d): %s", response.Code, response.Msg)
	}
	return nil
}

func (g *Gateway) restoreReactionMonitors(ctx context.Context) {
	g.stateMu.Lock()
	records := make([]*reactionRecord, 0, len(g.state.Reactions))
	for _, record := range g.state.Reactions {
		records = append(records, record)
	}
	g.stateMu.Unlock()
	for _, record := range records {
		go g.monitorReaction(ctx, record)
	}
}

func (g *Gateway) hubJSON(ctx context.Context, method, resource string, body, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, g.cfg.HubURL+resource, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	g.addHeaders(request)
	response, err := g.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &failure)
		if failure.Error == "" {
			failure.Error = strings.TrimSpace(string(payload))
		}
		return fmt.Errorf("Hub HTTP %d: %s", response.StatusCode, failure.Error)
	}
	if target != nil && len(payload) > 0 {
		return json.Unmarshal(payload, target)
	}
	return nil
}

func (g *Gateway) addHeaders(request *http.Request) {
	if g.cfg.ConnectorToken != "" {
		request.Header.Set("X-Codex-Loom-Connector-Token", g.cfg.ConnectorToken)
	}
}

func (g *Gateway) setError(value string) {
	g.lastErrMu.Lock()
	g.lastErr = strings.TrimSpace(value)
	g.lastErrMu.Unlock()
}

func (g *Gateway) errorText() string {
	g.lastErrMu.RLock()
	defer g.lastErrMu.RUnlock()
	return g.lastErr
}

func (g *Gateway) loadState() error {
	payload, err := os.ReadFile(g.cfg.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, &g.state); err != nil {
		return err
	}
	if g.state.Reactions == nil {
		g.state.Reactions = map[string]*reactionRecord{}
	}
	if g.state.Deliveries == nil {
		g.state.Deliveries = map[string]*deliveryRecord{}
	}
	return nil
}

func (g *Gateway) saveStateLocked() error {
	payload, err := json.MarshalIndent(g.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := g.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.cfg.StateFile)
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func defaultDataDir() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_LOOM_DATA")); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, ".codex-loom")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	return filepath.Join(home, ".codex-hub")
}
