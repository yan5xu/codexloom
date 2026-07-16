package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeUsageRollout(t *testing.T, threadID string) {
	t.Helper()
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "13")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-07-13T09-00-00-"+threadID+".jsonl")
	data := `{"timestamp":"2026-07-12T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-12T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6"}}
{"timestamp":"2026-07-12T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110},"model_context_window":1000}}}
{"timestamp":"2026-07-13T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-13T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-5.6-sol"}}
{"timestamp":"2026-07-13T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":100,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":200},"last_token_usage":{"input_tokens":80,"cached_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":90},"model_context_window":1000}}}
{"timestamp":"2026-07-13T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":100,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":200},"last_token_usage":{"input_tokens":80,"cached_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":90},"model_context_window":1000}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)
}

func TestBuildAgentUsageSeparatesTodayPeriodAndLifetime(t *testing.T) {
	const threadID = "usage-thread"
	writeUsageRollout(t, threadID)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	agent := AgentView{Agent: Agent{ID: "agent-1", Name: "research", ThreadID: threadID, Status: "idle"}}

	usage := buildAgentUsage(agent, 7, now)
	if !usage.Available {
		t.Fatal("usage should be available")
	}
	if usage.Lifetime.TotalTokens != 200 || usage.Period.TotalTokens != 200 {
		t.Fatalf("lifetime/period = %d/%d", usage.Lifetime.TotalTokens, usage.Period.TotalTokens)
	}
	if usage.Today.TotalTokens != 90 {
		t.Fatalf("today = %#v, want second call only", usage.Today)
	}
	if usage.CacheHitPercent < 55.5 || usage.CacheHitPercent > 55.6 {
		t.Fatalf("cache hit = %v, want about 55.56", usage.CacheHitPercent)
	}
	if usage.Context.UsedPercent != 8 {
		t.Fatalf("context = %#v, want 8%%", usage.Context)
	}
	if len(usage.Models) != 2 || usage.Models[0].Model != "gpt-5.6" {
		t.Fatalf("models = %#v", usage.Models)
	}
}

func TestBuildAgentUsageWithoutThreadIsUnavailable(t *testing.T) {
	usage := buildAgentUsage(AgentView{Agent: Agent{ID: "agent-1", Name: "empty"}}, 7, time.Now())
	if usage.Available || len(usage.Daily) != 7 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestBuildAgentUsageRangeSelectsHistoricalDayAndPreviousDay(t *testing.T) {
	const threadID = "usage-range-thread"
	writeUsageRollout(t, threadID)
	location := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 7, 13, 0, 0, 0, 0, location)
	endExclusive := start.AddDate(0, 0, 1)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, location)
	agent := AgentView{Agent: Agent{ID: "agent-1", Name: "research", ThreadID: threadID, Status: "idle"}}

	usage := buildAgentUsageRange(agent, start, endExclusive, now)
	if usage.Period.TotalTokens != 90 {
		t.Fatalf("period = %#v, want Jul 13 usage", usage.Period)
	}
	if usage.Previous.TotalTokens != 110 {
		t.Fatalf("previous = %#v, want Jul 12 usage", usage.Previous)
	}
	if len(usage.Daily) != 1 || usage.Daily[0].Date != "2026-07-13" || usage.Daily[0].Usage.TotalTokens != 90 {
		t.Fatalf("daily = %#v", usage.Daily)
	}
}

func TestPreviousUsageRangeMatchesElapsedClockForLiveWindow(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, location)
	endExclusive := time.Date(2026, 7, 17, 0, 0, 0, 0, location)
	now := time.Date(2026, 7, 16, 10, 30, 0, 0, location)

	previousStart, previousEnd := previousUsageRange(start, endExclusive, now)
	if got := previousStart.Format(time.RFC3339); got != "2026-07-03T00:00:00+08:00" {
		t.Fatalf("previous start = %s", got)
	}
	if got := previousEnd.Format(time.RFC3339); got != "2026-07-09T10:30:00+08:00" {
		t.Fatalf("previous end = %s", got)
	}
}
