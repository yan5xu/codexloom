package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildWorkloadOverviewCombinesActivityWaitAndBacklog(t *testing.T) {
	const threadID = "workload-thread"
	sessions := t.TempDir()
	day := filepath.Join(sessions, "2026", "07", "15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(day, "rollout-2026-07-15T00-00-00-"+threadID+".jsonl")
	rolloutData := `{"timestamp":"2026-07-15T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-15T02:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
{"timestamp":"2026-07-15T04:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-15T04:30:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}
`
	if err := os.WriteFile(rolloutPath, []byte(rolloutData), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", sessions)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	snapshot := workloadSnapshot{
		agents: []Agent{{ID: "agent-1", Name: "research", ThreadID: threadID, Status: "running", CreatedAt: "2026-07-15T00:00:00Z"}},
		messages: []AgentMessage{
			{ID: "msg-1", ToAgentID: "agent-1", CreatedAt: "2026-07-15T03:00:00Z", DeliveredAt: "2026-07-15T03:10:00Z", DeliveryStatus: "delivered"},
			{ID: "msg-2", ToAgentID: "agent-1", ScheduleID: "sched-1", ScheduledAt: "2026-07-15T04:00:00Z", CreatedAt: "2026-07-15T03:59:00Z", DeliveredAt: "2026-07-15T04:30:00Z", DeliveryStatus: "delivered"},
		},
		inbox: []InboxItem{{ID: "inb-1", AgentID: "agent-1", MessageID: "imsg-1", State: "handled"}},
		inboxMessages: map[string]InboxMessage{
			"imsg-1": {ID: "imsg-1", Origin: "lark", ReceivedAt: "2026-07-15T05:00:00Z"},
		},
		attempts: map[string]HandlingAttempt{
			"att-1": {ID: "att-1", InboxItemID: "inb-1", StartedAt: "2026-07-15T05:20:00Z"},
		},
		humanRequests: []HumanRequest{{
			ID: "need-1", AgentID: "agent-1", State: "answered", DeliveryStatus: "queued", AnsweredAt: "2026-07-15T06:00:00Z",
		}},
		waitReasons: map[string]string{"agent-1": "agent_busy"},
	}

	overview := buildWorkloadOverview(snapshot, 1, now)
	if overview.ExecutingSeconds != 90*60 || overview.ExecutingPercent != 12.5 || overview.IdleProxyPercent != 87.5 {
		t.Fatalf("activity overview = %#v", overview)
	}
	if overview.Wait.Samples != 3 || overview.Wait.P50Ms != 20*60*1000 || overview.Wait.P90Ms != 30*60*1000 {
		t.Fatalf("wait stats = %#v", overview.Wait)
	}
	if overview.Backlog.Count != 1 || overview.Backlog.OldestMs != 6*60*60*1000 {
		t.Fatalf("backlog = %#v", overview.Backlog)
	}
	if len(overview.Agents) != 1 || overview.Agents[0].TurnCount != 2 || len(overview.Agents[0].Sources) != 4 {
		t.Fatalf("agent workload = %#v", overview.Agents)
	}
	if len(overview.Evidence) == 0 || overview.Evidence[0].ID != "need-1" || overview.Evidence[0].WaitReason != "agent_busy" {
		t.Fatalf("evidence = %#v", overview.Evidence)
	}
	if overview.DataQuality.IdleBasis != "calendar_non_executing_proxy" || overview.DataQuality.TrackedActivityAgents != 1 {
		t.Fatalf("data quality = %#v", overview.DataQuality)
	}
}

func TestWorkloadPercentileUsesNearestRank(t *testing.T) {
	values := []int64{10, 20}
	if got := workloadPercentile(values, 0.9); got != 20 {
		t.Fatalf("p90 = %d, want 20", got)
	}
}

func TestBuildWorkloadOverviewExcludesUnavailableActivityFromIdleDenominator(t *testing.T) {
	t.Setenv("CODEX_SESSIONS_DIR", t.TempDir())
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	overview := buildWorkloadOverview(workloadSnapshot{
		agents: []Agent{{ID: "agent-1", Name: "missing", ThreadID: "missing-thread", CreatedAt: "2026-07-15T00:00:00Z"}},
	}, 1, now)

	if overview.ObservedSeconds != 0 || overview.IdleProxyPercent != 0 {
		t.Fatalf("overview without rollout = %#v", overview)
	}
	if overview.Agents[0].ActivityAvailable || overview.Agents[0].ObservedSeconds != 0 {
		t.Fatalf("agent without rollout = %#v", overview.Agents[0])
	}
}

func TestBuildWorkloadOverviewIncludesBacklogOlderThanWindow(t *testing.T) {
	t.Setenv("CODEX_SESSIONS_DIR", t.TempDir())
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	overview := buildWorkloadOverview(workloadSnapshot{
		agents: []Agent{{ID: "agent-1", Name: "queued", ThreadID: "missing-thread"}},
		messages: []AgentMessage{{
			ID: "msg-old", ToAgentID: "agent-1", CreatedAt: "2026-06-01T00:00:00Z", DeliveryStatus: "queued",
		}},
		waitReasons: map[string]string{"agent-1": "active_goal"},
	}, 7, now)

	if overview.Backlog.Count != 1 || overview.Backlog.OldestMs != int64((44*24+12)*time.Hour/time.Millisecond) {
		t.Fatalf("old backlog = %#v", overview.Backlog)
	}
	if len(overview.Evidence) != 1 || overview.Evidence[0].WaitReason != "active_goal" {
		t.Fatalf("old backlog evidence = %#v", overview.Evidence)
	}
}

func TestBuildWorkloadOverviewRangeSeparatesHistoricalSamplesFromCurrentBacklog(t *testing.T) {
	const threadID = "historical-workload-thread"
	sessions := t.TempDir()
	day := filepath.Join(sessions, "2026", "07", "15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(day, "rollout-2026-07-15T00-00-00-"+threadID+".jsonl")
	rolloutData := `{"timestamp":"2026-07-15T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-selected"}}
{"timestamp":"2026-07-15T02:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-selected"}}
{"timestamp":"2026-07-16T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-outside"}}
{"timestamp":"2026-07-16T03:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-outside"}}
`
	if err := os.WriteFile(rolloutPath, []byte(rolloutData), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", sessions)
	start := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	endExclusive := start.AddDate(0, 0, 1)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	overview := buildWorkloadOverviewRange(workloadSnapshot{
		agents: []Agent{{ID: "agent-1", Name: "research", ThreadID: threadID, CreatedAt: "2026-07-14T00:00:00Z"}},
		messages: []AgentMessage{
			{ID: "msg-selected", ToAgentID: "agent-1", CreatedAt: "2026-07-15T03:00:00Z", DeliveredAt: "2026-07-15T03:10:00Z", DeliveryStatus: "delivered"},
			{ID: "msg-outside", ToAgentID: "agent-1", CreatedAt: "2026-07-16T03:00:00Z", DeliveredAt: "2026-07-16T03:30:00Z", DeliveryStatus: "delivered"},
			{ID: "msg-current", ToAgentID: "agent-1", CreatedAt: "2026-07-17T11:00:00Z", DeliveryStatus: "queued"},
		},
		waitReasons: map[string]string{"agent-1": "agent_busy"},
	}, start, endExclusive, now)

	if overview.Live || overview.Days != 1 || overview.Since != "2026-07-15" || overview.Through != "2026-07-15" {
		t.Fatalf("historical window = %#v", overview)
	}
	if overview.ObservedSeconds != 24*60*60 || overview.ExecutingSeconds != 60*60 {
		t.Fatalf("historical activity = %#v", overview)
	}
	if overview.Wait.Samples != 1 || overview.Wait.P50Ms != 10*60*1000 {
		t.Fatalf("historical wait = %#v", overview.Wait)
	}
	if overview.Backlog.Count != 1 || overview.Backlog.OldestMs != 60*60*1000 {
		t.Fatalf("current backlog = %#v", overview.Backlog)
	}
	if len(overview.Agents) != 1 || overview.Agents[0].TurnCount != 1 || len(overview.Daily) != 1 || overview.Daily[0].TurnCount != 1 {
		t.Fatalf("daily activity = %#v", overview.Daily)
	}
}

func TestBuildWorkloadOverviewRangeClipsTodayAtNow(t *testing.T) {
	const threadID = "live-workload-thread"
	sessions := t.TempDir()
	day := filepath.Join(sessions, "2026", "07", "16")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(day, "rollout-2026-07-16T00-00-00-"+threadID+".jsonl")
	rolloutData := `{"timestamp":"2026-07-16T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-live"}}
{"timestamp":"2026-07-16T02:00:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-live"}}
`
	if err := os.WriteFile(rolloutPath, []byte(rolloutData), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", sessions)
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	endExclusive := start.AddDate(0, 0, 1)
	now := start.Add(12 * time.Hour)
	overview := buildWorkloadOverviewRange(workloadSnapshot{
		agents: []Agent{{ID: "agent-1", Name: "research", ThreadID: threadID, CreatedAt: "2026-07-15T00:00:00Z"}},
	}, start, endExclusive, now)

	if !overview.Live || overview.ObservedSeconds != 12*60*60 || overview.ExecutingSeconds != 60*60 {
		t.Fatalf("live workload window = %#v", overview)
	}
	if len(overview.Daily) != 1 || overview.Daily[0].ObservedSeconds != 12*60*60 {
		t.Fatalf("live daily denominator = %#v", overview.Daily)
	}
}
