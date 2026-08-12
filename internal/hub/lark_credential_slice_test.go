package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

func newLarkFixture(t *testing.T) r0bFixture {
	t.Helper()
	fixture := newR0bFixture(t)
	connection, err := fixture.h.CreateConnection(ConnectionParams{
		Provider: "lark", AccountRef: "app", ScopeRef: "org",
		CredentialRef: "keychain:com.codexloom.lark", Capabilities: []string{"receive_events"},
	})
	if err != nil {
		t.Fatal(err)
	}
	address, err := fixture.h.CreateAddress(AddressParams{
		Agent: "r0b-agent", ConnectionID: connection.ID, ExternalIdentity: "external-lark",
		TriggerPolicy: "all", ReplyPolicy: "final_answer", TrustDomain: "workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.connection = connection
	fixture.address = address
	return fixture
}

func TestLarkMigrateVerifyRollbackPreservesIdentity(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	secret := []byte("lark-app-secret-勿-泄露")
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FloorRaised || !strings.HasPrefix(result.CurrentRef, "managed:") || result.PreviousRef != "keychain:com.codexloom.lark" {
		t.Fatalf("migration result = %#v", result)
	}
	connections := fixture.h.ListConnections()
	var migrated PlatformConnection
	for _, candidate := range connections {
		if candidate.ID == fixture.connection.ID {
			migrated = candidate
		}
	}
	if migrated.ID != fixture.connection.ID || migrated.CredentialRef != result.CurrentRef {
		t.Fatalf("connection identity/reference changed incorrectly: %#v", migrated)
	}
	addresses, err := fixture.h.ListAddresses("r0b-agent")
	if err != nil || len(addresses) != 2 {
		t.Fatalf("addresses changed: %#v err=%v", addresses, err)
	}
	found := false
	for _, address := range addresses {
		if address.ID == fixture.address.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("original Lark address identity lost")
	}
	integrations, err := os.ReadFile(filepath.Join(fixture.dir, "integrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(integrations, secret) {
		t.Fatal("secret leaked into integrations durable state")
	}
	foundation, err := fixture.st.ReadStableFile("runtime-foundation.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(foundation, secret) {
		t.Fatal("secret leaked into the runtime foundation")
	}
	// Without an accepted exact R1 proof, Verify must fail closed.
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err == nil {
		t.Fatal("verify succeeded without an accepted Gateway exact proof")
	}
	rollback, err := fixture.h.RollbackLarkCredential(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.CurrentRef != "keychain:com.codexloom.lark" {
		t.Fatalf("rollback result = %#v", rollback)
	}
	if !fixture.st.CredentialFloorPresent() {
		t.Fatal("rollback lowered the credential floor")
	}
	after := fixture.h.ListConnections()
	for _, candidate := range after {
		if candidate.ID == fixture.connection.ID && candidate.CredentialRef != "keychain:com.codexloom.lark" {
			t.Fatalf("connection reference not restored: %#v", candidate)
		}
	}
}

func TestLarkMigrateVerifyAfterExactProof(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	attempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	fixture.h.mu.Unlock()
	if attempt == nil || attempt.Phase != gatewayAttemptAwaitingTargetProof {
		t.Fatalf("startup did not consume the typed plan: %#v", attempt)
	}
	proof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: proof}); err != nil {
		t.Fatal(err)
	}
	verified, err := fixture.h.VerifyLarkCredential(fixture.connection.ID)
	if err != nil || !verified.AlreadyMigrated || verified.RequiresProof {
		t.Fatalf("verify after exact target proof failed: %#v err=%v", verified, err)
	}
}

func TestLarkVerifyRejectsRecoveryProofAfterTargetFailure(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeGatewayServiceAdapter{
		applyResult:   gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: context.DeadlineExceeded},
		restoreResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	type launchResult struct {
		attempt gatewayTransitionAttempt
		err     error
	}
	result := make(chan launchResult, 1)
	spec := l2aLaunchSpec(t, &fixture, gatewayServiceManagerFake)
	go func() {
		attempt, err := fixture.h.startLarkGatewayLaunch(context.Background(), spec, gatewayAttemptMigration)
		result <- launchResult{attempt: attempt, err: err}
	}()
	var attempt gatewayTransitionAttempt
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fixture.h.mu.Lock()
		if current := fixture.h.gatewayState.Attempts[fixture.connection.ID]; current != nil {
			attempt = *current
		}
		fixture.h.mu.Unlock()
		if attempt.Phase == gatewayAttemptAwaitingRecoveryProof {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if attempt.Phase != gatewayAttemptAwaitingRecoveryProof {
		t.Fatalf("target failure did not reach recovery proof: %#v", attempt)
	}
	recoveryProof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.RecoveryGeneration, Build: attempt.Plan.Anchor.Descriptor.Build,
		ExecutableDigest: attempt.Plan.Anchor.Descriptor.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: recoveryProof}); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-result:
		if completed.err == nil || completed.attempt.Phase != gatewayAttemptRecovered {
			t.Fatalf("recovery result = %#v err=%v", completed.attempt, completed.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery attempt did not terminate")
	}
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err == nil {
		t.Fatal("verify accepted a legacy recovery proof as managed target success")
	}
}

func TestLarkMigratePlanPendingDurableAndReentrant(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	first, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.PlanPending || first.AlreadyMigrated {
		t.Fatalf("fresh migration must end durable in plan_pending: %#v", first)
	}
	record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != larkMigrationPhasePlanPending {
		t.Fatalf("record phase = %q", record.Phase)
	}
	// Re-entry before the plan is configured consumes no secret and stays
	// durable plan_pending.
	second, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.PlanPending || second.CurrentRef != first.CurrentRef {
		t.Fatalf("re-entered migrate did not stay plan_pending: %#v", second)
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) {
		return &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}, nil
	}
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	record, err = fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.Phase != larkMigrationPhaseCompleted {
		t.Fatalf("record did not complete: %#v err=%v", record, err)
	}
}

func timeNow() time.Time { return time.Now().UTC() }

func TestLarkMigrateDryRunZeroWrites(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun {
		t.Fatalf("dry run not marked: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(fixture.dir, credentials.DirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("dry run created credential store: %v", err)
	}
	if fixture.st.CredentialFloorPresent() {
		t.Fatal("dry run raised the credential floor")
	}
	if connectionRefByID(t, fixture.h, fixture.connection.ID) != "keychain:com.codexloom.lark" {
		t.Fatal("dry run changed the connection reference")
	}
}

func TestLarkMigrateIdempotent(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	first, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) {
		return &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}, nil
	}
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("different"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyMigrated || second.CurrentRef != first.CurrentRef {
		t.Fatalf("second migrate not idempotent: %#v vs %#v", second, first)
	}
}

func TestLarkRollbackReentrantAfterRefRestoredCrash(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) {
		return &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}, nil
	}
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the reference restore but before plan revoke,
	// credential deletion, and record removal.
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: "keychain:com.codexloom.lark"}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.advanceLarkMigrationPhase(fixture.connection.ID, larkMigrationPhaseRefRestored); err != nil {
		t.Fatal(err)
	}
	if ref := fixture.h.LarkGatewayLaunchPlanRef(fixture.connection.ID); ref == "" {
		t.Fatal("crash simulation did not leave a typed plan in place")
	}
	rollback, err := fixture.h.RollbackLarkCredential(fixture.connection.ID)
	if err != nil {
		t.Fatalf("re-entered rollback could not finish: %v", err)
	}
	if rollback.CurrentRef != "keychain:com.codexloom.lark" {
		t.Fatalf("rollback result = %#v", rollback)
	}
	if ref := fixture.h.LarkGatewayLaunchPlanRef(fixture.connection.ID); ref != "" {
		t.Fatalf("typed launch plan survived rollback: %q", ref)
	}
	if _, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID); !os.IsNotExist(err) {
		t.Fatalf("migration record survived rollback: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), credentials.DirectoryName))
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				t.Fatalf("managed credential file survived rollback: %s", entry.Name())
			}
		}
	}
}

func TestLarkMigrateIdempotentAfterCutover(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	attempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	fixture.h.mu.Unlock()
	proof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: proof}); err != nil {
		t.Fatal(err)
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	reopened.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatalf("preflight after successful cutover failed: %v", err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatalf("configure after successful cutover was not a no-op: %v", err)
	}
	again, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("different"), false)
	if err != nil || !again.AlreadyMigrated {
		t.Fatalf("migrate after successful cutover was not idempotent: %#v err=%v", again, err)
	}
}

func TestLarkMigrateRecordWriteCommittedButErrorKeepsCredential(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	fixture.h.larkMigrationRecordWriteForTest = func() error {
		return fmt.Errorf("injected dir fsync failure after commit")
	}
	secret := []byte("secret")
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, secret, false); err == nil {
		t.Fatal("migrate succeeded despite indeterminate record write")
	} else if !IsLarkMigrationIndeterminate(err) {
		t.Fatalf("committed-but-error record write was not a typed indeterminate: %v", err)
	}
	fixture.h.larkMigrationRecordWriteForTest = nil
	// The committed record must keep the managed credential for re-entry.
	record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.Phase != larkMigrationPhasePrepared {
		t.Fatalf("committed record was not retained: %#v err=%v", record, err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(credentials.Ref(record.CurrentRef)); err != nil {
		t.Fatalf("managed credential was deleted despite committed record: %v", err)
	}
	// Re-run migrate completes the migration without re-reading the secret.
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false)
	if err != nil || !result.PlanPending {
		t.Fatalf("re-run migrate did not resume plan_pending: %#v err=%v", result, err)
	}
}

func TestLarkMigrateConnectionWriteCommittedButErrorKeepsCredential(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	fixture.h.larkUpdateConnectionForTest = func(connectionID, credentialRef string) (PlatformConnection, error) {
		committed, commitErr := fixture.h.UpdateConnection(connectionID, ConnectionParams{CredentialRef: credentialRef})
		if commitErr != nil {
			return PlatformConnection{}, commitErr
		}
		return committed, fmt.Errorf("injected integrations fsync failure after commit")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
		t.Fatal("migrate succeeded despite indeterminate connection write")
	} else if !IsLarkMigrationIndeterminate(err) {
		t.Fatalf("committed-but-error connection write was not a typed indeterminate: %v", err)
	}
	fixture.h.larkUpdateConnectionForTest = nil
	record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.Phase != larkMigrationPhasePrepared {
		t.Fatalf("committed record was not retained: %#v err=%v", record, err)
	}
	if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != record.CurrentRef {
		t.Fatalf("connection reference was not committed: %q", ref)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(credentials.Ref(record.CurrentRef)); err != nil {
		t.Fatalf("managed credential was deleted despite committed reference: %v", err)
	}
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false)
	if err != nil || !result.PlanPending {
		t.Fatalf("re-run migrate did not resume plan_pending: %#v err=%v", result, err)
	}
}

func TestLarkMigrateRecordWriteAmbiguousReadbackRetainsAllArtifacts(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	connectionID := fixture.connection.ID
	recordPath := filepath.Join(fixture.dir, "lark-migrations", connectionID+".json")
	fixture.h.larkMigrationRecordWriteForTest = func() error {
		// The record rename committed, then the reported fsync error is
		// followed by an unreadable record: readback cannot classify the
		// outcome, so nothing may be compensated away.
		if err := os.WriteFile(recordPath, []byte("{corrupt"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("injected fsync failure after commit")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), connectionID, []byte("secret"), false); err == nil {
		t.Fatal("migrate succeeded despite ambiguous record outcome")
	} else if !IsLarkMigrationIndeterminate(err) {
		t.Fatalf("ambiguous record outcome was not a typed indeterminate: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), credentials.DirectoryName))
	if err != nil || len(entries) != 1 {
		t.Fatalf("credential was not retained despite ambiguous outcome: entries=%v err=%v", entries, err)
	}
	// The record artifact is retained (corrupt) and the reference must not move.
	if ref := connectionRefByID(t, fixture.h, connectionID); ref != "keychain:com.codexloom.lark" {
		t.Fatalf("connection reference changed on ambiguous outcome: %q", ref)
	}
	if _, err := fixture.h.readLarkMigrationRecord(connectionID); err == nil {
		t.Fatal("record was not retained in its corrupt/ambiguous state")
	}
}

func TestLarkMigrateCleanupRecordRemoveErrorRetainsSafeSide(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	fixture.h.larkUpdateConnectionForTest = func(string, string) (PlatformConnection, error) {
		return PlatformConnection{}, fmt.Errorf("injected connection persist failure (uncommitted)")
	}
	fixture.h.larkMigrationRecordRemoveForTest = func(string) error {
		return fmt.Errorf("injected record removal failure")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
		t.Fatal("migrate succeeded despite cleanup failure")
	} else if !IsLarkMigrationIndeterminate(err) {
		t.Fatalf("cleanup failure was not a typed indeterminate: %v", err)
	}
	fixture.h.larkMigrationRecordRemoveForTest = nil
	record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.Phase != larkMigrationPhasePrepared {
		t.Fatalf("record was not retained after cleanup failure: %#v err=%v", record, err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(credentials.Ref(record.CurrentRef)); err != nil {
		t.Fatalf("credential was deleted despite cleanup failure: %v", err)
	}
	// Crash/reopen keeps the safe side and the same migrate re-enters.
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false)
	if err != nil || !result.PlanPending {
		t.Fatalf("re-run migrate after reopen did not resume plan_pending: %#v err=%v", result, err)
	}
}

func TestLarkMigratePreparedResumeRequiresExistingCredential(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if err := fixture.st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := credentialStore.Put([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	record := larkCredentialMigrationRecord{
		Version: 1, ConnectionID: fixture.connection.ID, PreviousRef: "keychain:com.codexloom.lark",
		CurrentRef: string(ref), Phase: larkMigrationPhasePrepared, MigratedAt: now(),
	}
	if err := fixture.h.writeLarkMigrationRecord(record); err != nil {
		t.Fatal(err)
	}
	if err := credentialStore.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false); err == nil {
		t.Fatal("prepared resume advanced the binding to a missing managed credential")
	}
	if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != "keychain:com.codexloom.lark" {
		t.Fatalf("connection reference advanced to a dangling ref: %q", ref)
	}
}

func TestLarkMigrateRecordWriteValidUnequalRetainsAll(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	connectionID := fixture.connection.ID
	recordPath := filepath.Join(fixture.dir, "lark-migrations", connectionID+".json")
	fixture.h.larkMigrationRecordWriteForTest = func() error {
		// The intended record committed, then a valid but different record
		// (same CurrentRef, different MigratedAt) replaced it before the error.
		data, err := os.ReadFile(recordPath)
		if err != nil {
			t.Fatal(err)
		}
		var committed larkCredentialMigrationRecord
		if err := json.Unmarshal(data, &committed); err != nil {
			t.Fatal(err)
		}
		committed.MigratedAt = "different-timestamp"
		payload, err := json.Marshal(committed)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("injected fsync failure after commit")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), connectionID, []byte("secret"), false); err == nil {
		t.Fatal("migrate succeeded despite valid-unequal record outcome")
	} else if !IsLarkMigrationIndeterminate(err) {
		t.Fatalf("valid-unequal record outcome was not a typed indeterminate: %v", err)
	}
	record, err := fixture.h.readLarkMigrationRecord(connectionID)
	if err != nil || record.Phase != larkMigrationPhasePrepared {
		t.Fatalf("valid-unequal record was not retained: %#v err=%v", record, err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(credentials.Ref(record.CurrentRef)); err != nil {
		t.Fatalf("only credential was deleted despite valid-unequal record: %v", err)
	}
	// Re-entry must resume from the retained record/credential.
	fixture.h.larkMigrationRecordWriteForTest = nil
	result, err := fixture.h.MigrateLarkCredential(context.Background(), connectionID, nil, false)
	if err != nil || !result.PlanPending {
		t.Fatalf("re-run migrate did not resume plan_pending: %#v err=%v", result, err)
	}
}

func TestLarkMigrateRecordWriteAbsentCleansCredential(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	connectionID := fixture.connection.ID
	recordPath := filepath.Join(fixture.dir, "lark-migrations", connectionID+".json")
	fixture.h.larkMigrationRecordWriteForTest = func() error {
		// The record is absent at decision time: the effect provably did not
		// commit, so the orphan credential may be cleaned.
		if err := os.Remove(recordPath); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("injected fsync failure after commit")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), connectionID, []byte("secret"), false); err == nil {
		t.Fatal("migrate succeeded despite absent record outcome")
	} else if IsLarkMigrationIndeterminate(err) {
		t.Fatalf("proven-absent record write was misclassified as indeterminate: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), credentials.DirectoryName))
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan credential was not cleaned: entries=%v err=%v", entries, err)
	}
	if _, err := fixture.h.readLarkMigrationRecord(connectionID); !os.IsNotExist(err) {
		t.Fatalf("absent record was not cleaned: %v", err)
	}
}

func TestLarkMigrateThirdLegacyRefFailsClosedAfterReopen(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	connectionID := fixture.connection.ID
	record := larkCredentialMigrationRecord{
		Version: 1, ConnectionID: connectionID, PreviousRef: "keychain:com.codexloom.lark",
		CurrentRef: "managed:" + strings.Repeat("a", 64), Phase: larkMigrationPhasePrepared, MigratedAt: now(),
	}
	if err := fixture.h.writeLarkMigrationRecord(record); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.UpdateConnection(connectionID, ConnectionParams{CredentialRef: "keychain:com.codexloom.third"}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := fixture.h.MigrateLarkCredential(context.Background(), connectionID, []byte("secret"), false); err == nil {
			t.Fatal("migrate entered fresh Put on a third-party reference with a prepared record")
		}
		if ref := connectionRefByID(t, fixture.h, connectionID); ref != "keychain:com.codexloom.third" {
			t.Fatalf("connection reference changed on inconsistent tuple: %q", ref)
		}
		if _, err := fixture.h.readLarkMigrationRecord(connectionID); err != nil {
			t.Fatalf("migration record was overwritten on inconsistent tuple: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), credentials.DirectoryName))
		if err == nil && len(entries) != 0 {
			t.Fatalf("migrate created a credential on inconsistent tuple: %v", entries)
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read credentials directory: %v", err)
		}
		if attempt == 0 {
			fixture.h.Shutdown()
			fixture.h = nil
			reopened, err := Open(fixture.st)
			if err != nil {
				t.Fatal(err)
			}
			fixture.h = reopened
		}
	}
}

func TestLarkMigrateManagedPreparedMissingSecretDoesNotAdvance(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if err := fixture.st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := credentialStore.Put([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	record := larkCredentialMigrationRecord{
		Version: 1, ConnectionID: fixture.connection.ID, PreviousRef: "keychain:com.codexloom.lark",
		CurrentRef: string(ref), Phase: larkMigrationPhasePrepared, MigratedAt: now(),
	}
	if err := fixture.h.writeLarkMigrationRecord(record); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: string(ref)}); err != nil {
		t.Fatal(err)
	}
	if err := credentialStore.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, nil, false); err == nil {
		t.Fatal("managed prepared migration advanced without its managed credential")
	}
	after, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || after.Phase != larkMigrationPhasePrepared {
		t.Fatalf("prepared phase advanced without the managed credential: %#v err=%v", after, err)
	}
	if planRef := fixture.h.LarkGatewayLaunchPlanRef(fixture.connection.ID); planRef != "" {
		t.Fatalf("launch plan was frozen without the managed credential: %q", planRef)
	}
}

func TestLarkRollbackIntentDurableBeforeBindingRestore(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	migratedRef := connectionRefByID(t, fixture.h, fixture.connection.ID)
	record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.CurrentRef != migratedRef {
		t.Fatalf("migration setup: %#v err=%v", record, err)
	}
	fixture.h.larkUpdateConnectionForTest = func(string, string) (PlatformConnection, error) {
		return PlatformConnection{}, fmt.Errorf("injected restore failure before commit")
	}
	if _, err := fixture.h.RollbackLarkCredential(fixture.connection.ID); err == nil {
		t.Fatal("rollback succeeded despite injected restore failure")
	}
	fixture.h.larkUpdateConnectionForTest = nil
	// The rollback intent is durable even though the binding restore did not
	// run: migrate must refuse and rollback must re-enter.
	record, err = fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || record.Phase != larkMigrationPhaseRefRestored {
		t.Fatalf("rollback intent was not durable: %#v err=%v", record, err)
	}
	if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != migratedRef {
		t.Fatalf("binding restored despite injected failure: %q", ref)
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
		t.Fatal("migrate re-entered while rollback cleanup is pending")
	}
	after, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
	if err != nil || after.Phase != larkMigrationPhaseRefRestored || after.CurrentRef != migratedRef {
		t.Fatalf("migrate mutated the pending rollback state: %#v err=%v", after, err)
	}
	if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != migratedRef {
		t.Fatalf("migrate changed the reference during pending rollback: %q", ref)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(credentials.Ref(migratedRef)); err != nil {
		t.Fatalf("managed credential was removed during pending rollback: %v", err)
	}
	rollback, err := fixture.h.RollbackLarkCredential(fixture.connection.ID)
	if err != nil || rollback.CurrentRef != "keychain:com.codexloom.lark" {
		t.Fatalf("re-entered rollback could not finish: %#v err=%v", rollback, err)
	}
}

func TestLarkMigrateInconsistentCommittedPhaseWithPreviousRefFailsClosed(t *testing.T) {
	for _, phase := range []string{larkMigrationPhasePlanPending, larkMigrationPhaseCompleted} {
		t.Run(phase, func(t *testing.T) {
			fixture := newLarkFixture(t)
			defer fixture.close(t)
			if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
				t.Fatal(err)
			}
			record, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
			if err != nil {
				t.Fatal(err)
			}
			record.Phase = phase
			if err := fixture.h.writeLarkMigrationRecord(record); err != nil {
				t.Fatal(err)
			}
			// Simulate a legacy/inconsistent tuple: reference restored to the
			// previous value while the committed-phase record remains.
			if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: "keychain:com.codexloom.lark"}); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
				t.Fatal("migrate entered fresh Put on an inconsistent committed-phase tuple")
			}
			if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != "keychain:com.codexloom.lark" {
				t.Fatalf("migrate changed the inconsistent reference: %q", ref)
			}
			after, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID)
			if err != nil || after.Phase != phase || after.CurrentRef != record.CurrentRef {
				t.Fatalf("migrate overwrote the rollback anchor: %#v err=%v", after, err)
			}
			rollback, err := fixture.h.RollbackLarkCredential(fixture.connection.ID)
			if err != nil || rollback.CurrentRef != "keychain:com.codexloom.lark" {
				t.Fatalf("rollback could not finish the inconsistent tuple: %#v err=%v", rollback, err)
			}
			if _, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID); !os.IsNotExist(err) {
				t.Fatalf("rollback did not remove the record: %v", err)
			}
		})
	}
}

func TestLarkRollbackRejectsThirdPartyBindingDrift(t *testing.T) {
	for _, third := range []string{"keychain:com.codexloom.other", "managed:" + strings.Repeat("c", 64)} {
		fixture := newLarkFixture(t)
		defer fixture.close(t)
		if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
			t.Fatal(err)
		}
		fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) {
			return &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}, nil
		}
		executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
		if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: third}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.h.RollbackLarkCredential(fixture.connection.ID); err == nil {
			t.Fatalf("rollback accepted third-party binding drift %q", third)
		}
		if ref := connectionRefByID(t, fixture.h, fixture.connection.ID); ref != third {
			t.Fatalf("rollback changed the drifted reference: %q", ref)
		}
		if ref := fixture.h.LarkGatewayLaunchPlanRef(fixture.connection.ID); ref == "" {
			t.Fatal("rollback revoked the typed plan on third-party drift")
		}
		if _, err := fixture.h.readLarkMigrationRecord(fixture.connection.ID); err != nil {
			t.Fatalf("rollback removed the migration record on third-party drift: %v", err)
		}
	}
}

func TestLarkVerifyFreshnessRefreshedByContinuousExactHeartbeat(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.FinishLarkGatewayLaunchPlan(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	attempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	exact := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	fixture.h.mu.Unlock()
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: exact}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * gatewayProcessProofFreshness).Format(time.RFC3339Nano)
	fixture.h.mu.Lock()
	live := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	live.AcceptedProof.ObservedAt = old
	live.UpdatedAt = old
	fixture.h.mu.Unlock()
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err == nil {
		t.Fatal("verify passed with a stale accepted proof")
	}
	wrong := &GatewayProcessHeartbeatParams{AttemptID: "gattempt_other", Generation: "ggen_other", Build: "x", ExecutableDigest: "y"}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: wrong}); err == nil {
		t.Fatal("wrong identity heartbeat was accepted")
	}
	fixture.h.mu.Lock()
	staleAfterWrong := fixture.h.gatewayState.Attempts[fixture.connection.ID].AcceptedProof.ObservedAt
	fixture.h.mu.Unlock()
	if staleAfterWrong != old {
		t.Fatal("wrong identity heartbeat refreshed the accepted proof")
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: exact}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err != nil {
		t.Fatalf("verify failed after a fresh exact heartbeat: %v", err)
	}
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatalf("cutover-ready no-op failed after freshness refresh: %v", err)
	}
}

func TestLarkMigrateRejectsNonLarkAndArchived(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	other, err := fixture.h.CreateConnection(ConnectionParams{Provider: "slack", CredentialRef: "keychain:slack"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), other.ID, []byte("x"), false); err == nil {
		t.Fatal("non-Lark connection migrated")
	}
	fixture.h.mu.Lock()
	fixture.h.connections[fixture.connection.ID].ArchivedAt = now()
	fixture.h.mu.Unlock()
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("x"), false); err == nil {
		t.Fatal("archived connection migrated")
	}
}

func TestLarkMigrateBlockedByManualLatch(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryManual, "manual required")
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
		t.Fatal("migration bypassed a manual recovery latch")
	}
	var view PlatformConnection
	for _, candidate := range fixture.h.ListConnections() {
		if candidate.ID == fixture.connection.ID {
			view = candidate
		}
	}
	if view.Status != "degraded" || view.LastError != "manual required" {
		t.Fatalf("manual latch cleared or projection turned green: %#v", view)
	}
}

func TestLarkMigrateUpdateFailureDeletesCredential(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	fixture.h.larkUpdateConnectionForTest = func(string, string) (PlatformConnection, error) {
		return PlatformConnection{}, errf(500, "injected persist failure")
	}
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false); err == nil {
		t.Fatal("migration succeeded despite injected update failure")
	}
	fixture.h.larkUpdateConnectionForTest = nil
	if connectionRefByID(t, fixture.h, fixture.connection.ID) != "keychain:com.codexloom.lark" {
		t.Fatal("connection reference changed despite update failure")
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), credentials.DirectoryName))
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				t.Fatalf("managed credential file survived update failure: %s", entry.Name())
			}
		}
	}
	_ = credentialStore
}

func TestLarkVerifyDanglingRefFails(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	result, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, []byte("secret"), false)
	if err != nil {
		t.Fatal(err)
	}
	credentialStore, err := credentials.New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if err := credentialStore.Delete(credentials.Ref(result.CurrentRef)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err == nil {
		t.Fatal("verify succeeded with a dangling managed reference")
	}
}

func TestLarkMigrateSecretNeverInErrors(t *testing.T) {
	fixture := newLarkFixture(t)
	defer fixture.close(t)
	secret := []byte("super-secret-value")
	if _, err := fixture.h.MigrateLarkCredential(context.Background(), fixture.connection.ID, secret, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.VerifyLarkCredential(fixture.connection.ID); err != nil && strings.Contains(err.Error(), string(secret)) {
		t.Fatal("secret leaked into an error")
	}
}

func connectionRefByID(t *testing.T, h *Hub, connectionID string) string {
	t.Helper()
	for _, candidate := range h.ListConnections() {
		if candidate.ID == connectionID {
			return candidate.CredentialRef
		}
	}
	t.Fatalf("connection %s not found", connectionID)
	return ""
}
