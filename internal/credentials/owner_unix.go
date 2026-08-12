//go:build darwin || linux

package credentials

import (
	"fmt"
	"os"
	"syscall"
)

func verifyOwnerOnlyFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("credential file is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return verifyOwnerOnlyStat(info, false)
}

func verifyOwnerOnlyPath(path string, isDir bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return verifyOwnerOnlyStat(info, isDir)
}

func verifyOwnerOnlyStat(info os.FileInfo, isDir bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fmt.Errorf("credential ownership is unavailable")
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("credential is not owner-only")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("credential path is a symbolic link")
	}
	if isDir {
		if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("credential directory is not owner-only")
		}
		return nil
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("credential file is not owner-only")
	}
	return nil
}
