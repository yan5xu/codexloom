package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestSetupParallCreatesStableIdentityAndMembershipIdempotently(t *testing.T) {
	fake := &fakeParallAPI{
		organizations: []parall.Organization{{ID: "org_test", Name: "Test Org", Role: "owner"}},
		chats:         []parall.Chat{{ID: "chat_alpha", Name: "Alpha", Description: "Alpha work", Type: "group", Visibility: "private"}},
	}
	restore := stubParall(t, fake)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-parall", Name: "parall-agent", Cwd: t.TempDir(), ThreadID: "thread-parall"}); err != nil {
		t.Fatal(err)
	}
	s := New(h, st, nil)
	params := parallSetupParams{Agent: "parall-agent", OrgID: "org_test", ExternalDisplayName: "Parall Lead", ChatID: "chat_alpha", Purpose: "Coordinate Alpha", Role: "Own Alpha questions", Guidance: "Stay in scope"}
	first, err := s.setupParall(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	params.ExternalAgentID = "usr_external"
	second, err := s.setupParall(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	if fake.createAgentCalls != 1 || fake.addMemberCalls != 1 {
		t.Fatalf("create calls = %d, add member calls = %d", fake.createAgentCalls, fake.addMemberCalls)
	}
	firstConnection := first["connection"].(hub.PlatformConnection)
	secondConnection := second["connection"].(hub.PlatformConnection)
	firstAddress := first["address"].(hub.AgentAddress)
	secondAddress := second["address"].(hub.AgentAddress)
	if firstConnection.ID != secondConnection.ID || firstAddress.ID != secondAddress.ID {
		t.Fatalf("setup was not idempotent: first=%#v/%#v second=%#v/%#v", firstConnection, firstAddress, secondConnection, secondAddress)
	}
	if firstAddress.ExternalIdentity != "prll://usr_external" || firstConnection.CredentialRef != "keychain:"+parall.AgentCredentialService("org_test", "usr_external") {
		t.Fatalf("connection/address = %#v / %#v", firstConnection, firstAddress)
	}
	memberships, err := h.ListConversationMemberships("parall-agent", firstAddress.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 1 || memberships[0].DisplayName != "Alpha" || memberships[0].Role != params.Role || memberships[0].TriggerPolicy != "explicit_dispatch" {
		t.Fatalf("memberships = %#v", memberships)
	}
	repaired, err := s.repairParallGateway(context.Background(), firstConnection.ID, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	if gateway := repaired["gateway"].(managedParallGateway); !gateway.Managed || gateway.Service != firstConnection.ID {
		t.Fatalf("repaired gateway = %#v", gateway)
	}
}

func TestSetupParallRejectsDirectChatBeforeCreatingExternalIdentity(t *testing.T) {
	fake := &fakeParallAPI{
		organizations: []parall.Organization{{ID: "org_test", Name: "Test Org", Role: "owner"}},
		chats:         []parall.Chat{{ID: "chat_dm", Name: "", Type: "direct", Visibility: "private"}},
	}
	restore := stubParall(t, fake)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-parall", Name: "parall-agent", Cwd: t.TempDir(), ThreadID: "thread-parall"}); err != nil {
		t.Fatal(err)
	}
	_, err = New(h, st, nil).setupParall(context.Background(), parallSetupParams{Agent: "parall-agent", OrgID: "org_test", ExternalDisplayName: "Parall Lead", ChatID: "chat_dm"}, "http://127.0.0.1:4870")
	if err == nil {
		t.Fatal("expected direct conversation to be rejected")
	}
	if fake.createAgentCalls != 0 {
		t.Fatalf("created %d external Agents before validating the conversation", fake.createAgentCalls)
	}
}

func TestDiscoverParallUsesAgentCredentialsWithoutOwnerAccess(t *testing.T) {
	fake := &fakeParallAPI{memberChats: []parall.Chat{{ID: "chat_joined", Name: "Joined group", Description: "Existing work", Type: "group"}}}
	oldNew := newParallClient
	oldLoadOwner := loadParallOwnerCredentials
	oldLoadAgent := loadParallAgentCredentials
	oldSaveAgent := saveParallAgentCredentials
	defer func() {
		newParallClient = oldNew
		loadParallOwnerCredentials = oldLoadOwner
		loadParallAgentCredentials = oldLoadAgent
		saveParallAgentCredentials = oldSaveAgent
	}()
	newParallClient = func(string, string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) { return parall.Credentials{}, nil }
	stored := parall.Credentials{}
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) {
		return stored, nil
	}
	saveParallAgentCredentials = func(_, _, apiURL, apiKey string) error {
		stored = parall.Credentials{APIURL: apiURL, APIKey: apiKey}
		return nil
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	result, err := New(h, st, nil).saveParallAgentCredential(context.Background(), parallAgentCredentialParams{
		APIURL: "https://api.example.test", OrgID: "org_test", AgentID: "usr_external", AgentAPIKey: "agent-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OwnerReady || !result.AgentCredentialStored || !result.ExternalReady || !result.SocketReady {
		t.Fatalf("discovery readiness = %#v", result)
	}
	if len(result.Chats) != 1 || result.Chats[0].ID != "chat_joined" || !result.Chats[0].Member {
		t.Fatalf("joined conversations = %#v", result.Chats)
	}
}

func TestImportParallAgentWithoutOwnerIsIdempotent(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, nil)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	s := New(h, st, nil)
	params := parallImportParams{Agent: "ai-community", APIURL: "https://api.example.test", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: "agent-key"}
	first, err := s.importParallAgent(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.importParallAgent(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	firstConnection := first["connection"].(hub.PlatformConnection)
	secondConnection := second["connection"].(hub.PlatformConnection)
	firstAddress := first["address"].(hub.AgentAddress)
	secondAddress := second["address"].(hub.AgentAddress)
	if firstConnection.ID != secondConnection.ID || firstAddress.ID != secondAddress.ID {
		t.Fatalf("import duplicated resources: %#v/%#v then %#v/%#v", firstConnection, firstAddress, secondConnection, secondAddress)
	}
	if len(h.ListConnections()) != 1 || stored.APIKey != "agent-key" || firstConnection.CredentialRef != "keychain:"+parall.AgentCredentialService("org_test", "usr_external") {
		t.Fatalf("import state: connections=%#v credentials=%#v", h.ListConnections(), *stored)
	}
}

func TestImportParallAgentReusesStoredCredentialWithoutKeyFile(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, nil)
	defer restore()
	*stored = parall.Credentials{APIURL: "https://api.example.test", APIKey: "stored-agent-key"}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	result, err := New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external",
	}, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	if reused, _ := result["credentialReused"].(bool); !reused || stored.APIKey != "stored-agent-key" {
		t.Fatalf("credential was not reused: result=%#v stored=%#v", result, *stored)
	}
}

func TestImportParallAgentDoesNotSendStoredCredentialToAnotherAPI(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, nil)
	defer restore()
	*stored = parall.Credentials{APIURL: "https://api.example.test", APIKey: "stored-agent-key"}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	_, err = New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", APIURL: "https://different.example.test",
	}, "http://127.0.0.1:4870")
	if err == nil || !strings.Contains(err.Error(), "Cannot override") {
		t.Fatalf("expected stored credential URL guard, got %v", err)
	}
}

func TestImportParallAgentMigratesSingleLegacyIdentityInPlace(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	_, restore := stubParallImport(t, fake, nil)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org-agent:usr_external", CredentialRef: "env:PRLL_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	legacyAddress, err := h.CreateAddress(hub.AddressParams{Agent: "ai-community", ConnectionID: legacy.ID, ExternalIdentity: "prll://usr_external", DisplayName: "Legacy Observer", TrustDomain: "parall:org_test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: "agent-key",
	}, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	connection := result["connection"].(hub.PlatformConnection)
	address := result["address"].(hub.AgentAddress)
	if connection.ID != legacy.ID || address.ID != legacyAddress.ID || connection.AccountRef != "org_test" || !strings.HasPrefix(connection.CredentialRef, "keychain:") {
		t.Fatalf("legacy identity was not migrated in place: connection=%#v address=%#v", connection, address)
	}
	if len(h.ListConnections()) != 1 {
		t.Fatalf("in-place migration created a duplicate: %#v", h.ListConnections())
	}
}

func TestImportParallAgentArchivesDuplicateIdentityAndConvergesMembership(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	_, restore := stubParallImport(t, fake, nil)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org-agent:usr_external", CredentialRef: "env:PRLL_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	legacyAddress, err := h.CreateAddress(hub.AddressParams{Agent: "ai-community", ConnectionID: legacy.ID, ExternalIdentity: "prll://usr_external", TrustDomain: "parall:org_test"})
	if err != nil {
		t.Fatal(err)
	}
	role, enabled := "Established observer role", false
	legacyMembership, _, err := h.UpsertConversationMembership(hub.ConversationMembershipParams{AddressID: legacyAddress.ID, ConversationID: "chat_daily", Role: &role, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	credentialRef := "keychain:" + parall.AgentCredentialService("org_test", "usr_external")
	managed, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org_test", CredentialRef: credentialRef})
	if err != nil {
		t.Fatal(err)
	}
	managedAddress, err := h.CreateAddress(hub.AddressParams{Agent: "ai-community", ConnectionID: managed.ID, ExternalIdentity: "prll://usr_external", TrustDomain: "parall:org_test"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	managedMembership, _, err := h.UpsertConversationMembership(hub.ConversationMembershipParams{AddressID: managedAddress.ID, ConversationID: "chat_daily", Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: "agent-key",
	}, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	if result["connection"].(hub.PlatformConnection).ID != managed.ID || result["address"].(hub.AgentAddress).ID != managedAddress.ID {
		t.Fatalf("managed identity was not selected as canonical: %#v", result)
	}
	connections := h.ListConnections()
	addresses, _ := h.ListAddresses("")
	memberships, _ := h.ListConversationMemberships("ai-community", "")
	var archivedConnection hub.PlatformConnection
	var archivedAddress hub.AgentAddress
	var archivedMembership, canonicalMembership hub.ConversationMembership
	for _, value := range connections {
		if value.ID == legacy.ID {
			archivedConnection = value
		}
	}
	for _, value := range addresses {
		if value.ID == legacyAddress.ID {
			archivedAddress = value
		}
	}
	for _, value := range memberships {
		switch value.ID {
		case legacyMembership.ID:
			archivedMembership = value
		case managedMembership.ID:
			canonicalMembership = value
		}
	}
	if archivedConnection.ArchivedAt == "" || archivedConnection.SupersededBy != managed.ID || archivedConnection.Enabled {
		t.Fatalf("legacy connection was not archived: %#v", archivedConnection)
	}
	if archivedAddress.ArchivedAt == "" || archivedAddress.SupersededBy != managedAddress.ID || archivedAddress.Enabled {
		t.Fatalf("legacy address was not archived: %#v", archivedAddress)
	}
	if archivedMembership.ArchivedAt == "" || archivedMembership.SupersededBy != managedMembership.ID || archivedMembership.Enabled {
		t.Fatalf("legacy membership was not archived: %#v", archivedMembership)
	}
	if canonicalMembership.Enabled || canonicalMembership.Role != role || canonicalMembership.ArchivedAt != "" {
		t.Fatalf("canonical membership did not inherit the active policy: %#v", canonicalMembership)
	}
	if _, err := h.UpdateConnection(legacy.ID, hub.ConnectionParams{AccountRef: "mutated"}); err == nil {
		t.Fatal("archived connection remained mutable")
	}
	if _, err := h.UpdateAddress(legacyAddress.ID, hub.AddressParams{DisplayName: "mutated"}); err == nil {
		t.Fatal("archived address remained mutable")
	}
	if _, err := h.ReplaceConversationCandidates(legacyAddress.ID, hub.ConversationCandidateSnapshotParams{}); err == nil {
		t.Fatal("archived address still accepted discovery updates")
	}
}

func TestImportParallAgentRollsBackCredentialAndResourcesOnGatewayFailure(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, errors.New("gateway unavailable"))
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	_, err = New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: "agent-key",
	}, "http://127.0.0.1:4870")
	if err == nil {
		t.Fatal("expected gateway failure")
	}
	addresses, listErr := h.ListAddresses("")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(h.ListConnections()) != 0 || len(addresses) != 0 || stored.APIKey != "" {
		t.Fatalf("partial import remained: connections=%#v addresses=%#v credentials=%#v", h.ListConnections(), addresses, *stored)
	}
}

func TestImportParallAgentRestoresLegacyConnectionOnGatewayFailure(t *testing.T) {
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, errors.New("gateway unavailable"))
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-community", Name: "ai-community", Cwd: t.TempDir(), ThreadID: "thread-community"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org_test", CredentialRef: "env:PRLL_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateAddress(hub.AddressParams{Agent: "ai-community", ConnectionID: legacy.ID, ExternalIdentity: "prll://usr_external", DisplayName: "Legacy Observer", TrustDomain: "parall:org_test"}); err != nil {
		t.Fatal(err)
	}
	_, err = New(h, st, nil).importParallAgent(context.Background(), parallImportParams{
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: "agent-key",
	}, "http://127.0.0.1:4870")
	if err == nil {
		t.Fatal("expected gateway failure")
	}
	connections := h.ListConnections()
	addresses, listErr := h.ListAddresses("")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(connections) != 1 || connections[0].ID != legacy.ID || connections[0].CredentialRef != "env:PRLL_API_KEY" {
		t.Fatalf("legacy connection was not restored: %#v", connections)
	}
	if len(addresses) != 1 || addresses[0].DisplayName != "Legacy Observer" || stored.APIKey != "" {
		t.Fatalf("legacy address/credential was not restored: addresses=%#v credentials=%#v", addresses, *stored)
	}
}

type fakeParallAPI struct {
	organizations    []parall.Organization
	agents           []parall.User
	chats            []parall.Chat
	memberChats      []parall.Chat
	createAgentCalls int
	addMemberCalls   int
}

func (f *fakeParallAPI) GetMe(context.Context) (parall.User, error) {
	return parall.User{ID: "usr_owner", DisplayName: "Owner", Status: "active"}, nil
}

func (f *fakeParallAPI) GetOrganizations(context.Context) ([]parall.Organization, error) {
	return f.organizations, nil
}

func (f *fakeParallAPI) GetAgents(context.Context, string) ([]parall.User, error) {
	return append([]parall.User(nil), f.agents...), nil
}

func (f *fakeParallAPI) GetChats(context.Context, string) ([]parall.Chat, error) {
	return append([]parall.Chat(nil), f.chats...), nil
}

func (f *fakeParallAPI) GetMemberChats(context.Context, string, string) ([]parall.Chat, error) {
	return append([]parall.Chat(nil), f.memberChats...), nil
}

func (f *fakeParallAPI) CreateAgent(_ context.Context, _ string, name string) (parall.CreateAgentResponse, error) {
	f.createAgentCalls++
	user := parall.User{ID: "usr_external", DisplayName: name, Status: "active", Presence: &parall.Presence{Online: true, Status: "online"}}
	f.agents = append(f.agents, user)
	return parall.CreateAgentResponse{User: user, APIKey: "agent-key"}, nil
}

func (f *fakeParallAPI) UpdateAgent(_ context.Context, _, agentID, name string) (parall.User, error) {
	for i := range f.agents {
		if f.agents[i].ID == agentID {
			f.agents[i].DisplayName = name
			return f.agents[i], nil
		}
	}
	return parall.User{}, nil
}

func (f *fakeParallAPI) CreateAgentAPIKey(context.Context, string, string) (parall.APIKey, error) {
	return parall.APIKey{ID: "key_1", APIKey: "agent-key"}, nil
}

func (f *fakeParallAPI) AddChatMember(_ context.Context, _, chatID, _ string) error {
	f.addMemberCalls++
	for _, chat := range f.chats {
		if chat.ID == chatID {
			f.memberChats = append(f.memberChats, chat)
		}
	}
	return nil
}

func (f *fakeParallAPI) GetAgentMe(context.Context, string) (parall.User, error) {
	for _, agent := range f.agents {
		if agent.ID == "usr_external" {
			return agent, nil
		}
	}
	return parall.User{ID: "usr_external", DisplayName: "Parall Lead", Status: "active"}, nil
}

func (f *fakeParallAPI) GetWSTicket(context.Context) (parall.Ticket, error) {
	return parall.Ticket{Ticket: "ticket", WSURL: "wss://example.test/ws"}, nil
}

func stubParall(t *testing.T, fake *fakeParallAPI) func() {
	t.Helper()
	oldNew := newParallClient
	oldLoadOwner, oldSaveOwner := loadParallOwnerCredentials, saveParallOwnerCredentials
	oldLoadAgent, oldSaveAgent := loadParallAgentCredentials, saveParallAgentCredentials
	oldInstall, oldRetire := installManagedParallGateway, retireManagedParallGateways
	agentCredentials := parall.Credentials{}
	newParallClient = func(apiURL, apiKey string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) {
		return parall.Credentials{APIURL: "https://api.example.test", APIKey: "owner-key"}, nil
	}
	saveParallOwnerCredentials = func(string, string, string) error { return nil }
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) { return agentCredentials, nil }
	saveParallAgentCredentials = func(_, _, apiURL, apiKey string) error {
		agentCredentials = parall.Credentials{APIURL: apiURL, APIKey: apiKey}
		return nil
	}
	installManagedParallGateway = func(_ *Server, connection hub.PlatformConnection, _ hub.AgentAddress, orgID, agentID, _ string) (managedParallGateway, error) {
		if orgID != "org_test" || agentID != "usr_external" {
			t.Fatalf("gateway identity = %s/%s", orgID, agentID)
		}
		return managedParallGateway{Managed: true, Manager: "test", Service: connection.ID}, nil
	}
	retireManagedParallGateways = func(_ *Server, _ []string) error { return nil }
	return func() {
		newParallClient = oldNew
		loadParallOwnerCredentials, saveParallOwnerCredentials = oldLoadOwner, oldSaveOwner
		loadParallAgentCredentials, saveParallAgentCredentials = oldLoadAgent, oldSaveAgent
		installManagedParallGateway, retireManagedParallGateways = oldInstall, oldRetire
	}
}

func stubParallImport(t *testing.T, fake *fakeParallAPI, installErr error) (*parall.Credentials, func()) {
	t.Helper()
	oldNew := newParallClient
	oldLoadOwner := loadParallOwnerCredentials
	oldLoadAgent, oldSaveAgent, oldDeleteAgent := loadParallAgentCredentials, saveParallAgentCredentials, deleteParallAgentCredentials
	oldInstall, oldRetire := installManagedParallGateway, retireManagedParallGateways
	stored := &parall.Credentials{}
	newParallClient = func(string, string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) { return parall.Credentials{}, nil }
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) { return *stored, nil }
	saveParallAgentCredentials = func(_, _, apiURL, apiKey string) error {
		*stored = parall.Credentials{APIURL: apiURL, APIKey: apiKey}
		return nil
	}
	deleteParallAgentCredentials = func(string, string) error {
		*stored = parall.Credentials{}
		return nil
	}
	installManagedParallGateway = func(_ *Server, connection hub.PlatformConnection, _ hub.AgentAddress, _, _, _ string) (managedParallGateway, error) {
		if installErr != nil {
			return managedParallGateway{}, installErr
		}
		return managedParallGateway{Managed: true, Manager: "test", Service: connection.ID}, nil
	}
	retireManagedParallGateways = func(_ *Server, _ []string) error { return nil }
	return stored, func() {
		newParallClient = oldNew
		loadParallOwnerCredentials = oldLoadOwner
		loadParallAgentCredentials, saveParallAgentCredentials, deleteParallAgentCredentials = oldLoadAgent, oldSaveAgent, oldDeleteAgent
		installManagedParallGateway, retireManagedParallGateways = oldInstall, oldRetire
	}
}
