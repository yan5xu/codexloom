package hub

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestNormalizeAgentCwdValidatesAbsoluteReadableDirectoryWithoutWriting(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "agent-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "keep.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := directoryNames(t, home)

	got, err := normalizeAgentCwd(filepath.Join(home, "..", "agent-home", "."))
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("normalized cwd = %q, want %q", got, home)
	}
	after := directoryNames(t, home)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("cwd validation changed directory entries: before=%v after=%v", before, after)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("cwd validation changed marker: data=%q err=%v", data, err)
	}

	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	tests := []struct {
		name   string
		value  string
		status int
	}{
		{name: "relative", value: "relative/home", status: 400},
		{name: "missing", value: filepath.Join(root, "missing"), status: 404},
		{name: "file", value: file, status: 409},
		{name: "permission", value: locked, status: 403},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeAgentCwd(test.value)
			var hubErr *HubError
			if !errors.As(err, &hubErr) || hubErr.Status != test.status {
				t.Fatalf("normalize error = %v, want HubError status %d", err, test.status)
			}
		})
	}
}

func TestUpdateAgentCwdCreatesDefaultAgentHomeWithoutMigratingContentAndIsIdempotent(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("PINIX_EDGE_NAMES", filepath.Join(t.TempDir(), "missing.json"))
	installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	oldCwd := t.TempDir()
	marker := filepath.Join(oldCwd, "keep.txt")
	if err := os.WriteFile(marker, []byte("not migrated"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: oldCwd, ThreadID: "thread-1",
		Status: "idle", CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(userHome, "codexloom", "agents", "worker")
	result, err := h.UpdateAgentCwd("worker", AgentCwdUpdateParams{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Update.OldCwd != oldCwd || result.Update.NewCwd != want || result.Update.AgentID != "agent-1" || result.Update.ThreadID != "thread-1" {
		t.Fatalf("default Agent Home receipt = %#v", result.Update)
	}
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("default Agent Home info = %#v, %v", info, err)
	}
	if entries := directoryNames(t, want); len(entries) != 0 {
		t.Fatalf("default Agent Home received migrated content: %v", entries)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "not migrated" {
		t.Fatalf("old Agent Home content changed: data=%q err=%v", data, err)
	}

	repeated, err := h.UpdateAgentCwd("agent-1", AgentCwdUpdateParams{})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Update.EffectiveState != agentCwdEffectiveUnchanged || repeated.Update.OldCwd != want || repeated.Update.NewCwd != want {
		t.Fatalf("repeated default update = %#v", repeated.Update)
	}
	var persisted map[string]*Agent
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["agent-1"] == nil || persisted["agent-1"].Cwd != want || persisted["agent-1"].ThreadID != "thread-1" {
		t.Fatalf("persisted default Agent Home = %#v", persisted["agent-1"])
	}
}

func TestUpdateAgentCwdRejectsRunningBeforeCreatingDefaultAgentHome(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	oldCwd := t.TempDir()
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", Cwd: oldCwd, ThreadID: "thread-1", Status: "running", UpdatedAt: now()}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.UpdateAgentCwd("worker", AgentCwdUpdateParams{}); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("running default update error = %v", err)
	}
	want := filepath.Join(userHome, "codexloom", "agents", "worker")
	if _, err := os.Stat(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected update created default Agent Home: %v", err)
	}
	if h.agents["agent-1"].Cwd != oldCwd {
		t.Fatalf("rejected default update changed cwd to %q", h.agents["agent-1"].Cwd)
	}
}

func TestUpdateAgentCwdPreservesIdentityRefreshesSkillsAndColdResumes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PINIX_EDGE_NAMES", filepath.Join(t.TempDir(), "missing.json"))
	logPath := installFakeSharedCodexHost(t)
	t.Setenv("CODEX_LOOM_CODEX_BIN", filepath.Join(filepath.Dir(logPath), "codex"))
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agentSkillConfigs = map[string]*AgentSkillConfig{}
	defer h.Shutdown()

	oldCwd := t.TempDir()
	newCwd := t.TempDir()
	const (
		agentID  = "agent-cwd"
		threadID = "019fe65e-01a1-77d1-8395-7c2e551b92e4"
	)
	meta := &Agent{
		ID: agentID, Name: "worker", Cwd: oldCwd, ThreadID: threadID,
		Sandbox: "workspace-write", ApprovalPolicy: "on-request",
		Effort: "high", Status: "idle",
		CreatedAt: "2026-08-01T00:00:00Z", UpdatedAt: now(),
		ProviderHistory: []ProviderBindingChange{{ProviderID: deepSeekProviderID, Model: deepSeekModel, SwitchedAt: "2026-08-02T00:00:00Z"}},
	}
	profile := &AgentProfile{AgentID: agentID, Identity: "stable", Version: 3}
	goal := &ThreadGoal{ThreadID: meta.ThreadID, Objective: "keep goal", Status: GoalStatusPaused}
	relationship := &TeamRelationship{ID: "rel-1", FromAgentID: agentID, ToAgentID: "other"}
	message := &AgentMessage{ID: "msg-1", FromAgentID: agentID, ToAgentID: "other", Subject: "keep message"}
	topic := &Topic{
		ID: "topic-1", Title: "keep topic", Status: TopicStatusActive, ResponsibleAgentID: agentID,
		Participants: []TopicParticipant{{AgentID: agentID, Agent: "worker", Responsibility: "keep responsibility"}},
	}
	h.agents[agentID] = meta
	h.seqs[agentID] = 0
	h.profiles[agentID] = profile
	h.goals[agentID] = goal
	h.teamLinks[relationship.ID] = relationship
	h.comms[message.ID] = message
	if h.topics == nil {
		h.topics = map[string]*Topic{}
	}
	h.topics[topic.ID] = topic
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	h.emitLocked(agentID, "fixture/history", map[string]any{"value": "preserved"})
	historyBeforeUpdate, err := st.ReadEvents(agentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	h.mu.Lock()
	loadedRuntime, err := h.getRuntimeLocked(meta)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(loadedRuntime); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	before := *meta
	profileBeforeUpdate := h.profiles[agentID]
	goalBeforeUpdate := h.goals[agentID]
	relationshipBeforeUpdate := h.teamLinks[relationship.ID]
	messageBeforeUpdate := h.comms[message.ID]
	topicBeforeUpdate := h.topics[topic.ID]
	h.mu.Unlock()

	result, err := h.UpdateAgentCwd("worker", AgentCwdUpdateParams{Cwd: newCwd})
	if err != nil {
		t.Fatal(err)
	}
	if result.Update.AgentID != agentID || result.Update.ThreadID != before.ThreadID ||
		result.Update.OldCwd != oldCwd || result.Update.NewCwd != newCwd {
		t.Fatalf("cwd update receipt = %#v", result.Update)
	}
	if result.Update.EffectiveState != agentCwdEffectiveNextTurn ||
		result.Update.RuntimeState != agentCwdRuntimeColdResume ||
		result.Update.SkillsState != agentCwdSkillsRefreshed {
		t.Fatalf("cwd update states = %#v", result.Update)
	}
	if result.Agent.ID != agentID || result.Agent.ThreadID != before.ThreadID || result.Agent.Cwd != newCwd || result.Agent.ProcessAlive {
		t.Fatalf("updated Agent view = %#v", result.Agent)
	}

	h.mu.Lock()
	after := *h.agents[agentID]
	_, runtimeStillLoaded := h.runtimes[agentID]
	profilePreserved := h.profiles[agentID] == profileBeforeUpdate
	goalPreserved := h.goals[agentID] == goalBeforeUpdate
	relationshipPreserved := h.teamLinks[relationship.ID] == relationshipBeforeUpdate
	messagePreserved := h.comms[message.ID] == messageBeforeUpdate
	topicPreserved := h.topics[topic.ID] == topicBeforeUpdate
	h.mu.Unlock()
	if runtimeStillLoaded {
		t.Fatal("old Agent runtime remained loaded after cwd update")
	}
	normalizedAfter := after
	normalizedAfter.Cwd = before.Cwd
	normalizedAfter.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(normalizedAfter, before) {
		t.Fatalf("cwd update changed non-cwd Agent fields:\n before=%#v\n after=%#v", before, after)
	}
	if !profilePreserved || !goalPreserved || !relationshipPreserved || !messagePreserved || !topicPreserved {
		t.Fatalf("durable identity projections changed: profile=%v goal=%v relationship=%v message=%v topic=%v", profilePreserved, goalPreserved, relationshipPreserved, messagePreserved, topicPreserved)
	}
	historyAfterUpdate, err := st.ReadEvents(agentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(historyAfterUpdate) != len(historyBeforeUpdate)+1 || !reflect.DeepEqual(historyAfterUpdate[:len(historyBeforeUpdate)], historyBeforeUpdate) || historyAfterUpdate[len(historyAfterUpdate)-1].Type != "loom/agent-cwd-updated" {
		t.Fatalf("existing history was rewritten: before=%#v after=%#v", historyBeforeUpdate, historyAfterUpdate)
	}

	skillParams := lastRequestParams(t, logPath, "skills/list")
	cwds := stringSlice(skillParams["cwds"])
	if !containsString(cwds, newCwd) || containsString(cwds, oldCwd) {
		t.Fatalf("refreshed Skill cwds = %v, want new cwd only for target", cwds)
	}
	if result.Inventory.AgentID != agentID || result.Inventory.Cwd != newCwd {
		t.Fatalf("projected Agent Skill inventory = %#v", result.Inventory)
	}

	persisted := map[string]*Agent{}
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[agentID] == nil || persisted[agentID].Cwd != newCwd || persisted[agentID].ThreadID != before.ThreadID {
		t.Fatalf("persisted Agent = %#v", persisted[agentID])
	}

	h.mu.Lock()
	coldRuntime, err := h.getRuntimeLocked(h.agents[agentID])
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(coldRuntime); err != nil {
		t.Fatal(err)
	}
	resume := lastRequestParams(t, logPath, "thread/resume")
	if resume["threadId"] != before.ThreadID || resume["cwd"] != newCwd || resume["sandbox"] != before.Sandbox {
		t.Fatalf("cold-resume params = %#v", resume)
	}
	if _, ok := resume["modelProvider"]; ok {
		t.Fatalf("cold-resume unexpectedly changed default Provider binding: %#v", resume)
	}
	if _, ok := resume["model"]; ok {
		t.Fatalf("cold-resume unexpectedly changed default model binding: %#v", resume)
	}

	h.mu.Lock()
	host := h.codexHost
	h.mu.Unlock()
	host.client.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		closed := h.codexHost == nil
		h.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	reconnectedRuntime, err := h.getRuntimeLocked(h.agents[agentID])
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(reconnectedRuntime); err != nil {
		t.Fatal(err)
	}
	resume = lastRequestParams(t, logPath, "thread/resume")
	if resume["threadId"] != before.ThreadID || resume["cwd"] != newCwd {
		t.Fatalf("Host reconnect resume params = %#v", resume)
	}
}

func TestUpdateAgentCwdRejectsRunningActiveAndPendingTurnState(t *testing.T) {
	newCwd := t.TempDir()
	for _, test := range []struct {
		name   string
		status string
		rt     *runtime
		want   string
	}{
		{name: "running projection", status: "running", want: "running"},
		{name: "active Turn", status: "idle", rt: &runtime{activeTurn: &turnState{turnID: "turn-1"}}, want: "active Turn"},
		{name: "pending approval", status: "idle", rt: &runtime{approvals: map[string]*approval{"approval-1": {}}}, want: "pending approval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h := testHub(st)
			oldCwd := t.TempDir()
			h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", Cwd: oldCwd, ThreadID: "thread-1", Status: test.status, UpdatedAt: now()}
			if test.rt != nil {
				h.runtimes["agent-1"] = test.rt
			}
			if err := h.persistAgentsLocked(); err != nil {
				t.Fatal(err)
			}
			_, err = h.UpdateAgentCwd("worker", AgentCwdUpdateParams{Cwd: newCwd})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("UpdateAgentCwd error = %v, want %q", err, test.want)
			}
			if h.agents["agent-1"].Cwd != oldCwd {
				t.Fatalf("rejected update changed cwd to %q", h.agents["agent-1"].Cwd)
			}
		})
	}
}

func TestUpdateAgentCwdInvalidPathsLeaveOldValue(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	oldCwd := t.TempDir()
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", Cwd: oldCwd, ThreadID: "thread-1", Status: "idle", UpdatedAt: now()}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"relative/home", filepath.Join(t.TempDir(), "missing")} {
		if _, err := h.UpdateAgentCwd("worker", AgentCwdUpdateParams{Cwd: value}); err == nil {
			t.Fatalf("accepted invalid cwd %q", value)
		}
		if h.agents["agent-1"].Cwd != oldCwd {
			t.Fatalf("invalid cwd %q changed old value to %q", value, h.agents["agent-1"].Cwd)
		}
	}
	persisted := map[string]*Agent{}
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["agent-1"] == nil || persisted["agent-1"].Cwd != oldCwd {
		t.Fatalf("invalid update changed persisted Agent: %#v", persisted["agent-1"])
	}
}

func TestUpdateAgentCwdSkillRefreshFailureLeavesOldValue(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	oldCwd := t.TempDir()
	newCwd := filepath.Join(userHome, "codexloom", "agents", "worker")
	installFailingAgentCwdCodex(t, newCwd)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: oldCwd,
		ThreadID: "019fe65e-01a1-77d1-8395-7c2e551b92e4",
		Sandbox:  "danger-full-access", ApprovalPolicy: "never", Status: "idle", UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	_, err = h.UpdateAgentCwd("worker", AgentCwdUpdateParams{})
	if err == nil || !strings.Contains(err.Error(), "refresh Agent Skills") {
		t.Fatalf("Skill refresh failure = %v", err)
	}
	if h.agents["agent-1"].Cwd != oldCwd {
		t.Fatalf("failed Skill refresh changed cwd to %q", h.agents["agent-1"].Cwd)
	}
	persisted := map[string]*Agent{}
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["agent-1"] == nil || persisted["agent-1"].Cwd != oldCwd {
		t.Fatalf("failed Skill refresh changed persisted Agent: %#v", persisted["agent-1"])
	}
	if _, err := os.Stat(newCwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Skill refresh left default Agent Home behind: %v", err)
	}
}

func TestUpdateAgentCwdPersistenceFailureKeepsOldRegistryAndRuntime(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	logPath := installFakeSharedCodexHost(t)
	t.Setenv("CODEX_LOOM_CODEX_BIN", filepath.Join(filepath.Dir(logPath), "codex"))
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	oldCwd := t.TempDir()
	newCwd := filepath.Join(userHome, "codexloom", "agents", "worker")
	meta := &Agent{
		ID: "agent-1", Name: "worker", Cwd: oldCwd,
		ThreadID: "019fe65e-01a1-77d1-8395-7c2e551b92e4",
		Sandbox:  "danger-full-access", ApprovalPolicy: "never", Status: "idle", UpdatedAt: now(),
	}
	h.agents[meta.ID] = meta
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	loadedRuntime, err := h.getRuntimeLocked(meta)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(loadedRuntime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dataDir, "sessions.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dataDir, "sessions.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = h.UpdateAgentCwd("worker", AgentCwdUpdateParams{})
	if err == nil || !strings.Contains(err.Error(), "save Agent cwd") {
		t.Fatalf("persistence failure = %v", err)
	}
	if h.agents[meta.ID].Cwd != oldCwd || h.runtimes[meta.ID] != loadedRuntime {
		t.Fatalf("failed persistence changed memory state: agent=%#v runtimeSame=%v", h.agents[meta.ID], h.runtimes[meta.ID] == loadedRuntime)
	}
	persisted := map[string]*Agent{}
	if err := st.LoadAgents(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[meta.ID] == nil || persisted[meta.ID].Cwd != oldCwd {
		t.Fatalf("failed persistence changed canonical registry: %#v", persisted[meta.ID])
	}
	events, err := st.ReadEvents(meta.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "loom/agent-cwd-updated" {
			t.Fatalf("failed update emitted committed receipt: %#v", event)
		}
	}
	if _, err := os.Stat(newCwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed persistence left default Agent Home behind: %v", err)
	}
}

func TestAgentCwdFenceRejectsTurnStart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", Cwd: t.TempDir(), Status: "idle"}
	h.agentCwdUpdates = map[string]struct{}{"agent-1": {}}
	_, err = h.SendTask("worker", "must not start", time.Second)
	if err == nil || !strings.Contains(err.Error(), "cwd update is in progress") {
		t.Fatalf("SendTask during cwd update error = %v", err)
	}
	h.mu.Lock()
	_, err = h.getRuntimeLocked(h.agents["agent-1"])
	h.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "cwd update is in progress") {
		t.Fatalf("runtime load during cwd update error = %v", err)
	}
}

func directoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func installFailingAgentCwdCodex(t *testing.T, failCwd string) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"result":{"userAgent":"fake-agent-cwd-failure"}}\n' "$id" ;;
    *'"method":"skills/list"'*)
      case "$line" in
        *"$CODEX_HOST_FAIL_CWD"*) printf '{"id":%s,"error":{"code":-32603,"message":"forced Skill refresh failure"}}\n' "$id" ;;
        *) printf '{"id":%s,"result":{"data":[]}}\n' "$id" ;;
      esac ;;
    *'"method":"remoteControl/status/read"'*)
      printf '{"id":%s,"result":{"status":"disabled","serverName":"test.local","installationId":"test-install","environmentId":null}}\n' "$id" ;;
    *) printf '{"id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_LOOM_CODEX_BIN", binPath)
	t.Setenv("CODEX_REMOTE_BIN", binPath)
	t.Setenv("CODEX_HOST_FAIL_CWD", failCwd)
}
