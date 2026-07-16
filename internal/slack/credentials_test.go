package slack

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestTokenKeyringRoundTripAndLegacyFallback(t *testing.T) {
	keyring.MockInit()
	if err := SaveTokens("A_TEST", "xoxb-test", "xapp-test"); err != nil {
		t.Fatal(err)
	}
	tokens, err := LoadTokens("A_TEST", "T_TEST")
	if err != nil || tokens.Bot != "xoxb-test" || tokens.App != "xapp-test" {
		t.Fatalf("LoadTokens() = %#v, %v", tokens, err)
	}
	if err := DeleteTokens("A_TEST"); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(CredentialService("A_TEST")+".bot-token", "T_TEST", "legacy-bot"); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(CredentialService("A_TEST")+".app-token", "T_TEST", "legacy-app"); err != nil {
		t.Fatal(err)
	}
	tokens, err = LoadTokens("A_TEST", "T_TEST")
	if err != nil || tokens.Bot != "legacy-bot" || tokens.App != "legacy-app" {
		t.Fatalf("legacy LoadTokens() = %#v, %v", tokens, err)
	}
}
