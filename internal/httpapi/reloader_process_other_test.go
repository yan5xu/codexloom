//go:build !unix

package httpapi

import (
	"os/exec"
	"testing"
)

func TestConfigureReloaderProcessUsesPortableDefaults(t *testing.T) {
	command := exec.Command("reloader")
	configureReloaderProcess(command)
	if command.SysProcAttr != nil {
		t.Fatal("non-Unix reloader process unexpectedly received platform process attributes")
	}
}
