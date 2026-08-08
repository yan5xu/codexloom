//go:build !darwin && !linux && !windows

package store

import (
	"fmt"
	"os"
)

func verifyLocalLeasePath(path string) error {
	return fmt.Errorf("data directory writer lease is unsupported on this platform: %s", path)
}

func lockWriterFile(*os.File) error   { return fmt.Errorf("data directory writer lease is unsupported") }
func unlockWriterFile(*os.File) error { return nil }
