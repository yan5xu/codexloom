package credentialstore

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoreRoundTripIsOwnerOnlyAndIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.IssueID()
	if err != nil {
		t.Fatal(err)
	}
	payload := randomPayload(t)
	first, err := store.Put(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, DirectoryName, id+".json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(id, payload)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != id || second.ID != id || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idempotent write changed the credential entry")
	}
	loaded, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != payload.Provider || loaded.Kind != payload.Kind || !sameValues(loaded.Values, payload.Values) {
		t.Fatal("loaded credential did not match the stored value")
	}
	rootInfo, err := os.Stat(filepath.Join(dataDir, DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 || before.Mode().Perm() != 0o600 {
		t.Fatal("credential directory or file permissions are not owner-only")
	}
}

func TestStoreRejectsSymlinkAndBroadPermissions(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.IssueID()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, DirectoryName, id+".json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(id); err == nil || !errors.Is(err, ErrUnsafe) {
		t.Fatal("symlink credential was not rejected")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(id, randomPayload(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(id); err == nil || !errors.Is(err, ErrUnsafe) {
		t.Fatal("broad credential permissions were not rejected")
	}
}

func TestStoreRejectsCorruptionWithoutLeakingValues(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.IssueID()
	if err != nil {
		t.Fatal(err)
	}
	payload := randomPayload(t)
	if _, err := store.Put(id, payload); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, DirectoryName, id+".json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(id)
	if err == nil {
		t.Fatal("corrupt credential was not rejected")
	}
	for _, value := range payload.Values {
		if strings.Contains(err.Error(), value) {
			t.Fatal("credential value appeared in an error")
		}
	}
}

func TestStoreSerializesConcurrentWriters(t *testing.T) {
	dataDir := t.TempDir()
	firstStore, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := firstStore.IssueID()
	if err != nil {
		t.Fatal(err)
	}
	payloads := []Payload{randomPayload(t), randomPayload(t)}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, len(payloads))
	for index, store := range []*Store{firstStore, secondStore} {
		wait.Add(1)
		go func(store *Store, payload Payload) {
			defer wait.Done()
			_, err := store.Put(id, payload)
			errorsSeen <- err
		}(store, payloads[index])
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := firstStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !sameValues(loaded.Values, payloads[0].Values) && !sameValues(loaded.Values, payloads[1].Values) {
		t.Fatal("concurrent write produced a partial credential")
	}
}

func TestPutBoundIsStableAndReferenceRequiresExistingEntry(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstRef, firstMetadata, err := store.PutBound("provider-binding", randomPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	secondRef, secondMetadata, err := store.PutBound("provider-binding", randomPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	if firstRef != secondRef || firstMetadata.ID != secondMetadata.ID || !strings.HasPrefix(firstRef, ManagedReferencePrefix) {
		t.Fatal("stable provider binding produced different opaque references")
	}
	missingID, err := store.IssueID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reference(missingID); err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatal("reference was issued for a missing credential")
	}
	if _, err := ParseReference("managed:../../outside"); err == nil {
		t.Fatal("path-like managed reference was accepted")
	}
}

func TestStoreRejectsRepositoryDataDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "runtime")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dataDir); err == nil || !errors.Is(err, ErrUnsafe) {
		t.Fatal("repository data directory was not rejected")
	}
}

func randomPayload(t *testing.T) Payload {
	t.Helper()
	return Payload{
		Provider: "test-provider",
		Kind:     "test-kind",
		Values: map[string]string{
			"primary":   randomValue(t),
			"secondary": randomValue(t),
		},
	}
}

func randomValue(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test value generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func sameValues(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}
