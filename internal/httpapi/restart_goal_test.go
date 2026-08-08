package httpapi

import (
	"os"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestRestartGoalIntentRoundTripIsAtomicAndCanonical(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	intent := restartGoalIntent{
		Version: 1, AgentIDs: []string{"agent-z", "agent-a", "agent-z"}, CreatedAt: "2026-07-17T00:00:00Z",
	}
	if err := writeRestartGoalIntent(st, intent); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(restartGoalIntentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("restart Goal intent mode = %o, want 600", info.Mode().Perm())
	}
	loaded, found, err := readRestartGoalIntent(dir)
	if err != nil || !found {
		t.Fatalf("read restart Goal intent = %#v, found=%v, err=%v", loaded, found, err)
	}
	if len(loaded.AgentIDs) != 2 || loaded.AgentIDs[0] != "agent-a" || loaded.AgentIDs[1] != "agent-z" {
		t.Fatalf("restart Goal Agent IDs = %#v", loaded.AgentIDs)
	}
	matches, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundIntent := false
	for _, match := range matches {
		if match.Name() == restartGoalIntentFile {
			foundIntent = true
		}
		if strings.HasPrefix(match.Name(), ".restart-goals-") && strings.HasSuffix(match.Name(), ".tmp") {
			t.Fatalf("restart Goal intent left temporary file %q", match.Name())
		}
	}
	if !foundIntent {
		t.Fatalf("restart Goal intent is missing from %#v", matches)
	}
	if err := clearRestartGoalIntent(st); err != nil {
		t.Fatal(err)
	}
	if _, found, err := readRestartGoalIntent(dir); err != nil || found {
		t.Fatalf("cleared restart Goal intent found=%v, err=%v", found, err)
	}
}

func TestRestartGoalIntentRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(restartGoalIntentPath(dir), []byte(`{"version":2,"agentIds":["agent-a"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readRestartGoalIntent(dir); err == nil {
		t.Fatal("unknown restart Goal intent version was accepted")
	}
}

func TestRestartGoalIntentRequiresLiveHubAfterOwnershipRetires(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ownership, err := st.ClaimWritableOwnership()
	if err != nil {
		t.Fatal(err)
	}
	ownership.Release()
	err = writeRestartGoalIntent(st, restartGoalIntent{
		Version: 1, AgentIDs: []string{"agent-a"}, CreatedAt: "2026-08-08T00:00:00Z",
	})
	if err == nil || !strings.Contains(err.Error(), "no live writable Hub owner") {
		t.Fatalf("retired ownership restart intent error = %v", err)
	}
}
