package slack

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
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

func TestManagedTokensBindAppAndTeamWithoutCrossWorkspaceReuse(t *testing.T) {
	credentials, err := credentialstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	botOne, appOne := randomCredentialValue(t), randomCredentialValue(t)
	refOne, err := SaveManagedTokens(credentials, "A_SHARED", "T_ONE", botOne, appOne)
	if err != nil {
		t.Fatal(err)
	}
	botTwo, appTwo := randomCredentialValue(t), randomCredentialValue(t)
	refTwo, err := SaveManagedTokens(credentials, "A_SHARED", "T_TWO", botTwo, appTwo)
	if err != nil {
		t.Fatal(err)
	}
	if refOne == refTwo {
		t.Fatal("Slack managed references collided across workspaces")
	}
	resolvedApp, tokens, err := LoadTokensAndAppReference(credentials, refOne, "A_SHARED", "T_ONE")
	if err != nil || resolvedApp != "A_SHARED" || tokens.Bot != botOne || tokens.App != appOne {
		t.Fatal("workspace-bound Slack tokens did not round-trip")
	}
	if _, _, err := LoadTokensAndAppReference(credentials, refOne, "A_SHARED", "T_TWO"); err == nil {
		t.Fatal("Slack managed tokens were accepted for the wrong workspace")
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
