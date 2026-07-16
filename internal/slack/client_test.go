package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestDiscoverVerifiesTokensAndListsChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatal("missing authorization")
		}
		_ = r.ParseForm()
		responses := map[string]any{
			"/auth.test":             map[string]any{"ok": true, "team": "Acme", "team_id": "T1", "user": "loom", "user_id": "U1", "bot_id": "B1"},
			"/bots.info":             map[string]any{"ok": true, "bot": map[string]any{"app_id": "A1", "name": "CodexLoom", "user_id": "U1"}},
			"/apps.connections.open": map[string]any{"ok": true, "url": "wss://example.invalid"},
			"/conversations.list": map[string]any{"ok": true, "channels": []any{
				map[string]any{"id": "C2", "name": "random", "is_member": false},
				map[string]any{"id": "C1", "name": "general", "is_member": true, "purpose": map[string]any{"value": "Company updates"}},
			}, "response_metadata": map[string]any{"next_cursor": ""}},
		}
		response, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	discovery, err := (&Client{BaseURL: server.URL, HTTPClient: server.Client()}).Discover(context.Background(), "xoxb", "xapp")
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Identity.AppID != "A1" || discovery.Identity.TeamID != "T1" || discovery.Identity.BotName != "CodexLoom" {
		t.Fatalf("identity = %#v", discovery.Identity)
	}
	if got := []string{discovery.Channels[0].ID, discovery.Channels[1].ID}; !reflect.DeepEqual(got, []string{"C1", "C2"}) {
		t.Fatalf("channel order = %#v", got)
	}
}

func TestDiscoverReturnsPartialIdentityOnMissingChannelScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responses := map[string]any{
			"/auth.test":             map[string]any{"ok": true, "team": "Acme", "team_id": "T1", "user": "loom", "user_id": "U1", "bot_id": "B1"},
			"/bots.info":             map[string]any{"ok": true, "bot": map[string]any{"app_id": "A1", "name": "CodexLoom", "user_id": "U1"}},
			"/apps.connections.open": map[string]any{"ok": true, "url": "wss://example.invalid"},
			"/conversations.list":    map[string]any{"ok": false, "error": "missing_scope", "needed": "channels:read,groups:read", "provided": "chat:write"},
		}
		_ = json.NewEncoder(w).Encode(responses[r.URL.Path])
	}))
	defer server.Close()

	discovery, err := (&Client{BaseURL: server.URL, HTTPClient: server.Client()}).Discover(context.Background(), "xoxb", "xapp")
	if discovery.Identity.AppID != "A1" {
		t.Fatalf("partial discovery = %#v", discovery)
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "missing_scope" || !reflect.DeepEqual(apiErr.Needed, []string{"channels:read", "groups:read"}) {
		t.Fatalf("error = %#v", err)
	}
}
