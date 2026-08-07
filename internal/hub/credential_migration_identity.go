package hub

import (
	"sort"
	"strings"
)

// CredentialMigrationAddressSnapshot is non-secret internal coordination
// state. It freezes every Address binding attached to the migrating
// Connection so a provider or gateway cannot be verified against one identity
// and committed against another.
type CredentialMigrationAddressSnapshot struct {
	ID               string `json:"id"`
	AgentID          string `json:"agentId"`
	ConnectionID     string `json:"connectionId"`
	ExternalIdentity string `json:"externalIdentity"`
	Enabled          bool   `json:"enabled"`
	SupersededBy     string `json:"supersededBy,omitempty"`
	ArchivedAt       string `json:"archivedAt,omitempty"`
	DeletedAt        string `json:"deletedAt,omitempty"`
	Version          int    `json:"version"`
}

func credentialMigrationActiveState(state string) bool {
	switch state {
	case CredentialMigrationPreparing, CredentialMigrationCredentialStored,
		CredentialMigrationProviderVerified, CredentialMigrationGatewayActivating,
		CredentialMigrationGatewayVerified, CredentialMigrationSwitchingReference,
		CredentialMigrationRollingBack:
		return true
	default:
		return false
	}
}

func (h *Hub) activeCredentialMigrationLocked(connectionID string) *CredentialMigrationReceipt {
	connectionID = strings.TrimSpace(connectionID)
	for _, receipt := range h.credentialMigrations {
		if receipt != nil && receipt.ConnectionID == connectionID && credentialMigrationActiveState(receipt.State) {
			return receipt
		}
	}
	return nil
}

func credentialMigrationInProgressError() error {
	return errf(409, "credential_migration_in_progress")
}

// RequireCredentialMigrationsIdle lets provider-specific operator flows share
// the same durable per-Connection reservation as migration and rollback. The
// caller should hold its process-local Connection lock for the full operation;
// Hub mutation paths still repeat this check under h.mu before committing.
func (h *Hub) RequireCredentialMigrationsIdle(connectionIDs ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, connectionID := range connectionIDs {
		if h.activeCredentialMigrationLocked(connectionID) != nil {
			return credentialMigrationInProgressError()
		}
	}
	return nil
}

func (h *Hub) credentialMigrationAddressSnapshotsLocked(connectionID string) []CredentialMigrationAddressSnapshot {
	result := []CredentialMigrationAddressSnapshot{}
	for _, address := range h.addresses {
		if address == nil || address.ConnectionID != connectionID {
			continue
		}
		result = append(result, CredentialMigrationAddressSnapshot{
			ID: address.ID, AgentID: address.AgentID, ConnectionID: address.ConnectionID,
			ExternalIdentity: address.ExternalIdentity, Enabled: address.Enabled,
			SupersededBy: address.SupersededBy, ArchivedAt: address.ArchivedAt,
			DeletedAt: address.DeletedAt, Version: address.Version,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (h *Hub) initializeCredentialMigrationIdentityLocked(receipt *CredentialMigrationReceipt, connection *PlatformConnection) {
	receipt.Provider = connection.Provider
	receipt.AccountRef = connection.AccountRef
	receipt.ScopeRef = connection.ScopeRef
	receipt.PreviousCredentialRef = connection.CredentialRef
	receipt.ConnectionRevision = h.connectionControlVersions[connection.ID]
	receipt.AddressSnapshots = h.credentialMigrationAddressSnapshotsLocked(connection.ID)
}

func validCredentialMigrationIdentitySnapshot(receipt CredentialMigrationReceipt) bool {
	if receipt.ConnectionRevision < 1 || receipt.Provider == "" || receipt.PreviousCredentialRef == "" {
		return false
	}
	previousID := ""
	for _, snapshot := range receipt.AddressSnapshots {
		if snapshot.ID == "" || snapshot.ConnectionID != receipt.ConnectionID || snapshot.Version < 1 || snapshot.ID <= previousID {
			return false
		}
		previousID = snapshot.ID
	}
	return true
}

// MatchCredentialMigrationIdentity proves the receipt still owns the exact
// Connection and Address binding it froze. allowedCredentialRefs accounts only
// for the migration-owned canonical reference transition.
func (h *Hub) MatchCredentialMigrationIdentity(receipt CredentialMigrationReceipt, allowedCredentialRefs ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.matchCredentialMigrationIdentityLocked(receipt, allowedCredentialRefs...)
}

func (h *Hub) matchCredentialMigrationIdentityLocked(receipt CredentialMigrationReceipt, allowedCredentialRefs ...string) error {
	if !validCredentialMigrationIdentitySnapshot(receipt) {
		return errf(409, "credential migration identity snapshot is unavailable")
	}
	connection := h.connections[receipt.ConnectionID]
	if connection == nil {
		return errf(404, "connection not found: %s", receipt.ConnectionID)
	}
	if connection.Provider != receipt.Provider || connection.AccountRef != receipt.AccountRef ||
		connection.ScopeRef != receipt.ScopeRef || h.connectionControlVersions[connection.ID] != receipt.ConnectionRevision {
		return errf(409, "credential migration connection identity changed")
	}
	allowed := map[string]bool{}
	for _, reference := range allowedCredentialRefs {
		allowed[strings.TrimSpace(reference)] = true
	}
	if len(allowed) == 0 {
		allowed[receipt.PreviousCredentialRef] = true
	}
	if !allowed[connection.CredentialRef] {
		return errf(409, "credential migration canonical reference changed")
	}
	currentAddresses := h.credentialMigrationAddressSnapshotsLocked(connection.ID)
	if len(currentAddresses) != len(receipt.AddressSnapshots) {
		return errf(409, "credential migration Address binding changed")
	}
	for index := range currentAddresses {
		if currentAddresses[index] != receipt.AddressSnapshots[index] {
			return errf(409, "credential migration Address binding changed")
		}
	}
	return nil
}

func connectionControlChanged(previous, next PlatformConnection) bool {
	return previous.Provider != next.Provider || previous.AccountRef != next.AccountRef ||
		previous.ScopeRef != next.ScopeRef || previous.CredentialRef != next.CredentialRef ||
		previous.Enabled != next.Enabled || previous.SupersededBy != next.SupersededBy || previous.ArchivedAt != next.ArchivedAt
}

func (h *Hub) incrementConnectionControlVersionLocked(connectionID string) {
	if h.connectionControlVersions[connectionID] < 1 {
		h.connectionControlVersions[connectionID] = 1
	}
	h.connectionControlVersions[connectionID]++
}

func cloneConnectionControlVersions(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for id, version := range values {
		result[id] = version
	}
	return result
}
