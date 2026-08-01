package hub

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yan5xu/codex-loom/internal/rollout"
)

type UsageDay struct {
	Date  string             `json:"date"`
	Usage rollout.TokenUsage `json:"usage"`
}

type UsageModel struct {
	ProviderID string             `json:"providerId"`
	Model      string             `json:"model"`
	Usage      rollout.TokenUsage `json:"usage"`
}

type ContextUsage struct {
	InputTokens  int64   `json:"inputTokens"`
	WindowTokens int64   `json:"windowTokens"`
	UsedPercent  float64 `json:"usedPercent"`
}

type AgentUsage struct {
	AgentID          string             `json:"agentId"`
	AgentName        string             `json:"agentName"`
	ThreadID         string             `json:"threadId,omitempty"`
	Status           string             `json:"status"`
	Available        bool               `json:"available"`
	Lifetime         rollout.TokenUsage `json:"lifetime"`
	Period           rollout.TokenUsage `json:"period"`
	Previous         rollout.TokenUsage `json:"previous"`
	Today            rollout.TokenUsage `json:"today"`
	LatestCall       rollout.TokenUsage `json:"latestCall"`
	LatestProviderID string             `json:"latestProviderId,omitempty"`
	LatestModel      string             `json:"latestModel,omitempty"`
	CacheHitPercent  float64            `json:"cacheHitPercent"`
	Context          ContextUsage       `json:"context"`
	Daily            []UsageDay         `json:"daily"`
	Models           []UsageModel       `json:"models"`
	LastUpdatedAt    string             `json:"lastUpdatedAt,omitempty"`
}

type UsageOverview struct {
	Days          int                `json:"days"`
	Since         string             `json:"since"`
	Through       string             `json:"through"`
	Timezone      string             `json:"timezone"`
	GeneratedAt   string             `json:"generatedAt"`
	Live          bool               `json:"live"`
	TrackedAgents int                `json:"trackedAgents"`
	Lifetime      rollout.TokenUsage `json:"lifetime"`
	Period        rollout.TokenUsage `json:"period"`
	Previous      rollout.TokenUsage `json:"previous"`
	Today         rollout.TokenUsage `json:"today"`
	Daily         []UsageDay         `json:"daily"`
	Models        []UsageModel       `json:"models"`
	Agents        []AgentUsage       `json:"agents"`
}

type agentUsageCacheEntry struct {
	report *rollout.UsageReport
	usage  AgentUsage
	access uint64
}

const maxAgentUsageCacheEntries = 512

var agentUsageCache = struct {
	sync.Mutex
	entries map[string]agentUsageCacheEntry
	clock   uint64
}{entries: map[string]agentUsageCacheEntry{}}

func cachedAgentUsage(key string, report *rollout.UsageReport) (AgentUsage, bool) {
	agentUsageCache.Lock()
	defer agentUsageCache.Unlock()
	cached, ok := agentUsageCache.entries[key]
	if !ok {
		return AgentUsage{}, false
	}
	if cached.report != report {
		delete(agentUsageCache.entries, key)
		return AgentUsage{}, false
	}
	agentUsageCache.clock++
	cached.access = agentUsageCache.clock
	agentUsageCache.entries[key] = cached
	return cached.usage, true
}

func cacheAgentUsage(key string, report *rollout.UsageReport, usage AgentUsage) {
	agentUsageCache.Lock()
	defer agentUsageCache.Unlock()
	agentUsageCache.clock++
	agentUsageCache.entries[key] = agentUsageCacheEntry{report: report, usage: usage, access: agentUsageCache.clock}
	if len(agentUsageCache.entries) <= maxAgentUsageCacheEntries {
		return
	}
	oldestKey := ""
	oldestAccess := ^uint64(0)
	for candidate, entry := range agentUsageCache.entries {
		if entry.access < oldestAccess {
			oldestKey = candidate
			oldestAccess = entry.access
		}
	}
	delete(agentUsageCache.entries, oldestKey)
}

func normalizeUsageDays(days int) int {
	if days <= 0 {
		return 7
	}
	if days > 90 {
		return 90
	}
	return days
}

func (h *Hub) AgentTokenUsage(key string, days int) (AgentUsage, error) {
	now := time.Now()
	start, through := usageRange(now, normalizeUsageDays(days))
	return h.AgentTokenUsageRange(key, start, through.AddDate(0, 0, 1))
}

func (h *Hub) AgentTokenUsageRange(key string, start, endExclusive time.Time) (AgentUsage, error) {
	h.mu.Lock()
	meta := h.resolveLocked(key)
	if meta == nil {
		h.mu.Unlock()
		return AgentUsage{}, errf(404, "agent not found: %s", key)
	}
	agent := h.viewLocked(meta)
	h.mu.Unlock()
	return buildAgentUsageRange(agent, start, endExclusive, time.Now().In(start.Location())), nil
}

func (h *Hub) TokenUsageOverview(days int) UsageOverview {
	days = normalizeUsageDays(days)
	now := time.Now()
	start, through := usageRange(now, days)
	return h.TokenUsageOverviewRange(start, through.AddDate(0, 0, 1))
}

func (h *Hub) TokenUsageOverviewRange(start, endExclusive time.Time) UsageOverview {
	now := time.Now().In(start.Location())
	days := usageCalendarDays(start, endExclusive)
	through := endExclusive.AddDate(0, 0, -1)
	overview := UsageOverview{
		Days: days, Since: start.Format("2006-01-02"), Through: through.Format("2006-01-02"),
		Timezone: usageTimezoneLabel(now), GeneratedAt: now.UTC().Format(time.RFC3339Nano), Live: usageRangeIsLive(endExclusive, now),
		Daily: emptyUsageDays(start, days), Models: []UsageModel{}, Agents: []AgentUsage{},
	}
	modelUsage := map[string]rollout.TokenUsage{}
	h.mu.Lock()
	agents := make([]AgentView, 0, len(h.agents))
	for _, meta := range h.agents {
		agents = append(agents, h.viewLocked(meta))
	}
	h.mu.Unlock()
	for _, agent := range agents {
		usage := buildAgentUsageRange(agent, start, endExclusive, now)
		overview.Agents = append(overview.Agents, usage)
		if usage.Available {
			overview.TrackedAgents++
		}
		overview.Lifetime.Add(usage.Lifetime)
		overview.Period.Add(usage.Period)
		overview.Previous.Add(usage.Previous)
		overview.Today.Add(usage.Today)
		for i := range overview.Daily {
			if i < len(usage.Daily) {
				overview.Daily[i].Usage.Add(usage.Daily[i].Usage)
			}
		}
		for _, model := range usage.Models {
			key := model.ProviderID + "\x00" + model.Model
			current := modelUsage[key]
			current.Add(model.Usage)
			modelUsage[key] = current
		}
	}
	for key, usage := range modelUsage {
		parts := strings.SplitN(key, "\x00", 2)
		overview.Models = append(overview.Models, UsageModel{ProviderID: parts[0], Model: parts[1], Usage: usage})
	}
	sort.SliceStable(overview.Models, func(i, j int) bool {
		return overview.Models[i].Usage.TotalTokens > overview.Models[j].Usage.TotalTokens
	})
	sort.SliceStable(overview.Agents, func(i, j int) bool {
		if overview.Agents[i].Period.TotalTokens != overview.Agents[j].Period.TotalTokens {
			return overview.Agents[i].Period.TotalTokens > overview.Agents[j].Period.TotalTokens
		}
		if overview.Agents[i].Lifetime.TotalTokens != overview.Agents[j].Lifetime.TotalTokens {
			return overview.Agents[i].Lifetime.TotalTokens > overview.Agents[j].Lifetime.TotalTokens
		}
		return overview.Agents[i].AgentName < overview.Agents[j].AgentName
	})
	return overview
}

func buildAgentUsage(agent AgentView, days int, now time.Time) AgentUsage {
	days = normalizeUsageDays(days)
	start, through := usageRange(now, days)
	return buildAgentUsageRange(agent, start, through.AddDate(0, 0, 1), now)
}

func buildAgentUsageRange(agent AgentView, start, endExclusive, now time.Time) AgentUsage {
	days := usageCalendarDays(start, endExclusive)
	now = now.In(start.Location())
	endLimit := endExclusive
	if now.Before(endLimit) {
		endLimit = now
	}
	previousStart, previousEnd := previousUsageRange(start, endExclusive, now)
	todayStart, _ := usageRange(now, 1)
	result := AgentUsage{
		AgentID: agent.ID, AgentName: agent.Name, ThreadID: agent.ThreadID, Status: agent.Status,
		LatestProviderID: publicProviderID(agent.ProviderID), Daily: emptyUsageDays(start, days), Models: []UsageModel{},
	}
	if strings.TrimSpace(agent.ThreadID) == "" {
		return result
	}
	report, err := rollout.ReadUsage(agent.ThreadID)
	if err != nil {
		return result
	}
	historyVersion := strconv.Itoa(len(agent.ProviderHistory))
	if len(agent.ProviderHistory) > 0 {
		historyVersion += ":" + agent.ProviderHistory[len(agent.ProviderHistory)-1].SwitchedAt
	}
	cacheKey := agent.ThreadID + "\x00" + strconv.Itoa(days) + "\x00" + start.Format("2006-01-02") + "\x00" + endExclusive.Format("2006-01-02") + "\x00" + now.Location().String() + "\x00" + publicProviderID(agent.ProviderID) + "\x00" + historyVersion
	if cached, ok := cachedAgentUsage(cacheKey, report); ok {
		result = cached
		result.AgentID = agent.ID
		result.AgentName = agent.Name
		result.Status = agent.Status
		return result
	}
	result.Available = true
	result.Lifetime = report.Lifetime
	result.LatestCall = report.LatestCall
	result.LatestModel = report.LatestModel
	result.LastUpdatedAt = report.LastUpdatedAt
	result.LatestProviderID = publicProviderID(agent.ProviderID)
	if latestAt, parseErr := time.Parse(time.RFC3339Nano, report.LastUpdatedAt); parseErr == nil {
		result.LatestProviderID = providerAt(agent.Agent, latestAt)
	}
	result.Context = ContextUsage{
		InputTokens:  report.ContextInputTokens,
		WindowTokens: report.ModelContextWindow,
		UsedPercent:  percent(report.ContextInputTokens, report.ModelContextWindow),
	}

	dailyIndex := map[string]int{}
	for index, day := range result.Daily {
		dailyIndex[day.Date] = index
	}
	models := map[string]rollout.TokenUsage{}
	for _, event := range report.Events {
		timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			continue
		}
		local := timestamp.In(now.Location())
		if !local.Before(start) && local.Before(endLimit) {
			result.Period.Add(event.Usage)
			if index, ok := dailyIndex[local.Format("2006-01-02")]; ok {
				result.Daily[index].Usage.Add(event.Usage)
			}
			model := event.Model
			if model == "" {
				model = "unknown"
			}
			providerID := providerAt(agent.Agent, timestamp)
			key := providerID + "\x00" + model
			usage := models[key]
			usage.Add(event.Usage)
			models[key] = usage
		}
		if !local.Before(previousStart) && local.Before(previousEnd) {
			result.Previous.Add(event.Usage)
		}
		if !local.Before(todayStart) && !local.After(now) {
			result.Today.Add(event.Usage)
		}
	}
	result.CacheHitPercent = percent(result.Period.CachedInputTokens, result.Period.InputTokens)
	for key, usage := range models {
		parts := strings.SplitN(key, "\x00", 2)
		result.Models = append(result.Models, UsageModel{ProviderID: parts[0], Model: parts[1], Usage: usage})
	}
	sort.SliceStable(result.Models, func(i, j int) bool {
		return result.Models[i].Usage.TotalTokens > result.Models[j].Usage.TotalTokens
	})
	cacheAgentUsage(cacheKey, report, result)
	return result
}

func providerAt(agent Agent, timestamp time.Time) string {
	providerID := publicProviderID(agent.ProviderID)
	if len(agent.ProviderHistory) == 0 {
		return providerID
	}
	providerID = agent.ProviderHistory[0].PreviousProviderID
	if providerID == "" {
		providerID = "openai"
	}
	for _, change := range agent.ProviderHistory {
		switchedAt, err := time.Parse(time.RFC3339Nano, change.SwitchedAt)
		if err != nil || timestamp.Before(switchedAt) {
			continue
		}
		providerID = change.ProviderID
		if providerID == "" {
			providerID = "openai"
		}
	}
	return providerID
}

func usageCalendarDays(start, endExclusive time.Time) int {
	days := 0
	for cursor := start; cursor.Before(endExclusive); cursor = cursor.AddDate(0, 0, 1) {
		days++
	}
	if days < 1 {
		return 1
	}
	return days
}

func usageRangeIsLive(endExclusive, now time.Time) bool {
	finalDayStart := endExclusive.AddDate(0, 0, -1)
	return !now.Before(finalDayStart) && now.Before(endExclusive)
}

func previousUsageRange(start, endExclusive, now time.Time) (time.Time, time.Time) {
	days := usageCalendarDays(start, endExclusive)
	previousStart := start.AddDate(0, 0, -days)
	previousEnd := start
	if usageRangeIsLive(endExclusive, now) {
		elapsedToday := now.Sub(endExclusive.AddDate(0, 0, -1))
		previousEnd = start.AddDate(0, 0, -1).Add(elapsedToday)
	}
	return previousStart, previousEnd
}

func usageRange(now time.Time, days int) (time.Time, time.Time) {
	local := now.In(now.Location())
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	return today.AddDate(0, 0, -(days - 1)), today
}

func usageTimezoneLabel(now time.Time) string {
	location := now.Location().String()
	if location == "UTC" {
		return "UTC"
	}
	_, offset := now.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s (UTC%s%02d:%02d)", location, sign, offset/3600, offset%3600/60)
}

func emptyUsageDays(start time.Time, days int) []UsageDay {
	result := make([]UsageDay, 0, days)
	for i := 0; i < days; i++ {
		result = append(result, UsageDay{Date: start.AddDate(0, 0, i).Format("2006-01-02")})
	}
	return result
}

func percent(value, total int64) float64 {
	if value <= 0 || total <= 0 {
		return 0
	}
	result := float64(value) * 100 / float64(total)
	if result > 100 {
		return 100
	}
	return result
}
