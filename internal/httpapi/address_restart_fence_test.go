package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/credentialstore"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestAddressIdentityMutationsShareManagedRestartSerialDomain(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *hub.Hub, hub.AgentAddress) (string, string, any)
	}{
		{
			name: "create",
			prepare: func(_ *testing.T, _ *hub.Hub, address hub.AgentAddress) (string, string, any) {
				return http.MethodPost, "/api/agents/agent-one/addresses", map[string]any{
					"connectionId": address.ConnectionID, "externalIdentity": "bot-created", "trustDomain": "local",
				}
			},
		},
		{
			name: "update",
			prepare: func(_ *testing.T, _ *hub.Hub, address hub.AgentAddress) (string, string, any) {
				return http.MethodPatch, "/api/integrations/addresses/" + address.ID, map[string]any{"displayName": "changed"}
			},
		},
		{
			name: "archive lifecycle",
			prepare: func(_ *testing.T, _ *hub.Hub, address hub.AgentAddress) (string, string, any) {
				return http.MethodPost, "/api/integrations/addresses/" + address.ID + "/lifecycle", map[string]any{
					"action": "archive", "expectedVersion": address.Version, "confirm": address.ID,
				}
			},
		},
		{
			name: "transfer lifecycle",
			prepare: func(_ *testing.T, _ *hub.Hub, address hub.AgentAddress) (string, string, any) {
				return http.MethodPost, "/api/integrations/addresses/" + address.ID + "/lifecycle", map[string]any{
					"action": "transfer", "targetAgent": "agent-two", "expectedVersion": address.Version, "confirm": address.ID,
				}
			},
		},
		{
			name: "transfer rollback",
			prepare: func(t *testing.T, h *hub.Hub, address hub.AgentAddress) (string, string, any) {
				version := address.Version
				result, err := h.ApplyAddressLifecycle(address.ID, hub.AddressLifecycleParams{
					Action: hub.AddressLifecycleTransfer, TargetAgent: "agent-two", ExpectedVersion: &version, Confirm: address.ID,
				})
				if err != nil || result.Operation == nil {
					t.Fatalf("prepare transfer rollback: result=%#v err=%v", result, err)
				}
				return http.MethodPost, "/api/integrations/address-operations/" + result.Operation.ID + "/rollback", map[string]any{
					"expectedVersion": result.Address.Version, "confirm": result.Operation.ID,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, h, server, connection, address := newAddressRestartFenceFixture(t)
			defer h.Shutdown()
			method, path, body := test.prepare(t, h, address)

			previousRestart := restartManagedConnector
			previousCredential := preflightManagedConnectorCredential
			previousProcess := preflightManagedConnectorProcess
			defer func() {
				restartManagedConnector = previousRestart
				preflightManagedConnectorCredential = previousCredential
				preflightManagedConnectorProcess = previousProcess
			}()
			entered := make(chan struct{})
			release := make(chan struct{})
			preflightManagedConnectorCredential = func(*Server, hub.PlatformConnection) error { return nil }
			preflightManagedConnectorProcess = func(*Server, hub.PlatformConnection) error { return nil }
			restartManagedConnector = func(_ context.Context, _ *Server, candidate hub.PlatformConnection) (bool, error) {
				if candidate.ID != connection.ID {
					t.Fatalf("restart connection = %s, want %s", candidate.ID, connection.ID)
				}
				close(entered)
				<-release
				return true, nil
			}
			restartDone := make(chan struct{})
			go func() {
				defer close(restartDone)
				server.RestartManagedGateways()
			}()
			<-entered
			mutationDone := make(chan int, 1)
			go func() { mutationDone <- integrationMutationStatus(server.Handler(), method, path, body) }()
			select {
			case status := <-mutationDone:
				close(release)
				<-restartDone
				t.Fatalf("address mutation crossed active service effect with status %d", status)
			case <-time.After(75 * time.Millisecond):
			}
			close(release)
			<-restartDone
			select {
			case status := <-mutationDone:
				if status < 200 || status >= 300 {
					t.Fatalf("address mutation after fence = %d", status)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("address mutation did not resume after restart effect")
			}
			_ = st
		})
	}
}

func TestConnectionMutationLocksUseDeterministicOrder(t *testing.T) {
	_, h, server, first, _ := newAddressRestartFenceFixture(t)
	defer h.Shutdown()
	second, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: "keychain:second"})
	if err != nil {
		t.Fatal(err)
	}
	firstHeld := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondAcquired := make(chan struct{})
	go func() {
		unlock, err := server.lockCredentialIdentityMutation(second.ID, first.ID)
		if err != nil {
			t.Errorf("first lock: %v", err)
			close(firstHeld)
			return
		}
		close(firstHeld)
		<-releaseFirst
		unlock()
	}()
	<-firstHeld
	go func() {
		unlock, err := server.lockCredentialIdentityMutation(first.ID, second.ID)
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(secondAcquired)
			return
		}
		unlock()
		close(secondAcquired)
	}()
	select {
	case <-secondAcquired:
		t.Fatal("overlapping multi-Connection mutation acquired locks concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-secondAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("deterministically ordered lock waiter did not resume")
	}
}

func TestManagedRestartDropsAddressDriftBeforeServiceEffect(t *testing.T) {
	_, h, server, connection, address := newAddressRestartFenceFixture(t)
	defer h.Shutdown()
	previousBefore := beforeManagedConnectorRestartLock
	previousRestart := restartManagedConnector
	previousCredential := preflightManagedConnectorCredential
	previousProcess := preflightManagedConnectorProcess
	defer func() {
		beforeManagedConnectorRestartLock = previousBefore
		restartManagedConnector = previousRestart
		preflightManagedConnectorCredential = previousCredential
		preflightManagedConnectorProcess = previousProcess
	}()
	snapshotReady := make(chan struct{})
	continueRestart := make(chan struct{})
	beforeManagedConnectorRestartLock = func(id string) {
		if id != connection.ID {
			t.Errorf("restart connection = %s, want %s", id, connection.ID)
		}
		close(snapshotReady)
		<-continueRestart
	}
	preflightManagedConnectorCredential = func(*Server, hub.PlatformConnection) error { return nil }
	preflightManagedConnectorProcess = func(*Server, hub.PlatformConnection) error { return nil }
	effects := 0
	restartManagedConnector = func(context.Context, *Server, hub.PlatformConnection) (bool, error) {
		effects++
		return true, nil
	}
	done := make(chan struct{})
	go func() { defer close(done); server.RestartManagedGateways() }()
	<-snapshotReady
	status := integrationMutationStatus(server.Handler(), http.MethodPatch, "/api/integrations/addresses/"+address.ID, map[string]any{"displayName": "drifted"})
	if status != http.StatusOK {
		close(continueRestart)
		<-done
		t.Fatalf("address drift request = %d", status)
	}
	close(continueRestart)
	<-done
	if effects != 0 {
		t.Fatalf("stale Address snapshot reached service effect %d times", effects)
	}
}

func newAddressRestartFenceFixture(t *testing.T) (*store.Store, *hub.Hub, *Server, hub.PlatformConnection, hub.AgentAddress) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agents := map[string]*hub.Agent{
		"agent-one": {ID: "agent-one", Name: "agent-one", Status: "idle", CreatedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:00:00Z"},
		"agent-two": {ID: "agent-two", Name: "agent-two", Status: "idle", CreatedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:00:00Z"},
	}
	if err := st.SaveAgents(agents); err != nil {
		t.Fatal(err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := h.CredentialStore()
	if err != nil {
		t.Fatal(err)
	}
	reference, _, err := credentials.PutBound("address-restart/lark", credentialstore.Payload{
		Provider: "lark", Kind: "fixture", Values: map[string]string{"value": randomTestCredential(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := h.CreateConnection(hub.ConnectionParams{Provider: "lark", CredentialRef: reference})
	if err != nil {
		t.Fatal(err)
	}
	address, err := h.CreateAddress(hub.AddressParams{
		Agent: "agent-one", ConnectionID: connection.ID, ExternalIdentity: "bot-original", TrustDomain: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, h, New(h, st, nil), connection, address
}

func integrationMutationStatus(handler http.Handler, method, path string, body any) int {
	data, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}
