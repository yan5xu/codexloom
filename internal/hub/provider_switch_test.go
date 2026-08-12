package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestSwitchAgentProviderColdResumesSameThread(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	view, err := h.SwitchAgentProvider("agent-1", ProviderSwitchParams{ProviderID: "deepseek", Model: deepSeekModel})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != "agent-1" || view.ThreadID != "thr-stale" || view.ProviderID != "deepseek" || view.Model != deepSeekModel {
		t.Fatalf("switched Agent = %#v", view.Agent)
	}
	if view.PendingProviderSwitch != nil || len(view.ProviderHistory) != 1 {
		t.Fatalf("Provider switch projection = %#v", view.Agent)
	}
	resume := lastRequestParams(t, logPath, "thread/resume")
	if resume["threadId"] != "thr-stale" || resume["modelProvider"] != "deepseek" || resume["model"] != deepSeekModel {
		t.Fatalf("cold resume params = %#v", resume)
	}
	if got := countRequestMethod(t, logPath, "initialize"); got != 2 {
		t.Fatalf("initialize requests = %d, want initial host plus cold switch host", got)
	}

	events, err := st.ReadEvents("agent-1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var switched bool
	for _, event := range events {
		if event.Type != "loom/agent-provider-switched" {
			continue
		}
		var data map[string]any
		_ = json.Unmarshal(event.Data, &data)
		switched = data["previousProviderId"] == "openai" && data["providerId"] == "deepseek" && data["threadId"] == "thr-stale"
	}
	if !switched {
		t.Fatalf("Provider switch event missing: %#v", events)
	}
}

func TestSwitchAgentProviderRollsBackWhenColdResumeFails(t *testing.T) {
	installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		ProviderID: "deepseek", Model: deepSeekModel, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	_, err = h.SwitchAgentProvider("agent-1", ProviderSwitchParams{ProviderID: "openai", Model: "fail-model"})
	if err == nil || !strings.Contains(err.Error(), "provider resume rejected") {
		t.Fatalf("switch error = %v", err)
	}
	view, getErr := h.GetAgent("agent-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if view.ProviderID != "deepseek" || view.Model != deepSeekModel || view.PendingProviderSwitch != nil || len(view.ProviderHistory) != 0 {
		t.Fatalf("Agent binding was not rolled back: %#v", view.Agent)
	}
	if h.providerSwitching {
		t.Fatal("Provider maintenance guard remained active after rollback")
	}
}

func TestSwitchAgentProviderToOpenAIRepairsReasoningContent(t *testing.T) {
	const threadID = "thr-openai"
	installFakeSharedCodexHost(t)
	rolloutPath := writeReasoningRollout(t, threadID)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: threadID,
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		ProviderID: "deepseek", Model: deepSeekModel, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	view, err := h.SwitchAgentProvider("agent-1", ProviderSwitchParams{ProviderID: "openai", Model: "gpt-5.6-sol"})
	if err != nil {
		t.Fatalf("SwitchAgentProvider: %v", err)
	}
	if view.ProviderID != "" || view.Model != "gpt-5.6-sol" || view.PendingProviderSwitch != nil {
		t.Fatalf("switched Agent = %#v", view.Agent)
	}
	data, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":[]`) || strings.Contains(string(data), "plaintext reasoning") {
		t.Fatalf("reasoning content was not sanitized: %s", data)
	}
	backupDir := filepath.Join(st.Dir(), "backups", "provider-switch")
	matches, err := filepath.Glob(filepath.Join(backupDir, "*"+threadID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("provider switch backups = %v, want 1", matches)
	}
	if _, err := os.Stat(matches[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchAgentProviderToOpenAIRestoresRolloutOnFailure(t *testing.T) {
	const threadID = "thr-openai"
	installFakeSharedCodexHost(t)
	rolloutPath := writeReasoningRollout(t, threadID)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "worker", Cwd: "/tmp/stale", ThreadID: threadID,
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		ProviderID: "deepseek", Model: deepSeekModel, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := h.persistAgentsLocked(); err != nil {
		t.Fatal(err)
	}

	_, err = h.SwitchAgentProvider("agent-1", ProviderSwitchParams{ProviderID: "openai", Model: "fail-model"})
	if err == nil || !strings.Contains(err.Error(), "provider resume rejected") {
		t.Fatalf("switch error = %v", err)
	}
	data, readErr := os.ReadFile(rolloutPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "plaintext reasoning") || strings.Contains(string(data), `"content":[]`) {
		t.Fatalf("rollout was not restored after failed switch: %s", data)
	}
	backupDir := filepath.Join(st.Dir(), "backups", "provider-switch")
	matches, err := filepath.Glob(filepath.Join(backupDir, "*"+threadID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("provider switch backups = %v, want 1", matches)
	}
}

func TestSwitchAgentProviderRejectsActiveGoal(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "worker", ThreadID: "thr-1", Status: "idle"}
	h.goals["agent-1"] = &ThreadGoal{ThreadID: "thr-1", Status: GoalStatusActive}
	_, err = h.SwitchAgentProvider("agent-1", ProviderSwitchParams{ProviderID: "openai", Model: "gpt-5.6"})
	if err == nil || !strings.Contains(err.Error(), "active Goal") {
		t.Fatalf("active Goal switch error = %v", err)
	}
}

func writeReasoningRollout(t *testing.T, threadID string) string {
	t.Helper()
	sessionsDir := t.TempDir()
	day := filepath.Join(sessionsDir, "2026", "08", "06")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(day, "rollout-2026-08-06T00-00-00-"+threadID+".jsonl")
	sample := `{"timestamp":"2026-08-06T00:00:00Z","type":"session_meta","payload":{"cwd":"/tmp/stale"}}
{"timestamp":"2026-08-06T00:00:01Z","type":"response_item","payload":{"type":"reasoning","id":"reason-1","summary":[],"content":[{"type":"reasoning_text","text":"plaintext reasoning"}],"encrypted_content":null}}
`
	if err := os.WriteFile(rolloutPath, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", sessionsDir)
	return rolloutPath
}
