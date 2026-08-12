package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func newL2aFixture(t *testing.T) r0bFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	seedInboxAgent(t, h, "agent-l2a", "l2a-agent")
	credentialRef := "managed:" + strings.Repeat("a", 64)
	connection, err := h.CreateConnection(ConnectionParams{
		Provider: "lark", AccountRef: "cli_l2a_app", Domain: "lark", CredentialRef: credentialRef,
		Capabilities: []string{"receive_events", "proactive_send"},
	})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(AddressParams{
		Agent: "l2a-agent", ConnectionID: connection.ID, ExternalIdentity: "lark://ou_l2a",
		TriggerPolicy: "mention", ReplyPolicy: "final_answer", TrustDomain: "lark:cli_l2a_app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	return r0bFixture{dir: dir, st: st, h: h, connection: connection, address: address}
}

func l2aExecutable(t *testing.T, dir, name, contents string) larkGatewayExecutableSpec {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(contents))
	return larkGatewayExecutableSpec{Executable: path, Build: name + "-build", ExecutableDigest: hex.EncodeToString(digest[:])}
}

func l2aLaunchSpec(t *testing.T, fixture *r0bFixture, manager gatewayServiceManager) larkGatewayLaunchSpec {
	t.Helper()
	extension := ".unit"
	if manager == gatewayServiceManagerLaunchd {
		extension = ".plist"
	} else if manager == gatewayServiceManagerSystemd {
		extension = ".service"
	}
	return larkGatewayLaunchSpec{
		ConnectionID: fixture.connection.ID, AddressID: fixture.address.ID, Manager: manager,
		ServiceID: "com.codexloom.feishu." + fixture.connection.ID,
		UnitPath:  filepath.Join(fixture.dir, "feishu"+extension), WorkingDirectory: fixture.dir,
		HubURL: "http://127.0.0.1:4870", DataDir: fixture.st.Dir(), LogPath: filepath.Join(fixture.dir, "feishu.log"),
		Target: l2aExecutable(t, fixture.dir, "loom-feishu-gateway-target", "target gateway\n"),
		Anchor: l2aExecutable(t, fixture.dir, "loom-feishu-gateway-anchor", "anchor gateway\n"),
	}
}

func TestL2aProductionCallerFreezesBindingRunsOneEffectAndAcceptsPrivateHeartbeatProof(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }

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
		if attempt.Phase == gatewayAttemptAwaitingTargetProof {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if attempt.Phase != gatewayAttemptAwaitingTargetProof || attempt.Plan.Target.ManagedCredentialRef != fixture.connection.CredentialRef ||
		attempt.Plan.Target.AddressID != fixture.address.ID || attempt.Plan.Target.AccountRef != fixture.connection.AccountRef ||
		attempt.Plan.Target.Domain != "lark" || attempt.Plan.IntegritySHA256 == "" {
		t.Fatalf("production launch did not freeze the complete binding: %#v", attempt)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 1 || !strings.HasPrefix(calls[0], "apply:"+attempt.ID+":"+attempt.TargetGeneration) {
		t.Fatalf("production caller bypassed or duplicated the R1 effect: %v", calls)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.ActiveAttemptID != attempt.ID || control.Recovery == gatewayRecoveryNone {
		t.Fatalf("ordinary heartbeat cleared the active process attempt: %#v", control)
	}
	wrong := &GatewayProcessHeartbeatParams{
		AttemptID: "gattempt-stale", Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: wrong}); err == nil {
		t.Fatal("late process heartbeat satisfied another attempt")
	}
	exact := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	wire, err := json.Marshal(ConnectionHeartbeatParams{Status: "connected", GatewayProcess: exact})
	if err != nil || !bytes.Contains(wire, []byte(`"_gatewayProcess"`)) {
		t.Fatalf("private connector proof field is not serializable: %s err=%v", wire, err)
	}
	var decoded ConnectionHeartbeatParams
	if err := json.Unmarshal(wire, &decoded); err != nil || decoded.GatewayProcess == nil || decoded.GatewayProcess.AttemptID != attempt.ID {
		t.Fatalf("private connector proof field did not round trip: %#v err=%v", decoded, err)
	}
	view, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{
		Status: "connected", Capabilities: []string{"receive_events"}, GatewayProcess: exact,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-result:
		if completed.err != nil || completed.attempt.Phase != gatewayAttemptSucceeded || completed.attempt.AcceptedProof == nil {
			t.Fatalf("production caller returned before exact terminal proof: attempt=%#v err=%v", completed.attempt, completed.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production caller did not return after exact proof")
	}
	control = fixture.h.gatewayState.Controls[fixture.connection.ID]
	storedAttempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	if control.ActiveAttemptID != "" || control.Recovery != gatewayRecoveryNone || storedAttempt.Phase != gatewayAttemptSucceeded || storedAttempt.AcceptedProof == nil {
		t.Fatalf("exact private proof was not committed with terminal control: control=%#v attempt=%#v", control, storedAttempt)
	}
	if view.Status != "connected" || !reflect.DeepEqual(view.Capabilities, fixture.connection.Capabilities) {
		t.Fatalf("proof heartbeat changed configured projection: %#v", view)
	}
	payload, err := json.Marshal(view)
	if err != nil || bytes.Contains(payload, []byte("gatewayProcess")) || bytes.Contains(payload, []byte(attempt.ID)) {
		t.Fatalf("private proof leaked into public Connection JSON: %s err=%v", payload, err)
	}
	if err := fixture.h.requireAcceptedGatewayProof(fixture.connection.ID); err != nil {
		t.Fatalf("Lark verification cannot consume the accepted proof: %v", err)
	}
	promoted := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	currentUnit, currentErr := renderGatewayServiceUnitForAttempt(attempt.Plan.Target, attempt.ID, attempt.TargetGeneration)
	anchorUnit, anchorErr := renderGatewayServiceUnitForAttempt(promoted.Anchor.Descriptor, promoted.Anchor.AttemptID, promoted.Anchor.Generation)
	if currentErr != nil || anchorErr != nil || !bytes.Equal(currentUnit, anchorUnit) {
		t.Fatalf("accepted target was not promoted to the next authoritative registration anchor: equal=%v errors=%v/%v", bytes.Equal(currentUnit, anchorUnit), currentErr, anchorErr)
	}
	nextAttempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil || nextAttempt.ID == attempt.ID || nextAttempt.ID == promoted.Anchor.AttemptID {
		t.Fatalf("automatic restart did not consume the promoted exact anchor: attempt=%#v err=%v", nextAttempt, err)
	}
	nextProof := &GatewayProcessHeartbeatParams{
		AttemptID: nextAttempt.ID, Generation: nextAttempt.TargetGeneration, Build: nextAttempt.Plan.Target.Build,
		ExecutableDigest: nextAttempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: nextProof}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: nextProof}); err != nil {
		t.Fatalf("repeated exact proof heartbeat was not idempotent: %v", err)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: exact}); err == nil {
		t.Fatal("late proof from the prior attempt was accepted after a newer terminal attempt")
	}
	if calls := adapter.snapshotCalls(); len(calls) != 2 {
		t.Fatalf("proof return bypassed or duplicated the shared service path: %v", calls)
	}
}

func TestL2aManagedRefIsDerivedFromFrozenBindingAndChangesPlanDigest(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	spec := l2aLaunchSpec(t, &fixture, gatewayServiceManagerFake)
	fixture.h.mu.Lock()
	binding, err := fixture.h.gatewayBindingLocked(fixture.connection.ID)
	fixture.h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLarkGatewayLaunchPlan(binding, spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.ManagedCredentialRef != binding.Connection.CredentialRef {
		t.Fatalf("caller substituted managed ref: target=%q binding=%q", plan.Target.ManagedCredentialRef, binding.Connection.CredentialRef)
	}
	tampered := plan
	tampered.Target.ManagedCredentialRef = "managed:" + strings.Repeat("b", 64)
	tampered.IntegritySHA256 = gatewayLaunchPlanIntegrity(tampered)
	if tampered.IntegritySHA256 == plan.IntegritySHA256 {
		t.Fatal("managed ref change did not change the launch plan digest")
	}
	if err := validateGatewayLaunchPlanForBinding(tampered, binding); err == nil {
		t.Fatal("plan with a different managed ref matched the frozen binding")
	}
}

func TestL2aMaintenancePlanIsDormantUntilProductionStartupCaller(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if _, err := fixture.h.configureLarkGatewayLaunch(l2aLaunchSpec(t, &fixture, gatewayServiceManagerFake)); err != nil {
		t.Fatal(err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("maintenance plan commit performed a service effect: %v", calls)
	}
	if fixture.h.gatewayState.Version != gatewayLaunchProofStateVersion {
		t.Fatalf("typed maintenance plan state version = %d", fixture.h.gatewayState.Version)
	}
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	attempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	if attempt == nil || attempt.Phase != gatewayAttemptAwaitingTargetProof {
		t.Fatalf("production startup caller did not consume the typed plan: %#v", attempt)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 1 || !strings.HasPrefix(calls[0], "apply:"+attempt.ID+":") {
		t.Fatalf("production startup bypassed or duplicated the R1 adapter: %v", calls)
	}
	proof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: proof}); err != nil {
		t.Fatal(err)
	}
	if got := fixture.h.gatewayState.Attempts[fixture.connection.ID]; got.Phase != gatewayAttemptSucceeded || got.AcceptedProof == nil {
		t.Fatalf("private proof did not terminate the startup attempt: %#v", got)
	}
}

func TestL2aBindingDriftReopensFailClosedWithoutDiscardingTypedPlan(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if _, err := fixture.h.configureLarkGatewayLaunch(l2aLaunchSpec(t, &fixture, gatewayServiceManagerFake)); err != nil {
		t.Fatal(err)
	}
	newRef := "managed:" + strings.Repeat("b", 64)
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: newRef}); err != nil {
		t.Fatal(err)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	plan := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	if control.Recovery != gatewayRecoveryReconcile || control.Binding.Connection.CredentialRef != newRef || plan.Target.ManagedCredentialRef == newRef {
		t.Fatalf("binding drift was not retained as an explicit stale-plan stop: control=%#v plan=%#v", control, plan)
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatalf("expected stale typed plan could not reopen for reconciliation: %v", err)
	}
	fixture.h = reopened
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("stale typed plan produced a startup effect after reopen: %v", calls)
	}
	view := fixture.h.ListConnections()[0]
	if view.Status != "degraded" || !strings.Contains(strings.ToLower(view.LastError), "reconcil") {
		t.Fatalf("stale typed plan was not projected fail closed after reopen: %#v", view)
	}
}

func TestL2aProductionCallerWaitsForExactRecoveryProofAfterTargetFailure(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{
		applyResult:   gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: context.DeadlineExceeded},
		restoreResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
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
	if attempt.Phase != gatewayAttemptAwaitingRecoveryProof || attempt.RecoveryGeneration == attempt.TargetGeneration {
		t.Fatalf("target failure did not reach distinct recovery proof: %#v", attempt)
	}
	proof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.RecoveryGeneration, Build: attempt.Plan.Anchor.Descriptor.Build,
		ExecutableDigest: attempt.Plan.Anchor.Descriptor.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: proof}); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-result:
		if completed.err == nil || completed.attempt.Phase != gatewayAttemptRecovered || completed.attempt.AcceptedProof == nil {
			t.Fatalf("recovery caller result = attempt %#v err=%v", completed.attempt, completed.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production caller returned before neither proof nor bounded stop")
	}
	promoted := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	recoveryUnit, recoveryErr := renderGatewayServiceUnitForAttempt(attempt.Plan.Anchor.Descriptor, attempt.ID, attempt.RecoveryGeneration)
	anchorUnit, anchorErr := renderGatewayServiceUnitForAttempt(promoted.Anchor.Descriptor, promoted.Anchor.AttemptID, promoted.Anchor.Generation)
	if recoveryErr != nil || anchorErr != nil || !bytes.Equal(recoveryUnit, anchorUnit) {
		t.Fatalf("accepted recovery was not promoted to the authoritative anchor: equal=%v errors=%v/%v", bytes.Equal(recoveryUnit, anchorUnit), recoveryErr, anchorErr)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 2 || !strings.HasPrefix(calls[0], "apply:") || !strings.HasPrefix(calls[1], "restore:") {
		t.Fatalf("target recovery bypassed or duplicated R1 effects: %v", calls)
	}
}

func TestL2aFeishuUnitsUseRealFlagsAndFreezeAttemptProofIdentity(t *testing.T) {
	ref := "managed:" + strings.Repeat("c", 64)
	base := gatewayLaunchDescriptor{
		ConnectionID: "conn_l2a", Provider: "lark", AddressID: "addr_l2a", AccountRef: "cli_l2a", Domain: "lark",
		ManagedCredentialRef: ref, ServiceID: "com.codexloom.feishu.conn_l2a",
		Executable: "/opt/codexloom/loom-feishu-gateway", WorkingDirectory: "/opt/codexloom",
		HubURL: "http://127.0.0.1:4870", DataDir: "/var/tmp/codexloom", LogPath: "/var/tmp/codexloom/gateway/feishu.log",
		Build: "build-l2a", ExecutableDigest: strings.Repeat("d", 64),
	}
	for _, test := range []struct {
		manager gatewayServiceManager
		unit    string
	}{
		{manager: gatewayServiceManagerLaunchd, unit: "/Users/owner/Library/LaunchAgents/com.codexloom.feishu.conn_l2a.plist"},
		{manager: gatewayServiceManagerSystemd, unit: "/home/owner/.config/systemd/user/codexloom-feishu-conn_l2a.service"},
	} {
		t.Run(string(test.manager), func(t *testing.T) {
			descriptor := base
			descriptor.Manager, descriptor.UnitPath = test.manager, test.unit
			unit, err := renderGatewayServiceUnitForAttempt(descriptor, "gattempt_l2a", "ggen_l2a")
			if err != nil {
				t.Fatal(err)
			}
			text := string(unit)
			for _, want := range []string{
				"--hub", descriptor.HubURL, "--connection", descriptor.ConnectionID, "--address", descriptor.AddressID,
				"--app-id", descriptor.AccountRef, "--domain", descriptor.Domain, "CODEX_LOOM_DATA", "CODEX_LOOM_MANAGED_CREDENTIAL_REF", ref,
				"CODEX_LOOM_GATEWAY_ATTEMPT_ID", "gattempt_l2a", "CODEX_LOOM_GATEWAY_GENERATION", "ggen_l2a",
				"CODEX_LOOM_GATEWAY_BUILD", descriptor.Build, "CODEX_LOOM_GATEWAY_EXECUTABLE_DIGEST", descriptor.ExecutableDigest,
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s unit omitted %q: %s", test.manager, want, text)
				}
			}
			for _, forbidden := range []string{"--connection-id", "--data-dir", "--generation", "--build", "--executable-digest"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s unit used a flag unsupported by loom-feishu-gateway: %q", test.manager, forbidden)
				}
			}
			if test.manager == gatewayServiceManagerLaunchd && (!strings.Contains(text, "<key>KeepAlive</key><true/>") || !strings.Contains(text, "<key>ProcessType</key><string>Background</string>")) {
				t.Fatalf("launchd unit drifted from the production Feishu lifecycle: %s", text)
			}
			if test.manager == gatewayServiceManagerSystemd && !strings.Contains(text, "Restart=always\nRestartSec=2") {
				t.Fatalf("systemd unit drifted from the production Feishu lifecycle: %s", text)
			}
			late, err := renderGatewayServiceUnitForAttempt(descriptor, "gattempt_other", "ggen_l2a")
			if err != nil || bytes.Equal(unit, late) {
				t.Fatalf("attempt identity is not frozen in the rendered registration: equal=%v err=%v", bytes.Equal(unit, late), err)
			}
		})
	}
}

func TestL2aLegacyLaunchdAnchorMatchesExistingFeishuManagerBytes(t *testing.T) {
	descriptor := gatewayLaunchDescriptor{
		Manager: gatewayServiceManagerLaunchd, ConnectionID: "conn_l2a", Provider: "lark", AddressID: "addr_l2a", AccountRef: "cli_l2a",
		ServiceID: "com.codexloom.feishu.conn_l2a", UnitPath: "/Users/owner/Library/LaunchAgents/com.codexloom.feishu.conn_l2a.plist",
		Executable: "/opt/codexloom/loom-feishu-gateway", WorkingDirectory: "/opt/codexloom",
		HubURL: "http://127.0.0.1:4870", DataDir: "/var/tmp/codexloom", LogPath: "/var/tmp/codexloom/gateway/feishu-conn_l2a.log",
		Build: "anchor-build", ExecutableDigest: strings.Repeat("e", 64),
	}
	got, err := renderGatewayServiceUnitForAttempt(descriptor, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.codexloom.feishu.conn_l2a</string>
    <key>ProgramArguments</key>
    <array>
      <string>/opt/codexloom/loom-feishu-gateway</string>
      <string>--hub</string>
      <string>http://127.0.0.1:4870</string>
      <string>--connection</string>
      <string>conn_l2a</string>
      <string>--address</string>
      <string>addr_l2a</string>
      <string>--app-id</string>
      <string>cli_l2a</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict><key>CODEX_LOOM_DATA</key><string>/var/tmp/codexloom</string></dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>ProcessType</key><string>Background</string>
    <key>StandardOutPath</key><string>/var/tmp/codexloom/gateway/feishu-conn_l2a.log</string>
    <key>StandardErrorPath</key><string>/var/tmp/codexloom/gateway/feishu-conn_l2a.log</string>
  </dict>
</plist>
`
	if string(got) != want {
		t.Fatalf("typed Runtime anchor drifted from the existing Feishu launchd manager\nwant:\n%s\ngot:\n%s", want, got)
	}
}
