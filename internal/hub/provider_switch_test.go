package hub

import (
	"encoding/json"
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
