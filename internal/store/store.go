// Package store persists codex-hub state on disk.
//
// Layout (default ~/.codex-hub, override with CODEX_HUB_DATA):
//
//	sessions.json        session metadata (atomic write via rename)
//	events/<id>.ndjson   append-only per-session event log, one JSON per line
//
// Events carry a per-session monotonically increasing seq so observers can
// replay from any point (?since=SEQ) and then follow live.
package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Event struct {
	Seq  int64           `json:"seq"`
	TS   string          `json:"ts"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Store struct {
	dir string
}

func DefaultDir() string {
	if d := os.Getenv("CODEX_HUB_DATA"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex-hub"
	}
	return filepath.Join(home, ".codex-hub")
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) sessionsFile() string { return filepath.Join(s.dir, "sessions.json") }

func (s *Store) eventsFile(sessionID string) string {
	return filepath.Join(s.dir, "events", sessionID+".ndjson")
}

// LoadSessions unmarshals sessions.json into v (a *map[string]*Session).
func (s *Store) LoadSessions(v any) error {
	data, err := os.ReadFile(s.sessionsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

// SaveSessions writes sessions.json atomically.
func (s *Store) SaveSessions(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.sessionsFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.sessionsFile())
}

func (s *Store) AppendEvent(sessionID string, ev Event) error {
	f, err := os.OpenFile(s.eventsFile(sessionID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadEvents returns events with seq > since; tail>0 keeps only the last N.
func (s *Store) ReadEvents(sessionID string, since int64, tail int) ([]Event, error) {
	f, err := os.Open(s.eventsFile(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Seq > since {
			out = append(out, ev)
		}
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out, nil
}

// LastSeq returns the highest seq in the session's log (0 if none).
func (s *Store) LastSeq(sessionID string) int64 {
	events, err := s.ReadEvents(sessionID, 0, 1)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}
