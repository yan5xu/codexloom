// Package procutil provides the small set of process and path primitives that
// differ between Unix and native Windows: starting a detached child, stopping
// a process by PID, checking liveness, and naming executables and log files.
// Everything else in CodexLoom is expected to stay platform-neutral.
package procutil

import (
	"os"
	"path/filepath"
)

// ExecutableName appends the platform executable suffix (".exe" on Windows).
func ExecutableName(base string) string {
	return base + executableSuffix
}

// IsExecutableFile reports whether info describes a regular file that the
// platform would run: mode bits on Unix, extension on Windows.
func IsExecutableFile(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	return isExecutable(path, info)
}

// DefaultLogPath returns the historical /tmp location on Unix and the user
// temp directory on Windows, where /tmp does not exist.
func DefaultLogPath(name string) string {
	return filepath.Join(defaultLogDir(), name)
}
