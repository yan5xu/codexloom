package hub

import (
	"sort"
	"strings"
)

const (
	AddressLifecycleArchive  = "archive"
	AddressLifecycleRestore  = "restore"
	AddressLifecycleDelete   = "delete"
	AddressLifecycleTransfer = "transfer"
	AddressLifecycleRollback = "rollback_transfer"
)

type AddressLifecycleMembershipSnapshot struct {
	ID            string `json:"id"`
	Enabled       bool   `json:"enabled"`
	ArchivedAt    string `json:"archivedAt,omitempty"`
	SupersededBy  string `json:"supersededBy,omitempty"`
	VersionBefore int    `json:"versionBefore"`
	VersionAfter  int    `json:"versionAfter"`
}

// AddressLifecycleOperation is the durable receipt for one operator action.
// Address and Membership IDs remain stable; the receipt records enough state
// to validate an explicit archive restore or a clean transfer rollback.
type AddressLifecycleOperation struct {
	ID                   string                               `json:"id"`
	Action               string                               `json:"action"`
	AddressID            string                               `json:"addressId"`
	SourceOperationID    string                               `json:"sourceOperationId,omitempty"`
	FromAgentID          string                               `json:"fromAgentId,omitempty"`
	ToAgentID            string                               `json:"toAgentId,omitempty"`
	AddressVersionBefore int                                  `json:"addressVersionBefore"`
	AddressVersionAfter  int                                  `json:"addressVersionAfter"`
	AddressEnabledBefore bool                                 `json:"addressEnabledBefore"`
	MembershipsBefore    []AddressLifecycleMembershipSnapshot `json:"membershipsBefore,omitempty"`
	InboxOrderFence      int                                  `json:"inboxOrderFence,omitempty"`
	OutboxOrderFence     int                                  `json:"outboxOrderFence,omitempty"`
	ProviderOrderFence   int                                  `json:"providerOrderFence,omitempty"`
	ReversedBy           string                               `json:"reversedBy,omitempty"`
	CreatedAt            string                               `json:"createdAt"`
}

type AddressLifecycleParams struct {
	Action          string `json:"action"`
	TargetAgent     string `json:"targetAgent,omitempty"`
	ExpectedVersion *int   `json:"expectedVersion,omitempty"`
	Confirm         string `json:"confirm,omitempty"`
	DryRun          bool   `json:"dryRun,omitempty"`
}

type AddressTransferRollbackParams struct {
	ExpectedVersion *int   `json:"expectedVersion,omitempty"`
	Confirm         string `json:"confirm,omitempty"`
	DryRun          bool   `json:"dryRun,omitempty"`
}

type AddressLifecycleBlocker struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	State   string `json:"state,omitempty"`
	Message string `json:"message"`
}

type AddressLifecyclePreflight struct {
	Action                 string                    `json:"action"`
	AddressID              string                    `json:"addressId"`
	CurrentVersion         int                       `json:"currentVersion"`
	FromAgentID            string                    `json:"fromAgentId,omitempty"`
	ToAgentID              string                    `json:"toAgentId,omitempty"`
	SourceOperationID      string                    `json:"sourceOperationId,omitempty"`
	Allowed                bool                      `json:"allowed"`
	Blockers               []AddressLifecycleBlocker `json:"blockers"`
	Warnings               []string                  `json:"warnings"`
	MembershipCount        int                       `json:"membershipCount"`
	EnabledMembershipCount int                       `json:"enabledMembershipCount"`
	CatchUp                string                    `json:"catchUp"`
}

type AddressLifecycleResult struct {
	Preflight AddressLifecyclePreflight  `json:"preflight"`
	Address   AgentAddress               `json:"address"`
	Operation *AddressLifecycleOperation `json:"operation,omitempty"`
}

func (h *Hub) normalizeAddressLifecycleLocked() bool {
	changed := false
	if h.addressOperations == nil {
		h.addressOperations = map[string]*AddressLifecycleOperation{}
	}
	for _, address := range h.addresses {
		if address == nil {
			continue
		}
		if address.Version < 1 {
			address.Version = 1
			changed = true
		}
		if address.DeletedAt != "" {
			if address.ArchivedAt == "" {
				address.ArchivedAt = address.DeletedAt
				changed = true
			}
			if address.Enabled {
				address.Enabled = false
				changed = true
			}
		}
	}
	for _, membership := range h.memberships {
		if membership != nil && membership.Version < 1 {
			membership.Version = 1
			changed = true
		}
	}
	return changed
}

func (h *Hub) ListAddressLifecycleOperations(addressID string) []AddressLifecycleOperation {
	h.mu.Lock()
	defer h.mu.Unlock()
	addressID = strings.TrimSpace(addressID)
	out := make([]AddressLifecycleOperation, 0, len(h.addressOperations))
	for _, operation := range h.addressOperations {
		if operation == nil || addressID != "" && operation.AddressID != addressID {
			continue
		}
		out = append(out, cloneAddressLifecycleOperation(*operation))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (h *Hub) GetAddressLifecycleOperation(id string) (AddressLifecycleOperation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	operation := h.addressOperations[strings.TrimSpace(id)]
	if operation == nil {
		return AddressLifecycleOperation{}, errf(404, "address lifecycle operation not found: %s", id)
	}
	return cloneAddressLifecycleOperation(*operation), nil
}

func (h *Hub) PreflightAddressLifecycle(addressID string, params AddressLifecycleParams) (AddressLifecyclePreflight, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	plan, _, err := h.preflightAddressLifecycleLocked(addressID, params)
	return plan, err
}

func (h *Hub) ApplyAddressLifecycle(addressID string, params AddressLifecycleParams) (AddressLifecycleResult, error) {
	addressID = strings.TrimSpace(addressID)
	unlock := h.lockGatewayMutationScope(nil, []string{addressID})
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()

	plan, sourceOperation, err := h.preflightAddressLifecycleLocked(addressID, params)
	address := h.addresses[strings.TrimSpace(addressID)]
	result := AddressLifecycleResult{Preflight: plan}
	if address != nil {
		result.Address = *address
	}
	if err != nil {
		return result, err
	}
	if params.DryRun {
		return result, nil
	}
	if strings.TrimSpace(params.Confirm) != address.ID {
		return result, errf(400, "confirm must exactly match address id %s", address.ID)
	}
	if params.ExpectedVersion == nil {
		return result, errf(400, "expectedVersion is required")
	}
	if *params.ExpectedVersion != address.Version {
		return result, errf(409, "address version changed: expected %d, current %d", *params.ExpectedVersion, address.Version)
	}
	if !plan.Allowed {
		return result, errf(409, "address %s %s preflight failed", address.ID, plan.Action)
	}

	nextAddresses := cloneAddresses(h.addresses)
	nextMemberships := cloneMemberships(h.memberships)
	nextCandidates := cloneConversationCandidates(h.conversationCandidates)
	nextOperations := cloneAddressLifecycleOperations(h.addressOperations)
	nextAddress := nextAddresses[address.ID]
	ts := now()
	operation := &AddressLifecycleOperation{
		ID: newIntegrationID("aop"), Action: plan.Action, AddressID: address.ID,
		FromAgentID: address.AgentID, AddressVersionBefore: address.Version,
		AddressEnabledBefore: address.Enabled, CreatedAt: ts,
	}

	switch plan.Action {
	case AddressLifecycleArchive:
		operation.MembershipsBefore = archiveAddressMemberships(nextMemberships, address.ID, ts)
		nextAddress.Enabled = false
		nextAddress.ArchivedAt = ts
		nextAddress.UpdatedAt = ts
		nextAddress.Version++
		markAddressCandidatesUnavailable(nextCandidates, address.ID, ts)
	case AddressLifecycleRestore:
		operation.SourceOperationID = sourceOperation.ID
		operation.ToAgentID = address.AgentID
		operation.MembershipsBefore = append([]AddressLifecycleMembershipSnapshot(nil), sourceOperation.MembershipsBefore...)
		nextAddress.Enabled = sourceOperation.AddressEnabledBefore
		nextAddress.ArchivedAt = ""
		nextAddress.SupersededBy = ""
		nextAddress.UpdatedAt = ts
		nextAddress.Version++
		restoreAddressMemberships(nextMemberships, sourceOperation.MembershipsBefore, ts)
		nextOperations[sourceOperation.ID].ReversedBy = operation.ID
	case AddressLifecycleDelete:
		operation.MembershipsBefore = archiveAddressMemberships(nextMemberships, address.ID, ts)
		nextAddress.Enabled = false
		if nextAddress.ArchivedAt == "" {
			nextAddress.ArchivedAt = ts
		}
		nextAddress.DeletedAt = ts
		nextAddress.UpdatedAt = ts
		nextAddress.Version++
		markAddressCandidatesUnavailable(nextCandidates, address.ID, ts)
	case AddressLifecycleTransfer:
		target := h.resolveLocked(strings.TrimSpace(params.TargetAgent))
		operation.ToAgentID = target.ID
		operation.MembershipsBefore = snapshotAddressMemberships(nextMemberships, address.ID)
		operation.InboxOrderFence = len(h.inboxOrder)
		operation.OutboxOrderFence = len(h.outboxOrder)
		operation.ProviderOrderFence = len(h.providerOperationOrder)
		nextAddress.AgentID = target.ID
		nextAddress.UpdatedAt = ts
		nextAddress.Version++
	default:
		return result, errf(400, "unsupported address lifecycle action %q", plan.Action)
	}
	operation.AddressVersionAfter = nextAddress.Version
	nextOperations[operation.ID] = operation
	ticket, err := h.prepareGatewayMutationLocked(address.ConnectionID)
	if err != nil {
		return result, err
	}
	if err := h.commitAddressLifecycleLocked(nextAddresses, nextMemberships, nextCandidates, nextOperations); err != nil {
		return result, err
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return result, err
	}
	if ticket.active() {
		if err := h.persistIntegrationsLocked(); err != nil {
			return result, errf(500, "save reconciled address lifecycle operation: %s", err)
		}
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": *nextAddress})
	for _, snapshot := range operation.MembershipsBefore {
		if membership := nextMemberships[snapshot.ID]; membership != nil {
			h.emitGlobalLocked("loom/conversation-membership", map[string]any{"membership": *membership})
		}
	}
	h.emitGlobalLocked("loom/integration-address-lifecycle", map[string]any{"operation": operation, "address": *nextAddress})
	result.Address = *nextAddress
	copy := cloneAddressLifecycleOperation(*operation)
	result.Operation = &copy
	return result, nil
}

func (h *Hub) PreflightAddressTransferRollback(operationID string) (AddressLifecyclePreflight, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	plan, _, _, err := h.preflightAddressTransferRollbackLocked(operationID, nil)
	return plan, err
}

func (h *Hub) RollbackAddressTransfer(operationID string, params AddressTransferRollbackParams) (AddressLifecycleResult, error) {
	h.mu.Lock()
	operation := h.addressOperations[strings.TrimSpace(operationID)]
	addressID := ""
	if operation != nil {
		addressID = operation.AddressID
	}
	h.mu.Unlock()
	unlock := h.lockGatewayMutationScope(nil, []string{addressID})
	defer unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	plan, source, address, err := h.preflightAddressTransferRollbackLocked(operationID, params.ExpectedVersion)
	result := AddressLifecycleResult{Preflight: plan}
	if address != nil {
		result.Address = *address
	}
	if err != nil {
		return result, err
	}
	if params.DryRun {
		return result, nil
	}
	if strings.TrimSpace(params.Confirm) != source.ID {
		return result, errf(400, "confirm must exactly match transfer operation id %s", source.ID)
	}
	if params.ExpectedVersion == nil {
		return result, errf(400, "expectedVersion is required")
	}
	if *params.ExpectedVersion != address.Version {
		return result, errf(409, "address version changed: expected %d, current %d", *params.ExpectedVersion, address.Version)
	}
	if !plan.Allowed {
		return result, errf(409, "address transfer rollback preflight failed for %s", source.ID)
	}

	nextAddresses := cloneAddresses(h.addresses)
	nextOperations := cloneAddressLifecycleOperations(h.addressOperations)
	nextAddress := nextAddresses[address.ID]
	ts := now()
	rollback := &AddressLifecycleOperation{
		ID: newIntegrationID("aop"), Action: AddressLifecycleRollback, AddressID: address.ID,
		SourceOperationID: source.ID, FromAgentID: source.ToAgentID, ToAgentID: source.FromAgentID,
		AddressVersionBefore: address.Version, AddressEnabledBefore: address.Enabled, CreatedAt: ts,
	}
	nextAddress.AgentID = source.FromAgentID
	nextAddress.UpdatedAt = ts
	nextAddress.Version++
	rollback.AddressVersionAfter = nextAddress.Version
	nextOperations[source.ID].ReversedBy = rollback.ID
	nextOperations[rollback.ID] = rollback
	ticket, err := h.prepareGatewayMutationLocked(address.ConnectionID)
	if err != nil {
		return result, err
	}
	if err := h.commitAddressLifecycleLocked(nextAddresses, h.memberships, h.conversationCandidates, nextOperations); err != nil {
		return result, err
	}
	if err := h.finishGatewayMutationLocked(ticket); err != nil {
		return result, err
	}
	if ticket.active() {
		if err := h.persistIntegrationsLocked(); err != nil {
			return result, errf(500, "save reconciled address transfer rollback: %s", err)
		}
	}
	h.emitGlobalLocked("loom/integration-address", map[string]any{"address": *nextAddress})
	h.emitGlobalLocked("loom/integration-address-lifecycle", map[string]any{"operation": rollback, "address": *nextAddress})
	result.Address = *nextAddress
	copy := cloneAddressLifecycleOperation(*rollback)
	result.Operation = &copy
	return result, nil
}

func (h *Hub) preflightAddressLifecycleLocked(addressID string, params AddressLifecycleParams) (AddressLifecyclePreflight, *AddressLifecycleOperation, error) {
	action := strings.ToLower(strings.TrimSpace(params.Action))
	if !oneOf(action, AddressLifecycleArchive, AddressLifecycleRestore, AddressLifecycleDelete, AddressLifecycleTransfer) {
		return AddressLifecyclePreflight{}, nil, errf(400, "action must be archive, restore, delete, or transfer")
	}
	addressID = strings.TrimSpace(addressID)
	address := h.addresses[addressID]
	if address == nil {
		return AddressLifecyclePreflight{}, nil, errf(404, "agent address not found: %s", addressID)
	}
	plan := h.baseAddressLifecyclePlanLocked(action, address)
	if params.ExpectedVersion != nil && *params.ExpectedVersion != address.Version {
		plan.addBlocker("expected_version", address.ID, "stale", "expectedVersion does not match the current Address version")
	}
	var sourceOperation *AddressLifecycleOperation
	switch action {
	case AddressLifecycleArchive:
		if address.DeletedAt != "" {
			plan.addBlocker("deleted", address.ID, "deleted", "deleted addresses cannot be archived")
		} else if address.ArchivedAt != "" {
			plan.addBlocker("archived", address.ID, "archived", "address is already archived")
		}
		plan.Blockers = append(plan.Blockers, h.activeAddressWorkBlockersLocked(address.ID)...)
	case AddressLifecycleRestore:
		if address.DeletedAt != "" {
			plan.addBlocker("deleted", address.ID, "deleted", "delete is terminal and cannot be restored")
		} else if address.ArchivedAt == "" {
			plan.addBlocker("not_archived", address.ID, "active", "address is not archived")
		} else {
			sourceOperation = h.latestRestorableArchiveLocked(address.ID)
			if sourceOperation == nil {
				plan.addBlocker("missing_archive_receipt", address.ID, "archived", "address has no managed archive receipt to restore")
			} else {
				plan.SourceOperationID = sourceOperation.ID
				if address.Version != sourceOperation.AddressVersionAfter {
					plan.addBlocker("version_changed", address.ID, "changed", "address changed after archive")
				}
				plan.Blockers = append(plan.Blockers, h.membershipRestoreBlockersLocked(sourceOperation)...)
			}
		}
		plan.Blockers = append(plan.Blockers, h.addressConnectionBlockersLocked(address)...)
		plan.Blockers = append(plan.Blockers, h.identityConflictBlockersLocked(address, address.ID)...)
	case AddressLifecycleDelete:
		if address.DeletedAt != "" {
			plan.addBlocker("deleted", address.ID, "deleted", "address is already deleted")
		}
		plan.Blockers = append(plan.Blockers, h.activeAddressWorkBlockersLocked(address.ID)...)
	case AddressLifecycleTransfer:
		if address.DeletedAt != "" || address.ArchivedAt != "" {
			plan.addBlocker("inactive_address", address.ID, "archived", "only a non-archived address can be transferred")
		}
		target := h.resolveLocked(strings.TrimSpace(params.TargetAgent))
		if target == nil {
			plan.addBlocker("target_agent", strings.TrimSpace(params.TargetAgent), "missing", "target Agent was not found")
		} else {
			plan.ToAgentID = target.ID
			if target.ID == address.AgentID {
				plan.addBlocker("target_agent", target.ID, "same_owner", "target Agent already owns the address")
			}
			plan.Blockers = append(plan.Blockers, h.targetTrustDomainBlockersLocked(address, target.ID)...)
		}
		plan.Blockers = append(plan.Blockers, h.addressConnectionBlockersLocked(address)...)
		plan.Blockers = append(plan.Blockers, h.identityConflictBlockersLocked(address, address.ID)...)
		plan.Blockers = append(plan.Blockers, h.activeAddressWorkBlockersLocked(address.ID)...)
		if !address.Enabled {
			plan.Warnings = append(plan.Warnings, "address is disabled; transfer preserves disabled state")
		}
	}
	plan.Allowed = len(plan.Blockers) == 0
	sortLifecyclePlan(&plan)
	return plan, sourceOperation, nil
}

func (h *Hub) preflightAddressTransferRollbackLocked(operationID string, expectedVersion *int) (AddressLifecyclePreflight, *AddressLifecycleOperation, *AgentAddress, error) {
	operationID = strings.TrimSpace(operationID)
	source := h.addressOperations[operationID]
	if source == nil {
		return AddressLifecyclePreflight{}, nil, nil, errf(404, "address lifecycle operation not found: %s", operationID)
	}
	if source.Action != AddressLifecycleTransfer {
		return AddressLifecyclePreflight{}, nil, nil, errf(409, "operation %s is not an address transfer", operationID)
	}
	address := h.addresses[source.AddressID]
	if address == nil {
		return AddressLifecyclePreflight{}, source, nil, errf(404, "transferred address not found: %s", source.AddressID)
	}
	plan := h.baseAddressLifecyclePlanLocked(AddressLifecycleRollback, address)
	if expectedVersion != nil && *expectedVersion != address.Version {
		plan.addBlocker("expected_version", address.ID, "stale", "expectedVersion does not match the current Address version")
	}
	plan.SourceOperationID = source.ID
	plan.FromAgentID = source.ToAgentID
	plan.ToAgentID = source.FromAgentID
	if source.ReversedBy != "" {
		plan.addBlocker("already_reversed", source.ID, "reversed", "transfer was already reversed by "+source.ReversedBy)
	}
	if address.DeletedAt != "" || address.ArchivedAt != "" {
		plan.addBlocker("inactive_address", address.ID, "archived", "transferred address is no longer active")
	}
	if address.AgentID != source.ToAgentID {
		plan.addBlocker("owner_changed", address.ID, address.AgentID, "address is no longer owned by the transfer target")
	}
	if address.Version != source.AddressVersionAfter {
		plan.addBlocker("version_changed", address.ID, "changed", "address changed after the transfer")
	}
	plan.Blockers = append(plan.Blockers, h.activeAddressWorkBlockersLocked(address.ID)...)
	plan.Blockers = append(plan.Blockers, h.postTransferActivityBlockersLocked(source)...)
	plan.Blockers = append(plan.Blockers, h.membershipRollbackBlockersLocked(source)...)
	plan.Blockers = append(plan.Blockers, h.addressConnectionBlockersLocked(address)...)
	plan.Blockers = append(plan.Blockers, h.identityConflictBlockersLocked(address, address.ID)...)
	if from := h.agents[source.FromAgentID]; from == nil {
		plan.addBlocker("source_agent", source.FromAgentID, "missing", "original Agent no longer exists")
	} else {
		plan.Blockers = append(plan.Blockers, h.targetTrustDomainBlockersLocked(address, from.ID)...)
	}
	plan.Allowed = len(plan.Blockers) == 0
	sortLifecyclePlan(&plan)
	return plan, source, address, nil
}

func (h *Hub) baseAddressLifecyclePlanLocked(action string, address *AgentAddress) AddressLifecyclePreflight {
	plan := AddressLifecyclePreflight{
		Action: action, AddressID: address.ID, CurrentVersion: address.Version,
		FromAgentID: address.AgentID, Blockers: []AddressLifecycleBlocker{}, Warnings: []string{},
		CatchUp: "the Connection cursor and Address ID are preserved; provider replay for a disabled interval is connector-dependent and is not performed by this operation",
	}
	for _, membership := range h.memberships {
		if membership == nil || membership.AddressID != address.ID {
			continue
		}
		plan.MembershipCount++
		if membership.Enabled && membership.ArchivedAt == "" {
			plan.EnabledMembershipCount++
		}
	}
	failedOutbox := 0
	for _, item := range h.outbox {
		if item != nil && item.AddressID == address.ID && item.State == "failed" {
			failedOutbox++
		}
	}
	if failedOutbox > 0 {
		plan.Warnings = append(plan.Warnings, "historical failed Outbox items remain readable but cannot be retried after ownership changes or deletion")
	}
	return plan
}

func (p *AddressLifecyclePreflight) addBlocker(kind, id, state, message string) {
	p.Blockers = append(p.Blockers, AddressLifecycleBlocker{Kind: kind, ID: id, State: state, Message: message})
}

func (h *Hub) activeAddressWorkBlockersLocked(addressID string) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	for _, item := range h.inbox {
		if item == nil || item.AddressID != addressID || oneOf(item.State, "handled", "cancelled") {
			continue
		}
		blockers = append(blockers, AddressLifecycleBlocker{Kind: "inbox", ID: item.ID, State: item.State, Message: "Inbox item is not terminal"})
	}
	for _, item := range h.outbox {
		if item == nil || item.AddressID != addressID || !oneOf(item.State, "pending", "sending") {
			continue
		}
		blockers = append(blockers, AddressLifecycleBlocker{Kind: "outbox", ID: item.ID, State: item.State, Message: "Outbox delivery is still active"})
	}
	for _, operation := range h.providerOperations {
		if operation == nil || operation.AddressID != addressID || !oneOf(operation.State, "pending", "running") {
			continue
		}
		blockers = append(blockers, AddressLifecycleBlocker{Kind: "provider_operation", ID: operation.ID, State: operation.State, Message: "provider operation is still active"})
	}
	return blockers
}

func (h *Hub) identityConflictBlockersLocked(address *AgentAddress, exceptID string) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	for _, other := range h.addresses {
		if other == nil || other.ID == exceptID || other.DeletedAt != "" || other.ArchivedAt != "" {
			continue
		}
		if other.ConnectionID == address.ConnectionID && other.ExternalIdentity == address.ExternalIdentity {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "identity_conflict", ID: other.ID, State: "bound", Message: "canonical external identity is bound to another active Address"})
		}
	}
	return blockers
}

func (h *Hub) addressConnectionBlockersLocked(address *AgentAddress) []AddressLifecycleBlocker {
	connection := h.connections[address.ConnectionID]
	if connection == nil {
		return []AddressLifecycleBlocker{{Kind: "connection", ID: address.ConnectionID, State: "missing", Message: "Address Connection no longer exists"}}
	}
	if connection.ArchivedAt != "" {
		return []AddressLifecycleBlocker{{Kind: "connection", ID: connection.ID, State: "archived", Message: "Address Connection is archived"}}
	}
	return nil
}

func (h *Hub) targetTrustDomainBlockersLocked(address *AgentAddress, targetAgentID string) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	for _, other := range h.addresses {
		if other == nil || other.ID == address.ID || other.AgentID != targetAgentID || other.DeletedAt != "" {
			continue
		}
		if other.TrustDomain != address.TrustDomain {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "trust_domain", ID: other.ID, State: other.TrustDomain, Message: "target Agent has an Address in a different trust domain"})
		}
	}
	return blockers
}

func (h *Hub) latestRestorableArchiveLocked(addressID string) *AddressLifecycleOperation {
	var latest *AddressLifecycleOperation
	for _, operation := range h.addressOperations {
		if operation == nil || operation.AddressID != addressID || operation.Action != AddressLifecycleArchive || operation.ReversedBy != "" {
			continue
		}
		if latest == nil || operation.AddressVersionAfter > latest.AddressVersionAfter ||
			operation.AddressVersionAfter == latest.AddressVersionAfter && (operation.CreatedAt > latest.CreatedAt || operation.CreatedAt == latest.CreatedAt && operation.ID > latest.ID) {
			latest = operation
		}
	}
	return latest
}

func (h *Hub) membershipRestoreBlockersLocked(operation *AddressLifecycleOperation) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	for _, snapshot := range operation.MembershipsBefore {
		membership := h.memberships[snapshot.ID]
		if membership == nil {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "membership_missing", ID: snapshot.ID, State: "missing", Message: "archived Membership no longer exists"})
			continue
		}
		if membership.Version != snapshot.VersionAfter {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "membership_changed", ID: snapshot.ID, State: "changed", Message: "Membership changed after archive"})
		}
	}
	return blockers
}

func (h *Hub) membershipRollbackBlockersLocked(operation *AddressLifecycleOperation) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	seen := map[string]bool{}
	for _, snapshot := range operation.MembershipsBefore {
		seen[snapshot.ID] = true
		membership := h.memberships[snapshot.ID]
		if membership == nil || membership.Version != snapshot.VersionBefore {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "membership_changed", ID: snapshot.ID, State: "changed", Message: "Membership changed after transfer"})
		}
	}
	for _, membership := range h.memberships {
		if membership != nil && membership.AddressID == operation.AddressID && !seen[membership.ID] {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "membership_created", ID: membership.ID, State: "created", Message: "Membership was created after transfer"})
		}
	}
	return blockers
}

func (h *Hub) postTransferActivityBlockersLocked(operation *AddressLifecycleOperation) []AddressLifecycleBlocker {
	blockers := []AddressLifecycleBlocker{}
	if operation.InboxOrderFence > len(h.inboxOrder) || operation.OutboxOrderFence > len(h.outboxOrder) || operation.ProviderOrderFence > len(h.providerOperationOrder) {
		return []AddressLifecycleBlocker{{Kind: "activity_fence", ID: operation.ID, State: "invalid", Message: "post-transfer activity fence cannot be validated"}}
	}
	for _, id := range h.inboxOrder[operation.InboxOrderFence:] {
		item := h.inbox[id]
		if item != nil && item.AddressID == operation.AddressID && item.AgentID == operation.ToAgentID {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "post_transfer_inbox", ID: item.ID, State: item.State, Message: "target Agent received Inbox activity after transfer"})
		}
	}
	for _, id := range h.outboxOrder[operation.OutboxOrderFence:] {
		item := h.outbox[id]
		if item != nil && item.AddressID == operation.AddressID && item.AgentID == operation.ToAgentID {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "post_transfer_outbox", ID: item.ID, State: item.State, Message: "target Agent created Outbox activity after transfer"})
		}
	}
	for _, id := range h.providerOperationOrder[operation.ProviderOrderFence:] {
		providerOperation := h.providerOperations[id]
		if providerOperation != nil && providerOperation.AddressID == operation.AddressID && providerOperation.AgentID == operation.ToAgentID {
			blockers = append(blockers, AddressLifecycleBlocker{Kind: "post_transfer_provider_operation", ID: providerOperation.ID, State: providerOperation.State, Message: "target Agent created provider activity after transfer"})
		}
	}
	return blockers
}

func snapshotAddressMemberships(memberships map[string]*ConversationMembership, addressID string) []AddressLifecycleMembershipSnapshot {
	out := []AddressLifecycleMembershipSnapshot{}
	for _, membership := range memberships {
		if membership == nil || membership.AddressID != addressID {
			continue
		}
		out = append(out, AddressLifecycleMembershipSnapshot{
			ID: membership.ID, Enabled: membership.Enabled, ArchivedAt: membership.ArchivedAt,
			SupersededBy: membership.SupersededBy, VersionBefore: membership.Version, VersionAfter: membership.Version,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func archiveAddressMemberships(memberships map[string]*ConversationMembership, addressID, ts string) []AddressLifecycleMembershipSnapshot {
	out := []AddressLifecycleMembershipSnapshot{}
	for _, membership := range memberships {
		if membership == nil || membership.AddressID != addressID || membership.ArchivedAt != "" {
			continue
		}
		snapshot := AddressLifecycleMembershipSnapshot{
			ID: membership.ID, Enabled: membership.Enabled, ArchivedAt: membership.ArchivedAt,
			SupersededBy: membership.SupersededBy, VersionBefore: membership.Version,
		}
		membership.Enabled = false
		membership.ArchivedAt = ts
		membership.SupersededBy = ""
		membership.UpdatedAt = ts
		membership.Version++
		snapshot.VersionAfter = membership.Version
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func restoreAddressMemberships(memberships map[string]*ConversationMembership, snapshots []AddressLifecycleMembershipSnapshot, ts string) {
	for _, snapshot := range snapshots {
		membership := memberships[snapshot.ID]
		if membership == nil {
			continue
		}
		membership.Enabled = snapshot.Enabled
		membership.ArchivedAt = snapshot.ArchivedAt
		membership.SupersededBy = snapshot.SupersededBy
		membership.UpdatedAt = ts
		membership.Version++
	}
}

func markAddressCandidatesUnavailable(candidates map[string]*ConversationCandidate, addressID, ts string) {
	for _, candidate := range candidates {
		if candidate != nil && candidate.AddressID == addressID && candidate.Available {
			candidate.Available = false
			candidate.UpdatedAt = ts
		}
	}
}

func (h *Hub) commitAddressLifecycleLocked(addresses map[string]*AgentAddress, memberships map[string]*ConversationMembership, candidates map[string]*ConversationCandidate, operations map[string]*AddressLifecycleOperation) error {
	previousAddresses, previousMemberships := h.addresses, h.memberships
	previousCandidates, previousOperations := h.conversationCandidates, h.addressOperations
	h.addresses, h.memberships = addresses, memberships
	h.conversationCandidates, h.addressOperations = candidates, operations
	if err := h.persistIntegrationsLocked(); err != nil {
		h.addresses, h.memberships = previousAddresses, previousMemberships
		h.conversationCandidates, h.addressOperations = previousCandidates, previousOperations
		return errf(500, "save address lifecycle operation: %s", err)
	}
	return nil
}

func cloneAddressLifecycleOperations(values map[string]*AddressLifecycleOperation) map[string]*AddressLifecycleOperation {
	out := make(map[string]*AddressLifecycleOperation, len(values))
	for id, value := range values {
		if value == nil {
			continue
		}
		copy := cloneAddressLifecycleOperation(*value)
		out[id] = &copy
	}
	return out
}

func cloneAddressLifecycleOperation(value AddressLifecycleOperation) AddressLifecycleOperation {
	value.MembershipsBefore = append([]AddressLifecycleMembershipSnapshot(nil), value.MembershipsBefore...)
	return value
}

func sortLifecyclePlan(plan *AddressLifecyclePreflight) {
	sort.Slice(plan.Blockers, func(i, j int) bool {
		if plan.Blockers[i].Kind != plan.Blockers[j].Kind {
			return plan.Blockers[i].Kind < plan.Blockers[j].Kind
		}
		return plan.Blockers[i].ID < plan.Blockers[j].ID
	})
	sort.Strings(plan.Warnings)
}
