//go:build !windows

package procutil

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const executableSuffix = ""

// DetachedSysProcAttr detaches a child from the caller's session so it
// survives the caller's exit and does not receive its terminal signals.
func DetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// Terminate asks pid to shut down gracefully. A process that is already gone
// is not an error.
func Terminate(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	if err == nil || err == syscall.ESRCH {
		return nil
	}
	return err
}

// Kill forcibly ends pid. A process that is already gone is not an error.
func Kill(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if err == nil || err == syscall.ESRCH {
		return nil
	}
	return err
}

// Alive reports whether pid refers to a running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// CommandLine returns the command line of pid, for identity checks before
// stopping a recorded process.
func CommandLine(pid int) (string, error) {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	return strings.TrimSpace(string(output)), err
}

func isExecutable(_ string, info os.FileInfo) bool {
	return info.Mode()&0o111 != 0
}

func defaultLogDir() string {
	return "/tmp"
}
