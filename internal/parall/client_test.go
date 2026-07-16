package parall

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCreatesAgentAndPaginatesChats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/orgs/org_1/agents":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["runtime_type"] != "codex" || body["model_management"] != "self" {
				t.Fatalf("body = %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "usr_1", "display_name": body["display_name"]}, "api_key": "agent-key"})
		case "/api/v1/orgs/org_1/chats":
			if r.URL.Query().Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "chat_2", "name": "Zulu"}}, "has_more": true, "next_cursor": "next"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "chat_1", "name": "Alpha"}}, "has_more": false})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "owner-key")
	created, err := client.CreateAgent(context.Background(), "org_1", "Loom Agent")
	if err != nil || created.User.ID != "usr_1" || created.APIKey != "agent-key" {
		t.Fatalf("created = %#v, %v", created, err)
	}
	chats, err := client.GetChats(context.Background(), "org_1")
	if err != nil || len(chats) != 2 || chats[0].ID != "chat_1" {
		t.Fatalf("chats = %#v, %v", chats, err)
	}
}
