package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/feishu"
	githubapi "github.com/yan5xu/codex-loom/internal/github"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/parall"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

type credentialMigrationPreflight struct {
	ConnectionID        string `json:"connectionId"`
	Provider            string `json:"provider"`
	CredentialScheme    string `json:"credentialScheme"`
	Eligible            bool   `json:"eligible"`
	Status              string `json:"status"`
	ExistingReceiptID   string `json:"existingReceiptId,omitempty"`
	CredentialsIncluded bool   `json:"credentialsIncluded"`
}

type credentialMigrationMaterial struct {
	Binding credentialstoreBinding
	Payload credentialstore.Payload
	Subject string
}

type credentialstoreBinding string

type credentialMigrationRequest struct {
	DryRun  bool   `json:"dryRun"`
	Confirm string `json:"confirm"`
}

var loadCredentialMigrationSource = func(s *Server, connection hub.PlatformConnection) (credentialMigrationMaterial, error) {
	return s.loadKeychainMigrationSource(connection)
}

var verifyCredentialMigrationProvider = func(ctx context.Context, s *Server, connection hub.PlatformConnection, material credentialMigrationMaterial) (hub.CredentialMigrationProviderReceipt, error) {
	return s.verifyMigrationProvider(ctx, connection, material)
}

var activateCredentialMigrationGateway = func(ctx context.Context, s *Server, connection hub.PlatformConnection, targetRef, receiptID, hubURL string, prepared hub.CredentialMigrationGatewayReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	return s.activateMigrationGateway(ctx, connection, targetRef, receiptID, hubURL, prepared)
}

var rollbackCredentialMigrationGateway = func(ctx context.Context, s *Server, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	return s.rollbackMigrationGateway(ctx, connection, receipt)
}

var preflightCredentialMigrationGateway = func(s *Server, connection hub.PlatformConnection) error {
	return s.preflightMigrationGateway(connection)
}

var prepareCredentialMigrationGatewayEffect = func(s *Server, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
	return s.prepareMigrationGatewayEffect(connection, receipt)
}

var prepareCredentialMigrationRollbackEffect = func(s *Server, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
	return s.prepareMigrationGatewayRollbackEffect(connection, receipt)
}

var reconcileCredentialMigrationGatewayEffect = func(ctx context.Context, s *Server, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt, rollback bool) (hub.CredentialMigrationGatewayReceipt, bool, error) {
	return s.reconcileMigrationGatewayEffect(ctx, connection, receipt, rollback)
}

var preflightCredentialMigrationRollback = func(s *Server, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) error {
	return s.preflightMigrationGatewayRollback(connection, receipt)
}

type credentialMigrationRollbackValidation struct {
	Status string
	Reason string
}

func (s *Server) preflightCredentialMigrations(ctx context.Context, connectionID string) ([]credentialMigrationPreflight, error) {
	connectionID = strings.TrimSpace(connectionID)
	connections := s.hub.ListConnections()
	if connectionID != "" {
		found := false
		for _, connection := range connections {
			if connection.ID == connectionID {
				connections = []hub.PlatformConnection{connection}
				found = true
				break
			}
		}
		if !found {
			return nil, &hub.HubError{Status: 404, Message: "Connection not found: " + connectionID}
		}
	}
	results := make([]credentialMigrationPreflight, 0, len(connections))
	for _, connection := range connections {
		if connectionID == "" && (!connection.Enabled || connection.ArchivedAt != "" || !strings.HasPrefix(connection.CredentialRef, "keychain:")) {
			continue
		}
		item := credentialMigrationPreflight{
			ConnectionID: connection.ID, Provider: connection.Provider,
			CredentialScheme: credentialReferenceScheme(connection.CredentialRef),
			Status:           "not_eligible", CredentialsIncluded: false,
		}
		if receipt, exists := s.hub.GetCredentialMigrationForConnection(connection.ID); exists {
			item.ExistingReceiptID = receipt.ID
		}
		switch {
		case connection.ArchivedAt != "":
			item.Status = "archived"
		case !connection.Enabled:
			item.Status = "disabled"
		case strings.HasPrefix(connection.CredentialRef, credentialstore.ManagedReferencePrefix):
			item.Status = "already_managed"
		case strings.HasPrefix(connection.CredentialRef, "env:"):
			item.Status = "environment_reference_not_auto_migrated"
		case !strings.HasPrefix(connection.CredentialRef, "keychain:"):
			item.Status = "unsupported_reference"
		default:
			if _, err := s.hub.CredentialStore(); err != nil {
				item.Status = "managed_store_unsafe"
			} else if err := verifyManagedCredentialWriteFloor(s); err != nil {
				item.Status = "credential_rollback_build_floor_unavailable"
			} else if _, err := loadCredentialMigrationSource(s, connection); err != nil {
				item.Status = "source_unavailable"
			} else if err := preflightCredentialMigrationGateway(s, connection); err != nil {
				item.Status = "gateway_not_reversibly_managed"
			} else {
				item.Eligible = true
				item.Status = "ready"
			}
		}
		results = append(results, item)
	}
	return results, nil
}

func (s *Server) migrateCredential(ctx context.Context, connectionID string, request credentialMigrationRequest, hubURL string) (map[string]any, error) {
	if !request.DryRun {
		unlock := s.lockCredentialMigration("connection:" + strings.TrimSpace(connectionID))
		defer unlock()
	}
	connection, err := s.migrationConnection(connectionID)
	if err != nil {
		return nil, err
	}
	if request.DryRun {
		items, err := s.preflightCredentialMigrations(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"dryRun": true, "preflight": items[0], "credentialsIncluded": false,
			"runnableRestore": false, "backupStatus": "credentials_excluded",
		}, nil
	}
	if strings.TrimSpace(request.Confirm) != connection.ID {
		return nil, &hub.HubError{Status: 400, Message: "Confirm this migration with the exact Connection ID"}
	}

	receipt, found := s.hub.GetCredentialMigrationForConnection(connection.ID)
	if found {
		if !connection.Enabled || connection.ArchivedAt != "" {
			switch receipt.State {
			case hub.CredentialMigrationCompleted, hub.CredentialMigrationManualRecoveryRequired, hub.CredentialMigrationRollingBack:
			default:
				return nil, &hub.HubError{Status: 409, Message: "Credential migration requires an enabled, non-archived Connection"}
			}
		}
		switch receipt.State {
		case hub.CredentialMigrationCompleted:
			if connection.CredentialRef != receipt.TargetCredentialRef {
				receipt = s.manualCredentialRecovery(receipt, "reference", "completed_reference_drift", "Completed migration no longer matches the canonical credential reference")
			}
			return credentialMigrationReceiptView(receipt), nil
		case hub.CredentialMigrationManualRecoveryRequired:
			return credentialMigrationReceiptView(receipt), nil
		case hub.CredentialMigrationRolledBack:
			if connection.CredentialRef != receipt.PreviousCredentialRef {
				receipt = s.manualCredentialRecovery(receipt, "reference", "rolled_back_reference_drift", "Rolled-back migration no longer matches its previous credential reference")
				return credentialMigrationReceiptView(receipt), nil
			}
			receipt.State = hub.CredentialMigrationPreparing
			receipt.ProviderReceipt = nil
			receipt.Error = nil
			receipt = s.saveCredentialMigrationControlled(receipt, receipt.PreviousCredentialRef)
			if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
				return credentialMigrationReceiptView(receipt), nil
			}
		case hub.CredentialMigrationRollingBack:
			receipt = s.continueCredentialMigrationRollback(ctx, receipt, false)
			return credentialMigrationReceiptView(receipt), nil
		case hub.CredentialMigrationFailed:
			if connection.CredentialRef != receipt.PreviousCredentialRef {
				receipt = s.manualCredentialRecovery(receipt, "reference", "failed_reference_drift", "Failed migration no longer matches its previous credential reference")
				return credentialMigrationReceiptView(receipt), nil
			}
			if receipt.TargetCredentialRef == "" {
				receipt.State = hub.CredentialMigrationPreparing
			} else {
				receipt.State = hub.CredentialMigrationCredentialStored
			}
			receipt.Error = nil
			receipt = s.saveCredentialMigrationControlled(receipt, receipt.PreviousCredentialRef)
			if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
				return credentialMigrationReceiptView(receipt), nil
			}
		}
	} else {
		if !connection.Enabled || connection.ArchivedAt != "" || !strings.HasPrefix(connection.CredentialRef, "keychain:") {
			return nil, &hub.HubError{Status: 409, Message: "Connection is not an enabled, non-archived Keychain migration source"}
		}
		receipt, _, err = s.hub.BeginCredentialMigration(connection)
		if err != nil {
			return nil, err
		}
	}
	receipt = s.resumeCredentialMigration(ctx, connection, receipt, hubURL)
	return credentialMigrationReceiptView(receipt), nil
}

func (s *Server) resumeCredentialMigration(ctx context.Context, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt, hubURL string) hub.CredentialMigrationReceipt {
	executeActivation := false
	current, err := s.migrationConnection(connection.ID)
	if err != nil {
		return s.manualCredentialRecovery(receipt, "connection", "connection_unavailable", "Connection is unavailable while resuming migration")
	}
	if receipt.State == hub.CredentialMigrationSwitchingReference && receipt.TargetCredentialRef != "" && current.CredentialRef == receipt.TargetCredentialRef {
		if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.TargetCredentialRef); err != nil {
			return s.manualCredentialRecovery(receipt, "identity", "migration_identity_changed", "Credential migration identity changed before canonical completion")
		}
		receipt.State = hub.CredentialMigrationCompleted
		receipt.Error = nil
		return s.saveCredentialMigration(receipt)
	}
	if current.CredentialRef != receipt.PreviousCredentialRef {
		return s.manualCredentialRecovery(receipt, "reference", "active_reference_drift", "Active migration no longer matches its previous credential reference")
	}
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef); err != nil {
		return s.manualCredentialRecovery(receipt, "identity", "migration_identity_changed", "Credential migration Connection or Address identity changed")
	}

	var material credentialMigrationMaterial
	if receipt.State == hub.CredentialMigrationPreparing {
		if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef); err != nil {
			return s.manualCredentialRecovery(receipt, "identity", "migration_identity_changed", "Credential migration identity changed before credential storage")
		}
		if err := preflightCredentialMigrationGateway(s, current); err != nil {
			return s.failCredentialMigration(receipt, "gateway_preflight", "gateway_not_reversibly_managed", "Gateway cannot be safely activated and rolled back")
		}
		if err := s.requireManagedCredentialWriteFloor(); err != nil {
			return s.failCredentialMigration(receipt, "backup", "credential_rollback_build_floor_unavailable", "A verified credential-excluding rollback build is required before managed credential storage")
		}
		material, err = loadCredentialMigrationSource(s, current)
		if err != nil {
			return s.failCredentialMigration(receipt, "source", "source_unavailable", "Keychain credential source is unavailable")
		}
		credentials, storeErr := s.hub.CredentialStore()
		if storeErr != nil {
			return s.failCredentialMigration(receipt, "store", "managed_store_unsafe", "Managed credential store is unavailable")
		}
		targetRef, _, storeErr := credentials.PutBound(string(material.Binding), material.Payload)
		if storeErr != nil {
			return s.failCredentialMigration(receipt, "store", "credential_write_failed", "Managed credential write or verification failed")
		}
		if receipt.TargetCredentialRef != "" && receipt.TargetCredentialRef != targetRef {
			return s.manualCredentialRecovery(receipt, "store", "credential_identity_changed", "Managed credential identity changed while resuming migration")
		}
		receipt.TargetCredentialRef = targetRef
		receipt.State = hub.CredentialMigrationCredentialStored
		receipt.Error = nil
		receipt = s.saveCredentialMigration(receipt)
		if credentialMigrationPersistenceFailed(receipt) {
			return receipt
		}
	}

	if receipt.State == hub.CredentialMigrationCredentialStored {
		if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef); err != nil {
			return s.manualCredentialRecovery(receipt, "identity", "migration_identity_changed", "Credential migration identity changed before provider verification")
		}
		if material.Payload.Values == nil {
			material, err = s.loadManagedMigrationMaterial(current, receipt.TargetCredentialRef)
			if err != nil {
				return s.manualCredentialRecovery(receipt, "store", "stored_credential_unavailable", "Stored managed credential could not be validated while resuming migration")
			}
		}
		providerReceipt, verifyErr := verifyCredentialMigrationProvider(ctx, s, current, material)
		if verifyErr != nil {
			return s.failCredentialMigration(receipt, "provider", "provider_verification_failed", "Provider credential verification failed")
		}
		receipt.ProviderReceipt = &providerReceipt
		receipt.State = hub.CredentialMigrationProviderVerified
		receipt.Error = nil
		receipt = s.saveCredentialMigration(receipt)
		if credentialMigrationPersistenceFailed(receipt) {
			return receipt
		}
	}

	if receipt.State == hub.CredentialMigrationProviderVerified {
		effectID, prepared, prepareErr := prepareCredentialMigrationGatewayEffect(s, current, receipt)
		if prepareErr != nil {
			return s.failCredentialMigration(receipt, "gateway_preflight", "gateway_effect_prepare_failed", "Gateway activation effect could not be durably prepared")
		}
		receipt.GatewayEffectID = effectID
		receipt.GatewayEffectAttempt++
		receipt.GatewayEffectState = "activation_prepared"
		receipt.GatewayReceipt = &prepared
		receipt.State = hub.CredentialMigrationGatewayActivating
		receipt = s.saveCredentialMigrationControlled(receipt, receipt.PreviousCredentialRef)
		if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
			return receipt
		}
		executeActivation = true
	}
	if receipt.State == hub.CredentialMigrationGatewayActivating {
		if !executeActivation {
			reconciled, proven, reconcileErr := reconcileCredentialMigrationGatewayEffect(ctx, s, current, receipt, false)
			reconciled = preserveMigrationGatewayEffectTarget(receipt.GatewayReceipt, reconciled)
			if reconcileErr != nil {
				return s.manualCredentialRecovery(receipt, "gateway", "gateway_effect_reconcile_failed", "Gateway activation effect could not be reconciled")
			}
			if !proven {
				return s.manualCredentialRecovery(receipt, "gateway", "gateway_effect_indeterminate", "Gateway activation may have occurred; explicit reconciliation is required before retry")
			}
			receipt.GatewayReceipt = &reconciled
			receipt.GatewayEffectState = "activation_applied"
			receipt.State = hub.CredentialMigrationGatewayVerified
			receipt = s.saveCredentialMigration(receipt)
			if credentialMigrationPersistenceFailed(receipt) {
				return receipt
			}
		} else {
			gatewayReceipt, activationErr := activateCredentialMigrationGateway(ctx, s, current, receipt.TargetCredentialRef, receipt.ID, hubURL, *receipt.GatewayReceipt)
			gatewayReceipt = preserveMigrationGatewayEffectTarget(receipt.GatewayReceipt, gatewayReceipt)
			receipt.GatewayReceipt = &gatewayReceipt
			if activationErr != nil {
				return s.rollbackFailedCredentialMigration(ctx, receipt, "gateway", "gateway_activation_failed", "Gateway activation or heartbeat verification failed")
			}
			receipt.GatewayEffectState = "activation_applied"
			receipt.State = hub.CredentialMigrationGatewayVerified
			receipt = s.saveCredentialMigration(receipt)
			if credentialMigrationPersistenceFailed(receipt) {
				return receipt
			}
		}
	}

	if receipt.State == hub.CredentialMigrationGatewayVerified {
		receipt.State = hub.CredentialMigrationSwitchingReference
		receipt = s.saveCredentialMigrationControlled(receipt, receipt.PreviousCredentialRef)
		if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
			return receipt
		}
	}
	if receipt.State == hub.CredentialMigrationSwitchingReference {
		var err error
		if _, receipt, err = s.hub.CompareAndSwapConnectionCredentialForMigration(receipt.ID, receipt.PreviousCredentialRef, receipt.TargetCredentialRef); err != nil {
			return s.rollbackFailedCredentialMigration(ctx, receipt, "reference", "credential_reference_conflict", "Canonical credential reference changed concurrently")
		}
		receipt.State = hub.CredentialMigrationCompleted
		receipt.Error = nil
		return s.saveCredentialMigration(receipt)
	}
	return s.manualCredentialRecovery(receipt, "state", "unsupported_migration_state", "Credential migration state cannot be resumed automatically")
}

func (s *Server) rollbackCredentialMigration(ctx context.Context, receiptID string, request credentialMigrationRequest) (map[string]any, error) {
	receipt, err := s.hub.GetCredentialMigration(strings.TrimSpace(receiptID))
	if err != nil {
		return nil, err
	}
	plan := s.validateCredentialMigrationRollback(receipt)
	if request.DryRun {
		view := credentialMigrationReceiptView(receipt)
		view["dryRun"] = true
		view["rollbackStatus"] = plan.Status
		if plan.Reason != "" {
			view["rollbackReason"] = plan.Reason
		}
		return view, nil
	}
	unlock := s.lockCredentialMigration("connection:" + receipt.ConnectionID)
	defer unlock()
	receipt, err = s.hub.GetCredentialMigration(receipt.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Confirm) != receipt.ID {
		return nil, &hub.HubError{Status: 400, Message: "Confirm this rollback with the exact receipt ID"}
	}
	if receipt.State == hub.CredentialMigrationRolledBack {
		return credentialMigrationReceiptView(receipt), nil
	}
	plan = s.validateCredentialMigrationRollback(receipt)
	if plan.Status != "ready" {
		return nil, &hub.HubError{Status: 409, Message: "credential rollback blocked: " + plan.Reason}
	}
	receipt = s.prepareAndContinueCredentialMigrationRollback(ctx, receipt)
	return credentialMigrationReceiptView(receipt), nil
}

func (s *Server) validateCredentialMigrationRollback(receipt hub.CredentialMigrationReceipt) credentialMigrationRollbackValidation {
	if receipt.State == hub.CredentialMigrationRolledBack {
		return credentialMigrationRollbackValidation{Status: "already_rolled_back"}
	}
	if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
		reason := "manual_recovery_required"
		if receipt.Error != nil && receipt.Error.Code != "" {
			reason = receipt.Error.Code
		}
		return credentialMigrationRollbackValidation{Status: "manual", Reason: reason}
	}
	if receipt.State != hub.CredentialMigrationCompleted && receipt.State != hub.CredentialMigrationFailed {
		return credentialMigrationRollbackValidation{Status: "blocked", Reason: "receipt_phase_not_rollbackable"}
	}
	current, err := s.migrationConnection(receipt.ConnectionID)
	if err != nil {
		return credentialMigrationRollbackValidation{Status: "blocked", Reason: "connection_unavailable"}
	}
	expectedReference := receipt.PreviousCredentialRef
	if receipt.State == hub.CredentialMigrationCompleted {
		expectedReference = receipt.TargetCredentialRef
		if _, err := credentialstore.ParseReference(expectedReference); err != nil {
			return credentialMigrationRollbackValidation{Status: "blocked", Reason: "target_reference_unverified"}
		}
	}
	if current.CredentialRef != expectedReference {
		return credentialMigrationRollbackValidation{Status: "blocked", Reason: "canonical_reference_mismatch"}
	}
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, expectedReference); err != nil {
		return credentialMigrationRollbackValidation{Status: "blocked", Reason: "identity_snapshot_mismatch"}
	}
	if credentialMigrationGatewayRollbackRequired(current.Provider, receipt) {
		if err := preflightCredentialMigrationRollback(s, current, receipt); err != nil {
			return credentialMigrationRollbackValidation{Status: "blocked", Reason: "gateway_anchor_or_platform_unverified"}
		}
	}
	return credentialMigrationRollbackValidation{Status: "ready"}
}

func (s *Server) rollbackFailedCredentialMigration(ctx context.Context, receipt hub.CredentialMigrationReceipt, stage, code, message string) hub.CredentialMigrationReceipt {
	receipt.Error = &hub.CredentialMigrationError{Stage: stage, Code: code, Message: message}
	return s.prepareAndContinueCredentialMigrationRollback(ctx, receipt)
}

func (s *Server) prepareAndContinueCredentialMigrationRollback(ctx context.Context, receipt hub.CredentialMigrationReceipt) hub.CredentialMigrationReceipt {
	current, err := s.migrationConnection(receipt.ConnectionID)
	if err != nil {
		return s.manualCredentialRecovery(receipt, "rollback", "connection_unavailable", "Connection is unavailable for automatic rollback")
	}
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef, receipt.TargetCredentialRef); err != nil {
		return s.manualCredentialRecovery(receipt, "rollback", "migration_identity_changed", "Credential migration identity changed before rollback")
	}
	var effectID string
	var prepared hub.CredentialMigrationGatewayReceipt
	var prepareErr error
	if credentialMigrationGatewayRollbackRequired(current.Provider, receipt) {
		effectID, prepared, prepareErr = prepareCredentialMigrationRollbackEffect(s, current, receipt)
	} else {
		effectID = migrationGatewayEffectID(receipt.ID, receipt.PreviousCredentialRef, "rollback", receipt.RollbackEffectAttempt+1)
		prepared = hub.CredentialMigrationGatewayReceipt{Status: "not_applicable"}
	}
	if prepareErr != nil {
		return s.manualCredentialRecovery(receipt, "rollback", "rollback_effect_prepare_failed", "Gateway rollback effect could not be durably prepared")
	}
	receipt.RollbackEffectID = effectID
	receipt.RollbackEffectAttempt++
	receipt.RollbackEffectState = "rollback_prepared"
	receipt.RollbackGatewayReceipt = &prepared
	receipt.State = hub.CredentialMigrationRollingBack
	receipt = s.saveCredentialMigrationControlled(receipt, receipt.PreviousCredentialRef, receipt.TargetCredentialRef)
	if receipt.State == hub.CredentialMigrationManualRecoveryRequired {
		return receipt
	}
	return s.continueCredentialMigrationRollback(ctx, receipt, true)
}

func (s *Server) continueCredentialMigrationRollback(ctx context.Context, receipt hub.CredentialMigrationReceipt, execute bool) hub.CredentialMigrationReceipt {
	current, err := s.migrationConnection(receipt.ConnectionID)
	if err != nil {
		return s.manualCredentialRecovery(receipt, "rollback", "connection_unavailable", "Connection is unavailable for automatic rollback")
	}
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef, receipt.TargetCredentialRef); err != nil {
		return s.manualCredentialRecovery(receipt, "rollback", "migration_identity_changed", "Credential migration identity changed during rollback")
	}
	if current.CredentialRef == receipt.TargetCredentialRef {
		var err error
		if _, receipt, err = s.hub.CompareAndSwapConnectionCredentialForMigration(receipt.ID, receipt.TargetCredentialRef, receipt.PreviousCredentialRef); err != nil {
			return s.manualCredentialRecovery(receipt, "rollback", "reference_restore_failed", "Canonical credential reference could not be restored")
		}
	} else if current.CredentialRef != receipt.PreviousCredentialRef {
		return s.manualCredentialRecovery(receipt, "rollback", "reference_conflict", "Canonical credential reference no longer matches either rollback endpoint")
	}
	if credentialMigrationGatewayRollbackRequired(current.Provider, receipt) {
		var gatewayReceipt hub.CredentialMigrationGatewayReceipt
		var rollbackErr error
		if execute {
			gatewayReceipt, rollbackErr = rollbackCredentialMigrationGateway(ctx, s, current, receipt)
		} else {
			var proven bool
			gatewayReceipt, proven, rollbackErr = reconcileCredentialMigrationGatewayEffect(ctx, s, current, receipt, true)
			if rollbackErr == nil && !proven {
				return s.manualCredentialRecovery(receipt, "rollback", "gateway_rollback_indeterminate", "Gateway rollback may have occurred; explicit reconciliation is required before retry")
			}
		}
		gatewayReceipt = preserveMigrationGatewayEffectTarget(receipt.RollbackGatewayReceipt, gatewayReceipt)
		receipt.RollbackGatewayReceipt = &gatewayReceipt
		if rollbackErr != nil {
			return s.manualCredentialRecovery(receipt, "rollback", "gateway_restore_failed", "Previous gateway executable, unit, or heartbeat could not be restored")
		}
	}
	receipt.RollbackEffectState = "rollback_applied"
	receipt.State = hub.CredentialMigrationRolledBack
	return s.saveCredentialMigration(receipt)
}

func credentialMigrationGatewayRollbackRequired(provider string, receipt hub.CredentialMigrationReceipt) bool {
	if managedGatewayProvider(provider) == "" {
		return false
	}
	return receipt.State == hub.CredentialMigrationCompleted || receipt.GatewayEffectAttempt > 0 ||
		receipt.GatewayEffectID != "" || receipt.GatewayReceipt != nil
}

func (s *Server) migrationConnection(connectionID string) (hub.PlatformConnection, error) {
	for _, connection := range s.hub.ListConnections() {
		if connection.ID == strings.TrimSpace(connectionID) {
			return connection, nil
		}
	}
	return hub.PlatformConnection{}, &hub.HubError{Status: 404, Message: "Connection not found: " + strings.TrimSpace(connectionID)}
}

func (s *Server) saveCredentialMigration(receipt hub.CredentialMigrationReceipt) hub.CredentialMigrationReceipt {
	saved, err := s.credentialMigrationSave(receipt, receipt.Version)
	if err != nil {
		receipt.State = hub.CredentialMigrationManualRecoveryRequired
		receipt.Error = &hub.CredentialMigrationError{Stage: "receipt", Code: "receipt_persist_failed", Message: "Credential migration receipt could not be persisted"}
		return receipt
	}
	return saved
}

func (s *Server) saveCredentialMigrationControlled(receipt hub.CredentialMigrationReceipt, allowedCredentialRefs ...string) hub.CredentialMigrationReceipt {
	saved, err := s.credentialMigrationControlledSave(receipt, receipt.Version, allowedCredentialRefs...)
	if err != nil {
		return s.manualCredentialRecovery(receipt, "identity", "credential_migration_reservation_failed", "Credential migration identity reservation could not be persisted or matched")
	}
	return saved
}

func credentialMigrationPersistenceFailed(receipt hub.CredentialMigrationReceipt) bool {
	return receipt.State == hub.CredentialMigrationManualRecoveryRequired && receipt.Error != nil && receipt.Error.Code == "receipt_persist_failed"
}

func (s *Server) lockCredentialMigration(key string) func() {
	s.credentialMu.Lock()
	lock := s.credentialLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.credentialLocks[key] = lock
	}
	s.credentialMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (s *Server) failCredentialMigration(receipt hub.CredentialMigrationReceipt, stage, code, message string) hub.CredentialMigrationReceipt {
	receipt.State = hub.CredentialMigrationFailed
	receipt.Error = &hub.CredentialMigrationError{Stage: stage, Code: code, Message: message}
	return s.saveCredentialMigration(receipt)
}

func (s *Server) manualCredentialRecovery(receipt hub.CredentialMigrationReceipt, stage, code, message string) hub.CredentialMigrationReceipt {
	receipt.State = hub.CredentialMigrationManualRecoveryRequired
	receipt.Error = &hub.CredentialMigrationError{Stage: stage, Code: code, Message: message}
	return s.saveCredentialMigration(receipt)
}

func credentialMigrationReceiptView(receipt hub.CredentialMigrationReceipt) map[string]any {
	view := map[string]any{
		"id": receipt.ID, "connectionId": receipt.ConnectionID, "provider": receipt.Provider,
		"state": receipt.State, "previousCredentialScheme": credentialReferenceScheme(receipt.PreviousCredentialRef),
		"targetCredentialRef": receipt.TargetCredentialRef,
		"credentialsIncluded": false, "runnableRestore": false,
		"backupStatus": "credentials_excluded", "version": receipt.Version,
		"createdAt": receipt.CreatedAt, "updatedAt": receipt.UpdatedAt,
	}
	if receipt.ProviderReceipt != nil {
		view["providerReceipt"] = receipt.ProviderReceipt
	}
	if receipt.GatewayReceipt != nil {
		view["gatewayReceipt"] = receipt.GatewayReceipt
	}
	if receipt.Error != nil {
		view["error"] = receipt.Error
	}
	return view
}

func credentialReferenceScheme(reference string) string {
	if index := strings.Index(strings.TrimSpace(reference), ":"); index > 0 {
		return strings.ToLower(strings.TrimSpace(reference[:index]))
	}
	return "none"
}

func (s *Server) loadKeychainMigrationSource(connection hub.PlatformConnection) (credentialMigrationMaterial, error) {
	if !strings.HasPrefix(strings.TrimSpace(connection.CredentialRef), "keychain:") {
		return credentialMigrationMaterial{}, errors.New("not a Keychain credential reference")
	}
	switch managedGatewayProvider(connection.Provider) {
	case "feishu":
		appID := strings.TrimSpace(connection.AccountRef)
		if connection.CredentialRef != "keychain:"+feishu.CredentialService(appID) {
			return credentialMigrationMaterial{}, errors.New("Feishu credential identity mismatch")
		}
		secret, err := feishu.LoadAppSecret(appID)
		if err != nil || secret == "" {
			return credentialMigrationMaterial{}, errors.New("Feishu credential unavailable")
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(feishu.ManagedCredentialBinding(appID)), Subject: appID,
			Payload: credentialstore.Payload{Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": appID, "appSecret": secret}},
		}, nil
	case "slack":
		appID := slackAppIDFromCredentialRef(connection.CredentialRef)
		tokens, err := loomslack.LoadTokens(appID, connection.AccountRef)
		if err != nil || appID == "" || tokens.Bot == "" || tokens.App == "" {
			return credentialMigrationMaterial{}, errors.New("Slack credential unavailable")
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(loomslack.ManagedCredentialBinding(appID, connection.AccountRef)), Subject: connection.AccountRef,
			Payload: credentialstore.Payload{Provider: "slack", Kind: "tokens", Values: map[string]string{"appID": appID, "teamID": connection.AccountRef, "botToken": tokens.Bot, "appToken": tokens.App}},
		}, nil
	case "parall":
		agentID, identityErr := s.parallMigrationAgentID(connection)
		if identityErr != nil {
			return credentialMigrationMaterial{}, identityErr
		}
		loaded, err := parall.LoadAgentCredentials(connection.AccountRef, agentID)
		if err != nil || loaded.APIURL == "" || loaded.APIKey == "" {
			return credentialMigrationMaterial{}, errors.New("Parall credential unavailable")
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(parall.ManagedAgentCredentialBinding(connection.AccountRef, agentID)), Subject: agentID,
			Payload: credentialstore.Payload{Provider: "parall", Kind: "agent", Values: map[string]string{
				"orgID": connection.AccountRef, "agentID": agentID, "apiURL": loaded.APIURL, "apiKey": loaded.APIKey,
			}},
		}, nil
	case "":
		if strings.EqualFold(connection.Provider, "github") {
			token, err := githubapi.LoadCredential(connection.CredentialRef)
			if err != nil || token == "" || connection.AccountRef == "" || connection.ScopeRef == "" {
				return credentialMigrationMaterial{}, errors.New("GitHub credential unavailable")
			}
			return credentialMigrationMaterial{
				Binding: credentialstoreBinding(githubapi.ManagedCredentialBinding(connection.AccountRef, connection.ScopeRef)),
				Subject: connection.AccountRef + "/" + connection.ScopeRef,
				Payload: credentialstore.Payload{Provider: "github", Kind: "access-token", Values: map[string]string{
					"login": connection.AccountRef, "resourceOwner": connection.ScopeRef, "token": token,
				}},
			}, nil
		}
	}
	return credentialMigrationMaterial{}, fmt.Errorf("provider is not supported for managed credential migration")
}

func (s *Server) loadManagedMigrationMaterial(connection hub.PlatformConnection, reference string) (credentialMigrationMaterial, error) {
	credentials, err := s.hub.CredentialStore()
	if err != nil {
		return credentialMigrationMaterial{}, err
	}
	switch managedGatewayProvider(connection.Provider) {
	case "feishu":
		appID := strings.TrimSpace(connection.AccountRef)
		secret, err := feishu.LoadAppSecretReference(credentials, reference, appID)
		if err != nil {
			return credentialMigrationMaterial{}, err
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(feishu.ManagedCredentialBinding(appID)), Subject: appID,
			Payload: credentialstore.Payload{Provider: "lark", Kind: "app-secret", Values: map[string]string{"appID": appID, "appSecret": secret}},
		}, nil
	case "slack":
		appID, tokens, err := loomslack.LoadTokensAndAppReference(credentials, reference, "", connection.AccountRef)
		if err != nil {
			return credentialMigrationMaterial{}, err
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(loomslack.ManagedCredentialBinding(appID, connection.AccountRef)), Subject: connection.AccountRef,
			Payload: credentialstore.Payload{Provider: "slack", Kind: "tokens", Values: map[string]string{"appID": appID, "teamID": connection.AccountRef, "botToken": tokens.Bot, "appToken": tokens.App}},
		}, nil
	case "parall":
		agentID, err := s.parallMigrationAgentID(connection)
		if err != nil {
			return credentialMigrationMaterial{}, err
		}
		loaded, err := parall.LoadAgentCredentialsReference(credentials, reference, connection.AccountRef, agentID)
		if err != nil {
			return credentialMigrationMaterial{}, err
		}
		return credentialMigrationMaterial{
			Binding: credentialstoreBinding(parall.ManagedAgentCredentialBinding(connection.AccountRef, agentID)), Subject: agentID,
			Payload: credentialstore.Payload{Provider: "parall", Kind: "agent", Values: map[string]string{
				"orgID": connection.AccountRef, "agentID": agentID, "apiURL": loaded.APIURL, "apiKey": loaded.APIKey,
			}},
		}, nil
	case "":
		if strings.EqualFold(connection.Provider, "github") {
			token, err := githubapi.LoadCredentialFor(credentials, reference, connection.AccountRef, connection.ScopeRef)
			if err != nil {
				return credentialMigrationMaterial{}, err
			}
			return credentialMigrationMaterial{
				Binding: credentialstoreBinding(githubapi.ManagedCredentialBinding(connection.AccountRef, connection.ScopeRef)),
				Subject: connection.AccountRef + "/" + connection.ScopeRef,
				Payload: credentialstore.Payload{Provider: "github", Kind: "access-token", Values: map[string]string{
					"login": connection.AccountRef, "resourceOwner": connection.ScopeRef, "token": token,
				}},
			}, nil
		}
	}
	return credentialMigrationMaterial{}, errors.New("managed credential provider is unsupported")
}

func (s *Server) parallMigrationAgentID(connection hub.PlatformConnection) (string, error) {
	agentID := parallAgentIDFromCredentialRef(connection.CredentialRef)
	addresses, err := s.hub.ListAddresses("")
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if address.ConnectionID != connection.ID || address.ArchivedAt != "" || address.DeletedAt != "" {
			continue
		}
		candidate := strings.TrimPrefix(strings.TrimSpace(address.ExternalIdentity), "prll://")
		if candidate == "" {
			continue
		}
		if agentID != "" && agentID != candidate {
			return "", errors.New("Parall credential and Address identities do not match")
		}
		agentID = candidate
	}
	if agentID == "" {
		return "", errors.New("Parall Agent identity is unavailable")
	}
	return agentID, nil
}

func (s *Server) verifyMigrationProvider(ctx context.Context, connection hub.PlatformConnection, material credentialMigrationMaterial) (hub.CredentialMigrationProviderReceipt, error) {
	receipt := hub.CredentialMigrationProviderReceipt{Status: "verified", Subject: material.Subject, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	switch managedGatewayProvider(connection.Provider) {
	case "feishu":
		_, err := discoverFeishu(ctx, material.Payload.Values["appID"], material.Payload.Values["appSecret"])
		return receipt, err
	case "slack":
		discovery, err := discoverSlackClient(ctx, material.Payload.Values["botToken"], material.Payload.Values["appToken"])
		var apiErr *loomslack.APIError
		if err != nil && errors.As(err, &apiErr) && apiErr.Method == "conversations.list" && apiErr.Code == "missing_scope" {
			err = nil
		}
		if err == nil && (discovery.Identity.AppID != material.Payload.Values["appID"] || discovery.Identity.TeamID != material.Payload.Values["teamID"] || discovery.Identity.TeamID != connection.AccountRef) {
			return receipt, errors.New("Slack App and Team identity verification failed")
		}
		return receipt, err
	case "parall":
		client := newParallClient(material.Payload.Values["apiURL"], material.Payload.Values["apiKey"])
		external, err := client.GetAgentMe(ctx, material.Payload.Values["orgID"])
		if err != nil || external.ID != material.Payload.Values["agentID"] || external.Status != "active" {
			return receipt, errors.New("Parall identity verification failed")
		}
		_, err = client.GetWSTicket(ctx)
		return receipt, err
	case "":
		if strings.EqualFold(connection.Provider, "github") {
			user, err := validateGitHubCredential(ctx, material.Payload.Values["token"])
			if err != nil || !strings.EqualFold(user.Login, connection.AccountRef) {
				return receipt, errors.New("GitHub identity verification failed")
			}
			return receipt, nil
		}
	}
	return receipt, errors.New("provider verification is unsupported")
}
