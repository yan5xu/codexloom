//go:build darwin || linux

package credentials

import (
	"context"
	"os"
	"os/exec"
)

func spawnWithCredentialFD(ctx context.Context, executable string, args, env []string, secret *os.File) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = append([]string(nil), env...)
	cmd.ExtraFiles = []*os.File{secret}
	return cmd, nil
}
