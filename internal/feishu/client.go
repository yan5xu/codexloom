package feishu

import (
	"context"
	"fmt"
	"sort"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Bot struct {
	AppID          string `json:"appId"`
	OpenID         string `json:"openId,omitempty"`
	Name           string `json:"name,omitempty"`
	ActivateStatus int    `json:"activateStatus,omitempty"`
}

type Chat struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	External    bool   `json:"external"`
}

type Discovery struct {
	Bot   Bot    `json:"bot"`
	Chats []Chat `json:"chats"`
}

func Discover(ctx context.Context, appID, appSecret string) (Discovery, error) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return Discovery{}, fmt.Errorf("Feishu App ID and App Secret are required")
	}
	client := lark.NewClient(appID, appSecret)
	ch := channel.NewChannel(client, nil)
	identity := ch.GetBotIdentity(ctx)
	if identity == nil || identity.OpenID == "" {
		return Discovery{}, fmt.Errorf("Feishu did not return a bot identity; check the App ID, App Secret, and bot capability")
	}

	result := Discovery{
		Bot:   Bot{AppID: appID, OpenID: identity.OpenID, Name: identity.Name, ActivateStatus: identity.ActivateStatus},
		Chats: []Chat{},
	}
	pageToken := ""
	seenPageTokens := map[string]struct{}{}
	for {
		builder := larkim.NewListChatReqBuilder().PageSize(100).Types("group")
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		response, err := client.Im.V1.Chat.List(ctx, builder.Build())
		if err != nil {
			return Discovery{}, fmt.Errorf("list Feishu groups: %w", err)
		}
		if !response.Success() {
			return Discovery{}, fmt.Errorf("list Feishu groups: code %d: %s", response.Code, response.Msg)
		}
		if response.Data == nil {
			return Discovery{}, fmt.Errorf("list Feishu groups: response data is missing")
		}
		for _, item := range response.Data.Items {
			if item == nil || stringValue(item.ChatId) == "" {
				continue
			}
			name := stringValue(item.Name)
			if name == "" {
				name = stringValue(item.ChatId)
			}
			result.Chats = append(result.Chats, Chat{
				ID: stringValue(item.ChatId), Name: name, Description: stringValue(item.Description),
				Avatar: stringValue(item.Avatar), External: boolValue(item.External),
			})
		}
		if !boolValue(response.Data.HasMore) {
			break
		}
		nextPageToken := stringValue(response.Data.PageToken)
		if nextPageToken == "" {
			break
		}
		if _, repeated := seenPageTokens[nextPageToken]; repeated {
			break
		}
		seenPageTokens[nextPageToken] = struct{}{}
		pageToken = nextPageToken
	}
	result.Chats = normalizeChats(result.Chats)
	return result, nil
}

func normalizeChats(chats []Chat) []Chat {
	byID := make(map[string]Chat, len(chats))
	for _, chat := range chats {
		chat.ID = strings.TrimSpace(chat.ID)
		if chat.ID == "" {
			continue
		}
		chat.Name = strings.TrimSpace(chat.Name)
		chat.Description = strings.TrimSpace(chat.Description)
		chat.Avatar = strings.TrimSpace(chat.Avatar)
		if chat.Name == "" {
			chat.Name = chat.ID
		}
		if existing, ok := byID[chat.ID]; ok {
			if existing.Name == existing.ID && chat.Name != chat.ID {
				existing.Name = chat.Name
			}
			if existing.Description == "" {
				existing.Description = chat.Description
			}
			if existing.Avatar == "" {
				existing.Avatar = chat.Avatar
			}
			existing.External = existing.External || chat.External
			byID[chat.ID] = existing
			continue
		}
		byID[chat.ID] = chat
	}
	result := make([]Chat, 0, len(byID))
	for _, chat := range byID {
		result = append(result, chat)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := strings.ToLower(result[i].Name), strings.ToLower(result[j].Name)
		if left == right {
			return result[i].ID < result[j].ID
		}
		return left < right
	})
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
