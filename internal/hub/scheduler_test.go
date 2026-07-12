package hub

import (
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

func testHub(st *store.Store) *Hub {
	return &Hub{
		st:         st,
		agents:     map[string]*Agent{},
		comms:      map[string]*AgentMessage{},
		schedules:  map[string]*Schedule{},
		profiles:   map[string]*AgentProfile{},
		teamLinks:  map[string]*TeamRelationship{},
		seqs:       map[string]int64{},
		runtimes:   map[string]*runtime{},
		subs:       map[string]map[*subscriber]struct{}{},
		globalSubs: map[*subscriber]struct{}{},
	}
}
