package main

import (
	"strings"
	"testing"
)

func TestWorkloadDays(t *testing.T) {
	for value, want := range map[string]int{"": 7, "1": 1, "7": 7, "30": 30, "90": 90} {
		got, err := workloadDays(value)
		if err != nil || got != want {
			t.Fatalf("workloadDays(%q) = %d, %v; want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"0", "31", "91", "week"} {
		if _, err := workloadDays(value); err == nil {
			t.Fatalf("workloadDays(%q) accepted an unsupported window", value)
		}
	}
}

func TestFormatWorkloadReportKeepsProxySamplesAndDataQualityVisible(t *testing.T) {
	overview := workloadOverviewFixture()
	got := formatWorkloadReport("organization", overview, overview, false)
	for _, fragment := range []string{
		"Organization workload",
		"window: 7 days · 2026-07-10 to 2026-07-16 · Local (UTC+08:00)",
		"executing: 25.0% of 14.0 observed Agent-days · 2.0h aggregate recorded Turn time across 2 Agents",
		"calendar non-executing proxy: 75.0%",
		"p50 2s · p90 1.5m · max 2.0m · n=8",
		"Agents (alphabetical; not a ranking)",
		"alpha:",
		"zeta:",
		"coverage: 2/2 Agents",
		"Idle proxy includes machine downtime.",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("workload output missing %q:\n%s", fragment, got)
		}
	}
	if strings.Index(got, "alpha:") > strings.Index(got, "zeta:") {
		t.Fatalf("Agents are not alphabetical:\n%s", got)
	}
	for _, forbidden := range []string{"overloaded", "underutilized", "split", "merge"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("workload output contains recommendation label %q:\n%s", forbidden, got)
		}
	}
}

func TestFormatAgentWorkloadDistinguishesMissingActivityAndZeroSamples(t *testing.T) {
	overview := workloadOverviewFixture()
	agent := map[string]any{
		"agentId": "agent-missing", "agentName": "missing", "status": "idle", "activityAvailable": false,
		"wait": map[string]any{"samples": 0.0}, "backlog": map[string]any{"count": 0.0},
		"sources": []any{}, "evidence": []any{},
	}
	got := formatWorkloadReport("agent", overview, agent, true)
	for _, fragment := range []string{
		"executing: unavailable",
		"calendar non-executing proxy: unavailable",
		"no completed samples (n=0)",
		"current backlog: 0 · oldest —",
		"Evidence (0 of 0; queued first, then longest waits; metadata only)",
		"Data quality",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("Agent workload output missing %q:\n%s", fragment, got)
		}
	}
}

func workloadOverviewFixture() map[string]any {
	wait := map[string]any{"samples": 8.0, "p50Ms": 2000.0, "p90Ms": 90000.0, "maxMs": 120000.0}
	backlog := map[string]any{"count": 1.0, "oldestMs": 3600000.0}
	agent := func(name string) map[string]any {
		return map[string]any{
			"agentId": name + "-id", "agentName": name, "status": "idle", "activityAvailable": true,
			"executingPercent": 25.0, "idleProxyPercent": 75.0, "executingSeconds": 3600.0, "turnCount": 4.0,
			"wait": wait, "backlog": backlog, "sources": []any{}, "evidence": []any{},
		}
	}
	return map[string]any{
		"days": 7.0, "since": "2026-07-10", "through": "2026-07-16", "timezone": "Local (UTC+08:00)",
		"generatedAt": "2026-07-16T01:00:00Z", "executingPercent": 25.0, "idleProxyPercent": 75.0,
		"observedSeconds":  14.0 * 24 * 60 * 60,
		"executingSeconds": 7200.0, "wait": wait, "backlog": backlog,
		"daily":    []any{map[string]any{"turnCount": 8.0}},
		"sources":  []any{map[string]any{"source": "internal", "wait": wait, "backlog": backlog}},
		"agents":   []any{agent("zeta"), agent("alpha")},
		"evidence": []any{},
		"dataQuality": map[string]any{
			"trackedActivityAgents": 2.0, "totalAgents": 2.0, "activityBasis": "codex_rollout_turn_intervals",
			"idleBasis": "calendar_non_executing_proxy", "historicalWaitReasons": "unrecorded",
			"limitations": []any{"Idle proxy includes machine downtime."},
		},
	}
}

func TestFormatWorkloadReportLabelsTimeSemanticsAndEvidenceSelection(t *testing.T) {
	overview := workloadOverviewFixture()
	overview["sources"] = []any{
		map[string]any{"source": "continuation", "wait": map[string]any{"samples": 3.0}, "backlog": map[string]any{"count": 0.0}},
		map[string]any{"source": "internal", "wait": map[string]any{"samples": 8.0}, "backlog": map[string]any{"count": 1.0, "oldestMs": 1000.0}},
	}
	overview["evidence"] = []any{map[string]any{
		"id": "msg_1", "agentName": "alpha", "source": "internal", "state": "started", "waitMs": 1000.0,
		"waitReason": "unrecorded", "queuedAt": "2026-07-16T00:00:00Z", "startedAt": "2026-07-16T00:00:01Z", "evidenceHref": "#messages",
	}}
	got := formatWorkloadReport("organization", overview, overview, true)
	for _, fragment := range []string{
		"continuation (excluded from new-work headline)",
		"current_status=idle",
		"Evidence (1 of 12; queued first, then longest waits; metadata only)",
		"navigation=#messages (generic)",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("workload output missing %q:\n%s", fragment, got)
		}
	}
}
