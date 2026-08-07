package github

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/zalando/go-keyring"
)

const credentialAccount = "access-token"

func CredentialService(login string) string {
	return "com.codexloom.github." + strings.ToLower(strings.TrimSpace(login))
}

func ScopedCredentialService(login, resourceOwner string) string {
	scope := strings.ToLower(strings.TrimSpace(resourceOwner))
	if scope == "*" {
		scope = "all"
	}
	var normalized strings.Builder
	for _, char := range scope {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
			normalized.WriteRune(char)
		} else {
			normalized.WriteByte('_')
		}
	}
	if normalized.Len() == 0 {
		return CredentialService(login)
	}
	return CredentialService(login) + "." + normalized.String()
}

func ManagedCredentialBinding(login, resourceOwner string) string {
	return "github/access-token/" + strings.ToLower(strings.TrimSpace(login)) + "/" + strings.ToLower(strings.TrimSpace(resourceOwner))
}

func SaveManagedScopedToken(store *credentialstore.Store, login, resourceOwner, token string) (string, error) {
	login = strings.TrimSpace(login)
	resourceOwner = strings.ToLower(strings.TrimSpace(resourceOwner))
	token = strings.TrimSpace(token)
	if store == nil || login == "" || resourceOwner == "" || token == "" {
		return "", fmt.Errorf("GitHub login, resource owner, and token are required")
	}
	reference, _, err := store.PutBound(ManagedCredentialBinding(login, resourceOwner), credentialstore.Payload{
		Provider: "github", Kind: "access-token",
		Values: map[string]string{"login": login, "resourceOwner": resourceOwner, "token": token},
	})
	return reference, err
}

func ManagedCredentialReference(store *credentialstore.Store, login, resourceOwner string) (string, error) {
	if store == nil || strings.TrimSpace(login) == "" || strings.TrimSpace(resourceOwner) == "" {
		return "", fmt.Errorf("GitHub login and resource owner are required")
	}
	return store.BoundReference(ManagedCredentialBinding(login, resourceOwner))
}

func LoadCredentialFor(store *credentialstore.Store, ref, login, resourceOwner string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, credentialstore.ManagedReferencePrefix) {
		return LoadCredential(ref)
	}
	if store == nil {
		return "", fmt.Errorf("managed GitHub credential store is unavailable")
	}
	if err := store.ValidateBinding(ref, ManagedCredentialBinding(login, resourceOwner)); err != nil {
		return "", err
	}
	return LoadManagedCredential(store, ref)
}

func LoadManagedCredential(store *credentialstore.Store, ref string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("managed GitHub credential store is unavailable")
	}
	payload, err := store.Resolve(ref)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(payload.Values["token"])
	if payload.Provider != "github" || payload.Kind != "access-token" || strings.TrimSpace(payload.Values["login"]) == "" || strings.TrimSpace(payload.Values["resourceOwner"]) == "" || token == "" {
		return "", fmt.Errorf("managed GitHub credential has the wrong provider, kind, or fields")
	}
	return token, nil
}

func SaveToken(login, token string) (string, error) {
	return saveToken(CredentialService(login), login, token)
}

func SaveScopedToken(login, resourceOwner, token string) (string, error) {
	return saveToken(ScopedCredentialService(login, resourceOwner), login, token)
}

func saveToken(service, login, token string) (string, error) {
	login = strings.TrimSpace(login)
	token = strings.TrimSpace(token)
	if login == "" || token == "" {
		return "", fmt.Errorf("GitHub login and token are required")
	}
	if err := keyring.Set(service, credentialAccount, token); err != nil {
		return "", err
	}
	return "keychain:" + service, nil
}

func LoadCredential(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "keychain:"):
		service := strings.TrimSpace(strings.TrimPrefix(ref, "keychain:"))
		if service == "" {
			return "", fmt.Errorf("empty GitHub Keychain reference")
		}
		token, err := keyring.Get(service, credentialAccount)
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("GitHub credential is missing from Keychain")
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(token), nil
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if name == "" {
			return "", fmt.Errorf("empty GitHub environment credential reference")
		}
		token := strings.TrimSpace(os.Getenv(name))
		if token == "" {
			return "", fmt.Errorf("GitHub credential environment variable %s is empty", name)
		}
		return token, nil
	default:
		return "", fmt.Errorf("unsupported GitHub credential reference")
	}
}

func DeleteToken(login string) error {
	return deleteToken(CredentialService(login))
}

func DeleteScopedToken(login, resourceOwner string) error {
	return deleteToken(ScopedCredentialService(login, resourceOwner))
}

func deleteToken(service string) error {
	err := keyring.Delete(service, credentialAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
