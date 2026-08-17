//go:build darwin || linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const fdChildEnv = "CODEXLOOM_FD3_CHILD"

func TestReadInheritedCredentialFD(t *testing.T) {
	if os.Getenv(fdChildEnv) == "1" {
		data, ok := readInheritedCredentialFD()
		if !ok {
			os.Exit(3)
		}
		_, _ = os.Stdout.Write(data)
		os.Exit(0)
	}
	secret := []byte("fd3-secret-value\n")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=TestReadInheritedCredentialFD")
	cmd.Env = append(os.Environ(), fdChildEnv+"=1")
	cmd.ExtraFiles = []*os.File{reader}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(secret); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child failed: %v output=%s", err, output.String())
	}
	if strings.TrimSpace(output.String()) != strings.TrimSpace(string(secret)) {
		t.Fatalf("child did not receive the credential on fd 3: %q", output.String())
	}
}
