package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRestartManagedGatewaysDoesNotRestartLegacyConnectionsWithoutTypedPlans(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer func() { h.Shutdown(); _ = st.Close() }()
	for _, provider := range []string{"lark", "slack", "parall", "custom"} {
		if _, err := h.CreateConnection(hub.ConnectionParams{Provider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	New(h, st, nil).RestartManagedGateways()
	if _, err := os.Stat(filepath.Join(st.Dir(), "runtime-foundation.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy startup created an R1 record/floor: %v", err)
	}
	for _, connection := range h.ListConnections() {
		if connection.Status != "disconnected" || connection.LastHeartbeatAt != "" {
			t.Fatalf("legacy Connection was treated as a typed process: %#v", connection)
		}
	}
}
