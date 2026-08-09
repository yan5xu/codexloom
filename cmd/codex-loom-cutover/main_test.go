//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const childEnv = "CODEXLOOM_CUTOVER_CHILD"

func TestL2CutoverRehearsalSuccessAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "rollback"} {
		t.Run(scenario, func(t *testing.T) { rehearse(t, scenario == "rollback") })
	}
}

func rehearse(t *testing.T, failMigrate bool) {
	t.Helper()
	base := t.TempDir()
	workdir := filepath.Join(base, "workdir")
	dataDir := filepath.Join(base, "data")
	binDir := filepath.Join(base, "bin")
	for _, dir := range []string{workdir, dataDir, binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldScript := filepath.Join(binDir, "hub-old.sh")
	newScript := filepath.Join(binDir, "hub-new.sh")
	target := filepath.Join(binDir, "hub.sh")
	oldContent := "#!/bin/sh\n" + fmt.Sprintf("echo started-old >> \"$CODEX_LOOM_CUTOVER_DATA/markers\"; echo $$ > \"$CODEX_LOOM_CUTOVER_DATA/hub.pid\"; sleep 120\n")
	newContent := "#!/bin/sh\n" + fmt.Sprintf("echo started-new >> \"$CODEX_LOOM_CUTOVER_DATA/markers\"; echo $$ > \"$CODEX_LOOM_CUTOVER_DATA/hub.pid\"; sleep 120\n")
	if err := os.WriteFile(oldScript, []byte(oldContent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newScript, []byte(newContent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "original-marker"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectedOld := sha256Hex(oldScript)
	expectedNew := sha256Hex(newScript)
	label := fmt.Sprintf("codexloom.l2test.%d", os.Getpid())
	plist := filepath.Join(base, "hub.plist")
	if err := os.WriteFile(plist, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string></array>
<key>EnvironmentVariables</key><dict><key>CODEX_LOOM_CUTOVER_DATA</key><string>%s</string></dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><false/>
</dict></plist>`, label, target, dataDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = launchctlAction("unload", label, plist)
	}
	cleanup()
	t.Cleanup(cleanup)

	migrateCommand := "touch \"$CODEX_LOOM_CUTOVER_DATA/migrated\""
	if failMigrate {
		migrateCommand = "kill $(cat \"$CODEX_LOOM_CUTOVER_DATA/hub.pid\") 2>/dev/null; touch \"$CODEX_LOOM_CUTOVER_DATA/hub-killed\"; exit 1"
	}
	book := runbook{
		Stages: []stage{
			{Name: "precheck-old", Kind: "hash", Path: oldScript, Expected: expectedOld},
			{Name: "precheck-new", Kind: "hash", Path: newScript, Expected: expectedNew},
			{Name: "snapshot", Kind: "snapshot"},
			{Name: "stop", Kind: "launchctl", Action: "unload", Label: label, Plist: plist},
			{Name: "install", Kind: "copy", Src: newScript, Dst: target},
			{Name: "migrate", Kind: "exec", Command: migrateCommand},
			{Name: "start", Kind: "launchctl", Action: "load", Label: label, Plist: plist},
			{Name: "verify", Kind: "exec", Command: "sleep 1; test -f \"$CODEX_LOOM_CUTOVER_DATA/migrated\" && grep -q started-new \"$CODEX_LOOM_CUTOVER_DATA/markers\""},
		},
		Rollback: []stage{
			{Name: "rb-stop", Kind: "launchctl", Action: "unload", Label: label, Plist: plist},
			{Name: "rb-restore-data", Kind: "restore"},
			{Name: "rb-restore-bin", Kind: "copy", Src: oldScript, Dst: target},
			{Name: "rb-start", Kind: "launchctl", Action: "load", Label: label, Plist: plist},
			{Name: "rb-verify", Kind: "exec", Command: "test -f \"$CODEX_LOOM_CUTOVER_DATA/original-marker\""},
		},
	}
	runbookPath := filepath.Join(workdir, "runbook.json")
	payload, _ := json.MarshalIndent(book, "", "  ")
	if err := os.WriteFile(runbookPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(base, "snapshot.tar")
	receiptFile := filepath.Join(workdir, "receipt.json")
	cmd := exec.Command(os.Args[0], "-test.run=TestL2CutoverRehearsalSuccessAndRollback")
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		"CODEXLOOM_CUTOVER_WORKDIR="+workdir,
		"CODEXLOOM_CUTOVER_DATA="+dataDir,
		"CODEXLOOM_CUTOVER_RUNBOOK="+runbookPath,
		"CODEXLOOM_CUTOVER_SNAPSHOT="+snapshot,
		"CODEXLOOM_CUTOVER_RECEIPT="+receiptFile,
	)
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	// Give the supervisor time to reach the migrate stage; the dummy hub must
	// be running before the rollback path kills it.
	time.Sleep(800 * time.Millisecond)
	select {
	case err := <-done:
		if failMigrate {
			if err == nil {
				t.Fatalf("supervisor unexpectedly succeeded on rollback scenario: %s", output.String())
			}
		} else if err != nil {
			t.Fatalf("supervisor failed on success scenario: %v %s", err, output.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("supervisor did not finish: %s", output.String())
	}
	recData, err := os.ReadFile(receiptFile)
	if err != nil {
		t.Fatal(err)
	}
	var rec receipt
	if err := json.Unmarshal(recData, &rec); err != nil {
		t.Fatal(err)
	}
	if failMigrate {
		if rec.Status != "rolled_back" || !rec.RolledBack {
			t.Fatalf("rollback receipt = %#v", rec)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "migrated")); !os.IsNotExist(err) {
			t.Fatalf("rollback left migrated marker: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "original-marker")); err != nil {
			t.Fatalf("rollback did not restore data snapshot: %v", err)
		}
		got, _ := sha256File(target)
		if got != expectedOld {
			t.Fatalf("rollback did not restore old binary: got %s want %s", got, expectedOld)
		}
	} else {
		if rec.Status != "ok" {
			t.Fatalf("success receipt = %#v", rec)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "migrated")); err != nil {
			t.Fatalf("migrate marker missing: %v", err)
		}
		got, _ := sha256File(target)
		if got != expectedNew {
			t.Fatalf("install did not place the new binary: got %s want %s", got, expectedNew)
		}
	}
	// The rehearsal must clean up its temporary launchd job.
	cleanup()
	time.Sleep(500 * time.Millisecond)
	listed, _ := exec.Command("launchctl", "list").Output()
	if strings.Contains(string(listed), label) {
		t.Fatalf("temporary launchd label %s was not cleaned up", label)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		err := run(
			os.Getenv("CODEXLOOM_CUTOVER_WORKDIR"),
			os.Getenv("CODEXLOOM_CUTOVER_DATA"),
			os.Getenv("CODEXLOOM_CUTOVER_RUNBOOK"),
			os.Getenv("CODEXLOOM_CUTOVER_SNAPSHOT"),
			os.Getenv("CODEXLOOM_CUTOVER_RECEIPT"),
			false,
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func sha256Hex(path string) string {
	got, err := sha256File(path)
	if err != nil {
		return ""
	}
	return got
}
