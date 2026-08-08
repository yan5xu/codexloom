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

func supportedLinuxLocalFilesystemType(value uint64) bool {
	switch value {
	case 0xEF53, // EXT2/3/4_SUPER_MAGIC
		0x58465342, // XFS_SUPER_MAGIC
		0x9123683E, // BTRFS_SUPER_MAGIC
		0x01021994, // TMPFS_MAGIC
		0x858458F6, // RAMFS_MAGIC
		0x794C7630: // OVERLAYFS_SUPER_MAGIC
		return true
	default:
		return false
	}
}

type dataDirWriterLease struct {
	requested       string
	canonical       string
	processKey      string
	provisionalFile *os.File
	file            *os.File
	root            *os.File
	rootAccess      *os.Root
	rootInfo        os.FileInfo
	once            sync.Once
	err             error
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

	processKey := "path:" + canonical
	processWriterLeases.Lock()
	if _, exists := processWriterLeases.held[processKey]; exists {
		processWriterLeases.Unlock()
		return nil, fmt.Errorf("data directory already has a writable CodexLoom process: %s", canonical)
	}
	processWriterLeases.held[processKey] = struct{}{}
	processWriterLeases.Unlock()

	releaseProcess := true
	defer func() {
		if releaseProcess {
			processWriterLeases.Lock()
			delete(processWriterLeases.held, processKey)
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
	return &dataDirWriterLease{requested: filepath.Clean(dir), canonical: canonical, processKey: processKey, provisionalFile: file}, nil
}

// stabilize replaces the caller-path bootstrap lease with an ownership key and
// OS lock derived from the opened data directory itself. The root handle stays
// open for the entire Store lifetime and every write revalidates that both the
// canonical path and the original caller path still resolve to this identity.
func (l *dataDirWriterLease) stabilize(dir string) error {
	if l == nil {
		return fmt.Errorf("data directory writer lease is unavailable")
	}
	canonical, err := canonicalDataDir(dir)
	if err != nil {
		return err
	}
	if err := verifyLocalLeasePath(canonical); err != nil {
		return err
	}
	root, err := os.Open(canonical)
	if err != nil {
		return fmt.Errorf("open stable data directory handle: %w", err)
	}
	rootInfo, err := root.Stat()
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("inspect stable data directory handle: %w", err)
	}
	identity, err := stableFileIdentity(root, rootInfo)
	if err != nil {
		_ = root.Close()
		return err
	}
	processKey := "fs:" + identity
	processWriterLeases.Lock()
	if _, exists := processWriterLeases.held[processKey]; exists {
		processWriterLeases.Unlock()
		_ = root.Close()
		return fmt.Errorf("data directory already has a writable CodexLoom process: %s", canonical)
	}
	processWriterLeases.held[processKey] = struct{}{}
	processWriterLeases.Unlock()
	releaseIdentity := true
	defer func() {
		if releaseIdentity {
			processWriterLeases.Lock()
			delete(processWriterLeases.held, processKey)
			processWriterLeases.Unlock()
		}
	}()

	rootAccess, err := os.OpenRoot(canonical)
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("open stable data directory root: %w", err)
	}
	file, err := rootAccess.OpenFile(".codex-loom-writer.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = rootAccess.Close()
		_ = root.Close()
		return fmt.Errorf("open stable data directory writer lease: %w", err)
	}
	if err := lockWriterFile(file); err != nil {
		_ = file.Close()
		_ = rootAccess.Close()
		_ = root.Close()
		return fmt.Errorf("data directory already has a writable CodexLoom process: %s: %w", canonical, err)
	}

	if l.provisionalFile != nil {
		_ = unlockWriterFile(l.provisionalFile)
		_ = l.provisionalFile.Close()
		l.provisionalFile = nil
	}
	processWriterLeases.Lock()
	delete(processWriterLeases.held, l.processKey)
	processWriterLeases.Unlock()
	l.canonical = canonical
	l.processKey = processKey
	l.file = file
	l.root = root
	l.rootAccess = rootAccess
	l.rootInfo = rootInfo
	releaseIdentity = false
	return nil
}

func (l *dataDirWriterLease) verify() error {
	if l == nil || l.root == nil || l.rootAccess == nil || l.rootInfo == nil || l.file == nil {
		return fmt.Errorf("stable data directory writer lease is unavailable")
	}
	if err := verifyLocalLeasePath(l.canonical); err != nil {
		return err
	}
	current, err := os.Stat(l.canonical)
	if err != nil {
		return fmt.Errorf("data directory identity changed: %w", err)
	}
	if !os.SameFile(l.rootInfo, current) {
		return fmt.Errorf("data directory identity changed at %s", l.canonical)
	}
	if requested, err := canonicalDataDir(l.requested); err != nil {
		return fmt.Errorf("data directory caller path identity changed: %w", err)
	} else if requested != l.canonical {
		requestedInfo, statErr := os.Stat(requested)
		if statErr != nil || !os.SameFile(l.rootInfo, requestedInfo) {
			return fmt.Errorf("data directory caller path identity changed: %s", l.requested)
		}
	}
	handleInfo, err := l.root.Stat()
	if err != nil || !os.SameFile(l.rootInfo, handleInfo) {
		return fmt.Errorf("data directory handle identity changed")
	}
	return nil
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
		if l.provisionalFile != nil {
			unlockErr := unlockWriterFile(l.provisionalFile)
			closeErr := l.provisionalFile.Close()
			if unlockErr != nil {
				l.err = unlockErr
			} else if closeErr != nil {
				l.err = closeErr
			}
		}
		if l.file != nil {
			unlockErr := unlockWriterFile(l.file)
			closeErr := l.file.Close()
			if unlockErr != nil {
				l.err = unlockErr
			} else {
				l.err = closeErr
			}
		}
		if l.root != nil {
			if closeErr := l.root.Close(); l.err == nil {
				l.err = closeErr
			}
		}
		if l.rootAccess != nil {
			if closeErr := l.rootAccess.Close(); l.err == nil {
				l.err = closeErr
			}
		}
		processWriterLeases.Lock()
		delete(processWriterLeases.held, l.processKey)
		processWriterLeases.Unlock()
	})
	return l.err
}
