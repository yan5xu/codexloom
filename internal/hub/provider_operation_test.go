package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestProviderOperationUsesAddressConnectionAndPersistsNativeResult(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	h.connections["conn_prll"] = &PlatformConnection{ID: "conn_prll", Provider: "parall", Enabled: true}
	h.addresses["addr_prll"] = &AgentAddress{
		ID: "addr_prll", AgentID: "agent_1", ConnectionID: "conn_prll", Enabled: true,
	}

	operation, err := h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: "addr_prll", Resource: "messages", Action: "replies",
		Arguments: map[string]any{"messageId": "msg_root", "limit": "12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "pending" || operation.AgentID != "agent_1" || operation.ConnectionID != "conn_prll" {
		t.Fatalf("created operation = %#v", operation)
	}

	command, err := h.ClaimNextConnectorCommand("conn_prll")
	if err != nil {
		t.Fatal(err)
	}
	if command == nil || command.Type != "provider_operation" || command.ProviderOperation == nil {
		t.Fatalf("connector command = %#v", command)
	}
	if command.ProviderOperation.ID != operation.ID || command.ProviderOperation.AttemptCount != 1 {
		t.Fatalf("claimed operation = %#v", command.ProviderOperation)
	}

	result := json.RawMessage(`{"data":[{"id":"msg_1","content":{"text":"hello"}}],"has_more":false}`)
	completed, err := h.CompleteProviderOperation("conn_prll", operation.ID, ProviderOperationResultParams{
		AttemptToken: command.ProviderOperation.AttemptToken, Success: true, Result: result,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "succeeded" || string(completed.Result) != string(result) || completed.CompletedAt == "" {
		t.Fatalf("completed operation = %#v", completed)
	}

	reloaded := New(st)
	defer reloaded.Shutdown()
	persisted, err := reloaded.GetProviderOperation(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != "succeeded" || string(persisted.Result) != string(result) {
		t.Fatalf("persisted operation = %#v", persisted)
	}
}

func TestProviderOperationRejectsWrongProviderAndUnsupportedWrite(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	h.connections["conn_lark"] = &PlatformConnection{ID: "conn_lark", Provider: "lark", Enabled: true}
	h.addresses["addr_lark"] = &AgentAddress{ID: "addr_lark", ConnectionID: "conn_lark", Enabled: true}

	_, err = h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: "addr_lark", Resource: "messages", Action: "get",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to lark") {
		t.Fatalf("wrong provider error = %v", err)
	}
	_, err = h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: "addr_lark", Resource: "messages", Action: "send",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported parall provider operation") {
		t.Fatalf("write operation error = %v", err)
	}
}

func TestLarkProviderOperationRequiresEnabledConversationMembership(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	h.connections["conn_lark"] = &PlatformConnection{ID: "conn_lark", Provider: "lark", Enabled: true}
	h.addresses["addr_lark"] = &AgentAddress{
		ID: "addr_lark", AgentID: "agent_1", ConnectionID: "conn_lark", Enabled: true,
	}

	params := ProviderOperationParams{
		Provider: "lark", AddressID: "addr_lark", Resource: "messages", Action: "get",
		Arguments: map[string]any{"chatId": "oc_team", "messageId": "om_root"},
	}
	if _, err := h.CreateProviderOperation(params); err == nil || !strings.Contains(err.Error(), "no enabled Membership") {
		t.Fatalf("missing Membership error = %v", err)
	}
	h.memberships["mem_lark"] = &ConversationMembership{
		ID: "mem_lark", AddressID: "addr_lark", ConversationID: "oc_team", Enabled: false,
	}
	if _, err := h.CreateProviderOperation(params); err == nil || !strings.Contains(err.Error(), "no enabled Membership") {
		t.Fatalf("disabled Membership error = %v", err)
	}
	h.memberships["mem_lark"].Enabled = true
	operation, err := h.CreateProviderOperation(params)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Provider != "lark" || operation.AgentID != "agent_1" || operation.Arguments["chatId"] != "oc_team" {
		t.Fatalf("Lark operation = %#v", operation)
	}
	command, err := h.ClaimNextConnectorCommand("conn_lark")
	if err != nil || command == nil || command.ProviderOperation == nil || command.ProviderOperation.ID != operation.ID {
		t.Fatalf("Lark connector command = %#v, err=%v", command, err)
	}
}

func TestLarkProviderOperationRejectsWritesAndMissingNativeIDs(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	h.connections["conn_lark"] = &PlatformConnection{ID: "conn_lark", Provider: "lark", Enabled: true}
	h.addresses["addr_lark"] = &AgentAddress{ID: "addr_lark", ConnectionID: "conn_lark", Enabled: true}
	h.memberships["mem_lark"] = &ConversationMembership{ID: "mem_lark", AddressID: "addr_lark", ConversationID: "oc_team", Enabled: true}

	for name, params := range map[string]ProviderOperationParams{
		"write":           {Provider: "lark", AddressID: "addr_lark", Resource: "messages", Action: "send", Arguments: map[string]any{"chatId": "oc_team"}},
		"missing chat":    {Provider: "lark", AddressID: "addr_lark", Resource: "messages", Action: "list"},
		"missing message": {Provider: "lark", AddressID: "addr_lark", Resource: "messages", Action: "replies", Arguments: map[string]any{"chatId": "oc_team"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.CreateProviderOperation(params); err == nil {
				t.Fatal("invalid Lark provider operation was accepted")
			}
		})
	}
}

func TestUnexpiredProviderOperationClaimSurvivesRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	h.connections["conn_prll"] = &PlatformConnection{ID: "conn_prll", Provider: "parall", Enabled: true}
	h.addresses["addr_prll"] = &AgentAddress{ID: "addr_prll", ConnectionID: "conn_prll", Enabled: true}
	operation, err := h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: "addr_prll", Resource: "chats", Action: "list",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := h.ClaimNextProviderOperation("conn_prll")
	if err != nil {
		t.Fatal(err)
	}
	h.Shutdown()

	reloaded := New(st)
	defer reloaded.Shutdown()
	reloaded.connections["conn_prll"] = &PlatformConnection{ID: "conn_prll", Provider: "parall", Enabled: true}
	reloaded.addresses["addr_prll"] = &AgentAddress{ID: "addr_prll", ConnectionID: "conn_prll", Enabled: true}
	recovered, err := reloaded.GetProviderOperation(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != "running" || recovered.AttemptToken != claimed.ProviderOperation.AttemptToken {
		t.Fatalf("recovered operation = %#v", recovered)
	}
	if command, err := reloaded.ClaimNextProviderOperation("conn_prll"); err != nil || command != nil {
		t.Fatalf("unexpired operation was replayed: command=%#v err=%v", command, err)
	}
}

func TestExpiredProviderOperationClaimIsReclaimedAndRejectsStaleResult(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	h.connections["conn_prll"] = &PlatformConnection{ID: "conn_prll", Provider: "parall", Enabled: true}
	h.addresses["addr_prll"] = &AgentAddress{ID: "addr_prll", ConnectionID: "conn_prll", Enabled: true}
	operation, err := h.CreateProviderOperation(ProviderOperationParams{
		Provider: "parall", AddressID: "addr_prll", Resource: "messages", Action: "get",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.ClaimNextProviderOperation("conn_prll")
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	expired := *h.providerOperations[operation.ID]
	expired.ClaimExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	if err := h.commitProviderOperationLocked(expired); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.mu.Unlock()
	second, err := h.ClaimNextProviderOperation("conn_prll")
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.ProviderOperation.AttemptToken == first.ProviderOperation.AttemptToken {
		t.Fatalf("reclaimed operation = %#v", second)
	}
	_, err = h.CompleteProviderOperation("conn_prll", operation.ID, ProviderOperationResultParams{
		AttemptToken: first.ProviderOperation.AttemptToken, Success: true, Result: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale result error = %v", err)
	}
	completed, err := h.CompleteProviderOperation("conn_prll", operation.ID, ProviderOperationResultParams{
		AttemptToken: second.ProviderOperation.AttemptToken, Success: true, Result: json.RawMessage(`{}`),
	})
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("current result: operation=%#v err=%v", completed, err)
	}
}

func TestProviderOperationRecoveryDoesNotReplayTerminalHistory(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := "2026-07-15T01:00:00Z"
	completed := "2026-07-15T01:00:01Z"
	for _, operation := range []ProviderOperation{
		{ID: "pop-1", Provider: "parall", State: "running", CreatedAt: started, UpdatedAt: started},
		{ID: "pop-1", Provider: "parall", State: "succeeded", Result: json.RawMessage(`{"ok":true}`), CreatedAt: started, UpdatedAt: completed, CompletedAt: completed},
		{ID: "pop-1", Provider: "parall", State: "pending", LastError: "stale historical recovery", CreatedAt: started, UpdatedAt: completed},
	} {
		if err := st.AppendProviderOperation(operation); err != nil {
			t.Fatal(err)
		}
	}
	for restart := 1; restart <= 2; restart++ {
		h, err := OpenWithOptions(st, OpenOptions{Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		got := h.providerOperations["pop-1"]
		if got == nil || got.State != "succeeded" || string(got.Result) != `{"ok":true}` {
			t.Fatalf("restart %d operation = %#v", restart, got)
		}
		h.Shutdown()
	}
}
