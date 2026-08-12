package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const gatewayStateVersion = 1

type gatewayBindingLifecycle string

const (
	gatewayBindingProvisioning gatewayBindingLifecycle = "provisioning"
	gatewayBindingAdopted      gatewayBindingLifecycle = "adopted"
)

type gatewayRecoveryDisposition string

const (
	gatewayRecoveryNone      gatewayRecoveryDisposition = "none"
	gatewayRecoveryReconcile gatewayRecoveryDisposition = "needs_reconcile"
	gatewayRecoveryManual    gatewayRecoveryDisposition = "manual_recovery_required"
)

// gatewayConnectionBinding contains configured and authorized fields only.
// Provider liveness, cursors, errors, and observed capabilities are kept in a
// separate observation plane and cannot silently change this snapshot.
type gatewayConnectionBinding struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	AccountRef    string   `json:"accountRef,omitempty"`
	ScopeRef      string   `json:"scopeRef,omitempty"`
	Domain        string   `json:"domain,omitempty"`
	CredentialRef string   `json:"credentialRef,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Enabled       bool     `json:"enabled"`
	SupersededBy  string   `json:"supersededBy,omitempty"`
	ArchivedAt    string   `json:"archivedAt,omitempty"`
	CreatedAt     string   `json:"createdAt"`
}

type gatewayAddressBinding struct {
	ID                 string   `json:"id"`
	AgentID            string   `json:"agentId"`
	ConnectionID       string   `json:"connectionId"`
	ExternalIdentity   string   `json:"externalIdentity"`
	DisplayName        string   `json:"displayName,omitempty"`
	TriggerPolicy      string   `json:"triggerPolicy"`
	ReplyPolicy        string   `json:"replyPolicy"`
	DMPolicy           string   `json:"dmPolicy,omitempty"`
	TrustDomain        string   `json:"trustDomain"`
	AllowActors        []string `json:"allowActors,omitempty"`
	AllowConversations []string `json:"allowConversations,omitempty"`
	BlockActors        []string `json:"blockActors,omitempty"`
	BlockConversations []string `json:"blockConversations,omitempty"`
	Enabled            bool     `json:"enabled"`
	SupersededBy       string   `json:"supersededBy,omitempty"`
	ArchivedAt         string   `json:"archivedAt,omitempty"`
	DeletedAt          string   `json:"deletedAt,omitempty"`
	Version            int      `json:"version"`
	CreatedAt          string   `json:"createdAt"`
}

type gatewayConfiguredBinding struct {
	Connection gatewayConnectionBinding `json:"connection"`
	Addresses  []gatewayAddressBinding  `json:"addresses"`
}

type gatewayControl struct {
	ConnectionID    string                     `json:"connectionId"`
	Epoch           uint64                     `json:"epoch"`
	Lifecycle       gatewayBindingLifecycle    `json:"lifecycle"`
	Recovery        gatewayRecoveryDisposition `json:"recovery"`
	Reason          string                     `json:"reason,omitempty"`
	Binding         gatewayConfiguredBinding   `json:"binding"`
	ActiveAttemptID string                     `json:"activeAttemptId,omitempty"`
	UpdatedAt       string                     `json:"updatedAt"`
}

type gatewayProcessObservation struct {
	ConnectionID         string   `json:"connectionId"`
	Sequence             uint64   `json:"sequence"`
	Status               string   `json:"status"`
	Error                string   `json:"error,omitempty"`
	Cursor               string   `json:"cursor,omitempty"`
	LastEventAt          string   `json:"lastEventAt,omitempty"`
	ObservedCapabilities []string `json:"observedCapabilities,omitempty"`
	HeartbeatAt          string   `json:"heartbeatAt,omitempty"`
	ObservedAt           string   `json:"observedAt"`
}

type gatewayState struct {
	Version      int                                   `json:"version"`
	Controls     map[string]*gatewayControl            `json:"controls"`
	Observations map[string]*gatewayProcessObservation `json:"observations"`
	LaunchPlans  map[string]*gatewayLaunchPlan         `json:"launchPlans,omitempty"`
	Attempts     map[string]*gatewayTransitionAttempt  `json:"attempts,omitempty"`
}

// gatewayBindingSnapshot is a private authorization token for a future R1
// process effect. The open generation intentionally makes every pre-reopen
// snapshot stale even when an indeterminate write proved not to have landed.
type gatewayBindingSnapshot struct {
	OpenGeneration string                   `json:"-"`
	ControlEpoch   uint64                   `json:"-"`
	Binding        gatewayConfiguredBinding `json:"-"`
}

type gatewayHealthProjection struct {
	Status      string
	Error       string
	Recovery    gatewayRecoveryDisposition
	Lifecycle   gatewayBindingLifecycle
	Observation uint64
}

// gatewayFoundationIndeterminateError always leaves the current Hub poisoned.
// Committed only reports immediate authoritative readback; callers still must
// reopen/reconcile and must not authorize another mutation or effect.
type gatewayFoundationIndeterminateError struct {
	Cause       error
	ReadbackErr error
	Committed   bool
}

func (e *gatewayFoundationIndeterminateError) Error() string {
	message := "Gateway foundation commit is indeterminate"
	if e.Committed {
		message += " (authoritative readback contains the requested state)"
	} else if e.ReadbackErr != nil {
		message += ": readback failed: " + e.ReadbackErr.Error()
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *gatewayFoundationIndeterminateError) Unwrap() error { return e.Cause }

type gatewayConnectionCoordinator struct {
	mu         sync.Mutex
	locks      map[string]*sync.Mutex
	beforeLock func([]string)
}

func newGatewayConnectionCoordinator() *gatewayConnectionCoordinator {
	return &gatewayConnectionCoordinator{locks: map[string]*sync.Mutex{}}
}

func (h *Hub) gatewayCoordinatorForUse() *gatewayConnectionCoordinator {
	h.gatewayCoordinatorInitMu.Lock()
	defer h.gatewayCoordinatorInitMu.Unlock()
	if h.gatewayCoordinator == nil {
		h.gatewayCoordinator = newGatewayConnectionCoordinator()
	}
	return h.gatewayCoordinator
}

func (c *gatewayConnectionCoordinator) lock(connectionIDs ...string) func() {
	ids := normalizeGatewayConnectionIDs(connectionIDs)
	if c.beforeLock != nil {
		c.beforeLock(append([]string(nil), ids...))
	}
	c.mu.Lock()
	locks := make([]*sync.Mutex, 0, len(ids))
	for _, id := range ids {
		lock := c.locks[id]
		if lock == nil {
			lock = &sync.Mutex{}
			c.locks[id] = lock
		}
		locks = append(locks, lock)
	}
	c.mu.Unlock()
	for _, lock := range locks {
		lock.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}
}

func normalizeGatewayConnectionIDs(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func gatewayConnectionIDSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (h *Hub) gatewayConnectionIDsForAddressesLocked(addressIDs []string) []string {
	result := []string{}
	for _, addressID := range addressIDs {
		if address := h.addresses[strings.TrimSpace(addressID)]; address != nil {
			result = append(result, address.ConnectionID)
		}
	}
	return normalizeGatewayConnectionIDs(result)
}

// lockGatewayMutationScope closes the Address lookup-to-lock race. A transfer
// or consolidation may move an Address while the caller waits, so the sorted
// Connection set is recomputed until it is stable while held.
func (h *Hub) lockGatewayMutationScope(connectionIDs, addressIDs []string) func() {
	fixed := normalizeGatewayConnectionIDs(connectionIDs)
	for {
		h.mu.Lock()
		resolved := append(append([]string(nil), fixed...), h.gatewayConnectionIDsForAddressesLocked(addressIDs)...)
		h.mu.Unlock()
		resolved = normalizeGatewayConnectionIDs(resolved)
		unlock := h.gatewayCoordinatorForUse().lock(resolved...)
		h.mu.Lock()
		confirmed := append(append([]string(nil), fixed...), h.gatewayConnectionIDsForAddressesLocked(addressIDs)...)
		h.mu.Unlock()
		confirmed = normalizeGatewayConnectionIDs(confirmed)
		if gatewayConnectionIDSlicesEqual(resolved, confirmed) {
			return unlock
		}
		unlock()
	}
}

func emptyGatewayState() gatewayState {
	return gatewayState{Version: gatewayStateVersion, Controls: map[string]*gatewayControl{}, Observations: map[string]*gatewayProcessObservation{}}
}

func cloneGatewayState(value gatewayState) gatewayState {
	result := gatewayState{Version: value.Version, Controls: map[string]*gatewayControl{}, Observations: map[string]*gatewayProcessObservation{}}
	if result.Version == 0 {
		result.Version = gatewayStateVersion
	}
	for id, control := range value.Controls {
		if control == nil {
			continue
		}
		copy := *control
		copy.Binding = cloneGatewayBinding(control.Binding)
		result.Controls[id] = &copy
	}
	for id, observation := range value.Observations {
		if observation == nil {
			continue
		}
		copy := *observation
		copy.ObservedCapabilities = append([]string(nil), observation.ObservedCapabilities...)
		result.Observations[id] = &copy
	}
	if value.LaunchPlans != nil {
		result.LaunchPlans = make(map[string]*gatewayLaunchPlan, len(value.LaunchPlans))
		for id, plan := range value.LaunchPlans {
			if plan == nil {
				continue
			}
			copy := *plan
			result.LaunchPlans[id] = &copy
		}
	}
	if value.Attempts != nil {
		result.Attempts = make(map[string]*gatewayTransitionAttempt, len(value.Attempts))
		for id, attempt := range value.Attempts {
			if attempt == nil {
				continue
			}
			copy := *attempt
			copy.Binding = cloneGatewayBinding(attempt.Binding)
			if attempt.AcceptedProof != nil {
				proof := *attempt.AcceptedProof
				copy.AcceptedProof = &proof
			}
			result.Attempts[id] = &copy
		}
	}
	return result
}

func upgradeGatewayStateForProcess(value gatewayState) gatewayState {
	result := cloneGatewayState(value)
	if result.Version < gatewayProcessStateVersion {
		result.Version = gatewayProcessStateVersion
	}
	if result.LaunchPlans == nil {
		result.LaunchPlans = map[string]*gatewayLaunchPlan{}
	}
	if result.Attempts == nil {
		result.Attempts = map[string]*gatewayTransitionAttempt{}
	}
	return result
}

func upgradeGatewayStateForLaunchProof(value gatewayState) gatewayState {
	result := upgradeGatewayStateForProcess(value)
	result.Version = gatewayLaunchProofStateVersion
	return result
}

func cloneGatewayBinding(value gatewayConfiguredBinding) gatewayConfiguredBinding {
	result := value
	result.Connection.Capabilities = append([]string(nil), value.Connection.Capabilities...)
	result.Addresses = make([]gatewayAddressBinding, len(value.Addresses))
	for index, address := range value.Addresses {
		result.Addresses[index] = address
		result.Addresses[index].AllowActors = append([]string(nil), address.AllowActors...)
		result.Addresses[index].AllowConversations = append([]string(nil), address.AllowConversations...)
		result.Addresses[index].BlockActors = append([]string(nil), address.BlockActors...)
		result.Addresses[index].BlockConversations = append([]string(nil), address.BlockConversations...)
	}
	return result
}

func (h *Hub) loadGatewayState() error {
	state := emptyGatewayState()
	exists, err := h.loadRuntimeGatewayState(&state)
	if err != nil {
		return err
	}
	if !exists {
		h.gatewayState = state
		return nil
	}
	if err := h.validateGatewayStateLocked(state); err != nil {
		return err
	}
	h.gatewayState = cloneGatewayState(state)
	for connectionID := range h.gatewayState.Controls {
		h.applyGatewayHealthProjectionLocked(connectionID)
	}
	return nil
}

func (h *Hub) validateGatewayStateLocked(state gatewayState) error {
	if (state.Version != gatewayStateVersion && state.Version != gatewayProcessStateVersion && state.Version != gatewayLaunchProofStateVersion) || state.Controls == nil || state.Observations == nil {
		return fmt.Errorf("unsupported Gateway state version %d", state.Version)
	}
	if state.Version == gatewayStateVersion && (len(state.LaunchPlans) != 0 || len(state.Attempts) != 0) {
		return fmt.Errorf("R0b Gateway state contains R1 process records")
	}
	if state.Version == gatewayProcessStateVersion && len(state.LaunchPlans) == 0 {
		return fmt.Errorf("R1 Gateway process state has no explicit launch plan")
	}
	for id, control := range state.Controls {
		if control == nil || strings.TrimSpace(id) == "" || control.ConnectionID != id || control.Epoch == 0 ||
			!validGatewayLifecycle(control.Lifecycle) || !validGatewayRecovery(control.Recovery) {
			return fmt.Errorf("invalid Gateway control %q", id)
		}
		if h.connections[id] == nil {
			return fmt.Errorf("Gateway control references missing Connection %q", id)
		}
		if control.Binding.Connection.ID != id || !gatewayBindingCanonical(control.Binding) {
			return fmt.Errorf("invalid Gateway configured binding for %q", id)
		}
		current, err := h.gatewayBindingLocked(id)
		if err != nil {
			return err
		}
		if control.Recovery == gatewayRecoveryNone && !gatewayBindingsEqual(current, control.Binding) {
			return fmt.Errorf("Gateway binding drift requires reconciliation for %q", id)
		}
		attempt := state.Attempts[id]
		if control.ActiveAttemptID == "" {
			if attempt != nil && !gatewayAttemptTerminal(attempt.Phase) {
				return fmt.Errorf("Gateway control %q lost its active attempt", id)
			}
		} else if attempt == nil || attempt.ID != control.ActiveAttemptID || gatewayAttemptTerminal(attempt.Phase) || control.Recovery == gatewayRecoveryNone {
			return fmt.Errorf("Gateway control %q has invalid active attempt", id)
		}
	}
	for id, observation := range state.Observations {
		if observation == nil || observation.ConnectionID != id || state.Controls[id] == nil || observation.Sequence == 0 ||
			!validGatewayHealthStatus(observation.Status) || !gatewayStringSlicesEqual(observation.ObservedCapabilities, normalizeCapabilities(observation.ObservedCapabilities)) {
			return fmt.Errorf("invalid Gateway observation %q", id)
		}
	}
	for id, plan := range state.LaunchPlans {
		if plan == nil || plan.ConnectionID != id || state.Controls[id] == nil {
			return fmt.Errorf("invalid Gateway launch plan %q", id)
		}
		if err := validateGatewayLaunchPlan(*plan); err != nil {
			return fmt.Errorf("invalid Gateway launch plan %q: %w", id, err)
		}
		if plan.Target.Provider != "" {
			if state.Version != gatewayLaunchProofStateVersion {
				return fmt.Errorf("typed Lark Gateway launch plan %q requires L2a state", id)
			}
			control := state.Controls[id]
			if control.Recovery == gatewayRecoveryNone || control.ActiveAttemptID != "" {
				if err := validateGatewayLaunchPlanForBinding(*plan, control.Binding); err != nil {
					return fmt.Errorf("invalid Lark Gateway launch plan %q: %w", id, err)
				}
			}
		}
		if h.st == nil || filepath.Clean(plan.Target.DataDir) != filepath.Clean(h.st.Dir()) {
			return fmt.Errorf("Gateway launch plan %q targets another Runtime data directory", id)
		}
	}
	for id, attempt := range state.Attempts {
		if attempt == nil || attempt.ConnectionID != id || state.Controls[id] == nil || state.LaunchPlans[id] == nil {
			return fmt.Errorf("invalid Gateway transition attempt %q", id)
		}
		if err := validateGatewayTransitionAttempt(*attempt); err != nil {
			return fmt.Errorf("invalid Gateway transition attempt %q: %w", id, err)
		}
		control := state.Controls[id]
		if gatewayAttemptTerminal(attempt.Phase) {
			continue
		}
		if !gatewayLaunchPlansEqual(*state.LaunchPlans[id], attempt.Plan) {
			return fmt.Errorf("active Gateway attempt %q drifted from its launch plan", id)
		}
		if control.Epoch != attempt.BindingEpoch || !gatewayBindingsEqual(control.Binding, attempt.Binding) {
			return fmt.Errorf("active Gateway attempt %q drifted from its frozen control", id)
		}
		connection := h.connections[id]
		current, currentErr := h.gatewayBindingLocked(id)
		if control.Lifecycle != gatewayBindingAdopted || connection == nil || !connection.Enabled || connection.ArchivedAt != "" || connection.SupersededBy != "" ||
			currentErr != nil || !gatewayBindingsEqual(current, attempt.Binding) || !gatewayBindingEligible(current) {
			return fmt.Errorf("active Gateway attempt %q is no longer eligible", id)
		}
	}
	return nil
}

func (h *Hub) loadRuntimeGatewayState(state *gatewayState) (bool, error) {
	if h.loadGatewayStateForTest != nil {
		return h.loadGatewayStateForTest(state)
	}
	return h.st.LoadRuntimeGatewayState(state)
}

func (h *Hub) saveGatewayStateLocked(next gatewayState) error {
	var err error
	if h.saveGatewayStateForTest != nil {
		err = h.saveGatewayStateForTest(next)
	} else {
		err = h.st.SaveRuntimeGatewayState(next)
	}
	if err == nil {
		h.gatewayState = cloneGatewayState(next)
		return nil
	}
	committed, readbackErr := h.gatewayStateReadbackEquals(next)
	h.gatewayFoundationPoisoned = true
	h.gatewayFoundationPoisonReason = "Gateway foundation requires reopen/reconciliation"
	if committed {
		h.gatewayState = cloneGatewayState(next)
	}
	return &gatewayFoundationIndeterminateError{Cause: err, ReadbackErr: readbackErr, Committed: committed}
}

func (h *Hub) gatewayStateReadbackEquals(expected gatewayState) (bool, error) {
	var actual gatewayState
	exists, err := h.loadRuntimeGatewayState(&actual)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		return false, err
	}
	return bytes.Equal(expectedJSON, actualJSON), nil
}

func (h *Hub) requireGatewayFoundationHealthyLocked() error {
	if h.gatewayState.Version == 0 {
		h.gatewayState = emptyGatewayState()
	}
	if h.gatewayOpenGeneration == "" {
		h.gatewayOpenGeneration = newIntegrationID("gopen")
	}
	if !h.gatewayFoundationPoisoned {
		return nil
	}
	reason := strings.TrimSpace(h.gatewayFoundationPoisonReason)
	if reason == "" {
		reason = "Gateway foundation requires reopen/reconciliation"
	}
	return errf(503, "%s", reason)
}

func validGatewayLifecycle(value gatewayBindingLifecycle) bool {
	return value == gatewayBindingProvisioning || value == gatewayBindingAdopted
}

func validGatewayRecovery(value gatewayRecoveryDisposition) bool {
	return value == gatewayRecoveryNone || value == gatewayRecoveryReconcile || value == gatewayRecoveryManual
}

func advanceGatewayEpoch(current uint64) (uint64, error) {
	if current == ^uint64(0) {
		return 0, errf(409, "Gateway control epoch is exhausted")
	}
	return current + 1, nil
}

func validGatewayHealthStatus(value string) bool {
	return oneOf(value, "disconnected", "connecting", "connected", "degraded")
}

func gatewayBindingCanonical(binding gatewayConfiguredBinding) bool {
	if binding.Connection.ID == "" || binding.Connection.Provider == "" ||
		!gatewayStringSlicesEqual(binding.Connection.Capabilities, normalizeCapabilities(binding.Connection.Capabilities)) {
		return false
	}
	if _, err := normalizeConnectionDomain(binding.Connection.Provider, binding.Connection.Domain); err != nil {
		return false
	}
	previous := ""
	for _, address := range binding.Addresses {
		if address.ID == "" || address.AgentID == "" || address.ConnectionID != binding.Connection.ID || address.ExternalIdentity == "" || address.ID <= previous ||
			!gatewayStringSlicesEqual(address.AllowActors, normalizeIdentityList(address.AllowActors)) ||
			!gatewayStringSlicesEqual(address.AllowConversations, normalizeIdentityList(address.AllowConversations)) ||
			!gatewayStringSlicesEqual(address.BlockActors, normalizeIdentityList(address.BlockActors)) ||
			!gatewayStringSlicesEqual(address.BlockConversations, normalizeIdentityList(address.BlockConversations)) {
			return false
		}
		previous = address.ID
	}
	return true
}

func (h *Hub) gatewayBindingLocked(connectionID string) (gatewayConfiguredBinding, error) {
	connection := h.connections[strings.TrimSpace(connectionID)]
	if connection == nil {
		return gatewayConfiguredBinding{}, errf(404, "connection not found: %s", connectionID)
	}
	binding := gatewayConfiguredBinding{Connection: gatewayConnectionBinding{
		ID: connection.ID, Provider: connection.Provider, AccountRef: connection.AccountRef,
		ScopeRef: connection.ScopeRef, Domain: connection.Domain, CredentialRef: connection.CredentialRef,
		Capabilities: normalizeCapabilities(connection.Capabilities), Enabled: connection.Enabled,
		SupersededBy: connection.SupersededBy, ArchivedAt: connection.ArchivedAt, CreatedAt: connection.CreatedAt,
	}}
	for _, address := range h.addresses {
		if address == nil || address.ConnectionID != connection.ID {
			continue
		}
		binding.Addresses = append(binding.Addresses, gatewayAddressBinding{
			ID: address.ID, AgentID: address.AgentID, ConnectionID: address.ConnectionID,
			ExternalIdentity: address.ExternalIdentity, DisplayName: address.DisplayName,
			TriggerPolicy: address.TriggerPolicy, ReplyPolicy: address.ReplyPolicy, DMPolicy: address.DMPolicy,
			TrustDomain: address.TrustDomain, AllowActors: normalizeIdentityList(address.AllowActors),
			AllowConversations: normalizeIdentityList(address.AllowConversations), BlockActors: normalizeIdentityList(address.BlockActors),
			BlockConversations: normalizeIdentityList(address.BlockConversations), Enabled: address.Enabled,
			SupersededBy: address.SupersededBy, ArchivedAt: address.ArchivedAt, DeletedAt: address.DeletedAt,
			Version: address.Version, CreatedAt: address.CreatedAt,
		})
	}
	sort.Slice(binding.Addresses, func(i, j int) bool { return binding.Addresses[i].ID < binding.Addresses[j].ID })
	return binding, nil
}

func gatewayBindingsEqual(left, right gatewayConfiguredBinding) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func gatewayStringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (h *Hub) snapshotGatewayBinding(connectionID string) (gatewayBindingSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayBindingSnapshot{}, err
	}
	control := h.gatewayState.Controls[strings.TrimSpace(connectionID)]
	if control == nil || control.Lifecycle != gatewayBindingAdopted || control.Recovery != gatewayRecoveryNone {
		return gatewayBindingSnapshot{}, errf(409, "Gateway binding is not eligible")
	}
	binding, err := h.gatewayBindingLocked(connectionID)
	if err != nil {
		return gatewayBindingSnapshot{}, err
	}
	if !gatewayBindingsEqual(binding, control.Binding) {
		return gatewayBindingSnapshot{}, errf(409, "Gateway configured binding changed")
	}
	return gatewayBindingSnapshot{OpenGeneration: h.gatewayOpenGeneration, ControlEpoch: control.Epoch, Binding: binding}, nil
}

func (h *Hub) snapshotGatewayProvisioningBinding(connectionID string) (gatewayBindingSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayBindingSnapshot{}, err
	}
	control := h.gatewayState.Controls[strings.TrimSpace(connectionID)]
	if control == nil || control.Lifecycle != gatewayBindingProvisioning || control.Recovery != gatewayRecoveryNone {
		return gatewayBindingSnapshot{}, errf(409, "Gateway binding is not provisioning")
	}
	binding, err := h.gatewayBindingLocked(connectionID)
	if err != nil {
		return gatewayBindingSnapshot{}, err
	}
	if !gatewayBindingsEqual(binding, control.Binding) {
		return gatewayBindingSnapshot{}, errf(409, "Gateway configured binding changed")
	}
	return gatewayBindingSnapshot{OpenGeneration: h.gatewayOpenGeneration, ControlEpoch: control.Epoch, Binding: binding}, nil
}

func (h *Hub) matchGatewayBinding(snapshot gatewayBindingSnapshot) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	if snapshot.OpenGeneration == "" || snapshot.OpenGeneration != h.gatewayOpenGeneration {
		return errf(409, "Gateway binding snapshot belongs to another Hub generation")
	}
	connectionID := snapshot.Binding.Connection.ID
	control := h.gatewayState.Controls[connectionID]
	if control == nil || control.Epoch != snapshot.ControlEpoch || control.Lifecycle != gatewayBindingAdopted || control.Recovery != gatewayRecoveryNone {
		return errf(409, "Gateway control epoch changed")
	}
	current, err := h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(current, snapshot.Binding) || !gatewayBindingsEqual(current, control.Binding) {
		return errf(409, "Gateway configured binding changed")
	}
	return nil
}

// initializeGatewayControl is dormant: no current HTTP, provider, startup, or
// service path calls it. Future stages must deliberately opt a Connection in.
func (h *Hub) initializeGatewayControl(connectionID string, lifecycle gatewayBindingLifecycle, recovery gatewayRecoveryDisposition, reason string) (gatewayControl, error) {
	if !validGatewayLifecycle(lifecycle) || !validGatewayRecovery(recovery) {
		return gatewayControl{}, errf(400, "invalid Gateway control state")
	}
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayControl{}, err
	}
	connectionID = strings.TrimSpace(connectionID)
	if h.gatewayState.Controls[connectionID] != nil {
		return gatewayControl{}, errf(409, "Gateway control already exists")
	}
	binding, err := h.gatewayBindingLocked(connectionID)
	if err != nil {
		return gatewayControl{}, err
	}
	if lifecycle == gatewayBindingAdopted && !gatewayBindingEligible(binding) {
		return gatewayControl{}, errf(409, "Gateway binding must be complete before adoption")
	}
	control := &gatewayControl{ConnectionID: connectionID, Epoch: 1, Lifecycle: lifecycle, Recovery: recovery,
		Reason: strings.TrimSpace(reason), Binding: binding, UpdatedAt: now()}
	next := cloneGatewayState(h.gatewayState)
	next.Controls[connectionID] = control
	connection := h.connections[connectionID]
	status := connection.Status
	if !validGatewayHealthStatus(status) {
		status = "disconnected"
	}
	observedAt := now()
	next.Observations[connectionID] = &gatewayProcessObservation{ConnectionID: connectionID, Sequence: 1,
		Status: status, Error: connection.LastError, Cursor: connection.Cursor, LastEventAt: connection.LastEventAt,
		HeartbeatAt: connection.LastHeartbeatAt, ObservedAt: observedAt}
	if err := h.saveGatewayStateLocked(next); err != nil {
		return gatewayControl{}, fmt.Errorf("persist Gateway control: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return *control, nil
}

// createProvisioningGatewayConnection is the dormant, foundation-owned
// creation primitive for future managed vertical slices. It deliberately is
// not wired to the legacy/public CreateConnection path in R0b. The control and
// floor land before integrations.json, so a crash can leave an orphan that
// fails closed, never a durable enabled Connection without a provisioning
// control.
func (h *Hub) createProvisioningGatewayConnection(candidate PlatformConnection) (PlatformConnection, gatewayControl, error) {
	if strings.TrimSpace(candidate.Provider) == "" {
		return PlatformConnection{}, gatewayControl{}, errf(400, "provider is required")
	}
	if strings.TrimSpace(candidate.ID) == "" {
		candidate.ID = newIntegrationID("conn")
	}
	candidate.Provider = strings.ToLower(strings.TrimSpace(candidate.Provider))
	candidate.Capabilities = normalizeCapabilities(candidate.Capabilities)
	if candidate.Status == "" || !validGatewayHealthStatus(candidate.Status) {
		candidate.Status = "disconnected"
	}
	if candidate.CreatedAt == "" {
		candidate.CreatedAt = now()
	}
	if candidate.UpdatedAt == "" {
		candidate.UpdatedAt = candidate.CreatedAt
	}
	unlock := h.gatewayCoordinatorForUse().lock(candidate.ID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return PlatformConnection{}, gatewayControl{}, err
	}
	if h.connections[candidate.ID] != nil || h.gatewayState.Controls[candidate.ID] != nil {
		return PlatformConnection{}, gatewayControl{}, errf(409, "Gateway Connection already exists")
	}
	copy := clonePlatformConnectionValue(candidate)
	h.connections[candidate.ID] = &copy
	binding, err := h.gatewayBindingLocked(candidate.ID)
	if err != nil {
		delete(h.connections, candidate.ID)
		return PlatformConnection{}, gatewayControl{}, err
	}
	control := &gatewayControl{ConnectionID: candidate.ID, Epoch: 1, Lifecycle: gatewayBindingProvisioning,
		Recovery: gatewayRecoveryNone, Reason: "Gateway binding is provisioning", Binding: binding, UpdatedAt: now()}
	next := cloneGatewayState(h.gatewayState)
	next.Controls[candidate.ID] = control
	next.Observations[candidate.ID] = &gatewayProcessObservation{ConnectionID: candidate.ID, Sequence: 1,
		Status: candidate.Status, Error: candidate.LastError, Cursor: candidate.Cursor,
		LastEventAt: candidate.LastEventAt, HeartbeatAt: candidate.LastHeartbeatAt, ObservedAt: now()}
	if err := h.saveGatewayStateLocked(next); err != nil {
		delete(h.connections, candidate.ID)
		return PlatformConnection{}, gatewayControl{}, fmt.Errorf("persist provisioning Gateway control: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(candidate.ID)
	if err := h.persistIntegrationsLocked(); err != nil {
		h.gatewayFoundationPoisoned = true
		h.gatewayFoundationPoisonReason = "provisioning Connection persistence requires reopen/reconciliation"
		return PlatformConnection{}, gatewayControl{}, errf(500, "persist provisioning Connection: %s", err)
	}
	h.emitGlobalLocked("loom/integration-connection", map[string]any{"connection": *h.connections[candidate.ID]})
	return clonePlatformConnectionValue(*h.connections[candidate.ID]), *control, nil
}

// adoptGatewayBinding is the only R0b provisioning exit. It proves only the
// configured snapshot and does not clear recovery or accept a process proof.
func (h *Hub) adoptGatewayBinding(snapshot gatewayBindingSnapshot) (gatewayControl, error) {
	connectionID := snapshot.Binding.Connection.ID
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayControl{}, err
	}
	if snapshot.OpenGeneration != h.gatewayOpenGeneration {
		return gatewayControl{}, errf(409, "Gateway binding snapshot belongs to another Hub generation")
	}
	control := h.gatewayState.Controls[connectionID]
	if control == nil || control.Epoch != snapshot.ControlEpoch || control.Lifecycle != gatewayBindingProvisioning || control.Recovery != gatewayRecoveryNone {
		return gatewayControl{}, errf(409, "Gateway provisioning control changed")
	}
	current, err := h.gatewayBindingLocked(connectionID)
	if err != nil || !gatewayBindingsEqual(current, snapshot.Binding) || !gatewayBindingEligible(current) {
		return gatewayControl{}, errf(409, "Gateway binding is incomplete or changed")
	}
	next := cloneGatewayState(h.gatewayState)
	nextControl := next.Controls[connectionID]
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return gatewayControl{}, err
	}
	nextControl.Epoch = nextEpoch
	nextControl.Lifecycle = gatewayBindingAdopted
	nextControl.Binding = current
	nextControl.Reason = ""
	nextControl.UpdatedAt = now()
	if err := h.saveGatewayStateLocked(next); err != nil {
		return gatewayControl{}, fmt.Errorf("persist Gateway binding adoption: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return *nextControl, nil
}

// setGatewayRecovery may only preserve or escalate recovery. R0b deliberately
// has no operation that clears needs_reconcile or manual_recovery_required.
func (h *Hub) setGatewayRecovery(connectionID string, expectedEpoch uint64, recovery gatewayRecoveryDisposition, reason string) (gatewayControl, error) {
	if recovery != gatewayRecoveryReconcile && recovery != gatewayRecoveryManual {
		return gatewayControl{}, errf(409, "R0b cannot clear Gateway recovery without an accepted process proof")
	}
	unlock := h.gatewayCoordinatorForUse().lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return gatewayControl{}, err
	}
	control := h.gatewayState.Controls[strings.TrimSpace(connectionID)]
	if control == nil {
		return gatewayControl{}, errf(404, "Gateway control not found")
	}
	if control.Epoch != expectedEpoch {
		return gatewayControl{}, errf(409, "Gateway control epoch changed")
	}
	if control.ActiveAttemptID != "" {
		return gatewayControl{}, errf(409, "Gateway process attempt is active")
	}
	if control.Recovery == gatewayRecoveryManual && recovery != gatewayRecoveryManual {
		return gatewayControl{}, errf(409, "manual Gateway recovery requires an accepted process proof")
	}
	next := cloneGatewayState(h.gatewayState)
	nextControl := next.Controls[connectionID]
	nextEpoch, err := advanceGatewayEpoch(nextControl.Epoch)
	if err != nil {
		return gatewayControl{}, err
	}
	nextControl.Epoch = nextEpoch
	nextControl.Recovery = recovery
	nextControl.Reason = strings.TrimSpace(reason)
	nextControl.UpdatedAt = now()
	if err := h.saveGatewayStateLocked(next); err != nil {
		return gatewayControl{}, fmt.Errorf("persist Gateway recovery disposition: %w", err)
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return *nextControl, nil
}

func (h *Hub) gatewayEligibleConnectionIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gatewayFoundationPoisoned {
		return nil
	}
	result := []string{}
	for id, control := range h.gatewayState.Controls {
		connection := h.connections[id]
		if connection == nil || control.Lifecycle != gatewayBindingAdopted || control.Recovery != gatewayRecoveryNone ||
			!connection.Enabled || connection.ArchivedAt != "" || connection.SupersededBy != "" {
			continue
		}
		binding, err := h.gatewayBindingLocked(id)
		if err == nil && gatewayBindingsEqual(binding, control.Binding) && gatewayBindingEligible(binding) {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func gatewayBindingEligible(binding gatewayConfiguredBinding) bool {
	for _, address := range binding.Addresses {
		if address.Enabled && address.ArchivedAt == "" && address.DeletedAt == "" && address.ConnectionID == binding.Connection.ID {
			return true
		}
	}
	return false
}

func (h *Hub) reduceGatewayHealthLocked(connection PlatformConnection) gatewayHealthProjection {
	result := gatewayHealthProjection{Status: connection.Status, Error: connection.LastError}
	control := h.gatewayState.Controls[connection.ID]
	if h.gatewayFoundationPoisoned {
		result.Status = "degraded"
		result.Error = "Gateway foundation requires reopen/reconciliation"
		return result
	}
	if control == nil {
		return result
	}
	result.Recovery, result.Lifecycle = control.Recovery, control.Lifecycle
	if observation := h.gatewayState.Observations[connection.ID]; observation != nil {
		result.Observation = observation.Sequence
	}
	// Recovery always dominates lifecycle and provider observation.
	if control.Recovery == gatewayRecoveryManual {
		result.Status, result.Error = "degraded", control.Reason
		if result.Error == "" {
			result.Error = "manual Gateway recovery required"
		}
		return result
	}
	if control.Recovery == gatewayRecoveryReconcile {
		result.Status, result.Error = "degraded", control.Reason
		if result.Error == "" {
			result.Error = "Gateway reconciliation required"
		}
		return result
	}
	// Configured lifecycle dominates a late connected observation.
	if connection.ArchivedAt != "" || connection.SupersededBy != "" {
		result.Status, result.Error = "disconnected", "connection is archived or superseded"
		return result
	}
	if !connection.Enabled {
		result.Status, result.Error = "disconnected", "connection is disabled"
		return result
	}
	if control.Lifecycle == gatewayBindingProvisioning {
		result.Status, result.Error = "connecting", "Gateway binding is provisioning"
		return result
	}
	if observation := h.gatewayState.Observations[connection.ID]; observation != nil {
		result.Status, result.Error = observation.Status, observation.Error
	}
	return result
}

func (h *Hub) applyGatewayHealthProjectionLocked(connectionID string) bool {
	connection := h.connections[connectionID]
	if connection == nil {
		return false
	}
	projection := h.reduceGatewayHealthLocked(*connection)
	observation := h.gatewayState.Observations[connectionID]
	changed := connection.Status != projection.Status || connection.LastError != projection.Error
	connection.Status, connection.LastError = projection.Status, projection.Error
	if observation != nil {
		if observation.HeartbeatAt != "" {
			connection.LastHeartbeatAt = observation.HeartbeatAt
		}
		connection.Cursor = observation.Cursor
		connection.LastEventAt = observation.LastEventAt
		if observation.ObservedAt > connection.UpdatedAt {
			connection.UpdatedAt = observation.ObservedAt
		}
	}
	return changed
}

// recordGatewayObservationLocked returns handled=false for legacy Connections;
// their persisted behavior remains unchanged until a future stage explicitly
// opts them into the foundation. Configured Capabilities are never overwritten
// for controlled Connections.
func (h *Hub) recordGatewayObservationLocked(connectionID, status, detail, observedAt, heartbeatAt, cursor, lastEventAt string, capabilities []string) (bool, error) {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return h.gatewayState.Controls[connectionID] != nil, err
	}
	if h.gatewayState.Controls[connectionID] == nil {
		return false, nil
	}
	if !validGatewayHealthStatus(status) {
		return true, errf(400, "invalid connection status %q", status)
	}
	next := cloneGatewayState(h.gatewayState)
	sequence := uint64(1)
	observedCapabilities := []string(nil)
	if previous := next.Observations[connectionID]; previous != nil {
		sequence = previous.Sequence + 1
		observedCapabilities = append(observedCapabilities, previous.ObservedCapabilities...)
		if cursor == "" {
			cursor = previous.Cursor
		}
		if lastEventAt == "" {
			lastEventAt = previous.LastEventAt
		}
		if heartbeatAt == "" {
			heartbeatAt = previous.HeartbeatAt
		}
	}
	if capabilities != nil {
		observedCapabilities = normalizeCapabilities(capabilities)
	}
	next.Observations[connectionID] = &gatewayProcessObservation{ConnectionID: connectionID, Sequence: sequence,
		Status: status, Error: strings.TrimSpace(detail), Cursor: cursor, LastEventAt: lastEventAt,
		ObservedCapabilities: observedCapabilities, HeartbeatAt: heartbeatAt, ObservedAt: observedAt}
	if err := h.saveGatewayStateLocked(next); err != nil {
		return true, err
	}
	h.applyGatewayHealthProjectionLocked(connectionID)
	return true, nil
}

func (h *Hub) rawGatewayObservationLocked(connectionID, fallbackStatus, fallbackError string) (string, string) {
	if observation := h.gatewayState.Observations[connectionID]; observation != nil {
		return observation.Status, observation.Error
	}
	if !validGatewayHealthStatus(fallbackStatus) {
		fallbackStatus = "disconnected"
	}
	return fallbackStatus, fallbackError
}

type gatewayMutationTicket struct {
	preparedEpochs map[string]uint64
}

func (h *Hub) prepareGatewayMutationLocked(connectionIDs ...string) (*gatewayMutationTicket, error) {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return nil, err
	}
	ticket := &gatewayMutationTicket{preparedEpochs: map[string]uint64{}}
	next := cloneGatewayState(h.gatewayState)
	for _, id := range normalizeGatewayConnectionIDs(connectionIDs) {
		control := next.Controls[id]
		if control == nil {
			continue
		}
		if control.Recovery != gatewayRecoveryNone {
			return nil, errf(409, "Gateway control for %s requires recovery", id)
		}
		nextEpoch, err := advanceGatewayEpoch(control.Epoch)
		if err != nil {
			return nil, err
		}
		control.Epoch = nextEpoch
		control.Recovery = gatewayRecoveryReconcile
		control.Reason = "configured binding mutation requires reconciliation"
		control.UpdatedAt = now()
		ticket.preparedEpochs[id] = control.Epoch
	}
	if len(ticket.preparedEpochs) == 0 {
		return ticket, nil
	}
	if err := h.saveGatewayStateLocked(next); err != nil {
		return nil, fmt.Errorf("persist Gateway mutation fence: %w", err)
	}
	return ticket, nil
}

func (h *Hub) finishGatewayMutationLocked(ticket *gatewayMutationTicket) error {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	if ticket == nil || len(ticket.preparedEpochs) == 0 {
		return nil
	}
	next := cloneGatewayState(h.gatewayState)
	for id, epoch := range ticket.preparedEpochs {
		control := next.Controls[id]
		if control == nil || control.Epoch != epoch || control.Recovery != gatewayRecoveryReconcile {
			return errf(409, "Gateway mutation fence changed for %s", id)
		}
		binding, err := h.gatewayBindingLocked(id)
		if err != nil {
			if control.Lifecycle != gatewayBindingProvisioning {
				return err
			}
			delete(next.Controls, id)
			delete(next.Observations, id)
			continue
		}
		control.Binding = binding
		if next.LaunchPlans[id] != nil {
			control.Recovery = gatewayRecoveryReconcile
			control.Reason = "configured binding changed; typed Gateway launch plan requires exact process reconciliation"
		} else {
			control.Recovery = gatewayRecoveryNone
			control.Reason = ""
		}
		control.UpdatedAt = now()
	}
	if err := h.saveGatewayStateLocked(next); err != nil {
		return fmt.Errorf("persist Gateway mutation completion: %w", err)
	}
	for id := range ticket.preparedEpochs {
		h.applyGatewayHealthProjectionLocked(id)
	}
	return nil
}
