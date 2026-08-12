//go:build darwin

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
	if stat.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Errorf("non-local data directory filesystems are unsupported")
	}
	return nil
}
