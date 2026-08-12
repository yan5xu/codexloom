package hub

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestS0WritableHubOwnsStoreUntilShutdown(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(st); err == nil {
		t.Fatal("same Store admitted a second writable Hub")
	}
	if err := st.Close(); err == nil {
		t.Fatal("Store released its lease while Hub was alive")
	}
	if _, err := OpenWithOptions(st, OpenOptions{Passive: true}); err == nil {
		t.Fatal("passive Hub borrowed a writable Store")
	}
	h.Shutdown()
	if _, err := h.CreateSchedule(ScheduleParams{Name: "retired", To: "nobody", Subject: "x", Body: "x", Cron: "0 0 * * *", Timezone: "UTC"}); err == nil {
		t.Fatal("retired Hub retained writable ownership")
	}
	reopened, err := Open(st)
	if err != nil {
		t.Fatalf("clean shutdown did not release Hub ownership: %v", err)
	}
	reopened.Shutdown()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestS0PassiveOpenAndShutdownLeaveWholeTreeByteExact(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("agents.json", map[string]any{"agent": map[string]any{
		"id": "agent", "name": "agent", "threadId": "thread", "status": "running", "pendingProviderSwitch": map[string]any{"providerId": "old"},
	}})
	write("topics.json", map[string]any{"topic": map[string]any{"id": "topic", "title": "legacy", "status": "active"}})
	write("triggers.json", map[string]any{"trigger": map[string]any{"id": "trigger", "agentId": "agent", "state": "pending"}})
	write("integrations.json", map[string]any{"connections": map[string]any{}, "addresses": map[string]any{}})
	write("comms.ndjson", map[string]any{"message": map[string]any{"id": "msg", "from": "old", "to": "agent", "status": "open"}})
	write("inbox.ndjson", map[string]any{"id": "inbox", "agentId": "agent", "state": "handling"})
	write("attempts.ndjson", map[string]any{"id": "attempt", "agentId": "agent", "status": "running"})
	write("outbox.ndjson", map[string]any{"id": "outbox", "state": "sending"})
	write("provider-operations.ndjson", map[string]any{"id": "provider", "state": "running"})
	if err := os.Mkdir(filepath.Join(dir, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events", "agent.ndjson"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotHubTree(t, dir)
	ro, err := store.OpenWithOptions(dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(ro, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	after := snapshotHubTree(t, dir)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("passive lifecycle mutated durable tree: before=%v after=%v", before, after)
	}
}

func snapshotHubTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = fmt.Sprintf("%x:%d", sum, len(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
