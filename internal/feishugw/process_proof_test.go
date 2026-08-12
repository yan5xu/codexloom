package feishugw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func TestSendHeartbeatAttachesExactProofOnlyAfterSocketOpen(t *testing.T) {
	var received atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConnectionHeartbeatParams
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode heartbeat: %v", err)
		}
		received.Store(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	proof := &hub.GatewayProcessHeartbeatParams{
		AttemptID: "gattempt_l2a", Generation: "ggen_l2a", Build: "build-l2a", ExecutableDigest: "digest-l2a",
	}
	gateway, err := New(Config{
		HubURL: server.URL, ConnectionID: "conn_l2a", AddressID: "addr_l2a", AppID: "cli_l2a",
		AppSecret: "legacy-secret", HTTPClient: server.Client(), ProcessProof: proof,
	})
	if err != nil {
		t.Fatal(err)
	}

	gateway.sendHeartbeat(context.Background())
	before := received.Load().(hub.ConnectionHeartbeatParams)
	if before.GatewayProcess != nil {
		t.Fatal("proof attached before the provider socket was open")
	}
	if before.Status != "connecting" {
		t.Fatalf("expected connecting status, got %q", before.Status)
	}

	gateway.connected.Store(true)
	gateway.sendHeartbeat(context.Background())
	connected := received.Load().(hub.ConnectionHeartbeatParams)
	if connected.GatewayProcess == nil || *connected.GatewayProcess != *proof {
		t.Fatalf("connected heartbeat did not carry the exact proof: %#v", connected.GatewayProcess)
	}
	if connected.Status != "connected" {
		t.Fatalf("expected connected status, got %q", connected.Status)
	}

	gateway.connected.Store(false)
	gateway.sendHeartbeat(context.Background())
	after := received.Load().(hub.ConnectionHeartbeatParams)
	if after.GatewayProcess != nil {
		t.Fatal("proof attached after the provider socket closed")
	}
}

func TestSendHeartbeatWithoutProofStaysLegacy(t *testing.T) {
	var received atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body hub.ConnectionHeartbeatParams
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode heartbeat: %v", err)
		}
		received.Store(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	gateway, err := New(Config{
		HubURL: server.URL, ConnectionID: "conn_legacy", AddressID: "addr_legacy", AppID: "cli_legacy",
		AppSecret: "legacy-secret", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway.connected.Store(true)
	gateway.sendHeartbeat(context.Background())
	body := received.Load().(hub.ConnectionHeartbeatParams)
	if body.GatewayProcess != nil {
		t.Fatal("legacy gateway heartbeat carried a process proof")
	}
}
