package hub

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestPassiveOpenServeShutdownLeavesEntireDurableTreeUnchanged(t *testing.T) {
	dir := t.TempDir()
	fixtures := map[string]string{
		"agents.json":                `{"agent-1":{"id":"agent-1","name":"agent-one","threadId":"thread-1","status":"idle","pendingProviderSwitch":{"providerId":"custom","model":"fixture","startedAt":"2026-08-07T00:00:00Z"},"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}`,
		"integrations.json":          `{"connections":{"conn-legacy":{"id":"conn-legacy","provider":"fixture","status":"disconnected","enabled":true,"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}},"addresses":{"addr-legacy":{"id":"addr-legacy","agentId":"agent-1","connectionId":"conn-legacy","externalIdentity":"fixture://legacy","triggerPolicy":"direct","replyPolicy":"explicit","trustDomain":"fixture","allowConversations":["conversation-legacy"],"enabled":true,"version":0,"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}}`,
		"topics.json":                `{"topic-legacy":{"title":"legacy","status":"unknown","responsibleAgentId":"agent-1","currentBrief":{}}}`,
		"comms.ndjson":               `{"message":{"id":"msg-trigger","from":"agent-one","to":"agent-one","response":"none","status":"closed","deliveryStatus":"delivered","handlingStatus":"completed","triggerId":"trigger-legacy","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}` + "\n",
		"inbox.ndjson":               `{"id":"inbox-running","agentId":"agent-1","state":"handling","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"attempts.ndjson":            `{"id":"attempt-running","inboxItemId":"inbox-running","sessionId":"agent-1","status":"running","startedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"outbox.ndjson":              `{"id":"outbox-sending","agentId":"agent-1","state":"sending","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"provider-operations.ndjson": `{"id":"provider-running","provider":"lark","agentId":"agent-1","state":"running","createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}` + "\n",
		"triggers.json":              `{"trigger-legacy":{"id":"trigger-legacy","agentId":"agent-1","state":"pending","version":1,"createdAt":"2026-08-07T00:00:00Z","updatedAt":"2026-08-07T00:00:00Z"}}`,
		"credential-migrations.json": `{"old_candidate":{"state":"manual_recovery_required"}}`,
	}
	for name, value := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotPassiveTree(t, dir)
	st, err := store.OpenWithOptions(dir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := OpenWithOptions(st, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.ListConnections()
	h.Shutdown()
	after := snapshotPassiveTree(t, dir)
	if len(before) != len(after) {
		t.Fatalf("passive fileset changed: before=%v after=%v", before, after)
	}
	for path, want := range before {
		if after[path] != want {
			t.Fatalf("passive bytes changed for %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime-foundation.json")); !os.IsNotExist(err) {
		t.Fatalf("passive legacy open created foundation: %v", err)
	}
}

func snapshotPassiveTree(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := map[string][sha256.Size]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative+"/"] = sha256.Sum256(nil)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
