package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

type slackDiscovery struct {
	Available        bool           `json:"available"`
	Runtime          string         `json:"runtime"`
	AppID            string         `json:"appId,omitempty"`
	TeamID           string         `json:"teamId,omitempty"`
	TeamName         string         `json:"teamName,omitempty"`
	CredentialStored bool           `json:"credentialStored"`
	BotReady         bool           `json:"botReady"`
	SocketReady      bool           `json:"socketReady"`
	BotUserID        string         `json:"botUserId,omitempty"`
	BotName          string         `json:"botName,omitempty"`
	Channels         []slackChannel `json:"channels"`
	MissingScopes    []string       `json:"missingScopes,omitempty"`
	Error            string         `json:"error,omitempty"`
}

type slackChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
	Member      bool   `json:"member"`
}

type slackCredentialParams struct {
	BotToken string `json:"botToken"`
	AppToken string `json:"appToken"`
}

type slackSetupParams struct {
	Agent       string `json:"agent"`
	AppID       string `json:"appId"`
	TeamID      string `json:"teamId"`
	ChannelID   string `json:"channelId"`
	Purpose     string `json:"purpose"`
	Role        string `json:"role"`
	Guidance    string `json:"guidance"`
	TrustDomain string `json:"trustDomain"`
}

var (
	loadSlackTokens     = loomslack.LoadTokens
	saveSlackTokens     = loomslack.SaveTokens
	discoverSlackClient = loomslack.Discover
)

func (s *Server) discoverSlack(ctx context.Context, connectionID, requestedAppID string) slackDiscovery {
	result := slackDiscovery{Available: true, Runtime: "managed-socket-mode", Channels: []slackChannel{}}
	appID := strings.TrimSpace(requestedAppID)
	teamID := ""
	if connectionID != "" {
		for _, connection := range s.hub.ListConnections() {
			if connection.ID == connectionID && connection.Provider == "slack" {
				teamID = connection.AccountRef
				if appID == "" {
					appID = slackAppIDFromCredentialRef(connection.CredentialRef)
				}
				break
			}
		}
	}
	if appID == "" {
		for _, connection := range s.hub.ListConnections() {
			if connection.Provider == "slack" {
				appID = slackAppIDFromCredentialRef(connection.CredentialRef)
				teamID = connection.AccountRef
				break
			}
		}
	}
	result.AppID, result.TeamID = appID, teamID
	if appID == "" {
		return result
	}
	tokens, err := loadSlackTokens(appID, teamID)
	if err != nil {
		result.Error = "Read Slack credentials: " + err.Error()
		return result
	}
	result.CredentialStored = tokens.Bot != "" && tokens.App != ""
	if !result.CredentialStored {
		result.Error = "Enter the Bot token and App token to let CodexLoom manage this Slack connection"
		return result
	}
	discovery, discoverErr := discoverSlackClient(ctx, tokens.Bot, tokens.App)
	return slackDiscoveryResult(discovery, discoverErr, true)
}

func (s *Server) saveSlackCredentials(ctx context.Context, p slackCredentialParams) (slackDiscovery, error) {
	botToken := strings.TrimSpace(p.BotToken)
	appToken := strings.TrimSpace(p.AppToken)
	if botToken == "" || appToken == "" {
		return slackDiscovery{}, &hub.HubError{Status: 400, Message: "Slack Bot token and App token are required"}
	}
	discovery, err := discoverSlackClient(ctx, botToken, appToken)
	var apiErr *loomslack.APIError
	if err != nil && (!errors.As(err, &apiErr) || apiErr.Method != "conversations.list" || apiErr.Code != "missing_scope") {
		return slackDiscovery{}, &hub.HubError{Status: 400, Message: "Slack verification failed: " + err.Error()}
	}
	if discovery.Identity.AppID == "" {
		return slackDiscovery{}, &hub.HubError{Status: 400, Message: "Slack verification did not return an App ID"}
	}
	if err := saveSlackTokens(discovery.Identity.AppID, botToken, appToken); err != nil {
		return slackDiscovery{}, &hub.HubError{Status: 500, Message: "Save Slack credentials: " + err.Error()}
	}
	return slackDiscoveryResult(discovery, err, true), nil
}

func (s *Server) setupSlack(ctx context.Context, p slackSetupParams, hubURL string) (map[string]any, error) {
	agentKey := strings.TrimSpace(p.Agent)
	if agentKey == "" {
		return nil, &hub.HubError{Status: 400, Message: "Choose an Agent for this Slack identity"}
	}
	agentID := ""
	for _, agent := range s.hub.ListAgents() {
		if agent.Name == agentKey || agent.ID == agentKey {
			agentID, agentKey = agent.ID, agent.Name
			break
		}
	}
	if agentID == "" {
		return nil, &hub.HubError{Status: 404, Message: "Agent not found: " + agentKey}
	}
	appID := strings.TrimSpace(p.AppID)
	teamID := strings.TrimSpace(p.TeamID)
	tokens, err := loadSlackTokens(appID, teamID)
	if err != nil || tokens.Bot == "" || tokens.App == "" {
		return nil, &hub.HubError{Status: 409, Message: "Slack credentials are not configured"}
	}
	discovered, discoverErr := discoverSlackClient(ctx, tokens.Bot, tokens.App)
	var apiErr *loomslack.APIError
	if discoverErr != nil && (!errors.As(discoverErr, &apiErr) || apiErr.Method != "conversations.list" || apiErr.Code != "missing_scope") {
		return nil, &hub.HubError{Status: 409, Message: discoverErr.Error()}
	}
	if discovered.Identity.AppID == "" || discovered.Identity.TeamID == "" || discovered.Identity.BotUserID == "" {
		return nil, &hub.HubError{Status: 409, Message: "Slack bot identity is incomplete"}
	}
	appID, teamID = discovered.Identity.AppID, discovered.Identity.TeamID

	credentialRef := "keychain:" + loomslack.CredentialService(appID)
	var connection hub.PlatformConnection
	for _, candidate := range s.hub.ListConnections() {
		if candidate.Provider == "slack" && (candidate.CredentialRef == credentialRef || candidate.AccountRef == teamID) {
			connection = candidate
			break
		}
	}
	if connection.ID == "" {
		connection, err = s.hub.CreateConnection(hub.ConnectionParams{
			Provider: "slack", AccountRef: teamID, CredentialRef: credentialRef,
			Capabilities: []string{"receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"},
		})
	} else {
		enabled := true
		connection, err = s.hub.UpdateConnection(connection.ID, hub.ConnectionParams{AccountRef: teamID, CredentialRef: credentialRef, Enabled: &enabled})
	}
	if err != nil {
		return nil, err
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
			return nil, &hub.HubError{Status: 409, Message: "This Slack app is already assigned to another Agent"}
		}
		address = candidate
		break
	}
	if address.ID == "" {
		trustDomain := strings.TrimSpace(p.TrustDomain)
		if trustDomain == "" {
			trustDomain = "slack:" + teamID
		}
		displayName := discovered.Identity.BotName
		if displayName == "" {
			displayName = "Slack"
		}
		address, err = s.hub.CreateAddress(hub.AddressParams{
			Agent: agentKey, ConnectionID: connection.ID,
			ExternalIdentity: "slack://" + teamID + "/" + discovered.Identity.BotUserID,
			DisplayName:      displayName, TriggerPolicy: "mention", ReplyPolicy: "final_answer",
			DMPolicy: "managed", TrustDomain: trustDomain,
		})
		if err != nil {
			return nil, err
		}
	}

	memberships := []hub.ConversationMembership{}
	channelID := strings.TrimSpace(p.ChannelID)
	if channelID != "" {
		var selected *loomslack.Channel
		for i := range discovered.Channels {
			if discovered.Channels[i].ID == channelID {
				selected = &discovered.Channels[i]
				break
			}
		}
		if selected == nil {
			return nil, &hub.HubError{Status: 400, Message: "The selected Slack channel is no longer visible to the bot"}
		}
		if !selected.Member {
			return nil, &hub.HubError{Status: 400, Message: "Invite the Slack bot to #" + selected.Name + " before adding it"}
		}
		purpose := strings.TrimSpace(p.Purpose)
		if purpose == "" {
			purpose = selected.Description
		}
		if purpose == "" {
			purpose = "Support the work of #" + selected.Name
		}
		role := strings.TrimSpace(p.Role)
		if role == "" {
			role = agentKey + " is the domain Agent serving this channel"
		}
		guidance := strings.TrimSpace(p.Guidance)
		if guidance == "" {
			guidance = "Respond when mentioned or directly asked. Stay within the Agent's domain and the purpose of this channel."
		}
		displayName, trigger, reply, trust, conversationType := "#"+selected.Name, "mention", "final_answer", address.TrustDomain, "group"
		membership, _, err := s.hub.UpsertConversationMembership(hub.ConversationMembershipParams{
			AddressID: address.ID, ConversationID: channelID, DisplayName: &displayName,
			Purpose: &purpose, Role: &role, Guidance: &guidance, ConversationType: &conversationType,
			TriggerPolicy: &trigger, ReplyPolicy: &reply, TrustDomain: &trust,
		})
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}

	gateway, err := installManagedSlackGateway(s, connection, address, appID, teamID, discovered.Identity.BotUserID, hubURL)
	if err != nil {
		return nil, &hub.HubError{Status: 500, Message: "Configure Slack gateway: " + err.Error()}
	}
	return map[string]any{
		"connection": connection, "address": address, "memberships": memberships,
		"discovery": slackDiscoveryResult(discovered, discoverErr, true), "gateway": gateway,
	}, nil
}

func slackDiscoveryResult(discovery loomslack.Discovery, err error, credentialStored bool) slackDiscovery {
	result := slackDiscovery{
		Available: true, Runtime: "managed-socket-mode", CredentialStored: credentialStored,
		AppID: discovery.Identity.AppID, TeamID: discovery.Identity.TeamID, TeamName: discovery.Identity.TeamName,
		BotReady:  discovery.Identity.AppID != "" && discovery.Identity.BotUserID != "",
		BotUserID: discovery.Identity.BotUserID, BotName: discovery.Identity.BotName, Channels: []slackChannel{},
	}
	var apiErr *loomslack.APIError
	if err == nil {
		result.SocketReady = true
	} else {
		result.Error = err.Error()
		if errors.As(err, &apiErr) {
			result.MissingScopes = apiErr.Needed
			result.SocketReady = apiErr.Method == "conversations.list"
		}
	}
	for _, channel := range discovery.Channels {
		result.Channels = append(result.Channels, slackChannel{
			ID: channel.ID, Name: channel.Name, Description: channel.Description,
			Private: channel.Private, Member: channel.Member,
		})
	}
	return result
}

func slackAppIDFromCredentialRef(value string) string {
	const prefix = "keychain:com.codexloom.slack."
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	appID := strings.TrimPrefix(value, prefix)
	appID = strings.TrimSuffix(appID, ".bot-token")
	appID = strings.TrimSuffix(appID, ".app-token")
	return strings.TrimSpace(appID)
}
