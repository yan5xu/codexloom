package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEventActiveBytes  = 64 << 20
	defaultEventReplayEvents = 10_000
	defaultEventReplayBytes  = 32 << 20
	defaultEventArchives     = 2
	defaultEventArchiveBytes = 128 << 20
	defaultEventArchiveAge   = 7 * 24 * time.Hour
)

// EventLogPolicy bounds the derived live-event cache. Complete Thread history
// remains in the Codex rollout; these files only support recent SSE replay and
// operational diagnostics.
type EventLogPolicy struct {
	MaxActiveBytes  int64
	ReplayEvents    int
	MaxReplayBytes  int64
	MaxArchives     int
	MaxArchiveBytes int64
	MaxArchiveAge   time.Duration
}

func DefaultEventLogPolicy() EventLogPolicy {
	return EventLogPolicy{
		MaxActiveBytes:  defaultEventActiveBytes,
		ReplayEvents:    defaultEventReplayEvents,
		MaxReplayBytes:  defaultEventReplayBytes,
		MaxArchives:     defaultEventArchives,
		MaxArchiveBytes: defaultEventArchiveBytes,
		MaxArchiveAge:   defaultEventArchiveAge,
	}
}

func EventLogPolicyFromEnv() EventLogPolicy {
	policy := DefaultEventLogPolicy()
	policy.MaxActiveBytes = envPositiveInt64("CODEX_LOOM_EVENT_ACTIVE_MB", policy.MaxActiveBytes>>20) << 20
	policy.ReplayEvents = int(envPositiveInt64("CODEX_LOOM_EVENT_REPLAY_COUNT", int64(policy.ReplayEvents)))
	policy.MaxReplayBytes = envPositiveInt64("CODEX_LOOM_EVENT_REPLAY_MB", policy.MaxReplayBytes>>20) << 20
	policy.MaxArchives = int(envNonNegativeInt64("CODEX_LOOM_EVENT_ARCHIVES", int64(policy.MaxArchives)))
	policy.MaxArchiveBytes = envNonNegativeInt64("CODEX_LOOM_EVENT_ARCHIVE_MB", policy.MaxArchiveBytes>>20) << 20
	policy.MaxArchiveAge = time.Duration(envNonNegativeInt64("CODEX_LOOM_EVENT_ARCHIVE_DAYS", int64(policy.MaxArchiveAge/(24*time.Hour)))) * 24 * time.Hour
	return policy.normalize()
}

func (p EventLogPolicy) normalize() EventLogPolicy {
	defaults := DefaultEventLogPolicy()
	if p.MaxActiveBytes <= 0 {
		p.MaxActiveBytes = defaults.MaxActiveBytes
	}
	if p.ReplayEvents <= 0 {
		p.ReplayEvents = defaults.ReplayEvents
	}
	if p.MaxReplayBytes <= 0 {
		p.MaxReplayBytes = defaults.MaxReplayBytes
	}
	if p.MaxArchives < 0 {
		p.MaxArchives = defaults.MaxArchives
	}
	if p.MaxArchiveBytes < 0 {
		p.MaxArchiveBytes = defaults.MaxArchiveBytes
	}
	if p.MaxArchiveAge < 0 {
		p.MaxArchiveAge = defaults.MaxArchiveAge
	}
	return p
}

type EventMaintenanceReport struct {
	Rotated         int
	Compressed      int
	Removed         int
	RemovedBytes    int64
	CompressedBytes int64
}

// SetEventLogPolicy changes the event-cache policy. It is primarily useful for
// tests and controlled installations; environment settings are loaded by Open.
func (s *Store) SetEventLogPolicy(policy EventLogPolicy) {
	s.eventMu.Lock()
	s.eventPolicy = policy.normalize()
	s.eventMu.Unlock()
}

func (s *Store) AppendEvent(agentID string, ev Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	f, err := os.OpenFile(s.eventsFile(agentID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		return err
	}
	s.eventLastSeq[agentID] = ev.Seq
	return nil
}

// ReadEvents returns events with seq > since. tail keeps only the newest N
// matching events and uses a bounded reverse read instead of scanning the file.
func (s *Store) ReadEvents(agentID string, since int64, tail int) ([]Event, error) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	path := s.eventsFile(agentID)
	if tail > 0 {
		lines, err := readEventTail(path, tail, s.eventPolicy.MaxReplayBytes)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		return decodeEvents(lines, since, tail), nil
	}
	return readEventFile(path, since)
}

// LastSeq returns the highest active event seq without scanning the complete
// event file. The value is cached after the first bounded tail read.
func (s *Store) LastSeq(agentID string) int64 {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if seq, ok := s.eventLastSeq[agentID]; ok {
		return seq
	}
	lines, err := readEventTail(s.eventsFile(agentID), 1, s.eventPolicy.MaxReplayBytes)
	if err == nil {
		events := decodeEvents(lines, 0, 1)
		if len(events) > 0 {
			s.eventLastSeq[agentID] = events[len(events)-1].Seq
			return events[len(events)-1].Seq
		}
	}
	seq := s.lastArchivedSeq(agentID)
	s.eventLastSeq[agentID] = seq
	return seq
}

// MaintainEventLogs rotates oversized active logs, compresses immutable
// segments, and prunes old diagnostic archives. It is safe to run repeatedly.
func (s *Store) MaintainEventLogs() (EventMaintenanceReport, error) {
	var report EventMaintenanceReport
	entries, err := os.ReadDir(filepath.Join(s.dir, "events"))
	if err != nil {
		return report, err
	}

	s.eventMu.Lock()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") || strings.Contains(entry.Name(), ".events-") {
			continue
		}
		agentID := strings.TrimSuffix(entry.Name(), ".ndjson")
		rotated, rotateErr := s.rotateEventLogIfNeededLocked(agentID)
		if rotateErr != nil {
			s.eventMu.Unlock()
			return report, rotateErr
		}
		if rotated {
			report.Rotated++
		}
	}
	s.eventMu.Unlock()

	entries, err = os.ReadDir(filepath.Join(s.dir, "events"))
	if err != nil {
		return report, err
	}
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson.pending") {
			continue
		}
		path := filepath.Join(s.dir, "events", entry.Name())
		compressedBytes, compressErr := compressEventSegment(path)
		if compressErr != nil {
			errs = append(errs, compressErr)
			continue
		}
		report.Compressed++
		report.CompressedBytes += compressedBytes
	}
	removed, removedBytes, pruneErr := s.pruneEventArchives()
	report.Removed = removed
	report.RemovedBytes = removedBytes
	if pruneErr != nil {
		errs = append(errs, pruneErr)
	}
	return report, errors.Join(errs...)
}

func (s *Store) rotateEventLogIfNeededLocked(agentID string) (bool, error) {
	path := s.eventsFile(agentID)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size() <= s.eventPolicy.MaxActiveBytes {
		return false, nil
	}
	lines, err := readEventTail(path, s.eventPolicy.ReplayEvents, s.eventPolicy.MaxReplayBytes)
	if err != nil {
		return false, err
	}
	lastSeq := int64(0)
	if events := decodeEvents(lines, 0, 1); len(events) > 0 {
		lastSeq = events[len(events)-1].Seq
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	pending := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s.events-%s-%d.ndjson.pending", agentID, stamp, lastSeq))
	next := path + ".next"
	if err := writeSyncedFile(next, joinEventLines(lines), 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(path, pending); err != nil {
		_ = os.Remove(next)
		return false, err
	}
	if err := os.Rename(next, path); err != nil {
		_ = os.Rename(pending, path)
		_ = os.Remove(next)
		return false, err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	return true, nil
}

func readEventTail(path string, maxEvents int, maxBytes int64) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}
	if maxBytes <= 0 || maxBytes > info.Size() {
		maxBytes = info.Size()
	}
	start := info.Size() - maxBytes
	buf := make([]byte, maxBytes)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, err
	}
	if start > 0 {
		if newline := bytes.IndexByte(buf, '\n'); newline >= 0 {
			buf = buf[newline+1:]
		} else {
			return nil, nil
		}
	}
	if last := bytes.LastIndexByte(buf, '\n'); last >= 0 {
		buf = buf[:last]
	} else {
		return nil, nil
	}
	rawLines := bytes.Split(buf, []byte{'\n'})
	lines := make([][]byte, 0, len(rawLines))
	for _, raw := range rawLines {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		lines = append(lines, append([]byte(nil), raw...))
	}
	if maxEvents > 0 && len(lines) > maxEvents {
		lines = lines[len(lines)-maxEvents:]
	}
	return lines, nil
}

func joinEventLines(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	size := len(lines)
	for _, line := range lines {
		size += len(line)
	}
	out := make([]byte, 0, size)
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

func decodeEvents(lines [][]byte, since int64, tail int) []Event {
	out := make([]Event, 0, len(lines))
	for _, line := range lines {
		var event Event
		if json.Unmarshal(line, &event) != nil || event.Seq <= since {
			continue
		}
		out = append(out, event)
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out
}

func readEventFile(path string, since int64) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<26)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Seq > since {
			out = append(out, event)
		}
	}
	return out, scanner.Err()
}

func compressEventSegment(path string) (int64, error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	target := strings.TrimSuffix(path, ".pending") + ".gz"
	tmp := target + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	gz, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	if err := out.Sync(); err != nil {
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return 0, err
	}
	if err := os.Remove(path); err != nil {
		return 0, err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return 0, err
	}
	ok = true
	info, err := os.Stat(target)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type eventArchive struct {
	path    string
	agentID string
	size    int64
	modTime time.Time
}

func (s *Store) pruneEventArchives() (int, int64, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "events"))
	if err != nil {
		return 0, 0, err
	}
	byAgent := map[string][]eventArchive{}
	for _, entry := range entries {
		marker := strings.LastIndex(entry.Name(), ".events-")
		if entry.IsDir() || marker <= 0 || !strings.HasSuffix(entry.Name(), ".ndjson.gz") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return 0, 0, infoErr
		}
		agentID := entry.Name()[:marker]
		byAgent[agentID] = append(byAgent[agentID], eventArchive{
			path: filepath.Join(s.dir, "events", entry.Name()), agentID: agentID,
			size: info.Size(), modTime: info.ModTime(),
		})
	}
	removed := 0
	removedBytes := int64(0)
	var errs []error
	now := time.Now()
	for _, archives := range byAgent {
		sort.Slice(archives, func(i, j int) bool { return archives[i].modTime.After(archives[j].modTime) })
		keptBytes := int64(0)
		for index, archive := range archives {
			overCount := index >= s.eventPolicy.MaxArchives
			overBytes := s.eventPolicy.MaxArchiveBytes == 0 || keptBytes+archive.size > s.eventPolicy.MaxArchiveBytes
			overAge := s.eventPolicy.MaxArchiveAge == 0 || now.Sub(archive.modTime) > s.eventPolicy.MaxArchiveAge
			if overCount || overBytes || overAge {
				if err := os.Remove(archive.path); err != nil {
					errs = append(errs, err)
					continue
				}
				removed++
				removedBytes += archive.size
				continue
			}
			keptBytes += archive.size
		}
	}
	return removed, removedBytes, errors.Join(errs...)
}

func (s *Store) lastArchivedSeq(agentID string) int64 {
	pattern := filepath.Join(s.dir, "events", agentID+".events-*")
	paths, _ := filepath.Glob(pattern)
	var maxSeq int64
	for _, path := range paths {
		name := filepath.Base(path)
		name = strings.TrimSuffix(name, ".gz")
		name = strings.TrimSuffix(name, ".pending")
		name = strings.TrimSuffix(name, ".ndjson")
		lastDash := strings.LastIndexByte(name, '-')
		if lastDash < 0 {
			continue
		}
		seq, err := strconv.ParseInt(name[lastDash+1:], 10, 64)
		if err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func envPositiveInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envNonNegativeInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
