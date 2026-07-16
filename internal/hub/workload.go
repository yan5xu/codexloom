package hub

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/rollout"
)

type WorkloadWaitStats struct {
	Samples int   `json:"samples"`
	P50Ms   int64 `json:"p50Ms"`
	P90Ms   int64 `json:"p90Ms"`
	MaxMs   int64 `json:"maxMs"`
}

type WorkloadBacklog struct {
	Count    int   `json:"count"`
	OldestMs int64 `json:"oldestMs"`
}

type WorkloadDay struct {
	Date             string  `json:"date"`
	ObservedSeconds  int64   `json:"observedSeconds"`
	ExecutingSeconds int64   `json:"executingSeconds"`
	ExecutingPercent float64 `json:"executingPercent"`
	TurnCount        int     `json:"turnCount"`
}

type WorkloadSource struct {
	Source  string            `json:"source"`
	Wait    WorkloadWaitStats `json:"wait"`
	Backlog WorkloadBacklog   `json:"backlog"`
}

type WorkloadEvidence struct {
	ID           string `json:"id"`
	AgentID      string `json:"agentId"`
	AgentName    string `json:"agentName"`
	Source       string `json:"source"`
	Provider     string `json:"provider,omitempty"`
	State        string `json:"state"`
	QueuedAt     string `json:"queuedAt"`
	StartedAt    string `json:"startedAt,omitempty"`
	WaitMs       int64  `json:"waitMs"`
	WaitReason   string `json:"waitReason"`
	EvidenceHref string `json:"evidenceHref"`
}

type AgentWorkload struct {
	AgentID           string             `json:"agentId"`
	AgentName         string             `json:"agentName"`
	Status            string             `json:"status"`
	ActivityAvailable bool               `json:"activityAvailable"`
	ObservedSeconds   int64              `json:"observedSeconds"`
	ExecutingSeconds  int64              `json:"executingSeconds"`
	ExecutingPercent  float64            `json:"executingPercent"`
	IdleProxyPercent  float64            `json:"idleProxyPercent"`
	TurnCount         int                `json:"turnCount"`
	OpenTurns         int                `json:"openTurns"`
	InferredTurns     int                `json:"inferredTurns"`
	Wait              WorkloadWaitStats  `json:"wait"`
	Backlog           WorkloadBacklog    `json:"backlog"`
	Daily             []WorkloadDay      `json:"daily"`
	Sources           []WorkloadSource   `json:"sources"`
	Evidence          []WorkloadEvidence `json:"evidence"`
}

type WorkloadDataQuality struct {
	ActivityBasis         string   `json:"activityBasis"`
	IdleBasis             string   `json:"idleBasis"`
	HistoricalWaitReasons string   `json:"historicalWaitReasons"`
	TrackedActivityAgents int      `json:"trackedActivityAgents"`
	TotalAgents           int      `json:"totalAgents"`
	Limitations           []string `json:"limitations"`
}

type WorkloadOverview struct {
	Days             int                 `json:"days"`
	Since            string              `json:"since"`
	Through          string              `json:"through"`
	Timezone         string              `json:"timezone"`
	GeneratedAt      string              `json:"generatedAt"`
	Live             bool                `json:"live"`
	ObservedSeconds  int64               `json:"observedSeconds"`
	ExecutingSeconds int64               `json:"executingSeconds"`
	ExecutingPercent float64             `json:"executingPercent"`
	IdleProxyPercent float64             `json:"idleProxyPercent"`
	Wait             WorkloadWaitStats   `json:"wait"`
	Backlog          WorkloadBacklog     `json:"backlog"`
	Daily            []WorkloadDay       `json:"daily"`
	Sources          []WorkloadSource    `json:"sources"`
	Agents           []AgentWorkload     `json:"agents"`
	Evidence         []WorkloadEvidence  `json:"evidence"`
	DataQuality      WorkloadDataQuality `json:"dataQuality"`
}

type workloadQueueSample struct {
	evidence         WorkloadEvidence
	complete         bool
	includeInNewWork bool
}

type workloadSnapshot struct {
	agents        []Agent
	messages      []AgentMessage
	inbox         []InboxItem
	inboxMessages map[string]InboxMessage
	attempts      map[string]HandlingAttempt
	humanRequests []HumanRequest
	waitReasons   map[string]string
}

func (h *Hub) WorkloadOverview(days int) WorkloadOverview {
	return buildWorkloadOverview(h.workloadSnapshot(), days, time.Now())
}

func buildWorkloadOverview(snapshot workloadSnapshot, days int, now time.Time) WorkloadOverview {
	days = normalizeUsageDays(days)
	start, through := usageRange(now, days)
	return buildWorkloadOverviewRange(snapshot, start, through.AddDate(0, 0, 1), now)
}

func (h *Hub) WorkloadOverviewRange(start, endExclusive time.Time) WorkloadOverview {
	now := time.Now().In(start.Location())
	return buildWorkloadOverviewRange(h.workloadSnapshot(), start, endExclusive, now)
}

func buildWorkloadOverviewRange(snapshot workloadSnapshot, start, endExclusive, now time.Time) WorkloadOverview {
	now = now.In(start.Location())
	days := usageCalendarDays(start, endExclusive)
	through := endExclusive.AddDate(0, 0, -1)
	windowEnd := endExclusive
	if now.Before(windowEnd) {
		windowEnd = now
	}
	overview := WorkloadOverview{
		Days: days, Since: start.Format("2006-01-02"), Through: through.Format("2006-01-02"),
		Timezone: usageTimezoneLabel(now), GeneratedAt: now.UTC().Format(time.RFC3339Nano), Live: usageRangeIsLive(endExclusive, now),
		Daily: emptyWorkloadDays(start, days), Sources: []WorkloadSource{}, Agents: []AgentWorkload{}, Evidence: []WorkloadEvidence{},
		DataQuality: WorkloadDataQuality{
			ActivityBasis: "codex_rollout_turn_intervals", IdleBasis: "calendar_non_executing_proxy",
			HistoricalWaitReasons: "unrecorded", TotalAgents: len(snapshot.agents),
			Limitations: []string{
				"Idle proxy includes machine and service downtime because historical online intervals are not persisted.",
				"Turn time includes tool, network, approval, and model waiting inside an executing Turn.",
				"Historical queue wait reasons were not persisted; only current backlog reasons are classified.",
				"Backlog is a current snapshot and is not reconstructed for historical calendar ranges.",
				"Legacy internal messages without handling attempts use delivery acceptance as an estimated start time.",
			},
		},
	}

	queueByAgent := collectWorkloadQueueSamples(snapshot, start, windowEnd, now)
	allSamples := []workloadQueueSample{}
	for _, agent := range snapshot.agents {
		workload := buildAgentWorkload(agent, queueByAgent[agent.ID], start, days, windowEnd)
		overview.Agents = append(overview.Agents, workload)
		if workload.ActivityAvailable {
			overview.DataQuality.TrackedActivityAgents++
		}
		overview.ObservedSeconds += workload.ObservedSeconds
		overview.ExecutingSeconds += workload.ExecutingSeconds
		for index := range overview.Daily {
			if index >= len(workload.Daily) {
				continue
			}
			overview.Daily[index].ObservedSeconds += workload.Daily[index].ObservedSeconds
			overview.Daily[index].ExecutingSeconds += workload.Daily[index].ExecutingSeconds
			overview.Daily[index].TurnCount += workload.Daily[index].TurnCount
		}
		allSamples = append(allSamples, queueByAgent[agent.ID]...)
	}
	overview.ExecutingPercent = workloadPercent(overview.ExecutingSeconds, overview.ObservedSeconds)
	if overview.ObservedSeconds > 0 {
		overview.IdleProxyPercent = 100 - overview.ExecutingPercent
	}
	for index := range overview.Daily {
		overview.Daily[index].ExecutingPercent = workloadPercent(overview.Daily[index].ExecutingSeconds, overview.Daily[index].ObservedSeconds)
	}
	overview.Wait, overview.Backlog, overview.Sources, overview.Evidence = summarizeWorkloadQueue(allSamples)
	sort.SliceStable(overview.Agents, func(i, j int) bool {
		if overview.Agents[i].Backlog.OldestMs != overview.Agents[j].Backlog.OldestMs {
			return overview.Agents[i].Backlog.OldestMs > overview.Agents[j].Backlog.OldestMs
		}
		if overview.Agents[i].Wait.P90Ms != overview.Agents[j].Wait.P90Ms {
			return overview.Agents[i].Wait.P90Ms > overview.Agents[j].Wait.P90Ms
		}
		if overview.Agents[i].ExecutingPercent != overview.Agents[j].ExecutingPercent {
			return overview.Agents[i].ExecutingPercent > overview.Agents[j].ExecutingPercent
		}
		return overview.Agents[i].AgentName < overview.Agents[j].AgentName
	})
	return overview
}

func (h *Hub) AgentWorkload(key string, days int) (AgentWorkload, error) {
	overview := h.WorkloadOverview(days)
	return h.agentWorkloadFromOverview(key, overview)
}

func (h *Hub) AgentWorkloadRange(key string, start, endExclusive time.Time) (AgentWorkload, error) {
	overview := h.WorkloadOverviewRange(start, endExclusive)
	return h.agentWorkloadFromOverview(key, overview)
}

func (h *Hub) agentWorkloadFromOverview(key string, overview WorkloadOverview) (AgentWorkload, error) {
	h.mu.Lock()
	agent := h.resolveLocked(key)
	agentID := ""
	if agent != nil {
		agentID = agent.ID
	}
	h.mu.Unlock()
	if agentID == "" {
		return AgentWorkload{}, errf(404, "agent not found: %s", key)
	}
	for _, workload := range overview.Agents {
		if workload.AgentID == agentID {
			return workload, nil
		}
	}
	return AgentWorkload{}, errf(404, "agent workload not found: %s", key)
}

func (h *Hub) workloadSnapshot() workloadSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot := workloadSnapshot{
		agents: make([]Agent, 0, len(h.agents)), messages: make([]AgentMessage, 0, len(h.comms)),
		inbox: make([]InboxItem, 0, len(h.inbox)), inboxMessages: make(map[string]InboxMessage, len(h.messages)),
		attempts: make(map[string]HandlingAttempt, len(h.attempts)), humanRequests: make([]HumanRequest, 0, len(h.humanRequests)),
		waitReasons: map[string]string{},
	}
	for _, agent := range h.agents {
		if agent == nil {
			continue
		}
		snapshot.agents = append(snapshot.agents, *agent)
		snapshot.waitReasons[agent.ID] = h.currentWaitReasonLocked(agent.ID)
	}
	for _, message := range h.comms {
		if message != nil {
			snapshot.messages = append(snapshot.messages, *message)
		}
	}
	for _, item := range h.inbox {
		if item != nil {
			snapshot.inbox = append(snapshot.inbox, *item)
		}
	}
	for id, message := range h.messages {
		if message != nil {
			snapshot.inboxMessages[id] = *message
		}
	}
	for id, attempt := range h.attempts {
		if attempt != nil {
			snapshot.attempts[id] = *attempt
		}
	}
	for _, request := range h.humanRequests {
		if request != nil {
			snapshot.humanRequests = append(snapshot.humanRequests, cloneHumanRequest(*request))
		}
	}
	return snapshot
}

func (h *Hub) currentWaitReasonLocked(agentID string) string {
	if h.draining {
		return "restart_drain"
	}
	if h.activeGoalReservesThreadLocked(agentID) {
		return "active_goal"
	}
	if agent := h.agents[agentID]; agent != nil && agent.Status == "running" {
		return "agent_busy"
	}
	if runtime := h.runtimes[agentID]; runtime != nil && runtime.activeTurn != nil && !runtime.activeTurn.finished {
		return "agent_busy"
	}
	return "ready_pending_dispatch"
}

func buildAgentWorkload(agent Agent, samples []workloadQueueSample, start time.Time, days int, rangeEnd time.Time) AgentWorkload {
	observedStart := start
	if createdAt, ok := workloadTime(agent.CreatedAt); ok && createdAt.After(observedStart) {
		observedStart = createdAt
	}
	workload := AgentWorkload{
		AgentID: agent.ID, AgentName: agent.Name, Status: agent.Status,
		Daily: emptyWorkloadDays(start, days), Sources: []WorkloadSource{}, Evidence: []WorkloadEvidence{},
	}
	report, err := rollout.ReadUsage(agent.ThreadID)
	if err == nil {
		workload.ActivityAvailable = true
		workload.ObservedSeconds = maxDurationSeconds(rangeEnd.Sub(observedStart))
		intervals := workloadIntervals(report.Activity, observedStart, rangeEnd)
		workload.ExecutingSeconds = intervalDurationSeconds(intervals)
		for _, activity := range report.Activity {
			started, ok := workloadTime(activity.StartedAt)
			if !ok || !started.Before(rangeEnd) {
				continue
			}
			ended, hasEnd := workloadTime(activity.EndedAt)
			overlapsWindow := started.Before(rangeEnd) && (!hasEnd || ended.After(observedStart))
			if !started.Before(observedStart) {
				workload.TurnCount++
			}
			if !hasEnd && overlapsWindow {
				workload.OpenTurns++
			}
			if activity.InferredEnd && overlapsWindow {
				workload.InferredTurns++
			}
		}
		for index := range workload.Daily {
			dayStart := start.AddDate(0, 0, index)
			dayEnd := dayStart.AddDate(0, 0, 1)
			windowStart := dayStart
			if observedStart.After(windowStart) {
				windowStart = observedStart
			}
			dayWindowEnd := dayEnd
			if rangeEnd.Before(dayWindowEnd) {
				dayWindowEnd = rangeEnd
			}
			if dayWindowEnd.After(windowStart) {
				workload.Daily[index].ObservedSeconds = maxDurationSeconds(dayWindowEnd.Sub(windowStart))
			}
			workload.Daily[index].ExecutingSeconds = intervalDurationSeconds(clipWorkloadIntervals(intervals, dayStart, dayWindowEnd))
			for _, activity := range report.Activity {
				started, ok := workloadTime(activity.StartedAt)
				if ok && !started.Before(dayStart) && started.Before(dayWindowEnd) {
					workload.Daily[index].TurnCount++
				}
			}
			workload.Daily[index].ExecutingPercent = workloadPercent(workload.Daily[index].ExecutingSeconds, workload.Daily[index].ObservedSeconds)
		}
	}
	workload.ExecutingPercent = workloadPercent(workload.ExecutingSeconds, workload.ObservedSeconds)
	if workload.ObservedSeconds > 0 {
		workload.IdleProxyPercent = 100 - workload.ExecutingPercent
	}
	workload.Wait, workload.Backlog, workload.Sources, workload.Evidence = summarizeWorkloadQueue(samples)
	return workload
}

func collectWorkloadQueueSamples(snapshot workloadSnapshot, start, selectedEnd, now time.Time) map[string][]workloadQueueSample {
	result := map[string][]workloadQueueSample{}
	agentNames := map[string]string{}
	for _, agent := range snapshot.agents {
		agentNames[agent.ID] = agent.Name
	}
	for _, message := range snapshot.messages {
		queuedAtText := message.CreatedAt
		source := "internal"
		if message.ScheduleID != "" {
			source = "schedule"
			if message.ScheduledAt != "" {
				queuedAtText = message.ScheduledAt
			}
		} else if message.ReplyTo != "" && message.DeliveryMode == "turn_steer" {
			source = "continuation"
		}
		queuedAt, ok := workloadTime(queuedAtText)
		if !ok {
			continue
		}
		startedAtText := firstAgentMessageStart(message)
		startedAt, complete := workloadTime(startedAtText)
		backlog := message.DeliveryStatus == "queued" || message.DeliveryStatus == "delivering"
		inSelectedWindow := !queuedAt.Before(start) && queuedAt.Before(selectedEnd)
		if (complete && !inSelectedWindow) || (!complete && !backlog) {
			continue
		}
		end := now
		state := message.DeliveryStatus
		reason := "unrecorded"
		if complete {
			end = startedAt
			state = "started"
		} else {
			reason = snapshot.waitReasons[message.ToAgentID]
		}
		result[message.ToAgentID] = append(result[message.ToAgentID], workloadQueueSample{
			evidence: WorkloadEvidence{
				ID: message.ID, AgentID: message.ToAgentID, AgentName: agentNames[message.ToAgentID], Source: source,
				State: state, QueuedAt: queuedAtText, StartedAt: startedAtText, WaitMs: maxDurationMillis(end.Sub(queuedAt)),
				WaitReason: reason, EvidenceHref: "#messages",
			},
			complete: complete, includeInNewWork: source != "continuation",
		})
	}

	attemptsByInbox := map[string][]HandlingAttempt{}
	for _, attempt := range snapshot.attempts {
		attemptsByInbox[attempt.InboxItemID] = append(attemptsByInbox[attempt.InboxItemID], attempt)
	}
	for _, item := range snapshot.inbox {
		message, ok := snapshot.inboxMessages[item.MessageID]
		if !ok {
			continue
		}
		queuedAtText := message.ReceivedAt
		if queuedAtText == "" {
			queuedAtText = item.CreatedAt
		}
		queuedAt, ok := workloadTime(queuedAtText)
		if !ok {
			continue
		}
		startedAtText := firstInboxAttemptStart(attemptsByInbox[item.ID])
		startedAt, complete := workloadTime(startedAtText)
		backlog := item.State == "queued" || item.State == "deferred" || item.State == "handling"
		inSelectedWindow := !queuedAt.Before(start) && queuedAt.Before(selectedEnd)
		if (complete && !inSelectedWindow) || (!complete && !backlog) {
			continue
		}
		end := now
		state := item.State
		reason := "unrecorded"
		if complete {
			end = startedAt
			state = "started"
		} else if item.State == "deferred" {
			reason = "deferred_until"
		} else {
			reason = snapshot.waitReasons[item.AgentID]
		}
		result[item.AgentID] = append(result[item.AgentID], workloadQueueSample{
			evidence: WorkloadEvidence{
				ID: item.ID, AgentID: item.AgentID, AgentName: agentNames[item.AgentID], Source: "external", Provider: message.Origin,
				State: state, QueuedAt: queuedAtText, StartedAt: startedAtText, WaitMs: maxDurationMillis(end.Sub(queuedAt)),
				WaitReason: reason, EvidenceHref: "#inbox?item=" + item.ID,
			},
			complete: complete, includeInNewWork: true,
		})
	}

	for _, request := range snapshot.humanRequests {
		if request.AnsweredAt == "" {
			continue
		}
		queuedAt, ok := workloadTime(request.AnsweredAt)
		if !ok {
			continue
		}
		startedAt, complete := workloadTime(request.DeliveredAt)
		backlog := request.State == "answered" && (request.DeliveryStatus == "queued" || request.DeliveryStatus == "delivering")
		inSelectedWindow := !queuedAt.Before(start) && queuedAt.Before(selectedEnd)
		if (complete && !inSelectedWindow) || (!complete && !backlog) {
			continue
		}
		end := now
		state := request.DeliveryStatus
		reason := "unrecorded"
		if complete {
			end = startedAt
			state = "started"
		} else {
			reason = snapshot.waitReasons[request.AgentID]
		}
		result[request.AgentID] = append(result[request.AgentID], workloadQueueSample{
			evidence: WorkloadEvidence{
				ID: request.ID, AgentID: request.AgentID, AgentName: agentNames[request.AgentID], Source: "human_answer",
				State: state, QueuedAt: request.AnsweredAt, StartedAt: request.DeliveredAt, WaitMs: maxDurationMillis(end.Sub(queuedAt)),
				WaitReason: reason, EvidenceHref: "#needs-you?request=" + request.ID,
			},
			complete: complete, includeInNewWork: true,
		})
	}
	return result
}

func firstAgentMessageStart(message AgentMessage) string {
	first := message.DeliveredAt
	for _, attempt := range message.HandlingAttempts {
		if attempt.StartedAt != "" && (first == "" || attempt.StartedAt < first) {
			first = attempt.StartedAt
		}
	}
	return first
}

func firstInboxAttemptStart(attempts []HandlingAttempt) string {
	first := ""
	for _, attempt := range attempts {
		if attempt.StartedAt != "" && (first == "" || attempt.StartedAt < first) {
			first = attempt.StartedAt
		}
	}
	return first
}

func summarizeWorkloadQueue(samples []workloadQueueSample) (WorkloadWaitStats, WorkloadBacklog, []WorkloadSource, []WorkloadEvidence) {
	waits := []int64{}
	backlog := WorkloadBacklog{}
	type sourceAccumulator struct {
		waits   []int64
		backlog WorkloadBacklog
	}
	bySource := map[string]*sourceAccumulator{}
	evidence := make([]WorkloadEvidence, 0, len(samples))
	for _, sample := range samples {
		accumulator := bySource[sample.evidence.Source]
		if accumulator == nil {
			accumulator = &sourceAccumulator{}
			bySource[sample.evidence.Source] = accumulator
		}
		if sample.complete {
			accumulator.waits = append(accumulator.waits, sample.evidence.WaitMs)
			if sample.includeInNewWork {
				waits = append(waits, sample.evidence.WaitMs)
			}
		} else {
			accumulator.backlog.Count++
			if sample.evidence.WaitMs > accumulator.backlog.OldestMs {
				accumulator.backlog.OldestMs = sample.evidence.WaitMs
			}
			if sample.includeInNewWork {
				backlog.Count++
				if sample.evidence.WaitMs > backlog.OldestMs {
					backlog.OldestMs = sample.evidence.WaitMs
				}
			}
		}
		evidence = append(evidence, sample.evidence)
	}
	sources := make([]WorkloadSource, 0, len(bySource))
	for source, accumulator := range bySource {
		sources = append(sources, WorkloadSource{Source: source, Wait: workloadWaitStats(accumulator.waits), Backlog: accumulator.backlog})
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Source < sources[j].Source })
	sort.SliceStable(evidence, func(i, j int) bool {
		if (evidence[i].StartedAt == "") != (evidence[j].StartedAt == "") {
			return evidence[i].StartedAt == ""
		}
		if evidence[i].WaitMs != evidence[j].WaitMs {
			return evidence[i].WaitMs > evidence[j].WaitMs
		}
		return evidence[i].QueuedAt > evidence[j].QueuedAt
	})
	if len(evidence) > 8 {
		evidence = evidence[:8]
	}
	return workloadWaitStats(waits), backlog, sources, evidence
}

func workloadWaitStats(values []int64) WorkloadWaitStats {
	if len(values) == 0 {
		return WorkloadWaitStats{}
	}
	copy := append([]int64(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	return WorkloadWaitStats{
		Samples: len(copy), P50Ms: workloadPercentile(copy, 0.5), P90Ms: workloadPercentile(copy, 0.9), MaxMs: copy[len(copy)-1],
	}
}

func workloadPercentile(sortedValues []int64, percentile float64) int64 {
	if len(sortedValues) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sortedValues))*percentile)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sortedValues) {
		index = len(sortedValues) - 1
	}
	return sortedValues[index]
}

type workloadInterval struct {
	start time.Time
	end   time.Time
}

func workloadIntervals(activity []rollout.TurnActivity, start, now time.Time) []workloadInterval {
	intervals := []workloadInterval{}
	for _, turn := range activity {
		turnStart, ok := workloadTime(turn.StartedAt)
		if !ok {
			continue
		}
		turnEnd := now
		if parsed, ok := workloadTime(turn.EndedAt); ok {
			turnEnd = parsed
		}
		if turnStart.Before(start) {
			turnStart = start
		}
		if turnEnd.After(now) {
			turnEnd = now
		}
		if turnEnd.After(turnStart) {
			intervals = append(intervals, workloadInterval{start: turnStart, end: turnEnd})
		}
	}
	return mergeWorkloadIntervals(intervals)
}

func mergeWorkloadIntervals(intervals []workloadInterval) []workloadInterval {
	if len(intervals) < 2 {
		return intervals
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	merged := []workloadInterval{intervals[0]}
	for _, interval := range intervals[1:] {
		last := &merged[len(merged)-1]
		if !interval.start.After(last.end) {
			if interval.end.After(last.end) {
				last.end = interval.end
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

func clipWorkloadIntervals(intervals []workloadInterval, start, end time.Time) []workloadInterval {
	clipped := []workloadInterval{}
	for _, interval := range intervals {
		clippedStart, clippedEnd := interval.start, interval.end
		if clippedStart.Before(start) {
			clippedStart = start
		}
		if clippedEnd.After(end) {
			clippedEnd = end
		}
		if clippedEnd.After(clippedStart) {
			clipped = append(clipped, workloadInterval{start: clippedStart, end: clippedEnd})
		}
	}
	return clipped
}

func intervalDurationSeconds(intervals []workloadInterval) int64 {
	var total int64
	for _, interval := range intervals {
		total += maxDurationSeconds(interval.end.Sub(interval.start))
	}
	return total
}

func emptyWorkloadDays(start time.Time, days int) []WorkloadDay {
	result := make([]WorkloadDay, 0, days)
	for index := 0; index < days; index++ {
		result = append(result, WorkloadDay{Date: start.AddDate(0, 0, index).Format("2006-01-02")})
	}
	return result
}

func workloadTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func maxDurationMillis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func maxDurationSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64(duration.Seconds())
}

func workloadPercent(value, total int64) float64 {
	if value <= 0 || total <= 0 {
		return 0
	}
	result := float64(value) * 100 / float64(total)
	if result > 100 {
		return 100
	}
	return result
}
