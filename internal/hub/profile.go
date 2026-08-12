package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/codex"
)

const schedulerAgentID = "system:scheduler"

// AgentProfile describes one long-lived agent's collaboration domain. It is
// deliberately independent from model settings, runtime state and task history.
type AgentProfile struct {
	AgentID   string `json:"agentId"`
	Identity  string `json:"identity,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ProfileParams struct {
	Identity        string `json:"identity"`
	Domain          string `json:"domain"`
	Scope           string `json:"scope"`
	ExpectedVersion *int   `json:"expectedVersion,omitempty"`
}

// TeamRelationship is a durable, directed collaboration edge. Description is
// intentionally free-form; the first version does not impose an org taxonomy.
type TeamRelationship struct {
	ID          string `json:"id"`
	FromAgentID string `json:"fromAgentId"`
	ToAgentID   string `json:"toAgentId"`
	From        string `json:"from"`
	To          string `json:"to"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type RelationshipParams struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Description string `json:"description"`
}

func (h *Hub) GetProfile(key string) (AgentProfile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentID, ok := h.resolveAgentIDLocked(strings.TrimSpace(key))
	if !ok {
		return AgentProfile{}, errf(404, "agent not found: %s", key)
	}
	if profile := h.profiles[agentID]; profile != nil {
		return *profile, nil
	}
	return AgentProfile{AgentID: agentID}, nil
}

func (h *Hub) UpdateProfile(key string, p ProfileParams) (AgentProfile, error) {
	identity := strings.TrimSpace(p.Identity)
	domain := strings.TrimSpace(p.Domain)
	scope := strings.TrimSpace(p.Scope)
	if len(identity) > 16_000 || len(domain) > 16_000 || len(scope) > 16_000 {
		return AgentProfile{}, errf(400, "each profile field must be at most 16000 bytes")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	agentID, ok := h.resolveAgentIDLocked(strings.TrimSpace(key))
	if !ok {
		return AgentProfile{}, errf(404, "agent not found: %s", key)
	}
	currentVersion := 0
	current := h.profiles[agentID]
	if current != nil {
		currentVersion = current.Version
	}
	if p.ExpectedVersion != nil && *p.ExpectedVersion != currentVersion {
		return AgentProfile{}, errf(409, "profile version changed: expected %d, current %d", *p.ExpectedVersion, currentVersion)
	}
	// An unconfigured agent has no Profile record. Saving an empty form must not
	// manufacture version 1 and schedule a meaningless developer message.
	if current == nil && identity == "" && domain == "" && scope == "" {
		return AgentProfile{AgentID: agentID}, nil
	}
	// Clearing a configured profile must remove the durable record instead of
	// preserving an empty, versioned orphan that still appears in Team data.
	if current != nil && identity == "" && domain == "" && scope == "" {
		delete(h.profiles, agentID)
		if err := h.st.SaveProfiles(h.profiles); err != nil {
			h.profiles[agentID] = current
			return AgentProfile{}, errf(500, "save profiles: %s", err)
		}
		cleared := &AgentProfile{AgentID: agentID}
		h.emitGlobalLocked("loom/profile-updated", map[string]any{"profile": cleared})
		return *cleared, nil
	}
	if current != nil && current.Identity == identity && current.Domain == domain && current.Scope == scope {
		return *current, nil
	}
	profile := &AgentProfile{
		AgentID: agentID, Identity: identity, Domain: domain, Scope: scope,
		Version: currentVersion + 1, UpdatedAt: now(),
	}
	previous := current
	h.profiles[agentID] = profile
	if err := h.st.SaveProfiles(h.profiles); err != nil {
		if previous == nil {
			delete(h.profiles, agentID)
		} else {
			h.profiles[agentID] = previous
		}
		return AgentProfile{}, errf(500, "save profile: %s", err)
	}
	h.emitGlobalLocked("loom/profile-updated", map[string]any{"profile": profile})
	return *profile, nil
}

func (h *Hub) ListRelationships(agent string) ([]TeamRelationship, error) {
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
	out := make([]TeamRelationship, 0, len(h.teamLinks))
	for _, rel := range h.teamLinks {
		if agentID != "" && rel.FromAgentID != agentID && rel.ToAgentID != agentID {
			continue
		}
		cp := *rel
		cp.From = h.agentNameLocked(cp.FromAgentID, cp.From)
		cp.To = h.agentNameLocked(cp.ToAgentID, cp.To)
		out = append(out, cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (h *Hub) CreateRelationship(p RelationshipParams) (TeamRelationship, error) {
	description := strings.TrimSpace(p.Description)
	if description == "" {
		return TeamRelationship{}, errf(400, "description is required")
	}
	if len(description) > 16_000 {
		return TeamRelationship{}, errf(400, "description must be at most 16000 bytes")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	from := h.resolveLocked(strings.TrimSpace(p.From))
	to := h.resolveLocked(strings.TrimSpace(p.To))
	if from == nil {
		return TeamRelationship{}, errf(404, "from agent not found: %s", p.From)
	}
	if to == nil {
		return TeamRelationship{}, errf(404, "to agent not found: %s", p.To)
	}
	if from.ID == to.ID {
		return TeamRelationship{}, errf(400, "relationship endpoints must be different agents")
	}
	for _, existing := range h.teamLinks {
		if existing.FromAgentID == from.ID && existing.ToAgentID == to.ID {
			return TeamRelationship{}, errf(409, "relationship already exists: %s", existing.ID)
		}
	}
	ts := now()
	rel := &TeamRelationship{
		ID: newRelationshipID(), FromAgentID: from.ID, ToAgentID: to.ID,
		From: from.Name, To: to.Name, Description: description, CreatedAt: ts, UpdatedAt: ts,
	}
	h.teamLinks[rel.ID] = rel
	if err := h.st.SaveTeamLinks(h.teamLinks); err != nil {
		delete(h.teamLinks, rel.ID)
		return TeamRelationship{}, errf(500, "save relationship: %s", err)
	}
	h.emitGlobalLocked("loom/team-link-updated", map[string]any{"relationship": rel})
	return *rel, nil
}

func (h *Hub) UpdateRelationship(id string, p RelationshipParams) (TeamRelationship, error) {
	description := strings.TrimSpace(p.Description)
	if description == "" {
		return TeamRelationship{}, errf(400, "description is required")
	}
	if len(description) > 16_000 {
		return TeamRelationship{}, errf(400, "description must be at most 16000 bytes")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	rel := h.teamLinks[strings.TrimSpace(id)]
	if rel == nil {
		return TeamRelationship{}, errf(404, "relationship not found: %s", id)
	}
	previousDescription, previousUpdatedAt := rel.Description, rel.UpdatedAt
	rel.Description = description
	rel.UpdatedAt = now()
	if err := h.st.SaveTeamLinks(h.teamLinks); err != nil {
		rel.Description, rel.UpdatedAt = previousDescription, previousUpdatedAt
		return TeamRelationship{}, errf(500, "save relationship: %s", err)
	}
	cp := *rel
	cp.From = h.agentNameLocked(cp.FromAgentID, cp.From)
	cp.To = h.agentNameLocked(cp.ToAgentID, cp.To)
	h.emitGlobalLocked("loom/team-link-updated", map[string]any{"relationship": cp})
	return cp, nil
}

func (h *Hub) DeleteRelationship(id string) (TeamRelationship, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rel := h.teamLinks[strings.TrimSpace(id)]
	if rel == nil {
		return TeamRelationship{}, errf(404, "relationship not found: %s", id)
	}
	for _, group := range h.collaborationGroups {
		if group.Status != CollaborationGroupStatusActive {
			continue
		}
		for _, relationshipID := range group.RelationshipIDs {
			if relationshipID == rel.ID {
				return TeamRelationship{}, errf(409, "relationship %s is included in active collaboration group %q (%s)", rel.ID, group.Name, group.ID)
			}
		}
	}
	cp := *rel
	delete(h.teamLinks, rel.ID)
	if err := h.st.SaveTeamLinks(h.teamLinks); err != nil {
		h.teamLinks[rel.ID] = rel
		return TeamRelationship{}, errf(500, "save relationship: %s", err)
	}
	h.emitGlobalLocked("loom/team-link-deleted", map[string]any{"relationship": cp})
	return cp, nil
}

func (h *Hub) resolveAgentIDLocked(key string) (string, bool) {
	if meta := h.resolveLocked(key); meta != nil {
		return meta.ID, true
	}
	if _, ok := h.profiles[key]; ok {
		return key, true
	}
	for _, rel := range h.teamLinks {
		if rel.FromAgentID == key || rel.ToAgentID == key {
			return key, true
		}
	}
	for _, relationship := range h.organizationLinks {
		if relationship.ParentAgentID == key || relationship.ChildAgentID == key {
			return key, true
		}
	}
	return "", false
}

func (h *Hub) agentNameLocked(agentID, fallback string) string {
	if agentID == schedulerAgentID {
		return schedulerIdentity
	}
	if agentID == triggerAgentID {
		return triggerIdentity
	}
	if meta := h.agents[agentID]; meta != nil {
		return meta.Name
	}
	if fallback != "" {
		return fallback
	}
	return agentID
}

func stableEdgeAgentID(threadID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return "edge_" + hex.EncodeToString(sum[:8])
}

func legacyAgentID(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return "legacy_" + hex.EncodeToString(sum[:8])
}

func newRelationshipID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("rel_%x", sha256.Sum256([]byte(now())))[:20]
	}
	return "rel_" + hex.EncodeToString(b)
}

// migrateCommAgentIDsLocked enriches the mutable communication index with
// stable identities and atomically compacts legacy snapshots. Codex rollouts
// are a separate source of truth and are never changed here.
func (h *Hub) migrateCommAgentIDsLocked() error {
	byName := map[string]string{}
	for _, agent := range h.agents {
		byName[agent.Name] = agent.ID
	}
	changed := false
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil {
			continue
		}
		normalizeAgentMessage(msg)
		if msg.From == schedulerIdentity && msg.FromAgentID == "" {
			msg.FromAgentID = schedulerAgentID
			changed = true
		}
		if msg.To == schedulerIdentity && msg.ToAgentID == "" {
			msg.ToAgentID = schedulerAgentID
			changed = true
		}
		if msg.ToAgentID == "" && msg.DeliveredAgentID != "" {
			if h.agents[msg.DeliveredAgentID] != nil || byName[msg.To] == "" {
				msg.ToAgentID = msg.DeliveredAgentID
				changed = true
			}
		}
		if msg.FromAgentID == "" && byName[msg.From] != "" {
			msg.FromAgentID = byName[msg.From]
			changed = true
		}
		if msg.ToAgentID == "" && byName[msg.To] != "" {
			msg.ToAgentID = byName[msg.To]
			changed = true
		}
	}
	// A delivered reply identifies both endpoints of its root message even if
	// one participant has since been renamed or removed.
	for pass := 0; pass < 2; pass++ {
		for _, id := range h.commOrder {
			reply := h.comms[id]
			if reply == nil || reply.ReplyTo == "" {
				continue
			}
			root := h.comms[reply.ReplyTo]
			if root == nil {
				continue
			}
			if reply.FromAgentID == "" && root.ToAgentID != "" {
				reply.FromAgentID = root.ToAgentID
				changed = true
			}
			if root.ToAgentID == "" && reply.FromAgentID != "" {
				root.ToAgentID = reply.FromAgentID
				changed = true
			}
			if reply.ToAgentID == "" && root.FromAgentID != "" {
				reply.ToAgentID = root.FromAgentID
				changed = true
			}
			if root.FromAgentID == "" && reply.ToAgentID != "" {
				root.FromAgentID = reply.ToAgentID
				changed = true
			}
		}
	}
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil {
			continue
		}
		if msg.FromAgentID == "" {
			msg.FromAgentID = legacyAgentID(msg.From)
			changed = true
		}
		if msg.ToAgentID == "" {
			msg.ToAgentID = legacyAgentID(msg.To)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if h.passive {
		return nil
	}
	records := make([]json.RawMessage, 0, len(h.commOrder))
	for _, id := range h.commOrder {
		msg := h.comms[id]
		if msg == nil {
			continue
		}
		raw, err := json.Marshal(commRecord{Message: *msg})
		if err != nil {
			return err
		}
		records = append(records, raw)
	}
	return h.st.ReplaceComms(records)
}

func (h *Hub) injectDeveloperContext(agentID string, rt *runtime, content string) error {
	h.mu.Lock()
	meta := h.agents[agentID]
	if meta == nil {
		h.mu.Unlock()
		return errf(404, "agent vanished")
	}
	threadID := meta.ThreadID
	h.mu.Unlock()
	_, err := rt.client.Request("thread/inject_items", map[string]any{
		"threadId": threadID,
		"items": []map[string]any{{
			"type": "message", "role": "developer",
			"content": []map[string]any{{"type": "input_text", "text": content}},
		}},
	}, h.effectiveDeveloperContextTimeout())
	if err != nil {
		if codex.IsRequestTimeout(err) {
			h.markThreadControlIndeterminate(rt, threadID, "thread/inject_items")
		}
		return errf(500, "inject Developer context: %s", err)
	}
	return nil
}

func (h *Hub) effectiveDeveloperContextTimeout() time.Duration {
	if h.developerContextTimeout > 0 {
		return h.developerContextTimeout
	}
	return 30 * time.Second
}
