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
	if knownRemoteFilesystemType(uint64(stat.Type)) {
		return fmt.Errorf("non-local data directory filesystems are unsupported: %s", path)
	}
	return nil
}

func knownRemoteFilesystemType(value uint64) bool {
	switch value {
	case 0x6969, // NFS_SUPER_MAGIC
		0x517B,     // SMB_SUPER_MAGIC
		0xFF534D42, // CIFS_MAGIC_NUMBER
		0x73757245, // CODA_SUPER_MAGIC
		0x564C,     // NCP_SUPER_MAGIC
		0x01021997, // V9FS_MAGIC (Plan 9 remote filesystem)
		0x5346414F, // AFS_SUPER_MAGIC
		0x00C36400, // CEPH_SUPER_MAGIC
		0x0BD00BD0, // LUSTRE_SUPER_MAGIC
		0x47504653, // GPFS_SUPER_MAGIC
		0x65735546: // FUSE_SUPER_MAGIC (may be remote; reject fail closed)
		return true
	default:
		return false
	}
}

func lockWriterFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockWriterFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
