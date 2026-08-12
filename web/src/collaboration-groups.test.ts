import { describe, expect, it } from "vitest";
import { normalizeCollaborationGroup, projectCollaborationGroup, relationshipContractAriaLabel } from "./collaboration-groups";
import type { CollaborationGroup, TeamRelationship } from "./types";

const relationships: TeamRelationship[] = [
  { id: "rel-platform-edge", fromAgentId: "platform", toAgentId: "edge", from: "platform", to: "edge", description: "API handoff", createdAt: "now", updatedAt: "now" },
  { id: "rel-lead-edge", fromAgentId: "lead", toAgentId: "edge", from: "lead", to: "edge", description: "Not included", createdAt: "now", updatedAt: "now" },
];

describe("projectCollaborationGroup", () => {
  it("keeps explicit members without inferring all-to-all relationships", () => {
    const group: CollaborationGroup = {
      id: "cgrp-parall", name: "Parall Development", description: "Stable interfaces", status: "active",
      memberAgentIds: ["lead", "platform", "edge"], relationshipIds: ["rel-platform-edge"],
      version: 1, createdAt: "now", updatedAt: "now",
    };
    const result = projectCollaborationGroup(group, relationships);
    expect(result.memberAgentIDs).toEqual(["lead", "platform", "edge"]);
    expect(result.includedRelationships.map((relationship) => relationship.id)).toEqual(["rel-platform-edge"]);
    expect(result.isolatedMemberAgentIDs).toEqual(["lead"]);
    expect(result.includedRelationships).not.toContainEqual(expect.objectContaining({ id: "rel-lead-edge" }));
  });

  it("preserves unavailable relationship ids for archived audit views", () => {
    const group: CollaborationGroup = {
      id: "cgrp-history", name: "Archived", description: "Historical view", status: "archived",
      memberAgentIds: ["lead"], relationshipIds: ["rel-removed"], version: 2, createdAt: "then", updatedAt: "now",
    };
    const result = projectCollaborationGroup(group, relationships);
    expect(result.includedRelationships).toEqual([]);
    expect(result.missingRelationshipIDs).toEqual(["rel-removed"]);
    expect(result.isolatedMemberAgentIDs).toEqual(["lead"]);
  });

  it("projects a legacy member-only group with null relationship ids", () => {
    const result = projectCollaborationGroup({
      id: "cgrp-members", name: "Member-only", description: "No declared edges", status: "active",
      memberAgentIds: ["lead"], relationshipIds: null,
      version: 1, createdAt: "now", updatedAt: "now",
    }, relationships);

    expect(result.memberAgentIDs).toEqual(["lead"]);
    expect(result.includedRelationships).toEqual([]);
    expect(result.isolatedMemberAgentIDs).toEqual(["lead"]);
    expect(result.missingRelationshipIDs).toEqual([]);
  });
});

describe("normalizeCollaborationGroup", () => {
  it.each([
    ["legacy null", null],
    ["missing", undefined],
    ["normal empty array", []],
  ])("normalizes %s relationship ids without changing group meaning", (_, relationshipIds) => {
    const group = normalizeCollaborationGroup({
      id: "cgrp-members", name: "Member-only", description: "No declared edges", status: "active",
      memberAgentIds: ["lead"], relationshipIds,
      version: 1, createdAt: "now", updatedAt: "now",
    });

    expect(group.memberAgentIds).toEqual(["lead"]);
    expect(group.relationshipIds).toEqual([]);
  });

  it("also restores a missing legacy member collection to an empty array", () => {
    const group = normalizeCollaborationGroup({
      id: "cgrp-empty", name: "Empty", description: "Legacy empty group", status: "active",
      relationshipIds: [], version: 1, createdAt: "now", updatedAt: "now",
    });

    expect(group.memberAgentIds).toEqual([]);
    expect(group.relationshipIds).toEqual([]);
  });
});

describe("relationshipContractAriaLabel", () => {
  it("includes relationship type, endpoints and the complete contract", () => {
    expect(relationshipContractAriaLabel("collaboration", "research", "operator", "Keep evidence strength unchanged."))
      .toBe("collaboration: research to operator. Keep evidence strength unchanged.");
  });
});
