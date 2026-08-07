package slack

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/zalando/go-keyring"
)

const (
	botTokenAccount = "bot-token"
	appTokenAccount = "app-token"
)

type Tokens struct {
	Bot string
	App string
}

func CredentialService(appID string) string {
	return "com.codexloom.slack." + strings.TrimSpace(appID)
}

func ManagedCredentialBinding(appID string) string {
	return "slack/tokens/" + strings.TrimSpace(appID)
}

func SaveManagedTokens(store *credentialstore.Store, appID, botToken, appToken string) (string, error) {
	appID = strings.TrimSpace(appID)
	botToken = strings.TrimSpace(botToken)
	appToken = strings.TrimSpace(appToken)
	if store == nil || appID == "" || botToken == "" || appToken == "" {
		return "", fmt.Errorf("Slack App ID, Bot token, and App token are required")
	}
	reference, _, err := store.PutBound(ManagedCredentialBinding(appID), credentialstore.Payload{
		Provider: "slack", Kind: "tokens",
		Values: map[string]string{"appID": appID, "botToken": botToken, "appToken": appToken},
	})
	return reference, err
}

func ManagedTokensReference(store *credentialstore.Store, appID string) (string, error) {
	if store == nil || strings.TrimSpace(appID) == "" {
		return "", fmt.Errorf("Slack App ID is required")
	}
	return store.BoundReference(ManagedCredentialBinding(appID))
}

func LoadTokensReference(store *credentialstore.Store, reference, appID, legacyAccount string) (Tokens, error) {
	_, tokens, err := LoadTokensAndAppReference(store, reference, appID, legacyAccount)
	return tokens, err
}

func LoadTokensAndAppReference(store *credentialstore.Store, reference, appID, legacyAccount string) (string, Tokens, error) {
	reference, appID = strings.TrimSpace(reference), strings.TrimSpace(appID)
	switch {
	case strings.HasPrefix(reference, credentialstore.ManagedReferencePrefix):
		if store == nil {
			return "", Tokens{}, fmt.Errorf("managed Slack credential store is unavailable")
		}
		payload, err := store.Resolve(reference)
		if err != nil {
			return "", Tokens{}, err
		}
		storedAppID := strings.TrimSpace(payload.Values["appID"])
		if appID == "" {
			appID = storedAppID
		}
		if storedAppID != appID {
			return "", Tokens{}, fmt.Errorf("managed Slack credential does not match the App ID")
		}
		if err := store.ValidateBinding(reference, ManagedCredentialBinding(appID)); err != nil {
			return "", Tokens{}, err
		}
		tokens := Tokens{Bot: strings.TrimSpace(payload.Values["botToken"]), App: strings.TrimSpace(payload.Values["appToken"])}
		if payload.Provider != "slack" || payload.Kind != "tokens" || tokens.Bot == "" || tokens.App == "" {
			return "", Tokens{}, fmt.Errorf("managed Slack credential has the wrong provider, kind, or fields")
		}
		return appID, tokens, nil
	case strings.HasPrefix(reference, "keychain:"):
		if reference != "keychain:"+CredentialService(appID) {
			return "", Tokens{}, fmt.Errorf("Slack Keychain reference does not match the App ID")
		}
		tokens, err := LoadTokens(appID, legacyAccount)
		return appID, tokens, err
	default:
		return "", Tokens{}, fmt.Errorf("unsupported Slack credential reference")
	}
}

func SaveTokens(appID, botToken, appToken string) error {
	appID = strings.TrimSpace(appID)
	botToken = strings.TrimSpace(botToken)
	appToken = strings.TrimSpace(appToken)
	if appID == "" || botToken == "" || appToken == "" {
		return fmt.Errorf("Slack App ID, Bot token, and App token are required")
	}
	service := CredentialService(appID)
	if err := keyring.Set(service, botTokenAccount, botToken); err != nil {
		return err
	}
	if err := keyring.Set(service, appTokenAccount, appToken); err != nil {
		_ = keyring.Delete(service, botTokenAccount)
		return err
	}
	return nil
}

func LoadTokens(appID, legacyAccount string) (Tokens, error) {
	service := CredentialService(appID)
	botToken, botErr := keyring.Get(service, botTokenAccount)
	appToken, appErr := keyring.Get(service, appTokenAccount)
	if errors.Is(botErr, keyring.ErrNotFound) && strings.TrimSpace(legacyAccount) != "" {
		botToken, botErr = keyring.Get(service+".bot-token", strings.TrimSpace(legacyAccount))
	}
	if errors.Is(appErr, keyring.ErrNotFound) && strings.TrimSpace(legacyAccount) != "" {
		appToken, appErr = keyring.Get(service+".app-token", strings.TrimSpace(legacyAccount))
	}
	if errors.Is(botErr, keyring.ErrNotFound) {
		botErr = nil
		botToken = ""
	}
	if errors.Is(appErr, keyring.ErrNotFound) {
		appErr = nil
		appToken = ""
	}
	if botErr != nil {
		return Tokens{}, botErr
	}
	if appErr != nil {
		return Tokens{}, appErr
	}
	return Tokens{Bot: strings.TrimSpace(botToken), App: strings.TrimSpace(appToken)}, nil
}

func DeleteTokens(appID string) error {
	service := CredentialService(appID)
	for _, account := range []string{botTokenAccount, appTokenAccount} {
		if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
	}
	return nil
}
