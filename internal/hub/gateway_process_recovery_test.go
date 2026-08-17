package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

type fakeGatewayServiceAdapter struct {
	mu             sync.Mutex
	calls          []string
	applyResult    gatewayServiceEffectResult
	restoreResult  gatewayServiceEffectResult
	inspection     gatewayServiceInspection
	inspectErr     error
	anchorErr      error
	validations    int
	applyEntered   chan struct{}
	applyRelease   chan struct{}
	restoreEntered chan struct{}
	beforeApply    func()
}

func (a *fakeGatewayServiceAdapter) ValidateAnchor(_ context.Context, _ gatewayLaunchPlan) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.validations++
	return a.anchorErr
}

func (a *fakeGatewayServiceAdapter) Apply(_ context.Context, effect gatewayServiceEffect) gatewayServiceEffectResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "apply:"+effect.AttemptID+":"+effect.Generation)
	if a.beforeApply != nil {
		a.beforeApply()
	}
	if a.applyEntered != nil {
		select {
		case a.applyEntered <- struct{}{}:
		default:
		}
	}
	if a.applyRelease != nil {
		a.mu.Unlock()
		<-a.applyRelease
		a.mu.Lock()
	}
	return a.applyResult
}

func (a *fakeGatewayServiceAdapter) Restore(_ context.Context, effect gatewayServiceEffect) gatewayServiceEffectResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "restore:"+effect.AttemptID+":"+effect.Generation)
	if a.restoreEntered != nil {
		select {
		case a.restoreEntered <- struct{}{}:
		default:
		}
	}
	return a.restoreResult
}

func (a *fakeGatewayServiceAdapter) Inspect(_ context.Context, request gatewayServiceInspectionRequest) (gatewayServiceInspection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "inspect:"+request.AttemptID)
	return a.inspection, a.inspectErr
}

func (a *fakeGatewayServiceAdapter) snapshotCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

func (a *fakeGatewayServiceAdapter) setInspection(value gatewayServiceInspection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inspection = value
}

func r1Descriptor(connectionID, build, digest, executable string) gatewayLaunchDescriptor {
	return gatewayLaunchDescriptor{
		Manager:          gatewayServiceManagerFake,
		ConnectionID:     connectionID,
		ServiceID:        "codexloom-test-" + connectionID,
		UnitPath:         "/tmp/codexloom-test-" + connectionID + ".unit",
		Executable:       executable,
		WorkingDirectory: "/tmp",
		HubURL:           "http://127.0.0.1:24821",
		DataDir:          "/tmp/codexloom-data",
		LogPath:          "/tmp/codexloom-test.log",
		Build:            build,
		ExecutableDigest: digest,
	}
}

func r1Plan(t *testing.T, connectionID string, dataDirs ...string) gatewayLaunchPlan {
	t.Helper()
	dir := t.TempDir()
	writeExecutable := func(name, content string) (string, string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(content))
		return path, hex.EncodeToString(digest[:])
	}
	targetPath, targetDigest := writeExecutable("gateway-target", "target executable\n")
	anchorPath, anchorDigest := writeExecutable("gateway-anchor", "anchor executable\n")
	target := r1Descriptor(connectionID, "build-target", targetDigest, targetPath)
	anchorDescriptor := r1Descriptor(connectionID, "build-anchor", anchorDigest, anchorPath)
	if len(dataDirs) != 0 {
		target.DataDir = dataDirs[0]
		anchorDescriptor.DataDir = dataDirs[0]
	}
	anchor, err := newGatewayRegistrationAnchor(anchorDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	return gatewayLaunchPlan{ConnectionID: connectionID, Target: target, Anchor: anchor}
}

func configureR1Fixture(t *testing.T, fixture *r0bFixture, adapter *fakeGatewayServiceAdapter, recovery gatewayRecoveryDisposition) gatewayLaunchPlan {
	t.Helper()
	initializeAdoptedR0bControl(t, fixture, recovery, "")
	snapshot, err := fixture.h.snapshotGatewayBindingForProcessPlan(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := r1Plan(t, fixture.connection.ID, fixture.st.Dir())
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if _, err := fixture.h.configureGatewayLaunchPlan(snapshot, plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func exactR1Proof(attempt gatewayTransitionAttempt, recovery bool, observedAt time.Time) gatewayProcessProof {
	generation, build, digest := attempt.TargetGeneration, attempt.Plan.Target.Build, attempt.Plan.Target.ExecutableDigest
	if recovery {
		generation, build, digest = attempt.RecoveryGeneration, attempt.Plan.Anchor.Descriptor.Build, attempt.Plan.Anchor.Descriptor.ExecutableDigest
	}
	return gatewayProcessProof{AttemptID: attempt.ID, Generation: generation, Build: build, ExecutableDigest: digest, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano)}
}

func TestR1LegacyProvisioningAndMissingPlanHaveZeroServiceEffects(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("legacy Connection produced service effects: %v", calls)
	}
	if _, err := fixture.h.initializeGatewayControl(fixture.connection.ID, gatewayBindingProvisioning, gatewayRecoveryNone, "incomplete"); err != nil {
		t.Fatal(err)
	}
	foundationPath := filepath.Join(fixture.dir, "runtime-foundation.json")
	before, err := os.ReadFile(foundationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("provisioning/missing-plan Connection produced service effects: %v", calls)
	}
	after, err := os.ReadFile(foundationPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("missing-plan startup mutated R0b foundation: err=%v", err)
	}
}

func TestR1PersistsValidatedIntentBeforeOneServiceEffect(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	plan := configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)

	persistedBeforeEffect := false
	adapter.beforeApply = func() {
		fixture.h.mu.Lock()
		defer fixture.h.mu.Unlock()
		attempt := fixture.h.gatewayState.Attempts[fixture.connection.ID]
		persistedBeforeEffect = attempt != nil && attempt.Phase == gatewayAttemptTargetIntent &&
			fixture.h.gatewayState.Controls[fixture.connection.ID].ActiveAttemptID == attempt.ID
	}
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	adapter.setInspection(gatewayServiceInspection{State: gatewayServiceObservedTarget, Generation: attempt.TargetGeneration,
		Build: attempt.Plan.Target.Build, ExecutableDigest: attempt.Plan.Target.ExecutableDigest})
	if !persistedBeforeEffect {
		t.Fatal("service effect ran before durable attempt intent")
	}
	if attempt.TargetGeneration == "" || attempt.RecoveryGeneration == "" || attempt.TargetGeneration == attempt.RecoveryGeneration ||
		attempt.Plan.Anchor.IntegritySHA256 == "" || !gatewayBindingsEqual(attempt.Binding, fixture.h.gatewayState.Controls[fixture.connection.ID].Binding) {
		t.Fatalf("attempt did not freeze required identity: %#v", attempt)
	}
	if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
		t.Fatal("second active attempt was accepted")
	}
	if got := adapter.snapshotCalls(); len(got) != 1 || got[0] != "apply:"+attempt.ID+":"+attempt.TargetGeneration {
		t.Fatalf("service calls = %v", got)
	}

	bad := plan
	bad.Anchor.Descriptor.HubURL = "http://user:raw-secret@127.0.0.1:24821"
	bad.Anchor.IntegritySHA256 = gatewayAnchorIntegrity(bad.Anchor.Descriptor)
	if err := validateGatewayLaunchPlan(bad); err == nil {
		t.Fatal("secret-bearing registration anchor was accepted")
	}
}

func TestR1ExactFreshProofIsOnlyTerminalClear(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	nowAt := time.Now().UTC()
	for name, proof := range map[string]gatewayProcessProof{
		"late old generation": func() gatewayProcessProof { p := exactR1Proof(attempt, false, nowAt); p.Generation = "old"; return p }(),
		"wrong build":         func() gatewayProcessProof { p := exactR1Proof(attempt, false, nowAt); p.Build = "other"; return p }(),
		"wrong digest": func() gatewayProcessProof {
			p := exactR1Proof(attempt, false, nowAt)
			p.ExecutableDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			return p
		}(),
		"stale": exactR1Proof(attempt, false, nowAt.Add(-gatewayProcessProofFreshness-time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, proof, nowAt); err == nil {
				t.Fatal("mismatched/stale proof was accepted")
			}
			control := fixture.h.gatewayState.Controls[fixture.connection.ID]
			if control.ActiveAttemptID != attempt.ID || control.Recovery == gatewayRecoveryNone {
				t.Fatalf("rejected proof cleared active control: %#v", control)
			}
		})
	}
	completed, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt)
	if err != nil {
		t.Fatal(err)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if completed.Phase != gatewayAttemptSucceeded || completed.AcceptedProof == nil || control.ActiveAttemptID != "" || control.Recovery != gatewayRecoveryNone {
		t.Fatalf("proof/control terminal commit = attempt %#v control %#v", completed, control)
	}
	view := fixture.h.ListConnections()[0]
	if view.Status != "connected" || view.LastHeartbeatAt != completed.AcceptedProof.ObservedAt {
		t.Fatalf("accepted process proof did not atomically project health: %#v", view)
	}
}

func TestR1DefinitiveTargetFailureUsesDistinctRecoveryAndExactProof(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{
		applyResult:   gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: errors.New("target rejected")},
		restoreResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
	}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err == nil || attempt.Phase != gatewayAttemptAwaitingRecoveryProof {
		t.Fatalf("target failure = attempt %#v err=%v", attempt, err)
	}
	calls := adapter.snapshotCalls()
	want := []string{"apply:" + attempt.ID + ":" + attempt.TargetGeneration, "restore:" + attempt.ID + ":" + attempt.RecoveryGeneration}
	if !reflect.DeepEqual(calls, want) || attempt.TargetGeneration == attempt.RecoveryGeneration {
		t.Fatalf("recovery calls/generation = %v, attempt=%#v", calls, attempt)
	}
	nowAt := time.Now().UTC()
	if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt); err == nil {
		t.Fatal("late target proof satisfied recovery")
	}
	recovered, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, true, nowAt), nowAt)
	if err != nil || recovered.Phase != gatewayAttemptRecovered {
		t.Fatalf("recovery proof = %#v, %v", recovered, err)
	}
}

func TestR1IndeterminateEffectReopensIntoInspectionWithoutBlindReplay(t *testing.T) {
	fixture := newR0bFixture(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: errors.New("timeout")}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err == nil || attempt.Phase != gatewayAttemptReconcileRequired {
		t.Fatalf("indeterminate effect = attempt %#v err=%v", attempt, err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 1 || calls[0][:6] != "apply:" {
		t.Fatalf("initial effect calls = %v", calls)
	}
	fixture.h.Shutdown()
	fixture.h = nil

	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	adapter.inspection = gatewayServiceInspection{State: gatewayServiceObservedTarget, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build, ExecutableDigest: attempt.Plan.Target.ExecutableDigest}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	calls := adapter.snapshotCalls()
	if len(calls) != 2 || calls[1] != "inspect:"+attempt.ID {
		t.Fatalf("startup blindly replayed indeterminate effect: %v", calls)
	}
}

func TestR1RecoveryFailureAndTerminalPersistUncertaintyFailClosed(t *testing.T) {
	t.Run("recovery failure", func(t *testing.T) {
		fixture := newR0bFixture(t)
		defer fixture.close(t)
		adapter := &fakeGatewayServiceAdapter{
			applyResult:   gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: errors.New("target failed")},
			restoreResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectFailed, Err: errors.New("restore failed")},
		}
		configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
		attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
		if err == nil || attempt.Phase != gatewayAttemptManualRecoveryRequired || fixture.h.gatewayState.Controls[fixture.connection.ID].Recovery != gatewayRecoveryManual {
			t.Fatalf("failed recovery did not latch manual: attempt=%#v err=%v", attempt, err)
		}
	})

	t.Run("terminal persist indeterminate", func(t *testing.T) {
		fixture := newR0bFixture(t)
		defer fixture.close(t)
		adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
		configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
		attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
		if err != nil {
			t.Fatal(err)
		}
		fixture.h.saveGatewayStateForTest = func(gatewayState) error { return errors.New("terminal write outcome unknown") }
		fixture.h.loadGatewayStateForTest = func(*gatewayState) (bool, error) { return false, errors.New("readback unavailable") }
		nowAt := time.Now().UTC()
		_, err = fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt)
		var indeterminate *gatewayFoundationIndeterminateError
		if !errors.As(err, &indeterminate) || !fixture.h.gatewayFoundationPoisoned {
			t.Fatalf("terminal uncertainty did not poison foundation: %v", err)
		}
		if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
			t.Fatal("poisoned foundation authorized another effect")
		}
		if got := adapter.snapshotCalls(); len(got) != 1 {
			t.Fatalf("poisoned retry called adapter: %v", got)
		}
	})
}

func TestR1ServiceAdapterDetailNeverEntersDurableFoundation(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	const sensitiveDetail = "fixture-sensitive-service-output"
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: errors.New(sensitiveDetail)}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
		t.Fatal("indeterminate adapter output returned success")
	}
	data, err := os.ReadFile(filepath.Join(fixture.dir, "runtime-foundation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(sensitiveDetail)) {
		t.Fatalf("service adapter detail leaked into foundation: %s", data)
	}
}

func TestR1LaunchPlanValidationIsBoundedCompleteAndSecretFree(t *testing.T) {
	plan := r1Plan(t, "conn-validation")
	if err := validateGatewayLaunchPlan(plan); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*gatewayLaunchPlan){
		"missing digest":       func(value *gatewayLaunchPlan) { value.Target.ExecutableDigest = "" },
		"bad anchor integrity": func(value *gatewayLaunchPlan) { value.Anchor.IntegritySHA256 = "bad" },
		"secret URL":           func(value *gatewayLaunchPlan) { value.Target.HubURL = "http://token@127.0.0.1" },
		"unbounded service":    func(value *gatewayLaunchPlan) { value.Target.ServiceID = stringsRepeat("x", gatewayProcessStringMax+1) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := plan
			mutate(&candidate)
			if err := validateGatewayLaunchPlan(candidate); err == nil {
				t.Fatal("invalid launch plan passed validation")
			}
		})
	}
}

func stringsRepeat(value string, count int) string {
	result := ""
	for len(result) < count {
		result += value
	}
	return result[:count]
}

func TestR1AdapterSelectionFailsClosedOnUnsupportedManager(t *testing.T) {
	plan := r1Plan(t, "conn-adapter")
	plan.Target.Manager = gatewayServiceManager("unsupported")
	plan.Anchor.Descriptor.Manager = plan.Target.Manager
	plan.Anchor.IntegritySHA256 = gatewayAnchorIntegrity(plan.Anchor.Descriptor)
	if _, err := defaultGatewayServiceAdapter(plan); err == nil {
		t.Fatal("unsupported service manager was accepted")
	}
}

func TestR1EffectPanicIsIndeterminate(t *testing.T) {
	result := invokeGatewayServiceEffect(func() gatewayServiceEffectResult { panic("lost response") })
	if result.Outcome != gatewayServiceEffectIndeterminate || result.Err == nil || fmt.Sprint(result.Err) == "" {
		t.Fatalf("panic result = %#v", result)
	}
}

func TestR1RegistrationAnchorValidationMatchesExactUnitAndGeneration(t *testing.T) {
	plan := r1Plan(t, "conn-anchor-unit")
	plan.Anchor.Generation = "previous-generation"
	plan.Anchor.IntegritySHA256 = gatewayAnchorIntegrityWithGeneration(plan.Anchor.Descriptor, plan.Anchor.Generation)
	plan.Target.UnitPath = filepath.Join(t.TempDir(), "gateway.unit")
	plan.Anchor.Descriptor.UnitPath = plan.Target.UnitPath
	plan.Anchor.IntegritySHA256 = gatewayAnchorIntegrityWithGeneration(plan.Anchor.Descriptor, plan.Anchor.Generation)
	unit, err := renderGatewayServiceUnit(plan.Anchor.Descriptor, plan.Anchor.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.Target.UnitPath, unit, 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &platformGatewayServiceAdapter{manager: gatewayServiceManagerFake}
	if err := adapter.ValidateAnchor(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.Target.UnitPath, append(unit, []byte("drift")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adapter.ValidateAnchor(context.Background(), plan); err == nil {
		t.Fatal("registration drift was accepted as the durable anchor")
	}
	other := filepath.Join(t.TempDir(), "other.unit")
	if err := os.WriteFile(other, unit, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plan.Target.UnitPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, plan.Target.UnitPath); err != nil {
		t.Fatal(err)
	}
	if err := adapter.ValidateAnchor(context.Background(), plan); err == nil {
		t.Fatal("symlinked registration anchor was accepted")
	}
}

func TestR1LaunchdAndSystemdUseTheSameTypedAdapterContract(t *testing.T) {
	for _, manager := range []gatewayServiceManager{gatewayServiceManagerLaunchd, gatewayServiceManagerSystemd} {
		t.Run(string(manager), func(t *testing.T) {
			plan := r1Plan(t, "conn-"+string(manager))
			plan.Target.Manager = manager
			plan.Anchor.Descriptor.Manager = manager
			extension := ".plist"
			if manager == gatewayServiceManagerSystemd {
				extension = ".service"
			}
			unitPath := filepath.Join(t.TempDir(), "gateway"+extension)
			plan.Target.UnitPath = unitPath
			plan.Anchor.Descriptor.UnitPath = unitPath
			plan.Anchor.Generation = "prior-generation"
			plan.Anchor.IntegritySHA256 = gatewayAnchorIntegrityWithGeneration(plan.Anchor.Descriptor, plan.Anchor.Generation)
			anchorUnit, err := renderGatewayServiceUnit(plan.Anchor.Descriptor, plan.Anchor.Generation)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(unitPath, anchorUnit, 0o600); err != nil {
				t.Fatal(err)
			}
			adapter := &platformGatewayServiceAdapter{manager: manager}
			if err := adapter.ValidateAnchor(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			targetUnit, err := renderGatewayServiceUnit(plan.Target, "target-generation")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"target-generation", plan.Target.Build, plan.Target.ExecutableDigest, plan.ConnectionID} {
				if !bytes.Contains(targetUnit, []byte(want)) {
					t.Fatalf("%s unit omitted frozen proof field %q: %s", manager, want, targetUnit)
				}
			}
			if bytes.Contains(bytes.ToLower(targetUnit), []byte("environment")) {
				t.Fatalf("%s unit introduced an untyped environment channel: %s", manager, targetUnit)
			}
		})
	}
}

func TestR1RejectedTargetPreconditionDoesNotRestoreUnknownRegistration(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectRejected, Err: errors.New("unit changed after intent")}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err == nil || attempt.Phase != gatewayAttemptManualRecoveryRequired {
		t.Fatalf("rejected precondition = attempt %#v err=%v", attempt, err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 1 || calls[0][:6] != "apply:" {
		t.Fatalf("rejected precondition attempted restore: %v", calls)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.Recovery != gatewayRecoveryManual || control.ActiveAttemptID != "" {
		t.Fatalf("rejected precondition did not latch manual recovery: %#v", control)
	}
}

func TestR1FirstPlanRaisesWriterFloorAndReopensWithoutPassiveWrites(t *testing.T) {
	fixture := newR0bFixture(t)
	initializeAdoptedR0bControl(t, &fixture, gatewayRecoveryNone, "")
	path := filepath.Join(fixture.dir, "runtime-foundation.json")
	beforePlan, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(beforePlan, []byte(`"minimumWriter": 2`)) {
		t.Fatalf("R0b floor before plan = %s, %v", beforePlan, err)
	}
	snapshot, err := fixture.h.snapshotGatewayBindingForProcessPlan(fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return &fakeGatewayServiceAdapter{}, nil }
	if _, err := fixture.h.configureGatewayLaunchPlan(snapshot, r1Plan(t, fixture.connection.ID, fixture.st.Dir())); err != nil {
		t.Fatal(err)
	}
	afterPlan, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(afterPlan, []byte(`"minimumWriter": 3`)) || !bytes.Contains(afterPlan, []byte(`"launchPlans"`)) {
		t.Fatalf("R1 plan/floor commit = %s, %v", afterPlan, err)
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	fixture.h.Shutdown()
	fixture.h = nil
	beforePassive := snapshotHubTree(t, fixture.dir)
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
	if afterPassive := snapshotHubTree(t, fixture.dir); !reflect.DeepEqual(beforePassive, afterPassive) {
		t.Fatalf("passive R1 open mutated durable tree: before=%v after=%v", beforePassive, afterPassive)
	}
	if err := fixture.st.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.st = nil
}

func TestR1ManualLatchClearsOnlyWithExactRepairProof(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryManual)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected"}); err != nil {
		t.Fatal(err)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.Recovery != gatewayRecoveryManual || control.ActiveAttemptID != attempt.ID {
		t.Fatalf("ordinary heartbeat cleared manual repair latch: %#v", control)
	}
	nowAt := time.Now().UTC()
	if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt); err != nil {
		t.Fatal(err)
	}
	control = fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.Recovery != gatewayRecoveryNone || control.ActiveAttemptID != "" {
		t.Fatalf("exact repair proof did not atomically clear latch: %#v", control)
	}
}

func TestR1IntentPersistFailureProducesZeroServiceHooks(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	fixture.h.saveGatewayStateForTest = func(gatewayState) error { return errors.New("intent outcome unknown") }
	fixture.h.loadGatewayStateForTest = func(*gatewayState) (bool, error) { return false, errors.New("readback unavailable") }
	if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
		t.Fatal("indeterminate intent commit was accepted")
	}
	if got := adapter.snapshotCalls(); len(got) != 0 {
		t.Fatalf("service adapter ran without durable intent: %v", got)
	}
}

func TestR1ExecutableDriftBeforeIntentProducesZeroServiceHooks(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	plan := configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	if err := os.WriteFile(plan.Target.Executable, []byte("drifted executable\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
		t.Fatal("digest-mismatched executable was authorized")
	}
	if got := adapter.snapshotCalls(); len(got) != 0 {
		t.Fatalf("service adapter ran after executable drift: %v", got)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.ActiveAttemptID != "" || control.Recovery != gatewayRecoveryManual {
		t.Fatalf("rejected integrity check did not latch a zero-effect manual stop: %#v", control)
	}
}

func TestR1ConcurrentBeginsEmitAtMostOneEffect(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{
		applyResult:  gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
		applyEntered: make(chan struct{}, 1), applyRelease: make(chan struct{}),
	}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	errs := make(chan error, 2)
	go func() {
		_, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
		errs <- err
	}()
	<-adapter.applyEntered
	go func() {
		_, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
		errs <- err
	}()
	close(adapter.applyRelease)
	first, second := <-errs, <-errs
	if (first == nil) == (second == nil) {
		t.Fatalf("concurrent begin results = %v, %v; want exactly one accepted", first, second)
	}
	if got := adapter.snapshotCalls(); len(got) != 1 {
		t.Fatalf("concurrent begins emitted %d effects: %v", len(got), got)
	}
}

func TestR1MissingTargetProofTriggersBoundedRecovery(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	fixture.h.gatewayProofWaitForTest = 100 * time.Millisecond
	adapter := &fakeGatewayServiceAdapter{
		applyResult:    gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
		restoreResult:  gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied},
		restoreEntered: make(chan struct{}, 1),
	}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	adapter.setInspection(gatewayServiceInspection{State: gatewayServiceObservedTarget, Generation: attempt.TargetGeneration,
		Build: attempt.Plan.Target.Build, ExecutableDigest: attempt.Plan.Target.ExecutableDigest})
	select {
	case <-adapter.restoreEntered:
	case <-time.After(time.Second):
		t.Fatal("target proof deadline did not trigger recovery")
	}
	deadline := time.Now().Add(time.Second)
	for {
		fixture.h.mu.Lock()
		current := *fixture.h.gatewayState.Attempts[fixture.connection.ID]
		fixture.h.mu.Unlock()
		if current.Phase == gatewayAttemptAwaitingRecoveryProof {
			if current.RecoveryGeneration == current.TargetGeneration {
				t.Fatal("proof timeout reused target generation")
			}
			nowAt := time.Now().UTC()
			if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(current, true, nowAt), nowAt); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery effect did not reach proof wait: %#v", current)
		}
		time.Sleep(time.Millisecond)
	}
	calls := adapter.snapshotCalls()
	want := []string{"apply:" + attempt.ID + ":" + attempt.TargetGeneration, "inspect:" + attempt.ID,
		"restore:" + attempt.ID + ":" + attempt.RecoveryGeneration}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("proof timeout service calls = %v, want %v", calls, want)
	}
}

func TestR1StartupInspectionOfKnownAnchorOnlyRestores(t *testing.T) {
	fixture := newR0bFixture(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectIndeterminate, Err: errors.New("timeout")}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err == nil {
		t.Fatal("indeterminate target returned success")
	}
	fixture.h.Shutdown()
	fixture.h = nil
	reopened, err := Open(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h = reopened
	adapter.inspection = gatewayServiceInspection{State: gatewayServiceObservedAnchor, Build: attempt.Plan.Anchor.Descriptor.Build, ExecutableDigest: attempt.Plan.Anchor.Descriptor.ExecutableDigest}
	adapter.restoreResult = gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	calls := adapter.snapshotCalls()
	wantSuffix := []string{"inspect:" + attempt.ID, "restore:" + attempt.ID + ":" + attempt.RecoveryGeneration}
	if len(calls) != 3 || !reflect.DeepEqual(calls[1:], wantSuffix) {
		t.Fatalf("known-anchor reconcile replayed target or skipped recovery: %v", calls)
	}
}

func TestR1AcceptedProofAndLatchClearShareOneFoundationCommit(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	commits := 0
	fixture.h.saveGatewayStateForTest = func(next gatewayState) error {
		commits++
		storedAttempt := next.Attempts[fixture.connection.ID]
		storedControl := next.Controls[fixture.connection.ID]
		if storedAttempt == nil || storedAttempt.AcceptedProof == nil || storedControl.ActiveAttemptID != "" || storedControl.Recovery != gatewayRecoveryNone {
			return fmt.Errorf("partial terminal state: attempt=%#v control=%#v", storedAttempt, storedControl)
		}
		return nil
	}
	nowAt := time.Now().UTC()
	if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatalf("proof/latch terminal commits = %d, want 1", commits)
	}
}

func TestR1ConfiguredBindingMutationInvalidatesTypedPlanUntilExactReconciliation(t *testing.T) {
	fixture := newR0bFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	configureR1Fixture(t, &fixture, adapter, gatewayRecoveryNone)
	attempt, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	nowAt := time.Now().UTC()
	if _, err := fixture.h.acceptGatewayProcessProof(fixture.connection.ID, exactR1Proof(attempt, false, nowAt), nowAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{AccountRef: "changed-account"}); err != nil {
		t.Fatal(err)
	}
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	if control.Recovery != gatewayRecoveryReconcile || control.ActiveAttemptID != "" {
		t.Fatalf("binding mutation reused a stale launch plan: %#v", control)
	}
	if got := fixture.h.gatewayProcessEligibleConnectionIDs(); len(got) != 0 {
		t.Fatalf("stale launch plan remained automatic-eligible: %v", got)
	}
	if _, err := fixture.h.beginGatewayProcessAttempt(context.Background(), fixture.connection.ID, gatewayAttemptAutomatic); err == nil {
		t.Fatal("automatic restart consumed a launch plan from the old binding")
	}
	if calls := adapter.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("binding mutation emitted a service effect: %v", calls)
	}
}
