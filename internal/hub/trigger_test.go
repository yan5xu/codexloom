package hub

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestTriggerObservationCheckpointsWithoutVersionChurn(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	trigger := &Trigger{
		ID: "trg-checkpoint", State: "armed", Version: 4,
		LastObservedAt: "2026-07-19T01:00:00Z", UpdatedAt: "2026-07-19T01:00:00Z",
	}
	h.triggers[trigger.ID] = trigger
	if err := st.SaveTriggers(h.triggers); err != nil {
		t.Fatal(err)
	}

	h.applyTriggerObservation(trigger.ID, TriggerObservation{ObservedAt: "2026-07-19T01:00:30Z"})
	if trigger.Version != 4 || trigger.UpdatedAt != "2026-07-19T01:00:00Z" || trigger.LastObservedAt != "2026-07-19T01:00:00Z" {
		t.Fatalf("early no-op observation mutated trigger: %#v", trigger)
	}

	h.applyTriggerObservation(trigger.ID, TriggerObservation{ObservedAt: "2026-07-19T01:01:00Z"})
	if trigger.Version != 4 || trigger.UpdatedAt != "2026-07-19T01:00:00Z" || trigger.LastObservedAt != "2026-07-19T01:01:00Z" {
		t.Fatalf("checkpoint changed semantic version fields: %#v", trigger)
	}
	loaded := map[string]*Trigger{}
	if err := st.LoadTriggers(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded[trigger.ID] == nil || loaded[trigger.ID].LastObservedAt != "2026-07-19T01:01:00Z" || loaded[trigger.ID].Version != 4 {
		t.Fatalf("persisted checkpoint = %#v", loaded[trigger.ID])
	}
}

func TestRepeatedTriggerErrorDoesNotChurnVersion(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	trigger := &Trigger{ID: "trg-error", State: "armed", Version: 2, UpdatedAt: "2026-07-19T01:00:00Z"}
	h.triggers[trigger.ID] = trigger
	h.recordTriggerError(trigger.ID, fmt.Errorf("provider unavailable"), false)
	version := trigger.Version
	updatedAt := trigger.UpdatedAt
	h.recordTriggerError(trigger.ID, fmt.Errorf("provider unavailable"), false)
	if trigger.Version != version || trigger.UpdatedAt != updatedAt {
		t.Fatalf("repeated error churned trigger: %#v", trigger)
	}
}

func TestResumeExpiredTriggerPersistsExpiredState(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	trigger := &Trigger{
		ID: "trg-expired", State: "paused", Version: 1,
		ExpiresAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), UpdatedAt: now(),
	}
	h.triggers[trigger.ID] = trigger
	if _, err := h.ResumeTrigger(trigger.ID); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("ResumeTrigger error = %v", err)
	}
	if trigger.State != "expired" {
		t.Fatalf("trigger state = %q, want expired", trigger.State)
	}
	loaded := map[string]*Trigger{}
	if err := st.LoadTriggers(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded[trigger.ID] == nil || loaded[trigger.ID].State != "expired" {
		t.Fatalf("persisted expired trigger = %#v", loaded[trigger.ID])
	}
}

func TestMatchedObservationDoesNotFireAfterTriggerExpires(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	trigger := &Trigger{
		ID: "trg-expired-observation", AgentID: "agent-1", Provider: "github",
		State: "armed", Version: 2, ExpiresAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
	}
	h.triggers[trigger.ID] = trigger
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}

	h.applyTriggerObservation(trigger.ID, TriggerObservation{
		Matched: true,
		Event: TriggerEvent{
			Event: "merged", EventKey: "github:pr:owner/repo#12:merged:abc",
			SubjectKey: "owner/repo#12", Summary: "Pull request merged.",
		},
	})
	if trigger.State != "expired" || len(h.commOrder) != 0 {
		t.Fatalf("expired trigger applied observation: trigger=%#v messages=%v", trigger, h.commOrder)
	}
	loaded := map[string]*Trigger{}
	if err := st.LoadTriggers(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded[trigger.ID] == nil || loaded[trigger.ID].State != "expired" {
		t.Fatalf("persisted trigger = %#v", loaded[trigger.ID])
	}
}

func TestNormalizeTriggerSubjectRejectsURLLikeOwnerAndRepo(t *testing.T) {
	for _, subject := range []map[string]string{
		{"owner": "https:", "repo": "repo", "number": "1"},
		{"owner": "owner", "repo": "nested/repo", "number": "1"},
	} {
		if _, err := normalizeTriggerSubject("github", "pull-request", subject); err == nil {
			t.Fatalf("accepted invalid subject %#v", subject)
		}
	}
}

func TestTriggerArmsAndCreatesOneDurableMessage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.triggers = map[string]*Trigger{}
	h.connections = map[string]*PlatformConnection{
		"conn-github": {ID: "conn-github", Provider: "github", Enabled: true, Status: "connected"},
	}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "running"}
	h.observeTrigger = func(_ context.Context, _ PlatformConnection, trigger Trigger) (TriggerObservation, error) {
		return TriggerObservation{ObservedAt: "2026-07-19T01:00:00Z", Event: TriggerEvent{SubjectKey: "owner/repo#12"}}, nil
	}

	trigger, err := h.CreateTrigger(TriggerParams{
		Agent: "lead", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "owner", "repo": "repo", "number": "12"},
		Conditions: []TriggerCondition{{Event: "merged"}}, ResumeInstruction: "Re-read the PR.",
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger = waitForTriggerState(t, h, trigger.ID, "armed")
	if trigger.State != "armed" || len(h.commOrder) != 0 {
		t.Fatalf("trigger=%#v messages=%v", trigger, h.commOrder)
	}

	h.observeTrigger = func(_ context.Context, _ PlatformConnection, trigger Trigger) (TriggerObservation, error) {
		return TriggerObservation{Matched: true, ObservedAt: "2026-07-19T01:01:00Z", Event: TriggerEvent{
			SubjectKey: "owner/repo#12", Event: "merged", EventKey: "github:pr:owner/repo#12:merged:abc",
			OccurredAt: "2026-07-19T01:00:59Z", Summary: "Pull request owner/repo#12 is merged.",
			Snapshot: map[string]any{"merged": true, "headSha": "abc"},
		}}, nil
	}
	h.PollTriggersNow()
	trigger, _ = h.GetTrigger(trigger.ID)
	if trigger.State != "triggered" || trigger.LastMessageID == "" || len(h.commOrder) != 1 {
		t.Fatalf("trigger=%#v messages=%v", trigger, h.commOrder)
	}
	message := h.comms[h.commOrder[0]]
	if message.TriggerID != trigger.ID || message.DeliveryStatus != "queued" || message.TriggerEvent == nil {
		t.Fatalf("message = %#v", message)
	}

	// Reconciliation of the same provider observation must not duplicate the occurrence.
	h.applyTriggerObservation(trigger.ID, TriggerObservation{Matched: true, Event: *message.TriggerEvent})
	if len(h.commOrder) != 1 {
		t.Fatalf("duplicate messages = %v", h.commOrder)
	}

	reloaded := map[string]*Trigger{}
	if err := st.LoadTriggers(&reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded[trigger.ID] == nil || reloaded[trigger.ID].State != "triggered" {
		t.Fatalf("persisted trigger = %#v", reloaded[trigger.ID])
	}
}

func TestCreateTriggerRejectsHeadChangedWithoutExpectedHead(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.triggers = map[string]*Trigger{}
	h.connections = map[string]*PlatformConnection{
		"conn-github": {ID: "conn-github", Provider: "github", Enabled: true, Status: "connected"},
	}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}

	_, err = h.CreateTrigger(TriggerParams{
		Agent: "lead", ConnectionID: "conn-github", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "owner", "repo": "repo", "number": "12"},
		Conditions: []TriggerCondition{{Event: "head-changed"}}, ResumeInstruction: "Re-read current HEAD.",
	})
	if err == nil || !strings.Contains(err.Error(), "expectedHead") {
		t.Fatalf("CreateTrigger error = %v, want expectedHead validation", err)
	}
}

func TestCreateTriggerSelectsGitHubConnectionByResourceOwner(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.triggers = map[string]*Trigger{}
	h.connections = map[string]*PlatformConnection{
		"conn-personal": {ID: "conn-personal", Provider: "github", AccountRef: "yan5xu", ScopeRef: "yan5xu", Enabled: true, Status: "connected"},
		"conn-parall":   {ID: "conn-parall", Provider: "github", AccountRef: "yan5xu", ScopeRef: "parall-hq", Enabled: true, Status: "connected"},
	}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}
	observedWith := ""
	h.observeTrigger = func(_ context.Context, connection PlatformConnection, _ Trigger) (TriggerObservation, error) {
		observedWith = connection.ID
		return TriggerObservation{ObservedAt: "2026-07-19T01:00:00Z"}, nil
	}

	trigger, err := h.CreateTrigger(TriggerParams{
		Agent: "lead", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "parall-hq", "repo": "parall-mono", "number": "1970"},
		Conditions: []TriggerCondition{{Event: "merged"}}, ResumeInstruction: "Re-read the pull request.",
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger = waitForTriggerState(t, h, trigger.ID, "armed")
	if trigger.ConnectionID != "conn-parall" || observedWith != "conn-parall" {
		t.Fatalf("trigger connection = %q, observed with = %q", trigger.ConnectionID, observedWith)
	}
}

func TestCreateTriggerRejectsUncoveredGitHubResourceOwner(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.triggers = map[string]*Trigger{}
	h.connections = map[string]*PlatformConnection{
		"conn-personal": {ID: "conn-personal", Provider: "github", AccountRef: "yan5xu", ScopeRef: "yan5xu", Enabled: true, Status: "connected"},
	}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}

	_, err = h.CreateTrigger(TriggerParams{
		Agent: "lead", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "parall-hq", "repo": "parall-mono", "number": "1970"},
		Conditions: []TriggerCondition{{Event: "merged"}}, ResumeInstruction: "Re-read the pull request.",
	})
	if err == nil || !strings.Contains(err.Error(), "no enabled GitHub connection covers resource owner parall-hq") {
		t.Fatalf("CreateTrigger error = %v", err)
	}
}

func TestResumeTriggerReroutesLegacyGitHubConnectionToExactOwner(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.triggers = map[string]*Trigger{}
	h.connections = map[string]*PlatformConnection{
		"conn-legacy": {ID: "conn-legacy", Provider: "github", AccountRef: "yan5xu", Enabled: true, Status: "connected"},
	}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}
	h.observeTrigger = func(_ context.Context, _ PlatformConnection, _ Trigger) (TriggerObservation, error) {
		return TriggerObservation{Permanent: true}, fmt.Errorf("legacy token cannot read repository")
	}
	trigger, err := h.CreateTrigger(TriggerParams{
		Agent: "lead", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "parall-hq", "repo": "parall-mono", "number": "1970"},
		Conditions: []TriggerCondition{{Event: "merged"}}, ResumeInstruction: "Re-read the pull request.",
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger = waitForTriggerState(t, h, trigger.ID, "failed")
	if trigger.State != "failed" || trigger.ConnectionID != "conn-legacy" {
		t.Fatalf("initial trigger = %#v", trigger)
	}
	h.mu.Lock()
	h.connections["conn-parall"] = &PlatformConnection{ID: "conn-parall", Provider: "github", AccountRef: "yan5xu", ScopeRef: "parall-hq", Enabled: true, Status: "connected"}
	h.mu.Unlock()
	observedWith := make(chan string, 1)
	h.observeTrigger = func(_ context.Context, connection PlatformConnection, _ Trigger) (TriggerObservation, error) {
		observedWith <- connection.ID
		return TriggerObservation{ObservedAt: "2026-07-19T01:00:00Z"}, nil
	}
	trigger, err = h.ResumeTrigger(trigger.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case connectionID := <-observedWith:
		trigger, _ = h.GetTrigger(trigger.ID)
		if trigger.State != "armed" || trigger.ConnectionID != "conn-parall" || connectionID != "conn-parall" {
			t.Fatalf("resumed trigger = %#v, observed with = %q", trigger, connectionID)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed Trigger was not observed")
	}
}

func TestCreateTriggerReturnsBeforeInitialObservationCompletes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.connections["conn-github"] = &PlatformConnection{ID: "conn-github", Provider: "github", Enabled: true, Status: "connected"}
	h.agents["agent-1"] = &Agent{ID: "agent-1", Name: "lead", ThreadID: "thread-1", Status: "idle"}
	started := make(chan struct{})
	release := make(chan struct{})
	h.observeTrigger = func(_ context.Context, _ PlatformConnection, _ Trigger) (TriggerObservation, error) {
		close(started)
		<-release
		return TriggerObservation{ObservedAt: "2026-07-19T01:00:00Z"}, nil
	}

	trigger, err := h.CreateTrigger(TriggerParams{
		Agent: "lead", ConnectionID: "conn-github", Provider: "github", ResourceKind: "pull-request",
		Subject:    map[string]string{"owner": "owner", "repo": "repo", "number": "12"},
		Conditions: []TriggerCondition{{Event: "merged"}}, ResumeInstruction: "Re-read the PR.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if trigger.State != "pending" {
		t.Fatalf("created trigger state = %q, want pending while observer is blocked", trigger.State)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial observer did not start")
	}
	close(release)
	waitForTriggerState(t, h, trigger.ID, "armed")
}

func TestPollTriggersUsesBoundedConcurrency(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.connections["conn-github"] = &PlatformConnection{ID: "conn-github", Provider: "github", Enabled: true, Status: "connected"}
	for index := 0; index < triggerPollConcurrency+2; index++ {
		id := fmt.Sprintf("trg-%d", index)
		h.triggers[id] = &Trigger{ID: id, ConnectionID: "conn-github", Provider: "github", State: "armed", Version: 1}
	}
	started := make(chan struct{}, len(h.triggers))
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	h.observeTrigger = func(_ context.Context, _ PlatformConnection, _ Trigger) (TriggerObservation, error) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return TriggerObservation{ObservedAt: "2026-07-19T01:00:00Z"}, nil
	}
	done := make(chan struct{})
	go func() {
		h.PollTriggersNow()
		close(done)
	}()
	for index := 0; index < triggerPollConcurrency; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded Trigger workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("Trigger poll exceeded its concurrency bound")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Trigger poll did not complete")
	}
	if maximum.Load() != triggerPollConcurrency {
		t.Fatalf("maximum concurrent observers = %d, want %d", maximum.Load(), triggerPollConcurrency)
	}
}

func TestPollTriggersStopsDispatchAndCancelsProviderObservation(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.stop = make(chan struct{})
	h.connections["conn-github"] = &PlatformConnection{ID: "conn-github", Provider: "github", Enabled: true, Status: "connected"}
	for index := 0; index < triggerPollConcurrency*3; index++ {
		id := fmt.Sprintf("trg-stop-%d", index)
		h.triggers[id] = &Trigger{ID: id, ConnectionID: "conn-github", Provider: "github", State: "armed", Version: 1}
	}
	started := make(chan struct{}, triggerPollConcurrency)
	h.observeTrigger = func(ctx context.Context, _ PlatformConnection, _ Trigger) (TriggerObservation, error) {
		started <- struct{}{}
		<-ctx.Done()
		return TriggerObservation{}, ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		h.PollTriggersNow()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Trigger observation did not start")
	}
	close(h.stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Trigger poll did not stop after Hub shutdown signal")
	}
}

func waitForTriggerState(t *testing.T, h *Hub, id, state string) Trigger {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		trigger, err := h.GetTrigger(id)
		if err != nil {
			t.Fatal(err)
		}
		if trigger.State == state {
			return trigger
		}
		time.Sleep(5 * time.Millisecond)
	}
	trigger, _ := h.GetTrigger(id)
	t.Fatalf("Trigger %s state = %q, want %q", id, trigger.State, state)
	return Trigger{}
}

func TestShouldCheckpointTriggerConnection(t *testing.T) {
	connection := &PlatformConnection{Status: "connected", LastHeartbeatAt: "2026-07-19T01:00:00Z"}
	if shouldCheckpointTriggerConnection(connection, "2026-07-19T01:00:30Z") {
		t.Fatal("checkpointed an unchanged connection before one minute")
	}
	if !shouldCheckpointTriggerConnection(connection, "2026-07-19T01:01:00Z") {
		t.Fatal("did not checkpoint an unchanged connection after one minute")
	}
	connection.Status = "degraded"
	if !shouldCheckpointTriggerConnection(connection, "2026-07-19T01:00:01Z") {
		t.Fatal("did not checkpoint a connection status recovery")
	}
}

func TestTriggerEnvelopePreservesCausalTimesAndRevalidation(t *testing.T) {
	message := &AgentMessage{
		ID: "msg-1", TriggerID: "trg-1", TriggerEvent: &TriggerEvent{
			Provider: "github", ConnectionID: "conn-1", ResourceKind: "pull-request",
			SubjectKey: "owner/repo#12", Event: "head-changed", EventKey: "event-1",
			OccurredAt: "2026-07-19T01:00:00Z", ObservedAt: "2026-07-19T01:00:05Z",
			Summary: "HEAD changed.", ResumeInstruction: "Re-read GitHub.", Snapshot: map[string]any{"headSha": "def"},
			Work: TriggerWorkAnchor{AgentID: "agent-1", ThreadIDAtCreation: "thread-1", SourceTurnID: "turn-1", GoalCreatedAt: 42},
		},
	}
	envelope := formatTriggerEnvelopeAt(message, "2026-07-19T01:00:06Z")
	for _, fragment := range []string{
		`<external_trigger version="1"`, `trigger_id="trg-1"`, `occurred_at="2026-07-19T01:00:00Z"`,
		`observed_at="2026-07-19T01:00:05Z"`, `provider="github"`, `key="owner/repo#12"`,
		`source_turn_id="turn-1"`, `goal_created_at="42"`,
		`<![CDATA[Re-read GitHub.]]>`, `not as proof`, `"headSha":"def"`,
	} {
		if !strings.Contains(envelope, fragment) {
			t.Fatalf("envelope missing %q:\n%s", fragment, envelope)
		}
	}
}

func TestTriggerReconcilesDurableMessageAfterRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trigger := &Trigger{
		ID: "trg-recover", AgentID: "agent-1", Agent: "lead", Provider: "github",
		ConnectionID: "conn-1", ResourceKind: "pull-request", State: "armed", Version: 1,
		CreatedAt: "2026-07-19T01:00:00Z", UpdatedAt: "2026-07-19T01:00:00Z",
	}
	if err := st.SaveTriggers(map[string]*Trigger{trigger.ID: trigger}); err != nil {
		t.Fatal(err)
	}
	message := AgentMessage{
		ID: "msg-recover", TriggerID: trigger.ID, FromAgentID: triggerAgentID, ToAgentID: "agent-1",
		From: triggerIdentity, To: "lead", Response: "none", Status: "closed", DeliveryStatus: "queued",
		TriggerEvent: &TriggerEvent{EventKey: "github:event:1", ObservedAt: "2026-07-19T01:01:00Z"},
		CreatedAt:    "2026-07-19T01:01:00Z", UpdatedAt: "2026-07-19T01:01:00Z",
	}
	if err := st.AppendComm(commRecord{Message: message}); err != nil {
		t.Fatal(err)
	}
	reader, err := st.OpenReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	h, err := OpenWithOptions(reader, OpenOptions{Passive: true})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := h.GetTrigger(trigger.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != "triggered" || recovered.LastMessageID != message.ID || recovered.LastEventKey != "github:event:1" {
		t.Fatalf("recovered Trigger = %#v", recovered)
	}
}
