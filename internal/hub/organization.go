package hub

import (
	"sort"
	"strings"
)

// OrganizationRelationship is a durable parent/child boundary in the Agent
// organization. It is separate from collaboration evidence and message flow.
type OrganizationRelationship struct {
	ID            string `json:"id"`
	ParentAgentID string `json:"parentAgentId"`
	ChildAgentID  string `json:"childAgentId"`
	Parent        string `json:"parent"`
	Child         string `json:"child"`
	Description   string `json:"description"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type OrganizationRelationshipParams struct {
	Parent      string `json:"parent"`
	Child       string `json:"child"`
	Description string `json:"description"`
}

func (h *Hub) ListOrganizationRelationships(agent string) ([]OrganizationRelationship, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID := ""
	if strings.TrimSpace(agent) != "" {
		var ok bool
		agentID, ok = h.resolveAgentIDLocked(strings.TrimSpace(agent))
		if !ok {
			return nil, errf(404, "agent not found: %s", agent)
		}
	}
	out := make([]OrganizationRelationship, 0, len(h.organizationLinks))
	for _, relationship := range h.organizationLinks {
		if agentID != "" && relationship.ParentAgentID != agentID && relationship.ChildAgentID != agentID {
			continue
		}
		copy := *relationship
		copy.Parent = h.agentNameLocked(copy.ParentAgentID, copy.Parent)
		copy.Child = h.agentNameLocked(copy.ChildAgentID, copy.Child)
		out = append(out, copy)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Parent != out[j].Parent {
			return out[i].Parent < out[j].Parent
		}
		if out[i].Child != out[j].Child {
			return out[i].Child < out[j].Child
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (h *Hub) CreateOrganizationRelationship(params OrganizationRelationshipParams) (OrganizationRelationship, error) {
	description := strings.TrimSpace(params.Description)
	if description == "" {
		return OrganizationRelationship{}, errf(400, "description is required")
	}
	if len(description) > 16_000 {
		return OrganizationRelationship{}, errf(400, "description must be at most 16000 bytes")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	parent := h.resolveLocked(strings.TrimSpace(params.Parent))
	child := h.resolveLocked(strings.TrimSpace(params.Child))
	if parent == nil {
		return OrganizationRelationship{}, errf(404, "parent agent not found: %s", params.Parent)
	}
	if child == nil {
		return OrganizationRelationship{}, errf(404, "child agent not found: %s", params.Child)
	}
	if parent.ID == child.ID {
		return OrganizationRelationship{}, errf(400, "organization endpoints must be different agents")
	}
	for _, existing := range h.organizationLinks {
		if existing.ChildAgentID == child.ID {
			return OrganizationRelationship{}, errf(409, "%s already reports to %s", child.Name, h.agentNameLocked(existing.ParentAgentID, existing.Parent))
		}
	}
	if organizationPathExists(h.organizationLinks, child.ID, parent.ID) {
		return OrganizationRelationship{}, errf(409, "organization relationship would create a cycle")
	}
	timestamp := now()
	relationship := &OrganizationRelationship{
		ID:            "org_" + strings.TrimPrefix(newRelationshipID(), "rel_"),
		ParentAgentID: parent.ID,
		ChildAgentID:  child.ID,
		Parent:        parent.Name,
		Child:         child.Name,
		Description:   description,
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
	}
	h.organizationLinks[relationship.ID] = relationship
	if err := h.st.SaveOrganizationLinks(h.organizationLinks); err != nil {
		delete(h.organizationLinks, relationship.ID)
		return OrganizationRelationship{}, errf(500, "save organization relationship: %s", err)
	}
	h.emitGlobalLocked("loom/organization-link-updated", map[string]any{"relationship": relationship})
	return *relationship, nil
}

func (h *Hub) UpdateOrganizationRelationship(id string, params OrganizationRelationshipParams) (OrganizationRelationship, error) {
	description := strings.TrimSpace(params.Description)
	if description == "" {
		return OrganizationRelationship{}, errf(400, "description is required")
	}
	if len(description) > 16_000 {
		return OrganizationRelationship{}, errf(400, "description must be at most 16000 bytes")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	relationship := h.organizationLinks[strings.TrimSpace(id)]
	if relationship == nil {
		return OrganizationRelationship{}, errf(404, "organization relationship not found: %s", id)
	}
	previousDescription, previousUpdatedAt := relationship.Description, relationship.UpdatedAt
	relationship.Description = description
	relationship.UpdatedAt = now()
	if err := h.st.SaveOrganizationLinks(h.organizationLinks); err != nil {
		relationship.Description, relationship.UpdatedAt = previousDescription, previousUpdatedAt
		return OrganizationRelationship{}, errf(500, "save organization relationship: %s", err)
	}
	copy := *relationship
	copy.Parent = h.agentNameLocked(copy.ParentAgentID, copy.Parent)
	copy.Child = h.agentNameLocked(copy.ChildAgentID, copy.Child)
	h.emitGlobalLocked("loom/organization-link-updated", map[string]any{"relationship": copy})
	return copy, nil
}

func (h *Hub) DeleteOrganizationRelationship(id string) (OrganizationRelationship, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	relationship := h.organizationLinks[strings.TrimSpace(id)]
	if relationship == nil {
		return OrganizationRelationship{}, errf(404, "organization relationship not found: %s", id)
	}
	copy := *relationship
	delete(h.organizationLinks, relationship.ID)
	if err := h.st.SaveOrganizationLinks(h.organizationLinks); err != nil {
		h.organizationLinks[relationship.ID] = relationship
		return OrganizationRelationship{}, errf(500, "save organization relationship: %s", err)
	}
	h.emitGlobalLocked("loom/organization-link-deleted", map[string]any{"relationship": copy})
	return copy, nil
}

func organizationPathExists(relationships map[string]*OrganizationRelationship, from, target string) bool {
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, relationship := range relationships {
			if relationship.ParentAgentID == current {
				queue = append(queue, relationship.ChildAgentID)
			}
		}
	}
	return false
}
