//go:build linux

package store

import "testing"

func TestS0LinuxFilesystemAllowlistRejectsUnknown(t *testing.T) {
	if supportedLinuxFilesystem(0xdeadbeef) {
		t.Fatal("unknown filesystem type was accepted")
	}
}
