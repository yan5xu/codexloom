// Package credentialstore persists CodexLoom-managed Connector credentials in
// an owner-only directory under the Loom data directory.
//
// The on-disk format and file names are deliberately private implementation
// details. Callers address entries only through Hub-issued opaque identifiers;
// paths and secret values must never be returned through public APIs or logs.
package credentialstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
)

const (
	// DirectoryName is excluded from ordinary backups and support bundles.
	// Its contents are secret-bearing local configuration.
	DirectoryName = "credentials"
	// ManagedReferencePrefix is the only public classification callers may
	// rely on. The opaque suffix must never be interpreted as a path.
	ManagedReferencePrefix = "managed:"

	formatVersion  = 1
	maxRecordBytes = 1 << 20
	maxFieldBytes  = 64 << 10
	maxFieldCount  = 32
)

var (
	ErrNotFound  = errors.New("managed credential not found")
	ErrUnsafe    = errors.New("managed credential storage is unsafe")
	processWrite sync.Mutex
)

// Payload is the secret-bearing value supplied to or loaded from the store.
// It must remain inside the credential/provider boundary.
type Payload struct {
	Provider string
	Kind     string
	Values   map[string]string
}

// Metadata is safe to use in internal receipts. It intentionally contains
// field names but never field values, checksums, or file-system paths.
type Metadata struct {
	Version  int
	ID       string
	Provider string
	Kind     string
	Fields   []string
}

// Error reports an actionable safety state without including a path, secret,
// raw file content, or wrapped OS error text.
type Error struct {
	Operation string
	ID        string
	Status    string
	cause     error
}

func (e *Error) Error() string {
	subject := "managed credential store"
	if e.ID != "" {
		subject = "managed credential " + e.ID
	}
	return subject + ": " + e.Status
}

func (e *Error) Unwrap() error { return e.cause }

// Store is rooted at dataDir/credentials. Store construction validates the
// data directory and creates or validates the private credential directory.
type Store struct {
	root string
}

type record struct {
	Version  int               `json:"version"`
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Kind     string            `json:"kind"`
	Values   map[string]string `json:"values"`
	Checksum string            `json:"checksum"`
}

type checksumMaterial struct {
	Version  int               `json:"version"`
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Kind     string            `json:"kind"`
	Values   map[string]string `json:"values"`
}

// Open creates or validates the managed credential directory. dataDir itself
// must already exist, be owned by the current user, and not be a symlink or a
// path inside a source repository.
func Open(dataDir string) (*Store, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, storeError("open", "", "data directory is required", ErrUnsafe)
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, storeError("open", "", "data directory path is invalid", err)
	}
	if err := validateDataDirectory(abs); err != nil {
		return nil, storeError("open", "", "data directory ownership or path is unsafe", errors.Join(ErrUnsafe, err))
	}
	inside, err := insideSourceRepository(abs)
	if err != nil {
		return nil, storeError("open", "", "data directory trust could not be verified", errors.Join(ErrUnsafe, err))
	}
	if inside {
		return nil, storeError("open", "", "credentials cannot be stored inside a source repository", ErrUnsafe)
	}

	root := filepath.Join(abs, DirectoryName)
	if err := createOrValidatePrivateDirectory(root); err != nil {
		return nil, storeError("open", "", "credential directory is not owner-only", errors.Join(ErrUnsafe, err))
	}
	return &Store{root: root}, nil
}

// IssueID returns a cryptographically random, non-semantic credential ID. The
// ID is suitable for the opaque portion of a managed reference but does not by
// itself authorize access to any entry.
func (s *Store) IssueID() (string, error) {
	if s == nil {
		return "", storeError("issue", "", "credential store is unavailable", ErrUnsafe)
	}
	buffer := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", storeError("issue", "", "credential identifier could not be issued", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer)
	return "crd_" + strings.ToLower(encoded), nil
}

// PutBound derives a stable opaque ID from a non-secret provider binding using
// a store-local random issuer key, then writes the credential. The binding is
// never persisted and cannot be recovered from the resulting ID.
func (s *Store) PutBound(binding string, payload Payload) (string, Metadata, error) {
	id, err := s.issueBoundID(binding)
	if err != nil {
		return "", Metadata{}, err
	}
	metadata, err := s.Put(id, payload)
	if err != nil {
		return "", Metadata{}, err
	}
	reference, err := s.Reference(id)
	if err != nil {
		return "", Metadata{}, err
	}
	return reference, metadata, nil
}

// BoundReference returns the stable reference for an existing provider binding
// without exposing or reading its values.
func (s *Store) BoundReference(binding string) (string, error) {
	id, err := s.issueBoundID(binding)
	if err != nil {
		return "", err
	}
	return s.Reference(id)
}

// ValidateBinding proves that a reference was issued for the expected
// non-secret provider identity and still resolves to a safe entry.
func (s *Store) ValidateBinding(reference, binding string) error {
	actual, err := ParseReference(reference)
	if err != nil {
		return err
	}
	expected, err := s.issueBoundID(binding)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return storeError("validate", actual, "credential binding does not match the provider identity", ErrUnsafe)
	}
	_, err = s.Inspect(actual)
	return err
}

// Reference returns a canonical managed reference only after proving that the
// current store contains a safe, valid entry for the ID.
func (s *Store) Reference(id string) (string, error) {
	metadata, err := s.Inspect(id)
	if err != nil {
		return "", err
	}
	return ManagedReferencePrefix + metadata.ID, nil
}

// Resolve loads a canonical managed reference without accepting paths or any
// alternative spelling.
func (s *Store) Resolve(reference string) (Payload, error) {
	id, err := ParseReference(reference)
	if err != nil {
		return Payload{}, err
	}
	return s.Get(id)
}

// ParseReference validates the canonical reference grammar. Callers that bind
// a reference to a business object must additionally call Store.Inspect or
// Store.Resolve to prove the entry exists and remains owner-only.
func ParseReference(reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if !strings.HasPrefix(reference, ManagedReferencePrefix) {
		return "", storeError("validate", "", "managed credential reference is invalid", ErrUnsafe)
	}
	id, err := validateID(strings.TrimPrefix(reference, ManagedReferencePrefix))
	if err != nil || reference != ManagedReferencePrefix+id {
		return "", storeError("validate", "", "managed credential reference is invalid", errors.Join(ErrUnsafe, err))
	}
	return id, nil
}

// Put atomically creates or replaces one credential and verifies the committed
// bytes by reopening the final owner-only file. Identical repeated writes are
// idempotent. A process-wide mutex plus an OS file lock enforces one writer
// across Store instances and cooperating Hub/gateway processes.
func (s *Store) Put(id string, payload Payload) (Metadata, error) {
	if s == nil {
		return Metadata{}, storeError("write", "", "credential store is unavailable", ErrUnsafe)
	}
	id, normalized, err := normalize(id, payload)
	if err != nil {
		return Metadata{}, err
	}
	encoded, target, err := encodeRecord(id, normalized)
	if err != nil {
		return Metadata{}, storeError("write", id, "credential record could not be encoded", err)
	}

	processWrite.Lock()
	defer processWrite.Unlock()
	if err := validatePrivateDirectory(s.root); err != nil {
		return Metadata{}, storeError("write", id, "credential directory is not owner-only", errors.Join(ErrUnsafe, err))
	}
	lock, err := acquireStoreLock(filepath.Join(s.root, ".lock"))
	if err != nil {
		return Metadata{}, storeError("write", id, "credential writer lock is unavailable or unsafe", errors.Join(ErrUnsafe, err))
	}
	defer lock.Close()
	if err := validatePrivateDirectory(s.root); err != nil {
		return Metadata{}, storeError("write", id, "credential directory changed while acquiring the writer lock", errors.Join(ErrUnsafe, err))
	}

	path := s.path(id)
	old, oldExists, err := readOptionalPrivateFile(path, maxRecordBytes)
	if err != nil {
		return Metadata{}, storeError("write", id, "existing credential file is unsafe or unreadable", errors.Join(ErrUnsafe, err))
	}
	if oldExists {
		current, decodeErr := decodeRecord(id, old)
		if decodeErr != nil {
			return Metadata{}, storeError("write", id, "existing credential record is corrupt", decodeErr)
		}
		if reflect.DeepEqual(current, target) {
			return metadataFor(target), nil
		}
	}

	if err := replacePrivateFile(s.root, path, encoded); err != nil {
		return Metadata{}, storeError("write", id, "credential file could not be atomically replaced", err)
	}
	committed, err := readPrivateFile(path, maxRecordBytes)
	if err == nil {
		var decoded record
		decoded, err = decodeRecord(id, committed)
		if err == nil && !reflect.DeepEqual(decoded, target) {
			err = errors.New("committed record mismatch")
		}
	}
	if err != nil {
		restoreErr := s.restoreLocked(path, old, oldExists)
		if restoreErr != nil {
			return Metadata{}, storeError("write", id, "credential verification failed and previous state could not be restored", errors.Join(ErrUnsafe, err, restoreErr))
		}
		return Metadata{}, storeError("write", id, "credential verification failed; previous state was restored", err)
	}
	return metadataFor(target), nil
}

// Get loads and validates one managed credential. It never returns partial or
// unchecked data.
func (s *Store) Get(id string) (Payload, error) {
	if s == nil {
		return Payload{}, storeError("read", "", "credential store is unavailable", ErrUnsafe)
	}
	id, err := validateID(id)
	if err != nil {
		return Payload{}, err
	}
	if err := validatePrivateDirectory(s.root); err != nil {
		return Payload{}, storeError("read", id, "credential directory is not owner-only", errors.Join(ErrUnsafe, err))
	}
	data, err := readPrivateFile(s.path(id), maxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Payload{}, storeError("read", id, "credential does not exist", ErrNotFound)
	}
	if err != nil {
		return Payload{}, storeError("read", id, "credential file is unsafe or unreadable", errors.Join(ErrUnsafe, err))
	}
	decoded, err := decodeRecord(id, data)
	if err != nil {
		return Payload{}, storeError("read", id, "credential record is corrupt", err)
	}
	return Payload{Provider: decoded.Provider, Kind: decoded.Kind, Values: cloneValues(decoded.Values)}, nil
}

// Inspect proves that an ID resolves to a currently safe, valid entry without
// exposing its values.
func (s *Store) Inspect(id string) (Metadata, error) {
	payload, err := s.Get(id)
	if err != nil {
		return Metadata{}, err
	}
	id, _ = validateID(id)
	return metadataFor(record{Version: formatVersion, ID: id, Provider: payload.Provider, Kind: payload.Kind, Values: payload.Values}), nil
}

func (s *Store) restoreLocked(path string, old []byte, existed bool) error {
	if existed {
		if err := replacePrivateFile(s.root, path, old); err != nil {
			return err
		}
		restored, err := readPrivateFile(path, maxRecordBytes)
		if err != nil || !bytes.Equal(restored, old) {
			return errors.Join(err, errors.New("restored record mismatch"))
		}
		return nil
	}
	return removePrivateFile(path, s.root)
}

func (s *Store) issueBoundID(binding string) (string, error) {
	if s == nil {
		return "", storeError("issue", "", "credential store is unavailable", ErrUnsafe)
	}
	binding = strings.TrimSpace(binding)
	if binding == "" || len(binding) > 1024 {
		return "", storeError("issue", "", "credential binding is invalid", ErrUnsafe)
	}

	processWrite.Lock()
	defer processWrite.Unlock()
	if err := validatePrivateDirectory(s.root); err != nil {
		return "", storeError("issue", "", "credential directory is not owner-only", errors.Join(ErrUnsafe, err))
	}
	lock, err := acquireStoreLock(filepath.Join(s.root, ".lock"))
	if err != nil {
		return "", storeError("issue", "", "credential issuer lock is unavailable or unsafe", errors.Join(ErrUnsafe, err))
	}
	defer lock.Close()
	issuerPath := filepath.Join(s.root, ".issuer")
	issuer, exists, err := readOptionalPrivateFile(issuerPath, 64)
	if err != nil {
		return "", storeError("issue", "", "credential issuer is unsafe or unreadable", errors.Join(ErrUnsafe, err))
	}
	if !exists {
		issuer = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, issuer); err != nil {
			return "", storeError("issue", "", "credential issuer could not be created", err)
		}
		if err := replacePrivateFile(s.root, issuerPath, issuer); err != nil {
			return "", storeError("issue", "", "credential issuer could not be persisted", err)
		}
		verified, err := readPrivateFile(issuerPath, 64)
		if err != nil || !bytes.Equal(verified, issuer) {
			return "", storeError("issue", "", "credential issuer verification failed", errors.Join(ErrUnsafe, err))
		}
	}
	if len(issuer) != 32 {
		return "", storeError("issue", "", "credential issuer is corrupt", ErrUnsafe)
	}
	mac := hmac.New(sha256.New, issuer)
	_, _ = mac.Write([]byte(binding))
	digest := mac.Sum(nil)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:20])
	return "crd_" + strings.ToLower(encoded), nil
}

func (s *Store) path(id string) string { return filepath.Join(s.root, id+".json") }

func encodeRecord(id string, payload Payload) ([]byte, record, error) {
	result := record{
		Version: formatVersion, ID: id, Provider: payload.Provider,
		Kind: payload.Kind, Values: cloneValues(payload.Values),
	}
	checksum, err := recordChecksum(result)
	if err != nil {
		return nil, record{}, err
	}
	result.Checksum = checksum
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, record{}, err
	}
	data = append(data, '\n')
	if len(data) > maxRecordBytes {
		return nil, record{}, errors.New("credential record exceeds size limit")
	}
	return data, result, nil
}

func decodeRecord(expectedID string, data []byte) (record, error) {
	if len(data) == 0 || len(data) > maxRecordBytes {
		return record{}, errors.New("credential record size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result record
	if err := decoder.Decode(&result); err != nil {
		return record{}, errors.New("credential record JSON is invalid")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return record{}, errors.New("credential record has trailing data")
	}
	if result.Version != formatVersion || result.ID != expectedID {
		return record{}, errors.New("credential record identity or version is invalid")
	}
	_, payload, err := normalize(result.ID, Payload{Provider: result.Provider, Kind: result.Kind, Values: result.Values})
	if err != nil {
		return record{}, errors.New("credential record fields are invalid")
	}
	result.Provider, result.Kind, result.Values = payload.Provider, payload.Kind, payload.Values
	want, err := recordChecksum(result)
	if err != nil {
		return record{}, errors.New("credential checksum could not be computed")
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(result.Checksum)) != 1 {
		return record{}, errors.New("credential checksum is invalid")
	}
	return result, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

func recordChecksum(value record) (string, error) {
	material := checksumMaterial{
		Version: value.Version, ID: value.ID, Provider: value.Provider,
		Kind: value.Kind, Values: value.Values,
	}
	data, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func normalize(id string, payload Payload) (string, Payload, error) {
	id, err := validateID(id)
	if err != nil {
		return "", Payload{}, err
	}
	payload.Provider = strings.ToLower(strings.TrimSpace(payload.Provider))
	payload.Kind = strings.ToLower(strings.TrimSpace(payload.Kind))
	if !validLabel(payload.Provider, 64) || !validLabel(payload.Kind, 64) {
		return "", Payload{}, storeError("validate", id, "credential provider or kind is invalid", ErrUnsafe)
	}
	if len(payload.Values) == 0 || len(payload.Values) > maxFieldCount {
		return "", Payload{}, storeError("validate", id, "credential field count is invalid", ErrUnsafe)
	}
	normalized := make(map[string]string, len(payload.Values))
	for rawKey, value := range payload.Values {
		key := strings.TrimSpace(rawKey)
		if !validField(key) || value == "" || len(value) > maxFieldBytes {
			return "", Payload{}, storeError("validate", id, "credential fields are invalid", ErrUnsafe)
		}
		normalized[key] = value
	}
	payload.Values = normalized
	return id, payload, nil
}

func validateID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 80 || !strings.HasPrefix(value, "crd_") {
		return "", storeError("validate", "", "credential identifier is invalid", ErrUnsafe)
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}
		return "", storeError("validate", "", "credential identifier is invalid", ErrUnsafe)
	}
	return value, nil
}

func validLabel(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validField(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func metadataFor(value record) Metadata {
	fields := make([]string, 0, len(value.Values))
	for field := range value.Values {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return Metadata{
		Version: value.Version, ID: value.ID, Provider: value.Provider,
		Kind: value.Kind, Fields: fields,
	}
}

func cloneValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func readOptionalPrivateFile(path string, limit int64) ([]byte, bool, error) {
	data, err := readPrivateFile(path, limit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func insideSourceRepository(path string) (bool, error) {
	current := filepath.Clean(path)
	for {
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func storeError(operation, id, status string, cause error) error {
	return &Error{Operation: operation, ID: id, Status: status, cause: cause}
}
