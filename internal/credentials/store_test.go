package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

type credentialFixture struct {
	st *store.Store
}

func newCredentialFixture(t *testing.T) credentialFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimWritableOwnership(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return credentialFixture{st: st}
}

func TestLPutRequiresCredentialFloor(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimWritableOwnership(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	credentialStore, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Put([]byte("secret")); err == nil {
		t.Fatal("Put succeeded without a raised credential floor")
	}
}

func TestVPutResolveRoundTripAndOwnerOnlyPermissions(t *testing.T) {
	fixture := newCredentialFixture(t)
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("v1-secret-勿-泄露")
	ref, err := credentialStore.Put(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(ref), "managed:") || len(string(ref)) != len("managed:")+idHexLen {
		t.Fatalf("ref is not canonical: %q", ref)
	}
	got, err := credentialStore.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatal("resolved secret does not match")
	}
	dirInfo, err := os.Stat(filepath.Join(fixture.st.Dir(), DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm() != dirMode {
		t.Fatalf("credential directory permissions = %v", dirInfo.Mode().Perm())
	}
	id, _ := parseRef(ref)
	fileInfo, err := os.Stat(filepath.Join(fixture.st.Dir(), DirectoryName, id))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != fileMode {
		t.Fatalf("credential file permissions = %v", fileInfo.Mode().Perm())
	}
}

func TestResolveReadOnlyWorksWithoutLiveWritableOwner(t *testing.T) {
	dir := t.TempDir()
	ownerStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerStore.ClaimWritableOwnership(); err != nil {
		t.Fatal(err)
	}
	if err := ownerStore.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	ownerCredentials, err := New(ownerStore)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("readonly-secret")
	ref, err := ownerCredentials.Put(secret)
	if err != nil {
		t.Fatal(err)
	}

	readOnlyStore, err := store.OpenWithOptions(dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readOnlyStore.Close() })
	got, err := ResolveReadOnly(readOnlyStore, ref)
	if err != nil {
		t.Fatalf("read-only resolution failed: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatal("read-only resolved secret does not match")
	}
	if _, err := ResolveReadOnly(readOnlyStore, Ref("managed:"+strings.Repeat("0", idHexLen))); err == nil {
		t.Fatal("read-only resolution accepted a missing reference")
	}
	if _, err := ResolveReadOnly(readOnlyStore, Ref("keychain:com.codexloom.feishu")); err == nil {
		t.Fatal("read-only resolution accepted a non-managed reference")
	}
}

func TestVPutRequiresLiveWritableStoreOwner(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := New(st); err == nil {
		t.Fatal("credential store constructed without a live writable Hub owner")
	}
}

func TestVPutIsImmutableAndNeverOverwrites(t *testing.T) {
	fixture := newCredentialFixture(t)
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	first, err := credentialStore.Put([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := credentialStore.Put([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two Puts returned the same reference")
	}
	firstValue, err := credentialStore.Resolve(first)
	if err != nil || string(firstValue) != "first" {
		t.Fatalf("first credential changed: %q err=%v", firstValue, err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.st.Dir(), DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("credential directory contains %d entries, want 2", len(entries))
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".managed-credential-") {
			t.Fatal("stale credential temp file survived")
		}
	}
}

func TestVDeleteRemovesOnlyTarget(t *testing.T) {
	fixture := newCredentialFixture(t)
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	first, err := credentialStore.Put([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := credentialStore.Put([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if err := credentialStore.Delete(first); err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(first); err == nil {
		t.Fatal("deleted credential still resolves")
	}
	if value, err := credentialStore.Resolve(second); err != nil || string(value) != "second" {
		t.Fatalf("unrelated credential damaged: %q err=%v", value, err)
	}
	if err := credentialStore.Delete(Ref("managed:" + strings.Repeat("0", idHexLen))); err == nil {
		t.Fatal("deleting a missing credential succeeded")
	}
}

func TestVRefRejectsPathsAndMalformedIDs(t *testing.T) {
	fixture := newCredentialFixture(t)
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		"../../etc/passwd", "managed:../..", "managed:", "managed:xyz",
		"managed:" + strings.Repeat("Z", idHexLen), "managed:" + strings.Repeat("a", idHexLen-1),
		"/abs/path", "C:\\windows\\system32",
	} {
		if _, err := credentialStore.Resolve(Ref(candidate)); err == nil {
			t.Fatalf("malformed reference resolved: %q", candidate)
		}
		if err := credentialStore.Delete(Ref(candidate)); err == nil {
			t.Fatalf("malformed reference deleted: %q", candidate)
		}
	}
}

func TestVSecretNeverAppearsInErrors(t *testing.T) {
	fixture := newCredentialFixture(t)
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("sensitive-value-should-not-leak")
	ref, err := credentialStore.Put(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(Ref("managed:" + strings.Repeat("b", idHexLen))); err != nil && strings.Contains(err.Error(), string(secret)) {
		t.Fatal("secret leaked into an error")
	}
	if err := credentialStore.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Resolve(ref); err != nil && strings.Contains(err.Error(), string(secret)) {
		t.Fatal("secret leaked into a not-found error")
	}
}

func TestVPutRejectsCaseAliasedFixedDirectory(t *testing.T) {
	fixture := newCredentialFixture(t)
	aliased := filepath.Join(fixture.st.Dir(), strings.ToUpper(DirectoryName))
	if err := os.Mkdir(aliased, dirMode); err != nil {
		t.Skipf("cannot create case alias fixture: %v", err)
	}
	probe, err := os.Stat(aliased)
	if err != nil {
		t.Skipf("case alias unavailable: %v", err)
	}
	exact, err := os.Stat(filepath.Join(fixture.st.Dir(), DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(probe, exact) {
		t.Skip("test volume is case-sensitive")
	}
	credentialStore, err := New(fixture.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialStore.Put([]byte("secret")); err == nil {
		t.Fatal("Put accepted a case-aliased fixed credential directory")
	}
}
