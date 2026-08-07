package parall

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/zalando/go-keyring"
)

const (
	apiURLAccount = "api-url"
	apiKeyAccount = "api-key"
)

type Credentials struct {
	APIURL string
	APIKey string
}

func OwnerCredentialService(orgID string) string {
	return "com.codexloom.parall.owner." + strings.TrimSpace(orgID)
}

func AgentCredentialService(orgID, agentID string) string {
	return "com.codexloom.parall.agent." + strings.TrimSpace(orgID) + "." + strings.TrimSpace(agentID)
}

func ManagedOwnerCredentialBinding(orgID string) string {
	return "parall/owner/" + strings.TrimSpace(orgID)
}

func ManagedAgentCredentialBinding(orgID, agentID string) string {
	return "parall/agent/" + strings.TrimSpace(orgID) + "/" + strings.TrimSpace(agentID)
}

func SaveManagedOwnerCredentials(store *credentialstore.Store, orgID, apiURL, apiKey string) (string, error) {
	return saveManagedCredentials(store, ManagedOwnerCredentialBinding(orgID), "owner", orgID, "", apiURL, apiKey)
}

func SaveManagedAgentCredentials(store *credentialstore.Store, orgID, agentID, apiURL, apiKey string) (string, error) {
	if strings.TrimSpace(agentID) == "" {
		return "", fmt.Errorf("Parall Agent ID is required")
	}
	return saveManagedCredentials(store, ManagedAgentCredentialBinding(orgID, agentID), "agent", orgID, agentID, apiURL, apiKey)
}

func ManagedOwnerCredentialsReference(store *credentialstore.Store, orgID string) (string, error) {
	if store == nil || strings.TrimSpace(orgID) == "" {
		return "", fmt.Errorf("Parall organization ID is required")
	}
	return store.BoundReference(ManagedOwnerCredentialBinding(orgID))
}

func ManagedAgentCredentialsReference(store *credentialstore.Store, orgID, agentID string) (string, error) {
	if store == nil || strings.TrimSpace(orgID) == "" || strings.TrimSpace(agentID) == "" {
		return "", fmt.Errorf("Parall organization ID and Agent ID are required")
	}
	return store.BoundReference(ManagedAgentCredentialBinding(orgID, agentID))
}

func LoadOwnerCredentialsReference(store *credentialstore.Store, reference, orgID string) (Credentials, error) {
	return loadCredentialsReference(store, reference, "owner", orgID, "")
}

func LoadAgentCredentialsReference(store *credentialstore.Store, reference, orgID, agentID string) (Credentials, error) {
	return loadCredentialsReference(store, reference, "agent", orgID, agentID)
}

func saveManagedCredentials(store *credentialstore.Store, binding, kind, orgID, agentID, apiURL, apiKey string) (string, error) {
	orgID = strings.TrimSpace(orgID)
	apiURL = normalizeAPIURL(apiURL)
	apiKey = strings.TrimSpace(apiKey)
	if store == nil || orgID == "" || apiURL == "" || apiKey == "" {
		return "", fmt.Errorf("Parall API URL, organization ID, and API key are required")
	}
	reference, _, err := store.PutBound(binding, credentialstore.Payload{
		Provider: "parall", Kind: kind,
		Values: managedValues(orgID, agentID, apiURL, apiKey),
	})
	return reference, err
}

func loadCredentialsReference(store *credentialstore.Store, reference, kind, orgID, agentID string) (Credentials, error) {
	reference, orgID, agentID = strings.TrimSpace(reference), strings.TrimSpace(orgID), strings.TrimSpace(agentID)
	binding := ManagedOwnerCredentialBinding(orgID)
	keychainService := OwnerCredentialService(orgID)
	if kind == "agent" {
		binding = ManagedAgentCredentialBinding(orgID, agentID)
		keychainService = AgentCredentialService(orgID, agentID)
	}
	switch {
	case strings.HasPrefix(reference, credentialstore.ManagedReferencePrefix):
		if store == nil {
			return Credentials{}, fmt.Errorf("managed Parall credential store is unavailable")
		}
		if err := store.ValidateBinding(reference, binding); err != nil {
			return Credentials{}, err
		}
		payload, err := store.Resolve(reference)
		if err != nil {
			return Credentials{}, err
		}
		credentials := Credentials{APIURL: normalizeAPIURL(payload.Values["apiURL"]), APIKey: strings.TrimSpace(payload.Values["apiKey"])}
		identityMatches := strings.TrimSpace(payload.Values["orgID"]) == orgID
		if kind == "agent" {
			identityMatches = identityMatches && strings.TrimSpace(payload.Values["agentID"]) == agentID
		}
		if payload.Provider != "parall" || payload.Kind != kind || !identityMatches || credentials.APIURL == "" || credentials.APIKey == "" {
			return Credentials{}, fmt.Errorf("managed Parall credential has the wrong provider, kind, or fields")
		}
		return credentials, nil
	case strings.HasPrefix(reference, "keychain:"):
		if reference != "keychain:"+keychainService {
			return Credentials{}, fmt.Errorf("Parall Keychain reference does not match the external identity")
		}
		return loadCredentials(keychainService)
	case kind == "agent" && strings.HasPrefix(reference, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(reference, "env:"))
		if name == "" {
			return Credentials{}, fmt.Errorf("empty Parall environment credential reference")
		}
		apiKey := strings.TrimSpace(os.Getenv(name))
		apiURL := normalizeAPIURL(os.Getenv("PRLL_API_URL"))
		if apiURL == "" {
			apiURL = DefaultAPIURL
		}
		if apiKey == "" {
			return Credentials{}, fmt.Errorf("Parall environment credential is empty")
		}
		return Credentials{APIURL: apiURL, APIKey: apiKey}, nil
	default:
		return Credentials{}, fmt.Errorf("unsupported Parall credential reference")
	}
}

func managedValues(orgID, agentID, apiURL, apiKey string) map[string]string {
	values := map[string]string{"orgID": orgID, "apiURL": apiURL, "apiKey": apiKey}
	if strings.TrimSpace(agentID) != "" {
		values["agentID"] = strings.TrimSpace(agentID)
	}
	return values
}

func SaveOwnerCredentials(orgID, apiURL, apiKey string) error {
	return saveCredentials(OwnerCredentialService(orgID), orgID, apiURL, apiKey)
}

func LoadOwnerCredentials(orgID string) (Credentials, error) {
	return loadCredentials(OwnerCredentialService(orgID))
}

func SaveAgentCredentials(orgID, agentID, apiURL, apiKey string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("Parall Agent ID is required")
	}
	return saveCredentials(AgentCredentialService(orgID, agentID), orgID, apiURL, apiKey)
}

func LoadAgentCredentials(orgID, agentID string) (Credentials, error) {
	return loadCredentials(AgentCredentialService(orgID, agentID))
}

func DeleteOwnerCredentials(orgID string) error {
	return deleteCredentials(OwnerCredentialService(orgID))
}

func DeleteAgentCredentials(orgID, agentID string) error {
	return deleteCredentials(AgentCredentialService(orgID, agentID))
}

func saveCredentials(service, orgID, apiURL, apiKey string) error {
	orgID = strings.TrimSpace(orgID)
	apiURL = normalizeAPIURL(apiURL)
	apiKey = strings.TrimSpace(apiKey)
	if orgID == "" || apiURL == "" || apiKey == "" {
		return fmt.Errorf("Parall API URL, organization ID, and API key are required")
	}
	if err := keyring.Set(service, apiURLAccount, apiURL); err != nil {
		return err
	}
	if err := keyring.Set(service, apiKeyAccount, apiKey); err != nil {
		_ = keyring.Delete(service, apiURLAccount)
		return err
	}
	return nil
}

func loadCredentials(service string) (Credentials, error) {
	apiURL, urlErr := keyring.Get(service, apiURLAccount)
	apiKey, keyErr := keyring.Get(service, apiKeyAccount)
	if errors.Is(urlErr, keyring.ErrNotFound) {
		urlErr, apiURL = nil, ""
	}
	if errors.Is(keyErr, keyring.ErrNotFound) {
		keyErr, apiKey = nil, ""
	}
	if urlErr != nil {
		return Credentials{}, urlErr
	}
	if keyErr != nil {
		return Credentials{}, keyErr
	}
	return Credentials{APIURL: normalizeAPIURL(apiURL), APIKey: strings.TrimSpace(apiKey)}, nil
}

func deleteCredentials(service string) error {
	for _, account := range []string{apiURLAccount, apiKeyAccount} {
		if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
	}
	return nil
}

func normalizeAPIURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
