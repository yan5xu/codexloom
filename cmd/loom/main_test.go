package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestFormatGoalShowsObjectiveAndNativeAccounting(t *testing.T) {
	useColor = false
	goal := map[string]any{
		"objective": "Audit the integration\nShip the fixes", "status": "active",
		"tokensUsed": 4300.0, "tokenBudget": 120000.0, "timeUsedSeconds": 92.0,
	}
	want := "Goal active\nobjective:\n  Audit the integration\n  Ship the fixes\nusage: 4.3K / 120.0K tokens · 1m32s\n"
	if got := formatGoal(goal); got != want {
		t.Fatalf("formatGoal() = %q, want %q", got, want)
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

func TestMsgResolveDispatchesWithRequiredFromFlag(t *testing.T) {
	a := args{
		positional: []string{"resolve", "msg_123"},
		flags:      map[string]string{"from": "alpha", "resolution": "superseded", "reason": "done"},
	}
	if got := msgSubcommand(a); got != "resolve" {
		t.Fatalf("msgSubcommand() = %q, want resolve", got)
	}
	if got := msgSubcommand(args{positional: []string{"status"}, flags: map[string]string{"from": "alpha"}}); got != "" {
		t.Fatalf("send to reserved agent name was parsed as subcommand: %q", got)
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
		"  policy: trigger=mention reply=final_answer outbound=reply_only trust=external\n"
	if got := formatConversationMembership(membership); got != want {
		t.Fatalf("formatConversationMembership() = %q, want %q", got, want)
	}
}

func TestFormatConversationMembershipShowsDirectMessageIdentity(t *testing.T) {
	useColor = false
	membership := map[string]any{
		"id": "mem_dm", "addressId": "addr_1", "conversationId": "chat_dm", "displayName": "Alice",
		"conversationType": "dm", "actorId": "user_alice", "triggerPolicy": "direct",
		"replyPolicy": "final_answer", "trustDomain": "external", "enabled": false, "version": 1.0,
	}
	want := "mem_dm chat_dm  disabled  v1  addr_1\n" +
		"  name: Alice\n" +
		"  conversation: type=dm actor=user_alice\n" +
		"  policy: trigger=direct reply=final_answer outbound=reply_only trust=external\n"
	if got := formatConversationMembership(membership); got != want {
		t.Fatalf("formatConversationMembership() = %q, want %q", got, want)
	}
}

func TestFormatConversationCandidateShowsDiscoveryWithoutMembershipLanguage(t *testing.T) {
	got := formatConversationCandidate(map[string]any{
		"id": "conv_123", "addressId": "addr_123", "conversationId": "chat_123", "conversationType": "group",
		"displayName": "MS合作", "description": "Partner work", "available": true, "lastSeenAt": "2026-07-14T10:00:00Z",
	})
	for _, fragment := range []string{"conv_123", "chat_123", "joined", "MS合作", "Partner work", "type=group"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("candidate output missing %q: %s", fragment, got)
		}
	}
}

func TestTypedIntegrationFlags(t *testing.T) {
	body := map[string]any{}
	flags := map[string]string{"enabled": "false", "expected-version": "3"}
	if err := addBoolFlag(body, flags, "enabled", "enabled"); err != nil {
		t.Fatal(err)
	}
	if err := addIntFlag(body, flags, "expected-version", "expectedVersion"); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false || body["expectedVersion"] != 3 {
		t.Fatalf("typed body = %#v", body)
	}
	if err := addBoolFlag(map[string]any{}, map[string]string{"enabled": "maybe"}, "enabled", "enabled"); err == nil {
		t.Fatal("invalid boolean was accepted")
	}
	if err := addIntFlag(map[string]any{}, map[string]string{"expected-version": "-1"}, "expected-version", "expectedVersion"); err == nil {
		t.Fatal("negative version was accepted")
	}
}

func TestParallImportRequestReadsOwnerOnlyKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.key")
	if err := os.WriteFile(path, []byte("secret-agent-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := parallImportRequest(args{flags: map[string]string{
		"agent": "ai-community", "org-id": "org_test", "external-agent-id": "usr_external", "agent-key-file": path,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if body["agentApiKey"] != "secret-agent-key" || body["agent"] != "ai-community" || body["externalAgentId"] != "usr_external" {
		t.Fatalf("import body = %#v", body)
	}
}

func TestParallImportRequestCanReuseStoredCredential(t *testing.T) {
	body, err := parallImportRequest(args{flags: map[string]string{
		"agent": "parall-dev-lead", "org-id": "org_test", "external-agent-id": "usr_external",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if body["agentApiKey"] != "" || body["agent"] != "parall-dev-lead" || body["externalAgentId"] != "usr_external" {
		t.Fatalf("credential reuse body = %#v", body)
	}
}

func TestParallImportRequestRejectsUnsafeKeyFiles(t *testing.T) {
	dir := t.TempDir()
	openPath := filepath.Join(dir, "open.key")
	if err := os.WriteFile(openPath, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{"agent": "ai-community", "org-id": "org_test", "external-agent-id": "usr_external", "agent-key-file": openPath}
	if _, err := parallImportRequest(args{flags: flags}); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("unsafe mode error = %v", err)
	}
	target := filepath.Join(dir, "target.key")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "agent.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	flags["agent-key-file"] = link
	if _, err := parallImportRequest(args{flags: flags}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestRequireSecureSecretTransport(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:4870", "http://[::1]:4870", "http://localhost:4870", "https://loom.example.test"} {
		if err := requireSecureSecretTransport(value); err != nil {
			t.Errorf("requireSecureSecretTransport(%q) = %v", value, err)
		}
	}
	if err := requireSecureSecretTransport("http://100.66.47.40:4870"); err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("insecure remote transport error = %v", err)
	}
}

func TestParsePrllMessagesListPreservesNativeArguments(t *testing.T) {
	request, err := parsePrllOperation(args{
		positional: []string{"messages", "list", "prll://cht_daily"},
		flags: map[string]string{
			"address": "addr_prll", "limit": "40", "before": "prll://msg_before",
			"thread-root-id": "prll://msg_root", "top-level": "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Resource != "messages" || request.Action != "list" {
		t.Fatalf("request = %#v", request)
	}
	if request.Arguments["chatId"] != "prll://cht_daily" || request.Arguments["limit"] != "40" ||
		request.Arguments["before"] != "prll://msg_before" || request.Arguments["threadRootId"] != "prll://msg_root" ||
		request.Arguments["topLevel"] != true {
		t.Fatalf("arguments = %#v", request.Arguments)
	}
}

func TestParsePrllNestedChatMembersList(t *testing.T) {
	request, err := parsePrllOperation(args{
		positional: []string{"chats", "members", "list", "cht_team"},
		flags:      map[string]string{"address": "addr_prll"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Resource != "chats" || request.Action != "members-list" || request.Arguments["chatId"] != "cht_team" {
		t.Fatalf("request = %#v", request)
	}
}

func TestParsePrllRejectsWritesUnknownFlagsAndUnboundedPages(t *testing.T) {
	for name, input := range map[string]args{
		"write": {
			positional: []string{"messages", "send", "cht_team"},
			flags:      map[string]string{"address": "addr_prll"},
		},
		"unknown flag": {
			positional: []string{"messages", "get", "msg_1"},
			flags:      map[string]string{"address": "addr_prll", "raw-url": "https://example.test"},
		},
		"large page": {
			positional: []string{"chats", "list"},
			flags:      map[string]string{"address": "addr_prll", "limit": "101"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePrllOperation(input); err == nil {
				t.Fatal("invalid Parall command was accepted")
			}
		})
	}
}

func TestParseLarkMessagesListPreservesNativeArguments(t *testing.T) {
	request, err := parseLarkOperation(args{
		positional: []string{"messages", "list", "oc_team"},
		flags: map[string]string{
			"address": "addr_lark", "limit": "40", "page-token": "next-page",
			"start-time": "1700000000", "end-time": "1700003600", "sort": "desc",
			"thread-id": "omt_topic", "thread-root-only": "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Resource != "messages" || request.Action != "list" {
		t.Fatalf("request = %#v", request)
	}
	if request.Arguments["chatId"] != "oc_team" || request.Arguments["limit"] != 40 ||
		request.Arguments["pageToken"] != "next-page" || request.Arguments["startTime"] != "1700000000" ||
		request.Arguments["endTime"] != "1700003600" || request.Arguments["sort"] != "ByCreateTimeDesc" ||
		request.Arguments["threadId"] != "omt_topic" || request.Arguments["threadRootOnly"] != true {
		t.Fatalf("arguments = %#v", request.Arguments)
	}
}

func TestParseLarkGetRequiresExplicitChatID(t *testing.T) {
	request, err := parseLarkOperation(args{
		positional: []string{"messages", "get", "om_message"},
		flags:      map[string]string{"address": "addr_lark", "chat-id": "oc_team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Arguments["messageId"] != "om_message" || request.Arguments["chatId"] != "oc_team" {
		t.Fatalf("arguments = %#v", request.Arguments)
	}
	if _, err := parseLarkOperation(args{
		positional: []string{"messages", "get", "om_message"},
		flags:      map[string]string{"address": "addr_lark"},
	}); err == nil || !strings.Contains(err.Error(), "--chat-id is required") {
		t.Fatalf("missing chat error = %v", err)
	}
}

func TestParseLarkRejectsWritesUnknownFlagsAndUnboundedPages(t *testing.T) {
	for name, input := range map[string]args{
		"write": {
			positional: []string{"messages", "send", "oc_team"},
			flags:      map[string]string{"address": "addr_lark"},
		},
		"unknown flag": {
			positional: []string{"messages", "get", "om_1"},
			flags:      map[string]string{"address": "addr_lark", "chat-id": "oc_team", "raw-url": "https://example.test"},
		},
		"large page": {
			positional: []string{"messages", "list", "oc_team"},
			flags:      map[string]string{"address": "addr_lark", "limit": "51"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLarkOperation(input); err == nil {
				t.Fatal("invalid Lark command was accepted")
			}
		})
	}
}

func TestEnabledState(t *testing.T) {
	if got := enabledState(map[string]any{"enabled": false}); got != "disabled" {
		t.Fatalf("enabledState(false) = %q", got)
	}
	if got := enabledState(map[string]any{"enabled": true}); got != "enabled" {
		t.Fatalf("enabledState(true) = %q", got)
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

func TestStageReplyAttachmentCopiesIntoLoomSpool(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CODEX_LOOM_DATA", dataDir)
	source := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(source, []byte("image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment, err := stageReplyAttachment(source)
	if err != nil {
		t.Fatal(err)
	}
	staged, _ := attachment["path"].(string)
	if !strings.HasPrefix(staged, filepath.Join(dataDir, "attachments", "outbound")+string(os.PathSeparator)) {
		t.Fatalf("staged path = %q", staged)
	}
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image bytes" || attachment["mimeType"] != "image/png" {
		t.Fatalf("staged attachment = %#v, data = %q", attachment, data)
	}
}

func TestStageOutboundAttachmentAcceptsFilesAndHashesSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CODEX_LOOM_DATA", dataDir)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment, err := stageOutboundAttachment(source)
	if err != nil {
		t.Fatal(err)
	}
	if attachment["mimeType"] != "text/plain; charset=utf-8" || !strings.HasPrefix(attachment["id"].(string), "art_") || len(attachment["sha256"].(string)) != 64 {
		t.Fatalf("staged attachment = %#v", attachment)
	}
}

func TestParseArgsPreservesRepeatedFiles(t *testing.T) {
	a := parseArgs([]string{"send", "--from", "alpha", "--file", "one.png", "--file", "two.pdf"})
	if got := a.flagValues["file"]; len(got) != 2 || got[0] != "one.png" || got[1] != "two.pdf" {
		t.Fatalf("file flags = %#v", got)
	}
}
