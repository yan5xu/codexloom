//go:build unix

package credentialstore

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type storeLock struct {
	file *os.File
}

func (l *storeLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func validateDataDirectory(path string) error {
	file, info, err := openDirectoryNoFollow(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	return validateUnixOwner(info)
}

func createOrValidatePrivateDirectory(path string) error {
	err := os.Mkdir(path, 0o700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	if err == nil {
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return validatePrivateDirectory(path)
}

func validatePrivateDirectory(path string) error {
	file, info, err := openDirectoryNoFollow(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := validateUnixOwner(info); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("directory permissions must be 0700")
	}
	return nil
}

func acquireStoreLock(path string) (*storeLock, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	created := false
	fd, err := unix.Open(path, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err == nil {
		created = true
	} else {
		if !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
		fd, err = unix.Open(path, flags, 0)
		if err != nil {
			return nil, err
		}
	}
	if created {
		if err := unix.Fchmod(fd, 0o600); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	file := os.NewFile(uintptr(fd), "credential-store-lock")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("lock file unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := validatePrivateFileInfo(info); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &storeLock{file: file}, nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "managed-credential")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("credential file unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validatePrivateFileInfo(info); err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("credential file size is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("credential file exceeds size limit")
	}
	return data, nil
}

func replacePrivateFile(root, target string, data []byte) error {
	if err := validatePrivateDirectory(root); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".credential-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	verified, err := readPrivateFile(temporaryPath, maxRecordBytes)
	if err != nil || !bytes.Equal(verified, data) {
		return errors.Join(err, errors.New("temporary credential verification failed"))
	}
	if err := validatePrivateDirectory(root); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return syncDirectory(root)
}

func removePrivateFile(path, root string) error {
	if err := validatePrivateDirectory(root); err != nil {
		return err
	}
	_, err := readPrivateFile(path, maxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(root)
}

func openDirectoryNoFollow(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("directory unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func validatePrivateFileInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("credential path is not a regular file")
	}
	if err := validateUnixOwner(info); err != nil {
		return err
	}
	mode := info.Mode()
	if mode.Perm() != 0o600 && mode.Perm() != 0o400 {
		return errors.New("credential file permissions must be 0600 or 0400")
	}
	if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("credential file has unsafe special permissions")
	}
	return nil
}

func validateUnixOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("file owner is unavailable")
	}
	return validateUnixOwnerID(stat.Uid, uint32(os.Geteuid()))
}

func validateUnixOwnerID(actual, current uint32) error {
	if actual != current {
		return errors.New("file is not owned by the current user")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, _, err := openDirectoryNoFollow(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
