package feishu

import "testing"

func TestNormalizeChatsDeduplicatesAndKeepsUsefulMetadata(t *testing.T) {
	chats := normalizeChats([]Chat{
		{ID: "oc_beta", Name: "Beta"},
		{ID: "oc_alpha", Name: "oc_alpha", External: false},
		{ID: "oc_alpha", Name: "Alpha", Description: "Alpha work", Avatar: "avatar", External: true},
		{ID: " oc_beta ", Name: " Beta ", Description: "Beta work"},
		{ID: ""},
	})

	if len(chats) != 2 {
		t.Fatalf("chats = %#v", chats)
	}
	if chats[0].ID != "oc_alpha" || chats[0].Name != "Alpha" || chats[0].Description != "Alpha work" || chats[0].Avatar != "avatar" || !chats[0].External {
		t.Fatalf("alpha = %#v", chats[0])
	}
	if chats[1].ID != "oc_beta" || chats[1].Description != "Beta work" {
		t.Fatalf("beta = %#v", chats[1])
	}
}
