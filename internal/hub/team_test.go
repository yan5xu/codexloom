package hub

import (
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestTeamAggregatesMessagesAndReplies(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["a"] = &Agent{ID: "sess_a", Name: "a", Status: "idle"}
	h.agents["b"] = &Agent{ID: "sess_b", Name: "b", Status: "running"}
	root := &AgentMessage{
		ID:             "msg_root",
		From:           "a",
		To:             "b",
		Subject:        "Need help",
		Body:           "Body",
		Response:       "required",
		Status:         "answered",
		DeliveryStatus: "delivered",
		CreatedAt:      "2026-07-09T01:00:00Z",
		UpdatedAt:      "2026-07-09T01:00:00Z",
	}
	reply := &AgentMessage{
		ID:             "msg_reply",
		From:           "b",
		To:             "a",
		Subject:        "Re: Need help",
		Body:           "Done",
		Response:       "none",
		ReplyTo:        root.ID,
		Status:         "closed",
		DeliveryStatus: "delivered",
		CreatedAt:      "2026-07-09T01:05:00Z",
		UpdatedAt:      "2026-07-09T01:05:00Z",
	}
	h.comms[root.ID] = root
	h.comms[reply.ID] = reply
	h.commOrder = []string{root.ID, reply.ID}

	team := h.Team()
	if len(team.ObservedLinks) != 1 {
		t.Fatalf("links = %#v, want one observed link", team.ObservedLinks)
	}
	link := team.ObservedLinks[0]
	if link.From != "a" || link.To != "b" || link.MessageCount != 1 || link.ReplyCount != 1 || link.AnsweredCount != 1 {
		t.Fatalf("link = %#v, want a->b with one message, reply and answered count", link)
	}
	var a, b *TeamAgent
	for i := range team.Agents {
		switch team.Agents[i].Name {
		case "a":
			a = &team.Agents[i]
		case "b":
			b = &team.Agents[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("agents = %#v, want a and b", team.Agents)
	}
	if a.MessageOut != 1 || a.MessageIn != 1 || b.MessageOut != 1 || b.MessageIn != 1 {
		t.Fatalf("a=%#v b=%#v, want bidirectional message metrics", a, b)
	}
}
