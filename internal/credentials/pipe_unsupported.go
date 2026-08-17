//go:build !darwin && !linux

package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func spawnWithCredentialFD(context.Context, string, []string, []string, *os.File) (*exec.Cmd, error) {
	return nil, fmt.Errorf("credential child descriptor inheritance is unsupported on this platform")
}
