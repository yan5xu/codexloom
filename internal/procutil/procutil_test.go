package procutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHelperProcessSleep(t *testing.T) {
	if os.Getenv("PROCUTIL_HELPER") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

func TestExecutableName(t *testing.T) {
	name := ExecutableName("codex-loom")
	if runtime.GOOS == "windows" {
		if name != "codex-loom.exe" {
			t.Fatalf("got %q", name)
		}
	} else if name != "codex-loom" {
		t.Fatalf("got %q", name)
	}
}

func TestDefaultLogPath(t *testing.T) {
	path := DefaultLogPath("x.log")
	if filepath.Base(path) != "x.log" || !filepath.IsAbs(path) {
		t.Fatalf("got %q", path)
	}
}

func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ExecutableName("tool"))
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsExecutableFile(path, info) {
		t.Fatalf("%s should be executable", path)
	}
	plain := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(plain)
	if err != nil {
		t.Fatal(err)
	}
	if IsExecutableFile(plain, info) {
		t.Fatalf("%s should not be executable", plain)
	}
}

func TestAliveTerminateKill(t *testing.T) {
	if Alive(-1) || Alive(0) {
		t.Fatal("nonpositive PIDs must not be alive")
	}
	if !Alive(os.Getpid()) {
		t.Fatal("current process must be alive")
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessSleep")
	cmd.Env = append(os.Environ(), "PROCUTIL_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !Alive(pid) {
		t.Fatalf("child %d should be alive", pid)
	}
	if err := Terminate(pid); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	_ = cmd.Wait()
	if Alive(pid) {
		t.Fatalf("child %d should be gone after terminate", pid)
	}
	// Both stops of an already-gone process must be no-ops.
	if err := Terminate(pid); err != nil {
		t.Fatalf("terminate reaped process: %v", err)
	}
	if err := Kill(pid); err != nil {
		t.Fatalf("kill reaped process: %v", err)
	}
}

func TestCommandLineIdentifiesChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessSleep")
	cmd.Env = append(os.Environ(), "PROCUTIL_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() {
		_ = Kill(pid)
		_ = cmd.Wait()
	}()
	line, err := CommandLine(pid)
	if err != nil {
		t.Fatalf("command line: %v", err)
	}
	if !strings.Contains(line, "TestHelperProcessSleep") {
		t.Fatalf("command line %q does not identify the child", line)
	}
}
