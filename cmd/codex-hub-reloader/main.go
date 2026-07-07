// codex-hub-reloader is a tiny external process that restarts codex-hub.
//
// The hub process must not terminate itself from inside an HTTP handler. The
// handler starts this process, returns to the browser, and then this process
// stops the old PID and launches the already-built replacement binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	oldPID := flag.Int("pid", 0, "codex-hub PID to replace")
	exe := flag.String("exe", "", "codex-hub executable to start")
	cwd := flag.String("cwd", "", "working directory for the new process")
	logPath := flag.String("log", "/tmp/codex-hub-reloader.log", "reloader log path")
	childLogPath := flag.String("child-log", "/tmp/codex-hub.log", "new codex-hub stdout/stderr log path")
	delay := flag.Duration("delay", 300*time.Millisecond, "delay before stopping the old process")
	timeout := flag.Duration("timeout", 15*time.Second, "time to wait for graceful shutdown")
	flag.Parse()

	if *oldPID <= 0 || *exe == "" {
		fmt.Fprintln(os.Stderr, "usage: codex-hub-reloader -pid PID -exe PATH [-cwd DIR] -- [codex-hub args...]")
		os.Exit(2)
	}

	reloaderLog, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open reloader log: %v\n", err)
		os.Exit(1)
	}
	defer reloaderLog.Close()
	log.SetOutput(reloaderLog)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Printf("restart requested oldPID=%d exe=%s cwd=%s args=%q", *oldPID, *exe, *cwd, flag.Args())
	time.Sleep(*delay)

	if processAlive(*oldPID) {
		log.Printf("sending SIGTERM to oldPID=%d", *oldPID)
		if err := syscall.Kill(*oldPID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			log.Fatalf("send SIGTERM: %v", err)
		}
	}

	deadline := time.Now().Add(*timeout)
	for processAlive(*oldPID) && time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
	}
	if processAlive(*oldPID) {
		log.Printf("oldPID=%d still alive after %s; sending SIGKILL", *oldPID, *timeout)
		if err := syscall.Kill(*oldPID, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			log.Fatalf("send SIGKILL: %v", err)
		}
		killDeadline := time.Now().Add(3 * time.Second)
		for processAlive(*oldPID) && time.Now().Before(killDeadline) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	childLog, err := os.OpenFile(*childLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Fatalf("open child log: %v", err)
	}
	defer childLog.Close()

	cmd := exec.Command(*exe, flag.Args()...)
	cmd.Env = os.Environ()
	cmd.Stdout = childLog
	cmd.Stderr = childLog
	if *cwd != "" {
		cmd.Dir = *cwd
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Fatalf("start new codex-hub: %v", err)
	}
	log.Printf("started new codex-hub pid=%d", cmd.Process.Pid)
	_ = cmd.Process.Release()
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
