import type { CollaborationGroup, TeamRelationship } from "./types";

export type CollaborationGroupInput = Omit<CollaborationGroup, "memberAgentIds" | "relationshipIds"> & {
  memberAgentIds?: string[] | null;
  relationshipIds?: string[] | null;
};

export function normalizeCollaborationGroup(group: CollaborationGroupInput): CollaborationGroup {
  return {
    ...group,
    memberAgentIds: Array.isArray(group.memberAgentIds) ? group.memberAgentIds : [],
    relationshipIds: Array.isArray(group.relationshipIds) ? group.relationshipIds : [],
  };
}

export function relationshipContractAriaLabel(kind: "organization" | "collaboration", from: string, to: string, description: string) {
  return `${kind}: ${from} to ${to}. ${description}`;
}

export function projectCollaborationGroup(group: CollaborationGroupInput, relationships: TeamRelationship[]) {
  const normalizedGroup = normalizeCollaborationGroup(group);
  const byID = new Map(relationships.map((relationship) => [relationship.id, relationship]));
  const includedRelationships = normalizedGroup.relationshipIds.map((id) => byID.get(id)).filter(Boolean) as TeamRelationship[];
  const connectedAgentIDs = new Set(includedRelationships.flatMap((relationship) => [relationship.fromAgentId, relationship.toAgentId]));
  return {
    memberAgentIDs: [...normalizedGroup.memberAgentIds],
    includedRelationships,
    isolatedMemberAgentIDs: normalizedGroup.memberAgentIds.filter((id) => !connectedAgentIDs.has(id)),
    missingRelationshipIDs: normalizedGroup.relationshipIds.filter((id) => !byID.has(id)),
  };
}
