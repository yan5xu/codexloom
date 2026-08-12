//go:build linux

package store

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func verifySupportedFilesystem(file *os.File) error {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return fmt.Errorf("inspect data directory filesystem: %w", err)
	}
	if supportedLinuxFilesystem(uint64(stat.Type)) {
		return nil
	}
	return fmt.Errorf("unsupported data directory filesystem type: 0x%x", uint64(stat.Type))
}

func supportedLinuxFilesystem(value uint64) bool {
	switch value {
	case 0xEF53, 0x58465342, 0x9123683E, 0x01021994, 0x858458F6, 0x794C7630:
		return true
	default:
		return false
	}
}
