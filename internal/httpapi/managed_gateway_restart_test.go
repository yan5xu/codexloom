package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRestartManagedGatewaysRestartsOnlyEnabledSupportedConnections(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}

	created := map[string]hub.PlatformConnection{}
	for _, provider := range []string{"lark", "slack", "parall", "custom"} {
		credentialRef, _, err := credentials.PutBound("restart-test/"+provider, credentialstore.Payload{
			Provider: provider, Kind: "test", Values: map[string]string{"value": randomTestCredential(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		connection, err := h.CreateConnection(hub.ConnectionParams{Provider: provider, CredentialRef: credentialRef})
		if err != nil {
			t.Fatal(err)
		}
		created[provider] = connection
	}
	disabled := false
	if _, err := h.UpdateConnection(created["slack"].ID, hub.ConnectionParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	previousRestart := restartManagedConnector
	previousPreflight := preflightManagedConnectorCredential
	previousProcessPreflight := preflightManagedConnectorProcess
	defer func() {
		restartManagedConnector = previousRestart
		preflightManagedConnectorCredential = previousPreflight
		preflightManagedConnectorProcess = previousProcessPreflight
	}()
	calls := []string{}
	preflightManagedConnectorCredential = func(_ *Server, _ hub.PlatformConnection) error { return nil }
	preflightManagedConnectorProcess = func(_ *Server, _ hub.PlatformConnection) error { return nil }
	restartManagedConnector = func(_ context.Context, _ *Server, connection hub.PlatformConnection) (bool, error) {
		provider, connectionID := managedGatewayProvider(connection.Provider), connection.ID
		calls = append(calls, fmt.Sprintf("%s:%s", provider, connectionID))
		return true, nil
	}

	New(h, st, nil).RestartManagedGateways()
	want := []string{
		"feishu:" + created["lark"].ID,
		"parall:" + created["parall"].ID,
	}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("managed gateway restarts = %#v, want %#v", calls, want)
	}
}

func TestRestartManagedGatewaysLeavesLegacyKeychainGatewayRunning(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	if _, err := h.CreateConnection(hub.ConnectionParams{
		Provider: "lark", AccountRef: "app_test", CredentialRef: "keychain:legacy-service",
	}); err != nil {
		t.Fatal(err)
	}
	previousRestart := restartManagedConnector
	previousPreflight := preflightManagedConnectorCredential
	previousProcessPreflight := preflightManagedConnectorProcess
	defer func() {
		restartManagedConnector = previousRestart
		preflightManagedConnectorCredential = previousPreflight
		preflightManagedConnectorProcess = previousProcessPreflight
	}()
	restartManagedConnector = func(context.Context, *Server, hub.PlatformConnection) (bool, error) {
		t.Fatal("legacy Keychain gateway was restarted")
		return false, nil
	}
	preflightManagedConnectorCredential = func(_ *Server, _ hub.PlatformConnection) error {
		t.Fatal("legacy Keychain gateway entered managed credential preflight")
		return nil
	}
	preflightManagedConnectorProcess = func(_ *Server, _ hub.PlatformConnection) error {
		t.Fatal("legacy Keychain gateway entered process preflight")
		return nil
	}
	New(h, st, nil).RestartManagedGateways()
}

func TestManagedGatewayProviderAliasesFeishu(t *testing.T) {
	for input, want := range map[string]string{
		"lark": "feishu", "Feishu": "feishu", "slack": "slack", "parall": "parall", "custom": "",
	} {
		if got := managedGatewayProvider(input); got != want {
			t.Fatalf("managedGatewayProvider(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDarwinManagedGatewayRestartRefreshesLaunchdRegistration(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd behavior is macOS-specific")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	label := "com.codexloom.parall.conn_test"
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := runManagedServiceCommand
	defer func() { runManagedServiceCommand = previous }()
	calls := []string{}
	runManagedServiceCommand = func(name string, arguments ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(arguments, " "))
		return nil, nil
	}

	restarted, err := restartManagedGatewayService("parall", "conn_test")
	if err != nil || !restarted {
		t.Fatalf("restart = %v, %v", restarted, err)
	}
	uid := fmt.Sprint(os.Getuid())
	want := []string{
		"launchctl bootout gui/" + uid + "/" + label,
		"launchctl bootstrap gui/" + uid + " " + unitPath,
		"launchctl kickstart -k gui/" + uid + "/" + label,
	}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("launchd calls = %#v, want %#v", calls, want)
	}
}

func TestDarwinManagedGatewayRestartRetriesTransientBootstrapFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd behavior is macOS-specific")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	label := "com.codexloom.parall.conn_retry"
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousCommand := runManagedServiceCommand
	previousWait := waitManagedServiceRetry
	defer func() {
		runManagedServiceCommand = previousCommand
		waitManagedServiceRetry = previousWait
	}()
	calls := []string{}
	bootstrapAttempts := 0
	runManagedServiceCommand = func(name string, arguments ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(arguments, " "))
		if len(arguments) > 0 && arguments[0] == "bootstrap" {
			bootstrapAttempts++
			if bootstrapAttempts == 1 {
				return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
			}
		}
		return nil, nil
	}
	waits := 0
	waitManagedServiceRetry = func(time.Duration) { waits++ }

	restarted, err := restartManagedGatewayService("parall", "conn_retry")
	if err != nil || !restarted {
		t.Fatalf("restart = %v, %v", restarted, err)
	}
	uid := fmt.Sprint(os.Getuid())
	want := []string{
		"launchctl bootout gui/" + uid + "/" + label,
		"launchctl bootstrap gui/" + uid + " " + unitPath,
		"launchctl bootstrap gui/" + uid + " " + unitPath,
		"launchctl kickstart -k gui/" + uid + "/" + label,
	}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("launchd calls = %#v, want %#v", calls, want)
	}
	if waits != 1 {
		t.Fatalf("retry waits = %d, want 1", waits)
	}
}

func TestDarwinManagedGatewayRestartDoesNotRetryPermanentBootstrapFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd behavior is macOS-specific")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	label := "com.codexloom.parall.conn_invalid"
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousCommand := runManagedServiceCommand
	previousWait := waitManagedServiceRetry
	defer func() {
		runManagedServiceCommand = previousCommand
		waitManagedServiceRetry = previousWait
	}()
	bootstrapAttempts := 0
	runManagedServiceCommand = func(_ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "bootstrap" {
			bootstrapAttempts++
			return []byte("Bootstrap failed: 5: malformed plist"), errors.New("exit status 5")
		}
		return nil, nil
	}
	waitManagedServiceRetry = func(time.Duration) {
		t.Fatal("permanent errors must not wait for a retry")
	}

	restarted, err := restartManagedGatewayService("parall", "conn_invalid")
	if err == nil || restarted {
		t.Fatalf("restart = %v, %v; want permanent bootstrap failure", restarted, err)
	}
	if bootstrapAttempts != 2 {
		t.Fatalf("bootstrap attempts = %d, want replacement plus recovery", bootstrapAttempts)
	}
}

func TestDarwinManagedGatewayRestartRecoversRegistrationAfterKickstartFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd behavior is macOS-specific")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	label := "com.codexloom.parall.conn_recover"
	unitPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := runManagedServiceCommand
	defer func() { runManagedServiceCommand = previous }()
	kickstarts := 0
	runManagedServiceCommand = func(_ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "kickstart" {
			kickstarts++
			if kickstarts == 1 {
				return []byte("replacement failed"), errors.New("exit status 1")
			}
		}
		return nil, nil
	}
	restarted, err := restartManagedGatewayService("parall", "conn_recover")
	var failure *managedGatewayRestartFailure
	if restarted || !errors.As(err, &failure) || !failure.Recovered || kickstarts != 2 {
		t.Fatalf("restart = %v, err=%v, failure=%#v, kickstarts=%d", restarted, err, failure, kickstarts)
	}
}

func TestRestartManagedGatewaysPersistsManualRecoveryWhenRegistrationCannotRecover(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	reference, _, err := credentials.PutBound("restart-manual/lark", credentialstore.Payload{
		Provider: "lark", Kind: "test", Values: map[string]string{"value": randomTestCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: reference})
	if err != nil {
		t.Fatal(err)
	}
	previousRestart := restartManagedConnector
	previousCredential := preflightManagedConnectorCredential
	previousProcess := preflightManagedConnectorProcess
	defer func() {
		restartManagedConnector = previousRestart
		preflightManagedConnectorCredential = previousCredential
		preflightManagedConnectorProcess = previousProcess
	}()
	preflightManagedConnectorCredential = func(*Server, hub.PlatformConnection) error { return nil }
	preflightManagedConnectorProcess = func(*Server, hub.PlatformConnection) error { return nil }
	restartManagedConnector = func(context.Context, *Server, hub.PlatformConnection) (bool, error) {
		return false, &managedGatewayRestartFailure{Stage: "test", Cause: errors.New("replacement and recovery failed")}
	}
	New(h, st, nil).RestartManagedGateways()
	var current hub.PlatformConnection
	for _, candidate := range h.ListConnections() {
		if candidate.ID == connection.ID {
			current = candidate
		}
	}
	if current.Status != "disconnected" || !strings.Contains(current.LastError, "manual_recovery_required") {
		t.Fatalf("manual recovery state = %#v", current)
	}
	h.Shutdown()
	reopened, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()
	found := false
	for _, candidate := range reopened.ListConnections() {
		if candidate.ID == connection.ID {
			found = true
			if candidate.Status != "disconnected" || !strings.Contains(candidate.LastError, "manual_recovery_required") {
				t.Fatalf("reopened manual recovery state = %#v", candidate)
			}
		}
	}
	if !found {
		t.Fatal("manual recovery connection was not durable across reopen")
	}
}

func TestRestartManagedGatewaysDropsStaleInitialSnapshotBeforeServiceEffect(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	reference, _, err := credentials.PutBound("restart-race/lark", credentialstore.Payload{
		Provider: "lark", Kind: "test", Values: map[string]string{"value": randomTestCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: reference})
	if err != nil {
		t.Fatal(err)
	}
	server := New(h, st, nil)

	previousBefore := beforeManagedConnectorRestartLock
	previousRestart := restartManagedConnector
	previousCredential := preflightManagedConnectorCredential
	previousProcess := preflightManagedConnectorProcess
	defer func() {
		beforeManagedConnectorRestartLock = previousBefore
		restartManagedConnector = previousRestart
		preflightManagedConnectorCredential = previousCredential
		preflightManagedConnectorProcess = previousProcess
	}()
	initialSnapshotReady := make(chan struct{})
	continueRestart := make(chan struct{})
	beforeManagedConnectorRestartLock = func(id string) {
		if id != connection.ID {
			t.Fatalf("restart barrier connection = %s", id)
		}
		close(initialSnapshotReady)
		<-continueRestart
	}
	preflightManagedConnectorCredential = func(*Server, hub.PlatformConnection) error { return nil }
	preflightManagedConnectorProcess = func(*Server, hub.PlatformConnection) error { return nil }
	var effects atomic.Int32
	restartManagedConnector = func(context.Context, *Server, hub.PlatformConnection) (bool, error) {
		effects.Add(1)
		return true, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.RestartManagedGateways()
	}()
	<-initialSnapshotReady
	request := httptest.NewRequest(http.MethodPatch, "/api/integrations/connections/"+connection.ID, strings.NewReader(`{"enabled":false}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("control update = %d: %s", response.Code, response.Body.String())
	}
	close(continueRestart)
	<-done
	if effects.Load() != 0 {
		t.Fatalf("stale restart service effects = %d, want 0", effects.Load())
	}
	current, err := h.SnapshotConnectionControl(connection.ID)
	if err != nil || current.Enabled {
		t.Fatalf("current control = %#v, err=%v", current, err)
	}
}

func TestVerifiedManagedGatewayRestartRecoversPreviousRegistrationAfterMissingHeartbeat(t *testing.T) {
	previousPrepare := prepareManagedConnectorRestart
	previousWrite := writeManagedConnectorRestartUnit
	previousService := restartManagedConnectorService
	previousWait := waitManagedConnectorRestartHeartbeat
	defer func() {
		prepareManagedConnectorRestart = previousPrepare
		writeManagedConnectorRestartUnit = previousWrite
		restartManagedConnectorService = previousService
		waitManagedConnectorRestartHeartbeat = previousWait
	}()
	digest := strings.Repeat("a", 64)
	plan := managedGatewayRestartPlan{
		Applicable: true, UnitPath: "/fixture/unit", OriginalUnit: []byte("old"), TargetUnit: []byte("new"),
		Previous: hub.CredentialMigrationGatewayReceipt{Build: "build-test", ExecutableSHA256: digest, Generation: "ggen-old"},
		Target:   hub.CredentialMigrationGatewayReceipt{Build: "build-test", ExecutableSHA256: digest, Generation: "ggen-new"},
	}
	prepareManagedConnectorRestart = func(*Server, hub.PlatformConnection) (managedGatewayRestartPlan, error) { return plan, nil }
	writes := []string{}
	writeManagedConnectorRestartUnit = func(_ string, payload []byte) error {
		writes = append(writes, string(payload))
		return nil
	}
	restarts := 0
	restartManagedConnectorService = func(string, string) (bool, error) {
		restarts++
		return true, nil
	}
	waits := 0
	waitManagedConnectorRestartHeartbeat = func(_ context.Context, _ *Server, _ string, _ time.Time, expected hub.CredentialMigrationGatewayReceipt) (string, error) {
		waits++
		if expected.Generation == plan.Target.Generation {
			return "", errors.New("replacement heartbeat missing")
		}
		return time.Now().UTC().Format(time.RFC3339Nano), nil
	}
	server := &Server{}
	restarted, err := server.restartManagedGatewayVerified(context.Background(), hub.PlatformConnection{ID: "conn-test", Provider: "lark"})
	var failure *managedGatewayRestartFailure
	if restarted || !errors.As(err, &failure) || !failure.Recovered {
		t.Fatalf("verified restart = %v, err=%v, failure=%#v", restarted, err, failure)
	}
	if fmt.Sprint(writes) != fmt.Sprint([]string{"new", "old"}) || restarts != 2 || waits != 2 {
		t.Fatalf("recovery writes=%v restarts=%d waits=%d", writes, restarts, waits)
	}
}

func TestManagedGatewayHeartbeatRequiresFreshExactProof(t *testing.T) {
	after := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	expected := hub.CredentialMigrationGatewayReceipt{
		Build: "build-test", ExecutableSHA256: strings.Repeat("b", 64), Generation: "ggen-target",
	}
	base := hub.PlatformConnection{
		ID: "conn-proof", Status: "connected", LastHeartbeatAt: after.Add(time.Second).Format(time.RFC3339Nano),
		GatewayBuild: expected.Build, GatewayExecutableSHA256: expected.ExecutableSHA256, GatewayGeneration: expected.Generation,
	}
	if _, ok := managedGatewayHeartbeat(nil, base.ID, after, expected); ok {
		t.Fatal("missing heartbeat matched")
	}
	mismatch := base
	mismatch.GatewayGeneration = "ggen-other"
	if _, ok := managedGatewayHeartbeat([]hub.PlatformConnection{mismatch}, base.ID, after, expected); ok {
		t.Fatal("mismatched generation heartbeat matched")
	}
	stale := base
	stale.LastHeartbeatAt = after.Add(-time.Nanosecond).Format(time.RFC3339Nano)
	if _, ok := managedGatewayHeartbeat([]hub.PlatformConnection{stale}, base.ID, after, expected); ok {
		t.Fatal("late stale heartbeat matched")
	}
	if heartbeat, ok := managedGatewayHeartbeat([]hub.PlatformConnection{base}, base.ID, after, expected); !ok || heartbeat != base.LastHeartbeatAt {
		t.Fatalf("fresh exact heartbeat = %q, %v", heartbeat, ok)
	}
}
