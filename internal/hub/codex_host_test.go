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

func TestSharedCodexHostRoutesRemoteTurnIntoAgentEvents(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.loadRemoteLocked()
	h.agents["agent-1"] = &Agent{
		ID: "agent-1", Name: "research", Cwd: "/tmp/research", ThreadID: "thr-shared",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		CreatedAt: now(), UpdatedAt: now(),
	}

	if _, err := h.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		last := h.agents["agent-1"].LastTurn
		h.mu.Unlock()
		if last != nil && last.TurnID == "turn-remote" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.mu.Lock()
	agent := *h.agents["agent-1"]
	host := h.codexHost
	runtime := h.runtimes["agent-1"]
	h.mu.Unlock()
	if host == nil || runtime == nil || runtime.client != host.client {
		t.Fatalf("Agent runtime is not attached to the shared CodexHost")
	}
	if agent.Status != "idle" || agent.LastTurn == nil || agent.LastTurn.TurnID != "turn-remote" {
		t.Fatalf("Agent state after Remote turn = %#v", agent)
	}

	events, err := st.ReadEvents("agent-1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawUser, sawDelta, sawCompleted bool
	for _, event := range events {
		switch event.Type {
		case "item/started":
			sawUser = strings.Contains(string(event.Data), "hello from phone")
		case "item/agentMessage/delta":
			sawDelta = true
		case "turn/completed":
			sawCompleted = true
		}
	}
	if !sawUser || !sawDelta || !sawCompleted {
		t.Fatalf("Remote events routed to Agent: user=%v delta=%v completed=%v, events=%#v", sawUser, sawDelta, sawCompleted, events)
	}

	initializes := 0
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &request)
		if request.Method == "initialize" {
			initializes++
		}
	}
	if initializes != 1 {
		t.Fatalf("initialize requests = %d, want one shared app-server", initializes)
	}
}

func TestSharedCodexHostAdoptsRemoteResumedThreadOnTurnStart(t *testing.T) {
	installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.loadRemoteLocked()
	host, err := h.ensureCodexHost()
	if err != nil {
		t.Fatal(err)
	}

	h.onHostNotification(host.generation, "turn/started", json.RawMessage(`{
		"threadId":"thr-resumed","turn":{"id":"turn-resumed","status":"inProgress"}
	}`))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		hydrated := false
		for _, agent := range h.agents {
			hydrated = hydrated || agent.ThreadID == "thr-resumed" && agent.Cwd == "/tmp/remote-project"
		}
		h.mu.Unlock()
		if hydrated {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	var adopted *Agent
	for _, agent := range h.agents {
		if agent.ThreadID == "thr-resumed" {
			adopted = agent
			break
		}
	}
	if adopted == nil {
		t.Fatal("Remote resumed Thread was not adopted")
	}
	if adopted.Status != "running" || adopted.CurrentTurnID != "turn-resumed" || adopted.Source != "remote" {
		t.Fatalf("adopted Agent = %#v", adopted)
	}
	if adopted.Cwd != "/tmp/remote-project" || adopted.Name != "mobile-research" {
		t.Fatalf("adopted Agent metadata was not hydrated: %#v", adopted)
	}
}

func TestTwoAgentsShareOneCodexHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := installFakeSharedCodexHost(t)
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()

	first, err := h.CreateAgent(CreateParams{Name: "one", Cwd: "/tmp/one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.CreateAgent(CreateParams{Name: "two", Cwd: "/tmp/two"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ThreadID != "thr-one" || second.ThreadID != "thr-two" {
		t.Fatalf("Thread bindings = %q, %q", first.ThreadID, second.ThreadID)
	}

	h.mu.Lock()
	firstRuntime := h.runtimes[first.ID]
	secondRuntime := h.runtimes[second.ID]
	host := h.codexHost
	h.mu.Unlock()
	if host == nil || firstRuntime == nil || secondRuntime == nil ||
		firstRuntime.client != host.client || secondRuntime.client != host.client {
		t.Fatal("Agents do not share the same CodexHost client")
	}
	if got := countRequestMethod(t, logPath, "initialize"); got != 1 {
		t.Fatalf("initialize requests = %d, want one", got)
	}
	if got := countRequestMethod(t, logPath, "skills/extraRoots/set"); got != 1 {
		t.Fatalf("skills/extraRoots/set requests = %d, want one", got)
	}
	if got := countRequestMethod(t, logPath, "skills/list"); got < 1 {
		t.Fatalf("skills/list requests = %d, want at least one", got)
	}
	inventory, err := h.ReloadSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Data) != 1 || len(inventory.Data[0].Skills) != 2 || inventory.Data[0].Skills[0].Name != "loom-needs-you" || inventory.Data[0].Skills[1].Name != "loom-external-messaging" {
		t.Fatalf("reloaded skill inventory = %#v", inventory)
	}
	reload := lastRequestParams(t, logPath, "skills/list")
	if reload["forceReload"] != true {
		t.Fatalf("skills/list forceReload = %#v, want true", reload["forceReload"])
	}
	cwds, ok := reload["cwds"].([]any)
	if !ok || len(cwds) != 2 || cwds[0] != "/tmp/one" || cwds[1] != "/tmp/two" {
		t.Fatalf("skills/list cwds = %#v, want both Agent workspaces", reload["cwds"])
	}
	for _, name := range []string{"loom-communication", "domain-agent-coaching", "loom-integrations", "loom-external-messaging", "loom-parall", "loom-feishu", "loom-needs-you", "loom-artifacts"} {
		if _, err := os.Stat(filepath.Join(dataDir, "builtin-skills", name, "SKILL.md")); err != nil {
			t.Fatalf("materialized %s: %v", name, err)
		}
	}
	if got := countRequestMethod(t, logPath, "thread/start"); got != 2 {
		t.Fatalf("thread/start requests = %d, want two", got)
	}
}

func TestSendTaskResumesCachedThreadBeforeTurnStart(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.agents["agent-stale"] = &Agent{
		ID: "agent-stale", Name: "stale", Cwd: "/tmp/stale", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		CreatedAt: now(), UpdatedAt: now(),
	}

	h.mu.Lock()
	rt, err := h.getRuntimeLocked(h.agents["agent-stale"])
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(rt); err != nil {
		t.Fatal(err)
	}
	marker := os.Getenv("CODEX_HOST_RESUMED")
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	result, err := h.SendTask("agent-stale", "hello", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID != "turn-stale" {
		t.Fatalf("turn id = %q, want turn-stale", result.TurnID)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cached Thread was not resumed before turn/start: %v", err)
	}
	resume := lastRequestParams(t, logPath, "thread/resume")
	if resume["sandbox"] != "danger-full-access" {
		t.Fatalf("thread/resume sandbox = %#v, want danger-full-access", resume["sandbox"])
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	policy, ok := turn["sandboxPolicy"].(map[string]any)
	if !ok || policy["type"] != "dangerFullAccess" {
		t.Fatalf("turn/start sandboxPolicy = %#v, want dangerFullAccess", turn["sandboxPolicy"])
	}
	input, ok := turn["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("internal Agent turn input = %#v, want only task text", turn["input"])
	}
	text, ok := input[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "hello" {
		t.Fatalf("task text input = %#v", input[0])
	}
}

func TestSendTaskDoesNotPinSkillsForExternalFacingAgent(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	defer h.Shutdown()
	h.addresses = map[string]*AgentAddress{}
	h.agents["agent-external"] = &Agent{
		ID: "agent-external", Name: "external", Cwd: "/tmp/one", ThreadID: "thr-stale",
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Status: "idle",
		CreatedAt: now(), UpdatedAt: now(),
	}
	h.addresses["addr-external"] = &AgentAddress{
		ID: "addr-external", AgentID: "agent-external", Enabled: true,
		CreatedAt: now(), UpdatedAt: now(),
	}

	if _, err := h.SendTask("agent-external", "publish the report", time.Minute); err != nil {
		t.Fatal(err)
	}
	turn := lastRequestParams(t, logPath, "turn/start")
	input, ok := turn["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("external Agent turn input = %#v, want only task text", turn["input"])
	}
	text, ok := input[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "publish the report" {
		t.Fatalf("task text input = %#v", input[0])
	}
}

func lastRequestParams(t *testing.T, path, method string) map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var params map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err == nil && request.Method == method {
			params = request.Params
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if params == nil {
		t.Fatalf("request %q not found in %s", method, path)
	}
	return params
}

func countRequestMethod(t *testing.T, path, method string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &request)
		if request.Method == method {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCodexHostEnvAddsConfiguredLoomDirectory(t *testing.T) {
	dir := t.TempDir()
	loomBin := filepath.Join(dir, "loom")
	if err := os.WriteFile(loomBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_LOOM_CLI_BIN", loomBin)
	t.Setenv("PATH", "/usr/bin:/bin")
	env := codexHostEnv()
	want := dir + string(os.PathListSeparator) + "/usr/bin:/bin"
	if env["PATH"] != want {
		t.Fatalf("CodexHost PATH = %q, want %q", env["PATH"], want)
	}
}

func TestMissingUserSkillsLetsUserSkillWin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userSkill := filepath.Join(home, ".agents", "skills", "loom-communication")
	if err := os.MkdirAll(userSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userSkill, "SKILL.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := missingUserSkills()
	if len(missing) != 7 || missing[0] != "domain-agent-coaching" || missing[1] != "loom-integrations" || missing[2] != "loom-external-messaging" || missing[3] != "loom-parall" || missing[4] != "loom-feishu" || missing[5] != "loom-needs-you" || missing[6] != "loom-artifacts" {
		t.Fatalf("missing user skills = %#v", missing)
	}
}

func installFakeSharedCodexHost(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codex")
	logPath := filepath.Join(dir, "requests.ndjson")
	resumeMarker := filepath.Join(dir, "resumed")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_HOST_LOG"
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"result":{"userAgent":"fake-shared"}}\n' "$id" ;;
	*'"method":"remoteControl/status/read"'*)
	  printf '{"id":%s,"result":{"status":"disabled","serverName":"shared.local","installationId":"install-shared","environmentId":null}}\n' "$id" ;;
	*'"method":"skills/list"'*'/tmp/stale'*)
	  printf '{"id":%s,"result":{"data":[{"cwd":"/tmp/stale","skills":[{"name":"loom-needs-you","description":"Human requests","path":"/tmp/needs/SKILL.md","scope":"user","enabled":true}],"errors":[]}]}}\n' "$id" ;;
	*'"method":"skills/list"'*)
	  printf '{"id":%s,"result":{"data":[{"cwd":"/tmp/one","skills":[{"name":"loom-needs-you","description":"Human requests","path":"/tmp/needs/SKILL.md","scope":"user","enabled":true},{"name":"loom-external-messaging","description":"External messaging","path":"/tmp/skill/SKILL.md","scope":"user","enabled":true}],"errors":[]}]}}\n' "$id" ;;
	*'"method":"thread/start"'*'"cwd":"/tmp/one"'*)
	  printf '{"method":"thread/started","params":{"thread":{"id":"thr-one","name":null,"cwd":"/tmp/one"}}}\n'
	  printf '{"id":%s,"result":{"thread":{"id":"thr-one"}}}\n' "$id" ;;
	*'"method":"thread/start"'*'"cwd":"/tmp/two"'*)
	  printf '{"method":"thread/started","params":{"thread":{"id":"thr-two","name":null,"cwd":"/tmp/two"}}}\n'
	  printf '{"id":%s,"result":{"thread":{"id":"thr-two"}}}\n' "$id" ;;
    *'"method":"remoteControl/enable"'*)
      printf '{"id":%s,"result":{"status":"connected","serverName":"shared.local","installationId":"install-shared","environmentId":"env-shared"}}\n' "$id"
      printf '{"method":"turn/started","params":{"threadId":"thr-shared","turn":{"id":"turn-remote","status":"inProgress"}}}\n'
      printf '{"method":"item/started","params":{"threadId":"thr-shared","turnId":"turn-remote","item":{"id":"user-1","type":"userMessage","content":[{"type":"text","text":"hello from phone"}]}}}\n'
      printf '{"method":"item/agentMessage/delta","params":{"threadId":"thr-shared","turnId":"turn-remote","itemId":"answer-1","delta":"hello"}}\n'
      printf '{"method":"turn/completed","params":{"threadId":"thr-shared","turn":{"id":"turn-remote","status":"completed"}}}\n' ;;
	*'"method":"thread/read"'*)
	  printf '{"id":%s,"result":{"thread":{"id":"thr-resumed","name":"mobile-research","cwd":"/tmp/remote-project"}}}\n' "$id" ;;
	*'"method":"thread/resume"'*)
	  : > "$CODEX_HOST_RESUMED"
	  printf '{"id":%s,"result":{"thread":{"id":"thr-stale"}}}\n' "$id" ;;
	*'"method":"turn/start"'*'"threadId":"thr-stale"'*)
	  if [ -f "$CODEX_HOST_RESUMED" ]; then
	    printf '{"id":%s,"result":{"turn":{"id":"turn-stale"}}}\n' "$id"
	  else
	    printf '{"id":%s,"error":{"code":-32602,"message":"thread not found: thr-stale"}}\n' "$id"
	  fi ;;
    *) printf '{"id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_REMOTE_BIN", binPath)
	t.Setenv("CODEX_HOST_LOG", logPath)
	t.Setenv("CODEX_HOST_RESUMED", resumeMarker)
	return logPath
}
