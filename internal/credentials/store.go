// Package credentials implements the v1 managed credential file store.
//
// Credentials are immutable: every Put writes a fresh random ID and never
// overwrites an existing file in place. All durable mutations go through the
// stable data-directory root with live-Hub writer ownership, so the merged S0
// single-writer invariant still holds; reads use a read-only stable view.
// Only macOS/POSIX is supported in this stage.
package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yan5xu/codex-loom/internal/store"
)

// DirectoryName is the fixed Owner-only credential directory beneath the
// Runtime data directory. Ordinary backups must exclude it.
const DirectoryName = "credentials"

const (
	idBytes   = 32
	idHexLen  = idBytes * 2
	maxSecret = 1 << 20
	dirMode   = 0o700
	fileMode  = 0o600
)

// Ref is the canonical Hub-issued reference for one managed credential. It is
// always "managed:" followed by the random opaque ID; it is never a path.
type Ref string

var errCredentialNotFound = errors.New("managed credential not found")

// IsCredentialNotFound reports whether err indicates a managed credential is
// absent, which is an idempotent success for delete/rollback flows.
func IsCredentialNotFound(err error) bool {
	return errors.Is(err, errCredentialNotFound)
}

// Store manages the fixed Owner-only credentials directory inside one stable
// data directory.
type Store struct {
	st *store.Store
}

// New returns a v1 credential store bound to a stable Store. Mutations require
// a live writable Hub owner; non-POSIX platforms fail closed.
func New(st *store.Store) (*Store, error) {
	if st == nil {
		return nil, fmt.Errorf("credential store requires a stable Store")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("managed credentials are unsupported on %s", runtime.GOOS)
	}
	if !st.HasLiveWritableOwner() {
		return nil, fmt.Errorf("managed credentials require a live writable Hub owner")
	}
	return &Store{st: st}, nil
}

// ResolveReadOnly resolves one canonical managed reference through the stable
// read-only view without requiring a live writable Hub owner. It is the narrow
// same-UID consumption path for child processes such as the Feishu gateway,
// which must never write credentials. Canonical reference parsing and
// owner-only file verification still apply; unsupported platforms fail closed.
func ResolveReadOnly(st *store.Store, ref Ref) ([]byte, error) {
	if st == nil {
		return nil, fmt.Errorf("credential store requires a stable Store")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("managed credentials are unsupported on %s", runtime.GOOS)
	}
	credentialStore := Store{st: st}
	return credentialStore.Resolve(ref)
}

// Put durably writes one new immutable credential and returns its canonical
// reference. The secret is written to a temporary file and atomically renamed
// to a fresh random ID; an existing ID is never overwritten.
func (s *Store) Put(secret []byte) (Ref, error) {
	if !s.st.CredentialFloorPresent() {
		return "", fmt.Errorf("credential floor must be raised before the first managed credential write")
	}
	if len(secret) == 0 || len(secret) > maxSecret {
		return "", fmt.Errorf("credential secret must be between 1 byte and %d bytes", maxSecret)
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	target := filepath.Join(DirectoryName, id)
	err = s.st.WithStableWriteRoot(func(root *os.Root) error {
		if err := root.MkdirAll(DirectoryName, dirMode); err != nil {
			return err
		}
		dirInfo, err := root.Stat(DirectoryName)
		if err != nil {
			return err
		}
		if err := verifyOwnerOnlyStat(dirInfo, true); err != nil {
			return err
		}
		if err := verifyExactDirectoryName(root, dirInfo); err != nil {
			return err
		}
		temporary := filepath.Join(DirectoryName, ".managed-credential-"+randomSuffix())
		file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			_ = file.Close()
			if !committed {
				_ = root.Remove(temporary)
			}
		}()
		if err := file.Chmod(fileMode); err != nil {
			return err
		}
		if _, err := file.Write(secret); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if _, err := root.Lstat(target); err == nil {
			return fmt.Errorf("credential ID collision")
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := root.Rename(temporary, target); err != nil {
			return err
		}
		committed = true
		if err := syncStableDir(root, DirectoryName); err != nil {
			return fmt.Errorf("credential write outcome is indeterminate: %w", err)
		}
		info, err := root.Stat(target)
		if err != nil {
			return err
		}
		return verifyOwnerOnlyStat(info, false)
	})
	if err != nil {
		return "", err
	}
	return Ref("managed:" + id), nil
}

// Resolve returns the secret bytes for one canonical reference through the
// read-only stable view.
func (s *Store) Resolve(ref Ref) ([]byte, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	relative := filepath.Join(DirectoryName, id)
	info, err := s.st.StatStableFile(relative)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errCredentialNotFound
		}
		return nil, err
	}
	if err := verifyOwnerOnlyStat(info, false); err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > maxSecret {
		return nil, fmt.Errorf("managed credential is not a bounded regular file")
	}
	data, err := s.st.ReadStableFile(relative)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("managed credential changed or is truncated")
	}
	return data, nil
}

// OpenSecretForChild opens the owner-only secret file for anonymous descriptor
// inheritance into an isolated Lark gateway child. The returned file must not
// outlive the caller.
func (s *Store) OpenSecretForChild(ref Ref) (*os.File, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	file, err := s.st.OpenStableFile(filepath.Join(DirectoryName, id), os.O_RDONLY)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errCredentialNotFound
		}
		return nil, err
	}
	if err := verifyOwnerOnlyFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// Delete removes one canonical managed credential through the stable write
// capability. A post-effect directory sync failure is reported as
// indeterminate rather than committed.
func (s *Store) Delete(ref Ref) error {
	id, err := parseRef(ref)
	if err != nil {
		return err
	}
	relative := filepath.Join(DirectoryName, id)
	err = s.st.WithStableWriteRoot(func(root *os.Root) error {
		if err := root.Remove(relative); err != nil {
			if os.IsNotExist(err) {
				return errCredentialNotFound
			}
			return err
		}
		if err := syncStableDir(root, DirectoryName); err != nil {
			return fmt.Errorf("credential delete outcome is indeterminate: %w", err)
		}
		return nil
	})
	if errors.Is(err, errCredentialNotFound) {
		return err
	}
	if err != nil {
		return err
	}
	return nil
}

func syncStableDir(root *os.Root, relative string) error {
	directory, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// verifyExactDirectoryName rejects a pre-existing case alias whose actual
// directory name differs from the fixed name on case-insensitive volumes.
func verifyExactDirectoryName(root *os.Root, dirInfo fs.FileInfo) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if os.SameFile(dirInfo, info) {
			if entry.Name() != DirectoryName {
				return fmt.Errorf("credential directory alias %q is not the exact fixed name", entry.Name())
			}
			return nil
		}
	}
	return fmt.Errorf("credential directory identity is not visible at the fixed name")
}

func newID() (string, error) {
	random := make([]byte, idBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("issue managed credential ID: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func randomSuffix() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(random)
}

func parseRef(ref Ref) (string, error) {
	value := strings.TrimSpace(string(ref))
	const prefix = "managed:"
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("managed credential reference is not Hub-issued")
	}
	id := strings.TrimPrefix(value, prefix)
	if len(id) != idHexLen || !isLowerHex(id) {
		return "", fmt.Errorf("managed credential reference is not canonical")
	}
	return id, nil
}

// IsManagedRef reports whether value is a canonical managed credential
// reference. It is used by the Hub to accept managed credential references on
// Connections without resolving or exposing any secret.
func IsManagedRef(value string) bool {
	_, err := parseRef(Ref(strings.TrimSpace(value)))
	return err == nil
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}
