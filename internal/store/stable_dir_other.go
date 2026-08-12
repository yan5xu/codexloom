//go:build !darwin && !linux && !windows

package store

import (
	"fmt"
	"os"
)

func stableFileIdentity(*os.File, os.FileInfo) (string, error) {
	return "", fmt.Errorf("stable data directory identity is unsupported on this platform")
}
func verifySupportedFilesystem(*os.File) error {
	return fmt.Errorf("data directory filesystem is unsupported on this platform")
}
func lockWriterFile(*os.File) error   { return fmt.Errorf("writer leases are unsupported") }
func unlockWriterFile(*os.File) error { return nil }
