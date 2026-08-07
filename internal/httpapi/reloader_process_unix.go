//go:build unix

package httpapi

import (
	"os/exec"
	"syscall"
)

func configureReloaderProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
