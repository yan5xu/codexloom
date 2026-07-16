package httpapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
)

type parallDiscovery struct {
	Available             bool          `json:"available"`
	Runtime               string        `json:"runtime"`
	APIURL                string        `json:"apiUrl,omitempty"`
	OrgID                 string        `json:"orgId,omitempty"`
	OrgName               string        `json:"orgName,omitempty"`
	OwnerCredentialStored bool          `json:"ownerCredentialStored"`
	OwnerReady            bool          `json:"ownerReady"`
	OwnerName             string        `json:"ownerName,omitempty"`
	OwnerRole             string        `json:"ownerRole,omitempty"`
	OwnerError            string        `json:"ownerError,omitempty"`
	SelectedAgentID       string        `json:"selectedAgentId,omitempty"`
	AgentCredentialStored bool          `json:"agentCredentialStored"`
	ExternalReady         bool          `json:"externalReady"`
	SocketReady           bool          `json:"socketReady"`
	Agents                []parallAgent `json:"agents"`
	Chats                 []parallChat  `json:"chats"`
	Error                 string        `json:"error,omitempty"`
}

type parallAgent struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Online           bool   `json:"online"`
	Presence         string `json:"presence,omitempty"`
	LastSeenAt       string `json:"lastSeenAt,omitempty"`
	CredentialStored bool   `json:"credentialStored"`
}

type parallChat struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Visibility  string `json:"visibility,omitempty"`
	Member      bool   `json:"member"`
}

type parallCredentialParams struct {
	APIURL      string `json:"apiUrl"`
	OrgID       string `json:"orgId"`
	OwnerAPIKey string `json:"ownerApiKey"`
}

type parallAgentCredentialParams struct {
	APIURL      string `json:"apiUrl"`
	OrgID       string `json:"orgId"`
	AgentID     string `json:"agentId"`
	AgentAPIKey string `json:"agentApiKey"`
}

type parallImportParams struct {
	Agent           string `json:"agent"`
	APIURL          string `json:"apiUrl"`
	OrgID           string `json:"orgId"`
	ExternalAgentID string `json:"externalAgentId"`
	AgentAPIKey     string `json:"agentApiKey"`
	TrustDomain     string `json:"trustDomain"`
}

type parallSetupParams struct {
	Agent               string `json:"agent"`
	OrgID               string `json:"orgId"`
	ExternalAgentID     string `json:"externalAgentId"`
	ExternalDisplayName string `json:"externalDisplayName"`
	ChatID              string `json:"chatId"`
	Purpose             string `json:"purpose"`
	Role                string `json:"role"`
	Guidance            string `json:"guidance"`
	TrustDomain         string `json:"trustDomain"`
}

type parallAPI interface {
	GetMe(context.Context) (parall.User, error)
	GetOrganizations(context.Context) ([]parall.Organization, error)
	GetAgents(context.Context, string) ([]parall.User, error)
	GetChats(context.Context, string) ([]parall.Chat, error)
	GetMemberChats(context.Context, string, string) ([]parall.Chat, error)
	CreateAgent(context.Context, string, string) (parall.CreateAgentResponse, error)
	UpdateAgent(context.Context, string, string, string) (parall.User, error)
	CreateAgentAPIKey(context.Context, string, string) (parall.APIKey, error)
	AddChatMember(context.Context, string, string, string) error
	GetAgentMe(context.Context, string) (parall.User, error)
	GetWSTicket(context.Context) (parall.Ticket, error)
}

var (
	newParallClient              = func(apiURL, apiKey string) parallAPI { return parall.NewClient(apiURL, apiKey) }
	loadParallOwnerCredentials   = parall.LoadOwnerCredentials
	saveParallOwnerCredentials   = parall.SaveOwnerCredentials
	loadParallAgentCredentials   = parall.LoadAgentCredentials
	saveParallAgentCredentials   = parall.SaveAgentCredentials
	deleteParallAgentCredentials = parall.DeleteAgentCredentials
)

func (s *Server) discoverParall(ctx context.Context, connectionID, requestedOrgID, requestedAgentID string) parallDiscovery {
	result := parallDiscovery{Available: true, Runtime: "managed-websocket", APIURL: parall.DefaultAPIURL, Agents: []parallAgent{}, Chats: []parallChat{}}
	orgID, agentID := strings.TrimSpace(requestedOrgID), strings.TrimSpace(requestedAgentID)
	if connectionID != "" || orgID == "" || agentID == "" {
		for _, connection := range s.hub.ListConnections() {
			if connection.Provider != "parall" || connectionID != "" && connection.ID != connectionID {
				continue
			}
			if orgID == "" {
				orgID = connection.AccountRef
			}
			if agentID == "" {
				agentID = parallAgentIDFromCredentialRef(connection.CredentialRef)
			}
			if agentID == "" {
				addresses, _ := s.hub.ListAddresses("")
				for _, address := range addresses {
					if address.ConnectionID == connection.ID {
						agentID = strings.TrimPrefix(address.ExternalIdentity, "prll://")
						break
					}
				}
			}
			break
		}
	}
	result.OrgID, result.SelectedAgentID = orgID, agentID
	if orgID == "" {
		return result
	}

	chatByID := map[string]parallChat{}
	addChats := func(chats []parall.Chat, member bool) {
		for _, chat := range chats {
			if strings.TrimSpace(chat.ID) == "" {
				continue
			}
			name := strings.TrimSpace(chat.Name)
			if name == "" {
				if chat.Type == "direct" {
					name = "Direct message"
				} else {
					name = chat.ID
				}
			}
			current := chatByID[chat.ID]
			chatByID[chat.ID] = parallChat{
				ID: chat.ID, Name: name, Description: firstNonEmpty(chat.Description, current.Description),
				Type: firstNonEmpty(chat.Type, current.Type), Visibility: firstNonEmpty(chat.Visibility, current.Visibility),
				Member: member || current.Member,
			}
		}
	}
	addAgent := func(item parall.User, credentialStored bool) {
		entry := parallAgent{ID: item.ID, Name: item.DisplayName, Status: item.Status, LastSeenAt: item.LastSeenAt, CredentialStored: credentialStored}
		if item.Presence != nil {
			entry.Online, entry.Presence = item.Presence.Online, item.Presence.Status
		}
		for index := range result.Agents {
			if result.Agents[index].ID == entry.ID {
				result.Agents[index] = entry
				return
			}
		}
		result.Agents = append(result.Agents, entry)
	}

	ownerCredentials, ownerCredentialsErr := loadParallOwnerCredentials(orgID)
	if ownerCredentialsErr != nil {
		result.OwnerError = "Read Parall Owner credentials: " + ownerCredentialsErr.Error()
	} else {
		if ownerCredentials.APIURL != "" {
			result.APIURL = ownerCredentials.APIURL
		}
		result.OwnerCredentialStored = ownerCredentials.APIURL != "" && ownerCredentials.APIKey != ""
	}
	if result.OwnerCredentialStored {
		ownerClient := newParallClient(ownerCredentials.APIURL, ownerCredentials.APIKey)
		owner, ownerErr := ownerClient.GetMe(ctx)
		if ownerErr != nil {
			result.OwnerError = "Verify Parall Owner: " + ownerErr.Error()
		} else {
			result.OwnerName = owner.DisplayName
			organizations, organizationsErr := ownerClient.GetOrganizations(ctx)
			if organizationsErr != nil {
				result.OwnerError = "Read Parall organizations: " + organizationsErr.Error()
			} else if organization, found := findParallOrganization(organizations, orgID); !found {
				result.OwnerError = "The stored Parall identity is not a member of organization " + orgID
			} else {
				result.OrgName, result.OwnerRole = organization.Name, organization.Role
				result.OwnerReady = organization.Role == "owner"
				if !result.OwnerReady {
					result.OwnerError = "Identity administration requires Owner access; current role is " + organization.Role
				}
			}
		}
		if result.OwnerReady {
			if agents, agentsErr := ownerClient.GetAgents(ctx, orgID); agentsErr != nil {
				result.OwnerError = "Read Parall Agents: " + agentsErr.Error()
			} else {
				for _, item := range agents {
					credentials, _ := loadParallAgentCredentials(orgID, item.ID)
					addAgent(item, credentials.APIKey != "")
				}
			}
			if chats, chatsErr := ownerClient.GetChats(ctx, orgID); chatsErr != nil {
				result.OwnerError = "Read Parall conversations: " + chatsErr.Error()
			} else {
				addChats(chats, false)
			}
			if agentID != "" {
				if memberChats, memberErr := ownerClient.GetMemberChats(ctx, orgID, agentID); memberErr == nil {
					addChats(memberChats, true)
				}
			}
		}
	}

	if agentID != "" {
		agentCredentials, agentCredentialsErr := loadParallAgentCredentials(orgID, agentID)
		if agentCredentialsErr != nil {
			result.Error = "Read Parall Agent credentials: " + agentCredentialsErr.Error()
		} else {
			result.AgentCredentialStored = agentCredentials.APIURL != "" && agentCredentials.APIKey != ""
			if result.AgentCredentialStored {
				if ownerCredentials.APIURL == "" && agentCredentials.APIURL != "" {
					result.APIURL = agentCredentials.APIURL
				}
				agentClient := newParallClient(agentCredentials.APIURL, agentCredentials.APIKey)
				external, externalErr := agentClient.GetAgentMe(ctx, orgID)
				if externalErr != nil {
					result.Error = "Verify Parall Agent: " + externalErr.Error()
				} else if external.ID != agentID || external.Status != "active" {
					result.Error = "The stored Parall Agent identity is not active or does not match " + agentID
				} else {
					result.ExternalReady = true
					addAgent(external, true)
					if memberChats, memberErr := agentClient.GetMemberChats(ctx, orgID, agentID); memberErr != nil {
						result.Error = "Read joined Parall conversations: " + memberErr.Error()
					} else {
						addChats(memberChats, true)
					}
					if _, socketErr := agentClient.GetWSTicket(ctx); socketErr != nil {
						result.Error = "Open Parall WebSocket: " + socketErr.Error()
					} else {
						result.SocketReady = true
					}
				}
			}
		}
	}
	for _, chat := range chatByID {
		result.Chats = append(result.Chats, chat)
	}
	sort.Slice(result.Agents, func(i, j int) bool {
		return strings.ToLower(result.Agents[i].Name) < strings.ToLower(result.Agents[j].Name)
	})
	sort.Slice(result.Chats, func(i, j int) bool {
		return strings.ToLower(result.Chats[i].Name) < strings.ToLower(result.Chats[j].Name)
	})
	return result
}

func (s *Server) saveParallCredentials(ctx context.Context, p parallCredentialParams) (parallDiscovery, error) {
	apiURL, orgID, apiKey := strings.TrimSpace(p.APIURL), strings.TrimSpace(p.OrgID), strings.TrimSpace(p.OwnerAPIKey)
	if apiURL == "" {
		apiURL = parall.DefaultAPIURL
	}
	if orgID == "" || apiKey == "" {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Parall organization ID and Owner API key are required"}
	}
	client := newParallClient(apiURL, apiKey)
	if _, err := client.GetMe(ctx); err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Parall Owner verification failed: " + err.Error()}
	}
	organizations, err := client.GetOrganizations(ctx)
	if err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Read Parall organizations: " + err.Error()}
	}
	organization, found := findParallOrganization(organizations, orgID)
	if !found {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "This Parall identity is not a member of organization " + orgID}
	}
	if organization.Role != "owner" {
		return parallDiscovery{}, &hub.HubError{Status: 403, Message: "Parall Owner credentials are required; current role is " + organization.Role}
	}
	if err := saveParallOwnerCredentials(orgID, apiURL, apiKey); err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 500, Message: "Save Parall Owner credentials: " + err.Error()}
	}
	return s.discoverParall(ctx, "", orgID, ""), nil
}

func (s *Server) saveParallAgentCredential(ctx context.Context, p parallAgentCredentialParams) (parallDiscovery, error) {
	apiURL, orgID, agentID, apiKey := strings.TrimSpace(p.APIURL), strings.TrimSpace(p.OrgID), strings.TrimSpace(p.AgentID), strings.TrimSpace(p.AgentAPIKey)
	if apiURL == "" {
		apiURL = parall.DefaultAPIURL
	}
	if orgID == "" || agentID == "" || apiKey == "" {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Parall Organization ID, Agent ID, and Agent API key are required"}
	}
	client := newParallClient(apiURL, apiKey)
	external, err := client.GetAgentMe(ctx, orgID)
	if err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Verify Parall Agent: " + err.Error()}
	}
	if external.ID != agentID || external.Status != "active" {
		return parallDiscovery{}, &hub.HubError{Status: 409, Message: fmt.Sprintf("Parall Agent %s is unavailable or does not match %s", external.ID, agentID)}
	}
	if _, err := client.GetWSTicket(ctx); err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 400, Message: "Verify Parall WebSocket: " + err.Error()}
	}
	if err := saveParallAgentCredentials(orgID, agentID, apiURL, apiKey); err != nil {
		return parallDiscovery{}, &hub.HubError{Status: 500, Message: "Save Parall Agent credentials: " + err.Error()}
	}
	return s.discoverParall(ctx, "", orgID, agentID), nil
}

func (s *Server) importParallAgent(ctx context.Context, p parallImportParams, hubURL string) (map[string]any, error) {
	agent, orgID := strings.TrimSpace(p.Agent), strings.TrimSpace(p.OrgID)
	externalID, apiKey := strings.TrimSpace(p.ExternalAgentID), strings.TrimSpace(p.AgentAPIKey)
	if agent == "" || orgID == "" || externalID == "" {
		return nil, &hub.HubError{Status: 400, Message: "Loom Agent, Parall Organization ID, and external Agent ID are required"}
	}
	previousCredential, err := loadParallAgentCredentials(orgID, externalID)
	if err != nil {
		return nil, &hub.HubError{Status: 500, Message: "Read existing Parall Agent credentials: " + err.Error()}
	}
	credentialProvided := apiKey != ""
	apiURL := strings.TrimSpace(p.APIURL)
	storedAPIURL := strings.TrimSpace(previousCredential.APIURL)
	if storedAPIURL == "" {
		storedAPIURL = parall.DefaultAPIURL
	}
	if !credentialProvided {
		if previousCredential.APIKey == "" {
			return nil, &hub.HubError{Status: 409, Message: "Parall Agent key is not stored; provide --agent-key-file for the first import"}
		}
		if apiURL != "" && strings.TrimRight(apiURL, "/") != strings.TrimRight(storedAPIURL, "/") {
			return nil, &hub.HubError{Status: 409, Message: "Cannot override the stored Parall API URL while reusing a Keychain credential"}
		}
		apiKey = previousCredential.APIKey
		apiURL = storedAPIURL
	} else if apiURL == "" {
		apiURL = storedAPIURL
	}
	if apiURL == "" {
		apiURL = parall.DefaultAPIURL
	}
	client := newParallClient(apiURL, apiKey)
	external, err := client.GetAgentMe(ctx, orgID)
	if err != nil {
		return nil, &hub.HubError{Status: 400, Message: "Verify Parall Agent: " + err.Error()}
	}
	if external.ID != externalID || external.Status != "active" {
		return nil, &hub.HubError{Status: 409, Message: fmt.Sprintf("Parall Agent %s is unavailable or does not match %s", external.ID, externalID)}
	}
	if _, err := client.GetWSTicket(ctx); err != nil {
		return nil, &hub.HubError{Status: 400, Message: "Verify Parall WebSocket: " + err.Error()}
	}

	connectionBefore := map[string]struct{}{}
	connectionsBefore := []hub.PlatformConnection{}
	for _, connection := range s.hub.ListConnections() {
		connectionBefore[connection.ID] = struct{}{}
		connectionsBefore = append(connectionsBefore, connection)
	}
	addressesBefore, err := s.hub.ListAddresses("")
	if err != nil {
		return nil, err
	}
	addressBefore := map[string]struct{}{}
	for _, address := range addressesBefore {
		addressBefore[address.ID] = struct{}{}
	}
	if credentialProvided {
		if err := saveParallAgentCredentials(orgID, externalID, apiURL, apiKey); err != nil {
			rollbackErr := deleteParallAgentCredentials(orgID, externalID)
			if previousCredential.APIKey != "" {
				rollbackErr = saveParallAgentCredentials(orgID, externalID, previousCredential.APIURL, previousCredential.APIKey)
			}
			if rollbackErr != nil {
				return nil, &hub.HubError{Status: 500, Message: fmt.Sprintf("Save Parall Agent credentials: %v; restore previous credential: %v", err, rollbackErr)}
			}
			return nil, &hub.HubError{Status: 500, Message: "Save Parall Agent credentials: " + err.Error()}
		}
	}

	result, setupErr := s.setupParall(ctx, parallSetupParams{
		Agent: agent, OrgID: orgID, ExternalAgentID: externalID,
		ExternalDisplayName: external.DisplayName, TrustDomain: strings.TrimSpace(p.TrustDomain),
	}, hubURL)
	if setupErr == nil {
		result["imported"] = true
		result["credentialReused"] = !credentialProvided
		return result, nil
	}

	var credentialRollbackErr error
	if credentialProvided {
		credentialRollbackErr = deleteParallAgentCredentials(orgID, externalID)
		if previousCredential.APIKey != "" {
			credentialRollbackErr = saveParallAgentCredentials(orgID, externalID, previousCredential.APIURL, previousCredential.APIKey)
		}
	}
	createdConnections := []string{}
	targetCredentialRef := "keychain:" + parall.AgentCredentialService(orgID, externalID)
	for _, connection := range s.hub.ListConnections() {
		if _, existed := connectionBefore[connection.ID]; !existed && connection.Provider == "parall" && connection.CredentialRef == targetCredentialRef {
			createdConnections = append(createdConnections, connection.ID)
		}
	}
	createdAddresses := []string{}
	afterAddresses, _ := s.hub.ListAddresses("")
	for _, address := range afterAddresses {
		if _, existed := addressBefore[address.ID]; !existed && address.ExternalIdentity == "prll://"+externalID {
			createdAddresses = append(createdAddresses, address.ID)
		}
	}
	integrationRollbackErr := s.hub.RollbackCreatedIntegration(createdConnections, createdAddresses)
	targetConnections := []hub.PlatformConnection{}
	for _, connection := range connectionsBefore {
		if connection.Provider != "parall" {
			continue
		}
		if connection.CredentialRef == targetCredentialRef || connectionUsesExternalIdentity(connection.ID, "prll://"+externalID, addressesBefore) {
			targetConnections = append(targetConnections, connection)
		}
	}
	targetAddresses := []hub.AgentAddress{}
	for _, address := range addressesBefore {
		if address.ExternalIdentity == "prll://"+externalID {
			targetAddresses = append(targetAddresses, address)
		}
	}
	restoreErr := s.hub.RestoreIntegrationResources(targetConnections, targetAddresses)
	if credentialRollbackErr != nil || integrationRollbackErr != nil || restoreErr != nil {
		return nil, &hub.HubError{Status: 500, Message: fmt.Sprintf("Parall import failed (%v); rollback failed (credential: %v, integration: %v, restore: %v)", setupErr, credentialRollbackErr, integrationRollbackErr, restoreErr)}
	}
	return nil, setupErr
}

func (s *Server) setupParall(ctx context.Context, p parallSetupParams, hubURL string) (map[string]any, error) {
	agentKey := strings.TrimSpace(p.Agent)
	if agentKey == "" {
		return nil, &hub.HubError{Status: 400, Message: "Choose a Loom Agent for this Parall identity"}
	}
	agentID := ""
	for _, candidate := range s.hub.ListAgents() {
		if candidate.Name == agentKey || candidate.ID == agentKey {
			agentID, agentKey = candidate.ID, candidate.Name
			break
		}
	}
	if agentID == "" {
		return nil, &hub.HubError{Status: 404, Message: "Agent not found: " + agentKey}
	}
	orgID := strings.TrimSpace(p.OrgID)
	if orgID == "" {
		return nil, &hub.HubError{Status: 400, Message: "Parall Organization ID is required"}
	}
	externalID := strings.TrimSpace(p.ExternalAgentID)
	chatID := strings.TrimSpace(p.ChatID)
	ownerCredentials, ownerCredentialErr := loadParallOwnerCredentials(orgID)
	var ownerClient parallAPI
	ownerReady := false
	if ownerCredentialErr == nil && ownerCredentials.APIURL != "" && ownerCredentials.APIKey != "" {
		ownerClient = newParallClient(ownerCredentials.APIURL, ownerCredentials.APIKey)
		organizations, organizationsErr := ownerClient.GetOrganizations(ctx)
		if organizationsErr == nil {
			organization, found := findParallOrganization(organizations, orgID)
			ownerReady = found && organization.Role == "owner"
		}
	}
	if (externalID == "" || chatID != "") && !ownerReady {
		return nil, &hub.HubError{Status: 409, Message: "Parall Owner credentials are required to create an external Agent or manage a group conversation"}
	}
	var selectedChat *parall.Chat
	if chatID != "" {
		chats, chatErr := ownerClient.GetChats(ctx, orgID)
		if chatErr != nil {
			return nil, &hub.HubError{Status: 400, Message: "Read Parall conversations: " + chatErr.Error()}
		}
		for i := range chats {
			if chats[i].ID == chatID {
				selectedChat = &chats[i]
				break
			}
		}
		if selectedChat == nil {
			return nil, &hub.HubError{Status: 400, Message: "The selected Parall conversation is no longer available"}
		}
		if selectedChat.Type == "direct" {
			return nil, &hub.HubError{Status: 400, Message: "Parall direct messages are configured per person after the first inbound message; choose a group conversation here"}
		}
	}

	displayName := strings.TrimSpace(p.ExternalDisplayName)
	if externalID == "" && displayName == "" {
		displayName = agentKey
	}
	var external parall.User
	if externalID == "" {
		created, createErr := ownerClient.CreateAgent(ctx, orgID, displayName)
		if createErr != nil {
			return nil, &hub.HubError{Status: 400, Message: "Create Parall Agent: " + createErr.Error()}
		}
		if created.User.ID == "" || created.APIKey == "" {
			return nil, &hub.HubError{Status: 502, Message: "Parall created an Agent without a stable ID or one-time API key"}
		}
		external, externalID = created.User, created.User.ID
		if err := saveParallAgentCredentials(orgID, externalID, ownerCredentials.APIURL, created.APIKey); err != nil {
			return nil, &hub.HubError{Status: 500, Message: "Secure the new Parall Agent API key: " + err.Error()}
		}
	} else {
		credentials, loadErr := loadParallAgentCredentials(orgID, externalID)
		if loadErr != nil {
			return nil, &hub.HubError{Status: 500, Message: "Read Parall Agent credentials: " + loadErr.Error()}
		}
		if credentials.APIKey != "" {
			externalClient := newParallClient(credentials.APIURL, credentials.APIKey)
			verified, verifyErr := externalClient.GetAgentMe(ctx, orgID)
			if verifyErr != nil || verified.ID != externalID || verified.Status != "active" {
				return nil, &hub.HubError{Status: 409, Message: "Verify Parall Agent credentials: " + errorOrMismatch(verifyErr, externalID, verified.ID)}
			}
			external = verified
			if displayName != "" && displayName != external.DisplayName && ownerReady {
				updated, updateErr := ownerClient.UpdateAgent(ctx, orgID, externalID, displayName)
				if updateErr != nil {
					return nil, &hub.HubError{Status: 400, Message: "Rename Parall Agent: " + updateErr.Error()}
				}
				external = updated
			}
		} else {
			if !ownerReady {
				return nil, &hub.HubError{Status: 409, Message: "Parall Agent credentials are not configured; import the existing Agent key or configure Owner access"}
			}
			agents, listErr := ownerClient.GetAgents(ctx, orgID)
			if listErr != nil {
				return nil, &hub.HubError{Status: 400, Message: "Read Parall Agents: " + listErr.Error()}
			}
			for _, candidate := range agents {
				if candidate.ID == externalID {
					external = candidate
					break
				}
			}
			if external.ID == "" {
				return nil, &hub.HubError{Status: 404, Message: "Parall Agent not found: " + externalID}
			}
			if displayName != "" && displayName != external.DisplayName {
				updated, updateErr := ownerClient.UpdateAgent(ctx, orgID, externalID, displayName)
				if updateErr != nil {
					return nil, &hub.HubError{Status: 400, Message: "Rename Parall Agent: " + updateErr.Error()}
				}
				external = updated
			}
			key, keyErr := ownerClient.CreateAgentAPIKey(ctx, orgID, externalID)
			if keyErr != nil {
				return nil, &hub.HubError{Status: 400, Message: "Create Parall Agent API key: " + keyErr.Error()}
			}
			if key.APIKey == "" {
				return nil, &hub.HubError{Status: 502, Message: "Parall did not return the new Agent API key"}
			}
			if err := saveParallAgentCredentials(orgID, externalID, ownerCredentials.APIURL, key.APIKey); err != nil {
				return nil, &hub.HubError{Status: 500, Message: "Secure the Parall Agent API key: " + err.Error()}
			}
		}
	}
	if external.DisplayName == "" {
		external.DisplayName = displayName
	}
	agentCredentials, err := loadParallAgentCredentials(orgID, externalID)
	if err != nil || agentCredentials.APIKey == "" {
		return nil, &hub.HubError{Status: 409, Message: "Parall Agent credentials are unavailable after setup"}
	}
	externalClient := newParallClient(agentCredentials.APIURL, agentCredentials.APIKey)
	verified, err := externalClient.GetAgentMe(ctx, orgID)
	if err != nil || verified.ID != externalID {
		return nil, &hub.HubError{Status: 409, Message: "Verify Parall Agent credentials: " + errorOrMismatch(err, externalID, verified.ID)}
	}
	if _, err := externalClient.GetWSTicket(ctx); err != nil {
		return nil, &hub.HubError{Status: 409, Message: "Verify Parall WebSocket: " + err.Error()}
	}

	credentialRef := "keychain:" + parall.AgentCredentialService(orgID, externalID)
	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		return nil, err
	}
	selection, err := selectParallIntegrationIdentity(s.hub.ListConnections(), addresses, agentID, "prll://"+externalID, orgID, credentialRef)
	if err != nil {
		return nil, err
	}
	connection := selection.Connection
	capabilities := []string{"receive_events", "threads", "mentions", "attachments", "reading", "ack", "proactive_send"}
	if connection.ID == "" {
		connection, err = s.hub.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: orgID, CredentialRef: credentialRef, Capabilities: capabilities})
	} else {
		enabled := true
		connection, err = s.hub.UpdateConnection(connection.ID, hub.ConnectionParams{AccountRef: orgID, CredentialRef: credentialRef, Capabilities: capabilities, Enabled: &enabled})
	}
	if err != nil {
		return nil, err
	}

	address := selection.Address
	if address.ID == "" {
		trustDomain := strings.TrimSpace(p.TrustDomain)
		if trustDomain == "" {
			for _, candidate := range addresses {
				if candidate.AgentID == agentID {
					trustDomain = candidate.TrustDomain
					break
				}
			}
		}
		if trustDomain == "" {
			trustDomain = "parall:" + orgID
		}
		address, err = s.hub.CreateAddress(hub.AddressParams{Agent: agentKey, ConnectionID: connection.ID, ExternalIdentity: "prll://" + externalID, DisplayName: external.DisplayName, TriggerPolicy: "explicit_dispatch", ReplyPolicy: "final_answer", DMPolicy: "managed", TrustDomain: trustDomain})
		if err != nil {
			return nil, err
		}
	} else {
		enabled := true
		address, err = s.hub.UpdateAddress(address.ID, hub.AddressParams{DisplayName: external.DisplayName, Enabled: &enabled})
		if err != nil {
			return nil, err
		}
	}

	memberships := []hub.ConversationMembership{}
	if chatID != "" {
		memberChats, _ := ownerClient.GetMemberChats(ctx, orgID, externalID)
		if !containsParallChat(memberChats, chatID) {
			if err := ownerClient.AddChatMember(ctx, orgID, chatID, externalID); err != nil {
				return nil, &hub.HubError{Status: 400, Message: "Add Parall Agent to conversation: " + err.Error()}
			}
		}
		name := strings.TrimSpace(selectedChat.Name)
		if name == "" {
			name = chatID
		}
		purpose := strings.TrimSpace(p.Purpose)
		if purpose == "" {
			purpose = strings.TrimSpace(selectedChat.Description)
		}
		if purpose == "" {
			purpose = "Support the work of " + name
		}
		role := strings.TrimSpace(p.Role)
		if role == "" {
			role = agentKey + " is the domain Agent serving this conversation"
		}
		guidance := strings.TrimSpace(p.Guidance)
		if guidance == "" {
			guidance = "Respond to explicit Parall dispatches. Stay within the Agent's domain and the purpose of this conversation."
		}
		conversationType := "group"
		trigger, reply, trust := "explicit_dispatch", "final_answer", address.TrustDomain
		membership, _, err := s.hub.UpsertConversationMembership(hub.ConversationMembershipParams{AddressID: address.ID, ConversationID: chatID, ConversationType: &conversationType, DisplayName: &name, Purpose: &purpose, Role: &role, Guidance: &guidance, TriggerPolicy: &trigger, ReplyPolicy: &reply, TrustDomain: &trust})
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}
	gateway, err := installManagedParallGateway(s, connection, address, orgID, externalID, hubURL)
	if err != nil {
		return nil, &hub.HubError{Status: 500, Message: "Configure Parall gateway: " + err.Error()}
	}
	var consolidation *hub.IntegrationConsolidationResult
	retirementWarning := ""
	if len(selection.DuplicateConnectionIDs) > 0 || len(selection.DuplicateAddressIDs) > 0 {
		value, err := s.hub.ConsolidateIntegrationIdentity(connection.ID, address.ID, selection.DuplicateConnectionIDs, selection.DuplicateAddressIDs)
		if err != nil {
			return nil, err
		}
		consolidation = &value
		if err := retireManagedParallGateways(s, selection.DuplicateConnectionIDs); err != nil {
			// The archived transport can no longer receive ingress or heartbeat. Keep
			// the canonical identity usable and surface host cleanup separately.
			retirementWarning = err.Error()
		}
	}
	result := map[string]any{"connection": connection, "address": address, "memberships": memberships, "discovery": s.discoverParall(ctx, connection.ID, orgID, externalID), "gateway": gateway}
	if consolidation != nil {
		result["consolidation"] = consolidation
	}
	if retirementWarning != "" {
		result["retirementWarning"] = retirementWarning
	}
	return result, nil
}

func (s *Server) repairParallGateway(ctx context.Context, connectionID, hubURL string) (map[string]any, error) {
	connectionID = strings.TrimSpace(connectionID)
	var connection hub.PlatformConnection
	for _, candidate := range s.hub.ListConnections() {
		if candidate.ID == connectionID {
			connection = candidate
			break
		}
	}
	if connection.ID == "" || connection.Provider != "parall" {
		return nil, &hub.HubError{Status: 404, Message: "Parall connection not found: " + connectionID}
	}
	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		return nil, err
	}
	var address hub.AgentAddress
	for _, candidate := range addresses {
		if candidate.ConnectionID == connection.ID {
			address = candidate
			break
		}
	}
	if address.ID == "" {
		return nil, &hub.HubError{Status: 409, Message: "Bind a Loom Agent Address before starting the Parall gateway"}
	}
	orgID := strings.TrimSpace(connection.AccountRef)
	agentID := strings.TrimPrefix(strings.TrimSpace(address.ExternalIdentity), "prll://")
	if orgID == "" || agentID == "" {
		return nil, &hub.HubError{Status: 409, Message: "Parall Organization ID and external Agent ID are required"}
	}
	credentials, err := loadParallAgentCredentials(orgID, agentID)
	if err != nil || credentials.APIURL == "" || credentials.APIKey == "" {
		return nil, &hub.HubError{Status: 409, Message: "Parall Agent credentials are not stored in Keychain"}
	}
	client := newParallClient(credentials.APIURL, credentials.APIKey)
	external, err := client.GetAgentMe(ctx, orgID)
	if err != nil {
		return nil, &hub.HubError{Status: 409, Message: "Verify Parall Agent credentials: " + err.Error()}
	}
	if external.ID != agentID || external.Status != "active" {
		return nil, &hub.HubError{Status: 409, Message: fmt.Sprintf("Parall Agent %s is unavailable or does not match %s", external.ID, agentID)}
	}
	if _, err := client.GetWSTicket(ctx); err != nil {
		return nil, &hub.HubError{Status: 409, Message: "Verify Parall WebSocket: " + err.Error()}
	}
	if external.DisplayName != "" && external.DisplayName != address.DisplayName {
		address, err = s.hub.UpdateAddress(address.ID, hub.AddressParams{DisplayName: external.DisplayName})
		if err != nil {
			return nil, err
		}
	}
	gateway, err := installManagedParallGateway(s, connection, address, orgID, agentID, hubURL)
	if err != nil {
		return nil, &hub.HubError{Status: 500, Message: "Configure Parall gateway: " + err.Error()}
	}
	return map[string]any{
		"connection": connection, "address": address, "gateway": gateway,
		"discovery": s.discoverParall(ctx, connection.ID, orgID, agentID),
	}, nil
}

func findParallOrganization(items []parall.Organization, orgID string) (parall.Organization, bool) {
	for _, item := range items {
		if item.ID == orgID {
			return item, true
		}
	}
	return parall.Organization{}, false
}

func containsParallChat(items []parall.Chat, chatID string) bool {
	for _, item := range items {
		if item.ID == chatID {
			return true
		}
	}
	return false
}

type parallIntegrationIdentitySelection struct {
	Connection             hub.PlatformConnection
	Address                hub.AgentAddress
	DuplicateConnectionIDs []string
	DuplicateAddressIDs    []string
}

func selectParallIntegrationIdentity(connections []hub.PlatformConnection, addresses []hub.AgentAddress, agentID, externalIdentity, orgID, credentialRef string) (parallIntegrationIdentitySelection, error) {
	connectionByID := map[string]hub.PlatformConnection{}
	for _, connection := range connections {
		connectionByID[connection.ID] = connection
	}
	type candidate struct {
		connection hub.PlatformConnection
		address    hub.AgentAddress
		score      int
	}
	candidates := []candidate{}
	seenConnection := map[string]bool{}
	for _, address := range addresses {
		if address.ExternalIdentity != externalIdentity {
			continue
		}
		if address.ArchivedAt != "" {
			continue
		}
		if address.AgentID != agentID {
			return parallIntegrationIdentitySelection{}, &hub.HubError{Status: 409, Message: "This Parall identity is already assigned to another Loom Agent"}
		}
		connection, ok := connectionByID[address.ConnectionID]
		if !ok || connection.Provider != "parall" || connection.ArchivedAt != "" {
			continue
		}
		score := 1000
		if connection.CredentialRef == credentialRef {
			score += 200
		}
		if connection.AccountRef == orgID {
			score += 50
		} else if connection.AccountRef == "org-agent:"+strings.TrimPrefix(externalIdentity, "prll://") {
			score += 10
		}
		candidates = append(candidates, candidate{connection: connection, address: address, score: score})
		seenConnection[connection.ID] = true
	}
	for _, connection := range connections {
		if connection.Provider != "parall" || connection.ArchivedAt != "" || connection.CredentialRef != credentialRef || seenConnection[connection.ID] {
			continue
		}
		candidates = append(candidates, candidate{connection: connection, score: 200})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].connection.CreatedAt < candidates[j].connection.CreatedAt
	})
	selection := parallIntegrationIdentitySelection{}
	if len(candidates) > 0 {
		selection.Connection = candidates[0].connection
		selection.Address = candidates[0].address
	}
	for i := 1; i < len(candidates); i++ {
		candidate := candidates[i]
		if candidate.address.ID != "" {
			selection.DuplicateAddressIDs = append(selection.DuplicateAddressIDs, candidate.address.ID)
		}
		if candidate.connection.ID != "" && candidate.connection.ID != selection.Connection.ID {
			selection.DuplicateConnectionIDs = append(selection.DuplicateConnectionIDs, candidate.connection.ID)
		}
	}
	selection.DuplicateConnectionIDs = orderedUnique(selection.DuplicateConnectionIDs)
	selection.DuplicateAddressIDs = orderedUnique(selection.DuplicateAddressIDs)
	return selection, nil
}

func orderedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func connectionUsesExternalIdentity(connectionID, externalIdentity string, addresses []hub.AgentAddress) bool {
	for _, address := range addresses {
		if address.ConnectionID == connectionID && address.ExternalIdentity == externalIdentity {
			return true
		}
	}
	return false
}

func parallAgentIDFromCredentialRef(value string) string {
	const marker = ".agent."
	value = strings.TrimPrefix(strings.TrimSpace(value), "keychain:com.codexloom.parall")
	index := strings.Index(value, marker)
	if index < 0 {
		return ""
	}
	rest := value[index+len(marker):]
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func errorOrMismatch(err error, expected, actual string) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("expected %s, received %s", expected, actual)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
