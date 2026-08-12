package hub

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func TestL2aPhase2ConfigureFreezesPlanZeroEffectAndRevoke(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	adapter := &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) { return adapter, nil }

	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	if calls := adapter.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("maintenance configure performed a service effect: %v", calls)
	}
	fixture.h.mu.Lock()
	plan := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	stateVersion := fixture.h.gatewayState.Version
	fixture.h.mu.Unlock()
	if plan == nil || plan.Target.Provider == "" || plan.Target.ManagedCredentialRef != fixture.connection.CredentialRef {
		t.Fatalf("maintenance configure did not freeze the typed managed plan: %#v", plan)
	}
	if plan.IntegritySHA256 == "" || control == nil || control.ActiveAttemptID != "" {
		t.Fatalf("maintenance plan/control is not dormant: control=%#v", control)
	}
	if stateVersion != gatewayLaunchProofStateVersion {
		t.Fatalf("typed plan state version = %d", stateVersion)
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
	if calls := adapter.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("startup consumed the plan through the shared adapter: %v", calls)
	}
	proof := &GatewayProcessHeartbeatParams{
		AttemptID: attempt.ID, Generation: attempt.TargetGeneration, Build: attempt.Plan.Target.Build,
		ExecutableDigest: attempt.Plan.Target.ExecutableDigest,
	}
	if _, err := fixture.h.HeartbeatConnection(fixture.connection.ID, ConnectionHeartbeatParams{Status: "connected", GatewayProcess: proof}); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	terminal := fixture.h.gatewayState.Attempts[fixture.connection.ID]
	fixture.h.mu.Unlock()
	if terminal == nil || terminal.Phase != gatewayAttemptSucceeded || terminal.AcceptedProof == nil {
		t.Fatalf("exact proof did not terminate the startup attempt: %#v", terminal)
	}

	if err := fixture.h.RevokeLarkGatewayLaunch(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RevokeLarkGatewayLaunch(fixture.connection.ID); err != nil {
		t.Fatalf("revoke was not idempotent: %v", err)
	}
	fixture.h.mu.Lock()
	removed := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	fixture.h.mu.Unlock()
	if removed != nil {
		t.Fatalf("typed plan was not revoked: %#v", removed)
	}
	before := len(adapter.snapshotCalls())
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	if after := len(adapter.snapshotCalls()); after != before {
		t.Fatalf("revoked plan produced a startup effect: %v", adapter.snapshotCalls())
	}
}

func TestL2aPhase2ConfigureRequiresManagedBindingAndActiveAddress(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	fixture.h.gatewayServiceAdapterForTest = func(gatewayLaunchPlan) (gatewayServiceAdapter, error) {
		return &fakeGatewayServiceAdapter{applyResult: gatewayServiceEffectResult{Outcome: gatewayServiceEffectApplied}}, nil
	}
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := fixture.h.CreateConnection(ConnectionParams{
		Provider: "lark", AccountRef: "cli_legacy", CredentialRef: "keychain:com.codexloom.lark",
		Capabilities: []string{"receive_events", "proactive_send"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(legacy.ID, executable); err == nil {
		t.Fatal("configure accepted a legacy Keychain binding")
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, filepath.Join(fixture.dir, "missing-binary")); err == nil {
		t.Fatal("configure accepted a missing executable")
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := writeL2aAnchorUnit(t, fixture, executable, home); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, executable); err != nil {
		t.Fatalf("preflight failed for a configurable launch: %v", err)
	}
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, filepath.Join(fixture.dir, "missing-binary")); err == nil {
		t.Fatal("preflight accepted a missing executable")
	}
	label := "com.codexloom.feishu." + fixture.connection.ID
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if goruntime.GOOS == "linux" {
		unitPath = filepath.Join(home, ".config", "systemd", "user", "codexloom-feishu-"+fixture.connection.ID+".service")
	}
	if err := os.Remove(unitPath); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, executable); err == nil {
		t.Fatal("preflight accepted a missing registration anchor unit")
	}
}

func TestL2aPhase2RevokeFailsClosedOnActiveAttempt(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
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
	if err := fixture.h.RestartGatewayProcesses(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.RevokeLarkGatewayLaunch(fixture.connection.ID); err == nil {
		t.Fatal("revoke succeeded with an active process attempt")
	}
	fixture.h.mu.Lock()
	plan := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	fixture.h.mu.Unlock()
	if plan == nil {
		t.Fatal("revoke removed the typed plan despite the active attempt")
	}
}

func TestL2aPhase2RevokeClearsPlanDerivedReconcileAfterRollback(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
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
	legacyRef := "keychain:com.codexloom.lark"
	if _, err := fixture.h.UpdateConnection(fixture.connection.ID, ConnectionParams{CredentialRef: legacyRef}); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	drifted := fixture.h.gatewayState.Controls[fixture.connection.ID]
	driftedRecovery := drifted.Recovery
	fixture.h.mu.Unlock()
	if driftedRecovery != gatewayRecoveryReconcile {
		t.Fatalf("binding drift did not open as reconcile: %s", driftedRecovery)
	}
	if err := fixture.h.RevokeLarkGatewayLaunch(fixture.connection.ID); err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	control := fixture.h.gatewayState.Controls[fixture.connection.ID]
	plan := fixture.h.gatewayState.LaunchPlans[fixture.connection.ID]
	fixture.h.mu.Unlock()
	if control == nil || control.Recovery != gatewayRecoveryNone || control.ActiveAttemptID != "" {
		t.Fatalf("revoke did not return the control to the legacy path: %#v", control)
	}
	if plan != nil {
		t.Fatalf("revoke left the typed plan: %#v", plan)
	}
	if control.Binding.Connection.CredentialRef != legacyRef {
		t.Fatalf("revoke changed the restored legacy binding: %q", control.Binding.Connection.CredentialRef)
	}
}

func writeL2aAnchorUnit(t *testing.T, fixture r0bFixture, executable, home string) error {
	t.Helper()
	logPath := filepath.Join(fixture.st.Dir(), "gateway", "feishu-"+fixture.connection.ID+".log")
	hubURL := "http://127.0.0.1:4870"
	switch goruntime.GOOS {
	case "darwin":
		label := "com.codexloom.feishu." + fixture.connection.ID
		unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return err
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>` + label + `</string>
    <key>ProgramArguments</key>
    <array>
      <string>` + executable + `</string>
      <string>--hub</string>
      <string>` + hubURL + `</string>
      <string>--connection</string>
      <string>` + fixture.connection.ID + `</string>
      <string>--address</string>
      <string>` + fixture.address.ID + `</string>
      <string>--app-id</string>
      <string>` + fixture.connection.AccountRef + `</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict><key>CODEX_LOOM_DATA</key><string>` + fixture.st.Dir() + `</string></dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>ProcessType</key><string>Background</string>
    <key>StandardOutPath</key><string>` + logPath + `</string>
    <key>StandardErrorPath</key><string>` + logPath + `</string>
  </dict>
</plist>
`
		return os.WriteFile(unitPath, []byte(plist), 0o600)
	case "linux":
		service := "codexloom-feishu-" + fixture.connection.ID + ".service"
		unitPath := filepath.Join(home, ".config", "systemd", "user", service)
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return err
		}
		unit := `[Unit]
Description=CodexLoom native Feishu gateway (` + fixture.connection.ID + `)
After=network-online.target

[Service]
Type=simple
ExecStart=` + executable + ` --hub ` + hubURL + ` --connection ` + fixture.connection.ID + ` --address ` + fixture.address.ID + ` --app-id ` + fixture.connection.AccountRef + `
Environment=CODEX_LOOM_DATA=` + fixture.st.Dir() + `
Restart=always
RestartSec=2
StandardOutput=append:` + logPath + `
StandardError=append:` + logPath + `

[Install]
WantedBy=default.target
`
		return os.WriteFile(unitPath, []byte(unit), 0o600)
	default:
		t.Skipf("typed Lark launch anchor rehearsal is unsupported on %s", goruntime.GOOS)
		return nil
	}
}

func TestPreflightAnchorRenderMatchesInstalledUnitBytes(t *testing.T) {
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	executable := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.WriteFile(executable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := writeL2aAnchorUnit(t, fixture, executable, home); err != nil {
		t.Fatal(err)
	}
	launchSpec, err := fixture.h.larkGatewayLaunchSpec(fixture.connection.ID, executable)
	if err != nil {
		t.Fatal(err)
	}
	fixture.h.mu.Lock()
	connection := fixture.h.connections[fixture.connection.ID]
	fixture.h.mu.Unlock()
	anchorDescriptor := gatewayLaunchDescriptor{
		Manager: launchSpec.Manager, ConnectionID: launchSpec.ConnectionID,
		Provider: "lark", AddressID: launchSpec.AddressID, AccountRef: connection.AccountRef,
		ServiceID: launchSpec.ServiceID, UnitPath: launchSpec.UnitPath,
		Executable: launchSpec.Anchor.Executable, WorkingDirectory: launchSpec.WorkingDirectory,
		HubURL: launchSpec.HubURL, DataDir: launchSpec.DataDir, LogPath: launchSpec.LogPath,
		Build: launchSpec.Anchor.Build, ExecutableDigest: launchSpec.Anchor.ExecutableDigest,
	}
	rendered, err := renderGatewayServiceUnitForAttempt(anchorDescriptor, "", "")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(launchSpec.UnitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, installed) {
		t.Fatalf("preflight anchor unit mismatch:\nrendered:\n%s\ninstalled:\n%s", rendered, installed)
	}
}

func TestPreflightRejectsSymlinkExecutableLikeConfigure(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("symlink fixture is unsupported on windows")
	}
	fixture := newL2aFixture(t)
	defer fixture.close(t)
	realExecutable := filepath.Join(fixture.dir, "loom-feishu-gateway-real")
	if err := os.WriteFile(realExecutable, []byte("accepted gateway binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(fixture.dir, "loom-feishu-gateway")
	if err := os.Symlink(realExecutable, symlink); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.PreflightLarkGatewayLaunch(fixture.connection.ID, symlink); err == nil {
		t.Fatal("preflight accepted a symlink executable that configure rejects")
	}
	if err := fixture.h.ConfigureLarkGatewayLaunch(fixture.connection.ID, symlink); err == nil {
		t.Fatal("configure accepted a symlink executable")
	}
}
