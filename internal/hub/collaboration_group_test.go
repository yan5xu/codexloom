package hub

import (
	"errors"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestCollaborationGroupLifecyclePreservesPairwiseRelationships(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*Agent{
		"lead":     {ID: "lead", Name: "parall-dev-lead", Cwd: t.TempDir(), ThreadID: "thread-lead", Status: "idle"},
		"platform": {ID: "platform", Name: "parall-platform-dev", Cwd: t.TempDir(), ThreadID: "thread-platform", Status: "idle"},
		"edge":     {ID: "edge", Name: "parall-edge-dev", Cwd: t.TempDir(), ThreadID: "thread-edge", Status: "idle"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()

	relationship, err := h.CreateRelationship(RelationshipParams{
		From: "parall-platform-dev", To: "parall-edge-dev", Description: "Stable API and artifact handoff.",
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "Parall Development", Description: "A readable view of the stable development interfaces.",
		MemberAgentIDs:  []string{"parall-dev-lead", "parall-platform-dev", "parall-edge-dev"},
		RelationshipIDs: []string{relationship.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if group.Status != CollaborationGroupStatusActive || group.Version != 1 {
		t.Fatalf("created group = %#v", group)
	}
	if len(group.MemberAgentIDs) != 3 || len(group.RelationshipIDs) != 1 {
		t.Fatalf("group did not preserve isolated member and explicit relationship: %#v", group)
	}

	second, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "Release Interfaces", Description: "The same declared edge can appear in another shared view.",
		MemberAgentIDs: []string{"platform", "edge"}, RelationshipIDs: []string{relationship.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.RelationshipIDs) != 1 || second.RelationshipIDs[0] != relationship.ID {
		t.Fatalf("second group = %#v", second)
	}

	if _, err := h.DeleteRelationship(relationship.ID); hubErrorStatus(err) != 409 {
		t.Fatalf("delete active grouped relationship error = %v, want 409", err)
	}
	archivedVersion := group.Version
	group.Status = CollaborationGroupStatusArchived
	archived, err := h.UpdateCollaborationGroup(group.ID, CollaborationGroupParams{
		Name: group.Name, Description: group.Description, Status: group.Status,
		MemberAgentIDs: group.MemberAgentIDs, RelationshipIDs: group.RelationshipIDs, ExpectedVersion: &archivedVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondVersion := second.Version
	second.Status = CollaborationGroupStatusArchived
	if _, err := h.UpdateCollaborationGroup(second.ID, CollaborationGroupParams{
		Name: second.Name, Description: second.Description, Status: second.Status,
		MemberAgentIDs: second.MemberAgentIDs, RelationshipIDs: second.RelationshipIDs, ExpectedVersion: &secondVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DeleteRelationship(relationship.ID); err != nil {
		t.Fatal(err)
	}

	h.Shutdown()
	reloaded := New(st)
	defer reloaded.Shutdown()
	persisted, err := reloaded.GetCollaborationGroup(archived.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != CollaborationGroupStatusArchived || len(persisted.RelationshipIDs) != 1 || persisted.RelationshipIDs[0] != relationship.ID {
		t.Fatalf("archived group lost historical relationship reference: %#v", persisted)
	}
	if len(reloaded.Team().CollaborationGroups) != 2 {
		t.Fatalf("Team view did not expose collaboration groups: %#v", reloaded.Team().CollaborationGroups)
	}
}

func TestCollaborationGroupValidationAndVersionGuard(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*Agent{
		"one": {ID: "one", Name: "one", Cwd: t.TempDir(), ThreadID: "thread-one", Status: "idle"},
		"two": {ID: "two", Name: "two", Cwd: t.TempDir(), ThreadID: "thread-two", Status: "idle"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	relationship, err := h.CreateRelationship(RelationshipParams{From: "one", To: "two", Description: "A stable interface."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateCollaborationGroup(CollaborationGroupParams{Name: "Empty", Description: "No members."}); hubErrorStatus(err) != 400 {
		t.Fatalf("empty group error = %v, want 400", err)
	}

	if _, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "Invalid", Description: "Missing one relationship endpoint.", MemberAgentIDs: []string{"one"}, RelationshipIDs: []string{relationship.ID},
	}); hubErrorStatus(err) != 400 {
		t.Fatalf("missing endpoint error = %v, want 400", err)
	}
	group, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "Shared", Description: "Valid group.", MemberAgentIDs: []string{"one", "two"}, RelationshipIDs: []string{relationship.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := group.Version - 1
	if _, err := h.UpdateCollaborationGroup(group.ID, CollaborationGroupParams{
		Name: group.Name, Description: "Stale update.", Status: group.Status,
		MemberAgentIDs: group.MemberAgentIDs, RelationshipIDs: group.RelationshipIDs, ExpectedVersion: &stale,
	}); hubErrorStatus(err) != 409 {
		t.Fatalf("stale update error = %v, want 409", err)
	}
	if _, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "shared", Description: "Duplicate active name.", MemberAgentIDs: []string{"one"},
	}); hubErrorStatus(err) != 409 {
		t.Fatalf("duplicate name error = %v, want 409", err)
	}
	if _, err := h.DeleteCollaborationGroup(group.ID, &stale); hubErrorStatus(err) != 409 {
		t.Fatalf("stale delete error = %v, want 409", err)
	}
	if _, err := h.DeleteCollaborationGroup(group.ID, &group.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetCollaborationGroup(group.ID); hubErrorStatus(err) != 404 {
		t.Fatalf("deleted group lookup error = %v, want 404", err)
	}
	if _, err := h.GetCollaborationGroup(relationship.ID); err == nil {
		t.Fatal("relationship ID unexpectedly resolved as a collaboration group")
	}
	if got, err := h.ListRelationships(""); err != nil || len(got) != 1 {
		t.Fatalf("deleting group changed pairwise relationships: got=%#v err=%v", got, err)
	}
}

func TestActiveCollaborationGroupProtectsAgentAndArchivedGroupPreservesMemberID(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgents(map[string]*Agent{
		"one": {ID: "one", Name: "one", Cwd: t.TempDir(), ThreadID: "thread-one", Status: "idle"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st)
	group, err := h.CreateCollaborationGroup(CollaborationGroupParams{
		Name: "Shared", Description: "An active group.", MemberAgentIDs: []string{"one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ArchiveAgent("one"); hubErrorStatus(err) != 409 {
		t.Fatalf("archive active group member error = %v, want 409", err)
	}
	version := group.Version
	group, err = h.UpdateCollaborationGroup(group.ID, CollaborationGroupParams{
		Name: group.Name, Description: group.Description, Status: CollaborationGroupStatusArchived,
		MemberAgentIDs: group.MemberAgentIDs, RelationshipIDs: group.RelationshipIDs, ExpectedVersion: &version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ArchiveAgent("one"); err != nil {
		t.Fatal(err)
	}
	h.Shutdown()

	reloaded := New(st)
	defer reloaded.Shutdown()
	persisted, err := reloaded.GetCollaborationGroup(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != CollaborationGroupStatusArchived || len(persisted.MemberAgentIDs) != 1 || persisted.MemberAgentIDs[0] != "one" {
		t.Fatalf("archived group lost historical member: %#v", persisted)
	}
}

func TestCollaborationGroupLoadResolvesOrganizationOnlyMember(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOrganizationLinks(map[string]*OrganizationRelationship{
		"org_one": {
			ID: "org_one", ParentAgentID: "lead", ChildAgentID: "specialist",
			Parent: "lead", Child: "specialist", Description: "Stable responsibility boundary.",
			CreatedAt: "2026-07-21T00:00:00Z", UpdatedAt: "2026-07-21T00:00:00Z",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCollaborationGroups(map[string]*CollaborationGroup{
		"cgrp_one": {
			ID: "cgrp_one", Name: "Organization members", Description: "Members resolved from declared organization.",
			Status: CollaborationGroupStatusActive, MemberAgentIDs: []string{"lead", "specialist"}, Version: 1,
			CreatedAt: "2026-07-21T00:00:00Z", UpdatedAt: "2026-07-21T00:00:00Z",
		},
	}); err != nil {
		t.Fatal(err)
	}

	ro, err := st.OpenReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	h, err := OpenWithOptions(ro, OpenOptions{Passive: true})
	if err != nil {
		t.Fatalf("open with organization-backed collaboration group: %v", err)
	}
	defer h.Shutdown()
	group, err := h.GetCollaborationGroup("cgrp_one")
	if err != nil || len(group.MemberAgentIDs) != 2 {
		t.Fatalf("loaded collaboration group = %#v, err = %v", group, err)
	}
}

func hubErrorStatus(err error) int {
	var hubError *HubError
	if errors.As(err, &hubError) {
		return hubError.Status
	}
	return 0
}
