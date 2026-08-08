//go:build linux

package store

import "testing"

func TestOnlyExplicitLocalFilesystemTypesAreSupported(t *testing.T) {
	for _, value := range []uint64{0xEF53, 0x58465342, 0x9123683E, 0x01021994, 0x858458F6, 0x794C7630} {
		if !supportedLinuxLocalFilesystemType(value) {
			t.Fatalf("local filesystem magic %#x was rejected", value)
		}
	}
	for _, value := range []uint64{0x6969, 0x517B, 0xFF534D42, 0x73757245, 0x564C, 0x01021997, 0x5346414F, 0x00C36400, 0x0BD00BD0, 0x47504653, 0x65735546, 0xDEADBEEF} {
		if supportedLinuxLocalFilesystemType(value) {
			t.Fatalf("unknown or remote filesystem magic %#x was accepted", value)
		}
	}
}
