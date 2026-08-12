package hub

import (
	"strings"
	"testing"
)

func TestValidAgentDisplayName(t *testing.T) {
	for _, value := range []string{"短篇改编", "Short drama", "Agent-01"} {
		if !validAgentDisplayName(value) {
			t.Fatalf("validAgentDisplayName(%q) = false", value)
		}
	}
	for _, value := range []string{"", "   ", "bad\nname", strings.Repeat("名", 81)} {
		if validAgentDisplayName(value) {
			t.Fatalf("validAgentDisplayName(%q) = true", value)
		}
	}
}

func TestAgentDisplayNameFallsBackToInternalName(t *testing.T) {
	agent := &Agent{Name: "internal-name"}
	if got := agentDisplayName(agent); got != "internal-name" {
		t.Fatalf("agentDisplayName() = %q", got)
	}
	agent.DisplayName = "短篇改编"
	if got := agentDisplayName(agent); got != "短篇改编" {
		t.Fatalf("agentDisplayName() = %q", got)
	}
}
