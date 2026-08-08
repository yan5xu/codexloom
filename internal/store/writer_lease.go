package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var processWriterLeases = struct {
	sync.Mutex
	held map[string]struct{}
}{held: map[string]struct{}{}}

type dataDirWriterLease struct {
	canonical string
	file      *os.File
	once      sync.Once
	err       error
}

func acquireDataDirWriterLease(dir string) (*dataDirWriterLease, error) {
	canonical, err := canonicalDataDir(dir)
	if err != nil {
		return nil, err
	}
	leaseDirectory, err := existingLeaseDirectory(filepath.Dir(canonical))
	if err != nil {
		return nil, err
	}
	if err := verifyLocalLeasePath(leaseDirectory); err != nil {
		return nil, err
	}
	// An existing data directory can itself be a mount point. Check both it
	// and its parent so a local parent cannot mask a network-mounted store.
	if _, err := os.Stat(canonical); err == nil {
		if err := verifyLocalLeasePath(canonical); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect data directory filesystem: %w", err)
	}

	processWriterLeases.Lock()
	if _, exists := processWriterLeases.held[canonical]; exists {
		processWriterLeases.Unlock()
		return nil, fmt.Errorf("data directory already has a writable CodexLoom process: %s", canonical)
	}
	processWriterLeases.held[canonical] = struct{}{}
	processWriterLeases.Unlock()

	releaseProcess := true
	defer func() {
		if releaseProcess {
			processWriterLeases.Lock()
			delete(processWriterLeases.held, canonical)
			processWriterLeases.Unlock()
		}
	}()

	digest := sha256.Sum256([]byte(canonical))
	lockPath := filepath.Join(leaseDirectory, ".codex-loom-writer-"+hex.EncodeToString(digest[:12])+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open data directory writer lease: %w", err)
	}
	if err := lockWriterFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("data directory already has a writable CodexLoom process: %s: %w", canonical, err)
	}
	releaseProcess = false
	return &dataDirWriterLease{canonical: canonical, file: file}, nil
}

func existingLeaseDirectory(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("data directory parent is not a directory: %s", current)
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect data directory parent: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("data directory has no existing parent: %s", path)
		}
		current = parent
	}
}

func canonicalDataDir(dir string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve data directory symlinks: %w", resolveErr)
		}
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect data directory: %w", err)
	}

	missing := []string{}
	current := absolute
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", fmt.Errorf("resolve data directory parent: %w", resolveErr)
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect data directory parent: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("data directory has no existing parent: %s", absolute)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func (l *dataDirWriterLease) close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file != nil {
			unlockErr := unlockWriterFile(l.file)
			closeErr := l.file.Close()
			if unlockErr != nil {
				l.err = unlockErr
			} else {
				l.err = closeErr
			}
		}
		processWriterLeases.Lock()
		delete(processWriterLeases.held, l.canonical)
		processWriterLeases.Unlock()
	})
	return l.err
}
