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
	Status      string `json:"status"`
	Manager     string `json:"manager,omitempty"`
	Service     string `json:"service,omitempty"`
	Build       string `json:"build,omitempty"`
	AnchorID    string `json:"anchorId,omitempty"`
	HeartbeatAt string `json:"heartbeatAt,omitempty"`
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
	ID                    string                              `json:"id"`
	ConnectionID          string                              `json:"connectionId"`
	Provider              string                              `json:"provider"`
	State                 string                              `json:"state"`
	PreviousCredentialRef string                              `json:"previousCredentialRef"`
	TargetCredentialRef   string                              `json:"targetCredentialRef,omitempty"`
	ProviderReceipt       *CredentialMigrationProviderReceipt `json:"providerReceipt,omitempty"`
	GatewayReceipt        *CredentialMigrationGatewayReceipt  `json:"gatewayReceipt,omitempty"`
	Error                 *CredentialMigrationError           `json:"error,omitempty"`
	CredentialsIncluded   bool                                `json:"credentialsIncluded"`
	RunnableRestore       bool                                `json:"runnableRestore"`
	CreatedAt             string                              `json:"createdAt"`
	UpdatedAt             string                              `json:"updatedAt"`
	Version               int                                 `json:"version"`
}

func (h *Hub) loadCredentialMigrations() error {
	if err := h.st.LoadCredentialMigrations(&h.credentialMigrations); err != nil {
		return err
	}
	if h.credentialMigrations == nil {
		h.credentialMigrations = map[string]*CredentialMigrationReceipt{}
	}
	connections := map[string]string{}
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
		if prior := connections[receipt.ConnectionID]; prior != "" && prior != id {
			return fmt.Errorf("multiple credential migration receipts for connection %s", receipt.ConnectionID)
		}
		connections[receipt.ConnectionID] = id
	}
	return nil
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
	for _, receipt := range h.credentialMigrations {
		if receipt != nil && receipt.ConnectionID == connection.ID {
			return cloneCredentialMigrationReceipt(*receipt), false, nil
		}
	}
	timestamp := now()
	receipt := CredentialMigrationReceipt{
		ID: newIntegrationID("cmig"), ConnectionID: connection.ID, Provider: connection.Provider,
		State: CredentialMigrationPreparing, PreviousCredentialRef: connection.CredentialRef,
		CredentialsIncluded: false, RunnableRestore: false,
		CreatedAt: timestamp, UpdatedAt: timestamp, Version: 1,
	}
	h.credentialMigrations[receipt.ID] = &receipt
	if err := h.persistCredentialMigrationsLocked(); err != nil {
		delete(h.credentialMigrations, receipt.ID)
		return CredentialMigrationReceipt{}, false, errf(500, "save credential migration: %s", err)
	}
	h.emitGlobalLocked("loom/credential-migration", map[string]any{"receipt": credentialMigrationPublicView(receipt)})
	return cloneCredentialMigrationReceipt(receipt), true, nil
}

func (h *Hub) SaveCredentialMigration(next CredentialMigrationReceipt, expectedVersion int) (CredentialMigrationReceipt, error) {
	if next.ID != strings.TrimSpace(next.ID) || !validCredentialMigrationID(next.ID) || !validCredentialMigrationState(next.State) {
		return CredentialMigrationReceipt{}, errf(400, "invalid credential migration receipt")
	}
	if next.TargetCredentialRef != "" {
		if _, err := credentialstore.ParseReference(next.TargetCredentialRef); err != nil {
			return CredentialMigrationReceipt{}, errf(400, "invalid managed credential migration target")
		}
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
	if next.ID != current.ID || next.ConnectionID != current.ConnectionID || next.Provider != current.Provider || next.PreviousCredentialRef != current.PreviousCredentialRef || next.CreatedAt != current.CreatedAt {
		return CredentialMigrationReceipt{}, errf(409, "credential migration receipt identity is immutable")
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
	if value.ProviderReceipt != nil {
		copy := *value.ProviderReceipt
		value.ProviderReceipt = &copy
	}
	if value.GatewayReceipt != nil {
		copy := *value.GatewayReceipt
		value.GatewayReceipt = &copy
	}
	if value.Error != nil {
		copy := *value.Error
		value.Error = &copy
	}
	return value
}

func credentialMigrationPublicView(receipt CredentialMigrationReceipt) map[string]any {
	return map[string]any{
		"id": receipt.ID, "connectionId": receipt.ConnectionID, "provider": receipt.Provider,
		"state": receipt.State, "credentialsIncluded": false, "runnableRestore": false,
		"createdAt": receipt.CreatedAt, "updatedAt": receipt.UpdatedAt, "version": receipt.Version,
	}
}
