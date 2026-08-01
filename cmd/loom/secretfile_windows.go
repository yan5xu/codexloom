//go:build windows

package main

import (
	"fmt"
	"os"
)

// Windows file modes reported by Go do not carry Unix owner-only semantics,
// and ACL verification has not been implemented yet. Refuse rather than
// silently accept a file whose protection cannot be verified.
func verifyOwnerOnlySecretFile(_ os.FileInfo) error {
	return fmt.Errorf("--agent-key-file owner-only verification is not supported on native Windows yet; run the import from WSL2 or a validated Unix host")
}
