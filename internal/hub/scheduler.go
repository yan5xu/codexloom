package hub

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (h *Hub) persistSchedulesLocked() error {
	return h.st.SaveSchedules(h.schedules)
}

func (h *Hub) ListSchedules() []Schedule {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Schedule, 0, len(h.schedules))
	for _, s := range h.schedules {
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NextRunAt == "" && out[j].NextRunAt != "" {
			return false
		}
		if out[i].NextRunAt != "" && out[j].NextRunAt == "" {
			return true
		}
		if out[i].NextRunAt != out[j].NextRunAt {
			return out[i].NextRunAt < out[j].NextRunAt
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (h *Hub) GetSchedule(id string) (Schedule, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.schedules[strings.TrimSpace(id)]
	if s == nil {
		return Schedule{}, errf(404, "schedule not found: %s", id)
	}
	return *s, nil
}

func (h *Hub) CreateSchedule(p ScheduleParams) (Schedule, error) {
	s, err := h.buildSchedule(p, time.Now().UTC())
	if err != nil {
		return Schedule{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.resolveLocked(s.To) == nil {
		return Schedule{}, errf(404, "target agent not found: %s", s.To)
	}
	for _, existing := range h.schedules {
		if existing.Name == s.Name {
			return Schedule{}, errf(409, "schedule %q already exists", s.Name)
		}
	}
	h.schedules[s.ID] = &s
	if err := h.persistSchedulesLocked(); err != nil {
		delete(h.schedules, s.ID)
		return Schedule{}, errf(500, "persist schedule: %s", err)
	}
	h.emitGlobalLocked("loom/schedule", map[string]any{"schedule": s})
	return s, nil
}

func (h *Hub) buildSchedule(p ScheduleParams, now time.Time) (Schedule, error) {
	name := strings.TrimSpace(p.Name)
	to := strings.TrimSpace(p.To)
	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	response := strings.TrimSpace(p.Response)
	at := strings.TrimSpace(p.At)
	cron := strings.TrimSpace(p.Cron)
	tz := strings.TrimSpace(p.Timezone)
	if name == "" {
		return Schedule{}, errf(400, "name is required")
	}
	if !nameRe.MatchString(name) {
		return Schedule{}, errf(400, "name must match %s", nameRe.String())
	}
	if to == "" {
		return Schedule{}, errf(400, "to is required")
	}
	if subject == "" {
		return Schedule{}, errf(400, "subject is required")
	}
	if body == "" {
		return Schedule{}, errf(400, "body is required")
	}
	if response == "" {
		response = "required"
	}
	if response != "required" && response != "none" {
		return Schedule{}, errf(400, "response must be required or none")
	}
	if (at == "") == (cron == "") {
		return Schedule{}, errf(400, "exactly one of at or cron is required")
	}
	if tz == "" {
		tz = schedulerDefaultTZ
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	nextRunAt := ""
	if at != "" {
		t, err := parseScheduleTime(at)
		if err != nil {
			return Schedule{}, errf(400, "invalid at: %s", err)
		}
		nextRunAt = t.UTC().Format(time.RFC3339Nano)
	} else {
		next, err := nextCronAfter(cron, tz, now)
		if err != nil {
			return Schedule{}, err
		}
		nextRunAt = next.UTC().Format(time.RFC3339Nano)
	}
	ts := now.Format(time.RFC3339Nano)
	return Schedule{
		ID:        newScheduleID(),
		Name:      name,
		To:        to,
		Subject:   subject,
		Body:      body,
		Response:  response,
		At:        at,
		Cron:      cron,
		Timezone:  tz,
		Enabled:   enabled,
		NextRunAt: nextRunAt,
		CreatedAt: ts,
		UpdatedAt: ts,
	}, nil
}

func (h *Hub) SetScheduleEnabled(id string, enabled bool) (Schedule, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.schedules[strings.TrimSpace(id)]
	if s == nil {
		return Schedule{}, errf(404, "schedule not found: %s", id)
	}
	previous := *s
	s.Enabled = enabled
	if enabled && s.NextRunAt == "" {
		next, err := h.nextRunForScheduleLocked(s, time.Now().UTC())
		if err != nil {
			return Schedule{}, err
		}
		s.NextRunAt = next
	}
	s.UpdatedAt = now()
	if err := h.persistSchedulesLocked(); err != nil {
		*s = previous
		return Schedule{}, errf(500, "persist schedule: %s", err)
	}
	cp := *s
	h.emitGlobalLocked("loom/schedule", map[string]any{"schedule": cp})
	return cp, nil
}

func (h *Hub) DeleteSchedule(id string) (Schedule, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.schedules[strings.TrimSpace(id)]
	if s == nil {
		return Schedule{}, errf(404, "schedule not found: %s", id)
	}
	cp := *s
	delete(h.schedules, s.ID)
	if err := h.persistSchedulesLocked(); err != nil {
		h.schedules[s.ID] = s
		return Schedule{}, errf(500, "persist schedule deletion: %s", err)
	}
	h.emitGlobalLocked("loom/schedule-deleted", map[string]any{"schedule": cp})
	return cp, nil
}

func (h *Hub) RunSchedule(id string) (Schedule, error) {
	h.mu.Lock()
	s := h.schedules[strings.TrimSpace(id)]
	if s == nil {
		h.mu.Unlock()
		return Schedule{}, errf(404, "schedule not found: %s", id)
	}
	runAt := now()
	message, err := h.ensureScheduleMessageLocked(s, "manual:"+runAt)
	if err != nil {
		h.mu.Unlock()
		return Schedule{}, err
	}
	previous := *s
	s.LastRunAt = runAt
	s.LastMessageID = message.ID
	s.LastError = ""
	s.UpdatedAt = runAt
	if s.Cron != "" {
		next, err := h.nextRunForScheduleLocked(s, time.Now().UTC())
		if err != nil {
			s.LastError = err.Error()
		} else {
			s.NextRunAt = next
		}
	}
	if err := h.persistSchedulesLocked(); err != nil {
		*s = previous
		h.mu.Unlock()
		return Schedule{}, errf(500, "persist manual schedule run: %s", err)
	}
	cp := *s
	h.emitGlobalLocked("loom/schedule", map[string]any{"schedule": cp})
	h.mu.Unlock()
	h.deliverNextQueuedForTarget(message.ToAgentID, defaultInactivity)
	return cp, nil
}

func (h *Hub) schedulerLoop() {
	h.recomputeMissingScheduleRuns()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.runDueSchedules(time.Now().UTC())
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) recomputeMissingScheduleRuns() {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	previous := make(map[string]Schedule)
	now := time.Now().UTC()
	for _, s := range h.schedules {
		if s.NextRunAt != "" || s.At != "" && !s.Enabled {
			continue
		}
		previous[s.ID] = *s
		next, err := h.nextRunForScheduleLocked(s, now)
		if err != nil {
			s.LastError = err.Error()
			s.UpdatedAt = now.Format(time.RFC3339Nano)
			changed = true
			continue
		}
		s.NextRunAt = next
		s.UpdatedAt = now.Format(time.RFC3339Nano)
		changed = true
	}
	if changed {
		if err := h.persistSchedulesLocked(); err != nil {
			for id, snapshot := range previous {
				if schedule := h.schedules[id]; schedule != nil {
					*schedule = snapshot
				}
			}
			log.Printf("[codex-loom] persist recomputed schedules: %v", err)
		}
	}
}

func (h *Hub) runDueSchedules(t time.Time) {
	targets := h.collectDueSchedules(t)
	for _, target := range targets {
		h.deliverNextQueuedForTarget(target, defaultInactivity)
	}
}

func (h *Hub) collectDueSchedules(t time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	targets := []string{}
	nowText := t.UTC().Format(time.RFC3339Nano)
	for _, s := range h.schedules {
		if !s.Enabled || s.NextRunAt == "" {
			continue
		}
		next, err := parseScheduleTime(s.NextRunAt)
		if err != nil {
			previous := *s
			s.LastError = "invalid nextRunAt: " + err.Error()
			s.UpdatedAt = nowText
			if saveErr := h.persistSchedulesLocked(); saveErr != nil {
				*s = previous
			}
			continue
		}
		if next.After(t) {
			continue
		}
		scheduledAt := s.NextRunAt
		message, err := h.ensureScheduleMessageLocked(s, scheduledAt)
		if err != nil {
			previous := *s
			s.LastError = err.Error()
			s.UpdatedAt = nowText
			if saveErr := h.persistSchedulesLocked(); saveErr != nil {
				*s = previous
			}
			continue
		}
		previous := *s
		s.LastRunAt = scheduledAt
		s.LastMessageID = message.ID
		s.LastError = ""
		s.UpdatedAt = nowText
		if s.At != "" {
			s.Enabled = false
			s.NextRunAt = ""
		} else {
			nextRun, err := h.nextRunForScheduleLocked(s, t)
			if err != nil {
				s.LastError = err.Error()
				s.Enabled = false
				s.NextRunAt = ""
			} else {
				s.NextRunAt = nextRun
			}
		}
		if err := h.persistSchedulesLocked(); err != nil {
			*s = previous
			continue
		}
		targets = append(targets, message.ToAgentID)
		cp := *s
		h.emitGlobalLocked("loom/schedule", map[string]any{"schedule": cp})
	}
	return targets
}

func (h *Hub) ensureScheduleMessageLocked(schedule *Schedule, scheduledAt string) (*AgentMessage, error) {
	for _, id := range h.commOrder {
		message := h.comms[id]
		if message != nil && message.ScheduleID == schedule.ID && message.ScheduledAt == scheduledAt {
			return message, nil
		}
	}
	target := h.resolveLocked(schedule.To)
	if target == nil {
		return nil, errf(404, "target agent not found: %s", schedule.To)
	}
	status := "closed"
	if schedule.Response == "required" {
		status = "open"
	}
	timestamp := now()
	message := AgentMessage{
		ID: newMessageID(), FromAgentID: schedulerAgentID, ToAgentID: target.ID,
		From: schedulerIdentity, To: target.Name, Subject: schedule.Subject, Body: schedule.Body,
		Response: schedule.Response, Status: status, DeliveryStatus: "queued", HandlingStatus: "pending",
		ScheduleID: schedule.ID, ScheduledAt: scheduledAt, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := h.commitAgentMessageLocked(message); err != nil {
		return nil, errf(500, "persist scheduled occurrence: %s", err)
	}
	copy := message
	return &copy, nil
}

func (h *Hub) nextRunForScheduleLocked(s *Schedule, after time.Time) (string, error) {
	if s.At != "" {
		t, err := parseScheduleTime(s.At)
		if err != nil {
			return "", err
		}
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	next, err := nextCronAfter(s.Cron, s.Timezone, after)
	if err != nil {
		return "", err
	}
	return next.UTC().Format(time.RFC3339Nano), nil
}

func parseScheduleTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func newScheduleID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sched_%d", time.Now().UnixNano())
	}
	return "sched_" + hex.EncodeToString(b)
}

type cronSpec struct {
	minute     map[int]bool
	hour       map[int]bool
	day        map[int]bool
	month      map[int]bool
	weekday    map[int]bool
	anyDay     bool
	anyWeekday bool
}

func nextCronAfter(expr, timezone string, after time.Time) (time.Time, error) {
	spec, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, errf(400, "invalid timezone: %s", err)
	}
	cur := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	deadline := cur.AddDate(5, 0, 0)
	for cur.Before(deadline) {
		if spec.matches(cur) {
			return cur.UTC(), nil
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}, errf(400, "cron has no run within 5 years")
}

func parseCron(expr string) (cronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSpec{}, errf(400, "cron must have 5 fields: minute hour day month weekday")
	}
	minute, _, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return cronSpec{}, errf(400, "invalid cron minute: %s", err)
	}
	hour, _, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return cronSpec{}, errf(400, "invalid cron hour: %s", err)
	}
	day, anyDay, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return cronSpec{}, errf(400, "invalid cron day: %s", err)
	}
	month, _, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return cronSpec{}, errf(400, "invalid cron month: %s", err)
	}
	weekday, anyWeekday, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return cronSpec{}, errf(400, "invalid cron weekday: %s", err)
	}
	return cronSpec{
		minute:     minute,
		hour:       hour,
		day:        day,
		month:      month,
		weekday:    weekday,
		anyDay:     anyDay,
		anyWeekday: anyWeekday,
	}, nil
}

func parseCronField(raw string, min, max int, sundayAlias bool) (map[int]bool, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, fmt.Errorf("empty field")
	}
	values := map[int]bool{}
	any := false
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("empty list item")
		}
		step := 1
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return nil, false, fmt.Errorf("invalid step %q", part)
			}
			part = pieces[0]
			n, err := strconv.Atoi(pieces[1])
			if err != nil || n <= 0 {
				return nil, false, fmt.Errorf("invalid step %q", pieces[1])
			}
			step = n
		}
		start, end := min, max
		if part == "*" {
			any = true
		} else if strings.Contains(part, "-") {
			pieces := strings.Split(part, "-")
			if len(pieces) != 2 {
				return nil, false, fmt.Errorf("invalid range %q", part)
			}
			var err error
			start, err = strconv.Atoi(pieces[0])
			if err != nil {
				return nil, false, fmt.Errorf("invalid range start %q", pieces[0])
			}
			end, err = strconv.Atoi(pieces[1])
			if err != nil {
				return nil, false, fmt.Errorf("invalid range end %q", pieces[1])
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, false, fmt.Errorf("invalid value %q", part)
			}
			start, end = n, n
		}
		if start < min || end > max || start > end {
			return nil, false, fmt.Errorf("value out of range %d-%d", min, max)
		}
		for n := start; n <= end; n += step {
			if sundayAlias && n == 7 {
				values[0] = true
			} else {
				values[n] = true
			}
		}
	}
	return values, any, nil
}

func (s cronSpec) matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	dayMatch := s.day[t.Day()]
	weekdayMatch := s.weekday[int(t.Weekday())]
	switch {
	case s.anyDay && s.anyWeekday:
		return true
	case s.anyDay:
		return weekdayMatch
	case s.anyWeekday:
		return dayMatch
	default:
		return dayMatch || weekdayMatch
	}
}
