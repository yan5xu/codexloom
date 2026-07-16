package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Identity struct {
	AppID     string `json:"appId"`
	TeamID    string `json:"teamId"`
	TeamName  string `json:"teamName"`
	BotID     string `json:"botId"`
	BotUserID string `json:"botUserId"`
	BotName   string `json:"botName"`
}

type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
	Member      bool   `json:"member"`
	Archived    bool   `json:"archived"`
}

type Discovery struct {
	Identity Identity  `json:"identity"`
	Channels []Channel `json:"channels"`
}

type APIError struct {
	Method   string
	Code     string
	Needed   []string
	Provided []string
}

func (e *APIError) Error() string {
	if len(e.Needed) > 0 {
		return fmt.Sprintf("Slack API %s: %s (add scopes: %s)", e.Method, e.Code, strings.Join(e.Needed, ", "))
	}
	return fmt.Sprintf("Slack API %s: %s", e.Method, e.Code)
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func Discover(ctx context.Context, botToken, appToken string) (Discovery, error) {
	return (&Client{}).Discover(ctx, botToken, appToken)
}

func (c *Client) Discover(ctx context.Context, botToken, appToken string) (Discovery, error) {
	botToken = strings.TrimSpace(botToken)
	appToken = strings.TrimSpace(appToken)
	if botToken == "" || appToken == "" {
		return Discovery{}, fmt.Errorf("Slack Bot token and App token are required")
	}
	var auth struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	if err := c.call(ctx, botToken, "auth.test", nil, &auth); err != nil {
		return Discovery{}, err
	}
	result := Discovery{Identity: Identity{
		TeamID: auth.TeamID, TeamName: auth.Team, BotID: auth.BotID,
		BotUserID: auth.UserID, BotName: auth.User,
	}, Channels: []Channel{}}

	var bot struct {
		Bot struct {
			AppID  string `json:"app_id"`
			Name   string `json:"name"`
			UserID string `json:"user_id"`
		} `json:"bot"`
	}
	if err := c.call(ctx, botToken, "bots.info", url.Values{"bot": {auth.BotID}}, &bot); err != nil {
		return result, err
	}
	result.Identity.AppID = strings.TrimSpace(bot.Bot.AppID)
	if bot.Bot.Name != "" {
		result.Identity.BotName = bot.Bot.Name
	}
	if bot.Bot.UserID != "" {
		result.Identity.BotUserID = bot.Bot.UserID
	}
	if result.Identity.AppID == "" {
		return result, fmt.Errorf("Slack did not return an App ID for bot %s", auth.BotID)
	}

	if err := c.call(ctx, appToken, "apps.connections.open", nil, &struct{}{}); err != nil {
		return result, err
	}

	cursor := ""
	seenCursors := map[string]struct{}{}
	for {
		params := url.Values{
			"types":            {"public_channel,private_channel"},
			"exclude_archived": {"true"},
			"limit":            {"200"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var page struct {
			Channels []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				IsPrivate  bool   `json:"is_private"`
				IsMember   bool   `json:"is_member"`
				IsArchived bool   `json:"is_archived"`
				Purpose    struct {
					Value string `json:"value"`
				} `json:"purpose"`
			} `json:"channels"`
			ResponseMetadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		if err := c.call(ctx, botToken, "conversations.list", params, &page); err != nil {
			return result, err
		}
		for _, item := range page.Channels {
			if strings.TrimSpace(item.ID) == "" {
				continue
			}
			result.Channels = append(result.Channels, Channel{
				ID: strings.TrimSpace(item.ID), Name: strings.TrimSpace(item.Name),
				Description: strings.TrimSpace(item.Purpose.Value), Private: item.IsPrivate,
				Member: item.IsMember, Archived: item.IsArchived,
			})
		}
		next := strings.TrimSpace(page.ResponseMetadata.NextCursor)
		if next == "" {
			break
		}
		if _, repeated := seenCursors[next]; repeated {
			break
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
	result.Channels = normalizeChannels(result.Channels)
	return result, nil
}

func (c *Client) call(ctx context.Context, token, method string, values url.Values, target any) error {
	endpoint := strings.TrimRight(c.BaseURL, "/")
	if endpoint == "" {
		endpoint = "https://slack.com/api"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/"+method, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Slack API %s: %w", method, err)
	}
	defer response.Body.Close()
	var envelope struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Provided string `json:"provided"`
	}
	payload := json.RawMessage{}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("Slack API %s: decode response: %w", method, err)
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("Slack API %s: decode status: %w", method, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Slack API %s: HTTP %d", method, response.StatusCode)
	}
	if !envelope.OK {
		return &APIError{Method: method, Code: envelope.Error, Needed: splitScopes(envelope.Needed), Provided: splitScopes(envelope.Provided)}
	}
	if target != nil {
		if err := json.Unmarshal(payload, target); err != nil {
			return fmt.Errorf("Slack API %s: decode payload: %w", method, err)
		}
	}
	return nil
}

func splitScopes(value string) []string {
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func normalizeChannels(channels []Channel) []Channel {
	byID := make(map[string]Channel, len(channels))
	for _, channel := range channels {
		channel.ID = strings.TrimSpace(channel.ID)
		if channel.ID == "" {
			continue
		}
		channel.Name = strings.TrimSpace(channel.Name)
		if channel.Name == "" {
			channel.Name = channel.ID
		}
		if current, exists := byID[channel.ID]; exists {
			current.Member = current.Member || channel.Member
			current.Private = current.Private || channel.Private
			if current.Description == "" {
				current.Description = channel.Description
			}
			byID[channel.ID] = current
			continue
		}
		byID[channel.ID] = channel
	}
	result := make([]Channel, 0, len(byID))
	for _, channel := range byID {
		result = append(result, channel)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Member != result[j].Member {
			return result[i].Member
		}
		left, right := strings.ToLower(result[i].Name), strings.ToLower(result[j].Name)
		if left == right {
			return result[i].ID < result[j].ID
		}
		return left < right
	})
	return result
}
