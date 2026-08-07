//go:build !unix

package httpapi

import "os/exec"

// Non-Unix platforms do not expose Setsid. Keep the portable exec defaults;
// the reloader still owns the platform-specific restart behavior.
func configureReloaderProcess(_ *exec.Cmd) {}
