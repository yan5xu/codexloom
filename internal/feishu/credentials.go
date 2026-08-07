package feishu

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/zalando/go-keyring"
)

const credentialAccount = "app-secret"

func CredentialService(appID string) string {
	return "com.codexloom.feishu." + strings.TrimSpace(appID)
}

func ManagedCredentialBinding(appID string) string {
	return "lark/app-secret/" + strings.TrimSpace(appID)
}

func SaveManagedAppSecret(store *credentialstore.Store, appID, appSecret string) (string, error) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if store == nil || appID == "" || appSecret == "" {
		return "", fmt.Errorf("Feishu App ID and App Secret are required")
	}
	reference, _, err := store.PutBound(ManagedCredentialBinding(appID), credentialstore.Payload{
		Provider: "lark", Kind: "app-secret",
		Values: map[string]string{"appID": appID, "appSecret": appSecret},
	})
	return reference, err
}

func ManagedAppSecretReference(store *credentialstore.Store, appID string) (string, error) {
	if store == nil || strings.TrimSpace(appID) == "" {
		return "", fmt.Errorf("Feishu App ID is required")
	}
	return store.BoundReference(ManagedCredentialBinding(appID))
}

func LoadAppSecretReference(store *credentialstore.Store, reference, appID string) (string, error) {
	reference, appID = strings.TrimSpace(reference), strings.TrimSpace(appID)
	switch {
	case strings.HasPrefix(reference, credentialstore.ManagedReferencePrefix):
		if store == nil {
			return "", fmt.Errorf("managed Feishu credential store is unavailable")
		}
		if err := store.ValidateBinding(reference, ManagedCredentialBinding(appID)); err != nil {
			return "", err
		}
		payload, err := store.Resolve(reference)
		if err != nil {
			return "", err
		}
		secret := strings.TrimSpace(payload.Values["appSecret"])
		if payload.Provider != "lark" || payload.Kind != "app-secret" || strings.TrimSpace(payload.Values["appID"]) != appID || secret == "" {
			return "", fmt.Errorf("managed Feishu credential has the wrong provider, kind, or fields")
		}
		return secret, nil
	case strings.HasPrefix(reference, "keychain:"):
		if reference != "keychain:"+CredentialService(appID) {
			return "", fmt.Errorf("Feishu Keychain reference does not match the App ID")
		}
		return LoadAppSecret(appID)
	case strings.HasPrefix(reference, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(reference, "env:"))
		if name == "" {
			return "", fmt.Errorf("empty Feishu environment credential reference")
		}
		secret := strings.TrimSpace(os.Getenv(name))
		if secret == "" {
			return "", fmt.Errorf("Feishu environment credential is empty")
		}
		return secret, nil
	default:
		return "", fmt.Errorf("unsupported Feishu credential reference")
	}
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
