package hub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestWritableHubOwnsStoreForItsLifetime(t *testing.T) {
	t.Run("same Store cannot back two writable Hubs", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		first, err := Open(st)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { first.Shutdown(); _ = st.Close() }()
		second, err := Open(st)
		if err == nil {
			second.Shutdown()
			t.Fatal("second writable Hub claimed the same Store")
		}
	})

	t.Run("Store Close cannot revoke a live Hub owner", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		h, err := Open(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err == nil || !strings.Contains(err.Error(), "writable Hub") {
			h.Shutdown()
			t.Fatalf("Store.Close while Hub alive = %v", err)
		}
		if _, err := h.CreateConnection(ConnectionParams{Provider: "fixture"}); err != nil {
			h.Shutdown()
			t.Fatalf("failed Close disabled the live Hub owner: %v", err)
		}
		h.Shutdown()
		if err := st.Close(); err != nil {
			t.Fatalf("Store.Close after Hub shutdown: %v", err)
		}
	})

	t.Run("normal shutdown releases ownership for reopen", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		first, err := Open(st)
		if err != nil {
			t.Fatal(err)
		}
		first.Shutdown()
		second, err := Open(st)
		if err != nil {
			t.Fatalf("writable Hub ownership was not released: %v", err)
		}
		if _, err := first.CreateConnection(ConnectionParams{Provider: "retired"}); err == nil || !strings.Contains(err.Error(), "read-only") {
			second.Shutdown()
			t.Fatalf("retired Hub wrote under successor ownership: %v", err)
		}
		if _, err := first.materializeModelCatalog(); err == nil || !strings.Contains(err.Error(), "read-only") {
			second.Shutdown()
			t.Fatalf("retired Hub materialized a model catalog under successor ownership: %v", err)
		}
		if passive, err := OpenWithOptions(first.st, OpenOptions{Passive: true}); err == nil {
			passive.Shutdown()
			second.Shutdown()
			t.Fatal("retired writable Hub view became an independent passive canary")
		}
		second.Shutdown()
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPassiveHubRejectsWritableStore(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err == nil {
		h.Shutdown()
		t.Fatal("passive Hub silently converted a writable Store into an independent canary")
	}
}

func TestGatewayFoundationWriteThenErrorPoisonsCurrentHub(t *testing.T) {
	t.Run("initialize committed then errored", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		before, err := fixture.h.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixture.h.saveRuntimeFoundation = func(document gatewayFoundationDocument) error {
			if err := fixture.st.SaveRuntimeFoundation(document); err != nil {
				return err
			}
			return errors.New("post-rename directory fsync failed")
		}
		_, err = fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, "")
		if err == nil || !strings.Contains(err.Error(), "indeterminate") {
			t.Errorf("initialize error = %v, want typed indeterminate result", err)
		}
		var indeterminate *GatewayFoundationCommitIndeterminateError
		if !errors.As(err, &indeterminate) || !indeterminate.Committed {
			t.Errorf("initialize typed receipt = %#v, err=%v", indeterminate, err)
		}
		fixture.h.saveRuntimeFoundation = nil
		if err := fixture.h.MatchGatewayBinding(before); err == nil {
			t.Error("pre-commit snapshot matched in poisoned Hub")
		}
		if _, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, "retry"); err == nil {
			t.Error("poisoned Hub authorized a second initialize")
		}
		reopened, _ := reopenGatewayRepairFixture(t, fixture)
		snapshot, err := reopened.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil || snapshot.ControlEpoch != 1 {
			t.Fatalf("reopened committed initialize = %#v err=%v", snapshot, err)
		}
	})

	t.Run("initialize error without readable next still poisons", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		fixture.h.saveRuntimeFoundation = func(gatewayFoundationDocument) error {
			return errors.New("write outcome unavailable")
		}
		_, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, "")
		var indeterminate *GatewayFoundationCommitIndeterminateError
		if !errors.As(err, &indeterminate) || indeterminate.Committed {
			t.Fatalf("uncommitted typed receipt = %#v err=%v", indeterminate, err)
		}
		health, healthErr := fixture.h.GatewayHealth(fixture.connection.ID)
		if healthErr != nil || health.Status != "degraded" || !strings.Contains(health.Error, "reopen") {
			t.Fatalf("poisoned health without control = %#v err=%v", health, healthErr)
		}
		listed := fixture.h.ListConnections()
		if len(listed) != 1 || listed[0].Status != "degraded" {
			t.Fatalf("poisoned list without control = %#v", listed)
		}
	})

	t.Run("CAS committed then errored", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		control, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, "")
		if err != nil {
			t.Fatal(err)
		}
		before, err := fixture.h.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixture.h.saveRuntimeFoundation = func(document gatewayFoundationDocument) error {
			if err := fixture.st.SaveRuntimeFoundation(document); err != nil {
				return err
			}
			return errors.New("post-rename directory fsync failed")
		}
		_, err = fixture.h.CompareAndSwapGatewayControl(fixture.connection.ID, control.Epoch, GatewayControlUpdate{Reason: "next"})
		if err == nil || !strings.Contains(err.Error(), "indeterminate") {
			t.Errorf("CAS error = %v, want typed indeterminate result", err)
		}
		var indeterminate *GatewayFoundationCommitIndeterminateError
		if !errors.As(err, &indeterminate) || !indeterminate.Committed {
			t.Errorf("CAS typed receipt = %#v, err=%v", indeterminate, err)
		}
		fixture.h.saveRuntimeFoundation = nil
		if err := fixture.h.MatchGatewayBinding(before); err == nil {
			t.Error("old epoch matched after an indeterminate committed CAS")
		}
		if _, err := fixture.h.CompareAndSwapGatewayControl(fixture.connection.ID, control.Epoch, GatewayControlUpdate{Reason: "retry"}); err == nil {
			t.Error("poisoned Hub authorized a second CAS")
		}
		reopened, _ := reopenGatewayRepairFixture(t, fixture)
		current, err := reopened.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil || current.ControlEpoch != control.Epoch+1 {
			t.Fatalf("reopened committed CAS = %#v err=%v", current, err)
		}
		if err := reopened.MatchGatewayBinding(before); err == nil {
			t.Error("old snapshot rematched after committed CAS reopen")
		}
	})

	t.Run("mutation fence committed then errored", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		if _, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, ""); err != nil {
			t.Fatal(err)
		}
		before, err := fixture.h.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixture.h.saveRuntimeFoundation = func(document gatewayFoundationDocument) error {
			if err := fixture.st.SaveRuntimeFoundation(document); err != nil {
				return err
			}
			return errors.New("post-rename directory fsync failed")
		}
		if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{AccountRef: "must-not-apply"}); err == nil || !strings.Contains(err.Error(), "indeterminate") {
			t.Errorf("mutation fence error = %v", err)
		}
		fixture.h.saveRuntimeFoundation = nil
		if err := fixture.h.MatchGatewayBinding(before); err == nil {
			t.Error("old snapshot matched after indeterminate mutation fence")
		}
		if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{AccountRef: "retry"}); err == nil {
			t.Error("poisoned Hub authorized a binding mutation")
		}
		reopened, _ := reopenGatewayRepairFixture(t, fixture)
		health, err := reopened.GatewayHealth(fixture.connection.ID)
		if err != nil || !health.NeedsReconcile {
			t.Fatalf("reopened committed mutation fence = %#v err=%v", health, err)
		}
	})

	t.Run("ambiguous readback poisons", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		control, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, "")
		if err != nil {
			t.Fatal(err)
		}
		before, err := fixture.h.SnapshotGatewayBinding(fixture.connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixture.h.saveRuntimeFoundation = func(document gatewayFoundationDocument) error {
			if err := fixture.st.SaveRuntimeFoundation(document); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(fixture.dir, "runtime-foundation.json"), []byte("{"), 0o600); err != nil {
				return err
			}
			return errors.New("post-rename directory fsync failed")
		}
		_, err = fixture.h.CompareAndSwapGatewayControl(fixture.connection.ID, control.Epoch, GatewayControlUpdate{Reason: "ambiguous"})
		if err == nil || !strings.Contains(err.Error(), "indeterminate") {
			t.Errorf("ambiguous CAS error = %v", err)
		}
		var indeterminate *GatewayFoundationCommitIndeterminateError
		if !errors.As(err, &indeterminate) || indeterminate.Committed || indeterminate.ReadbackErr == nil {
			t.Errorf("ambiguous typed receipt = %#v, err=%v", indeterminate, err)
		}
		fixture.h.saveRuntimeFoundation = nil
		if err := fixture.h.MatchGatewayBinding(before); err == nil {
			t.Error("old snapshot matched after ambiguous foundation commit")
		}
		if got := fixture.h.GatewayEligibleConnectionIDs(); len(got) != 0 {
			t.Errorf("poisoned Hub returned eligible gateways: %v", got)
		}
	})
}

func reopenGatewayRepairFixture(t *testing.T, fixture gatewayMutationFixture) (*Hub, *store.Store) {
	t.Helper()
	fixture.h.Shutdown()
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(fixture.dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(); _ = st.Close() })
	return h, st
}

func TestHeartbeatCapabilitiesRemainObservedNotConfigured(t *testing.T) {
	fixture := newGatewayMutationFixture(t)
	configured, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{Capabilities: []string{"configured_send"}})
	if err != nil {
		t.Fatal(err)
	}
	control, err := fixture.h.InitializeGatewayControl(configured.ID, GatewayDispositionStable, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.h.SnapshotGatewayBinding(configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixture.h.HeartbeatConnection(configured.ID, ConnectionHeartbeatParams{
		Status: "connected", Capabilities: []string{"observed_receive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Capabilities, ",") != "configured_send" {
		t.Fatalf("heartbeat rewrote configured capabilities: %v", got.Capabilities)
	}
	if err := fixture.h.MatchGatewayBinding(before); err != nil {
		t.Fatalf("observation invalidated configured binding snapshot: %v", err)
	}
	fixture.h.mu.Lock()
	observation := fixture.h.gatewayLifecycle.Observations[configured.ID]
	fixture.h.mu.Unlock()
	if observation == nil || strings.Join(observation.Capabilities, ",") != "observed_receive" {
		t.Fatalf("private observed capabilities = %#v", observation)
	}
	attempt := "future-attempt"
	active, err := fixture.h.CompareAndSwapGatewayControl(configured.ID, control.Epoch, GatewayControlUpdate{ActiveAttemptID: &attempt})
	if err != nil {
		t.Fatal(err)
	}
	activeSnapshot, err := fixture.h.SnapshotGatewayBinding(configured.ID)
	if err != nil || activeSnapshot.ControlEpoch != active.Epoch {
		t.Fatalf("active snapshot = %#v err=%v", activeSnapshot, err)
	}
	if _, err := fixture.h.HeartbeatConnection(configured.ID, ConnectionHeartbeatParams{Status: "connected", Capabilities: []string{"late"}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.MatchGatewayBinding(activeSnapshot); err != nil {
		t.Fatalf("late observation drifted active binding: %v", err)
	}
	if _, err := fixture.h.UpdateConnection(configured.ID, ConnectionParams{Capabilities: []string{"stale-write"}}); err == nil {
		t.Fatal("active attempt did not fence configured capability mutation")
	}
	listed := fixture.h.ListConnections()
	if len(listed) != 1 || strings.Join(listed[0].Capabilities, ",") != "configured_send" {
		t.Fatalf("late heartbeat rewrote configured capabilities: %#v", listed)
	}
	fixture.h.Shutdown()
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st, err = store.Open(fixture.dir)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h, err = Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { fixture.h.Shutdown(); _ = fixture.st.Close() }()
	listed = fixture.h.ListConnections()
	if len(listed) != 1 || strings.Join(listed[0].Capabilities, ",") != "configured_send" {
		t.Fatalf("reopen configured capabilities: %#v", listed)
	}

	manualFixture := newGatewayMutationFixture(t)
	manualConfigured, err := manualFixture.h.UpdateConnection(manualFixture.connection.ID, ConnectionParams{Capabilities: []string{"manual_configured"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manualFixture.h.InitializeGatewayControl(manualConfigured.ID, GatewayDispositionManualRecovery, "proof required"); err != nil {
		t.Fatal(err)
	}
	manualHeartbeat, err := manualFixture.h.HeartbeatConnection(manualConfigured.ID, ConnectionHeartbeatParams{
		Status: "connected", Capabilities: []string{"manual_observed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manualHealth, err := manualFixture.h.GatewayHealth(manualConfigured.ID)
	if err != nil || manualHealth.Disposition != GatewayDispositionManualRecovery || manualHealth.Status != "degraded" ||
		strings.Join(manualHeartbeat.Capabilities, ",") != "manual_configured" {
		t.Fatalf("manual capability/health projection = connection %#v health %#v err=%v", manualHeartbeat, manualHealth, err)
	}
}

func TestGatewayHealthReducerKeepsLifecycleAheadOfLateObservation(t *testing.T) {
	legacyFixture := newGatewayMutationFixture(t)
	if _, err := legacyFixture.h.HeartbeatConnection(legacyFixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	legacyDisabled := false
	if _, err := legacyFixture.h.UpdateConnection(legacyFixture.connection.ID, ConnectionParams{Enabled: &legacyDisabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyFixture.h.HeartbeatConnection(legacyFixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	if legacyFixture.h.gatewayLifecycle.Controls[legacyFixture.connection.ID] != nil {
		t.Fatal("legacy lifecycle projection unexpectedly created a control")
	}
	legacyHealth, err := legacyFixture.h.GatewayHealth(legacyFixture.connection.ID)
	if err != nil || legacyHealth.Status != "disconnected" || !strings.Contains(legacyHealth.Error, "disabled") {
		t.Fatalf("uncontrolled disabled connection health = %#v err=%v", legacyHealth, err)
	}
	legacyListed := legacyFixture.h.ListConnections()
	if len(legacyListed) != 1 || legacyListed[0].Status != "disconnected" {
		t.Fatalf("uncontrolled disabled ListConnections = %#v", legacyListed)
	}

	fixture := newGatewayMutationFixture(t)
	if _, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionStable, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	health, err := fixture.h.GatewayHealth(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "disconnected" || !strings.Contains(health.Error, "disabled") {
		t.Fatalf("disabled connection painted healthy by late observation: %#v", health)
	}
	listed := fixture.h.ListConnections()
	if len(listed) != 1 || listed[0].Status != "disconnected" {
		t.Fatalf("ListConnections disagrees with lifecycle health: %#v", listed)
	}

	fixture.h.mu.Lock()
	connection := fixture.h.connections[fixture.connection.ID]
	connection.Enabled = true
	connection.ArchivedAt = now()
	connection.SupersededBy = "conn-successor"
	control := fixture.h.gatewayLifecycle.Controls[fixture.connection.ID]
	binding, bindingErr := fixture.h.gatewayBindingIdentityLocked(fixture.connection.ID)
	if bindingErr != nil {
		fixture.h.mu.Unlock()
		t.Fatal(bindingErr)
	}
	control.Binding = binding
	if err := fixture.h.persistIntegrationsLocked(); err != nil {
		fixture.h.mu.Unlock()
		t.Fatal(err)
	}
	if err := fixture.h.saveGatewayLifecycleLocked(fixture.h.gatewayLifecycle); err != nil {
		fixture.h.mu.Unlock()
		t.Fatal(err)
	}
	fixture.h.mu.Unlock()
	health, err = fixture.h.GatewayHealth(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "disconnected" || !strings.Contains(health.Error, "superseded") {
		t.Fatalf("superseded connection painted healthy by old observation: %#v", health)
	}
	fixture.h.Shutdown()
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := store.Open(fixture.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedStore.Close()
	reopened, err := Open(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()
	listed = reopened.ListConnections()
	if len(listed) != 1 || listed[0].Status != "disconnected" || !strings.Contains(listed[0].LastError, "superseded") {
		t.Fatalf("reopened lifecycle health = %#v", listed)
	}
}

type epochOnlyGatewayManualClearer interface {
	ClearGatewayManualDisposition(string, uint64) (GatewayControl, error)
}

func TestR0ExposesNoEpochOnlyManualRecoveryClear(t *testing.T) {
	fixture := newGatewayMutationFixture(t)
	control, err := fixture.h.InitializeGatewayControl(fixture.connection.ID, GatewayDispositionManualRecovery, "proof required")
	if err != nil {
		t.Fatal(err)
	}
	if _, exposed := any(fixture.h).(epochOnlyGatewayManualClearer); exposed {
		t.Error("R0 exposes an epoch-only manual recovery clear primitive")
	}
	if _, err := fixture.h.CompareAndSwapGatewayControl(fixture.connection.ID, control.Epoch, GatewayControlUpdate{Disposition: GatewayDispositionStable}); err == nil {
		t.Error("correct epoch cleared manual recovery without accepted proof")
	}
}

func TestR0CannotClearActiveAttemptWithoutAcceptedProof(t *testing.T) {
	fixture := newGatewayMutationFixture(t)
	control := activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
	empty := ""
	if _, err := fixture.h.CompareAndSwapGatewayControl(fixture.connection.ID, control.Epoch, GatewayControlUpdate{ActiveAttemptID: &empty}); err == nil {
		t.Error("correct epoch cleared an active attempt without accepted proof")
	}
}
