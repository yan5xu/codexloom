package hub

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentCwdEffectiveUnchanged = "unchanged"
	agentCwdEffectiveNextTurn  = "next_thread_start_or_resume"
	agentCwdRuntimeUnchanged   = "unchanged"
	agentCwdRuntimeNotLoaded   = "not_loaded"
	agentCwdRuntimeColdResume  = "cold_resume_required"
	agentCwdSkillsRefreshed    = "refreshed"
	defaultAgentHomeRoot       = "codexloom"
	defaultAgentHomeAgentsDir  = "agents"
)

type AgentCwdUpdateParams struct {
	// An omitted or blank cwd selects the managed default Agent Home. A
	// non-blank cwd is always treated as a custom path and must already exist.
	Cwd string `json:"cwd"`
}

type AgentCwdUpdateReceipt struct {
	AgentID        string `json:"agentId"`
	AgentName      string `json:"agentName"`
	ThreadID       string `json:"threadId"`
	OldCwd         string `json:"oldCwd"`
	NewCwd         string `json:"newCwd"`
	EffectiveState string `json:"effectiveState"`
	RuntimeState   string `json:"runtimeState"`
	SkillsState    string `json:"skillsState"`
}

type AgentCwdUpdateResult struct {
	Agent     AgentView                `json:"agent"`
	Update    AgentCwdUpdateReceipt    `json:"update"`
	Inventory AgentSkillInventoryEntry `json:"inventory"`
}

func normalizeAgentCwd(value string) (string, error) {
	cwd := strings.TrimSpace(value)
	if cwd == "" {
		return "", errf(400, "cwd is required")
	}
	if !filepath.IsAbs(cwd) {
		return "", errf(400, "cwd must be an absolute directory")
	}
	cwd = filepath.Clean(cwd)
	info, err := os.Stat(cwd)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return "", errf(404, "cwd does not exist: %s", cwd)
		case errors.Is(err, os.ErrPermission):
			return "", errf(403, "cwd is not accessible: %s", cwd)
		default:
			return "", errf(400, "inspect cwd: %s", err)
		}
	}
	if !info.IsDir() {
		return "", errf(409, "cwd is not a directory: %s", cwd)
	}
	// Skills discovery needs both directory search and read access. The mode
	// check makes a wholly inaccessible fixture fail deterministically even
	// when tests run as root; os.Open/Readdirnames remains authoritative for
	// ACLs and the current process identity.
	if info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o444 == 0 {
		return "", errf(403, "cwd is not accessible: %s", cwd)
	}
	directory, err := os.Open(cwd)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", errf(403, "cwd is not accessible: %s", cwd)
		}
		return "", errf(400, "open cwd: %s", err)
	}
	defer directory.Close()
	if _, err := directory.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, os.ErrPermission) {
			return "", errf(403, "cwd is not readable: %s", cwd)
		}
		return "", errf(400, "read cwd: %s", err)
	}
	return cwd, nil
}

func defaultAgentHome(agentName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errf(500, "resolve user home for default Agent Home: %s", err)
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." || !filepath.IsAbs(home) {
		return "", errf(500, "resolve user home for default Agent Home: path is not absolute")
	}
	info, err := os.Stat(home)
	if err != nil {
		return "", errf(500, "inspect user home for default Agent Home: %s", err)
	}
	if !info.IsDir() {
		return "", errf(500, "resolve user home for default Agent Home: path is not a directory")
	}
	return filepath.Clean(filepath.Join(home, defaultAgentHomeRoot, defaultAgentHomeAgentsDir, agentName)), nil
}

// prepareDefaultAgentHome creates only the managed default path. The returned
// list is ordered leaf-first so a failed operation can remove only directories
// that this call created, and only while they remain empty.
func prepareDefaultAgentHome(agentName string) (string, []string, error) {
	cwd, err := defaultAgentHome(agentName)
	if err != nil {
		return "", nil, err
	}
	created := []string{}
	for current := cwd; ; current = filepath.Dir(current) {
		info, statErr := os.Stat(current)
		if statErr == nil {
			if !info.IsDir() {
				return "", nil, errf(409, "default Agent Home is not a directory: %s", current)
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			if errors.Is(statErr, os.ErrPermission) {
				return "", nil, errf(403, "default Agent Home is not accessible: %s", current)
			}
			return "", nil, errf(500, "inspect default Agent Home: %s", statErr)
		}
		created = append(created, current)
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, errf(500, "resolve default Agent Home parent: %s", cwd)
		}
	}
	if len(created) > 0 {
		if err := os.MkdirAll(cwd, 0o700); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return "", nil, defaultAgentHomePreparationError(errf(403, "create default Agent Home: %s", err), created)
			}
			return "", nil, defaultAgentHomePreparationError(errf(500, "create default Agent Home: %s", err), created)
		}
		// MkdirAll applies the requested mode to new directories subject to the
		// process umask. Make the leaf explicitly private without changing any
		// pre-existing parent.
		if err := os.Chmod(cwd, 0o700); err != nil {
			return "", nil, defaultAgentHomePreparationError(errf(500, "protect default Agent Home: %s", err), created)
		}
	}
	validated, err := normalizeAgentCwd(cwd)
	if err != nil {
		return "", nil, defaultAgentHomePreparationError(err, created)
	}
	return validated, created, nil
}

func rollbackCreatedAgentHome(created []string) error {
	for _, directory := range created {
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove newly-created directory %s: %w", directory, err)
		}
	}
	return nil
}

func defaultAgentHomePreparationError(cause error, created []string) error {
	if cleanupErr := rollbackCreatedAgentHome(created); cleanupErr != nil {
		return fmt.Errorf("%w; rollback default Agent Home: %v", cause, cleanupErr)
	}
	return cause
}

func (h *Hub) agentCwdUpdatePendingLocked(agentID string) bool {
	_, ok := h.agentCwdUpdates[agentID]
	return ok
}

func (h *Hub) validateAgentCwdUpdateStateLocked(agentID, oldCwd, updatedAt, threadID string, expectedRuntime *runtime) (*Agent, error) {
	if h.stopping {
		return nil, errf(503, "CodexLoom is shutting down")
	}
	if h.isDrainingLocked() {
		return nil, errf(409, "CodexLoom is draining for restart")
	}
	if h.providerSwitching {
		return nil, errf(409, "CodexLoom is switching an Agent Provider")
	}
	meta := h.agents[agentID]
	if meta == nil {
		return nil, errf(404, "agent vanished during cwd update")
	}
	if meta.Cwd != oldCwd || meta.UpdatedAt != updatedAt || meta.ThreadID != threadID {
		return nil, errf(409, "agent %q changed during cwd update; retry", meta.Name)
	}
	if meta.Status == "running" {
		return nil, errf(409, "agent %q is running; cwd changes apply between Turns", meta.Name)
	}
	currentRuntime := h.runtimes[agentID]
	if expectedRuntime != nil && currentRuntime != expectedRuntime {
		return nil, errf(409, "agent %q runtime changed during cwd update; retry", meta.Name)
	}
	if currentRuntime != nil {
		if currentRuntime.activeTurn != nil && !currentRuntime.activeTurn.finished {
			return nil, errf(409, "agent %q has an active Turn", meta.Name)
		}
		if len(currentRuntime.approvals) > 0 {
			return nil, errf(409, "agent %q has a pending approval", meta.Name)
		}
	}
	return meta, nil
}

func (h *Hub) UpdateAgentCwd(key string, params AgentCwdUpdateParams) (result AgentCwdUpdateResult, resultErr error) {
	requestedCwd := strings.TrimSpace(params.Cwd)
	useDefaultHome := requestedCwd == ""
	newCwd := ""
	var err error
	if !useDefaultHome {
		newCwd, err = normalizeAgentCwd(requestedCwd)
		if err != nil {
			return AgentCwdUpdateResult{}, err
		}
	}

	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(404, "agent not found: %s", key)
	}
	if meta.Source == "edge" {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(409, "edge Agent %q must be adopted before changing cwd", meta.Name)
	}
	if h.agentCwdUpdates == nil {
		h.agentCwdUpdates = map[string]struct{}{}
	}
	if h.agentCwdUpdatePendingLocked(meta.ID) {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(409, "agent %q already has a cwd update in progress", meta.Name)
	}
	if meta.Status == "running" {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(409, "agent %q is running; cwd changes apply between Turns", meta.Name)
	}
	rt := h.runtimes[meta.ID]
	if rt != nil && rt.activeTurn != nil && !rt.activeTurn.finished {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(409, "agent %q has an active Turn", meta.Name)
	}
	if rt != nil && len(rt.approvals) > 0 {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, errf(409, "agent %q has a pending approval", meta.Name)
	}
	agentID := meta.ID
	agentName := meta.Name
	oldCwd := meta.Cwd
	updatedAt := meta.UpdatedAt
	threadID := meta.ThreadID
	h.agentCwdUpdates[agentID] = struct{}{}
	h.mu.Unlock()

	runtimeLocked := false
	if rt != nil {
		rt.startMu.Lock()
		runtimeLocked = true
	}
	createdDefaultHome := []string{}
	keepDefaultHome := true
	defer func() {
		if !keepDefaultHome && len(createdDefaultHome) > 0 {
			if cleanupErr := rollbackCreatedAgentHome(createdDefaultHome); cleanupErr != nil {
				if resultErr == nil {
					resultErr = errf(500, "rollback default Agent Home: %s", cleanupErr)
				} else {
					resultErr = fmt.Errorf("%w; rollback default Agent Home: %v", resultErr, cleanupErr)
				}
			}
		}
		if runtimeLocked {
			rt.startMu.Unlock()
		}
		h.mu.Lock()
		delete(h.agentCwdUpdates, agentID)
		h.mu.Unlock()
	}()

	h.mu.Lock()
	meta, err = h.validateAgentCwdUpdateStateLocked(agentID, oldCwd, updatedAt, threadID, rt)
	if err != nil {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, err
	}
	h.mu.Unlock()

	if useDefaultHome {
		newCwd, createdDefaultHome, err = prepareDefaultAgentHome(agentName)
		if err != nil {
			return AgentCwdUpdateResult{}, err
		}
		keepDefaultHome = false
	}

	h.mu.Lock()
	meta, err = h.validateAgentCwdUpdateStateLocked(agentID, oldCwd, updatedAt, threadID, rt)
	if err != nil {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, err
	}
	cwds := h.agentSkillInventoryCwdsLocked(agentID, newCwd)
	h.mu.Unlock()

	host, err := h.ensureCodexHost()
	if err != nil {
		return AgentCwdUpdateResult{}, err
	}
	rollbackSkills := func(cause error) error {
		h.mu.Lock()
		previousCwds := h.agentSkillInventoryCwdsLocked("", "")
		h.mu.Unlock()
		previousInventory, rollbackErr := h.requestSkillInventoryForCwds(host, previousCwds)
		if rollbackErr != nil {
			return fmt.Errorf("%w; restore previous Agent Skills inventory: %v", cause, rollbackErr)
		}
		h.projectAgentSkillInventory(&previousInventory)
		return cause
	}
	inventory, err := h.requestSkillInventoryForCwds(host, cwds)
	if err != nil {
		return AgentCwdUpdateResult{}, rollbackSkills(errf(500, "refresh Agent Skills for cwd: %s", err))
	}
	validatedCwd, err := normalizeAgentCwd(newCwd)
	if err != nil {
		return AgentCwdUpdateResult{}, rollbackSkills(err)
	}
	if validatedCwd != newCwd {
		return AgentCwdUpdateResult{}, rollbackSkills(errf(409, "cwd changed while the update was in progress; retry"))
	}

	h.mu.Lock()
	meta, err = h.validateAgentCwdUpdateStateLocked(agentID, oldCwd, updatedAt, threadID, rt)
	if err != nil {
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, rollbackSkills(err)
	}
	receipt := AgentCwdUpdateReceipt{
		AgentID: agentID, AgentName: meta.Name, ThreadID: meta.ThreadID,
		OldCwd: oldCwd, NewCwd: newCwd, SkillsState: agentCwdSkillsRefreshed,
	}
	if oldCwd == newCwd {
		receipt.EffectiveState = agentCwdEffectiveUnchanged
		receipt.RuntimeState = agentCwdRuntimeUnchanged
		view := h.viewLocked(meta)
		h.mu.Unlock()
		h.projectAgentSkillInventory(&inventory)
		keepDefaultHome = true
		return AgentCwdUpdateResult{Agent: view, Update: receipt, Inventory: agentInventoryByID(inventory, agentID)}, nil
	}

	previous := *meta
	meta.Cwd = newCwd
	meta.UpdatedAt = now()
	if err := h.persistAgentsLocked(); err != nil {
		*meta = previous
		h.mu.Unlock()
		return AgentCwdUpdateResult{}, rollbackSkills(errf(500, "save Agent cwd: %s", err))
	}
	receipt.EffectiveState = agentCwdEffectiveNextTurn
	receipt.RuntimeState = agentCwdRuntimeNotLoaded
	if h.runtimes[agentID] != nil {
		delete(h.runtimes, agentID)
		receipt.RuntimeState = agentCwdRuntimeColdResume
	}
	h.emitLocked(agentID, "loom/agent-cwd-updated", receipt)
	view := h.viewLocked(meta)
	h.mu.Unlock()

	h.projectAgentSkillInventory(&inventory)
	keepDefaultHome = true
	return AgentCwdUpdateResult{Agent: view, Update: receipt, Inventory: agentInventoryByID(inventory, agentID)}, nil
}

func agentInventoryByID(inventory SkillInventory, agentID string) AgentSkillInventoryEntry {
	for _, entry := range inventory.Agents {
		if entry.AgentID == agentID {
			return entry
		}
	}
	return AgentSkillInventoryEntry{AgentID: agentID}
}
