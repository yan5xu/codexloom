package feishu

import "testing"

func TestDomainUsesOfficialRegionalBaseURL(t *testing.T) {
	for _, test := range []struct {
		input string
		want  Domain
		url   string
	}{
		{input: "", want: DomainFeishu, url: "https://open.feishu.cn"},
		{input: "feishu", want: DomainFeishu, url: "https://open.feishu.cn"},
		{input: " LARK ", want: DomainLark, url: "https://open.larksuite.com"},
	} {
		domain, err := ParseDomain(test.input)
		if err != nil || domain != test.want || OpenBaseURL(domain) != test.url {
			t.Fatalf("ParseDomain(%q) = %q, %v, URL %q", test.input, domain, err, OpenBaseURL(domain))
		}
	}
	if _, err := ParseDomain("example.com"); err == nil {
		t.Fatal("arbitrary provider domain was accepted")
	}
}

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
