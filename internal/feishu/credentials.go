package feishu

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const credentialAccount = "app-secret"

func CredentialService(appID string) string {
	return "com.codexloom.feishu." + strings.TrimSpace(appID)
}

func SaveAppSecret(appID, appSecret string) error {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return fmt.Errorf("Feishu App ID and App Secret are required")
	}
	return keyring.Set(CredentialService(appID), credentialAccount, appSecret)
}

func LoadAppSecret(appID string) (string, error) {
	secret, err := keyring.Get(CredentialService(appID), credentialAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(secret), nil
}

func DeleteAppSecret(appID string) error {
	err := keyring.Delete(CredentialService(appID), credentialAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
