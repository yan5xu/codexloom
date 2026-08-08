//go:build windows

package store

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func verifyLocalLeasePath(path string) error {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return fmt.Errorf("cannot resolve data directory volume: %s", path)
	}
	root := volume + `\`
	pointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return fmt.Errorf("resolve data directory volume: %w", err)
	}
	if driveType := windows.GetDriveType(pointer); driveType == windows.DRIVE_REMOTE {
		return fmt.Errorf("non-local data directory filesystems are unsupported: %s", path)
	} else if driveType == windows.DRIVE_UNKNOWN || driveType == windows.DRIVE_NO_ROOT_DIR {
		return fmt.Errorf("data directory volume is unavailable: %s", path)
	}
	return nil
}

func lockWriterFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
}

func unlockWriterFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}
