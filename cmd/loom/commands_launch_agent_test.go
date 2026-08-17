package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/launchagent"
)

func TestInstallManagedLaunchAgentInjectsOnlyExplicitCanonicalProxy(t *testing.T) {
	root := t.TempDir()
	launchAgents := filepath.Join(root, "LaunchAgents")
	if err := os.Mkdir(launchAgents, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(launchAgents, "com.pinix.codex-loom.plist")
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	t.Setenv("NO_PROXY", "localhost,EXAMPLE.invalid")
	t.Setenv("no_proxy", "example.invalid,127.0.0.1")
	t.Setenv("CODEX_LOOM_NO_PROXY", ".service.invalid")
	// A Provider config is present to prove the installer does not inspect it.
	configHome := filepath.Join(root, ".codex")
	if err := os.Mkdir(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "config.toml"), []byte(`base_url = "https://auto-provider.invalid/v1"`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", configHome)

	installedPath, inspection, err := installManagedLaunchAgent(args{flags: map[string]string{
		"executable": executable, "working-directory": root, "output": path,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if installedPath != path || inspection.Proxy.EntryCount != 4 {
		t.Fatalf("installed path=%q inspection=%#v", installedPath, inspection)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>localhost,EXAMPLE.invalid,127.0.0.1,.service.invalid</string>") {
		t.Fatalf("plist does not contain canonical explicit value: %s", data)
	}
	if strings.Contains(string(data), "auto-provider.invalid") {
		t.Fatalf("plist contains inferred Provider host: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plist mode = %#o", info.Mode().Perm())
	}
}

func TestDeployPlistDeclaresManagedProxyVariable(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", launchagent.Label+".plist")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<key>CODEX_LOOM_NO_PROXY</key>") {
		t.Fatal("deploy plist does not declare CODEX_LOOM_NO_PROXY")
	}
}

func TestInstallManagedLaunchAgentUpdatesExistingUnitWithoutDroppingCustomCodex(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _, err := launchagent.Render(launchagent.Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/bin", NoProxy: "old.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	withCustomCodex := strings.Replace(string(data), "    <key>PATH</key>", `    <key>CODEX_LOOM_CODEX_BIN</key>
    <string>/opt/codex/bin/codex</string>
    <key>PATH</key>`, 1)
	path := filepath.Join(root, "unit.plist")
	if err := os.WriteFile(path, []byte(withCustomCodex), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_PROXY", "new.invalid")
	t.Setenv("no_proxy", "")
	t.Setenv("CODEX_LOOM_NO_PROXY", "")
	_, inspection, err := installManagedLaunchAgent(args{flags: map[string]string{"output": path}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "/opt/codex/bin/codex") || inspection.Proxy.EntryCount != 2 {
		t.Fatalf("updated plist=%s inspection=%#v", updated, inspection)
	}
}

func TestInstallManagedLaunchAgentStopsBeforeWriteOnAnchorMismatch(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex-loom")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _, err := launchagent.Render(launchagent.Config{
		Executable: executable, WorkingDirectory: root, Path: "/usr/bin", NoProxy: "old.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unit.plist")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_PROXY", "new.invalid")
	t.Setenv("no_proxy", "")
	t.Setenv("CODEX_LOOM_NO_PROXY", "")
	_, _, err = installManagedLaunchAgent(args{flags: map[string]string{
		"output": path, "executable": "/unexpected/codex-loom",
	}})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("anchor mismatch error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(data) {
		t.Fatal("anchor mismatch modified the existing plist")
	}
}
