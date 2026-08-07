//go:build windows

package credentialstore

import (
	"bytes"
	"errors"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type storeLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func (l *storeLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	handle := windows.Handle(l.file.Fd())
	unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func validateDataDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("not a trusted directory")
	}
	if reparse, err := isReparsePoint(path); err != nil || reparse {
		return errors.Join(err, errors.New("directory is a reparse point"))
	}
	return validateWindowsACL(path)
}

func createOrValidatePrivateDirectory(path string) error {
	err := os.Mkdir(path, 0o700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	if err == nil {
		if err := applyCurrentUserACL(path); err != nil {
			return err
		}
	}
	return validatePrivateDirectory(path)
}

func validatePrivateDirectory(path string) error { return validateDataDirectory(path) }

func acquireStoreLock(path string) (*storeLock, error) {
	_, statErr := os.Lstat(path)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return nil, statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if created {
		if err := applyCurrentUserACL(path); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if err := validatePrivatePath(path, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	lock := &storeLock{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	if err := validatePrivatePath(path, false); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
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
	if err := applyCurrentUserACL(temporaryPath); err != nil {
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
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	committed = true
	return nil
}

func removePrivateFile(path, root string) error {
	if err := validatePrivateDirectory(root); err != nil {
		return err
	}
	if err := validatePrivatePath(path, false); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validatePrivatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || !directory && !info.Mode().IsRegular() {
		return errors.New("credential path type is unsafe")
	}
	if reparse, err := isReparsePoint(path); err != nil || reparse {
		return errors.Join(err, errors.New("credential path is a reparse point"))
	}
	return validateWindowsACL(path)
}

func isReparsePoint(path string) (bool, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func currentUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func applyCurrentUserACL(path string) error {
	current, err := currentUserSID()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	sddl := "O:" + current.String() + "D:P(A;;GA;;;" + current.String() + ")(A;;GA;;;" + system.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil,
	)
}

func validateWindowsACL(path string) error {
	current, err := currentUserSID()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(current) {
		return errors.Join(err, errors.New("path is not owned by the current user"))
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.Join(err, errors.New("path has no restrictive DACL"))
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("path has an unsupported access grant")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(current) && !sid.Equals(system) {
			return errors.New("path grants access outside the current user")
		}
	}
	return nil
}
