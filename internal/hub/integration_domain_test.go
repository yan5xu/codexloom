package hub

import (
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestLarkConnectionDomainPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	connection, err := h.CreateConnection(ConnectionParams{Provider: "lark", AccountRef: "cli-test", Domain: " LARK "})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Domain != "lark" {
		t.Fatalf("created domain = %q", connection.Domain)
	}
	h.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()
	defer reopenedStore.Close()
	connections := reopened.ListConnections()
	if len(connections) != 1 || connections[0].ID != connection.ID || connections[0].Domain != "lark" {
		t.Fatalf("reopened connections = %#v", connections)
	}
}

func TestConnectionDomainRejectsUnknownOrUnrelatedProviders(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := New(st)
	defer h.Shutdown()
	if _, err := h.CreateConnection(ConnectionParams{Provider: "lark", Domain: "https://example.com"}); err == nil {
		t.Fatal("arbitrary Lark domain was accepted")
	}
	if _, err := h.CreateConnection(ConnectionParams{Provider: "slack", Domain: "lark"}); err == nil {
		t.Fatal("Lark domain was accepted for another provider")
	}
}
