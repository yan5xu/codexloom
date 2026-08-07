package hub

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
)

const (
	CredentialMigrationPreparing              = "preparing"
	CredentialMigrationCredentialStored       = "credential_stored"
	CredentialMigrationProviderVerified       = "provider_verified"
	CredentialMigrationGatewayActivating      = "gateway_activating"
	CredentialMigrationGatewayVerified        = "gateway_verified"
	CredentialMigrationSwitchingReference     = "switching_reference"
	CredentialMigrationCompleted              = "completed"
	CredentialMigrationRollingBack            = "rolling_back"
	CredentialMigrationRolledBack             = "rolled_back"
	CredentialMigrationFailed                 = "failed"
	CredentialMigrationManualRecoveryRequired = "manual_recovery_required"
)

type CredentialMigrationProviderReceipt struct {
	Status     string `json:"status"`
	Subject    string `json:"subject,omitempty"`
	ObservedAt string `json:"observedAt,omitempty"`
}

type CredentialMigrationGatewayReceipt struct {
	Status           string `json:"status"`
	Manager          string `json:"manager,omitempty"`
	Service          string `json:"service,omitempty"`
	Build            string `json:"build,omitempty"`
	ExecutableSHA256 string `json:"executableSha256,omitempty"`
	Generation       string `json:"generation,omitempty"`
	AnchorID         string `json:"anchorId,omitempty"`
	HeartbeatAt      string `json:"heartbeatAt,omitempty"`
}

type CredentialMigrationError struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CredentialMigrationReceipt is non-secret durable coordination state. The
// credential files and rollback anchors it refers to remain under the excluded
// owner-only credentials directory.
type CredentialMigrationReceipt struct {
	ID                     string                               `json:"id"`
	ConnectionID           string                               `json:"connectionId"`
	Provider               string                               `json:"provider"`
	AccountRef             string                               `json:"accountRef,omitempty"`
	ScopeRef               string                               `json:"scopeRef,omitempty"`
	ConnectionRevision     int                                  `json:"connectionRevision,omitempty"`
	AddressSnapshots       []CredentialMigrationAddressSnapshot `json:"addressSnapshots,omitempty"`
	State                  string                               `json:"state"`
	PreviousCredentialRef  string                               `json:"previousCredentialRef"`
	TargetCredentialRef    string                               `json:"targetCredentialRef,omitempty"`
	ProviderReceipt        *CredentialMigrationProviderReceipt  `json:"providerReceipt,omitempty"`
	GatewayReceipt         *CredentialMigrationGatewayReceipt   `json:"gatewayReceipt,omitempty"`
	GatewayEffectID        string                               `json:"gatewayEffectId,omitempty"`
	GatewayEffectState     string                               `json:"gatewayEffectState,omitempty"`
	GatewayEffectAttempt   int                                  `json:"gatewayEffectAttempt,omitempty"`
	RollbackGatewayReceipt *CredentialMigrationGatewayReceipt   `json:"rollbackGatewayReceipt,omitempty"`
	RollbackEffectID       string                               `json:"rollbackEffectId,omitempty"`
	RollbackEffectState    string                               `json:"rollbackEffectState,omitempty"`
	RollbackEffectAttempt  int                                  `json:"rollbackEffectAttempt,omitempty"`
	Error                  *CredentialMigrationError            `json:"error,omitempty"`
	CredentialsIncluded    bool                                 `json:"credentialsIncluded"`
	RunnableRestore        bool                                 `json:"runnableRestore"`
	CreatedAt              string                               `json:"createdAt"`
	UpdatedAt              string                               `json:"updatedAt"`
	Version                int                                  `json:"version"`
}

func (h *Hub) loadCredentialMigrations(persistRecovery bool) error {
	if err := h.st.LoadCredentialMigrations(&h.credentialMigrations); err != nil {
		return err
	}
	if h.credentialMigrations == nil {
		h.credentialMigrations = map[string]*CredentialMigrationReceipt{}
	}
	connections := map[string]string{}
	changed := false
	for id, receipt := range h.credentialMigrations {
		if receipt == nil || receipt.ID != id || !validCredentialMigrationID(id) || strings.TrimSpace(receipt.ConnectionID) == "" ||
			!oneOf(strings.ToLower(strings.TrimSpace(receipt.Provider)), "lark", "feishu", "slack", "parall", "github") ||
			!validCredentialMigrationState(receipt.State) || receipt.Version < 1 || receipt.CreatedAt == "" || receipt.UpdatedAt == "" ||
			receipt.CredentialsIncluded || receipt.RunnableRestore || !strings.HasPrefix(receipt.PreviousCredentialRef, "keychain:") || strings.TrimSpace(strings.TrimPrefix(receipt.PreviousCredentialRef, "keychain:")) == "" {
			return fmt.Errorf("invalid credential migration receipt %s", id)
		}
		if receipt.TargetCredentialRef != "" {
			if _, err := credentialstore.ParseReference(receipt.TargetCredentialRef); err != nil {
				return fmt.Errorf("invalid credential migration target %s", id)
			}
		}
		if receipt.GatewayEffectID != "" && !validCredentialMigrationEffectID(receipt.GatewayEffectID) {
			return fmt.Errorf("invalid credential migration effect %s", id)
		}
		if !validCredentialMigrationEffectState(receipt.GatewayEffectState) {
			return fmt.Errorf("invalid credential migration effect state %s", id)
		}
		if receipt.GatewayEffectID != "" && receipt.GatewayEffectAttempt == 0 {
			receipt.GatewayEffectAttempt = 1
			changed = true
		}
		if receipt.GatewayEffectAttempt < 0 || (receipt.GatewayEffectAttempt == 0) != (receipt.GatewayEffectID == "") {
			return fmt.Errorf("invalid credential migration activation attempt %s", id)
		}
		if receipt.RollbackEffectID != "" && !validCredentialMigrationEffectID(receipt.RollbackEffectID) {
			return fmt.Errorf("invalid credential migration rollback effect %s", id)
		}
		if !validCredentialMigrationRollbackEffectState(receipt.RollbackEffectState) {
			return fmt.Errorf("invalid credential migration rollback effect state %s", id)
		}
		if receipt.RollbackEffectID != "" && receipt.RollbackEffectAttempt == 0 {
			receipt.RollbackEffectAttempt = 1
			changed = true
		}
		if receipt.RollbackEffectAttempt < 0 || (receipt.RollbackEffectAttempt == 0) != (receipt.RollbackEffectID == "") {
			return fmt.Errorf("invalid credential migration rollback attempt %s", id)
		}
		if !validCredentialMigrationIdentitySnapshot(*receipt) {
			if receipt.State != CredentialMigrationManualRecoveryRequired || receipt.Error == nil || receipt.Error.Code != "migration_identity_snapshot_unavailable" {
				receipt.State = CredentialMigrationManualRecoveryRequired
				receipt.Error = &CredentialMigrationError{
					Stage: "identity", Code: "migration_identity_snapshot_unavailable",
					Message: "Credential migration identity snapshot is unavailable; explicit recovery is required",
				}
				receipt.Version++
				receipt.UpdatedAt = now()
				changed = true
			}
		}
		if prior := connections[receipt.ConnectionID]; prior != "" && prior != id {
			return fmt.Errorf("multiple credential migration receipts for connection %s", receipt.ConnectionID)
		}
		connections[receipt.ConnectionID] = id
	}
	if changed && persistRecovery {
		return h.persistCredentialMigrationsLocked()
	}
	return nil
}

func validCredentialMigrationEffectID(value string) bool {
	if !strings.HasPrefix(value, "geff_") || len(value) > 100 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validCredentialMigrationEffectState(value string) bool {
	return oneOf(value, "", "activation_prepared", "activation_applied", "rollback_prepared", "rollback_applied")
}

func validCredentialMigrationRollbackEffectState(value string) bool {
	return oneOf(value, "", "rollback_prepared", "rollback_applied")
}

func validCredentialMigrationID(value string) bool {
	if !strings.HasPrefix(value, "cmig_") || len(value) > 100 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validCredentialMigrationState(state string) bool {
	switch state {
	case CredentialMigrationPreparing, CredentialMigrationCredentialStored, CredentialMigrationProviderVerified,
		CredentialMigrationGatewayActivating, CredentialMigrationGatewayVerified, CredentialMigrationSwitchingReference,
		CredentialMigrationCompleted, CredentialMigrationRollingBack, CredentialMigrationRolledBack,
		CredentialMigrationFailed, CredentialMigrationManualRecoveryRequired:
		return true
	default:
		return false
	}
}

func (h *Hub) BeginCredentialMigration(connection PlatformConnection) (CredentialMigrationReceipt, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.connections[strings.TrimSpace(connection.ID)]
	if current == nil {
		return CredentialMigrationReceipt{}, false, errf(404, "connection not found: %s", connection.ID)
	}
	if !current.Enabled || current.ArchivedAt != "" || !strings.HasPrefix(current.CredentialRef, "keychain:") {
		return CredentialMigrationReceipt{}, false, errf(409, "Connection is not an enabled, non-archived Keychain migration source")
	}
	for _, receipt := range h.credentialMigrations {
		if receipt != nil && receipt.ConnectionID == current.ID {
			return cloneCredentialMigrationReceipt(*receipt), false, nil
		}
	}
	timestamp := now()
	receipt := CredentialMigrationReceipt{
		ID: newIntegrationID("cmig"), ConnectionID: current.ID,
		State:               CredentialMigrationPreparing,
		CredentialsIncluded: false, RunnableRestore: false,
		CreatedAt: timestamp, UpdatedAt: timestamp, Version: 1,
	}
	h.initializeCredentialMigrationIdentityLocked(&receipt, current)
	h.credentialMigrations[receipt.ID] = &receipt
	if err := h.persistCredentialMigrationsLocked(); err != nil {
		delete(h.credentialMigrations, receipt.ID)
		return CredentialMigrationReceipt{}, false, errf(500, "save credential migration: %s", err)
	}
	h.emitGlobalLocked("loom/credential-migration", map[string]any{"receipt": credentialMigrationPublicView(receipt)})
	return cloneCredentialMigrationReceipt(receipt), true, nil
}

func (h *Hub) SaveCredentialMigration(next CredentialMigrationReceipt, expectedVersion int) (CredentialMigrationReceipt, error) {
	return h.saveCredentialMigration(next, expectedVersion, false, nil)
}

// SaveCredentialMigrationControlled atomically validates the frozen
// Connection/Address identity and persists the next receipt state. Once this
// stores an active state, ordinary identity mutation paths observe the same
// receipt under the Hub lock and return credential_migration_in_progress.
func (h *Hub) SaveCredentialMigrationControlled(next CredentialMigrationReceipt, expectedVersion int, allowedCredentialRefs ...string) (CredentialMigrationReceipt, error) {
	return h.saveCredentialMigration(next, expectedVersion, true, allowedCredentialRefs)
}

func (h *Hub) saveCredentialMigration(next CredentialMigrationReceipt, expectedVersion int, control bool, allowedCredentialRefs []string) (CredentialMigrationReceipt, error) {
	if next.ID != strings.TrimSpace(next.ID) || !validCredentialMigrationID(next.ID) || !validCredentialMigrationState(next.State) {
		return CredentialMigrationReceipt{}, errf(400, "invalid credential migration receipt")
	}
	if next.TargetCredentialRef != "" {
		if _, err := credentialstore.ParseReference(next.TargetCredentialRef); err != nil {
			return CredentialMigrationReceipt{}, errf(400, "invalid managed credential migration target")
		}
	}
	if next.GatewayEffectID != "" && !validCredentialMigrationEffectID(next.GatewayEffectID) || !validCredentialMigrationEffectState(next.GatewayEffectState) ||
		next.RollbackEffectID != "" && !validCredentialMigrationEffectID(next.RollbackEffectID) || !validCredentialMigrationRollbackEffectState(next.RollbackEffectState) {
		return CredentialMigrationReceipt{}, errf(400, "invalid credential migration gateway effect")
	}
	if next.GatewayEffectAttempt < 0 || (next.GatewayEffectAttempt == 0) != (next.GatewayEffectID == "") ||
		next.RollbackEffectAttempt < 0 || (next.RollbackEffectAttempt == 0) != (next.RollbackEffectID == "") {
		return CredentialMigrationReceipt{}, errf(400, "invalid credential migration gateway effect attempt")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.credentialMigrations[strings.TrimSpace(next.ID)]
	if current == nil {
		return CredentialMigrationReceipt{}, errf(404, "credential migration receipt not found")
	}
	if current.Version != expectedVersion {
		return CredentialMigrationReceipt{}, errf(409, "credential migration receipt changed; reload and retry")
	}
	if credentialMigrationActiveState(next.State) && !credentialMigrationActiveState(current.State) && !control {
		return CredentialMigrationReceipt{}, errf(409, "credential migration identity reservation is required")
	}
	if next.ID != current.ID || next.ConnectionID != current.ConnectionID || next.Provider != current.Provider ||
		next.AccountRef != current.AccountRef || next.ScopeRef != current.ScopeRef ||
		next.ConnectionRevision != current.ConnectionRevision || next.PreviousCredentialRef != current.PreviousCredentialRef ||
		next.CreatedAt != current.CreatedAt || !credentialMigrationAddressSnapshotsEqual(next.AddressSnapshots, current.AddressSnapshots) {
		return CredentialMigrationReceipt{}, errf(409, "credential migration receipt identity is immutable")
	}
	if current.TargetCredentialRef != "" && next.TargetCredentialRef != current.TargetCredentialRef {
		return CredentialMigrationReceipt{}, errf(409, "credential migration target reference is immutable")
	}
	if err := validateCredentialMigrationEffectAdvance(current.GatewayEffectID, current.GatewayEffectAttempt, next.GatewayEffectID, next.GatewayEffectAttempt); err != nil {
		return CredentialMigrationReceipt{}, err
	}
	if err := validateCredentialMigrationEffectAdvance(current.RollbackEffectID, current.RollbackEffectAttempt, next.RollbackEffectID, next.RollbackEffectAttempt); err != nil {
		return CredentialMigrationReceipt{}, err
	}
	if current.GatewayEffectAttempt == next.GatewayEffectAttempt && current.GatewayEffectAttempt > 0 &&
		!credentialMigrationGatewayTargetEqual(current.GatewayReceipt, next.GatewayReceipt) {
		return CredentialMigrationReceipt{}, errf(409, "credential migration activation target is immutable")
	}
	if current.RollbackEffectAttempt == next.RollbackEffectAttempt && current.RollbackEffectAttempt > 0 &&
		!credentialMigrationGatewayTargetEqual(current.RollbackGatewayReceipt, next.RollbackGatewayReceipt) {
		return CredentialMigrationReceipt{}, errf(409, "credential migration rollback target is immutable")
	}
	if control {
		if err := h.matchCredentialMigrationIdentityLocked(*current, allowedCredentialRefs...); err != nil {
			return CredentialMigrationReceipt{}, err
		}
	}
	previous := *current
	next.CredentialsIncluded = false
	next.RunnableRestore = false
	next.Version = current.Version + 1
	next.UpdatedAt = now()
	h.credentialMigrations[next.ID] = &next
	if err := h.persistCredentialMigrationsLocked(); err != nil {
		h.credentialMigrations[next.ID] = &previous
		return CredentialMigrationReceipt{}, errf(500, "save credential migration: %s", err)
	}
	h.emitGlobalLocked("loom/credential-migration", map[string]any{"receipt": credentialMigrationPublicView(next)})
	return cloneCredentialMigrationReceipt(next), nil
}

func (h *Hub) GetCredentialMigration(id string) (CredentialMigrationReceipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	receipt := h.credentialMigrations[strings.TrimSpace(id)]
	if receipt == nil {
		return CredentialMigrationReceipt{}, errf(404, "credential migration receipt not found")
	}
	return cloneCredentialMigrationReceipt(*receipt), nil
}

func (h *Hub) GetCredentialMigrationForConnection(connectionID string) (CredentialMigrationReceipt, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, receipt := range h.credentialMigrations {
		if receipt != nil && receipt.ConnectionID == strings.TrimSpace(connectionID) {
			return cloneCredentialMigrationReceipt(*receipt), true
		}
	}
	return CredentialMigrationReceipt{}, false
}

func (h *Hub) ListCredentialMigrations() []CredentialMigrationReceipt {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]CredentialMigrationReceipt, 0, len(h.credentialMigrations))
	for _, receipt := range h.credentialMigrations {
		if receipt != nil {
			result = append(result, cloneCredentialMigrationReceipt(*receipt))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt < result[j].CreatedAt })
	return result
}

func credentialMigrationTerminal(state string) bool {
	switch state {
	case CredentialMigrationCompleted, CredentialMigrationRolledBack, CredentialMigrationFailed, CredentialMigrationManualRecoveryRequired:
		return true
	default:
		return false
	}
}

func (h *Hub) persistCredentialMigrationsLocked() error {
	return h.st.SaveCredentialMigrations(h.credentialMigrations)
}

func cloneCredentialMigrationReceipt(value CredentialMigrationReceipt) CredentialMigrationReceipt {
	value.AddressSnapshots = append([]CredentialMigrationAddressSnapshot(nil), value.AddressSnapshots...)
	if value.ProviderReceipt != nil {
		copy := *value.ProviderReceipt
		value.ProviderReceipt = &copy
	}
	if value.GatewayReceipt != nil {
		copy := *value.GatewayReceipt
		value.GatewayReceipt = &copy
	}
	if value.RollbackGatewayReceipt != nil {
		copy := *value.RollbackGatewayReceipt
		value.RollbackGatewayReceipt = &copy
	}
	if value.Error != nil {
		copy := *value.Error
		value.Error = &copy
	}
	return value
}

func validateCredentialMigrationEffectAdvance(currentID string, currentAttempt int, nextID string, nextAttempt int) error {
	if nextAttempt < currentAttempt || nextAttempt > currentAttempt+1 {
		return errf(409, "credential migration gateway effect attempt is invalid")
	}
	if nextAttempt == currentAttempt && currentID != nextID {
		return errf(409, "credential migration gateway effect identity is immutable")
	}
	if nextAttempt == currentAttempt+1 && (nextID == "" || nextID == currentID) {
		return errf(409, "credential migration gateway effect attempt identity must be new")
	}
	return nil
}

func credentialMigrationAddressSnapshotsEqual(left, right []CredentialMigrationAddressSnapshot) bool {
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

func credentialMigrationGatewayTargetEqual(left, right *CredentialMigrationGatewayReceipt) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Manager == right.Manager && left.Service == right.Service && left.Build == right.Build &&
		left.ExecutableSHA256 == right.ExecutableSHA256 && left.Generation == right.Generation && left.AnchorID == right.AnchorID
}

func credentialMigrationPublicView(receipt CredentialMigrationReceipt) map[string]any {
	return map[string]any{
		"id": receipt.ID, "connectionId": receipt.ConnectionID, "provider": receipt.Provider,
		"state": receipt.State, "credentialsIncluded": false, "runnableRestore": false,
		"createdAt": receipt.CreatedAt, "updatedAt": receipt.UpdatedAt, "version": receipt.Version,
	}
}
