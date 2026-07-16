package httpapi

import (
	"os"
	"testing"
)

func TestRestartGoalIntentRoundTripIsAtomicAndCanonical(t *testing.T) {
	dir := t.TempDir()
	intent := restartGoalIntent{
		Version: 1, AgentIDs: []string{"agent-z", "agent-a", "agent-z"}, CreatedAt: "2026-07-17T00:00:00Z",
	}
	if err := writeRestartGoalIntent(dir, intent); err != nil {
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
	if len(matches) != 1 || matches[0].Name() != restartGoalIntentFile {
		t.Fatalf("restart Goal intent left temporary files: %#v", matches)
	}
	if err := clearRestartGoalIntent(dir); err != nil {
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
