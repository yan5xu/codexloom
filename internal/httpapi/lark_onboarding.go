package httpapi

import (
	"context"
	"net"
	"strings"

	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
)

type larkDiscovery struct {
	Available        bool       `json:"available"`
	Runtime          string     `json:"runtime"`
	AppID            string     `json:"appId,omitempty"`
	CredentialStored bool       `json:"credentialStored"`
	BotReady         bool       `json:"botReady"`
	BotOpenID        string     `json:"botOpenId,omitempty"`
	BotName          string     `json:"botName,omitempty"`
	Chats            []larkChat `json:"chats"`
	Error            string     `json:"error,omitempty"`
}

type larkChat struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	External    bool   `json:"external"`
}

type larkCredentialParams struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
}

type larkSetupParams struct {
	Agent       string `json:"agent"`
	AppID       string `json:"appId"`
	ChatID      string `json:"chatId"`
	Purpose     string `json:"purpose"`
	Role        string `json:"role"`
	Guidance    string `json:"guidance"`
	TrustDomain string `json:"trustDomain"`
}

var (
	loadFeishuSecret = feishu.LoadAppSecret
	saveFeishuSecret = feishu.SaveAppSecret
	discoverFeishu   = feishu.Discover
)

func (s *Server) discoverLark(ctx context.Context, requestedAppID string) larkDiscovery {
	appID := strings.TrimSpace(requestedAppID)
	if appID == "" {
		for _, connection := range s.hub.ListConnections() {
			if connection.Provider == "lark" {
				appID = connection.AccountRef
				break
			}
		}
	}
	result := larkDiscovery{Available: true, Runtime: "native", AppID: appID, Chats: []larkChat{}}
	if appID == "" {
		return result
	}
	secret, err := loadFeishuSecret(appID)
	if err != nil {
		result.Error = "Read Feishu credential: " + err.Error()
		return result
	}
	result.CredentialStored = secret != ""
	if secret == "" {
		result.Error = "Enter the App Secret once to migrate this Feishu connection to the native gateway"
		return result
	}
	discovery, err := discoverFeishu(ctx, appID, secret)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.BotReady = true
	result.BotOpenID = discovery.Bot.OpenID
	result.BotName = discovery.Bot.Name
	seenChats := make(map[string]struct{}, len(discovery.Chats))
	for _, chat := range discovery.Chats {
		chat.ID = strings.TrimSpace(chat.ID)
		if chat.ID == "" {
			continue
		}
		if _, exists := seenChats[chat.ID]; exists {
			continue
		}
		seenChats[chat.ID] = struct{}{}
		result.Chats = append(result.Chats, larkChat{
			ID: chat.ID, Name: chat.Name, Description: chat.Description,
			Avatar: chat.Avatar, External: chat.External,
		})
	}
	return result
}

func (s *Server) saveLarkCredentials(ctx context.Context, p larkCredentialParams) (larkDiscovery, error) {
	appID := strings.TrimSpace(p.AppID)
	appSecret := strings.TrimSpace(p.AppSecret)
	if appID == "" || appSecret == "" {
		return larkDiscovery{}, &hub.HubError{Status: 400, Message: "Feishu App ID and App Secret are required"}
	}
	if _, err := discoverFeishu(ctx, appID, appSecret); err != nil {
		return larkDiscovery{}, &hub.HubError{Status: 400, Message: "Feishu verification failed: " + err.Error()}
	}
	if err := saveFeishuSecret(appID, appSecret); err != nil {
		return larkDiscovery{}, &hub.HubError{Status: 500, Message: "Save Feishu credential: " + err.Error()}
	}
	return s.discoverLark(ctx, appID), nil
}

func (s *Server) setupLark(ctx context.Context, p larkSetupParams, hubURL string) (map[string]any, error) {
	agentKey := strings.TrimSpace(p.Agent)
	if agentKey == "" {
		return nil, &hub.HubError{Status: 400, Message: "Choose an Agent for this Feishu identity"}
	}
	agentID := ""
	for _, agent := range s.hub.ListAgents() {
		if agent.Name == agentKey || agent.ID == agentKey {
			agentID = agent.ID
			agentKey = agent.Name
			break
		}
	}
	if agentID == "" {
		return nil, &hub.HubError{Status: 404, Message: "Agent not found: " + agentKey}
	}

	discovery := s.discoverLark(ctx, p.AppID)
	if !discovery.BotReady {
		message := discovery.Error
		if message == "" {
			message = "Feishu credentials are not configured"
		}
		return nil, &hub.HubError{Status: 409, Message: message}
	}

	var connection hub.PlatformConnection
	for _, candidate := range s.hub.ListConnections() {
		if candidate.Provider == "lark" && candidate.AccountRef == discovery.AppID {
			connection = candidate
			break
		}
	}
	var err error
	credentialRef := "keychain:" + feishu.CredentialService(discovery.AppID)
	if connection.ID == "" {
		connection, err = s.hub.CreateConnection(hub.ConnectionParams{
			Provider: "lark", AccountRef: discovery.AppID, CredentialRef: credentialRef,
			Capabilities: []string{"receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"},
		})
		if err != nil {
			return nil, err
		}
	} else {
		enabled := true
		connection, err = s.hub.UpdateConnection(connection.ID, hub.ConnectionParams{CredentialRef: credentialRef, Enabled: &enabled})
		if err != nil {
			return nil, err
		}
	}

	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		return nil, err
	}
	var address hub.AgentAddress
	for _, candidate := range addresses {
		if candidate.ConnectionID != connection.ID {
			continue
		}
		if candidate.AgentID != agentID {
			return nil, &hub.HubError{Status: 409, Message: "This Feishu app is already assigned to another Agent"}
		}
		address = candidate
		break
	}
	if address.ID == "" {
		trustDomain := strings.TrimSpace(p.TrustDomain)
		if trustDomain == "" {
			for _, candidate := range addresses {
				if candidate.AgentID == agentID && candidate.TrustDomain != "" {
					trustDomain = candidate.TrustDomain
					break
				}
			}
		}
		if trustDomain == "" {
			trustDomain = "lark:" + discovery.AppID
		}
		displayName := discovery.BotName
		if displayName == "" {
			displayName = "Feishu"
		}
		externalIdentity := discovery.BotOpenID
		if externalIdentity == "" {
			externalIdentity = discovery.AppID
		}
		address, err = s.hub.CreateAddress(hub.AddressParams{
			Agent: agentKey, ConnectionID: connection.ID,
			ExternalIdentity: "lark://" + externalIdentity, DisplayName: displayName,
			TriggerPolicy: "mention", ReplyPolicy: "final_answer", DMPolicy: "managed", TrustDomain: trustDomain,
		})
		if err != nil {
			return nil, err
		}
	}

	memberships := []hub.ConversationMembership{}
	chatID := strings.TrimSpace(p.ChatID)
	if chatID != "" {
		var selected *larkChat
		for i := range discovery.Chats {
			if discovery.Chats[i].ID == chatID {
				selected = &discovery.Chats[i]
				break
			}
		}
		if selected == nil {
			return nil, &hub.HubError{Status: 400, Message: "The selected Feishu group is no longer visible to the bot"}
		}
		purpose := strings.TrimSpace(p.Purpose)
		if purpose == "" {
			purpose = selected.Description
		}
		if purpose == "" {
			purpose = "Support the work of \"" + selected.Name + "\""
		}
		role := strings.TrimSpace(p.Role)
		if role == "" {
			role = agentKey + " is the domain Agent serving this group"
		}
		guidance := strings.TrimSpace(p.Guidance)
		if guidance == "" {
			guidance = "Respond when mentioned or directly asked. Stay within the Agent's domain and the purpose of this group."
		}
		displayName := selected.Name
		trigger, reply, trust, conversationType := "mention", "final_answer", address.TrustDomain, "group"
		membership, _, err := s.hub.UpsertConversationMembership(hub.ConversationMembershipParams{
			AddressID: address.ID, ConversationID: chatID, DisplayName: &displayName,
			Purpose: &purpose, Role: &role, Guidance: &guidance,
			ConversationType: &conversationType,
			TriggerPolicy:    &trigger, ReplyPolicy: &reply, TrustDomain: &trust,
		})
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}

	gateway, err := installNativeFeishuGateway(s, connection, address, discovery.AppID, hubURL)
	if err != nil {
		return nil, &hub.HubError{Status: 500, Message: "Configure native Feishu gateway: " + err.Error()}
	}
	return map[string]any{
		"connection": connection, "address": address, "memberships": memberships,
		"discovery": discovery, "gateway": gateway,
	}, nil
}

func nativeHubURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "http://127.0.0.1:4870"
	}
	if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
		return "http://127.0.0.1:" + port
	}
	return "http://127.0.0.1:4870"
}
