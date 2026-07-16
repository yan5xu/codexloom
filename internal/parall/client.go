package parall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const DefaultAPIURL = "https://api.parall.com"

type User struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	LastSeenAt  string    `json:"last_seen_at"`
	Presence    *Presence `json:"presence,omitempty"`
}

type Presence struct {
	Online bool   `json:"online"`
	Status string `json:"status"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Chat struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type CreateAgentResponse struct {
	User   User   `json:"user"`
	APIKey string `json:"api_key"`
}

type APIKey struct {
	ID     string `json:"id"`
	APIKey string `json:"api_key"`
}

type Ticket struct {
	Ticket    string `json:"ticket"`
	WSURL     string `json:"ws_url"`
	ExpiresAt string `json:"expires_at"`
}

type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("Parall API %s %s: %s (%s)", e.Method, e.Path, message, e.Code)
	}
	return fmt.Sprintf("Parall API %s %s: %s", e.Method, e.Path, message)
}

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func NewClient(apiURL, apiKey string) *Client {
	apiURL = normalizeAPIURL(apiURL)
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	return &Client{BaseURL: apiURL, APIKey: strings.TrimSpace(apiKey)}
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var result User
	err := c.call(ctx, http.MethodGet, "/api/v1/users/me", nil, &result)
	return result, err
}

func (c *Client) GetOrganizations(ctx context.Context) ([]Organization, error) {
	var result struct {
		Data []Organization `json:"data"`
	}
	err := c.call(ctx, http.MethodGet, "/api/v1/orgs", nil, &result)
	return result.Data, err
}

func (c *Client) GetAgents(ctx context.Context, orgID string) ([]User, error) {
	var result struct {
		Data []User `json:"data"`
	}
	err := c.call(ctx, http.MethodGet, orgPath(orgID)+"/agents", nil, &result)
	if err == nil {
		sort.Slice(result.Data, func(i, j int) bool {
			return strings.ToLower(result.Data[i].DisplayName) < strings.ToLower(result.Data[j].DisplayName)
		})
	}
	return result.Data, err
}

func (c *Client) GetChats(ctx context.Context, orgID string) ([]Chat, error) {
	return c.getChats(ctx, orgPath(orgID)+"/chats")
}

func (c *Client) GetMemberChats(ctx context.Context, orgID, memberID string) ([]Chat, error) {
	return c.getChats(ctx, orgPath(orgID)+"/members/"+url.PathEscape(strings.TrimSpace(memberID))+"/chats")
}

func (c *Client) getChats(ctx context.Context, path string) ([]Chat, error) {
	items := []Chat{}
	cursor := ""
	for page := 0; page < 50; page++ {
		query := url.Values{"limit": {"100"}}
		if strings.Contains(path, "/chats") && !strings.Contains(path, "/members/") {
			query.Set("scope", "active")
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var result struct {
			Data       []Chat `json:"data"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.call(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &result); err != nil {
			return nil, err
		}
		items = append(items, result.Data...)
		if !result.HasMore || strings.TrimSpace(result.NextCursor) == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if left == right {
			return items[i].ID < items[j].ID
		}
		return left < right
	})
	return items, nil
}

func (c *Client) CreateAgent(ctx context.Context, orgID, displayName string) (CreateAgentResponse, error) {
	body := map[string]any{"display_name": strings.TrimSpace(displayName), "runtime_type": "codex", "model_management": "self"}
	var result CreateAgentResponse
	err := c.call(ctx, http.MethodPost, orgPath(orgID)+"/agents", body, &result)
	return result, err
}

func (c *Client) UpdateAgent(ctx context.Context, orgID, agentID, displayName string) (User, error) {
	var result User
	err := c.call(ctx, http.MethodPatch, orgPath(orgID)+"/agents/"+url.PathEscape(strings.TrimSpace(agentID)), map[string]string{"display_name": strings.TrimSpace(displayName)}, &result)
	return result, err
}

func (c *Client) CreateAgentAPIKey(ctx context.Context, orgID, agentID string) (APIKey, error) {
	var result APIKey
	err := c.call(ctx, http.MethodPost, orgPath(orgID)+"/agents/"+url.PathEscape(strings.TrimSpace(agentID))+"/api-keys", map[string]any{}, &result)
	return result, err
}

func (c *Client) AddChatMember(ctx context.Context, orgID, chatID, userID string) error {
	return c.call(ctx, http.MethodPost, orgPath(orgID)+"/chats/"+url.PathEscape(strings.TrimSpace(chatID))+"/members", map[string]string{"user_id": strings.TrimSpace(userID)}, nil)
}

func (c *Client) GetAgentMe(ctx context.Context, orgID string) (User, error) {
	var result User
	err := c.call(ctx, http.MethodGet, orgPath(orgID)+"/agents/me", nil, &result)
	return result, err
}

func (c *Client) GetWSTicket(ctx context.Context) (Ticket, error) {
	var result Ticket
	err := c.call(ctx, http.MethodPost, "/api/v1/ws/ticket", map[string]any{}, &result)
	if err == nil && strings.TrimSpace(result.Ticket) == "" {
		err = fmt.Errorf("Parall API POST /api/v1/ws/ticket: response did not include a ticket")
	}
	return result, err
}

func (c *Client) call(ctx context.Context, method, path string, body, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Parall API %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error   string `json:"error"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &envelope)
		message := envelope.Message
		if message == "" {
			message = envelope.Error
		}
		return &APIError{Method: method, Path: path, StatusCode: response.StatusCode, Code: envelope.Code, Message: message}
	}
	if target == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("Parall API %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func orgPath(orgID string) string {
	return "/api/v1/orgs/" + url.PathEscape(strings.TrimSpace(orgID))
}
