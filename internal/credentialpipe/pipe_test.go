package credentialpipe

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRunUsesAnonymousFDAndRemovesSecretEnvironment(t *testing.T) {
	if os.Getenv("CODEXLOOM_CREDENTIAL_PIPE_HELPER") == "1" {
		runCredentialPipeHelper()
		return
	}
	secret := randomPipeValue(t)
	t.Setenv("CODEXLOOM_PIPE_FORBIDDEN", secret)
	t.Setenv("CODEXLOOM_CREDENTIAL_PIPE_HELPER", "1")
	payload := map[string]string{"credential": secret}
	if err := Run(context.Background(), os.Args[0], []string{"-test.run=TestRunUsesAnonymousFDAndRemovesSecretEnvironment", "--"}, payload, "CODEXLOOM_PIPE_FORBIDDEN"); err != nil {
		t.Fatal(err)
	}
}

func runCredentialPipeHelper() {
	if os.Getenv("CODEXLOOM_PIPE_FORBIDDEN") != "" {
		os.Exit(11)
	}
	fd := -1
	for index := range os.Args {
		if os.Args[index] == "--credential-fd" && index+1 < len(os.Args) {
			_, _ = fmt.Sscan(os.Args[index+1], &fd)
		}
	}
	if fd != childCredentialFD {
		os.Exit(12)
	}
	file := os.NewFile(uintptr(fd), "credential-pipe")
	if file == nil {
		os.Exit(13)
	}
	defer file.Close()
	var payload map[string]string
	if err := json.NewDecoder(file).Decode(&payload); err != nil || payload["credential"] == "" {
		os.Exit(14)
	}
	for _, argument := range os.Args {
		if strings.Contains(argument, payload["credential"]) {
			os.Exit(15)
		}
	}
	for _, entry := range os.Environ() {
		if strings.Contains(entry, payload["credential"]) {
			os.Exit(16)
		}
	}
	os.Exit(0)
}

func randomPipeValue(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
