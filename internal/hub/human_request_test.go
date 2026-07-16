package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestHumanRequestPersistsAndResumesSameAgentThread(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-one"] = &Agent{
		ID: "agent-one", Name: "one", ThreadID: "thread-one", Status: "running",
		CurrentTurnID: "turn-source", CurrentTask: "Prepare release", CreatedAt: now(), UpdatedAt: now(),
	}
	h.runtimes["agent-one"] = &runtime{activeTurn: &turnState{
		turnID: "turn-source", task: "Prepare release", startedAt: time.Now(), stopWatchdog: make(chan struct{}),
	}}

	request, err := h.CreateHumanRequest(CreateHumanRequestParams{
		Agent: "one", Question: "Which release window should I use?", Context: "Two windows are available.",
		Options: []HumanRequestOption{{Label: "Tonight", Description: "Faster"}, {Label: "Tomorrow", Description: "Lower risk"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.AgentID != "agent-one" || request.ThreadID != "thread-one" || request.SourceTurnID != "turn-source" {
		t.Fatalf("request source = %#v", request)
	}
	if request.BlockedWork != "Prepare release" || request.DeliveryStatus != "waiting" {
		t.Fatalf("request blocking state = %#v", request)
	}

	answered, err := h.AnswerHumanRequest(request.ID, AnswerHumanRequestParams{Answer: "Use tomorrow morning."})
	if err != nil {
		t.Fatal(err)
	}
	if answered.State != "answered" || answered.DeliveryStatus != "queued" {
		t.Fatalf("answered request = %#v", answered)
	}
	// The source Turn is still running, so the asynchronous delivery must wait.
	time.Sleep(20 * time.Millisecond)
	queued, err := h.GetHumanRequest(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.DeliveryStatus != "queued" {
		t.Fatalf("delivery while Agent is busy = %q, want queued", queued.DeliveryStatus)
	}

	var deliveredAgent, envelope string
	h.dispatchHumanAnswer = func(key, text string) (SendResult, error) {
		deliveredAgent, envelope = key, text
		return SendResult{Dispatched: true, AgentID: key, SessionID: key, TurnID: "turn-resumed"}, nil
	}
	h.mu.Lock()
	h.agents["agent-one"].Status = "idle"
	h.agents["agent-one"].CurrentTurnID = ""
	h.runtimes["agent-one"].activeTurn = nil
	h.mu.Unlock()
	delivered, ok := h.deliverAnsweredHumanRequest("agent-one")
	if !ok {
		t.Fatalf("delivery failed: %#v", delivered)
	}
	if deliveredAgent != "agent-one" || delivered.ResumedTurnID != "turn-resumed" || delivered.DeliveryStatus != "delivered" {
		t.Fatalf("delivered request = %#v, agent = %q", delivered, deliveredAgent)
	}
	for _, want := range []string{`request_id="` + request.ID + `"`, `source_turn_id="turn-source"`, "<answer><![CDATA[Use tomorrow morning.]]></answer>"} {
		if !strings.Contains(envelope, want) {
			t.Fatalf("response envelope missing %q:\n%s", want, envelope)
		}
	}

	reloaded := testHub(st)
	if err := reloaded.loadHumanRequests(); err != nil {
		t.Fatal(err)
	}
	stored, err := reloaded.GetHumanRequest(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Answer != "Use tomorrow morning." || stored.ResumedTurnID != "turn-resumed" || stored.DeliveryStatus != "delivered" {
		t.Fatalf("reloaded request = %#v", stored)
	}
}

func TestHumanRequestRejectsDuplicateAnswer(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent-one"] = &Agent{ID: "agent-one", Name: "one", Status: "running", CreatedAt: now(), UpdatedAt: now()}
	request, err := h.CreateHumanRequest(CreateHumanRequestParams{Agent: "one", Expectation: HumanRequestOptional, Question: "Any preference?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnswerHumanRequest(request.ID, AnswerHumanRequestParams{Answer: "No preference."}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnswerHumanRequest(request.ID, AnswerHumanRequestParams{Answer: "Second answer"}); err == nil {
		t.Fatal("duplicate answer succeeded")
	}
}
