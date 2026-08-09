//go:build !darwin

package main

import "fmt"

func launchctlAction(action, label, plist string) error {
	return fmt.Errorf("launchctl rehearsal is only supported on darwin")
}
