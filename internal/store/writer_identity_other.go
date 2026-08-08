//go:build !darwin && !linux && !windows

package store

import (
	"fmt"
	"os"
)

func stableFileIdentity(*os.File, os.FileInfo) (string, error) {
	return "", fmt.Errorf("data directory filesystem identity is unsupported on this platform")
}
