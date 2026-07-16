package parall

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCredentialLifecycle(t *testing.T) {
	keyring.MockInit()
	if err := SaveOwnerCredentials("org_1", "https://api.example.test/", "owner-key"); err != nil {
		t.Fatal(err)
	}
	owner, err := LoadOwnerCredentials("org_1")
	if err != nil || owner.APIURL != "https://api.example.test" || owner.APIKey != "owner-key" {
		t.Fatalf("owner credentials = %#v, %v", owner, err)
	}
	if err := SaveAgentCredentials("org_1", "usr_agent", "https://api.example.test", "agent-key"); err != nil {
		t.Fatal(err)
	}
	agent, err := LoadAgentCredentials("org_1", "usr_agent")
	if err != nil || agent.APIKey != "agent-key" {
		t.Fatalf("agent credentials = %#v, %v", agent, err)
	}
	if OwnerCredentialService("org_1") == AgentCredentialService("org_1", "usr_agent") {
		t.Fatal("owner and Agent credentials share a keychain service")
	}
}
