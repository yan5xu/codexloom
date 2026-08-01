//go:build windows

package procutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const executableSuffix = ".exe"

const (
	detachedProcess                = 0x00000008
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259

	errorInvalidParameter = syscall.Errno(87)
)

// DetachedSysProcAttr starts a child in its own process group without a
// console, the closest Windows analogue to a new Unix session.
func DetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}

// Terminate stops pid. Windows has no cross-console graceful signal, so this
// is a hard TerminateProcess; callers relying on graceful shutdown must stop
// the process from its own console instead. A process that is already gone is
// not an error.
func Terminate(pid int) error {
	return Kill(pid)
}

// Kill forcibly ends pid. A process that is already gone is not an error.
func Kill(pid int) error {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if err == errorInvalidParameter {
			return nil
		}
		return err
	}
	defer syscall.CloseHandle(handle)
	return syscall.TerminateProcess(handle, 1)
}

// Alive reports whether pid refers to a running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}

// CommandLine returns the command line of pid, for identity checks before
// stopping a recorded process.
func CommandLine(pid int) (string, error) {
	filter := "ProcessId = " + strconv.Itoa(pid)
	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_Process -Filter '"+filter+"').CommandLine").Output()
	return strings.TrimSpace(string(output)), err
}

func isExecutable(path string, _ os.FileInfo) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".bat", ".cmd", ".com":
		return true
	}
	return false
}

func defaultLogDir() string {
	return os.TempDir()
}
