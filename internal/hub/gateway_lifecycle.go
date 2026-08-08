package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/yan5xu/codex-loom/internal/store"
)

const gatewayLifecycleStateVersion = 1

type GatewayDisposition string

const (
	GatewayDispositionProvisioning   GatewayDisposition = "provisioning"
	GatewayDispositionStable         GatewayDisposition = "stable"
	GatewayDispositionManualRecovery GatewayDisposition = "manual_recovery_required"
)

type GatewayAddressIdentity struct {
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
}

type GatewayConnectionIdentity struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	AccountRef    string   `json:"accountRef,omitempty"`
	ScopeRef      string   `json:"scopeRef,omitempty"`
	CredentialRef string   `json:"credentialRef,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Enabled       bool     `json:"enabled"`
	SupersededBy  string   `json:"supersededBy,omitempty"`
	ArchivedAt    string   `json:"archivedAt,omitempty"`
}

type GatewayBindingIdentity struct {
	Connection GatewayConnectionIdentity `json:"connection"`
	Addresses  []GatewayAddressIdentity  `json:"addresses"`
}

type GatewayBindingSnapshot struct {
	ControlEpoch uint64                 `json:"controlEpoch"`
	Binding      GatewayBindingIdentity `json:"binding"`
}

// GatewayControl is private Runtime state. ActiveAttemptID and AnchorRef are
// reserved for R1; R0 never performs a process effect.
type GatewayControl struct {
	ConnectionID    string                 `json:"connectionId"`
	Epoch           uint64                 `json:"epoch"`
	Disposition     GatewayDisposition     `json:"disposition"`
	Reason          string                 `json:"reason,omitempty"`
	ActiveAttemptID string                 `json:"activeAttemptId,omitempty"`
	AnchorRef       string                 `json:"anchorRef,omitempty"`
	NeedsReconcile  bool                   `json:"needsReconcile,omitempty"`
	Binding         GatewayBindingIdentity `json:"binding"`
	UpdatedAt       string                 `json:"updatedAt"`
}

type GatewayProcessObservation struct {
	ConnectionID string   `json:"connectionId"`
	Sequence     uint64   `json:"sequence"`
	Status       string   `json:"status"`
	Error        string   `json:"error,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	ObservedAt   string   `json:"observedAt"`
}

type GatewayControlUpdate struct {
	Disposition     GatewayDisposition
	Reason          string
	ActiveAttemptID *string
	AnchorRef       *string
	NeedsReconcile  *bool
}

type GatewayHealthStatus struct {
	ConnectionID   string
	Status         string
	Error          string
	Disposition    GatewayDisposition
	NeedsReconcile bool
	ObservationSeq uint64
}

type gatewayLifecycleState struct {
	Version      int                                   `json:"version"`
	Controls     map[string]*GatewayControl            `json:"controls"`
	Observations map[string]*GatewayProcessObservation `json:"observations"`
}

type gatewayFoundationDocument struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	MinimumWriter    int                   `json:"minimumWriter"`
	GatewayLifecycle gatewayLifecycleState `json:"gatewayLifecycle"`
}

// GatewayFoundationCommitIndeterminateError means the persistence call failed
// after entering an atomic-write boundary. Committed reports only whether an
// immediate authoritative readback proved the exact requested document; the
// caller still receives an error and the current Hub remains poisoned until a
// fresh open/reconciliation establishes one authoritative generation.
type GatewayFoundationCommitIndeterminateError struct {
	Cause       error
	ReadbackErr error
	Committed   bool
}

func (e *GatewayFoundationCommitIndeterminateError) Error() string {
	result := "gateway lifecycle commit is indeterminate"
	if e.Committed {
		result += " (authoritative readback contains the requested state)"
	} else if e.ReadbackErr != nil {
		result += ": readback failed: " + e.ReadbackErr.Error()
	}
	if e.Cause != nil {
		result += ": " + e.Cause.Error()
	}
	return result
}

func (e *GatewayFoundationCommitIndeterminateError) Unwrap() error { return e.Cause }

type gatewayMutationTicket struct {
	preparedEpochs map[string]uint64
}

func (t *gatewayMutationTicket) active() bool {
	return t != nil && len(t.preparedEpochs) > 0
}

type gatewayConnectionCoordinator struct {
	mu         sync.Mutex
	locks      map[string]*sync.Mutex
	beforeLock func([]string)
}

func newGatewayConnectionCoordinator() *gatewayConnectionCoordinator {
	return &gatewayConnectionCoordinator{locks: map[string]*sync.Mutex{}}
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

func (h *Hub) gatewayConnectionIDsForAddressesLocked(addressIDs []string) []string {
	result := []string{}
	for _, addressID := range addressIDs {
		if address := h.addresses[strings.TrimSpace(addressID)]; address != nil {
			result = append(result, address.ConnectionID)
		}
	}
	return normalizeGatewayConnectionIDs(result)
}

// lockGatewayMutationScope closes the lookup-to-lock race for Address-owned
// mutations. A concurrent transfer may change the Connection set while this
// caller waits, so the scope is recomputed after locking and retried until the
// exact sorted set is stable.
func (h *Hub) lockGatewayMutationScope(connectionIDs, addressIDs []string) func() {
	fixed := normalizeGatewayConnectionIDs(connectionIDs)
	for {
		h.mu.Lock()
		resolved := append(append([]string(nil), fixed...), h.gatewayConnectionIDsForAddressesLocked(addressIDs)...)
		h.mu.Unlock()
		resolved = normalizeGatewayConnectionIDs(resolved)
		unlock := h.gatewayCoordinator.lock(resolved...)
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

func (h *Hub) requireGatewayControlsAbsentLocked(connectionIDs []string) error {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	for _, connectionID := range normalizeGatewayConnectionIDs(connectionIDs) {
		if h.gatewayLifecycle.Controls[connectionID] != nil {
			return errf(409, "gateway lifecycle controls connection %s", connectionID)
		}
	}
	return nil
}

func emptyGatewayLifecycleState() gatewayLifecycleState {
	return gatewayLifecycleState{
		Version:  gatewayLifecycleStateVersion,
		Controls: map[string]*GatewayControl{}, Observations: map[string]*GatewayProcessObservation{},
	}
}

func cloneGatewayLifecycleState(value gatewayLifecycleState) gatewayLifecycleState {
	result := emptyGatewayLifecycleState()
	for id, control := range value.Controls {
		if control == nil {
			continue
		}
		copy := *control
		copy.Binding = cloneGatewayBindingIdentity(control.Binding)
		result.Controls[id] = &copy
	}
	for id, observation := range value.Observations {
		if observation == nil {
			continue
		}
		copy := *observation
		copy.Capabilities = append([]string(nil), observation.Capabilities...)
		result.Observations[id] = &copy
	}
	return result
}

func cloneGatewayBindingIdentity(value GatewayBindingIdentity) GatewayBindingIdentity {
	result := value
	result.Connection.Capabilities = append([]string(nil), value.Connection.Capabilities...)
	result.Addresses = make([]GatewayAddressIdentity, len(value.Addresses))
	for index, address := range value.Addresses {
		result.Addresses[index] = address
		result.Addresses[index].AllowActors = append([]string(nil), address.AllowActors...)
		result.Addresses[index].AllowConversations = append([]string(nil), address.AllowConversations...)
		result.Addresses[index].BlockActors = append([]string(nil), address.BlockActors...)
		result.Addresses[index].BlockConversations = append([]string(nil), address.BlockConversations...)
	}
	return result
}

func (h *Hub) loadGatewayLifecycle() error {
	state := emptyGatewayLifecycleState()
	envelope := store.RuntimeFoundationEnvelope{}
	exists, err := h.st.LoadRuntimeFoundation(&envelope)
	if err != nil {
		return err
	}
	if !exists {
		h.gatewayLifecycle = state
		h.gatewayFoundationPresent = false
		return nil
	}
	h.gatewayFoundationPresent = true
	if envelope.SchemaVersion != store.RuntimeFoundationSchemaVersion || envelope.MinimumWriter != store.RuntimeWriterFloorR0 {
		return fmt.Errorf("runtime foundation compatibility fields are invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.GatewayLifecycle))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("invalid gateway lifecycle state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid gateway lifecycle state: multiple JSON values")
		}
		return fmt.Errorf("invalid gateway lifecycle state trailer: %w", err)
	}
	if state.Version != gatewayLifecycleStateVersion || state.Controls == nil || state.Observations == nil {
		return fmt.Errorf("unsupported gateway lifecycle state version %d", state.Version)
	}
	for id, control := range state.Controls {
		if control == nil || strings.TrimSpace(id) == "" || control.ConnectionID != id || control.Epoch == 0 || !validGatewayDisposition(control.Disposition) {
			return fmt.Errorf("invalid gateway control %q", id)
		}
		if h.connections[id] == nil {
			return fmt.Errorf("gateway control references missing connection %q", id)
		}
		if control.Binding.Connection.ID != id || strings.TrimSpace(control.Binding.Connection.Provider) == "" {
			return fmt.Errorf("gateway control binding does not match connection %q", id)
		}
		if !gatewayBindingCanonical(control.Binding) {
			return fmt.Errorf("gateway control addresses are not canonical for %q", id)
		}
		currentBinding, bindingErr := h.gatewayBindingIdentityLocked(id)
		if bindingErr != nil {
			return bindingErr
		}
		if !control.NeedsReconcile && !gatewayBindingsEqual(currentBinding, control.Binding) {
			return fmt.Errorf("gateway control binding drift requires reconciliation for %q", id)
		}
	}
	for id, observation := range state.Observations {
		if observation == nil || observation.ConnectionID != id || state.Controls[id] == nil || observation.Sequence == 0 || !validGatewayHealthStatus(observation.Status) ||
			!gatewayStringSlicesEqual(observation.Capabilities, normalizeCapabilities(observation.Capabilities)) {
			return fmt.Errorf("invalid gateway process observation %q", id)
		}
	}
	h.gatewayLifecycle = cloneGatewayLifecycleState(state)
	return nil
}

func (h *Hub) saveGatewayLifecycleLocked(next gatewayLifecycleState) error {
	document := gatewayFoundationDocument{
		SchemaVersion: store.RuntimeFoundationSchemaVersion, MinimumWriter: store.RuntimeWriterFloorR0,
		GatewayLifecycle: next,
	}
	var err error
	if h.saveRuntimeFoundation != nil {
		err = h.saveRuntimeFoundation(document)
	} else {
		err = h.st.SaveRuntimeFoundation(document)
	}
	if err != nil {
		committed, readbackErr := h.gatewayFoundationReadbackEquals(document)
		h.gatewayFoundationPoisoned = true
		h.gatewayFoundationPoisonReason = "gateway lifecycle persistence outcome requires reopen/reconciliation"
		if committed {
			h.gatewayLifecycle = cloneGatewayLifecycleState(next)
			h.gatewayFoundationPresent = true
		}
		return &GatewayFoundationCommitIndeterminateError{Cause: err, ReadbackErr: readbackErr, Committed: committed}
	}
	h.gatewayFoundationPresent = true
	return nil
}

func (h *Hub) gatewayFoundationReadbackEquals(expected gatewayFoundationDocument) (bool, error) {
	if err := h.st.ValidateWriterIdentity(); err != nil {
		return false, err
	}
	var actual gatewayFoundationDocument
	exists, err := h.st.LoadRuntimeFoundation(&actual)
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
	if !h.gatewayFoundationPoisoned {
		return nil
	}
	reason := strings.TrimSpace(h.gatewayFoundationPoisonReason)
	if reason == "" {
		reason = "gateway lifecycle foundation requires reopen/reconciliation"
	}
	return errf(503, "%s", reason)
}

func validGatewayDisposition(value GatewayDisposition) bool {
	switch value {
	case GatewayDispositionProvisioning, GatewayDispositionStable, GatewayDispositionManualRecovery:
		return true
	default:
		return false
	}
}

func validGatewayHealthStatus(value string) bool {
	switch value {
	case "disconnected", "connecting", "connected", "degraded":
		return true
	default:
		return false
	}
}

func gatewayBindingCanonical(binding GatewayBindingIdentity) bool {
	if !gatewayStringSlicesEqual(binding.Connection.Capabilities, normalizeCapabilities(binding.Connection.Capabilities)) {
		return false
	}
	previous := ""
	for _, value := range binding.Addresses {
		if value.ID == "" || value.AgentID == "" || value.ConnectionID != binding.Connection.ID || value.ExternalIdentity == "" || value.ID <= previous ||
			!gatewayStringSlicesEqual(value.AllowActors, normalizeIdentityList(value.AllowActors)) ||
			!gatewayStringSlicesEqual(value.AllowConversations, normalizeIdentityList(value.AllowConversations)) ||
			!gatewayStringSlicesEqual(value.BlockActors, normalizeIdentityList(value.BlockActors)) ||
			!gatewayStringSlicesEqual(value.BlockConversations, normalizeIdentityList(value.BlockConversations)) {
			return false
		}
		previous = value.ID
	}
	return true
}

func (h *Hub) gatewayBindingIdentityLocked(connectionID string) (GatewayBindingIdentity, error) {
	connection := h.connections[strings.TrimSpace(connectionID)]
	if connection == nil {
		return GatewayBindingIdentity{}, errf(404, "connection not found: %s", connectionID)
	}
	identity := GatewayBindingIdentity{Connection: GatewayConnectionIdentity{
		ID: connection.ID, Provider: connection.Provider, AccountRef: connection.AccountRef,
		ScopeRef: connection.ScopeRef, CredentialRef: connection.CredentialRef, Enabled: connection.Enabled,
		Capabilities: normalizeCapabilities(connection.Capabilities), SupersededBy: connection.SupersededBy, ArchivedAt: connection.ArchivedAt,
	}}
	for _, address := range h.addresses {
		if address == nil || address.ConnectionID != connection.ID {
			continue
		}
		identity.Addresses = append(identity.Addresses, GatewayAddressIdentity{
			ID: address.ID, AgentID: address.AgentID, ConnectionID: address.ConnectionID,
			ExternalIdentity: address.ExternalIdentity, DisplayName: address.DisplayName,
			TriggerPolicy: address.TriggerPolicy, ReplyPolicy: address.ReplyPolicy, DMPolicy: address.DMPolicy, TrustDomain: address.TrustDomain,
			AllowActors: normalizeIdentityList(address.AllowActors), AllowConversations: normalizeIdentityList(address.AllowConversations),
			BlockActors: normalizeIdentityList(address.BlockActors), BlockConversations: normalizeIdentityList(address.BlockConversations),
			Enabled:      address.Enabled,
			SupersededBy: address.SupersededBy, ArchivedAt: address.ArchivedAt,
			DeletedAt: address.DeletedAt, Version: address.Version,
		})
	}
	sort.Slice(identity.Addresses, func(i, j int) bool { return identity.Addresses[i].ID < identity.Addresses[j].ID })
	return identity, nil
}

func gatewayBindingsEqual(left, right GatewayBindingIdentity) bool {
	if left.Connection.ID != right.Connection.ID || left.Connection.Provider != right.Connection.Provider ||
		left.Connection.AccountRef != right.Connection.AccountRef || left.Connection.ScopeRef != right.Connection.ScopeRef ||
		left.Connection.CredentialRef != right.Connection.CredentialRef || !gatewayStringSlicesEqual(left.Connection.Capabilities, right.Connection.Capabilities) ||
		left.Connection.Enabled != right.Connection.Enabled || left.Connection.SupersededBy != right.Connection.SupersededBy ||
		left.Connection.ArchivedAt != right.Connection.ArchivedAt || len(left.Addresses) != len(right.Addresses) {
		return false
	}
	for index := range left.Addresses {
		leftAddress, rightAddress := left.Addresses[index], right.Addresses[index]
		if leftAddress.ID != rightAddress.ID || leftAddress.AgentID != rightAddress.AgentID || leftAddress.ConnectionID != rightAddress.ConnectionID ||
			leftAddress.ExternalIdentity != rightAddress.ExternalIdentity || leftAddress.DisplayName != rightAddress.DisplayName ||
			leftAddress.TriggerPolicy != rightAddress.TriggerPolicy || leftAddress.ReplyPolicy != rightAddress.ReplyPolicy ||
			leftAddress.DMPolicy != rightAddress.DMPolicy || leftAddress.TrustDomain != rightAddress.TrustDomain ||
			!gatewayStringSlicesEqual(leftAddress.AllowActors, rightAddress.AllowActors) ||
			!gatewayStringSlicesEqual(leftAddress.AllowConversations, rightAddress.AllowConversations) ||
			!gatewayStringSlicesEqual(leftAddress.BlockActors, rightAddress.BlockActors) ||
			!gatewayStringSlicesEqual(leftAddress.BlockConversations, rightAddress.BlockConversations) ||
			leftAddress.Enabled != rightAddress.Enabled || leftAddress.SupersededBy != rightAddress.SupersededBy ||
			leftAddress.ArchivedAt != rightAddress.ArchivedAt || leftAddress.DeletedAt != rightAddress.DeletedAt ||
			leftAddress.Version != rightAddress.Version {
			return false
		}
	}
	return true
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

func (h *Hub) SnapshotGatewayBinding(connectionID string) (GatewayBindingSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return GatewayBindingSnapshot{}, err
	}
	identity, err := h.gatewayBindingIdentityLocked(connectionID)
	if err != nil {
		return GatewayBindingSnapshot{}, err
	}
	epoch := uint64(0)
	if control := h.gatewayLifecycle.Controls[strings.TrimSpace(connectionID)]; control != nil {
		epoch = control.Epoch
	}
	return GatewayBindingSnapshot{ControlEpoch: epoch, Binding: identity}, nil
}

func (h *Hub) MatchGatewayBinding(expected GatewayBindingSnapshot) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return err
	}
	identity, err := h.gatewayBindingIdentityLocked(expected.Binding.Connection.ID)
	if err != nil {
		return err
	}
	epoch := uint64(0)
	if control := h.gatewayLifecycle.Controls[expected.Binding.Connection.ID]; control != nil {
		epoch = control.Epoch
	}
	if epoch != expected.ControlEpoch || !gatewayBindingsEqual(identity, expected.Binding) {
		return errf(409, "gateway connection binding changed")
	}
	return nil
}

func (h *Hub) InitializeGatewayControl(connectionID string, disposition GatewayDisposition, reason string) (GatewayControl, error) {
	if !validGatewayDisposition(disposition) {
		return GatewayControl{}, errf(400, "invalid gateway disposition %q", disposition)
	}
	unlock := h.gatewayCoordinator.lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return GatewayControl{}, err
	}
	connectionID = strings.TrimSpace(connectionID)
	if h.gatewayLifecycle.Controls[connectionID] != nil {
		return GatewayControl{}, errf(409, "gateway control already exists")
	}
	binding, err := h.gatewayBindingIdentityLocked(connectionID)
	if err != nil {
		return GatewayControl{}, err
	}
	control := &GatewayControl{
		ConnectionID: connectionID, Epoch: 1, Disposition: disposition,
		Reason: strings.TrimSpace(reason), Binding: binding, UpdatedAt: now(),
	}
	next := cloneGatewayLifecycleState(h.gatewayLifecycle)
	next.Controls[connectionID] = control
	connection := h.connections[connectionID]
	observedAt := connection.LastHeartbeatAt
	if observedAt == "" {
		observedAt = now()
	}
	status := connection.Status
	if !validGatewayHealthStatus(status) {
		status = "disconnected"
	}
	next.Observations[connectionID] = &GatewayProcessObservation{
		ConnectionID: connectionID, Sequence: 1, Status: status,
		Error: connection.LastError, ObservedAt: observedAt,
	}
	if err := h.saveGatewayLifecycleLocked(next); err != nil {
		return GatewayControl{}, fmt.Errorf("persist gateway control: %w", err)
	}
	h.gatewayLifecycle = next
	if h.applyGatewayHealthProjectionLocked(connectionID) {
		if err := h.persistIntegrationsLocked(); err != nil {
			return *control, errf(500, "persist gateway health projection: %s", err)
		}
	}
	return *control, nil
}

func (h *Hub) CompareAndSwapGatewayControl(connectionID string, expectedEpoch uint64, update GatewayControlUpdate) (GatewayControl, error) {
	return h.compareAndSwapGatewayControl(connectionID, expectedEpoch, update)
}

func (h *Hub) compareAndSwapGatewayControl(connectionID string, expectedEpoch uint64, update GatewayControlUpdate) (GatewayControl, error) {
	unlock := h.gatewayCoordinator.lock(connectionID)
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return GatewayControl{}, err
	}
	connectionID = strings.TrimSpace(connectionID)
	current := h.gatewayLifecycle.Controls[connectionID]
	if current == nil {
		return GatewayControl{}, errf(404, "gateway control not found: %s", connectionID)
	}
	if current.Epoch != expectedEpoch {
		return GatewayControl{}, errf(409, "gateway control epoch changed")
	}
	if current.Disposition == GatewayDispositionManualRecovery && update.Disposition != "" && update.Disposition != GatewayDispositionManualRecovery {
		return GatewayControl{}, errf(409, "manual gateway recovery requires explicit reconciliation")
	}
	nextControl := *current
	nextControl.Binding = cloneGatewayBindingIdentity(current.Binding)
	if update.Disposition != "" {
		if !validGatewayDisposition(update.Disposition) {
			return GatewayControl{}, errf(400, "invalid gateway disposition %q", update.Disposition)
		}
		nextControl.Disposition = update.Disposition
	}
	nextControl.Reason = strings.TrimSpace(update.Reason)
	if update.ActiveAttemptID != nil {
		nextControl.ActiveAttemptID = strings.TrimSpace(*update.ActiveAttemptID)
	}
	if current.ActiveAttemptID != "" && nextControl.ActiveAttemptID != current.ActiveAttemptID {
		return GatewayControl{}, errf(409, "active gateway attempt requires an exact accepted proof path")
	}
	if update.AnchorRef != nil {
		nextControl.AnchorRef = strings.TrimSpace(*update.AnchorRef)
	}
	if update.NeedsReconcile != nil {
		nextControl.NeedsReconcile = *update.NeedsReconcile
	}
	if nextControl.Disposition == GatewayDispositionStable && update.NeedsReconcile == nil {
		nextControl.NeedsReconcile = false
	}
	if current.NeedsReconcile && !nextControl.NeedsReconcile {
		return GatewayControl{}, errf(409, "gateway lifecycle reconciliation requires an explicit proof path")
	}
	binding, err := h.gatewayBindingIdentityLocked(connectionID)
	if err != nil {
		return GatewayControl{}, err
	}
	nextControl.Binding = binding
	nextControl.Epoch++
	nextControl.UpdatedAt = now()
	next := cloneGatewayLifecycleState(h.gatewayLifecycle)
	next.Controls[connectionID] = &nextControl
	if err := h.saveGatewayLifecycleLocked(next); err != nil {
		return GatewayControl{}, fmt.Errorf("persist gateway control CAS: %w", err)
	}
	h.gatewayLifecycle = next
	if h.applyGatewayHealthProjectionLocked(connectionID) {
		if err := h.persistIntegrationsLocked(); err != nil {
			return nextControl, errf(500, "persist gateway health projection: %s", err)
		}
	}
	return nextControl, nil
}

func (h *Hub) GatewayHealth(connectionID string) (GatewayHealthStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.connections[strings.TrimSpace(connectionID)]
	if connection == nil {
		return GatewayHealthStatus{}, errf(404, "connection not found: %s", connectionID)
	}
	return h.reduceGatewayHealthLocked(*connection), nil
}

func (h *Hub) reduceGatewayHealthLocked(connection PlatformConnection) GatewayHealthStatus {
	result := GatewayHealthStatus{ConnectionID: connection.ID, Status: connection.Status, Error: connection.LastError}
	control := h.gatewayLifecycle.Controls[connection.ID]
	if h.gatewayFoundationPoisoned {
		if control != nil {
			result.Disposition = control.Disposition
			result.NeedsReconcile = control.NeedsReconcile
			if observation := h.gatewayLifecycle.Observations[connection.ID]; observation != nil {
				result.ObservationSeq = observation.Sequence
			}
		}
		result.Status = "degraded"
		result.Error = "gateway lifecycle foundation requires reopen/reconciliation"
		return result
	}
	var observation *GatewayProcessObservation
	if control != nil {
		result.Disposition = control.Disposition
		result.NeedsReconcile = control.NeedsReconcile
		observation = h.gatewayLifecycle.Observations[connection.ID]
		if observation != nil {
			result.ObservationSeq = observation.Sequence
		}
	}
	if control != nil && control.NeedsReconcile {
		result.Status = "degraded"
		result.Error = "gateway lifecycle reconciliation required"
		if control.Reason != "" {
			result.Error = control.Reason
		}
	} else if control != nil && control.Disposition == GatewayDispositionManualRecovery {
		result.Status = "degraded"
		result.Error = control.Reason
		if result.Error == "" {
			result.Error = "manual gateway recovery required"
		}
	} else if control != nil && control.ActiveAttemptID != "" {
		result.Status = "connecting"
		result.Error = "gateway lifecycle transition in progress"
	} else if control != nil && control.Disposition == GatewayDispositionProvisioning {
		result.Status = "connecting"
		result.Error = control.Reason
	} else if connection.ArchivedAt != "" || connection.SupersededBy != "" {
		result.Status = "disconnected"
		result.Error = "connection is archived or superseded"
	} else if !connection.Enabled {
		result.Status = "disconnected"
		result.Error = "connection is disabled"
	} else if observation != nil {
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
	if connection.Status == projection.Status && connection.LastError == projection.Error {
		return false
	}
	connection.Status = projection.Status
	connection.LastError = projection.Error
	connection.UpdatedAt = now()
	return true
}

func (h *Hub) recordGatewayObservationLocked(connectionID, status, detail, observedAt string) (bool, error) {
	return h.recordGatewayObservationWithCapabilitiesLocked(connectionID, status, detail, observedAt, nil)
}

func (h *Hub) recordGatewayObservationWithCapabilitiesLocked(connectionID, status, detail, observedAt string, capabilities []string) (bool, error) {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return h.gatewayLifecycle.Controls[connectionID] != nil, err
	}
	control := h.gatewayLifecycle.Controls[connectionID]
	if control == nil {
		h.applyGatewayHealthProjectionLocked(connectionID)
		return false, nil
	}
	if !validGatewayHealthStatus(status) {
		return true, errf(400, "invalid connection status %q", status)
	}
	next := cloneGatewayLifecycleState(h.gatewayLifecycle)
	sequence := uint64(1)
	observedCapabilities := []string(nil)
	if previous := next.Observations[connectionID]; previous != nil {
		sequence = previous.Sequence + 1
		observedCapabilities = append([]string(nil), previous.Capabilities...)
	}
	if capabilities != nil {
		observedCapabilities = normalizeCapabilities(capabilities)
	}
	next.Observations[connectionID] = &GatewayProcessObservation{
		ConnectionID: connectionID, Sequence: sequence, Status: status,
		Error: strings.TrimSpace(detail), Capabilities: observedCapabilities, ObservedAt: observedAt,
	}
	if err := h.saveGatewayLifecycleLocked(next); err != nil {
		return true, err
	}
	h.gatewayLifecycle = next
	h.applyGatewayHealthProjectionLocked(connectionID)
	return true, nil
}

func (h *Hub) GatewayEligibleConnectionIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gatewayFoundationPoisoned {
		return nil
	}
	result := []string{}
	for id, control := range h.gatewayLifecycle.Controls {
		connection := h.connections[id]
		if connection == nil || !connection.Enabled || connection.ArchivedAt != "" || control.Disposition != GatewayDispositionStable ||
			control.ActiveAttemptID != "" || control.NeedsReconcile {
			continue
		}
		binding, err := h.gatewayBindingIdentityLocked(id)
		if err == nil && gatewayBindingsEqual(binding, control.Binding) && gatewayBindingEligible(binding) {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func gatewayBindingEligible(binding GatewayBindingIdentity) bool {
	for _, address := range binding.Addresses {
		if address.Enabled && address.ArchivedAt == "" && address.DeletedAt == "" && address.ConnectionID == binding.Connection.ID {
			return true
		}
	}
	return false
}

func (h *Hub) prepareGatewayMutationLocked(connectionIDs ...string) (*gatewayMutationTicket, error) {
	if err := h.requireGatewayFoundationHealthyLocked(); err != nil {
		return nil, err
	}
	ids := normalizeGatewayConnectionIDs(connectionIDs)
	next := cloneGatewayLifecycleState(h.gatewayLifecycle)
	ticket := &gatewayMutationTicket{preparedEpochs: map[string]uint64{}}
	for _, id := range ids {
		control := next.Controls[id]
		if control == nil {
			continue
		}
		if control.ActiveAttemptID != "" || control.NeedsReconcile || control.Disposition != GatewayDispositionStable {
			return nil, errf(409, "gateway lifecycle controls connection %s", id)
		}
		copy := *control
		copy.Binding = cloneGatewayBindingIdentity(control.Binding)
		copy.Epoch++
		copy.NeedsReconcile = true
		copy.Reason = "connection binding mutation requires reconciliation"
		copy.UpdatedAt = now()
		next.Controls[id] = &copy
		ticket.preparedEpochs[id] = copy.Epoch
	}
	if len(ticket.preparedEpochs) == 0 {
		return ticket, nil
	}
	if err := h.saveGatewayLifecycleLocked(next); err != nil {
		return nil, fmt.Errorf("persist gateway mutation fence: %w", err)
	}
	h.gatewayLifecycle = next
	for id := range ticket.preparedEpochs {
		h.applyGatewayHealthProjectionLocked(id)
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
	next := cloneGatewayLifecycleState(h.gatewayLifecycle)
	for id, epoch := range ticket.preparedEpochs {
		control := next.Controls[id]
		if control == nil || control.Epoch != epoch || !control.NeedsReconcile {
			return errf(409, "gateway mutation fence changed for connection %s", id)
		}
		binding, err := h.gatewayBindingIdentityLocked(id)
		if err != nil {
			return err
		}
		control.Binding = binding
		control.NeedsReconcile = false
		control.Reason = ""
		control.UpdatedAt = now()
	}
	if err := h.saveGatewayLifecycleLocked(next); err != nil {
		return fmt.Errorf("persist gateway mutation completion: %w", err)
	}
	h.gatewayLifecycle = next
	for id := range ticket.preparedEpochs {
		h.applyGatewayHealthProjectionLocked(id)
	}
	return nil
}
