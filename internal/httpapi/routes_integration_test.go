package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestInboxItemMatchesAddressAndActiveFilters(t *testing.T) {
	addresses := stringSet([]string{" addr-a ", "addr-b", ""})
	tests := []struct {
		name       string
		item       hub.InboxItem
		activeOnly bool
		want       bool
	}{
		{name: "selected active address", item: hub.InboxItem{AddressID: "addr-a", State: "queued"}, activeOnly: true, want: true},
		{name: "different address", item: hub.InboxItem{AddressID: "addr-c", State: "queued"}, activeOnly: true, want: false},
		{name: "handled is terminal", item: hub.InboxItem{AddressID: "addr-a", State: "handled"}, activeOnly: true, want: false},
		{name: "cancelled is terminal", item: hub.InboxItem{AddressID: "addr-a", State: "cancelled"}, activeOnly: true, want: false},
		{name: "terminal allowed without active filter", item: hub.InboxItem{AddressID: "addr-b", State: "handled"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inboxItemMatches(tt.item, addresses, tt.activeOnly); got != tt.want {
				t.Fatalf("inboxItemMatches(%+v) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}
}

func TestAddressLifecycleAndTransferAPIReturnManagedReceipts(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agents := map[string]*hub.Agent{
		"agent-a": {ID: "agent-a", Name: "alpha", Status: "idle", CreatedAt: "2026-08-06T00:00:00Z", UpdatedAt: "2026-08-06T00:00:00Z"},
		"agent-b": {ID: "agent-b", Name: "beta", Status: "idle", CreatedAt: "2026-08-06T00:00:00Z", UpdatedAt: "2026-08-06T00:00:00Z"},
	}
	if err := st.SaveAgents(agents); err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "parall"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{
		Agent: "alpha", ConnectionID: connection.ID, ExternalIdentity: "prll://usr_alpha", TrustDomain: "workspace-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}).Handler()

	preflight := integrationAPIRequest(t, server, http.MethodPost, "/api/integrations/addresses/"+address.ID+"/lifecycle", map[string]any{
		"action": "transfer", "targetAgent": "beta", "dryRun": true,
	}, http.StatusOK)
	plan, _ := preflight["preflight"].(map[string]any)
	if plan["allowed"] != true || int(plan["currentVersion"].(float64)) != 1 || plan["toAgentId"] != "agent-b" {
		t.Fatalf("transfer API preflight = %#v", plan)
	}
	transferred := integrationAPIRequest(t, server, http.MethodPost, "/api/integrations/addresses/"+address.ID+"/lifecycle", map[string]any{
		"action": "transfer", "targetAgent": "beta", "expectedVersion": 1, "confirm": address.ID,
	}, http.StatusOK)
	operation, _ := transferred["operation"].(map[string]any)
	if operation["action"] != "transfer" || operation["addressId"] != address.ID || operation["toAgentId"] != "agent-b" {
		t.Fatalf("transfer API receipt = %#v", operation)
	}
	operationID, _ := operation["id"].(string)
	loaded := integrationAPIRequest(t, server, http.MethodGet, "/api/integrations/address-operations/"+operationID, nil, http.StatusOK)
	loadedOperation, _ := loaded["operation"].(map[string]any)
	if loadedOperation["id"] != operationID || loadedOperation["action"] != "transfer" {
		t.Fatalf("address operation API = %#v", loaded)
	}
	listed := integrationAPIRequest(t, server, http.MethodGet, "/api/integrations/address-operations?address="+address.ID, nil, http.StatusOK)
	if operations, _ := listed["operations"].([]any); len(operations) != 1 {
		t.Fatalf("address operations API = %#v", listed)
	}
	rollback := integrationAPIRequest(t, server, http.MethodPost, "/api/integrations/address-operations/"+operationID+"/rollback", map[string]any{
		"expectedVersion": 2, "confirm": operationID,
	}, http.StatusOK)
	rolledBack, _ := rollback["address"].(map[string]any)
	if rolledBack["agentId"] != "agent-a" || int(rolledBack["version"].(float64)) != 3 {
		t.Fatalf("rollback API result = %#v", rollback)
	}
}

func TestHeartbeatWireCarriesOptionalGatewayProcessEvidence(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown()
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: "keychain:test"})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(h, st, fstest.MapFS{"index.html": {Data: []byte("app")}}).Handler()
	digest := strings.Repeat("c", 64)
	body, err := json.Marshal(map[string]any{
		"status": "connected", "gatewayGeneration": "ggen_wire", "gatewayBuild": "build-wire",
		"gatewayExecutableSha256": digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/connections/"+connection.ID+"/heartbeat", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Connection hub.PlatformConnection `json:"connection"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Connection.GatewayGeneration != "ggen_wire" || result.Connection.GatewayBuild != "build-wire" || result.Connection.GatewayExecutableSHA256 != digest {
		t.Fatalf("heartbeat evidence = %#v", result.Connection)
	}
}

func integrationAPIRequest(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &encoded)
	request.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("CODEX_LOOM_ADMIN_TOKEN"); token != "" {
		request.Header.Set("X-Codex-Loom-Admin-Token", token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s = %d: %s", method, path, response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s %s JSON: %v (%s)", method, path, err, response.Body.String())
	}
	return result
}
