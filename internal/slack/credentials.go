package slack

import (
	"errors"
	"fmt"
	"strings"

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
