package hub

import (
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestOrganizationRelationshipsEnforceTreeInvariantsAndPersist(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["lead"] = &Agent{ID: "lead", Name: "lead", Status: "idle"}
	h.agents["web"] = &Agent{ID: "web", Name: "web", Status: "idle"}
	h.agents["ops"] = &Agent{ID: "ops", Name: "ops", Status: "idle"}

	first, err := h.CreateOrganizationRelationship(OrganizationRelationshipParams{
		Parent: "lead", Child: "web", Description: "Web domain owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ParentAgentID != "lead" || first.ChildAgentID != "web" {
		t.Fatalf("relationship = %#v", first)
	}
	if _, err := h.CreateOrganizationRelationship(OrganizationRelationshipParams{
		Parent: "ops", Child: "web", Description: "Second parent",
	}); err == nil {
		t.Fatal("expected one-parent invariant")
	}
	if _, err := h.CreateOrganizationRelationship(OrganizationRelationshipParams{
		Parent: "web", Child: "lead", Description: "Cycle",
	}); err == nil {
		t.Fatal("expected cycle rejection")
	}

	loaded := map[string]*OrganizationRelationship{}
	if err := st.LoadOrganizationLinks(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded[first.ID] == nil || loaded[first.ID].Description != "Web domain owner" {
		t.Fatalf("loaded = %#v", loaded)
	}
	team := h.Team()
	if len(team.OrganizationLinks) != 1 || team.OrganizationLinks[0].ID != first.ID {
		t.Fatalf("team organization = %#v", team.OrganizationLinks)
	}
}

func TestOrganizationRelationshipUsesStableIDsAcrossRename(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["lead"] = &Agent{ID: "lead", Name: "lead", Status: "idle"}
	h.agents["child"] = &Agent{ID: "child", Name: "child", Status: "idle"}
	relationship, err := h.CreateOrganizationRelationship(OrganizationRelationshipParams{
		Parent: "lead", Child: "child", Description: "Own the web surface",
	})
	if err != nil {
		t.Fatal(err)
	}
	h.agents["child"].Name = "web"
	items, err := h.ListOrganizationRelationships("web")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != relationship.ID || items[0].Child != "web" {
		t.Fatalf("relationships = %#v", items)
	}
}
