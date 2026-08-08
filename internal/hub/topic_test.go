package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestSummarizeTopicTextPreservesUnicodeBoundaries(t *testing.T) {
	input := strings.Repeat("阶段验收", 50)
	summary := summarizeTopicText(input)
	if !utf8.ValidString(summary) {
		t.Fatalf("summary is not valid UTF-8: %q", summary)
	}
	if utf8.RuneCountInString(summary) != 180 || !strings.HasSuffix(summary, "...") {
		t.Fatalf("summary rune count=%d suffix=%q", utf8.RuneCountInString(summary), summary[len(summary)-3:])
	}
}

func TestNormalizeTopicsCleansLegacyReplacementCharacters(t *testing.T) {
	h := topicTestHub(t)
	h.topics["legacy"] = &Topic{
		ID: "legacy", Status: TopicStatusActive, Version: 1,
		CurrentBrief: TopicBrief{Version: 1},
		Events:       []TopicEvent{{Seq: 1, Summary: "staging 验��..."}},
	}
	if !h.normalizeTopicsLocked() {
		t.Fatal("expected legacy Topic normalization to report a change")
	}
	if got := h.topics["legacy"].Events[0].Summary; got != "staging 验..." {
		t.Fatalf("cleaned summary = %q", got)
	}
}

func TestNormalizeTopicsCleansAndPersistsAllDisplayText(t *testing.T) {
	h := topicTestHub(t)
	h.topics["legacy"] = &Topic{
		ID: "legacy", Title: "验��主题", Purpose: "目��的", Status: TopicStatusActive, Version: 1,
		CurrentBrief: TopicBrief{Version: 1, Summary: "当��前", Evidence: []TopicRef{{Label: "证��据"}}},
		BriefHistory: []TopicBrief{{Version: 1, CurrentState: "历��史"}},
		Participants: []TopicParticipant{{Agent: "参��与者", Responsibility: "职��责"}},
		Links:        []TopicLink{{Label: "链��接"}},
	}
	if !h.normalizeTopicsLocked() {
		t.Fatal("expected normalization to report a change")
	}
	if err := h.persistTopicsLocked(); err != nil {
		t.Fatal(err)
	}

	reloaded := map[string]*Topic{}
	if err := h.st.LoadTopics(&reloaded); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(reloaded["legacy"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "�") {
		t.Fatalf("persisted Topic still contains replacement characters: %s", data)
	}
}

func TestTopicViewUsesEmptyArraysForEmptyCollections(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.mu.Lock()
	h.topics[topic.ID].Participants = nil
	h.mu.Unlock()

	view, err := h.GetTopic(topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Participants == nil || view.ActiveTurns == nil {
		t.Fatalf("Topic collections must be arrays: participants=%#v activeTurns=%#v", view.Participants, view.ActiveTurns)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	if !strings.Contains(encoded, `"participants":[]`) || !strings.Contains(encoded, `"activeTurns":[]`) {
		t.Fatalf("Topic JSON must expose empty arrays: %s", encoded)
	}
}

func topicTestHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.topics = map[string]*Topic{}
	h.agents["lead"] = &Agent{ID: "lead", Name: "parall-dev-lead", ThreadID: "thread-lead", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.agents["edge"] = &Agent{ID: "edge", Name: "parall-edge-dev", ThreadID: "thread-edge", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	h.agents["other"] = &Agent{ID: "other", Name: "other", ThreadID: "thread-other", Status: "idle", CreatedAt: now(), UpdatedAt: now()}
	return h
}

func createClipTopic(t *testing.T, h *Hub) TopicView {
	t.Helper()
	view, err := h.CreateTopic(CreateTopicParams{
		Title: "Parall Clip 0.2.0", Purpose: "Finish the shared staging delivery.", CompletionBoundary: "Merged candidate passes shared staging smoke.",
		ResponsibleAgent: "parall-dev-lead", CreatedBy: "owner",
		Participants: []TopicParticipantParams{{Agent: "parall-edge-dev", Responsibility: "Own packaged client smoke."}},
		InitialBrief: TopicBriefDraft{Summary: "Candidate is frozen.", CurrentState: "Waiting for review.", NextStep: "Re-check the current PR head."},
	})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestTopicPersistsVersionedBriefAndOwnerAttention(t *testing.T) {
	h := topicTestHub(t)
	created := createClipTopic(t, h)
	if created.Version != 1 || created.CurrentBrief.Version != 1 || len(created.Participants) != 1 {
		t.Fatalf("created Topic = %#v", created)
	}
	waiting := TopicWaitingOn{Kind: "trigger", RefID: "trg-1970", Summary: "Wait for #1970 merge.", ResumeAction: "Re-read GitHub and main."}
	updated, err := h.UpdateTopic(created.ID, UpdateTopicParams{
		Actor: "parall-dev-lead", ExpectedVersion: 1, Status: stringPtr(TopicStatusWaiting), WaitingOn: &waiting,
		Brief: &TopicBriefDraft{Summary: "Frozen candidate is waiting on merge.", CurrentState: "No local process must remain alive.", NextStep: "Revalidate the merge and downstream head."}, PublishResult: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.CurrentBrief.Version != 2 || !updated.ResultsReady || updated.WaitingOn == nil {
		t.Fatalf("updated Topic = %#v", updated)
	}
	if _, err := h.UpdateTopic(created.ID, UpdateTopicParams{Actor: "parall-dev-lead", ExpectedVersion: 1, Brief: &TopicBriefDraft{Summary: "stale"}}); err == nil || !strings.Contains(err.Error(), "version changed") {
		t.Fatalf("stale update error = %v", err)
	}
	if _, err := h.UpdateTopic(created.ID, UpdateTopicParams{Actor: "owner", ExpectedVersion: updated.Version, Brief: &TopicBriefDraft{Summary: "Owner-authored result"}, PublishResult: true}); err == nil || !strings.Contains(err.Error(), "responsible Agent") {
		t.Fatalf("Owner result publish error = %v", err)
	}
	if _, err := h.UpdateTopic(created.ID, UpdateTopicParams{Actor: "owner", ExpectedVersion: updated.Version, Brief: &TopicBriefDraft{Summary: "Owner bypass"}}); err == nil || !strings.Contains(err.Error(), "send Topic") {
		t.Fatalf("Owner brief bypass error = %v", err)
	}
	if _, err := h.AddTopicParticipant(created.ID, "owner", TopicParticipantParams{Agent: "other", Responsibility: "Bypass the Responsible"}); err == nil || !strings.Contains(err.Error(), "responsible Agent") {
		t.Fatalf("Owner participant bypass error = %v", err)
	}
	if _, err := h.AddTopicParticipant(created.ID, "parall-dev-lead", TopicParticipantParams{Agent: "other", Responsibility: "Provide a scoped review"}); err != nil {
		t.Fatalf("Responsible participant add: %v", err)
	}
	read, err := h.MarkTopicRead(created.ID)
	if err != nil || read.ResultsReady {
		t.Fatalf("read Topic = %#v err=%v", read, err)
	}

	reader, err := h.st.OpenReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reloaded, err := OpenWithOptions(reader, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reloaded.GetTopic(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentBrief.Summary != "Frozen candidate is waiting on merge." || stored.WaitingOn.RefID != "trg-1970" || stored.OwnerSeenBriefVersion != 2 {
		t.Fatalf("reloaded Topic = %#v", stored)
	}
}

func TestTopicCausalFieldsInheritIntoMessageAndHumanRequest(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.agents["edge"].Status = "running"
	h.agents["lead"].Status = "running"
	h.runtimes["edge"] = &runtime{activeTurn: &turnState{turnID: "turn-edge", topicID: topic.ID, task: "Run packaged smoke", startedAt: time.Now(), stopWatchdog: make(chan struct{})}}

	message, err := h.SendAgentMessage(CommParams{From: "parall-edge-dev", To: "parall-dev-lead", Subject: "Smoke result", Body: "Candidate passed.", Response: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if message.Message.TopicID != topic.ID || message.Message.SourceTurnID != "turn-edge" {
		t.Fatalf("message = %#v", message.Message)
	}
	envelope, _ := h.formatAgentEnvelopeForDelivery(message.Message)
	if !strings.Contains(envelope, `<loom_topic_context`) || !strings.Contains(envelope, `topic_id="`+topic.ID+`"`) || !strings.Contains(envelope, "Maintain the shared brief") {
		t.Fatalf("Topic envelope missing context:\n%s", envelope)
	}

	request, err := h.CreateHumanRequest(CreateHumanRequestParams{Agent: "parall-edge-dev", Question: "May I replace the candidate?"})
	if err != nil {
		t.Fatal(err)
	}
	if request.TopicID != topic.ID || request.SourceTurnID != "turn-edge" {
		t.Fatalf("human request = %#v", request)
	}
	if _, err := h.SendAgentMessage(CommParams{From: "parall-edge-dev", To: "other", Subject: "Wrong route", Body: "This is outside the Topic.", Response: "none"}); err == nil || !strings.Contains(err.Error(), "not part of Topic") {
		t.Fatalf("outside Topic message error = %v", err)
	}
	view, _ := h.GetTopic(topic.ID)
	var messageLinked, requestLinked bool
	for _, event := range view.Events {
		messageLinked = messageLinked || event.Ref != nil && event.Ref.Type == "message" && event.Ref.ID == message.Message.ID
		requestLinked = requestLinked || event.Ref != nil && event.Ref.Type == "human_request" && event.Ref.ID == request.ID
	}
	if !messageLinked || !requestLinked {
		t.Fatalf("Topic causal events missing: %#v", view.Events)
	}
}

func TestSendTopicInputUsesConciseDisplayTaskAndFullModelContext(t *testing.T) {
	logPath := installFakeSharedCodexHost(t)
	h := topicTestHub(t)
	defer h.Shutdown()
	h.agents["lead"].ThreadID = "thr-stale"
	h.agents["lead"].Cwd = "/tmp/stale"
	topic := createClipTopic(t, h)

	ownerInput := "Verify the Topic context without changing any external state."
	result, err := h.SendTopicInput(topic.ID, TopicInputParams{Text: ownerInput, TimeoutSec: 30})
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID == "" {
		t.Fatal("Topic input did not start a Turn")
	}
	if got := h.agents["lead"].CurrentTask; got != ownerInput {
		t.Fatalf("display task = %q, want %q", got, ownerInput)
	}

	turn := lastRequestParams(t, logPath, "turn/start")
	modelInput := fmt.Sprint(turn["input"])
	if !strings.Contains(modelInput, `<loom_topic_context`) || !strings.Contains(modelInput, ownerInput) || !strings.Contains(modelInput, topic.ID) {
		t.Fatalf("model input lost Topic context: %s", modelInput)
	}
	events, err := h.st.ReadEvents("lead", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawConciseUserMessage, sawConciseTurn bool
	for _, event := range events {
		switch event.Type {
		case "loom/user-message":
			sawConciseUserMessage = strings.Contains(string(event.Data), ownerInput) && !strings.Contains(string(event.Data), "loom_topic_context")
		case "loom/turn-started":
			sawConciseTurn = strings.Contains(string(event.Data), ownerInput) && !strings.Contains(string(event.Data), "loom_topic_context")
		}
	}
	if !sawConciseUserMessage || !sawConciseTurn {
		t.Fatalf("concise Topic events user=%v turn=%v events=%#v", sawConciseUserMessage, sawConciseTurn, events)
	}
}

func TestTopicOwnerSteerTargetsExactParticipantTurnAndRecordsIntervention(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.agents["edge"].Status = "running"
	h.agents["lead"].Status = "running"
	h.runtimes["edge"] = &runtime{activeTurn: &turnState{turnID: "turn-edge", topicID: topic.ID, task: "Run packaged smoke", startedAt: time.Now(), stopWatchdog: make(chan struct{})}}
	var input string
	h.steerTurn = func(threadID, turnID, value string, timeout time.Duration) (string, error) {
		if threadID != "thread-edge" || turnID != "turn-edge" {
			t.Fatalf("steer target = %s %s", threadID, turnID)
		}
		input = value
		return turnID, nil
	}
	result, err := h.InterveneTopicTurn("  "+topic.ID+"  ", TopicInterventionParams{Agent: "parall-edge-dev", Action: "steer", Text: "Test the current frozen SHA, not the previous candidate.", Reason: "Candidate evidence drifted."})
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicID != topic.ID || result.TurnID != "turn-edge" || result.Event.Type != "owner_intervention" || !strings.Contains(input, `<owner_topic_intervention`) {
		t.Fatalf("intervention = %#v input=%s", result, input)
	}
	view, _ := h.GetTopic(topic.ID)
	foundIntervention := false
	for _, event := range view.Events {
		foundIntervention = foundIntervention || event.Type == "owner_intervention" && event.Ref != nil && event.Ref.ID == "turn-edge"
	}
	if !foundIntervention {
		t.Fatalf("Topic events = %#v", view.Events)
	}
	if _, err := h.InterveneTopicTurn(topic.ID, TopicInterventionParams{Agent: "other", Action: "steer", Text: "wrong"}); err == nil {
		t.Fatal("non-participant intervention succeeded")
	}
}

func TestTopicClearWaitingReactivatesTopic(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	waiting, err := h.UpdateTopic(topic.ID, UpdateTopicParams{Actor: "parall-dev-lead", WaitingOn: &TopicWaitingOn{Kind: "github-pr", Summary: "Waiting for merge"}})
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != TopicStatusWaiting || waiting.WaitingOn == nil {
		t.Fatalf("waiting Topic = %#v", waiting)
	}
	active, err := h.UpdateTopic(topic.ID, UpdateTopicParams{Actor: "parall-dev-lead", ClearWaiting: true})
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != TopicStatusActive || active.WaitingOn != nil {
		t.Fatalf("cleared Topic = %#v", active)
	}
}

func TestTopicContextDeliversLargeDeltaWithoutSkippingEvents(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.mu.Lock()
	for i := 0; i < 12; i++ {
		h.appendTopicEventMemoryLocked(h.topics[topic.ID], TopicEvent{Type: "test", Summary: "event", CreatedAt: now()})
	}
	h.mu.Unlock()

	first, cursor, err := h.topicContextEnvelope(topic.ID, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(first, `<event seq=`) != 8 || cursor >= h.topics[topic.ID].NextEventSeq {
		t.Fatalf("first delta count=%d cursor=%d latest=%d", strings.Count(first, `<event seq=`), cursor, h.topics[topic.ID].NextEventSeq)
	}
	h.markTopicContextDelivered(topic.ID, "edge", cursor)
	second, nextCursor, err := h.topicContextEnvelope(topic.ID, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(second, `<event seq=`) == 0 || nextCursor != h.topics[topic.ID].NextEventSeq {
		t.Fatalf("second delta count=%d cursor=%d latest=%d", strings.Count(second, `<event seq=`), nextCursor, h.topics[topic.ID].NextEventSeq)
	}
}

func TestTopicContextIncludesBoundedExplicitEvidence(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	for index := 0; index < 10; index++ {
		if _, err := h.LinkTopic(topic.ID, "parall-dev-lead", TopicLink{Type: "artifact", ID: fmt.Sprintf("art-%d", index), Label: fmt.Sprintf("Evidence %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	context, _, err := h.topicContextEnvelope(topic.ID, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(context, `<link type=`) != 8 || strings.Contains(context, `id="art-0"`) || !strings.Contains(context, `id="art-9"`) {
		t.Fatalf("bounded key links missing from context:\n%s", context)
	}
}

func TestTopicArtifactsResolvePublishedLinkedArtifactsAcrossAgents(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	published, err := h.StageThreadArtifact("parall-edge-dev", "evidence.txt", "text/plain", strings.NewReader("topic evidence"))
	if err != nil {
		t.Fatal(err)
	}
	published, err = h.PublishThreadArtifact("parall-edge-dev", published.ID)
	if err != nil {
		t.Fatal(err)
	}
	unpublished, err := h.StageThreadArtifact("parall-edge-dev", "draft.txt", "text/plain", strings.NewReader("not published"))
	if err != nil {
		t.Fatal(err)
	}
	unlinked, err := h.StageThreadArtifact("parall-edge-dev", "private.txt", "text/plain", strings.NewReader("not linked"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.PublishThreadArtifact("parall-edge-dev", unlinked.ID); err != nil {
		t.Fatal(err)
	}
	for _, artifactID := range []string{published.ID, unpublished.ID} {
		if _, err := h.LinkTopic(topic.ID, "parall-dev-lead", TopicLink{Type: "artifact", ID: artifactID, Label: "Evidence"}); err != nil {
			t.Fatal(err)
		}
	}

	artifacts, err := h.TopicArtifacts(topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != published.ID || artifacts[0].AgentID != "edge" {
		t.Fatalf("Topic artifacts = %#v", artifacts)
	}
	opened, file, err := h.OpenTopicArtifact(topic.ID, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ID != published.ID || !bytes.Equal(data, []byte("topic evidence")) {
		t.Fatalf("opened artifact = %#v data=%q", opened, data)
	}
	if _, _, err := h.OpenTopicArtifact(topic.ID, unlinked.ID); err == nil {
		t.Fatal("unlinked artifact must not be readable through a Topic")
	}
}

func TestActiveTopicPreventsAgentArchive(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	if _, err := h.ArchiveAgent("parall-edge-dev"); err == nil || !strings.Contains(err.Error(), topic.ID) {
		t.Fatalf("archive active Topic participant error = %v", err)
	}
	if _, err := h.UpdateTopic(topic.ID, UpdateTopicParams{Actor: "owner", Status: stringPtr(TopicStatusArchived)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ArchiveAgent("parall-edge-dev"); err != nil {
		t.Fatalf("archive after Topic archive: %v", err)
	}
}

func TestTopicHumanAnswerResumesWithLatestContext(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.agents["edge"].Status = "running"
	h.runtimes["edge"] = &runtime{activeTurn: &turnState{turnID: "turn-edge", topicID: topic.ID, task: "Wait for Owner", startedAt: time.Now(), stopWatchdog: make(chan struct{})}}
	request, err := h.CreateHumanRequest(CreateHumanRequestParams{Agent: "parall-edge-dev", Question: "Use the replacement candidate?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.UpdateTopic(topic.ID, UpdateTopicParams{Actor: "parall-dev-lead", Brief: &TopicBriefDraft{Summary: "The replacement candidate is now authoritative."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnswerHumanRequest(request.ID, AnswerHumanRequestParams{Answer: "Yes, use the replacement."}); err != nil {
		t.Fatal(err)
	}
	var envelope string
	h.dispatchHumanAnswer = func(_ string, text string) (SendResult, error) {
		envelope = text
		return SendResult{Dispatched: true, AgentID: "edge", TurnID: "turn-resumed"}, nil
	}
	h.mu.Lock()
	h.agents["edge"].Status = "idle"
	h.runtimes["edge"].activeTurn = nil
	h.mu.Unlock()
	if _, ok := h.deliverAnsweredHumanRequest("edge"); !ok {
		t.Fatal("Topic human answer was not delivered")
	}
	if !strings.Contains(envelope, `<loom_topic_context`) || !strings.Contains(envelope, "The replacement candidate is now authoritative.") || !strings.Contains(envelope, `<human_input_response`) {
		t.Fatalf("Topic answer context missing:\n%s", envelope)
	}
}

func TestTopicHumanAnswerFallsBackWhenParticipantWasRemoved(t *testing.T) {
	h := topicTestHub(t)
	topic := createClipTopic(t, h)
	h.agents["edge"].Status = "running"
	h.runtimes["edge"] = &runtime{activeTurn: &turnState{turnID: "turn-edge", topicID: topic.ID, task: "Wait for Owner", startedAt: time.Now(), stopWatchdog: make(chan struct{})}}
	request, err := h.CreateHumanRequest(CreateHumanRequestParams{Agent: "parall-edge-dev", Question: "Use the replacement candidate?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnswerHumanRequest(request.ID, AnswerHumanRequestParams{Answer: "Yes."}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.agents["edge"].Status = "idle"
	h.runtimes["edge"].activeTurn = nil
	h.mu.Unlock()
	if _, err := h.RemoveTopicParticipant(topic.ID, "parall-edge-dev", "parall-dev-lead"); err != nil {
		t.Fatal(err)
	}
	var delivered string
	h.dispatchHumanAnswer = func(_ string, text string) (SendResult, error) {
		delivered = text
		return SendResult{Dispatched: true, AgentID: "edge", TurnID: "turn-resumed"}, nil
	}
	if resumed, ok := h.deliverAnsweredHumanRequest("edge"); !ok || resumed.DeliveryStatus != "delivered" {
		t.Fatalf("fallback delivery = %#v, ok=%v", resumed, ok)
	}
	if !strings.Contains(delivered, `<human_input_response`) || strings.Contains(delivered, `<loom_topic_context`) {
		t.Fatalf("fallback answer envelope = %s", delivered)
	}
}
