package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestProfileVersioningAndPersistence(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess_a"] = &Agent{ID: "sess_a", Name: "agent-a", Status: "idle"}

	profile, err := h.UpdateProfile("agent-a", ProfileParams{
		Identity: "Long-lived maintainer", Domain: "Agent collaboration", Scope: "Owns the hub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.AgentID != "sess_a" || profile.Version != 1 || profile.Domain != "Agent collaboration" {
		t.Fatalf("profile = %#v", profile)
	}

	stale := 0
	if _, err := h.UpdateProfile("agent-a", ProfileParams{Domain: "stale", ExpectedVersion: &stale}); err == nil {
		t.Fatal("expected version conflict")
	}
	wantVersion := 1
	updated, err := h.UpdateProfile("sess_a", ProfileParams{Domain: "Updated domain", ExpectedVersion: &wantVersion})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.Domain != "Updated domain" {
		t.Fatalf("updated = %#v", updated)
	}

	loaded := map[string]*AgentProfile{}
	if err := st.LoadProfiles(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded["sess_a"] == nil || loaded["sess_a"].Version != 2 {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestEmptyProfileDoesNotCreateVersionOrPersist(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess_a"] = &Agent{ID: "sess_a", Name: "agent-a", Status: "idle"}

	profile, err := h.UpdateProfile("agent-a", ProfileParams{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Version != 0 || h.profiles["sess_a"] != nil {
		t.Fatalf("empty profile should remain unconfigured: profile=%#v stored=%#v", profile, h.profiles["sess_a"])
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "profiles.json")); !os.IsNotExist(err) {
		t.Fatalf("profiles.json should not be created, err=%v", err)
	}
}

func TestRelationshipAndTeamUseStableAgentIDsAcrossRename(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess_a"] = &Agent{ID: "sess_a", Name: "agent-a", Status: "idle"}
	h.agents["sess_b"] = &Agent{ID: "sess_b", Name: "agent-b", Status: "idle"}
	if _, err := h.UpdateProfile("agent-a", ProfileParams{Domain: "Hub domain"}); err != nil {
		t.Fatal(err)
	}
	rel, err := h.CreateRelationship(RelationshipParams{From: "agent-a", To: "agent-b", Description: "Coordinate UI work"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.FromAgentID != "sess_a" || rel.ToAgentID != "sess_b" {
		t.Fatalf("relationship = %#v", rel)
	}

	h.agents["sess_b"].Name = "agent-b-renamed"
	team := h.Team()
	if len(team.ExplicitLinks) != 1 || team.ExplicitLinks[0].To != "agent-b-renamed" {
		t.Fatalf("explicit links = %#v", team.ExplicitLinks)
	}
	var a *TeamAgent
	for i := range team.Agents {
		if team.Agents[i].ID == "sess_a" {
			a = &team.Agents[i]
		}
	}
	if a == nil || a.Profile.Domain != "Hub domain" {
		t.Fatalf("agents = %#v", team.Agents)
	}
}

func TestCommMigrationInfersRenamedSenderFromReplyDelivery(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess_a"] = &Agent{ID: "sess_a", Name: "agent-a-renamed", Status: "idle"}
	h.agents["sess_b"] = &Agent{ID: "sess_b", Name: "agent-b", Status: "idle"}
	root := &AgentMessage{
		ID: "msg_root", From: "agent-a-old", To: "agent-b", Response: "required",
		Status: "answered", DeliveryStatus: "delivered", DeliveredSessionID: "sess_b",
	}
	reply := &AgentMessage{
		ID: "msg_reply", From: "agent-b", To: "agent-a-old", ReplyTo: root.ID, Response: "none",
		Status: "closed", DeliveryStatus: "delivered", DeliveredSessionID: "sess_a",
	}
	h.comms[root.ID], h.comms[reply.ID] = root, reply
	h.commOrder = []string{root.ID, reply.ID}
	if err := st.AppendComm(commRecord{Message: *root}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendComm(commRecord{Message: *reply}); err != nil {
		t.Fatal(err)
	}

	if err := h.migrateCommAgentIDsLocked(); err != nil {
		t.Fatal(err)
	}
	if root.FromAgentID != "sess_a" || root.ToAgentID != "sess_b" {
		t.Fatalf("root ids = %s -> %s", root.FromAgentID, root.ToAgentID)
	}
	if reply.FromAgentID != "sess_b" || reply.ToAgentID != "sess_a" {
		t.Fatalf("reply ids = %s -> %s", reply.FromAgentID, reply.ToAgentID)
	}

	records := []AgentMessage{}
	if err := st.ReadComms(func(raw json.RawMessage) {
		var record commRecord
		if json.Unmarshal(raw, &record) == nil {
			records = append(records, record.Message)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].FromAgentID != "sess_a" || records[1].ToAgentID != "sess_a" {
		t.Fatalf("records = %#v", records)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "comms.v1-name-addressed.ndjson")); err != nil {
		t.Fatalf("migration backup: %v", err)
	}
}

func TestRenderAgentProfileIsLongLivedDomainContext(t *testing.T) {
	text := renderAgentProfile("hub-dev", AgentProfile{
		AgentID: "sess_hub", Version: 3, Identity: "Hub maintainer", Domain: "Long-lived agents", Scope: "Owns hub",
	})
	for _, want := range []string{"<agent_profile", "Hub maintainer", "Long-lived agents", "across turns"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered profile missing %q: %s", want, text)
		}
	}
}
