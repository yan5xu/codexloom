//go:build !unix && !windows

package credentialstore

import "errors"

type storeLock struct{}

func (l *storeLock) Close() error { return nil }

func validateDataDirectory(string) error {
	return errors.New("owner-only credential storage is unsupported on this platform")
}

func createOrValidatePrivateDirectory(string) error {
	return errors.New("owner-only credential storage is unsupported on this platform")
}

func validatePrivateDirectory(string) error {
	return errors.New("owner-only credential storage is unsupported on this platform")
}

func acquireStoreLock(string) (*storeLock, error) {
	return nil, errors.New("credential locking is unsupported on this platform")
}

func readPrivateFile(string, int64) ([]byte, error) {
	return nil, errors.New("owner-only credential storage is unsupported on this platform")
}

func replacePrivateFile(string, string, []byte) error {
	return errors.New("owner-only credential storage is unsupported on this platform")
}

func removePrivateFile(string, string) error {
	return errors.New("owner-only credential storage is unsupported on this platform")
}
