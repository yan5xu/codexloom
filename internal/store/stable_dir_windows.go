//go:build windows

package store

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func stableFileIdentity(file *os.File, _ os.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", fmt.Errorf("inspect data directory filesystem identity: %w", err)
	}
	return fmt.Sprintf("%x:%x%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func verifySupportedFilesystem(file *os.File) error {
	path, err := filepath.Abs(file.Name())
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(path) + `\`
	p, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return err
	}
	typeID := windows.GetDriveType(p)
	if typeID != windows.DRIVE_FIXED && typeID != windows.DRIVE_RAMDISK {
		return fmt.Errorf("unsupported data directory drive type: %d", typeID)
	}
	return nil
}

func lockWriterFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func unlockWriterFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
