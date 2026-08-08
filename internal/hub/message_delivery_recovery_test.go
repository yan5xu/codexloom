package hub

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestAgentMessageInjectTimeoutFencesSameHostAndRecoversSameMessageAfterRestart(t *testing.T) {
	logPath, lateEffectPath := installFakeIndeterminateThreadHost(t, "thread/inject_items")
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	h := testHub(st)
	h.stop = make(chan struct{})
	h.agentSkillConfigs = map[string]*AgentSkillConfig{}
	h.topics = map[string]*Topic{}
	h.threadResumeTimeout = 100 * time.Millisecond
	h.developerContextTimeout = 75 * time.Millisecond
	createdAt := now()
	h.agents["agent-source"] = &Agent{
		ID: "agent-source", Name: "source", ThreadID: "thread-source", Cwd: t.TempDir(),
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	h.agents["agent-target"] = &Agent{
		ID: "agent-target", Name: "target", ThreadID: "thread-target", Cwd: t.TempDir(),
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	h.agents["agent-other"] = &Agent{
		ID: "agent-other", Name: "other", ThreadID: "thread-other", Cwd: t.TempDir(),
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	h.topics["topic-1"] = &Topic{
		ID: "topic-1", Title: "Recovery", Purpose: "Keep one causal delivery", CompletionBoundary: "One handling Turn",
		Status: TopicStatusActive, ResponsibleAgentID: "agent-source", ResponsibleAgent: "source",
		Participants: []TopicParticipant{
			{AgentID: "agent-source", Agent: "source", Responsibility: "request", JoinedAt: createdAt},
			{AgentID: "agent-target", Agent: "target", Responsibility: "handle", JoinedAt: createdAt},
		},
		CurrentBrief: TopicBrief{Version: 1, Summary: "Recover the original Message", UpdatedBy: "source", UpdatedAt: createdAt},
		Version:      1, NextEventSeq: 1, CreatedBy: "source", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	h.contextHistoryProbe = newContextHistoryHarness("initial:thread-target").probe
	message := AgentMessage{
		ID: "msg-original", FromAgentID: "agent-source", ToAgentID: "agent-target", From: "source", To: "target",
		Subject: "Handle once", Body: "Perform the bounded work once.", Response: "required", Status: "open",
		DeliveryStatus: "queued", HandlingStatus: "pending", SourceTurnID: "turn-source", TopicID: "topic-1",
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	h.mu.Lock()
	if err := h.persistAgentsLocked(); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	if err := h.persistTopicsLocked(); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	if err := h.commitAgentMessageLocked(message); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.mu.Unlock()

	host, err := h.ensureCodexHost()
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	h.mu.Lock()
	h.runtimes["agent-target"] = &runtime{
		agentID: "agent-target", client: host.client, hostGeneration: host.generation, ready: ready,
		approvals: map[string]*approval{}, skillConfigLoaded: true, skillConfigHash: agentSkillConfigHash(nil),
	}
	otherReady := make(chan struct{})
	close(otherReady)
	h.runtimes["agent-other"] = &runtime{
		agentID: "agent-other", client: host.client, hostGeneration: host.generation, ready: otherReady,
		approvals: map[string]*approval{}, skillConfigLoaded: true, skillConfigHash: agentSkillConfigHash(nil),
	}
	h.mu.Unlock()

	type deliveryResult struct {
		message *AgentMessage
		ok      bool
	}
	firstDelivery := make(chan deliveryResult, 1)
	go func() {
		delivered, ok := h.deliverNextQueuedForTarget("agent-target", time.Hour)
		firstDelivery <- deliveryResult{message: delivered, ok: ok}
	}()
	waitForRequestCount(t, logPath, "thread/inject_items", 1)
	if _, err := h.SendTask("agent-target", "Concurrent work must not cross the uncertain effect.", time.Hour); err == nil ||
		!strings.Contains(err.Error(), "control outcome is indeterminate") {
		t.Fatalf("caller waiting on startMu crossed the fence: %v", err)
	}
	first := <-firstDelivery
	if delivered, ok := first.message, first.ok; ok || delivered == nil {
		t.Fatalf("first delivery = %#v, ok=%v, want failed before Turn start", delivered, ok)
	}
	failed := mustAgentMessage(t, h, message.ID)
	if failed.DeliveryStatus != "failed" || failed.HandlingStatus != "pending" || len(failed.HandlingAttempts) != 0 ||
		!strings.Contains(failed.LastDeliveryError, "timeout waiting for thread/inject_items") {
		t.Fatalf("failed Message = %#v", failed)
	}
	waitForFile(t, lateEffectPath)
	methodsBeforeRetry := readRequestMethods(t, logPath)
	if countMethod(methodsBeforeRetry, "turn/start") != 0 {
		t.Fatalf("work started before delivery boundary: %v", methodsBeforeRetry)
	}

	if _, err := h.RetryAgentMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	waitForMessageDelivery(t, h, message.ID, "failed")
	fenced := mustAgentMessage(t, h, message.ID)
	if !strings.Contains(fenced.LastDeliveryError, "control outcome is indeterminate") ||
		!strings.Contains(fenced.LastDeliveryError, "replace the current CodexHost") {
		t.Fatalf("same-host retry was not fenced: %#v", fenced)
	}
	methodsAfterRetry := readRequestMethods(t, logPath)
	if len(methodsAfterRetry) != len(methodsBeforeRetry) {
		t.Fatalf("same-host retry emitted more RPCs: before=%v after=%v", methodsBeforeRetry, methodsAfterRetry)
	}
	if fenced.ID != message.ID || fenced.TopicID != message.TopicID || fenced.SourceTurnID != message.SourceTurnID || fenced.Status != "open" {
		t.Fatalf("fence changed causal identity: %#v", fenced)
	}
	h.developerContextTimeout = time.Second
	if _, err := h.SendTask("agent-other", "Unrelated work remains available.", time.Hour); err != nil {
		t.Fatalf("Thread-local fence blocked another Agent: %v; requests=%v", err, readRequestMethods(t, logPath))
	}
	if countRequestForThread(t, logPath, "turn/start", "thread-other") != 1 {
		t.Fatalf("unrelated Thread did not start exactly once")
	}
	h.mu.Lock()
	h.finishTurnLocked(h.agents["agent-other"], h.runtimes["agent-other"], "completed", "")
	h.mu.Unlock()
	h.Shutdown()

	h2, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Shutdown()
	h2.threadResumeTimeout = 100 * time.Millisecond
	h2.developerContextTimeout = 100 * time.Millisecond
	h2.contextHistoryProbe = newContextHistoryHarness("initial:thread-target").probe
	replayed := mustAgentMessage(t, h2, message.ID)
	if replayed.DeliveryStatus != "failed" || replayed.TopicID != message.TopicID || replayed.SourceTurnID != message.SourceTurnID {
		t.Fatalf("restart replay = %#v", replayed)
	}
	if _, err := h2.RetryAgentMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	waitForMessageDelivery(t, h2, message.ID, "delivered")
	recovered := mustAgentMessage(t, h2, message.ID)
	if recovered.ID != message.ID || recovered.DeliveryStatus != "delivered" || recovered.DeliveryMode != "turn_start" ||
		recovered.DeliveredTurnID != "turn-recovered" || recovered.HandlingStatus != "running" || len(recovered.HandlingAttempts) != 1 {
		t.Fatalf("recovered Message = %#v", recovered)
	}
	if recovered.TopicID != message.TopicID || recovered.SourceTurnID != message.SourceTurnID || recovered.Status != "open" {
		t.Fatalf("recovery lost causal lineage: %#v", recovered)
	}
	if messages := h2.ListComms("", ""); len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("recovery created a duplicate Message: %#v", messages)
	}
	methods := readRequestMethods(t, logPath)
	if countRequestForThread(t, logPath, "turn/start", "thread-target") != 1 {
		t.Fatalf("target turn/start count is not one, requests=%v", methods)
	}
}

func TestAgentMessageResumeTimeoutAlsoFencesSameHost(t *testing.T) {
	logPath, lateEffectPath := installFakeIndeterminateThreadHost(t, "thread/resume")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stop = make(chan struct{})
	h.agentSkillConfigs = map[string]*AgentSkillConfig{}
	h.topics = map[string]*Topic{}
	h.threadResumeTimeout = 15 * time.Millisecond
	h.developerContextTimeout = 100 * time.Millisecond
	stamp := now()
	h.agents["agent-target"] = &Agent{
		ID: "agent-target", Name: "target", ThreadID: "thread-target", Cwd: t.TempDir(),
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle", CreatedAt: stamp, UpdatedAt: stamp,
	}
	h.contextHistoryProbe = newContextHistoryHarness("initial:thread-target").probe
	message := AgentMessage{
		ID: "msg-resume", FromAgentID: schedulerAgentID, ToAgentID: "agent-target", From: schedulerIdentity, To: "target",
		Subject: "Resume once", Body: "Do not duplicate this work.", Response: "required", Status: "open",
		DeliveryStatus: "queued", HandlingStatus: "pending", SourceTurnID: "turn-source", CreatedAt: stamp, UpdatedAt: stamp,
	}
	h.mu.Lock()
	if err := h.persistAgentsLocked(); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	if err := h.commitAgentMessageLocked(message); err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.mu.Unlock()
	host, err := h.ensureCodexHost()
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	h.mu.Lock()
	h.runtimes["agent-target"] = &runtime{
		agentID: "agent-target", client: host.client, hostGeneration: host.generation, ready: ready,
		approvals: map[string]*approval{}, skillConfigLoaded: true, skillConfigHash: agentSkillConfigHash(nil),
	}
	h.mu.Unlock()

	if delivered, ok := h.deliverNextQueuedForTarget("agent-target", time.Hour); ok || delivered == nil {
		t.Fatalf("resume-timeout delivery = %#v, ok=%v", delivered, ok)
	}
	failed := mustAgentMessage(t, h, message.ID)
	if failed.DeliveryStatus != "failed" || !strings.Contains(failed.LastDeliveryError, "timeout waiting for thread/resume") ||
		failed.HandlingStatus != "pending" || len(failed.HandlingAttempts) != 0 {
		t.Fatalf("resume-timeout Message = %#v", failed)
	}
	waitForFile(t, lateEffectPath)
	methodsBeforeRetry := readRequestMethods(t, logPath)
	if countMethod(methodsBeforeRetry, "thread/inject_items") != 0 || countMethod(methodsBeforeRetry, "turn/start") != 0 {
		t.Fatalf("resume timeout crossed the delivery boundary: %v", methodsBeforeRetry)
	}
	if _, err := h.RetryAgentMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	waitForMessageDelivery(t, h, message.ID, "failed")
	fenced := mustAgentMessage(t, h, message.ID)
	if !strings.Contains(fenced.LastDeliveryError, "indeterminate after thread/resume timed out") {
		t.Fatalf("resume timeout was not fenced: %#v", fenced)
	}
	if methodsAfterRetry := readRequestMethods(t, logPath); len(methodsAfterRetry) != len(methodsBeforeRetry) {
		t.Fatalf("same-host retry emitted more RPCs: before=%v after=%v", methodsBeforeRetry, methodsAfterRetry)
	}
	h.Shutdown()
}

func installFakeIndeterminateThreadHost(t *testing.T, hangMethod string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	logPath := filepath.Join(dir, "requests.ndjson")
	hangOncePath := filepath.Join(dir, "hang-once")
	lateEffectPath := filepath.Join(dir, "late-effect")
	if err := os.WriteFile(hangOncePath, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_RECOVERY_LOG"
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"result":{"userAgent":"fake-recovery"}}\n' "$id" ;;
    *'"method":"skills/list"'*)
      printf '{"id":%s,"result":{"data":[]}}\n' "$id" ;;
    *'"method":"thread/resume"'*)
      if [ "$CODEX_RECOVERY_HANG_METHOD" = "thread/resume" ] && [ -f "$CODEX_RECOVERY_HANG_ONCE" ]; then
        rm -f "$CODEX_RECOVERY_HANG_ONCE"
        (
		  sleep 0.15
          : > "$CODEX_RECOVERY_LATE_EFFECT"
          printf '{"id":%s,"result":{"thread":{"id":"thread-target"}}}\n' "$id"
        ) &
      else
        printf '{"id":%s,"result":{"thread":{"id":"thread-target"}}}\n' "$id"
      fi ;;
    *'"method":"thread/inject_items"'*)
	  if [ "$CODEX_RECOVERY_HANG_METHOD" = "thread/inject_items" ] && [ -f "$CODEX_RECOVERY_HANG_ONCE" ]; then
        rm -f "$CODEX_RECOVERY_HANG_ONCE"
        (
		  sleep 0.15
          : > "$CODEX_RECOVERY_LATE_EFFECT"
          printf '{"id":%s,"result":{}}\n' "$id"
        ) &
      else
        printf '{"id":%s,"result":{}}\n' "$id"
      fi ;;
    *'"method":"turn/start"'*)
      printf '{"id":%s,"result":{"turn":{"id":"turn-recovered"}}}\n' "$id" ;;
    *)
      printf '{"id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_LOOM_CODEX_BIN", binPath)
	t.Setenv("CODEX_RECOVERY_LOG", logPath)
	t.Setenv("CODEX_RECOVERY_HANG_ONCE", hangOncePath)
	t.Setenv("CODEX_RECOVERY_LATE_EFFECT", lateEffectPath)
	t.Setenv("CODEX_RECOVERY_HANG_METHOD", hangMethod)
	return logPath, lateEffectPath
}

func mustAgentMessage(t *testing.T, h *Hub, id string) AgentMessage {
	t.Helper()
	message, err := h.GetAgentMessage(id)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func waitForMessageDelivery(t *testing.T, h *Hub, id, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if message := mustAgentMessage(t, h, id); message.DeliveryStatus == status {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Message %s did not reach delivery status %s: %#v", id, status, mustAgentMessage(t, h, id))
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForRequestCount(t *testing.T, path, method string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil && countMethod(readRequestMethods(t, path), method) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s request(s)", want, method)
}

func readRequestMethods(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	methods := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var request struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) == nil && request.Method != "" {
			methods = append(methods, request.Method)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return methods
}

func countMethod(methods []string, method string) int {
	count := 0
	for _, candidate := range methods {
		if candidate == method {
			count++
		}
	}
	return count
}

func countRequestForThread(t *testing.T, path, method, threadID string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var request struct {
			Method string `json:"method"`
			Params struct {
				ThreadID string `json:"threadId"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) == nil && request.Method == method && request.Params.ThreadID == threadID {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}
