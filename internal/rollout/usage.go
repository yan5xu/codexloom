package rollout

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
)

// TokenUsage is the non-overlapping token accounting for one model call or a
// group of calls. CachedInputTokens is part of InputTokens, and
// ReasoningOutputTokens is part of OutputTokens.
type TokenUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
	Calls                 int64 `json:"calls"`
}

func (u *TokenUsage) Add(other TokenUsage) {
	u.InputTokens += other.InputTokens
	u.CachedInputTokens += other.CachedInputTokens
	u.OutputTokens += other.OutputTokens
	u.ReasoningOutputTokens += other.ReasoningOutputTokens
	u.TotalTokens += other.TotalTokens
	u.Calls += other.Calls
}

func (u TokenUsage) Empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0
}

type UsageEvent struct {
	Timestamp string     `json:"timestamp"`
	TurnID    string     `json:"turnId,omitempty"`
	Model     string     `json:"model,omitempty"`
	Usage     TokenUsage `json:"usage"`
}

type TurnUsage struct {
	TurnID        string     `json:"turnId"`
	Model         string     `json:"model,omitempty"`
	Usage         TokenUsage `json:"usage"`
	LastUpdatedAt string     `json:"lastUpdatedAt,omitempty"`
}

// TurnActivity is the execution interval Codex records in the rollout. An
// empty EndedAt means the Turn was still active at the end of the parsed file.
type TurnActivity struct {
	TurnID      string `json:"turnId"`
	StartedAt   string `json:"startedAt"`
	EndedAt     string `json:"endedAt,omitempty"`
	Status      string `json:"status"`
	InferredEnd bool   `json:"inferredEnd,omitempty"`
}

// UsageReport is reconstructed from token_count events in the Codex rollout.
// Events contain deltas derived from cumulative high-water marks, so duplicate
// or stale snapshots from reconnecting clients do not inflate the result.
type UsageReport struct {
	Lifetime           TokenUsage     `json:"lifetime"`
	Events             []UsageEvent   `json:"events"`
	Turns              []TurnUsage    `json:"turns"`
	Activity           []TurnActivity `json:"activity"`
	LatestCall         TokenUsage     `json:"latestCall"`
	LatestModel        string         `json:"latestModel,omitempty"`
	ContextInputTokens int64          `json:"contextInputTokens"`
	ModelContextWindow int64          `json:"modelContextWindow"`
	LastUpdatedAt      string         `json:"lastUpdatedAt,omitempty"`
}

type usageCacheEntry struct {
	modTime int64
	parser  *usageParser
}

var usageCache = struct {
	sync.Mutex
	entries map[string]usageCacheEntry
}{entries: map[string]usageCacheEntry{}}

func ReadUsage(threadID string) (*UsageReport, error) {
	path, err := FindRollout(threadID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	usageCache.Lock()
	defer usageCache.Unlock()

	cached, ok := usageCache.entries[path]
	parser := cached.parser
	if !ok || parser == nil || info.Size() < parser.offset ||
		(info.Size() == parser.offset && cached.modTime != info.ModTime().UnixNano()) {
		parser = newUsageParser()
	}
	if parser.offset < info.Size() {
		if ok && parser == cached.parser {
			parser = cloneUsageParser(parser)
		}
		if err := readUsageInto(path, parser); err != nil {
			return nil, err
		}
		info, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
	}
	usageCache.entries[path] = usageCacheEntry{modTime: info.ModTime().UnixNano(), parser: parser}
	return parser.report, nil
}

func ReadUsageFile(path string) (*UsageReport, error) {
	parser := newUsageParser()
	if err := readUsageInto(path, parser); err != nil {
		return nil, err
	}
	return parser.report, nil
}

type usageParser struct {
	report        *UsageReport
	models        map[string]string
	turns         map[string]*TurnUsage
	turnOrder     []string
	activity      map[string]*TurnActivity
	activityOrder []string
	currentTurn   string
	highWater     TokenUsage
	offset        int64
	pending       []byte
}

func newUsageParser() *usageParser {
	return &usageParser{
		report: &UsageReport{Events: []UsageEvent{}, Turns: []TurnUsage{}, Activity: []TurnActivity{}},
		models: map[string]string{}, turns: map[string]*TurnUsage{}, turnOrder: []string{},
		activity: map[string]*TurnActivity{}, activityOrder: []string{},
	}
}

func readUsageInto(path string, parser *usageParser) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(parser.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			parser.offset += int64(len(line))
			if len(parser.pending) > 0 {
				parser.pending = append(parser.pending, line...)
				line = parser.pending
			}
			if line[len(line)-1] == '\n' {
				parser.consume(line)
				parser.pending = nil
			} else if len(parser.pending) == 0 {
				parser.pending = append([]byte(nil), line...)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	parser.rebuildTurns()
	return nil
}

func (p *usageParser) consume(line []byte) {
	var ln struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &ln) != nil {
		return
	}
	switch ln.Type {
	case "turn_context":
		var context struct {
			TurnID string `json:"turn_id"`
			Model  string `json:"model"`
		}
		if json.Unmarshal(ln.Payload, &context) == nil && context.TurnID != "" {
			p.models[context.TurnID] = context.Model
			if turn := p.turns[context.TurnID]; turn != nil {
				turn.Model = context.Model
			}
		}
	case "event_msg":
		p.consumeEvent(ln.Timestamp, ln.Payload)
	}
}

func (p *usageParser) consumeEvent(timestamp string, payload json.RawMessage) {
	var event struct {
		Type   string `json:"type"`
		TurnID string `json:"turn_id"`
		Info   struct {
			Total              rawTokenUsage `json:"total_token_usage"`
			Last               rawTokenUsage `json:"last_token_usage"`
			ModelContextWindow int64         `json:"model_context_window"`
		} `json:"info"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	if event.Type == "task_started" {
		if p.currentTurn != "" && p.currentTurn != event.TurnID {
			if current := p.activity[p.currentTurn]; current != nil && current.EndedAt == "" {
				current.EndedAt = timestamp
				current.Status = "interrupted"
				current.InferredEnd = true
			}
		}
		p.currentTurn = event.TurnID
		if event.TurnID != "" {
			if existing := p.activity[event.TurnID]; existing == nil {
				p.activity[event.TurnID] = &TurnActivity{TurnID: event.TurnID, StartedAt: timestamp, Status: "running"}
				p.activityOrder = append(p.activityOrder, event.TurnID)
			} else if existing.StartedAt == "" {
				existing.StartedAt = timestamp
			}
		}
		return
	}
	if event.Type == "task_complete" || event.Type == "turn_aborted" {
		turnID := event.TurnID
		if turnID == "" {
			turnID = p.currentTurn
		}
		if activity := p.activity[turnID]; activity != nil {
			activity.EndedAt = timestamp
			activity.Status = "completed"
			if event.Type == "turn_aborted" {
				activity.Status = "interrupted"
			}
		}
		if turnID == p.currentTurn {
			p.currentTurn = ""
		}
		return
	}
	if event.Type != "token_count" {
		return
	}

	total := event.Info.Total.tokenUsage()
	delta := cumulativeDelta(&p.highWater, total)
	if delta.Empty() {
		return
	}
	delta.Calls = 1
	last := event.Info.Last.tokenUsage()
	model := p.models[p.currentTurn]
	p.report.LatestCall = last
	p.report.ContextInputTokens = last.InputTokens
	p.report.ModelContextWindow = event.Info.ModelContextWindow
	p.report.LastUpdatedAt = timestamp
	if model != "" {
		p.report.LatestModel = model
	}
	p.report.Lifetime.Add(delta)
	p.report.Events = append(p.report.Events, UsageEvent{
		Timestamp: timestamp, TurnID: p.currentTurn, Model: model, Usage: delta,
	})
	if p.currentTurn == "" {
		return
	}
	turn := p.turns[p.currentTurn]
	if turn == nil {
		turn = &TurnUsage{TurnID: p.currentTurn, Model: model}
		p.turns[p.currentTurn] = turn
		p.turnOrder = append(p.turnOrder, p.currentTurn)
	}
	turn.Usage.Add(delta)
	turn.LastUpdatedAt = timestamp
}

func (p *usageParser) rebuildTurns() {
	p.report.Turns = p.report.Turns[:0]
	for _, id := range p.turnOrder {
		turn := p.turns[id]
		if turn.Model == "" {
			turn.Model = p.models[id]
		}
		p.report.Turns = append(p.report.Turns, *turn)
	}
	p.report.Activity = p.report.Activity[:0]
	for _, id := range p.activityOrder {
		if activity := p.activity[id]; activity != nil {
			p.report.Activity = append(p.report.Activity, *activity)
		}
	}
}

type rawTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

func (u rawTokenUsage) tokenUsage() TokenUsage {
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return TokenUsage{
		InputTokens: u.InputTokens, CachedInputTokens: u.CachedInputTokens,
		OutputTokens: u.OutputTokens, ReasoningOutputTokens: u.ReasoningOutputTokens,
		TotalTokens: total,
	}
}

func cumulativeDelta(highWater *TokenUsage, current TokenUsage) TokenUsage {
	delta := TokenUsage{
		InputTokens:           positiveDelta(current.InputTokens, highWater.InputTokens),
		CachedInputTokens:     positiveDelta(current.CachedInputTokens, highWater.CachedInputTokens),
		OutputTokens:          positiveDelta(current.OutputTokens, highWater.OutputTokens),
		ReasoningOutputTokens: positiveDelta(current.ReasoningOutputTokens, highWater.ReasoningOutputTokens),
		TotalTokens:           positiveDelta(current.TotalTokens, highWater.TotalTokens),
	}
	highWater.InputTokens = max(highWater.InputTokens, current.InputTokens)
	highWater.CachedInputTokens = max(highWater.CachedInputTokens, current.CachedInputTokens)
	highWater.OutputTokens = max(highWater.OutputTokens, current.OutputTokens)
	highWater.ReasoningOutputTokens = max(highWater.ReasoningOutputTokens, current.ReasoningOutputTokens)
	highWater.TotalTokens = max(highWater.TotalTokens, current.TotalTokens)
	return delta
}

func positiveDelta(current, highWater int64) int64 {
	if current <= highWater {
		return 0
	}
	return current - highWater
}

func cloneUsageReport(report *UsageReport) *UsageReport {
	if report == nil {
		return nil
	}
	copy := *report
	copy.Events = append([]UsageEvent(nil), report.Events...)
	copy.Turns = append([]TurnUsage(nil), report.Turns...)
	copy.Activity = append([]TurnActivity(nil), report.Activity...)
	return &copy
}

func cloneUsageParser(parser *usageParser) *usageParser {
	copy := *parser
	copy.report = cloneUsageReport(parser.report)
	copy.models = make(map[string]string, len(parser.models))
	for id, model := range parser.models {
		copy.models[id] = model
	}
	copy.turns = make(map[string]*TurnUsage, len(parser.turns))
	for id, turn := range parser.turns {
		turnCopy := *turn
		copy.turns[id] = &turnCopy
	}
	copy.turnOrder = append([]string(nil), parser.turnOrder...)
	copy.activity = make(map[string]*TurnActivity, len(parser.activity))
	for id, activity := range parser.activity {
		activityCopy := *activity
		copy.activity[id] = &activityCopy
	}
	copy.activityOrder = append([]string(nil), parser.activityOrder...)
	copy.pending = append([]byte(nil), parser.pending...)
	return &copy
}
