package parall

import (
	"errors"
	"fmt"
	"strings"

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
