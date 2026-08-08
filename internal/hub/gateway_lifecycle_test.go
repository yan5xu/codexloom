package hub

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

type gatewayMutationFixture struct {
	h          *Hub
	st         *store.Store
	dir        string
	connection PlatformConnection
	address    AgentAddress
}

func newGatewayMutationFixture(t *testing.T) gatewayMutationFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(); _ = st.Close() })
	h.mu.Lock()
	h.agents["agent-a"] = &Agent{ID: "agent-a", Name: "agent-a", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.agents["agent-b"] = &Agent{ID: "agent-b", Name: "agent-b", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.mu.Unlock()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "agent-a", ConnectionID: connection.ID, ExternalIdentity: "fixture://identity", TrustDomain: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	return gatewayMutationFixture{h: h, st: st, dir: dir, connection: connection, address: address}
}

func activateGatewayAttempt(t *testing.T, h *Hub, connectionID string) GatewayControl {
	t.Helper()
	control, err := h.InitializeGatewayControl(connectionID, GatewayDispositionStable, "")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := "future-attempt"
	control, err = h.CompareAndSwapGatewayControl(connectionID, control.Epoch, GatewayControlUpdate{ActiveAttemptID: &attemptID})
	if err != nil {
		t.Fatal(err)
	}
	return control
}

func lifecycleBytes(t *testing.T, dir string) []byte {
	t.Helper()
	var result []byte
	for _, name := range []string{"integrations.json", "runtime-foundation.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, []byte(name)...)
		result = append(result, 0)
		result = append(result, data...)
	}
	return result
}

func assertActiveFenceRejectsWithoutPersistence(t *testing.T, fixture gatewayMutationFixture, action func() error) {
	t.Helper()
	before := lifecycleBytes(t, fixture.dir)
	if err := action(); err == nil {
		t.Fatal("active gateway control accepted a stale mutation")
	}
	after := lifecycleBytes(t, fixture.dir)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected mutation changed durable state")
	}
}

func TestGatewayManualDispositionSurvivesHealthWritersAndReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.agents["agent-manual"] = &Agent{ID: "agent-manual", Name: "agent-manual", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	if err := h.persistAgentsLocked(); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.mu.Unlock()
	address, err := h.CreateAddress(AddressParams{
		Agent: "agent-manual", ConnectionID: connection.ID, ExternalIdentity: "fixture://manual", TrustDomain: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := h.InitializeGatewayControl(connection.ID, GatewayDispositionManualRecovery, "operator reconciliation required")
	if err != nil {
		t.Fatal(err)
	}
	h.saveRuntimeFoundation = func(gatewayFoundationDocument) error { return errors.New("injected observation persist failure") }
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err == nil {
		t.Fatal("heartbeat succeeded after observation persistence failed")
	}
	h.saveRuntimeFoundation = nil
	failedHealth, err := h.GatewayHealth(connection.ID)
	if err != nil || failedHealth.Disposition != GatewayDispositionManualRecovery || failedHealth.Status != "degraded" {
		t.Fatalf("manual latch after persist failure = %#v err=%v", failedHealth, err)
	}
	h.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err = Open(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.HeartbeatConnection(connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	h.MarkConnectionDisconnected(connection.ID, "connector command stream closed")
	item := OutboxItem{
		ID: "outbox-manual", AgentID: "agent-manual", AddressID: address.ID,
		State: "sending", AttemptToken: "attempt", IdempotencyKey: "manual",
		CreatedAt: now(), UpdatedAt: now(),
	}
	if err := st.AppendOutbox(item); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.outbox[item.ID] = &item
	h.outboxOrder = append(h.outboxOrder, item.ID)
	trigger := &Trigger{
		ID: "trigger-manual", AgentID: "agent-manual", Agent: "agent-manual", ConnectionID: connection.ID,
		Provider: "fixture", ResourceKind: "fixture", State: "pending", Version: 1, CreatedAt: now(), UpdatedAt: now(),
	}
	h.triggers[trigger.ID] = trigger
	if err := h.persistTriggersLocked(); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.mu.Unlock()
	if _, err := h.CompleteOutbox(connection.ID, item.ID, OutboxResultParams{
		AttemptToken: item.AttemptToken, Success: false, Error: "delivery failed",
	}); err != nil {
		t.Fatal(err)
	}
	h.applyTriggerObservation(trigger.ID, TriggerObservation{ObservedAt: now()})
	health, err := h.GatewayHealth(connection.ID)
	if err != nil || health.Disposition != GatewayDispositionManualRecovery || health.Status != "degraded" {
		t.Fatalf("manual health = %#v err=%v", health, err)
	}
	listed := h.ListConnections()
	if len(listed) != 1 || listed[0].Status != "degraded" || listed[0].LastError != "operator reconciliation required" {
		t.Fatalf("manual list projection = %#v", listed)
	}
	h.Shutdown()
	_ = st.Close()

	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err = Open(st)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	health, err = h.GatewayHealth(connection.ID)
	if err != nil || health.Disposition != GatewayDispositionManualRecovery {
		t.Fatalf("reopened manual health = %#v err=%v", health, err)
	}
	if _, err := h.CompareAndSwapGatewayControl(connection.ID, control.Epoch, GatewayControlUpdate{Disposition: GatewayDispositionStable}); err == nil {
		t.Fatal("generic control CAS cleared manual recovery")
	}
}

func TestGatewayControlPersistFailureAndReopenNeverRematchOldSnapshot(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.InitializeGatewayControl(connection.ID, GatewayDispositionStable, ""); err != nil {
		t.Fatal(err)
	}
	before, err := h.SnapshotGatewayBinding(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.saveIntegrations = func(integrationConfig) error { return errors.New("injected integrations persist failure") }
	if _, err := h.UpdateConnection(connection.ID, ConnectionParams{AccountRef: "changed"}); err == nil {
		t.Fatal("controlled mutation unexpectedly persisted")
	}
	h.saveIntegrations = nil
	if err := h.MatchGatewayBinding(before); err == nil {
		t.Fatal("old binding snapshot rematched after failed mutation")
	}
	h.Shutdown()
	_ = st.Close()

	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err = Open(st)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	if err := h.MatchGatewayBinding(before); err == nil {
		t.Fatal("old binding snapshot rematched after reopen")
	}
	health, err := h.GatewayHealth(connection.ID)
	if err != nil || !health.NeedsReconcile {
		t.Fatalf("failed mutation did not reopen fail closed: %#v err=%v", health, err)
	}
	current, err := h.SnapshotGatewayBinding(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	clear := false
	if _, err := h.CompareAndSwapGatewayControl(connection.ID, current.ControlEpoch, GatewayControlUpdate{
		Disposition: GatewayDispositionStable, NeedsReconcile: &clear,
	}); err == nil {
		t.Fatal("generic control CAS cleared durable reconciliation state")
	}
}

func TestGatewayLifecycleFailsClosedOnUnfencedBindingDrift(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.agents["agent-drift"] = &Agent{ID: "agent-drift", Name: "agent-drift", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.mu.Unlock()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "agent-drift", ConnectionID: connection.ID, ExternalIdentity: "fixture://drift", TrustDomain: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.InitializeGatewayControl(connection.ID, GatewayDispositionStable, ""); err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var config integrationConfig
	if err := st.LoadIntegrations(&config); err != nil {
		t.Fatal(err)
	}
	config.Addresses[address.ID].DisplayName = "unfenced old-writer mutation"
	if err := st.SaveIntegrations(config); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := Open(st); err == nil || !strings.Contains(err.Error(), "binding drift") {
		t.Fatalf("unfenced binding drift error = %v", err)
	}
}

func TestGatewayFoundationBlocksImplicitIntegrationNormalization(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.agents["agent-normalize"] = &Agent{ID: "agent-normalize", Name: "agent-normalize", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.mu.Unlock()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "agent-normalize", ConnectionID: connection.ID, ExternalIdentity: "fixture://normalize", TrustDomain: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.InitializeGatewayControl(connection.ID, GatewayDispositionStable, ""); err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var config integrationConfig
	if err := st.LoadIntegrations(&config); err != nil {
		t.Fatal(err)
	}
	config.Addresses[address.ID].Version = 0
	config.Addresses[address.ID].DMPolicy = ""
	if err := st.SaveIntegrations(config); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "integrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := Open(st); err == nil || !strings.Contains(err.Error(), "normalization requires explicit") {
		t.Fatalf("controlled normalization error = %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "integrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed controlled normalization rewrote integrations")
	}
}

func TestProvisioningConnectionIsNotGatewayEligibleUntilTypedCAS(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.GatewayEligibleConnectionIDs(); len(got) != 0 {
		t.Fatalf("unadopted connection became eligible: %v", got)
	}
	h.mu.Lock()
	h.agents["agent-provisioning"] = &Agent{ID: "agent-provisioning", Name: "agent-provisioning", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.mu.Unlock()
	if _, err := h.CreateAddress(AddressParams{
		Agent: "agent-provisioning", ConnectionID: connection.ID, ExternalIdentity: "fixture://provisioning", TrustDomain: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.GatewayEligibleConnectionIDs(); len(got) != 0 {
		t.Fatalf("bound but unadopted connection became eligible: %v", got)
	}
	control, err := h.InitializeGatewayControl(connection.ID, GatewayDispositionProvisioning, "binding incomplete")
	if err != nil {
		t.Fatal(err)
	}
	var envelope store.RuntimeFoundationEnvelope
	exists, err := st.LoadRuntimeFoundation(&envelope)
	if err != nil || !exists || envelope.SchemaVersion != store.RuntimeFoundationSchemaVersion || envelope.MinimumWriter != store.RuntimeWriterFloorR0 {
		t.Fatalf("first lifecycle record/floor = exists %v envelope %#v err=%v", exists, envelope, err)
	}
	if got := h.GatewayEligibleConnectionIDs(); len(got) != 0 {
		t.Fatalf("provisioning connection became eligible: %v", got)
	}
	if _, err := h.CompareAndSwapGatewayControl(connection.ID, control.Epoch, GatewayControlUpdate{Disposition: GatewayDispositionStable}); err != nil {
		t.Fatal(err)
	}
	if got := h.GatewayEligibleConnectionIDs(); len(got) != 1 || got[0] != connection.ID {
		t.Fatalf("stable connection eligibility = %v", got)
	}
}

func TestLegacyHubOpenAndShutdownDoesNotCreateGatewayFoundationOrFloor(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime-foundation.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy Hub lifecycle created writer floor: %v", err)
	}
}

func TestGatewayCoordinatorFencesEveryConnectionAndAddressMutation(t *testing.T) {
	t.Run("update connection", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			_, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{AccountRef: "changed"})
			return err
		})
	})
	t.Run("create address", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			_, err := fixture.h.CreateAddress(AddressParams{
				Agent: "agent-b", ConnectionID: fixture.connection.ID, ExternalIdentity: "fixture://second", TrustDomain: "fixture",
			})
			return err
		})
	})
	t.Run("update address", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			_, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "changed"})
			return err
		})
	})
	for _, action := range []string{AddressLifecycleArchive, AddressLifecycleDelete, AddressLifecycleTransfer} {
		action := action
		t.Run(action, func(t *testing.T) {
			fixture := newGatewayMutationFixture(t)
			activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
			assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
				version := fixture.address.Version
				params := AddressLifecycleParams{Action: action, ExpectedVersion: &version, Confirm: fixture.address.ID}
				if action == AddressLifecycleTransfer {
					params.TargetAgent = "agent-b"
				}
				_, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, params)
				return err
			})
		})
	}
	t.Run("restore address", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		version := fixture.address.Version
		archived, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
			Action: AddressLifecycleArchive, ExpectedVersion: &version, Confirm: fixture.address.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			version := archived.Address.Version
			_, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
				Action: AddressLifecycleRestore, ExpectedVersion: &version, Confirm: fixture.address.ID,
			})
			return err
		})
	})
	t.Run("rollback transfer", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		version := fixture.address.Version
		transferred, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
			Action: AddressLifecycleTransfer, TargetAgent: "agent-b", ExpectedVersion: &version, Confirm: fixture.address.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			version := transferred.Address.Version
			_, err := fixture.h.RollbackAddressTransfer(transferred.Operation.ID, AddressTransferRollbackParams{
				ExpectedVersion: &version, Confirm: transferred.Operation.ID,
			})
			return err
		})
	})
	t.Run("rollback created resources", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			return fixture.h.RollbackCreatedIntegration(nil, []string{fixture.address.ID})
		})
	})
	t.Run("restore resources", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			return fixture.h.RestoreIntegrationResources([]PlatformConnection{fixture.connection}, []AgentAddress{fixture.address})
		})
	})
	t.Run("consolidate identities", func(t *testing.T) {
		fixture := newGatewayMutationFixture(t)
		duplicate, err := fixture.h.CreateConnection(ConnectionParams{Provider: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		duplicateAddress, err := fixture.h.CreateAddress(AddressParams{
			Agent: "agent-a", ConnectionID: duplicate.ID, ExternalIdentity: fixture.address.ExternalIdentity, TrustDomain: "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		activateGatewayAttempt(t, fixture.h, fixture.connection.ID)
		assertActiveFenceRejectsWithoutPersistence(t, fixture, func() error {
			_, err := fixture.h.ConsolidateIntegrationIdentity(
				fixture.connection.ID, fixture.address.ID, []string{duplicate.ID}, []string{duplicateAddress.ID},
			)
			return err
		})
	})
}

func TestGatewayCoordinatorUsesStableOrderAndSerializesOverlappingSets(t *testing.T) {
	coordinator := newGatewayConnectionCoordinator()
	var observed []string
	coordinator.beforeLock = func(ids []string) { observed = append([]string(nil), ids...) }
	unlockFirst := coordinator.lock("conn-z", "conn-a", "conn-z", "")
	if !reflect.DeepEqual(observed, []string{"conn-a", "conn-z"}) {
		t.Fatalf("lock order = %v", observed)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		unlock := coordinator.lock("conn-z", "conn-a")
		close(entered)
		<-release
		unlock()
	}()
	select {
	case <-entered:
		t.Fatal("overlapping lock set entered before the first released")
	default:
	}
	unlockFirst()
	<-entered
	close(release)
	wait.Wait()
}

func TestGatewayMutationScopeRetriesWhenAddressConnectionChangesBeforeLock(t *testing.T) {
	fixture := newGatewayMutationFixture(t)
	second, err := fixture.h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := fixture.h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var hookMu sync.Mutex
	blockFirst := true
	var scopes [][]string
	fixture.h.gatewayCoordinator.beforeLock = func(ids []string) {
		hookMu.Lock()
		scopes = append(scopes, append([]string(nil), ids...))
		block := blockFirst
		if blockFirst {
			blockFirst = false
		}
		hookMu.Unlock()
		if block {
			close(started)
			<-release
		}
	}

	target := fixture.address
	target.ConnectionID = second.ID
	result := make(chan error, 1)
	go func() { result <- fixture.h.RestoreIntegrationResources(nil, []AgentAddress{target}) }()
	<-started
	intervening := fixture.address
	intervening.ConnectionID = third.ID
	if err := fixture.h.RestoreIntegrationResources(nil, []AgentAddress{intervening}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	got := fixture.h.addresses[fixture.address.ID].ConnectionID
	fixture.h.mu.Unlock()
	if got != second.ID {
		t.Fatalf("final address connection = %s, want %s", got, second.ID)
	}
	hookMu.Lock()
	defer hookMu.Unlock()
	wantInitial := normalizeGatewayConnectionIDs([]string{fixture.connection.ID, second.ID})
	wantIntervening := normalizeGatewayConnectionIDs([]string{fixture.connection.ID, third.ID})
	wantRetry := normalizeGatewayConnectionIDs([]string{second.ID, third.ID})
	if !containsGatewayScope(scopes, wantInitial) || !containsGatewayScope(scopes, wantIntervening) || !containsGatewayScope(scopes, wantRetry) {
		t.Fatalf("lock scopes = %v; want initial %v, intervening %v, retry %v", scopes, wantInitial, wantIntervening, wantRetry)
	}
}

func containsGatewayScope(scopes [][]string, target []string) bool {
	for _, scope := range scopes {
		if reflect.DeepEqual(scope, target) {
			return true
		}
	}
	return false
}

func TestCreateConnectionLocksGeneratedIdentityAndRemainsIneligible(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	var locked []string
	h.gatewayCoordinator.beforeLock = func(ids []string) { locked = append([]string(nil), ids...) }
	connection, err := h.CreateConnection(ConnectionParams{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != 1 || locked[0] != connection.ID {
		t.Fatalf("create lock identity = %v, connection = %s", locked, connection.ID)
	}
	if got := h.GatewayEligibleConnectionIDs(); len(got) != 0 {
		t.Fatalf("new connection became restart eligible: %v", got)
	}
	if !sort.StringsAreSorted(locked) {
		t.Fatalf("create lock order is unstable: %v", locked)
	}
}

func TestGatewayLifecycleUnknownAndOldCandidateStateFailClosedWithoutRewrite(t *testing.T) {
	states := map[string]string{
		"old candidate": `{"version":0,"controls":{},"observations":{}}`,
		"newer state":   `{"version":2,"controls":{},"observations":{}}`,
		"unknown field": `{"version":1,"controls":{},"observations":{},"futureAttemptSchema":true}`,
	}
	for name, lifecycle := range states {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "events"), 0o700); err != nil {
				t.Fatal(err)
			}
			integrations := `{"connections":{"conn-fixture":{"id":"conn-fixture","provider":"fixture","status":"disconnected","enabled":true,"createdAt":"2026-08-08T00:00:00Z","updatedAt":"2026-08-08T00:00:00Z"}},"addresses":{}}`
			foundation := []byte(`{"schemaVersion":1,"minimumWriter":1,"gatewayLifecycle":` + lifecycle + `}`)
			if err := os.WriteFile(filepath.Join(dir, "integrations.json"), []byte(integrations), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "runtime-foundation.json"), foundation, 0o600); err != nil {
				t.Fatal(err)
			}
			before := lifecycleBytes(t, dir)
			st, err := store.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Open(st); err == nil {
				_ = st.Close()
				t.Fatal("unsupported gateway lifecycle state opened")
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			after := lifecycleBytes(t, dir)
			if !bytes.Equal(before, after) {
				t.Fatal("failed lifecycle open rewrote durable state")
			}
		})
	}
}
