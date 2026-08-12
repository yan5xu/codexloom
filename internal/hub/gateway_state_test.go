package hub

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

type r0bFixture struct {
	dir        string
	st         *store.Store
	h          *Hub
	connection PlatformConnection
	address    AgentAddress
}

func newR0bFixture(t *testing.T) r0bFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	seedInboxAgent(t, h, "agent-r0b", "r0b-agent")
	connection, err := h.CreateConnection(ConnectionParams{Provider: "test", AccountRef: "acct", ScopeRef: "scope", Capabilities: []string{"configured"}})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{Agent: "r0b-agent", ConnectionID: connection.ID, ExternalIdentity: "external-r0b", TriggerPolicy: "all", ReplyPolicy: "final_answer", TrustDomain: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	return r0bFixture{dir: dir, st: st, h: h, connection: connection, address: address}
}

func (f *r0bFixture) close(t *testing.T) {
	t.Helper()
	if f.h != nil {
		f.h.Shutdown()
		f.h = nil
	}
	if f.st != nil {
		if err := f.st.Close(); err != nil {
			t.Fatal(err)
		}
		f.st = nil
	}
}

func initializeAdoptedR0bControl(t *testing.T, fixture *r0bFixture, recovery gatewayRecoveryDisposition, reason string) gatewayControl {
	t.Helper()
	control, err := fixture.h.initializeGatewayControl(fixture.connection.ID, gatewayBindingAdopted, recovery, reason)
	if err != nil {
		t.Fatal(err)
	}
	return control
}

func TestR0bLegacyOpenPassiveAndShutdownRemainDormant(t *testing.T) {
	fixture := newR0bFixture(t)
	fixture.h.Shutdown()
	fixture.h = nil
	if _, err := os.Stat(filepath.Join(fixture.dir, "runtime-foundation.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy activity created Gateway foundation: %v", err)
	}
	before := snapshotHubTree(t, fixture.dir)
	ro, err := store.OpenWithOptions(fixture.dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	passive, err := OpenWithOptions(ro, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	passive.Shutdown()
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	after := snapshotHubTree(t, fixture.dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("passive Gateway open mutated durable tree: before=%v after=%v", before, after)
	}
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st = nil
}

func TestR0bProvisioningRequiresExplicitBindingAdoption(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	control, err := fixture.h.initializeGatewayControl(fixture.connection.ID, gatewayBindingProvisioning, gatewayRecoveryNone, "binding incomplete")
	if err != nil {
		t.Fatal(err)
	}
	got := fixture.h.gatewayEligibleConnectionIDs()
	if control.Epoch != 1 || len(got) != 0 {
		t.Fatalf("provisioning control became eligible: control=%#v eligible=%v", control, got)
	}
	data, err := os.ReadFile(filepath.Join(fixture.dir, "runtime-foundation.json"))
	if err != nil || !bytes.Contains(data, []byte(`"minimumWriter": 2`)) || !bytes.Contains(data, []byte(`"provisioning"`)) {
		t.Fatalf("first control/floor commit = %s, err=%v", data, err)
	}
	snapshot, err := fixture.h.snapshotGatewayProvisioningBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := fixture.h.adoptGatewayBinding(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Epoch != 2 || adopted.Lifecycle != gatewayBindingAdopted {
		t.Fatalf("adopted control = %#v", adopted)
	}
	if got := fixture.h.gatewayEligibleConnectionIDs(); len(got) != 1 || got[0] != fixture.connection.ID {
		t.Fatalf("adopted binding eligibility = %v", got)
	}
}

func TestR0bManagedCreationPersistsProvisioningBeforeVisibility(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer func() { h.Shutdown(); _ = st.Close() }()
	seedInboxAgent(t, h, "agent-managed-r0b", "managed-r0b")
	created, control, err := h.createProvisioningGatewayConnection(PlatformConnection{
		ID: "conn-managed-r0b", Provider: "test", AccountRef: "account", Capabilities: []string{"configured"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.Lifecycle != gatewayBindingProvisioning || created.Status != "connecting" || len(h.gatewayEligibleConnectionIDs()) != 0 {
		t.Fatalf("managed creation = connection %#v control %#v eligible %v", created, control, h.gatewayEligibleConnectionIDs())
	}
	foundation, err := os.ReadFile(filepath.Join(dir, "runtime-foundation.json"))
	if err != nil || !bytes.Contains(foundation, []byte(created.ID)) || !bytes.Contains(foundation, []byte(`"provisioning"`)) {
		t.Fatalf("provisioning control missing before binding: %s, err=%v", foundation, err)
	}
	address, err := h.CreateAddress(AddressParams{Agent: "managed-r0b", ConnectionID: created.ID, ExternalIdentity: "managed-external", TriggerPolicy: "all", TrustDomain: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if address.ConnectionID != created.ID || len(h.gatewayEligibleConnectionIDs()) != 0 {
		t.Fatalf("binding creation prematurely enabled Gateway: address=%#v eligible=%v", address, h.gatewayEligibleConnectionIDs())
	}
	snapshot, err := h.snapshotGatewayProvisioningBinding(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.adoptGatewayBinding(snapshot); err != nil {
		t.Fatal(err)
	}
	if got := h.gatewayEligibleConnectionIDs(); !reflect.DeepEqual(got, []string{created.ID}) {
		t.Fatalf("adopted managed creation eligibility = %v", got)
	}
}

func TestR0bManualRecoveryAndConfiguredCapabilitiesSurviveOrdinaryWritersAndReopen(t *testing.T) {
	fixture := newR0bFixture(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryManual, "operator proof required")
	observed, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", Capabilities: []string{"provider-observed"}})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != "degraded" || observed.LastError != "operator proof required" {
		t.Fatalf("heartbeat cleared manual recovery: %#v", observed)
	}
	if !reflect.DeepEqual(observed.Capabilities, []string{"configured"}) {
		t.Fatalf("observation rewrote configured capabilities: %v", observed.Capabilities)
	}
	heartbeatAt := observed.LastHeartbeatAt
	fixture.h.MarkConnectionDisconnected(fixture.connection.ID, "command stream closed")
	view := fixture.h.ListConnections()[0]
	if view.Status != "degraded" || view.LastError != "operator proof required" {
		t.Fatalf("disconnect writer cleared manual recovery: %#v", view)
	}
	outbox, err := fixture.h.CreateOutbox(OutboxParams{
		Agent: "r0b-agent", AddressID: fixture.address.ID,
		Conversation: ConversationRef{ConversationID: "conversation-r0b"},
		Content:      MessageContent{Text: "failure observation"}, IdempotencyKey: "r0b-manual-outbox",
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := fixture.h.ClaimNextOutbox(fixture.connection.ID)
	if err != nil || command == nil || command.OutboxItem.ID != outbox.ID {
		t.Fatalf("claim outbox = %#v, err=%v", command, err)
	}
	if _, err := fixture.h.CompleteOutbox(fixture.connection.ID, outbox.ID, OutboxResultParams{AttemptToken: command.OutboxItem.AttemptToken, Error: "connector failed"}); err != nil {
		t.Fatal(err)
	}
	view = fixture.h.ListConnections()[0]
	if view.Status != "degraded" || view.LastError != "operator proof required" {
		t.Fatalf("Outbox failure writer cleared manual recovery: %#v", view)
	}
	if view.LastHeartbeatAt != heartbeatAt {
		t.Fatalf("non-heartbeat writer advanced heartbeat: before=%q after=%q", heartbeatAt, view.LastHeartbeatAt)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if _, err := fixture.h.setGatewayRecovery(fixture.connection.ID, control.Epoch, gatewayRecoveryNone, ""); err == nil {
		t.Fatal("R0b exposed an epoch-only manual recovery clear")
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	view = reopened.ListConnections()[0]
	if view.Status != "degraded" || view.LastError != "operator proof required" || !reflect.DeepEqual(view.Capabilities, []string{"configured"}) {
		t.Fatalf("reopen lost recovery/config separation: %#v", view)
	}
}

func TestR0bReturnedConfiguredSlicesCannotMutateHubBinding(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	snapshot, err := fixture.h.snapshotGatewayBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	connections := fixture.h.ListConnections()
	addresses, err := fixture.h.ListAddresses("r0b-agent")
	if err != nil {
		t.Fatal(err)
	}
	connections[0].Capabilities[0] = "caller-mutated"
	addresses[0].AllowActors = append(addresses[0].AllowActors, "caller-mutated")
	if err := fixture.h.matchGatewayBinding(snapshot); err != nil {
		t.Fatalf("returned slice alias changed configured binding: %v", err)
	}
	stored := fixture.h.ListConnections()[0]
	if !reflect.DeepEqual(stored.Capabilities, []string{"configured"}) {
		t.Fatalf("configured capabilities were aliased: %v", stored.Capabilities)
	}
}

func TestR0bReducerKeepsDisabledConnectionRedAfterLateConnectedObservation(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	view, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "disconnected" || !strings.Contains(view.LastError, "disabled") {
		t.Fatalf("disabled reducer projection = %#v", view)
	}
	view, err = fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "disconnected" || !strings.Contains(view.LastError, "disabled") {
		t.Fatalf("late observation painted disabled Connection green: %#v", view)
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	view = reopened.ListConnections()[0]
	if view.Status != "disconnected" || !strings.Contains(view.LastError, "disabled") {
		t.Fatalf("reopen reducer differs: %#v", view)
	}
}

func TestR0bBindingMutationBumpsEpochAndInvalidatesOldSnapshot(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	snapshot, err := fixture.h.snapshotGatewayBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "updated" {
		t.Fatalf("updated Address = %#v", updated)
	}
	if err := fixture.h.matchGatewayBinding(snapshot); err == nil {
		t.Fatal("old configured binding snapshot still matched after mutation")
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.Epoch <= snapshot.ControlEpoch || control.Recovery != gatewayRecoveryNone || control.Binding.Addresses[0].DisplayName != "updated" {
		t.Fatalf("mutation control = %#v", control)
	}
}

func TestR0bAddressTransferAndRollbackStayInsideControlEpoch(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	seedInboxAgent(t, fixture.h, "agent-r0b-two", "r0b-agent-two")
	control := initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	transferred, err := fixture.h.ApplyAddressLifecycle(fixture.address.ID, AddressLifecycleParams{
		Action: AddressLifecycleTransfer, TargetAgent: "r0b-agent-two", ExpectedVersion: intPointer(fixture.address.Version), Confirm: fixture.address.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transferred.Operation == nil || transferred.Address.AgentID != "agent-r0b-two" {
		t.Fatalf("transfer = %#v", transferred)
	}
	afterTransfer := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if afterTransfer.Epoch <= control.Epoch || afterTransfer.Binding.Addresses[0].AgentID != "agent-r0b-two" {
		t.Fatalf("transfer control = %#v", afterTransfer)
	}
	rolledBack, err := fixture.h.RollbackAddressTransfer(transferred.Operation.ID, AddressTransferRollbackParams{
		ExpectedVersion: intPointer(transferred.Address.Version), Confirm: transferred.Operation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Address.AgentID != "agent-r0b" {
		t.Fatalf("rollback = %#v", rolledBack)
	}
	afterRollback := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if afterRollback.Epoch <= afterTransfer.Epoch || afterRollback.Binding.Addresses[0].AgentID != "agent-r0b" || afterRollback.Recovery != gatewayRecoveryNone {
		t.Fatalf("rollback control = %#v", afterRollback)
	}
}

func TestR0bProvisioningRollbackRemovesControlButNeverLowersFloor(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	connection, err := fixture.h.CreateConnection(ConnectionParams{Provider: "provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.initializeGatewayControl(connection.ID, gatewayBindingProvisioning, gatewayRecoveryNone, "incomplete"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RollbackCreatedIntegration([]string{connection.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if fixture.h.gatewayState.Controls[connection.ID] != nil {
		t.Fatal("provisioning rollback retained an orphan control")
	}
	data, err := os.ReadFile(filepath.Join(fixture.dir, "runtime-foundation.json"))
	if err != nil || !bytes.Contains(data, []byte(`"minimumWriter": 2`)) {
		t.Fatalf("provisioning rollback lowered writer floor: %s, err=%v", data, err)
	}
}

func TestR0bManualFenceRejectsAddressMutationWithoutDurableChange(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryManual, "operator proof required")
	path := filepath.Join(fixture.dir, "integrations.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "must-not-land"}); err == nil {
		t.Fatal("manual recovery did not fence Address mutation")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("stale mutation changed integrations bytes")
	}
}

func TestR0bCoordinatorSortsMultiConnectionMutationDomain(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	second, err := fixture.h.CreateConnection(ConnectionParams{Provider: "test-two"})
	if err != nil {
		t.Fatal(err)
	}
	secondAddress, err := fixture.h.CreateAddress(AddressParams{Agent: "r0b-agent", ConnectionID: second.ID, ExternalIdentity: "external-two", TriggerPolicy: "all", TrustDomain: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.initializeGatewayControl(fixture.connection.ID, gatewayBindingAdopted, gatewayRecoveryNone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.initializeGatewayControl(second.ID, gatewayBindingAdopted, gatewayRecoveryNone, ""); err != nil {
		t.Fatal(err)
	}
	var locked []string
	fixture.h.gatewayCoordinator.beforeLock = func(ids []string) { locked = append([]string(nil), ids...) }
	moved := cloneAgentAddressValue(fixture.address)
	moved.ConnectionID = second.ID
	err = fixture.h.RestoreIntegrationResources(nil, []AgentAddress{moved})
	fixture.h.gatewayCoordinator.beforeLock = nil
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fixture.connection.ID, second.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(locked, want) {
		t.Fatalf("multi-Connection lock order = %v, want %v", locked, want)
	}
	_ = secondAddress
}

func TestR0bConnectionDomainBlocksConcurrentBindingMutation(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	held := fixture.h.gatewayCoordinatorForUse().lock(fixture.connection.ID)
	entered := make(chan struct{}, 1)
	fixture.h.gatewayCoordinator.beforeLock = func(ids []string) {
		if len(ids) == 1 && ids[0] == fixture.connection.ID {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "serialized"})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		held()
		t.Fatal("binding mutation did not enter the Connection coordinator")
	}
	select {
	case err := <-result:
		held()
		t.Fatalf("binding mutation escaped held Connection domain: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	held()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	fixture.h.gatewayCoordinator.beforeLock = nil
	view, err := fixture.h.ListAddresses("r0b-agent")
	if err != nil || len(view) != 1 || view[0].DisplayName != "serialized" {
		t.Fatalf("serialized mutation result = %#v, err=%v", view, err)
	}
}

func TestR0bCommittedThenErroredFoundationPoisonsHubAndOldSnapshot(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	control := initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	snapshot, err := fixture.h.snapshotGatewayBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("directory fsync failed after rename")
	fixture.h.saveGatewayStateForTest = func(next gatewayState) error {
		if err := fixture.st.SaveRuntimeGatewayState(next); err != nil {
			return err
		}
		return sentinel
	}
	_, err = fixture.h.setGatewayRecovery(fixture.connection.ID, control.Epoch, gatewayRecoveryReconcile, "readback required")
	var indeterminate *gatewayFoundationIndeterminateError
	if !errors.As(err, &indeterminate) || !indeterminate.Committed {
		t.Fatalf("write-then-error result = %#v, err=%v", indeterminate, err)
	}
	if err := fixture.h.matchGatewayBinding(snapshot); err == nil {
		t.Fatal("poisoned Hub continued to authorize old snapshot")
	}
	if _, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "blocked"}); err == nil {
		t.Fatal("poisoned Hub authorized a configured mutation")
	}
	fixture.h.saveGatewayStateForTest = nil
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	if err := reopened.matchGatewayBinding(snapshot); err == nil {
		t.Fatal("pre-reopen snapshot matched a new Hub generation")
	}
}

func TestR0bAmbiguousReadbackPoisonsWithoutReusingSnapshot(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	control := initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	snapshot, err := fixture.h.snapshotGatewayBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.saveGatewayStateForTest = func(gatewayState) error { return errors.New("write outcome unknown") }
	fixture.h.loadGatewayStateForTest = func(*gatewayState) (bool, error) { return false, errors.New("readback unavailable") }
	_, err = fixture.h.setGatewayRecovery(fixture.connection.ID, control.Epoch, gatewayRecoveryReconcile, "unknown")
	var indeterminate *gatewayFoundationIndeterminateError
	if !errors.As(err, &indeterminate) || indeterminate.Committed || indeterminate.ReadbackErr == nil {
		t.Fatalf("ambiguous result = %#v, err=%v", indeterminate, err)
	}
	if err := fixture.h.matchGatewayBinding(snapshot); err == nil {
		t.Fatal("ambiguous Hub continued to authorize old snapshot")
	}
}

func TestR0bMutationFenceWriteThenErrorHasZeroIntegrationEffect(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	snapshot, err := fixture.h.snapshotGatewayBinding(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	integrationsPath := filepath.Join(fixture.dir, "integrations.json")
	before, err := os.ReadFile(integrationsPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.saveGatewayStateForTest = func(next gatewayState) error {
		if err := fixture.st.SaveRuntimeGatewayState(next); err != nil {
			return err
		}
		return errors.New("write committed, acknowledgement lost")
	}
	if _, err := fixture.h.UpdateAddress(fixture.address.ID, AddressParams{DisplayName: "must-not-land"}); err == nil {
		t.Fatal("indeterminate fence allowed Address mutation")
	}
	after, err := os.ReadFile(integrationsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("binding mutation landed after indeterminate fence")
	}
	if err := fixture.h.matchGatewayBinding(snapshot); err == nil {
		t.Fatal("old snapshot remained authorized after indeterminate fence")
	}
}

func TestR0bControlledFoundationPassiveLifecycleIsByteExact(t *testing.T) {
	fixture := newR0bFixture(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryManual, "operator proof required")
	fixture.h.Shutdown()
	fixture.h = nil
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st = nil
	before := snapshotHubTree(t, fixture.dir)
	ro, err := store.OpenWithOptions(fixture.dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	passive, err := OpenWithOptions(ro, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	passive.Shutdown()
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	after := snapshotHubTree(t, fixture.dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("controlled Passive lifecycle mutated tree: before=%v after=%v", before, after)
	}
}

func TestR0bFoundationCorruptionFailsHubOpen(t *testing.T) {
	fixture := newR0bFixture(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	fixture.h.Shutdown()
	fixture.h = nil
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st = nil
	path := filepath.Join(fixture.dir, "runtime-foundation.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"epoch": 1`), []byte(`"epoch": 0`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := store.Open(fixture.dir); err == nil {
		_ = st.Close()
		t.Fatal("corrupt Gateway epoch was accepted")
	}
}

func TestR0bBindingDriftFailsBeforeStartupRecoveryWrites(t *testing.T) {
	fixture := newR0bFixture(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	fixture.h.Shutdown()
	fixture.h = nil
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st = nil
	path := filepath.Join(fixture.dir, "runtime-foundation.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte(`"accountRef": "acct"`), []byte(`"accountRef": "drift"`), 1)
	if bytes.Equal(changed, data) {
		t.Fatalf("fixture did not contain expected binding: %s", data)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotHubTree(t, fixture.dir)
	st, err := store.Open(fixture.dir)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := Open(st); err == nil {
		h.Shutdown()
		t.Fatal("semantic binding drift was accepted")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	after := snapshotHubTree(t, fixture.dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed semantic open mutated durable tree: before=%v after=%v", before, after)
	}
}

func TestR0bStateJSONHasNoSecretMaterial(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	data, err := os.ReadFile(filepath.Join(fixture.dir, "runtime-foundation.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "secret", "apiKey", "privateKey"} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
			t.Fatalf("foundation contains forbidden secret-shaped field %q: %s", forbidden, data)
		}
	}
	if !strings.Contains(string(data), fmt.Sprintf(`"connectionId": %q`, fixture.connection.ID)) {
		t.Fatal("foundation omitted stable Connection identity")
	}
}
