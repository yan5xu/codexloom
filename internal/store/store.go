// Package store persists CodexLoom state on disk.
//
// Layout (default ~/.codex-loom, override with CODEX_LOOM_DATA):
//
//	agents.json          Agent registry: stable identity plus primary Codex thread binding
//	sessions.json        compatibility mirror for pre-CodexLoom binaries
//	profiles.json        long-lived collaboration profiles keyed by agent id
//	team-links.json      explicit long-lived collaboration relationships
//	organization-links.json explicit parent/child organization relationships
//	comms.ndjson         append-only agent-to-agent communication log
//	schedules.json       durable scheduler definitions
//	integrations.json    platform connections, agent addresses and conversation memberships (no secrets)
//	messages.ndjson      normalized external communication facts
//	inbox.ndjson         per-agent inbox item snapshots
//	attempts.ndjson      inbox handling attempt snapshots
//	outbox.ndjson        durable outbound message snapshots
//	provider-operations.ndjson durable credential-mediated provider operation snapshots
//	human-requests.ndjson durable Agent-to-human request snapshots
//	events/<id>.ndjson   append-only per-Agent event log, one JSON per line
//
// agents.json is a small registry, not a history store: Thread history lives in
// Codex rollout files (see internal/rollout). The event log supports replay and
// live SSE observation while CodexLoom is attached to an Agent's Thread.
//
// Events carry a per-Agent monotonically increasing seq so observers can
// replay from any point (?since=SEQ) and then follow live.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Event struct {
	Seq  int64           `json:"seq"`
	TS   string          `json:"ts"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Store struct {
	dir          string
	eventMu      sync.Mutex
	eventPolicy  EventLogPolicy
	eventLastSeq map[string]int64
}

func DefaultDir() string {
	if d := os.Getenv("CODEX_LOOM_DATA"); d != "" {
		return d
	}
	if d := os.Getenv("CODEX_HUB_DATA"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex-loom"
	}
	return filepath.Join(home, ".codex-loom")
}

func Open(dir string) (*Store, error) {
	if err := migrateLegacyDefaultDir(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		return nil, err
	}
	return &Store{
		dir:          dir,
		eventPolicy:  EventLogPolicyFromEnv(),
		eventLastSeq: map[string]int64{},
	}, nil
}

func migrateLegacyDefaultDir(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	loomDir := filepath.Join(home, ".codex-loom")
	legacyDir := filepath.Join(home, ".codex-hub")
	if filepath.Clean(dir) != filepath.Clean(loomDir) {
		return nil
	}
	if _, err := os.Stat(loomDir); err == nil || !os.IsNotExist(err) {
		return nil
	}
	legacyInfo, err := os.Lstat(legacyDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if legacyInfo.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if err := os.Rename(legacyDir, loomDir); err != nil {
		return err
	}
	// Keep legacy binaries and gateway state paths working during the rename.
	if err := os.Symlink(loomDir, legacyDir); err != nil {
		return err
	}
	return nil
}

func (s *Store) Dir() string { return s.dir }

// EdgeAgent is one entry from pinix-edge's registry (~/.pinix/code_agents/names.json).
type EdgeAgent struct {
	Name     string
	ThreadID string
	Cwd      string
}

// EdgeNamesFile is pinix-edge's own name-to-Thread registry. CodexLoom reads it
// (never writes it), so edge-created Agents appear here and their history,
// which lives in the same Codex rollout files, is viewable immediately.
func EdgeNamesFile() string {
	if p := os.Getenv("PINIX_EDGE_NAMES"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pinix", "code_agents", "names.json")
}

// LoadEdgeAgents reads pinix-edge's names.json. Missing file → nil, nil.
func LoadEdgeAgents() ([]EdgeAgent, error) {
	path := EdgeNamesFile()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]struct {
		ThreadID string `json:"threadId"`
		Cwd      string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]EdgeAgent, 0, len(raw))
	for name, v := range raw {
		if v.ThreadID == "" {
			continue
		}
		out = append(out, EdgeAgent{Name: name, ThreadID: v.ThreadID, Cwd: v.Cwd})
	}
	return out, nil
}

func (s *Store) sessionsFile() string { return filepath.Join(s.dir, "sessions.json") }

func (s *Store) agentsFile() string { return filepath.Join(s.dir, "agents.json") }

func (s *Store) commsFile() string { return filepath.Join(s.dir, "comms.ndjson") }

func (s *Store) schedulesFile() string { return filepath.Join(s.dir, "schedules.json") }

func (s *Store) profilesFile() string { return filepath.Join(s.dir, "profiles.json") }

func (s *Store) teamLinksFile() string { return filepath.Join(s.dir, "team-links.json") }

func (s *Store) organizationLinksFile() string {
	return filepath.Join(s.dir, "organization-links.json")
}

func (s *Store) integrationsFile() string { return filepath.Join(s.dir, "integrations.json") }

func (s *Store) remoteFile() string { return filepath.Join(s.dir, "remote.json") }

func (s *Store) messagesFile() string { return filepath.Join(s.dir, "messages.ndjson") }

func (s *Store) inboxFile() string { return filepath.Join(s.dir, "inbox.ndjson") }

func (s *Store) attemptsFile() string { return filepath.Join(s.dir, "attempts.ndjson") }

func (s *Store) outboxFile() string { return filepath.Join(s.dir, "outbox.ndjson") }

func (s *Store) providerOperationsFile() string {
	return filepath.Join(s.dir, "provider-operations.ndjson")
}

func (s *Store) humanRequestsFile() string { return filepath.Join(s.dir, "human-requests.ndjson") }

func (s *Store) eventsFile(agentID string) string {
	return filepath.Join(s.dir, "events", agentID+".ndjson")
}

// LoadAgents reads the canonical Agent registry, falling back to the legacy
// sessions.json name for an in-place migration.
func (s *Store) LoadAgents(v any) error {
	data, err := os.ReadFile(s.agentsFile())
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		data, err = os.ReadFile(s.sessionsFile())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(data, v)
}

// SaveAgents writes agents.json and a compatibility sessions.json mirror.
func (s *Store) SaveAgents(v any) error {
	// The compatibility mirror is written first. If its write fails, the
	// canonical registry is untouched; if the canonical write fails, startup
	// still reads the previous agents.json and the caller receives the error.
	if err := saveJSON(s.sessionsFile(), v); err != nil {
		return err
	}
	return saveJSON(s.agentsFile(), v)
}

// Deprecated compatibility names.
func (s *Store) LoadSessions(v any) error { return s.LoadAgents(v) }

func (s *Store) SaveSessions(v any) error { return s.SaveAgents(v) }

func (s *Store) LoadSchedules(v any) error {
	data, err := os.ReadFile(s.schedulesFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) SaveSchedules(v any) error {
	return saveJSON(s.schedulesFile(), v)
}

func (s *Store) LoadProfiles(v any) error { return loadJSON(s.profilesFile(), v) }

func (s *Store) SaveProfiles(v any) error { return saveJSON(s.profilesFile(), v) }

func (s *Store) LoadTeamLinks(v any) error { return loadJSON(s.teamLinksFile(), v) }

func (s *Store) SaveTeamLinks(v any) error { return saveJSON(s.teamLinksFile(), v) }

func (s *Store) LoadOrganizationLinks(v any) error { return loadJSON(s.organizationLinksFile(), v) }

func (s *Store) SaveOrganizationLinks(v any) error { return saveJSON(s.organizationLinksFile(), v) }

func (s *Store) LoadIntegrations(v any) error { return loadJSON(s.integrationsFile(), v) }

func (s *Store) SaveIntegrations(v any) error { return saveJSON(s.integrationsFile(), v) }

func (s *Store) LoadRemote(v any) error { return loadJSON(s.remoteFile(), v) }

func (s *Store) SaveRemote(v any) error { return saveJSON(s.remoteFile(), v) }

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return replaceFile(path, data, 0o600)
}

func replaceFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *Store) AppendComm(v any) error {
	return appendNDJSON(s.commsFile(), v)
}

func (s *Store) ReadComms(fn func(json.RawMessage)) error {
	return readNDJSON(s.commsFile(), fn)
}

func (s *Store) AppendMessage(v any) error { return appendNDJSON(s.messagesFile(), v) }

func (s *Store) ReadMessages(fn func(json.RawMessage)) error {
	return readNDJSON(s.messagesFile(), fn)
}

func (s *Store) AppendInbox(v any) error { return appendNDJSON(s.inboxFile(), v) }

func (s *Store) ReadInbox(fn func(json.RawMessage)) error { return readNDJSON(s.inboxFile(), fn) }

func (s *Store) AppendAttempt(v any) error { return appendNDJSON(s.attemptsFile(), v) }

func (s *Store) ReadAttempts(fn func(json.RawMessage)) error {
	return readNDJSON(s.attemptsFile(), fn)
}

func (s *Store) AppendOutbox(v any) error { return appendNDJSON(s.outboxFile(), v) }

func (s *Store) ReadOutbox(fn func(json.RawMessage)) error { return readNDJSON(s.outboxFile(), fn) }

func (s *Store) AppendProviderOperation(v any) error {
	return appendNDJSON(s.providerOperationsFile(), v)
}

func (s *Store) ReadProviderOperations(fn func(json.RawMessage)) error {
	return readNDJSON(s.providerOperationsFile(), fn)
}

func (s *Store) AppendHumanRequest(v any) error { return appendNDJSON(s.humanRequestsFile(), v) }

func (s *Store) ReadHumanRequests(fn func(json.RawMessage)) error {
	return readNDJSON(s.humanRequestsFile(), fn)
}

func appendNDJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func readNDJSON(path string, fn func(json.RawMessage)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	lineNumber := 0
	for sc.Scan() {
		lineNumber++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			return fmt.Errorf("%s:%d: invalid JSON record", path, lineNumber)
		}
		fn(json.RawMessage(append([]byte(nil), line...)))
	}
	return sc.Err()
}

// ReplaceComms atomically compacts the communication index to one current
// snapshot per message. Codex Thread rollout history is intentionally untouched.
func (s *Store) ReplaceComms(records []json.RawMessage) error {
	if original, err := os.ReadFile(s.commsFile()); err == nil && len(original) > 0 {
		backup := filepath.Join(s.dir, "comms.v1-name-addressed.ndjson")
		if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
			if err := os.WriteFile(backup, original, 0o644); err != nil {
				return err
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	data := make([]byte, 0, len(records)*256)
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		data = append(data, record...)
		data = append(data, '\n')
	}
	return replaceFile(s.commsFile(), data, 0o600)
}
