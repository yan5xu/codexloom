package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SpawnWithCredentialFD launches an isolated child that receives the managed
// credential only on an inherited anonymous descriptor. The secret never
// appears in argv, the normal environment, logs, or durable state. This is the
// Lark-specific consumption path: no generic provider adapter or framework.
func SpawnWithCredentialFD(ctx context.Context, executable string, args, env []string, secret *os.File) (*exec.Cmd, error) {
	if secret == nil {
		return nil, fmt.Errorf("credential descriptor is required")
	}
	if err := verifyOwnerOnlyFile(secret); err != nil {
		return nil, err
	}
	if err := verifyChildExecutable(executable); err != nil {
		return nil, err
	}
	return spawnWithCredentialFD(ctx, executable, args, env, secret)
}

func verifyChildExecutable(executable string) error {
	absolute, err := filepath.Abs(executable)
	if err != nil || absolute != executable {
		return fmt.Errorf("credential child executable must be absolute and canonical")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 512<<20 {
		return fmt.Errorf("credential child executable is not a bounded regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("credential child executable is not executable")
	}
	return nil
}
