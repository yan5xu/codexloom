//go:build unix

package httpapi

import (
	"os/exec"
	"testing"
)

func TestConfigureReloaderProcessStartsNewSession(t *testing.T) {
	command := exec.Command("reloader")
	configureReloaderProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setsid {
		t.Fatal("Unix reloader process was not configured to start a new session")
	}
}
