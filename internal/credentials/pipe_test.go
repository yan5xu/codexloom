//go:build darwin || linux

package credentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

const childEnv = "CODEXLOOM_CRED_CHILD"

func TestLSpawnWithCredentialFDOnlyInheritsAnonymousDescriptor(t *testing.T) {
	if os.Getenv(childEnv) == "1" {
		file := os.NewFile(3, "credential")
		data, err := io.ReadAll(file)
		if err != nil {
			os.Exit(2)
		}
		digest := sha256.Sum256(data)
		_, _ = os.Stdout.Write([]byte(hex.EncodeToString(digest[:]) + "\n"))
		_, _ = os.Stdout.Write([]byte("argv=" + strings.Join(os.Args[1:], ",") + "\n"))
		os.Exit(0)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimWritableOwnership(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveCredentialFloor(); err != nil {
		t.Fatal(err)
	}
	credentialStore, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("lark-consumption-secret-不-进-argv")
	ref, err := credentialStore.Put(secret)
	if err != nil {
		t.Fatal(err)
	}
	secretFile, err := credentialStore.OpenSecretForChild(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer secretFile.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), childEnv+"=1")
	cmd, err := SpawnWithCredentialFD(context.Background(), executable, []string{"-test.run=TestLSpawnWithCredentialFDOnlyInheritsAnonymousDescriptor"}, env, secretFile)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed: %v output=%s", err, output.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("child output malformed: %q", output.String())
	}
	wantDigest := sha256.Sum256(secret)
	if lines[0] != hex.EncodeToString(wantDigest[:]) {
		t.Fatal("child did not receive the secret on the anonymous descriptor")
	}
	if strings.Contains(lines[1], string(secret)) || strings.Contains(strings.Join(env, ","), string(secret)) {
		t.Fatal("secret leaked into argv or the normal environment")
	}
	_ = exec.Command
}
