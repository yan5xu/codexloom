package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentials"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestGatewayProcessProofFromEnvRequiresCompleteIdentity(t *testing.T) {
	t.Setenv("CODEX_LOOM_GATEWAY_ATTEMPT_ID", "")
	t.Setenv("CODEX_LOOM_GATEWAY_GENERATION", "")
	t.Setenv("CODEX_LOOM_GATEWAY_BUILD", "")
	t.Setenv("CODEX_LOOM_GATEWAY_EXECUTABLE_DIGEST", "")
	if proof := gatewayProcessProofFromEnv(); proof != nil {
		t.Fatalf("empty proof environment produced a proof: %#v", proof)
	}
	t.Setenv("CODEX_LOOM_GATEWAY_ATTEMPT_ID", "gattempt_l2a")
	t.Setenv("CODEX_LOOM_GATEWAY_GENERATION", "ggen_l2a")
	t.Setenv("CODEX_LOOM_GATEWAY_BUILD", "build-l2a")
	t.Setenv("CODEX_LOOM_GATEWAY_EXECUTABLE_DIGEST", "digest-l2a")
	proof := gatewayProcessProofFromEnv()
	if proof == nil || proof.AttemptID != "gattempt_l2a" || proof.Generation != "ggen_l2a" ||
		proof.Build != "build-l2a" || proof.ExecutableDigest != "digest-l2a" {
		t.Fatalf("exact proof identity was not read: %#v", proof)
	}
}

func TestResolveManagedSecretReadOnly(t *testing.T) {
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
	credentialStore, err := credentials.New(ownerStore)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("gateway-managed-secret")
	ref, err := credentialStore.Put(want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveManagedSecret(dir, ref)
	if err != nil {
		t.Fatalf("read-only managed resolution failed: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("resolved secret does not match the managed credential")
	}
	if _, err := resolveManagedSecret(dir, credentials.Ref("managed:"+strings.Repeat("0", 64))); err == nil {
		t.Fatal("missing managed reference resolved without error")
	}
	if _, err := resolveManagedSecret("", ref); err == nil {
		t.Fatal("empty data directory resolved a managed reference")
	}
}

func TestManagedRefFromEnvRequiresCanonicalValue(t *testing.T) {
	os.Unsetenv("CODEX_LOOM_MANAGED_CREDENTIAL_REF")
	ref, set, err := managedRefFromEnv()
	if err != nil || set || ref != "" {
		t.Fatalf("unset managed ref env: ref=%q set=%v err=%v", ref, set, err)
	}
	t.Setenv("CODEX_LOOM_MANAGED_CREDENTIAL_REF", "   ")
	if _, _, err := managedRefFromEnv(); err == nil {
		t.Fatal("explicit blank managed ref did not fail closed")
	}
	t.Setenv("CODEX_LOOM_MANAGED_CREDENTIAL_REF", "keychain:com.codexloom.lark")
	if _, _, err := managedRefFromEnv(); err == nil {
		t.Fatal("explicit non-managed ref did not fail closed")
	}
	valid := "managed:" + strings.Repeat("a", 64)
	t.Setenv("CODEX_LOOM_MANAGED_CREDENTIAL_REF", valid)
	ref, set, err = managedRefFromEnv()
	if err != nil || !set || ref != valid {
		t.Fatalf("valid managed ref env: ref=%q set=%v err=%v", ref, set, err)
	}
}

func TestVerifySelfExecutableRejectsReplacedBinary(t *testing.T) {
	dir := t.TempDir()
	verified := filepath.Join(dir, "verified-gateway")
	replaced := filepath.Join(dir, "replaced-gateway")
	if err := os.WriteFile(verified, []byte("verified target binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replaced, []byte("replaced target binary\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := executableDigest(verified)
	if err != nil {
		t.Fatal(err)
	}
	proof := &hub.GatewayProcessHeartbeatParams{
		AttemptID: "gattempt_l2a", Generation: "ggen_l2a",
		Build: "sha256:" + digest[:16], ExecutableDigest: digest,
	}
	if err := verifyExecutableProof(verified, proof); err != nil {
		t.Fatalf("matching executable was rejected: %v", err)
	}
	if err := verifyExecutableProof(replaced, proof); err == nil {
		t.Fatal("replaced executable echoed the frozen target proof")
	}
	wrongBuild := *proof
	wrongBuild.Build = "sha256:" + strings.Repeat("0", 16)
	if err := verifyExecutableProof(verified, &wrongBuild); err == nil {
		t.Fatal("mismatched build identity was accepted")
	}
}

func TestVerifySelfExecutableMatchesRunningTestBinary(t *testing.T) {
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	proof := &hub.GatewayProcessHeartbeatParams{
		AttemptID: "gattempt_l2a", Generation: "ggen_l2a",
		Build: "sha256:" + digest[:16], ExecutableDigest: digest,
	}
	if err := verifySelfExecutable(proof); err != nil {
		t.Fatalf("running test binary was not accepted as its own proof: %v", err)
	}
}

func TestValidateGatewayStartupAllowsLegacyRecoveryUnit(t *testing.T) {
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	proof := &hub.GatewayProcessHeartbeatParams{
		AttemptID: "gattempt_recovery", Generation: "ggen_recovery",
		Build: "sha256:" + digest[:16], ExecutableDigest: digest,
	}
	// A legacy recovery unit carries proof identity but no managed reference:
	// it must consume the legacy source, not fail before the provider socket.
	if err := validateGatewayStartup(proof, false, ""); err != nil {
		t.Fatalf("proof-bearing legacy recovery unit was rejected: %v", err)
	}
	// An explicitly set (even blank) managed reference never falls back.
	if err := validateGatewayStartup(proof, true, "  "); err == nil {
		t.Fatal("explicit blank managed ref with proof was not rejected")
	}
	if err := validateGatewayStartup(proof, true, "managed:"+strings.Repeat("a", 64)); err != nil {
		t.Fatalf("valid managed ref with matching executable was rejected: %v", err)
	}
	wrongDigest := *proof
	wrongDigest.ExecutableDigest = strings.Repeat("0", 64)
	if err := validateGatewayStartup(&wrongDigest, true, "managed:"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("proof with mismatched executable was accepted")
	}
}
