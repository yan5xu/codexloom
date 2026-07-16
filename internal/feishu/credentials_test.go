package feishu

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestAppSecretKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()
	if err := SaveAppSecret("cli_test", "secret-value"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAppSecret("cli_test")
	if err != nil || got != "secret-value" {
		t.Fatalf("LoadAppSecret() = %q, %v", got, err)
	}
	if err := DeleteAppSecret("cli_test"); err != nil {
		t.Fatal(err)
	}
	got, err = LoadAppSecret("cli_test")
	if err != nil || got != "" {
		t.Fatalf("LoadAppSecret() after delete = %q, %v", got, err)
	}
}
