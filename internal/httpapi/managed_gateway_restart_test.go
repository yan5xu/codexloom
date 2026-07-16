package httpapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

	created := map[string]hub.PlatformConnection{}
	for _, provider := range []string{"lark", "slack", "parall", "custom"} {
		connection, err := h.CreateConnection(hub.ConnectionParams{Provider: provider})
		if err != nil {
			t.Fatal(err)
		}
		created[provider] = connection
	}
	disabled := false
	if _, err := h.UpdateConnection(created["slack"].ID, hub.ConnectionParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	previous := restartManagedConnectorService
	defer func() { restartManagedConnectorService = previous }()
	calls := []string{}
	restartManagedConnectorService = func(provider, connectionID string) (bool, error) {
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
	if bootstrapAttempts != 1 {
		t.Fatalf("bootstrap attempts = %d, want 1", bootstrapAttempts)
	}
}
