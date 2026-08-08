//go:build darwin

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterLeaseRejectsCaseAliasOnCaseInsensitiveDarwinVolume(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "GatewayData")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "gatewaydata")
	if _, err := os.Stat(alias); err != nil {
		t.Skipf("test volume is case-sensitive: %v", err)
	}
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := Open(alias); err == nil || !strings.Contains(err.Error(), "already has a writable CodexLoom process") {
		t.Fatalf("case alias acquired an independent writer lease: %v", err)
	}
}
