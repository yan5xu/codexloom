//go:build linux

package store

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func verifyLocalLeasePath(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return fmt.Errorf("inspect data directory filesystem: %w", err)
	}
	if !supportedLinuxLocalFilesystemType(uint64(stat.Type)) {
		return fmt.Errorf("unknown or non-local data directory filesystem %#x is unsupported: %s", uint64(stat.Type), path)
	}
	return nil
}

func lockWriterFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockWriterFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
