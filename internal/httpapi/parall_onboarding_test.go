package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRepairParallGatewayRejectsLegacyReferencesBeforeSideEffects(t *testing.T) {
	oldNew := newParallClient
	oldLoadAgent := loadParallAgentCredentials
	oldInstall := installManagedParallGateway
	defer func() {
		newParallClient = oldNew
		loadParallAgentCredentials = oldLoadAgent
		installManagedParallGateway = oldInstall
	}()

	secretReads, providerCalls, installerCalls := 0, 0, 0
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) {
		secretReads++
		return parall.Credentials{}, errors.New("legacy credential loader must not run")
	}
	newParallClient = func(string, string) parallAPI {
		providerCalls++
		return &fakeParallAPI{}
	}
	installManagedParallGateway = func(_ *Server, _ hub.PlatformConnection, _ hub.AgentAddress, _, _, _ string) (managedParallGateway, error) {
		installerCalls++
		return managedParallGateway{}, nil
	}

	for _, reference := range []string{
		"keychain:" + parall.AgentCredentialService("org_test", "usr_external"),
		"env:LOOM_PARALL_LEGACY_TEST",
	} {
		t.Run(strings.SplitN(reference, ":", 2)[0], func(t *testing.T) {
			server, connection, _ := newParallRepairFixture(t, reference)
			result := integrationAPIRequest(t, server.Handler(), http.MethodPost, "/api/integrations/providers/parall/gateway", map[string]any{
				"connectionId": connection.ID,
			}, http.StatusConflict)
			if result["error"] != "migration_required" {
				t.Fatalf("legacy repair response = %#v", result)
			}
		})
	}
	if secretReads != 0 || providerCalls != 0 || installerCalls != 0 {
		t.Fatalf("legacy repair side effects: secret=%d provider=%d installer=%d", secretReads, providerCalls, installerCalls)
	}
}

func TestRepairParallGatewaySharesActiveMigrationFenceBeforeSideEffects(t *testing.T) {
	oldNew := newParallClient
	oldLoadAgent := loadParallAgentCredentials
	oldInstall := installManagedParallGateway
	t.Cleanup(func() {
		newParallClient = oldNew
		loadParallAgentCredentials = oldLoadAgent
		installManagedParallGateway = oldInstall
	})
	secretReads, providerCalls, installerCalls := 0, 0, 0
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) {
		secretReads++
		return parall.Credentials{}, errors.New("migration fence must precede secret read")
	}
	newParallClient = func(string, string) parallAPI {
		providerCalls++
		return &fakeParallAPI{}
	}
	installManagedParallGateway = func(_ *Server, _ hub.PlatformConnection, _ hub.AgentAddress, _, _, _ string) (managedParallGateway, error) {
		installerCalls++
		return managedParallGateway{}, nil
	}
	server, connection, _ := newParallRepairFixture(t, "keychain:"+parall.AgentCredentialService("org_test", "usr_external"))
	if _, _, err := server.hub.BeginCredentialMigration(connection); err != nil {
		t.Fatal(err)
	}
	result := integrationAPIRequest(t, server.Handler(), http.MethodPost, "/api/integrations/providers/parall/gateway", map[string]any{
		"connectionId": connection.ID,
	}, http.StatusConflict)
	if result["error"] != "credential_migration_in_progress" {
		t.Fatalf("active migration repair response = %#v", result)
	}
	if secretReads != 0 || providerCalls != 0 || installerCalls != 0 {
		t.Fatalf("active migration repair crossed fence: secret=%d provider=%d installer=%d", secretReads, providerCalls, installerCalls)
	}
}

func TestRepairParallGatewayAcceptsEligibleManagedConnection(t *testing.T) {
	fake := &fakeParallAPI{credential: randomTestCredential(t)}
	restore := stubParall(t, fake)
	defer restore()

	server, connection, _ := newManagedParallRepairFixture(t)
	result := integrationAPIRequest(t, server.Handler(), http.MethodPost, "/api/integrations/providers/parall/gateway", map[string]any{
		"connectionId": connection.ID,
	}, http.StatusOK)
	gateway, _ := result["gateway"].(map[string]any)
	if gateway["managed"] != true || gateway["service"] != connection.ID {
		t.Fatalf("managed repair response = %#v", result)
	}
}

func TestRepairParallGatewayRejectsIneligibleManagedConnectionOrAddress(t *testing.T) {
	oldNew := newParallClient
	oldInstall := installManagedParallGateway
	defer func() {
		newParallClient = oldNew
		installManagedParallGateway = oldInstall
	}()
	newParallClient = func(string, string) parallAPI {
		t.Fatal("ineligible managed repair called provider")
		return nil
	}
	installManagedParallGateway = func(_ *Server, _ hub.PlatformConnection, _ hub.AgentAddress, _, _, _ string) (managedParallGateway, error) {
		t.Fatal("ineligible managed repair called installer")
		return managedParallGateway{}, nil
	}

	t.Run("disabled connection", func(t *testing.T) {
		server, h, connection, _ := newManagedParallRepairFixtureWithHub(t)
		disabled := false
		if _, err := h.UpdateConnection(connection.ID, hub.ConnectionParams{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		result := integrationAPIRequest(t, server.Handler(), http.MethodPost, "/api/integrations/providers/parall/gateway", map[string]any{
			"connectionId": connection.ID,
		}, http.StatusConflict)
		if result["error"] != "Parall connection is not eligible for repair" {
			t.Fatalf("disabled connection repair response = %#v", result)
		}
	})

	t.Run("disabled address", func(t *testing.T) {
		server, h, connection, address := newManagedParallRepairFixtureWithHub(t)
		disabled := false
		if _, err := h.UpdateAddress(address.ID, hub.AddressParams{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		result := integrationAPIRequest(t, server.Handler(), http.MethodPost, "/api/integrations/providers/parall/gateway", map[string]any{
			"connectionId": connection.ID,
		}, http.StatusConflict)
		if result["error"] != "An enabled Loom Agent Address is required before repairing the Parall gateway" {
			t.Fatalf("disabled address repair response = %#v", result)
		}
	})
}

func newParallRepairFixture(t *testing.T, credentialRef string) (*Server, hub.PlatformConnection, hub.AgentAddress) {
	t.Helper()
	t.Setenv("CODEX_LOOM_ADMIN_TOKEN", "parall-repair-test-admin-token")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	t.Cleanup(h.Shutdown)
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-parall-repair", Name: "parall-repair", Cwd: t.TempDir(), ThreadID: "thread-parall-repair"}); err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org_test", CredentialRef: credentialRef})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{Agent: "parall-repair", ConnectionID: connection.ID, ExternalIdentity: "prll://usr_external"})
	if err != nil {
		t.Fatal(err)
	}
	return New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}), connection, address
}

func newManagedParallRepairFixture(t *testing.T) (*Server, hub.PlatformConnection, hub.AgentAddress) {
	t.Helper()
	server, _, connection, address := newManagedParallRepairFixtureWithHub(t)
	return server, connection, address
}

func newManagedParallRepairFixtureWithHub(t *testing.T) (*Server, *hub.Hub, hub.PlatformConnection, hub.AgentAddress) {
	t.Helper()
	t.Setenv("CODEX_LOOM_ADMIN_TOKEN", "parall-repair-test-admin-token")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	t.Cleanup(h.Shutdown)
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{ID: "agent-parall-repair", Name: "parall-repair", Cwd: t.TempDir(), ThreadID: "thread-parall-repair"}); err != nil {
		t.Fatal(err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	reference, err := parall.SaveManagedAgentCredentials(credentials, "org_test", "usr_external", "https://api.example.test", randomTestCredential(t))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall", AccountRef: "org_test", CredentialRef: reference})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{Agent: "parall-repair", ConnectionID: connection.ID, ExternalIdentity: "prll://usr_external"})
	if err != nil {
		t.Fatal(err)
	}
	return New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}), h, connection, address
}

func TestSetupParallCreatesStableIdentityAndMembershipIdempotently(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
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
	if firstAddress.ExternalIdentity != "prll://usr_external" || !strings.HasPrefix(firstConnection.CredentialRef, credentialstore.ManagedReferencePrefix) {
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
	allowManagedCredentialWritesForTest(t)
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
	allowManagedCredentialWritesForTest(t)
	fake := &fakeParallAPI{memberChats: []parall.Chat{{ID: "chat_joined", Name: "Joined group", Description: "Existing work", Type: "group"}}}
	oldNew := newParallClient
	oldLoadOwner := loadParallOwnerCredentials
	oldLoadAgent := loadParallAgentCredentials
	defer func() {
		newParallClient = oldNew
		loadParallOwnerCredentials = oldLoadOwner
		loadParallAgentCredentials = oldLoadAgent
	}()
	newParallClient = func(string, string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) { return parall.Credentials{}, nil }
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) {
		return parall.Credentials{}, nil
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	result, err := New(h, st, nil).saveParallAgentCredential(context.Background(), parallAgentCredentialParams{
		APIURL: "https://api.example.test", OrgID: "org_test", AgentID: "usr_external", AgentAPIKey: randomTestCredential(t),
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
	allowManagedCredentialWritesForTest(t)
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
	s := New(h, st, nil)
	params := parallImportParams{Agent: "ai-community", APIURL: "https://api.example.test", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: randomTestCredential(t)}
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
	if len(h.ListConnections()) != 1 || !strings.HasPrefix(firstConnection.CredentialRef, credentialstore.ManagedReferencePrefix) {
		t.Fatalf("import state: connections=%#v", h.ListConnections())
	}
}

func TestImportParallAgentReusesStoredCredentialWithoutKeyFile(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, nil)
	defer restore()
	storedValue := randomTestCredential(t)
	*stored = parall.Credentials{APIURL: "https://api.example.test", APIKey: storedValue}
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
	if reused, _ := result["credentialReused"].(bool); !reused || stored.APIKey != storedValue {
		t.Fatalf("credential was not reused")
	}
}

func TestImportParallAgentDoesNotSendStoredCredentialToAnotherAPI(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
	fake := &fakeParallAPI{agents: []parall.User{{ID: "usr_external", DisplayName: "AI Observer", Status: "active"}}}
	stored, restore := stubParallImport(t, fake, nil)
	defer restore()
	*stored = parall.Credentials{APIURL: "https://api.example.test", APIKey: randomTestCredential(t)}
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
	allowManagedCredentialWritesForTest(t)
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
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: randomTestCredential(t),
	}, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	connection := result["connection"].(hub.PlatformConnection)
	address := result["address"].(hub.AgentAddress)
	if connection.ID != legacy.ID || address.ID != legacyAddress.ID || connection.AccountRef != "org_test" || !strings.HasPrefix(connection.CredentialRef, credentialstore.ManagedReferencePrefix) {
		t.Fatalf("legacy identity was not migrated in place: connection=%#v address=%#v", connection, address)
	}
	if len(h.ListConnections()) != 1 {
		t.Fatalf("in-place migration created a duplicate: %#v", h.ListConnections())
	}
}

func TestImportParallAgentArchivesDuplicateIdentityAndConvergesMembership(t *testing.T) {
	allowManagedCredentialWritesForTest(t)
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
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: randomTestCredential(t),
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
	allowManagedCredentialWritesForTest(t)
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
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: randomTestCredential(t),
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
	allowManagedCredentialWritesForTest(t)
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
		Agent: "ai-community", OrgID: "org_test", ExternalAgentID: "usr_external", AgentAPIKey: randomTestCredential(t),
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
	credential       string
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
	return parall.CreateAgentResponse{User: user, APIKey: f.credential}, nil
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
	return parall.APIKey{ID: "key_1", APIKey: f.credential}, nil
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
	return parall.Ticket{Ticket: f.credential, WSURL: "wss://example.test/ws"}, nil
}

func stubParall(t *testing.T, fake *fakeParallAPI) func() {
	t.Helper()
	oldNew := newParallClient
	oldLoadOwner := loadParallOwnerCredentials
	oldLoadAgent := loadParallAgentCredentials
	oldInstall, oldRetire := installManagedParallGateway, retireManagedParallGateways
	fake.credential = randomTestCredential(t)
	ownerCredential := randomTestCredential(t)
	newParallClient = func(apiURL, apiKey string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) {
		return parall.Credentials{APIURL: "https://api.example.test", APIKey: ownerCredential}, nil
	}
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) { return parall.Credentials{}, nil }
	installManagedParallGateway = func(_ *Server, connection hub.PlatformConnection, _ hub.AgentAddress, orgID, agentID, _ string) (managedParallGateway, error) {
		if orgID != "org_test" || agentID != "usr_external" {
			t.Fatalf("gateway identity = %s/%s", orgID, agentID)
		}
		return managedParallGateway{Managed: true, Manager: "test", Service: connection.ID}, nil
	}
	retireManagedParallGateways = func(_ *Server, _ []string) error { return nil }
	return func() {
		newParallClient = oldNew
		loadParallOwnerCredentials = oldLoadOwner
		loadParallAgentCredentials = oldLoadAgent
		installManagedParallGateway, retireManagedParallGateways = oldInstall, oldRetire
	}
}

func stubParallImport(t *testing.T, fake *fakeParallAPI, installErr error) (*parall.Credentials, func()) {
	t.Helper()
	oldNew := newParallClient
	oldLoadOwner := loadParallOwnerCredentials
	oldLoadAgent := loadParallAgentCredentials
	oldInstall, oldRetire := installManagedParallGateway, retireManagedParallGateways
	stored := &parall.Credentials{}
	fake.credential = randomTestCredential(t)
	newParallClient = func(string, string) parallAPI { return fake }
	loadParallOwnerCredentials = func(string) (parall.Credentials, error) { return parall.Credentials{}, nil }
	loadParallAgentCredentials = func(string, string) (parall.Credentials, error) { return *stored, nil }
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
		loadParallAgentCredentials = oldLoadAgent
		installManagedParallGateway, retireManagedParallGateways = oldInstall, oldRetire
	}
}
