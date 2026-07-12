package main

import "testing"

func TestFormatProfilePreservesReadableMultilineFields(t *testing.T) {
	profile := map[string]any{
		"version":  3.0,
		"identity": "Hub maintainer",
		"domain":   "Agent lifecycle\n\nIncludes:\n- Web UI\n- CLI",
		"scope":    "",
	}

	want := "profile v3\n" +
		"identity:\n  Hub maintainer\n" +
		"domain:\n  Agent lifecycle\n  \n  Includes:\n  - Web UI\n  - CLI\n" +
		"scope:\n\n"
	if got := formatProfile(profile); got != want {
		t.Fatalf("formatProfile() = %q, want %q", got, want)
	}
}

func TestCanonicalWatchEventType(t *testing.T) {
	tests := map[string]string{
		"loom/turn-started":       "loom/turn-started",
		"hub/turn-started":        "loom/turn-started",
		"hub/session-created":     "loom/agent-created",
		"hub/session-killed":      "loom/agent-archived",
		"item/agentMessage/delta": "item/agentMessage/delta",
	}
	for input, want := range tests {
		if got := canonicalWatchEventType(input); got != want {
			t.Errorf("canonicalWatchEventType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatConversationMembershipPreservesMultilineContext(t *testing.T) {
	useColor = false
	membership := map[string]any{
		"id": "mem_1", "addressId": "addr_1", "conversationId": "chat_1", "displayName": "Product group",
		"purpose": "Discuss product usage.\nKeep answers focused.", "role": "Support", "guidance": "- Mention only\n- No secrets",
		"triggerPolicy": "mention", "replyPolicy": "final_answer", "trustDomain": "external", "enabled": true, "version": 2.0,
	}
	want := "mem_1 chat_1  enabled  v2  addr_1\n" +
		"  name: Product group\n" +
		"  purpose:\n    Discuss product usage.\n    Keep answers focused.\n" +
		"  role:\n    Support\n" +
		"  guidance:\n    - Mention only\n    - No secrets\n" +
		"  policy: trigger=mention reply=final_answer trust=external\n"
	if got := formatConversationMembership(membership); got != want {
		t.Fatalf("formatConversationMembership() = %q, want %q", got, want)
	}
}

func TestLegacyAgentPath(t *testing.T) {
	tests := map[string]string{
		"/api/agents":                                   "/api/sessions",
		"/api/agents/alpha":                             "/api/sessions/alpha",
		"/api/agents/alpha/turns":                       "/api/sessions/alpha/messages",
		"/api/agents/alpha/turns/current/interrupt":     "/api/sessions/alpha/interrupt",
		"/api/agents/alpha/thread/history?count=10":     "/api/sessions/alpha/history?count=10",
		"/api/agents/alpha/thread/events?tail=50":       "/api/sessions/alpha/events?tail=50",
		"/api/agents/alpha/thread/approvals/approval-1": "/api/sessions/alpha/approvals/approval-1",
	}
	for input, want := range tests {
		if got := legacyAgentPath(input); got != want {
			t.Errorf("legacyAgentPath(%q) = %q, want %q", input, got, want)
		}
	}
}
