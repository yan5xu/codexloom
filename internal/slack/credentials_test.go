package slack

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestTokenKeyringRoundTripAndLegacyFallback(t *testing.T) {
	keyring.MockInit()
	botCredential := randomCredentialValue(t)
	appCredential := randomCredentialValue(t)
	if err := SaveTokens("A_TEST", botCredential, appCredential); err != nil {
		t.Fatal(err)
	}
	tokens, err := LoadTokens("A_TEST", "T_TEST")
	if err != nil || tokens.Bot != botCredential || tokens.App != appCredential {
		t.Fatal("managed token values did not round-trip")
	}
	if err := DeleteTokens("A_TEST"); err != nil {
		t.Fatal(err)
	}
	legacyBotCredential := randomCredentialValue(t)
	legacyAppCredential := randomCredentialValue(t)
	if err := keyring.Set(CredentialService("A_TEST")+".bot-token", "T_TEST", legacyBotCredential); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(CredentialService("A_TEST")+".app-token", "T_TEST", legacyAppCredential); err != nil {
		t.Fatal(err)
	}
	tokens, err = LoadTokens("A_TEST", "T_TEST")
	if err != nil || tokens.Bot != legacyBotCredential || tokens.App != legacyAppCredential {
		t.Fatal("legacy token values did not round-trip")
	}
}

func randomCredentialValue(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
