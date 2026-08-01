//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func verifyOwnerOnlySecretFile(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 {
		return fmt.Errorf("secret file permissions must be owner-only (0600 or 0400), got %04o", info.Mode().Perm())
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("secret file must be owned by the current user")
	}
	return nil
}
