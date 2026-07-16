package httpapi

import (
	"context"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestDiscoverSlackUsesStoredCredentialsAndPreservesMissingScopeDetails(t *testing.T) {
	restore := stubSlack(t, true)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.CreateConnection(hub.ConnectionParams{
		Provider: "slack", AccountRef: "T_TEST", CredentialRef: "keychain:" + loomslack.CredentialService("A_TEST"),
	}); err != nil {
		t.Fatal(err)
	}
	discovery := New(h, st, nil).discoverSlack(context.Background(), "", "")
	if !discovery.Available || !discovery.CredentialStored || !discovery.BotReady || !discovery.SocketReady {
		t.Fatalf("discovery = %#v", discovery)
	}
	if len(discovery.MissingScopes) != 2 || discovery.MissingScopes[0] != "channels:read" {
		t.Fatalf("missing scopes = %#v", discovery.MissingScopes)
	}
}

func TestSetupSlackCreatesDurableAddressAndChannelRoleIdempotently(t *testing.T) {
	restore := stubSlack(t, false)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{
		ID: "agent-slack", Name: "slack-agent", Cwd: t.TempDir(), ThreadID: "thread-slack",
	}); err != nil {
		t.Fatal(err)
	}
	s := New(h, st, nil)
	params := slackSetupParams{
		Agent: "slack-agent", AppID: "A_TEST", TeamID: "T_TEST", ChannelID: "C_ALPHA",
		Purpose: "Coordinate Alpha", Role: "Own Alpha questions", Guidance: "Do not expose secrets",
	}
	first, err := s.setupSlack(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.setupSlack(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	firstConnection := first["connection"].(hub.PlatformConnection)
	secondConnection := second["connection"].(hub.PlatformConnection)
	firstAddress := first["address"].(hub.AgentAddress)
	secondAddress := second["address"].(hub.AgentAddress)
	if firstConnection.ID != secondConnection.ID || firstAddress.ID != secondAddress.ID {
		t.Fatalf("setup was not idempotent: first=%#v/%#v second=%#v/%#v", firstConnection, firstAddress, secondConnection, secondAddress)
	}
	if firstConnection.CredentialRef != "keychain:"+loomslack.CredentialService("A_TEST") {
		t.Fatalf("credential ref = %q", firstConnection.CredentialRef)
	}
	memberships, err := h.ListConversationMemberships("slack-agent", firstAddress.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 1 || memberships[0].DisplayName != "#alpha" || memberships[0].Role != params.Role || memberships[0].Guidance != params.Guidance {
		t.Fatalf("memberships = %#v", memberships)
	}
}

func stubSlack(t *testing.T, missingScopes bool) func() {
	t.Helper()
	oldLoad, oldSave, oldDiscover, oldInstall := loadSlackTokens, saveSlackTokens, discoverSlackClient, installManagedSlackGateway
	loadSlackTokens = func(appID, teamID string) (loomslack.Tokens, error) {
		return loomslack.Tokens{Bot: "xoxb-test", App: "xapp-test"}, nil
	}
	saveSlackTokens = func(appID, botToken, appToken string) error { return nil }
	discoverSlackClient = func(ctx context.Context, botToken, appToken string) (loomslack.Discovery, error) {
		discovery := loomslack.Discovery{
			Identity: loomslack.Identity{AppID: "A_TEST", TeamID: "T_TEST", TeamName: "Test Workspace", BotID: "B_TEST", BotUserID: "U_TEST", BotName: "Test Bot"},
			Channels: []loomslack.Channel{{ID: "C_ALPHA", Name: "alpha", Description: "Alpha work", Member: true}},
		}
		if missingScopes {
			discovery.Channels = nil
			return discovery, &loomslack.APIError{Method: "conversations.list", Code: "missing_scope", Needed: []string{"channels:read", "groups:read"}}
		}
		return discovery, nil
	}
	installManagedSlackGateway = func(s *Server, connection hub.PlatformConnection, address hub.AgentAddress, appID, teamID, botUserID, hubURL string) (managedSlackGateway, error) {
		return managedSlackGateway{Managed: true, Manager: "test", Service: connection.ID}, nil
	}
	return func() {
		loadSlackTokens, saveSlackTokens, discoverSlackClient, installManagedSlackGateway = oldLoad, oldSave, oldDiscover, oldInstall
	}
}
