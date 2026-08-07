package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveExecutableReturnsCanonicalDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway")
	payload := []byte("candidate gateway\n")
	if err := os.WriteFile(path, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence, err := ObserveExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(payload)
	if evidence.SHA256 != hex.EncodeToString(want[:]) || !ValidExecutableSHA256(evidence.SHA256) {
		t.Fatalf("evidence = %#v", evidence)
	}
	for _, invalid := range []string{"", evidence.SHA256[:63], "ABC" + evidence.SHA256[3:]} {
		if ValidExecutableSHA256(invalid) {
			t.Fatalf("invalid digest %q accepted", invalid)
		}
	}
}

func TestValidBuildIdentityRejectsPlaceholdersAndControlCharacters(t *testing.T) {
	for _, value := range []string{"895950e80ae7", "v0.1.0-dev+895950e"} {
		if !ValidBuildIdentity(value) {
			t.Fatalf("valid build identity rejected: %q", value)
		}
	}
	for _, value := range []string{"", "unknown", "dev", "build with spaces", "build\nvalue"} {
		if ValidBuildIdentity(value) {
			t.Fatalf("invalid build identity accepted: %q", value)
		}
	}
}
