package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
)

// AgentSkillConfig stores Agent-scoped exceptions to the shared Codex Skill
// catalog. V1 intentionally supports only disables: enabling removes the
// Agent exception and falls back to Codex's user-level policy.
type AgentSkillConfig struct {
	AgentID       string   `json:"agentId"`
	DisabledPaths []string `json:"disabledPaths"`
	UpdatedAt     string   `json:"updatedAt"`
}

type AgentSkillConfigParams struct {
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

type AgentSkillConfigView struct {
	AgentID         string   `json:"agentId"`
	AgentName       string   `json:"agentName"`
	DisabledPaths   []string `json:"disabledPaths"`
	Applied         bool     `json:"applied"`
	RestartRequired bool     `json:"restartRequired"`
}

type AgentSkillInventoryEntry struct {
	AgentID         string                `json:"agentId"`
	AgentName       string                `json:"agentName"`
	Cwd             string                `json:"cwd"`
	Skills          []SkillInventorySkill `json:"skills"`
	Errors          []SkillInventoryError `json:"errors"`
	DisabledPaths   []string              `json:"disabledPaths"`
	Applied         bool                  `json:"applied"`
	RestartRequired bool                  `json:"restartRequired"`
}

func normalizeAgentSkillPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errf(400, "Skill path is required")
	}
	if !filepath.IsAbs(path) {
		return "", errf(400, "Skill path must be absolute")
	}
	path = filepath.Clean(path)
	if filepath.Base(path) != "SKILL.md" {
		return "", errf(400, "Skill path must point to SKILL.md")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = filepath.Clean(resolved)
	}
	return path, nil
}

func normalizedDisabledSkillPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if normalized, err := normalizeAgentSkillPath(path); err == nil {
			path = normalized
		} else {
			path = filepath.Clean(path)
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func agentSkillConfigHash(paths []string) string {
	sum := sha256.Sum256([]byte(strings.Join(normalizedDisabledSkillPaths(paths), "\x00")))
	return hex.EncodeToString(sum[:])
}

func codexAgentSkillConfig(paths []string) map[string]any {
	paths = normalizedDisabledSkillPaths(paths)
	entries := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, map[string]any{
			"path":    path,
			"enabled": false,
		})
	}
	return map[string]any{
		"skills": map[string]any{
			"config": entries,
		},
	}
}

func (h *Hub) disabledSkillPathsLocked(agentID string) []string {
	if h.agentSkillConfigs == nil {
		return nil
	}
	config := h.agentSkillConfigs[agentID]
	if config == nil {
		return nil
	}
	return append([]string(nil), config.DisabledPaths...)
}

func (h *Hub) agentSkillConfigViewLocked(meta *Agent) AgentSkillConfigView {
	paths := normalizedDisabledSkillPaths(h.disabledSkillPathsLocked(meta.ID))
	desiredHash := agentSkillConfigHash(paths)
	rt := h.runtimes[meta.ID]
	applied := rt != nil && rt.skillConfigLoaded && rt.skillConfigHash == desiredHash
	restartRequired := rt != nil && rt.skillConfigLoaded && rt.skillConfigHash != desiredHash
	return AgentSkillConfigView{
		AgentID:         meta.ID,
		AgentName:       meta.Name,
		DisabledPaths:   paths,
		Applied:         applied,
		RestartRequired: restartRequired,
	}
}

func (h *Hub) GetAgentSkillConfig(key string) (AgentSkillConfigView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.resolveLocked(key)
	if meta == nil {
		return AgentSkillConfigView{}, errf(404, "agent not found: %s", key)
	}
	return h.agentSkillConfigViewLocked(meta), nil
}

func (h *Hub) UpdateAgentSkillConfig(key string, params AgentSkillConfigParams) (AgentSkillConfigView, error) {
	path, err := normalizeAgentSkillPath(params.Path)
	if err != nil {
		return AgentSkillConfigView{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	meta := h.resolveLocked(key)
	if meta == nil {
		return AgentSkillConfigView{}, errf(404, "agent not found: %s", key)
	}
	if meta.Status == "running" {
		return AgentSkillConfigView{}, errf(409, "agent %q is running; Skill config changes apply between Turns", meta.Name)
	}
	if meta.Source == "edge" {
		return AgentSkillConfigView{}, errf(409, "edge Agent %q must be adopted before configuring Skills", meta.Name)
	}
	if h.agentSkillConfigs == nil {
		h.agentSkillConfigs = map[string]*AgentSkillConfig{}
	}

	previous, hadPrevious := h.agentSkillConfigs[meta.ID]
	paths := h.disabledSkillPathsLocked(meta.ID)
	if params.Enabled {
		filtered := paths[:0]
		for _, existing := range paths {
			if filepath.Clean(existing) != path {
				filtered = append(filtered, existing)
			}
		}
		paths = filtered
	} else {
		paths = append(paths, path)
	}
	paths = normalizedDisabledSkillPaths(paths)
	if len(paths) == 0 {
		delete(h.agentSkillConfigs, meta.ID)
	} else {
		h.agentSkillConfigs[meta.ID] = &AgentSkillConfig{
			AgentID:       meta.ID,
			DisabledPaths: paths,
			UpdatedAt:     now(),
		}
	}
	if err := h.st.SaveAgentSkillConfigs(h.agentSkillConfigs); err != nil {
		if hadPrevious {
			h.agentSkillConfigs[meta.ID] = previous
		} else {
			delete(h.agentSkillConfigs, meta.ID)
		}
		return AgentSkillConfigView{}, errf(500, "save Agent Skill config: %s", err)
	}
	h.emitLocked(meta.ID, "loom/agent-skill-config-updated", map[string]any{
		"path": path, "enabled": params.Enabled,
	})
	return h.agentSkillConfigViewLocked(meta), nil
}

func skillPathMatches(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
}

func (h *Hub) projectAgentSkillInventory(inventory *SkillInventory) {
	if inventory == nil {
		return
	}
	byCwd := make(map[string]SkillInventoryEntry, len(inventory.Data))
	for _, entry := range inventory.Data {
		byCwd[filepath.Clean(entry.Cwd)] = entry
	}

	h.mu.Lock()
	agents := make([]Agent, 0, len(h.agents))
	configs := make(map[string]AgentSkillConfigView, len(h.agents))
	for _, meta := range h.agents {
		agents = append(agents, *meta)
		configs[meta.ID] = h.agentSkillConfigViewLocked(meta)
	}
	h.mu.Unlock()
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return agents[i].Name < agents[j].Name
		}
		return agents[i].ID < agents[j].ID
	})

	inventory.Agents = make([]AgentSkillInventoryEntry, 0, len(agents))
	for _, agent := range agents {
		raw := byCwd[filepath.Clean(agent.Cwd)]
		config := configs[agent.ID]
		skills := append([]SkillInventorySkill(nil), raw.Skills...)
		for i := range skills {
			for _, disabledPath := range config.DisabledPaths {
				if skillPathMatches(skills[i].Path, disabledPath) {
					skills[i].Enabled = false
					break
				}
			}
		}
		inventory.Agents = append(inventory.Agents, AgentSkillInventoryEntry{
			AgentID:         agent.ID,
			AgentName:       agent.Name,
			Cwd:             agent.Cwd,
			Skills:          skills,
			Errors:          append([]SkillInventoryError(nil), raw.Errors...),
			DisabledPaths:   config.DisabledPaths,
			Applied:         config.Applied,
			RestartRequired: config.RestartRequired,
		})
	}
}

func (h *Hub) AgentSkillInventory(key string) (AgentSkillInventoryEntry, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return AgentSkillInventoryEntry{}, errf(404, "agent not found: %s", key)
	}
	agentID := meta.ID
	h.mu.Unlock()

	inventory, err := h.ReloadSkills()
	if err != nil {
		return AgentSkillInventoryEntry{}, err
	}
	for _, entry := range inventory.Agents {
		if entry.AgentID == agentID {
			return entry, nil
		}
	}
	return AgentSkillInventoryEntry{}, errf(500, "Agent Skill inventory missing after reload")
}
