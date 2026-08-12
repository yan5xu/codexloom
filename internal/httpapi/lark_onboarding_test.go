package httpapi

import (
	"context"
	"testing"

	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestDiscoverLarkUsesNativeCredentialAndListsChats(t *testing.T) {
	restore := stubFeishu(t)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	discovery := New(h, st, nil).discoverLark(context.Background(), "cli_test", "lark")
	if !discovery.Available || !discovery.BotReady || !discovery.CredentialStored || discovery.Runtime != "native" || discovery.AppID != "cli_test" || discovery.Domain != "lark" {
		t.Fatalf("discovery = %#v", discovery)
	}
	if len(discovery.Chats) != 2 || discovery.Chats[0].Name != "Alpha" || discovery.Chats[1].Name != "Zeta" {
		t.Fatalf("chats = %#v", discovery.Chats)
	}
}

func TestSetupLarkCreatesDurableAddressAndGroupRoleIdempotently(t *testing.T) {
	restore := stubFeishu(t)
	defer restore()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.RestoreAgent(hub.RestoreAgentParams{
		ID: "agent-lark", Name: "lark-agent", Cwd: t.TempDir(), ThreadID: "thread-lark",
	}); err != nil {
		t.Fatal(err)
	}
	s := New(h, st, nil)
	params := larkSetupParams{
		Agent: "lark-agent", AppID: "cli_test", Domain: "lark", ChatID: "oc_alpha", Purpose: "Coordinate Alpha",
		Role: "Own Alpha questions", Guidance: "Do not expose secrets",
	}
	first, err := s.setupLark(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.setupLark(context.Background(), params, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	repairParams := params
	repairParams.ChatID = ""
	repair, err := s.setupLark(context.Background(), repairParams, "http://127.0.0.1:4870")
	if err != nil {
		t.Fatal(err)
	}
	firstConnection := first["connection"].(hub.PlatformConnection)
	secondConnection := second["connection"].(hub.PlatformConnection)
	repairConnection := repair["connection"].(hub.PlatformConnection)
	firstAddress := first["address"].(hub.AgentAddress)
	secondAddress := second["address"].(hub.AgentAddress)
	repairAddress := repair["address"].(hub.AgentAddress)
	if firstConnection.ID != secondConnection.ID || firstConnection.ID != repairConnection.ID ||
		firstAddress.ID != secondAddress.ID || firstAddress.ID != repairAddress.ID {
		t.Fatalf("setup was not idempotent: first=%#v/%#v second=%#v/%#v repair=%#v/%#v", firstConnection, firstAddress, secondConnection, secondAddress, repairConnection, repairAddress)
	}
	if firstConnection.CredentialRef != "keychain:"+feishu.CredentialService("cli_test") {
		t.Fatalf("credential ref = %q", firstConnection.CredentialRef)
	}
	if firstConnection.Domain != "lark" {
		t.Fatalf("connection domain = %q", firstConnection.Domain)
	}
	memberships, err := h.ListConversationMemberships("lark-agent", firstAddress.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 1 || memberships[0].DisplayName != "Alpha" || memberships[0].Role != params.Role || memberships[0].Guidance != params.Guidance {
		t.Fatalf("memberships = %#v", memberships)
	}
	if repairMemberships := repair["memberships"].([]hub.ConversationMembership); len(repairMemberships) != 0 {
		t.Fatalf("repair unexpectedly recreated memberships: %#v", repairMemberships)
	}
}

func stubFeishu(t *testing.T) func() {
	t.Helper()
	oldLoad, oldSave, oldDiscover, oldInstall := loadFeishuSecret, saveFeishuSecret, discoverFeishu, installNativeFeishuGateway
	loadFeishuSecret = func(appID string) (string, error) { return "secret-value", nil }
	saveFeishuSecret = func(appID, secret string) error { return nil }
	discoverFeishu = func(ctx context.Context, appID, secret string, domain feishu.Domain) (feishu.Discovery, error) {
		if domain != feishu.DomainLark {
			t.Fatalf("discovery domain = %q", domain)
		}
		return feishu.Discovery{
			Bot: feishu.Bot{AppID: appID, OpenID: "ou_bot", Name: "Test Bot", ActivateStatus: 2},
			Chats: []feishu.Chat{
				{ID: "oc_alpha", Name: "Alpha", Description: "Alpha work"},
				{ID: "oc_alpha", Name: "Alpha duplicate"},
				{ID: "oc_zeta", Name: "Zeta", External: true},
			},
		}, nil
	}
	installNativeFeishuGateway = func(s *Server, connection hub.PlatformConnection, address hub.AgentAddress, appID, hubURL string) (managedFeishuGateway, error) {
		return managedFeishuGateway{Managed: true, Manager: "test", Service: connection.ID}, nil
	}
	return func() {
		loadFeishuSecret, saveFeishuSecret, discoverFeishu, installNativeFeishuGateway = oldLoad, oldSave, oldDiscover, oldInstall
	}
}
