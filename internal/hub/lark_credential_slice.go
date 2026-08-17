package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

// LarkCredentialMigration is the non-secret result of the narrow Lark
// credential migration flow. No secret material ever appears in it.
type LarkCredentialMigration struct {
	ConnectionID    string `json:"connectionId"`
	PreviousRef     string `json:"previousRef"`
	CurrentRef      string `json:"currentRef"`
	FloorRaised     bool   `json:"floorRaised"`
	DryRun          bool   `json:"dryRun"`
	AlreadyMigrated bool   `json:"alreadyMigrated"`
	RequiresProof   bool   `json:"requiresProof"`
	PlanPending     bool   `json:"planPending,omitempty"`
}

type larkCredentialMigrationRecord struct {
	Version      int    `json:"version"`
	ConnectionID string `json:"connectionId"`
	PreviousRef  string `json:"previousRef"`
	CurrentRef   string `json:"currentRef"`
	Phase        string `json:"phase"` // prepared | completed | ref_restored
	MigratedAt   string `json:"migratedAt"`
}

const (
	larkMigrationPhasePrepared    = "prepared"
	larkMigrationPhasePlanPending = "plan_pending"
	larkMigrationPhaseCompleted   = "completed"
	larkMigrationPhaseRefRestored = "ref_restored"
)

var errLarkCredentialSecretRequired = errors.New("credential secret is required")

// IsLarkCredentialSecretRequired reports whether err indicates a fresh
// migration needs the operator to supply the credential secret. plan_pending
// and completed re-entries return without this error and consume no secret.
func IsLarkCredentialSecretRequired(err error) bool {
	return errors.Is(err, errLarkCredentialSecretRequired)
}

// larkMigrationIndeterminateError is returned when a write-then-error outcome
// cannot be classified as committed or provably uncommitted. All artifacts are
// retained and the operator must reconcile (re-run migrate or rollback).
type larkMigrationIndeterminateError struct {
	Cause   error
	Message string
}

func (e *larkMigrationIndeterminateError) Error() string {
	return e.Message + ": " + e.Cause.Error()
}

func (e *larkMigrationIndeterminateError) Unwrap() error { return e.Cause }

// IsLarkMigrationIndeterminate reports whether err is a typed indeterminate
// migration outcome that requires manual reconcile.
func IsLarkMigrationIndeterminate(err error) bool {
	var target *larkMigrationIndeterminateError
	return errors.As(err, &target)
}

func larkMigrationRecordsEqual(left, right larkCredentialMigrationRecord) bool {
	return left.Version == right.Version && left.ConnectionID == right.ConnectionID &&
		left.PreviousRef == right.PreviousRef && left.CurrentRef == right.CurrentRef &&
		left.Phase == right.Phase && left.MigratedAt == right.MigratedAt
}

func validCredentialRef(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "env:") || strings.HasPrefix(value, "keychain:") {
		return true
	}
	return credentials.IsManagedRef(value)
}

// larkMigrationRecordPath is deliberately outside the backup-excluded
// credentials directory: the non-secret record must survive ordinary backups
// so a restored data directory can still roll back to the legacy reference.
func larkMigrationRecordPath(connectionID string) string {
	return filepath.Join("lark-migrations", connectionID+".json")
}

// MigrateLarkCredential is the narrow local operator flow for one Lark
// Connection. A durable per-Connection record with a phase is written before
// the credential reference effect, so a crash at any step is reconcilable and
// idempotent; failure stops in an explicit state and never changes Connection
// identity, Address, Membership, Conversation, Inbox, or Outbox history.
func (h *Hub) MigrateLarkCredential(_ context.Context, connectionID string, secret []byte, dryRun bool) (LarkCredentialMigration, error) {
	result, connection, err := h.larkMigrationBaseline(connectionID)
	if err != nil {
		return result, err
	}
	record, recordErr := h.readLarkMigrationRecord(connectionID)
	recordFound := recordErr == nil
	if recordErr != nil && !os.IsNotExist(recordErr) {
		return result, errf(409, "migration record is unreadable: %s", recordErr)
	}
	if recordFound {
		currentRef := strings.TrimSpace(connection.CredentialRef)
		recordCurrent := strings.TrimSpace(record.CurrentRef)
		recordPrevious := strings.TrimSpace(record.PreviousRef)
		switch record.Phase {
		case larkMigrationPhaseRefRestored:
			return result, errf(409, "migration rollback cleanup is pending; run rollback to finish")
		case larkMigrationPhasePlanPending, larkMigrationPhaseCompleted:
			// These phases mean the reference effect committed: only the
			// migrated reference is a consistent tuple. A previous reference
			// here is an inconsistent legacy state that must never re-enter a
			// fresh migration.
			if currentRef != recordCurrent {
				return result, errf(409, "connection reference %q is inconsistent with a %s migration record; manual reconcile required", currentRef, record.Phase)
			}
		case larkMigrationPhasePrepared:
			if currentRef != recordCurrent && currentRef != recordPrevious {
				return result, errf(409, "connection reference %q is inconsistent with the prepared migration record; manual reconcile required", currentRef)
			}
		}
	}
	if credentials.IsManagedRef(connection.CredentialRef) {
		if !recordFound || record.CurrentRef != connection.CredentialRef {
			return result, errf(409, "managed credential reference has no durable migration record; run rollback guidance or re-migrate")
		}
		switch record.Phase {
		case larkMigrationPhaseCompleted:
			if _, err := h.resolveManagedCredential(connection.CredentialRef); err != nil {
				return result, errf(409, "managed credential is dangling: %s", err)
			}
			result.CurrentRef = connection.CredentialRef
			result.AlreadyMigrated = true
			result.FloorRaised = h.st.CredentialFloorPresent()
			return result, nil
		case larkMigrationPhasePlanPending:
			if _, err := h.resolveManagedCredential(connection.CredentialRef); err != nil {
				return result, errf(409, "managed credential is dangling: %s", err)
			}
			result.CurrentRef = connection.CredentialRef
			result.PlanPending = true
			result.FloorRaised = h.st.CredentialFloorPresent()
			return result, nil
		case larkMigrationPhasePrepared:
			// Crash after the reference effect before the phase advanced.
			// The exact managed credential must still exist before the
			// migration can be advanced and a launch plan frozen.
			if _, err := h.resolveManagedCredential(connection.CredentialRef); err != nil {
				return result, errf(500, "managed credential for the prepared migration is missing; manual reconcile required: %s", err)
			}
			if err := h.advanceLarkMigrationPhase(connectionID, larkMigrationPhasePlanPending); err != nil {
				return result, errf(500, "reconcile migration record: %s", err)
			}
			result.CurrentRef = connection.CredentialRef
			result.PlanPending = true
			result.FloorRaised = h.st.CredentialFloorPresent()
			return result, nil
		default:
			return result, errf(409, "migration record phase %q is inconsistent with the connection reference", record.Phase)
		}
	}
	if recordFound && record.Phase == larkMigrationPhasePrepared && connection.CredentialRef == record.PreviousRef {
		// Crash after Put and record write but before the reference effect.
		// The exact managed credential must still exist before the binding is
		// advanced to it; a compensated/deleted secret fails closed.
		if _, err := h.resolveManagedCredential(record.CurrentRef); err != nil {
			return result, errf(500, "managed credential for the prepared migration is missing; manual reconcile required: %s", err)
		}
		if _, err := h.updateLarkConnection(connectionID, record.CurrentRef); err != nil {
			return result, errf(500, "resume migration reference update: %s", err)
		}
		if err := h.advanceLarkMigrationPhase(connectionID, larkMigrationPhasePlanPending); err != nil {
			return result, errf(500, "reconcile migration record: %s", err)
		}
		result.PreviousRef = record.PreviousRef
		result.CurrentRef = record.CurrentRef
		result.PlanPending = true
		result.FloorRaised = h.st.CredentialFloorPresent()
		return result, nil
	}
	if err := h.larkGatewayMigrationBlocker(connectionID); err != nil {
		return result, err
	}
	result.PreviousRef = connection.CredentialRef
	if dryRun {
		result.DryRun = true
		result.FloorRaised = h.st.CredentialFloorPresent()
		return result, nil
	}
	if len(secret) == 0 {
		return result, errLarkCredentialSecretRequired
	}
	if !h.st.CredentialFloorPresent() {
		if err := h.st.SaveCredentialFloor(); err != nil {
			return result, errf(500, "raise credential floor: %s", err)
		}
		result.FloorRaised = true
	}
	credentialStore, err := credentials.New(h.st)
	if err != nil {
		return result, errf(500, "open managed credential store: %s", err)
	}
	ref, err := credentialStore.Put(secret)
	if err != nil {
		return result, errf(500, "persist managed credential: %s", err)
	}
	result.CurrentRef = string(ref)
	record = larkCredentialMigrationRecord{
		Version: 1, ConnectionID: connectionID, PreviousRef: result.PreviousRef,
		CurrentRef: result.CurrentRef, Phase: larkMigrationPhasePrepared, MigratedAt: now(),
	}
	if err := h.writeLarkMigrationRecord(record); err != nil {
		readback, readErr := h.readLarkMigrationRecord(connectionID)
		switch {
		case readErr == nil && larkMigrationRecordsEqual(readback, record):
			return result, &larkMigrationIndeterminateError{Cause: err, Message: "migration record committed but reported indeterminate (managed credential retained; re-run migrate to reconcile)"}
		case os.IsNotExist(readErr):
			// Mechanically proven absent: the record effect did not commit.
			cleanupErr := credentialStore.Delete(ref)
			if cleanupErr != nil && !credentials.IsCredentialNotFound(cleanupErr) {
				return result, &larkMigrationIndeterminateError{Cause: errors.Join(err, cleanupErr), Message: "credential cleanup failed after proven-uncommitted record write (credential retained; manual reconcile required)"}
			}
			return result, errf(500, "persist migration record: %s (managed credential deleted; credential floor stays raised)", err)
		default:
			// A valid-but-different record or any readback error cannot prove
			// the intended record absent: retain every artifact.
			return result, &larkMigrationIndeterminateError{Cause: errors.Join(err, readErr), Message: "migration record write outcome is ambiguous (all artifacts retained; manual reconcile required)"}
		}
	}
	if _, err := h.updateLarkConnection(connectionID, result.CurrentRef); err != nil {
		persisted, readErr := h.larkConnectionRefPersisted(connectionID)
		switch {
		case readErr == nil && persisted == result.CurrentRef:
			return result, &larkMigrationIndeterminateError{Cause: err, Message: "connection reference committed but reported indeterminate (credential and record retained; re-run migrate to reconcile)"}
		case readErr != nil || (persisted != result.PreviousRef && persisted != ""):
			return result, &larkMigrationIndeterminateError{Cause: errors.Join(err, readErr), Message: "connection reference write outcome is ambiguous (all artifacts retained; manual reconcile required)"}
		default:
			// Mechanically proven uncommitted: durably remove the record first,
			// then delete the credential. Any cleanup error retains the safe
			// side and reports a typed indeterminate stop.
			if removeErr := h.removeLarkMigrationRecord(connectionID); removeErr != nil {
				return result, &larkMigrationIndeterminateError{Cause: errors.Join(err, removeErr), Message: "reference effect proven uncommitted but migration record cleanup failed (credential retained; re-run migrate to reconcile)"}
			}
			cleanupErr := credentialStore.Delete(ref)
			if cleanupErr != nil && !credentials.IsCredentialNotFound(cleanupErr) {
				return result, &larkMigrationIndeterminateError{Cause: errors.Join(err, cleanupErr), Message: "reference effect and record proven uncommitted but credential cleanup failed (orphan credential retained)"}
			}
			return result, errf(500, "update connection credential reference: %s (managed credential and record removed; credential floor stays raised)", err)
		}
	}
	if err := h.advanceLarkMigrationPhase(connectionID, larkMigrationPhasePlanPending); err != nil {
		return result, fmt.Errorf("migrated but migration record phase is pending: %w", err)
	}
	result.PlanPending = true
	return result, nil
}

// FinishLarkGatewayLaunchPlan advances a durable plan_pending migration to
// completed once the typed Lark launch plan is frozen with the exact managed
// reference of the committed binding. It is idempotent: a completed migration
// is a no-op, and a missing typed plan fails closed so the operator can re-run
// migrate to configure it.
func (h *Hub) FinishLarkGatewayLaunchPlan(connectionID string) error {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return errf(400, "connection id is required")
	}
	record, err := h.readLarkMigrationRecord(connectionID)
	if err != nil {
		if os.IsNotExist(err) {
			return errf(409, "connection is not migrated")
		}
		return errf(409, "migration record is unreadable: %s", err)
	}
	if record.Phase == larkMigrationPhaseCompleted {
		return nil
	}
	if record.Phase != larkMigrationPhasePlanPending {
		return errf(409, "migration record phase %q cannot complete the launch plan", record.Phase)
	}
	h.mu.Lock()
	connection := h.connections[connectionID]
	h.mu.Unlock()
	if connection == nil {
		return errf(404, "connection not found: %s", connectionID)
	}
	if !credentials.IsManagedRef(connection.CredentialRef) || connection.CredentialRef != record.CurrentRef {
		return errf(409, "migration record does not match the connection reference")
	}
	if planRef := h.LarkGatewayLaunchPlanRef(connectionID); planRef != connection.CredentialRef {
		return errf(409, "typed Gateway launch plan is not frozen with the managed reference")
	}
	return h.advanceLarkMigrationPhase(connectionID, larkMigrationPhaseCompleted)
}

// VerifyLarkCredential confirms the managed credential resolves and the
// Connection references it, and that the R1 Gateway lifecycle has accepted an
// exact fresh process proof. Without proof it fails closed instead of printing
// a false green.
func (h *Hub) VerifyLarkCredential(connectionID string) (LarkCredentialMigration, error) {
	result, connection, err := h.larkMigrationBaseline(connectionID)
	if err != nil {
		return result, err
	}
	if !credentials.IsManagedRef(connection.CredentialRef) {
		return result, errf(409, "connection does not reference a managed credential")
	}
	record, err := h.readLarkMigrationRecord(connectionID)
	if err != nil {
		return result, errf(409, "migration record is unavailable: %s", err)
	}
	if record.CurrentRef != connection.CredentialRef {
		return result, errf(409, "migration record does not match the connection reference")
	}
	if record.Phase != larkMigrationPhaseCompleted {
		return result, errf(409, "migration is not complete (phase %q); run migrate", record.Phase)
	}
	if _, err := h.resolveManagedCredential(connection.CredentialRef); err != nil {
		return result, errf(409, "managed credential does not resolve: %s", err)
	}
	if err := h.requireAcceptedGatewayProof(connectionID); err != nil {
		result.RequiresProof = true
		return result, errf(409, "managed credential resolves but the Gateway lifecycle has no accepted fresh exact proof: %s", err)
	}
	result.PreviousRef = record.PreviousRef
	result.CurrentRef = connection.CredentialRef
	result.AlreadyMigrated = true
	result.FloorRaised = h.st.CredentialFloorPresent()
	return result, nil
}

// RollbackLarkCredential restores the previous credential reference, revokes
// any durable typed Gateway launch plan, deletes the managed credential, and
// only then removes the migration record. Every step is idempotent and
// resumable: a crash anywhere leaves the record in a state that a re-run of
// rollback completes, and the credential writer floor is never lowered.
func (h *Hub) RollbackLarkCredential(connectionID string) (LarkCredentialMigration, error) {
	result, connection, err := h.larkMigrationBaseline(connectionID)
	if err != nil {
		return result, err
	}
	record, err := h.readLarkMigrationRecord(connectionID)
	if err != nil {
		if os.IsNotExist(err) {
			if credentials.IsManagedRef(connection.CredentialRef) {
				return result, errf(409, "managed credential reference has no migration record; previous reference is unrecoverable")
			}
			return result, errf(409, "connection is not migrated")
		}
		return result, errf(409, "migration record is unreadable: %s", err)
	}
	if record.CurrentRef == "" || record.PreviousRef == "" {
		return result, errf(409, "migration record is incomplete")
	}
	currentRef := strings.TrimSpace(connection.CredentialRef)
	recordCurrent := strings.TrimSpace(record.CurrentRef)
	recordPrevious := strings.TrimSpace(record.PreviousRef)
	if currentRef != recordCurrent && currentRef != recordPrevious {
		return result, errf(409, "connection reference %q is neither the migrated reference nor its previous reference; manual reconcile required", currentRef)
	}
	if currentRef == recordCurrent {
		if strings.TrimSpace(record.PreviousRef) == "" {
			return result, errf(409, "rollback requires a non-empty previous credential reference")
		}
		// The rollback intent must be durable before any binding effect so a
		// crash between the two steps leaves a tuple that migrate refuses and
		// rollback re-enters idempotently.
		if err := h.advanceLarkMigrationPhase(connectionID, larkMigrationPhaseRefRestored); err != nil {
			return result, errf(500, "persist rollback intent before restoring the reference: %s", err)
		}
		if _, err := h.updateLarkConnection(connectionID, record.PreviousRef); err != nil {
			return result, errf(500, "restore previous credential reference: %s", err)
		}
	}
	// The migration record must survive until the typed launch plan is revoked
	// durably, so a crash between these steps is re-entrant from the same
	// rollback command.
	if err := h.RevokeLarkGatewayLaunch(connectionID); err != nil {
		return result, errf(500, "credential reference restored but Gateway launch plan revocation failed: %s", err)
	}
	credentialStore, err := credentials.New(h.st)
	if err != nil {
		return result, errf(500, "open managed credential store: %s", err)
	}
	deleteErr := credentialStore.Delete(credentials.Ref(record.CurrentRef))
	if deleteErr != nil && !credentials.IsCredentialNotFound(deleteErr) {
		return result, errf(500, "credential reference restored but managed credential deletion failed: %s", deleteErr)
	}
	if err := h.removeLarkMigrationRecord(connectionID); err != nil {
		return result, errf(500, "managed credential deleted but migration record removal failed: %s", err)
	}
	result.PreviousRef = record.PreviousRef
	result.CurrentRef = record.PreviousRef
	result.FloorRaised = h.st.CredentialFloorPresent()
	return result, nil
}

func (h *Hub) larkMigrationBaseline(connectionID string) (LarkCredentialMigration, PlatformConnection, error) {
	if h == nil || h.st == nil || h.passive || h.st.ReadOnly() || !h.st.HasLiveWritableOwner() {
		return LarkCredentialMigration{}, PlatformConnection{}, errf(409, "managed credential migration requires a live writable Hub")
	}
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return LarkCredentialMigration{}, PlatformConnection{}, errf(400, "connection id is required")
	}
	h.mu.Lock()
	connection := h.connections[connectionID]
	h.mu.Unlock()
	if connection == nil {
		return LarkCredentialMigration{}, PlatformConnection{}, errf(404, "connection not found: %s", connectionID)
	}
	provider := strings.ToLower(strings.TrimSpace(connection.Provider))
	if provider != "lark" && provider != "feishu" {
		return LarkCredentialMigration{}, PlatformConnection{}, errf(409, "connection %s is not a Lark connection", connectionID)
	}
	if connection.ArchivedAt != "" {
		return LarkCredentialMigration{}, PlatformConnection{}, errf(409, "connection is archived")
	}
	return LarkCredentialMigration{ConnectionID: connectionID}, *connection, nil
}

func (h *Hub) larkGatewayMigrationBlocker(connectionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	control := h.gatewayState.Controls[connectionID]
	if control == nil {
		return nil
	}
	if control.ActiveAttemptID != "" {
		return errf(409, "Gateway process attempt is active")
	}
	if control.Recovery != gatewayRecoveryNone {
		return errf(409, "Gateway recovery is required before migration")
	}
	binding, err := h.gatewayBindingLocked(connectionID)
	if err != nil {
		return errf(409, "Gateway binding is unavailable")
	}
	if !gatewayBindingsEqual(binding, control.Binding) {
		return errf(409, "Gateway binding drift requires reconciliation before migration")
	}
	return nil
}

func (h *Hub) requireAcceptedGatewayProof(connectionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	control := h.gatewayState.Controls[connectionID]
	if control == nil {
		return fmt.Errorf("connection has no adopted Gateway lifecycle")
	}
	if control.ActiveAttemptID != "" {
		return fmt.Errorf("Gateway process attempt is active")
	}
	if control.Recovery != gatewayRecoveryNone {
		return fmt.Errorf("Gateway recovery is %q", control.Recovery)
	}
	attempt := h.gatewayState.Attempts[connectionID]
	if attempt == nil || attempt.AcceptedProof == nil || !gatewayAttemptTerminal(attempt.Phase) {
		return fmt.Errorf("no accepted exact process proof")
	}
	if attempt.Phase != gatewayAttemptSucceeded {
		return fmt.Errorf("Gateway lifecycle ended in %s, not an accepted managed target proof", attempt.Phase)
	}
	plan := h.gatewayState.LaunchPlans[connectionID]
	connection := h.connections[connectionID]
	if connection == nil || plan == nil || plan.Target.Provider == "" ||
		plan.Target.ManagedCredentialRef != strings.TrimSpace(connection.CredentialRef) {
		return fmt.Errorf("typed Gateway launch plan does not match the current managed binding")
	}
	proof := attempt.AcceptedProof
	if proof.Generation != attempt.TargetGeneration || proof.Build != attempt.Plan.Target.Build ||
		proof.ExecutableDigest != attempt.Plan.Target.ExecutableDigest {
		return fmt.Errorf("accepted process proof is not the managed target proof")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, proof.ObservedAt)
	if err != nil || time.Since(observedAt) > gatewayProcessProofFreshness {
		return fmt.Errorf("accepted process proof is stale")
	}
	return nil
}

func (h *Hub) resolveManagedCredential(ref string) ([]byte, error) {
	if !credentials.IsManagedRef(ref) {
		return nil, fmt.Errorf("not a managed credential reference")
	}
	credentialStore, err := credentials.New(h.st)
	if err != nil {
		return nil, err
	}
	return credentialStore.Resolve(credentials.Ref(ref))
}

func (h *Hub) updateLarkConnection(connectionID, credentialRef string) (PlatformConnection, error) {
	if h.larkUpdateConnectionForTest != nil {
		return h.larkUpdateConnectionForTest(connectionID, credentialRef)
	}
	return h.UpdateConnection(connectionID, ConnectionParams{CredentialRef: credentialRef})
}

// larkConnectionRefPersisted is the authoritative disk readback used to decide
// whether an indeterminate integrations write actually committed the reference
// effect. In-memory state is deliberately not trusted here because a persist
// error may already have reverted it.
func (h *Hub) larkConnectionRefPersisted(connectionID string) (string, error) {
	var config integrationConfig
	if err := h.st.LoadIntegrations(&config); err != nil {
		return "", err
	}
	connection := config.Connections[connectionID]
	if connection == nil {
		return "", nil
	}
	return strings.TrimSpace(connection.CredentialRef), nil
}

func (h *Hub) advanceLarkMigrationPhase(connectionID, phase string) error {
	record, err := h.readLarkMigrationRecord(connectionID)
	if err != nil {
		return err
	}
	record.Phase = phase
	return h.writeLarkMigrationRecord(record)
}

func (h *Hub) writeLarkMigrationRecord(record larkCredentialMigrationRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	relative := larkMigrationRecordPath(record.ConnectionID)
	return h.st.WithStableWriteRoot(func(root *os.Root) error {
		if err := root.MkdirAll("lark-migrations", 0o700); err != nil {
			return err
		}
		temporary := filepath.Join("lark-migrations", ".lark-migration-"+randomHubSuffix()+".tmp")
		file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			_ = file.Close()
			if !committed {
				_ = root.Remove(temporary)
			}
		}()
		if _, err := file.Write(payload); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := root.Rename(temporary, relative); err != nil {
			return err
		}
		committed = true
		if h.larkMigrationRecordWriteForTest != nil {
			if hookErr := h.larkMigrationRecordWriteForTest(); hookErr != nil {
				return hookErr
			}
		}
		directory, err := root.Open("lark-migrations")
		if err != nil {
			return err
		}
		defer directory.Close()
		return directory.Sync()
	})
}

func (h *Hub) readLarkMigrationRecord(connectionID string) (larkCredentialMigrationRecord, error) {
	data, err := h.st.ReadStableFile(larkMigrationRecordPath(connectionID))
	if os.IsNotExist(err) {
		return larkCredentialMigrationRecord{}, err
	}
	if err != nil {
		return larkCredentialMigrationRecord{}, err
	}
	var record larkCredentialMigrationRecord
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return larkCredentialMigrationRecord{}, fmt.Errorf("invalid migration record")
	}
	if record.Version != 1 || record.ConnectionID != connectionID || record.CurrentRef == "" {
		return larkCredentialMigrationRecord{}, fmt.Errorf("invalid migration record")
	}
	switch record.Phase {
	case larkMigrationPhasePrepared, larkMigrationPhasePlanPending, larkMigrationPhaseCompleted, larkMigrationPhaseRefRestored:
	default:
		return larkCredentialMigrationRecord{}, fmt.Errorf("invalid migration record phase")
	}
	return record, nil
}

func (h *Hub) removeLarkMigrationRecord(connectionID string) error {
	if h.larkMigrationRecordRemoveForTest != nil {
		if err := h.larkMigrationRecordRemoveForTest(connectionID); err != nil {
			return err
		}
	}
	return h.st.WithStableWriteRoot(func(root *os.Root) error {
		if err := root.Remove(larkMigrationRecordPath(connectionID)); err != nil && !os.IsNotExist(err) {
			return err
		}
		directory, err := root.Open("lark-migrations")
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer directory.Close()
		return directory.Sync()
	})
}

func randomHubSuffix() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(random)
}
