package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestNextCronAfter(t *testing.T) {
	after := time.Date(2026, 7, 9, 8, 59, 30, 0, time.UTC)
	next, err := nextCronAfter("0 9 * * *", "UTC", after)
	if err != nil {
		t.Fatalf("nextCronAfter: %v", err)
	}
	want := time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestCreateScheduleComputesNextRun(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent"] = &Agent{ID: "sess_agent", Name: "agent", Status: "idle"}

	s, err := h.CreateSchedule(ScheduleParams{
		Name:     "daily",
		To:       "agent",
		Subject:  "Daily check",
		Body:     "Check the repo.",
		Response: "required",
		Cron:     "0 9 * * *",
		Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if s.ID == "" || s.NextRunAt == "" || !s.Enabled {
		t.Fatalf("schedule = %#v, want id, next run and enabled", s)
	}
	if s.Response != "required" {
		t.Fatalf("response = %q, want required", s.Response)
	}
}

func TestReplyToSchedulerMessageRecordsAnswerWithoutSessionDelivery(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["sess_worker"] = &Agent{ID: "sess_worker", Name: "worker", Status: "idle"}
	orig := &AgentMessage{
		ID:             "msg_sched",
		From:           schedulerIdentity,
		To:             "worker",
		Subject:        "Scheduled check",
		Body:           "Do it",
		Response:       "required",
		Status:         "open",
		DeliveryStatus: "delivered",
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	h.comms[orig.ID] = orig
	h.commOrder = append(h.commOrder, orig.ID)

	result, err := h.SendAgentMessage(CommParams{
		From:    "worker",
		ReplyTo: orig.ID,
		Body:    "Done",
	})
	if err != nil {
		t.Fatalf("SendAgentMessage reply: %v", err)
	}
	if result.Message == nil {
		t.Fatal("reply message is nil")
	}
	if result.Message.To != schedulerIdentity || result.Message.DeliveryStatus != "delivered" {
		t.Fatalf("reply = %#v, want delivered to scheduler", result.Message)
	}
	if h.comms[orig.ID].Status != "answered" {
		t.Fatalf("orig status = %q, want answered", h.comms[orig.ID].Status)
	}
}

func TestDueScheduleCreatesOneDurableOccurrenceMessage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent"] = &Agent{ID: "agent", Name: "worker", Status: "idle"}
	dueAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	h.schedules["sched-1"] = &Schedule{
		ID: "sched-1", Name: "once", To: "worker", Subject: "Check", Body: "Do it",
		Response: "required", At: dueAt, Timezone: "UTC", Enabled: true,
		NextRunAt: dueAt, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := st.SaveSchedules(h.schedules); err != nil {
		t.Fatal(err)
	}

	targets := h.collectDueSchedules(time.Now().UTC())
	if len(targets) != 1 || targets[0] != "agent" {
		t.Fatalf("targets = %v", targets)
	}
	if len(h.commOrder) != 1 {
		t.Fatalf("messages = %v", h.commOrder)
	}
	message := h.comms[h.commOrder[0]]
	if message.ScheduleID != "sched-1" || message.ScheduledAt != dueAt || message.DeliveryStatus != "queued" {
		t.Fatalf("scheduled message = %#v", message)
	}
	if h.schedules["sched-1"].Enabled || h.schedules["sched-1"].NextRunAt != "" || h.schedules["sched-1"].LastMessageID != message.ID {
		t.Fatalf("advanced schedule = %#v", h.schedules["sched-1"])
	}

	// Simulate a crash after the occurrence message commit but before the
	// schedule position commit. Reprocessing the same scheduled_at reuses it.
	h.schedules["sched-1"].Enabled = true
	h.schedules["sched-1"].NextRunAt = dueAt
	targets = h.collectDueSchedules(time.Now().UTC())
	if len(targets) != 1 || len(h.commOrder) != 1 || h.schedules["sched-1"].LastMessageID != message.ID {
		t.Fatalf("recovered occurrence duplicated: targets=%v messages=%v schedule=%#v", targets, h.commOrder, h.schedules["sched-1"])
	}
}

func TestCreateScheduleRollsBackWhenPersistenceFails(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.agents["agent"] = &Agent{ID: "agent", Name: "worker", Status: "idle"}
	if err := os.Mkdir(filepath.Join(st.Dir(), "schedules.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = h.CreateSchedule(ScheduleParams{
		Name: "broken", To: "worker", Subject: "Check", Body: "Do it",
		At: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err == nil {
		t.Fatal("CreateSchedule succeeded when durable write failed")
	}
	if len(h.schedules) != 0 {
		t.Fatalf("failed schedule was published in memory: %#v", h.schedules)
	}
}

func TestRecomputeScheduleRollsBackWhenPersistenceFails(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	h.schedules["sched-1"] = &Schedule{
		ID: "sched-1", Name: "daily", To: "worker", Subject: "Check", Body: "Do it",
		Cron: "0 9 * * *", Timezone: "UTC", Enabled: true, CreatedAt: now(), UpdatedAt: now(),
	}
	if err := os.Mkdir(filepath.Join(st.Dir(), "schedules.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.recomputeMissingScheduleRuns()
	if got := h.schedules["sched-1"]; got.NextRunAt != "" || got.LastError != "" {
		t.Fatalf("uncommitted recomputation reached projection: %#v", got)
	}
}

func testHub(st *store.Store) *Hub {
	return &Hub{
		st:                st,
		agents:            map[string]*Agent{},
		comms:             map[string]*AgentMessage{},
		schedules:         map[string]*Schedule{},
		profiles:          map[string]*AgentProfile{},
		teamLinks:         map[string]*TeamRelationship{},
		organizationLinks: map[string]*OrganizationRelationship{},
		humanRequests:     map[string]*HumanRequest{},
		goals:             map[string]*ThreadGoal{},
		seqs:              map[string]int64{},
		runtimes:          map[string]*runtime{},
		subs:              map[string]map[*subscriber]struct{}{},
		globalSubs:        map[*subscriber]struct{}{},
	}
}
