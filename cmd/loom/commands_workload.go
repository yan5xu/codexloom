package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func cmdWorkload(a args) {
	if len(a.positional) > 1 {
		usage("workload [agent] [--days 1|7|30|90] [--evidence] [--json]")
	}
	for name := range a.flags {
		switch name {
		case "days", "evidence", "json":
		default:
			fail(fmt.Errorf("unknown workload flag: --%s", name))
		}
	}
	days, err := workloadDays(a.flags["days"])
	if err != nil {
		fail(err)
	}

	overviewResponse, err := api("GET", "/api/workload?days="+strconv.Itoa(days), nil)
	if err != nil {
		fail(err)
	}
	overview, _ := overviewResponse["workload"].(map[string]any)
	if overview == nil {
		fail(fmt.Errorf("CodexLoom returned an invalid workload report"))
	}

	scope := "organization"
	report := overview
	if len(a.positional) == 1 {
		scope = "agent"
		agentResponse, requestErr := api("GET", "/api/agents/"+url.PathEscape(a.positional[0])+"/workload?days="+strconv.Itoa(days), nil)
		if requestErr != nil {
			fail(requestErr)
		}
		report, _ = agentResponse["workload"].(map[string]any)
		if report == nil {
			fail(fmt.Errorf("CodexLoom returned an invalid Agent workload report"))
		}
	}

	if workloadFlag(a, "json") {
		output := map[string]any{
			"scope":       scope,
			"days":        overview["days"],
			"since":       overview["since"],
			"through":     overview["through"],
			"timezone":    overview["timezone"],
			"generatedAt": overview["generatedAt"],
			"workload":    report,
			"dataQuality": overview["dataQuality"],
		}
		data, marshalErr := json.MarshalIndent(output, "", "  ")
		if marshalErr != nil {
			fail(marshalErr)
		}
		fmt.Println(string(data))
		return
	}

	fmt.Print(formatWorkloadReport(scope, overview, report, workloadFlag(a, "evidence")))
}

func workloadDays(value string) (int, error) {
	if value == "" {
		return 7, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || (days != 1 && days != 7 && days != 30 && days != 90) {
		return 0, fmt.Errorf("--days must be one of 1, 7, 30, or 90")
	}
	return days, nil
}

func workloadFlag(a args, name string) bool {
	value, ok := a.flags[name]
	return ok && value != "false" && value != "0"
}

func formatWorkloadReport(scope string, overview, report map[string]any, showEvidence bool) string {
	var b strings.Builder
	if scope == "agent" {
		fmt.Fprintf(&b, "Agent workload: %s\n", value(report, "agentName", value(report, "agentId", "unknown")))
		fmt.Fprintf(&b, "current status: %s\n", value(report, "status", "unknown"))
	} else {
		b.WriteString("Organization workload\n")
	}
	fmt.Fprintf(&b, "window: %.0f days · %s to %s · %s\n",
		num(overview, "days"), value(overview, "since", "unknown"), value(overview, "through", "unknown"), value(overview, "timezone", "unknown"))
	fmt.Fprintf(&b, "generated: %s\n\n", value(overview, "generatedAt", "unknown"))

	formatWorkloadFacts(&b, report, scope == "agent")
	formatWorkloadSources(&b, report)
	if scope == "organization" {
		formatWorkloadAgents(&b, report)
	}
	if showEvidence {
		formatWorkloadEvidence(&b, report)
	}
	formatWorkloadDataQuality(&b, overview)
	return b.String()
}

func formatWorkloadFacts(b *strings.Builder, report map[string]any, agent bool) {
	activityAvailable := !agent || boolean(report, "activityAvailable")
	if activityAvailable {
		if agent {
			fmt.Fprintf(b, "executing: %.1f%% · %s recorded Turn time · %.0f Turns\n",
				num(report, "executingPercent"), formatWorkloadSeconds(num(report, "executingSeconds")), workloadTurnCount(report, true))
		} else {
			quality, _ := report["dataQuality"].(map[string]any)
			fmt.Fprintf(b, "executing: %.1f%% of %.1f observed Agent-days · %s aggregate recorded Turn time across %.0f Agents · %.0f Turns\n",
				num(report, "executingPercent"), num(report, "observedSeconds")/(24*60*60), formatWorkloadSeconds(num(report, "executingSeconds")),
				num(quality, "totalAgents"), workloadTurnCount(report, false))
		}
		fmt.Fprintf(b, "calendar non-executing proxy: %.1f%%\n", num(report, "idleProxyPercent"))
	} else {
		b.WriteString("executing: unavailable · no readable rollout activity\n")
		b.WriteString("calendar non-executing proxy: unavailable\n")
	}
	wait, _ := report["wait"].(map[string]any)
	backlog, _ := report["backlog"].(map[string]any)
	fmt.Fprintf(b, "new-work queue wait: %s\n", formatWorkloadWait(wait))
	fmt.Fprintf(b, "current backlog: %.0f · oldest %s\n\n", num(backlog, "count"), formatWorkloadOldest(backlog))
}

func workloadTurnCount(report map[string]any, agent bool) float64 {
	if agent {
		return num(report, "turnCount")
	}
	total := 0.0
	for _, item := range anySlice(report["daily"]) {
		day, _ := item.(map[string]any)
		total += num(day, "turnCount")
	}
	return total
}

func formatWorkloadSources(b *strings.Builder, report map[string]any) {
	b.WriteString("Work sources\n")
	sources := anySlice(report["sources"])
	if len(sources) == 0 {
		b.WriteString("  no queue evidence in this window\n\n")
		return
	}
	for _, item := range sources {
		source, _ := item.(map[string]any)
		wait, _ := source["wait"].(map[string]any)
		backlog, _ := source["backlog"].(map[string]any)
		label := value(source, "source", "unknown")
		if label == "continuation" {
			label += " (excluded from new-work headline)"
		}
		fmt.Fprintf(b, "  %s: wait %s · backlog %.0f · oldest %s\n",
			label, formatWorkloadWait(wait), num(backlog, "count"), formatWorkloadOldest(backlog))
	}
	b.WriteString("\n")
}

func formatWorkloadAgents(b *strings.Builder, report map[string]any) {
	agents := append([]any(nil), anySlice(report["agents"])...)
	sort.SliceStable(agents, func(i, j int) bool {
		left, _ := agents[i].(map[string]any)
		right, _ := agents[j].(map[string]any)
		return value(left, "agentName", "") < value(right, "agentName", "")
	})
	b.WriteString("Agents (alphabetical; not a ranking)\n")
	for _, item := range agents {
		agent, _ := item.(map[string]any)
		wait, _ := agent["wait"].(map[string]any)
		backlog, _ := agent["backlog"].(map[string]any)
		execution := "unavailable"
		proxy := "unavailable"
		if boolean(agent, "activityAvailable") {
			execution = fmt.Sprintf("%.1f%%", num(agent, "executingPercent"))
			proxy = fmt.Sprintf("%.1f%%", num(agent, "idleProxyPercent"))
		}
		fmt.Fprintf(b, "  %s: current_status=%s · executing=%s · proxy=%s · turns=%.0f · wait %s · backlog=%.0f oldest=%s\n",
			value(agent, "agentName", value(agent, "agentId", "unknown")), value(agent, "status", "unknown"), execution, proxy,
			num(agent, "turnCount"), formatWorkloadWait(wait), num(backlog, "count"), formatWorkloadOldest(backlog))
	}
	b.WriteString("\n")
}

func formatWorkloadEvidence(b *strings.Builder, report map[string]any) {
	evidence := anySlice(report["evidence"])
	total := workloadEvidenceTotal(report)
	if total < len(evidence) {
		total = len(evidence)
	}
	fmt.Fprintf(b, "Evidence (%d of %d; queued first, then longest waits; metadata only)\n", len(evidence), total)
	if len(evidence) == 0 {
		b.WriteString("  no queue evidence in this window\n\n")
		return
	}
	for _, item := range evidence {
		record, _ := item.(map[string]any)
		source := value(record, "source", "unknown")
		if provider := value(record, "provider", ""); provider != "" {
			source += "/" + provider
		}
		fmt.Fprintf(b, "  %s · agent=%s · source=%s · state=%s · wait=%s · reason=%s\n",
			value(record, "id", "unknown"), value(record, "agentName", value(record, "agentId", "unknown")), source,
			value(record, "state", "unknown"), formatWorkloadMillis(num(record, "waitMs")), value(record, "waitReason", "unrecorded"))
		navigation := value(record, "evidenceHref", "—")
		if navigation == "#messages" {
			navigation += " (generic)"
		}
		fmt.Fprintf(b, "    queued=%s · started=%s · navigation=%s\n", value(record, "queuedAt", "unknown"), value(record, "startedAt", "—"), navigation)
	}
	b.WriteString("\n")
}

func workloadEvidenceTotal(report map[string]any) int {
	total := 0
	for _, item := range anySlice(report["sources"]) {
		source, _ := item.(map[string]any)
		wait, _ := source["wait"].(map[string]any)
		backlog, _ := source["backlog"].(map[string]any)
		total += int(num(wait, "samples") + num(backlog, "count"))
	}
	return total
}

func formatWorkloadDataQuality(b *strings.Builder, overview map[string]any) {
	quality, _ := overview["dataQuality"].(map[string]any)
	b.WriteString("Data quality\n")
	fmt.Fprintf(b, "  coverage: %.0f/%.0f Agents with readable rollout activity\n",
		num(quality, "trackedActivityAgents"), num(quality, "totalAgents"))
	fmt.Fprintf(b, "  activity basis: %s\n", value(quality, "activityBasis", "unknown"))
	fmt.Fprintf(b, "  non-executing basis: %s\n", value(quality, "idleBasis", "unknown"))
	fmt.Fprintf(b, "  historical wait reasons: %s\n", value(quality, "historicalWaitReasons", "unknown"))
	for _, item := range anySlice(quality["limitations"]) {
		if limitation, ok := item.(string); ok && limitation != "" {
			fmt.Fprintf(b, "  - %s\n", limitation)
		}
	}
}

func formatWorkloadWait(wait map[string]any) string {
	samples := int(num(wait, "samples"))
	if samples == 0 {
		return "no completed samples (n=0)"
	}
	return fmt.Sprintf("p50 %s · p90 %s · max %s · n=%d",
		formatWorkloadMillis(num(wait, "p50Ms")), formatWorkloadMillis(num(wait, "p90Ms")), formatWorkloadMillis(num(wait, "maxMs")), samples)
}

func formatWorkloadOldest(backlog map[string]any) string {
	if num(backlog, "count") == 0 {
		return "—"
	}
	return formatWorkloadMillis(num(backlog, "oldestMs"))
}

func formatWorkloadSeconds(seconds float64) string {
	return formatWorkloadDuration(time.Duration(seconds * float64(time.Second)))
}

func formatWorkloadMillis(milliseconds float64) string {
	return formatWorkloadDuration(time.Duration(milliseconds * float64(time.Millisecond)))
}

func formatWorkloadDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	if duration < time.Second {
		return "<1s"
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.0fs", duration.Seconds())
	}
	if duration < time.Hour {
		return fmt.Sprintf("%.1fm", duration.Minutes())
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%.1fh", duration.Hours())
	}
	return fmt.Sprintf("%.1fd", duration.Hours()/24)
}
