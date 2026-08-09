//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func launchctlAction(action, label, plist string) error {
	switch action {
	case "unload":
		target := "gui/$(id -u)/" + label
		output, err := exec.Command("/bin/sh", "-c", "launchctl bootout "+target+" 2>/dev/null; exit 0").CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl bootout %s: %v %s", label, err, strings.TrimSpace(string(output)))
		}
		return nil
	case "load":
		output, err := exec.Command("/bin/sh", "-c", "launchctl bootstrap gui/$(id -u) "+plist).CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl bootstrap %s: %v %s", label, err, strings.TrimSpace(string(output)))
		}
		return nil
	default:
		return fmt.Errorf("unsupported launchctl action %q", action)
	}
}
