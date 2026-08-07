package hub

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestPassiveOpenAndShutdownLeaveEntireDurableTreeUnchanged(t *testing.T) {
	dir := t.TempDir()
	fixtures := map[string]string{
		"agents.json":                `{"agent-1":{"id":"agent-1","name":"agent-one","threadId":"thread-1","status":"idle","pendingProviderSwitch":{"providerId":"custom","model":"fixture","startedAt":"2026-08-07T00:00:00Z"},"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}`,
		"topics.json":                `{"topic-legacy":{"title":"legacy","status":"unknown","responsibleAgentId":"agent-1","currentBrief":{}}}`,
		"comms.ndjson":               `{"message":{"id":"msg-trigger","from":"agent-one","to":"agent-one","response":"none","status":"closed","deliveryStatus":"delivered","handlingStatus":"completed","triggerId":"trigger-legacy","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}` + "\n",
		"inbox.ndjson":               `{"id":"inbox-running","agentId":"agent-1","state":"handling","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"attempts.ndjson":            `{"id":"attempt-running","inboxItemId":"inbox-running","sessionId":"agent-1","status":"running","startedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"outbox.ndjson":              `{"id":"outbox-sending","agentId":"agent-1","state":"sending","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"provider-operations.ndjson": `{"id":"provider-running","provider":"lark","agentId":"agent-1","state":"running","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"triggers.json":              `{"trigger-legacy":{"id":"trigger-legacy","agentId":"agent-1","state":"pending","version":1,"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}`,
	}
	for name, value := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotDurableTree(t, dir)
	st, err := store.OpenWithOptions(dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.ListConnections()
	h.Shutdown()
	after := snapshotDurableTree(t, dir)
	if len(before) != len(after) {
		t.Fatalf("passive durable fileset changed: before=%v after=%v", sortedTreeKeys(before), sortedTreeKeys(after))
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok || got != want {
			t.Fatalf("passive durable bytes changed for %s: before=%x after=%x present=%v", path, want, got, ok)
		}
	}
}

func snapshotDurableTree(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := map[string][sha256.Size]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(bytes.Clone(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func sortedTreeKeys(values map[string][sha256.Size]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
