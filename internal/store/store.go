// Package store persists CodexLoom state on disk.
//
// Layout (default ~/.codex-loom, override with CODEX_LOOM_DATA):
//
//	agents.json          Agent registry: stable identity plus primary Codex thread binding
//	sessions.json        compatibility mirror for pre-CodexLoom binaries
//	agent-skill-config.json per-Agent disabled Skill paths
//	profiles.json        long-lived collaboration profiles keyed by agent id
//	team-links.json      explicit long-lived collaboration relationships
//	collaboration-groups.json named shared views over collaboration relationships
//	organization-links.json explicit parent/child organization relationships
//	comms.ndjson         append-only agent-to-agent communication log
//	schedules.json       durable scheduler definitions
//	triggers.json        durable external-condition definitions
//	topics.json          durable cross-Agent coordination records
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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
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
	dirHandle          *stableDataDir
	dir                string
	readOnly           bool
	closeMu            sync.RWMutex
	closed             bool
	ownerActive        bool
	ownerGeneration    uint64
	ownerRequired      bool
	borrowedHandle     bool
	eventMu            sync.Mutex
	eventMaintenanceMu sync.Mutex
	eventPolicy        EventLogPolicy
	eventLastSeq       map[string]int64
}

type OpenOptions struct {
	ReadOnly bool
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
	return OpenWithOptions(dir, OpenOptions{})
}

// OpenWithOptions establishes a stable directory handle and validates the
// private foundation before the first in-directory mutation. Read-only opens
// never create directories, lease files, events, or compatibility migrations.
func OpenWithOptions(dir string, options OpenOptions) (_ *Store, err error) {
	if !options.ReadOnly {
		if err := migrateLegacyDefaultDirStable(dir); err != nil {
			return nil, err
		}
	}
	handle, err := openStableDataDir(dir, options.ReadOnly)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = handle.close()
		}
	}()
	if !options.ReadOnly {
		if err := handle.root.MkdirAll("events", 0o755); err != nil {
			return nil, err
		}
		if err := handle.verifyIdentity(); err != nil {
			return nil, err
		}
	}
	return &Store{
		dirHandle:    handle,
		dir:          handle.canonical,
		readOnly:     options.ReadOnly,
		eventPolicy:  EventLogPolicyFromEnv(),
		eventLastSeq: map[string]int64{},
	}, nil
}

func migrateLegacyDefaultDirStable(dir string) error {
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
	legacy, err := openStableDataDir(legacyDir, false)
	if err != nil {
		return err
	}
	defer legacy.close()
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Rename(".codex-hub", ".codex-loom"); err != nil {
		return err
	}
	// Keep legacy binaries and gateway state paths working during the rename.
	if err := root.Symlink(".codex-loom", ".codex-hub"); err != nil {
		return err
	}
	return nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) ReadOnly() bool { return s != nil && s.readOnly }

func (s *Store) OpenReadOnly() (*Store, error) {
	if s == nil {
		return nil, fmt.Errorf("store is unavailable")
	}
	return OpenWithOptions(s.dir, OpenOptions{ReadOnly: true})
}

func (s *Store) RetiredReadOnlyView() *Store {
	if s == nil {
		return nil
	}
	return &Store{dirHandle: s.dirHandle, dir: s.dir, readOnly: true, borrowedHandle: true,
		eventPolicy: s.eventPolicy, eventLastSeq: map[string]int64{}}
}

type WritableOwnership struct {
	store      *Store
	generation uint64
	once       sync.Once
}

func (s *Store) ClaimWritableOwnership() (*WritableOwnership, error) {
	if s == nil || s.readOnly {
		return nil, fmt.Errorf("writable Hub requires a writable Store")
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	if s.ownerActive {
		return nil, fmt.Errorf("store already has a live writable Hub")
	}
	if err := s.dirHandle.verifyIdentity(); err != nil {
		return nil, err
	}
	s.ownerGeneration++
	s.ownerActive = true
	s.ownerRequired = true
	return &WritableOwnership{store: s, generation: s.ownerGeneration}, nil
}

// HasLiveWritableOwner reports whether a live writable Hub currently owns this
// Store. Foundation-owned subsystems (for example the managed credential
// store) require it before issuing any durable mutation capability.
func (s *Store) HasLiveWritableOwner() bool {
	if s == nil {
		return false
	}
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	return !s.closed && !s.readOnly && s.ownerActive
}

func (o *WritableOwnership) Release() {
	if o == nil || o.store == nil {
		return
	}
	o.once.Do(func() {
		o.store.closeMu.Lock()
		if o.store.ownerActive && o.store.ownerGeneration == o.generation {
			o.store.ownerActive = false
		}
		o.store.closeMu.Unlock()
	})
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	if s.ownerActive {
		return fmt.Errorf("store is owned by a live writable Hub")
	}
	s.closed = true
	if s.borrowedHandle {
		return nil
	}
	return s.dirHandle.close()
}

func (s *Store) beginRead() (func(), error) {
	if s == nil {
		return nil, fmt.Errorf("store is unavailable")
	}
	s.closeMu.RLock()
	if s.closed {
		s.closeMu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	if err := s.dirHandle.verifyIdentity(); err != nil {
		s.closeMu.RUnlock()
		return nil, err
	}
	return s.closeMu.RUnlock, nil
}

func (s *Store) beginWrite() (func(), error) {
	if s == nil || s.readOnly {
		return nil, fmt.Errorf("store is read-only")
	}
	s.closeMu.RLock()
	if s.closed {
		s.closeMu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	if s.ownerRequired && !s.ownerActive {
		s.closeMu.RUnlock()
		return nil, fmt.Errorf("store has no live writable Hub owner")
	}
	if err := s.dirHandle.verifyIdentity(); err != nil {
		s.closeMu.RUnlock()
		return nil, err
	}
	return s.closeMu.RUnlock, nil
}

// ValidateWritableIdentity is the zero-effect gate for legacy Runtime-owned
// data-dir writers that have not yet been converted to typed Store methods.
func (s *Store) ValidateWritableIdentity() error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	done()
	return nil
}

func (s *Store) finishWrite(err error) error {
	if err != nil {
		return err
	}
	if err := s.dirHandle.verifyIdentity(); err != nil {
		return fmt.Errorf("data directory identity changed during write: %w", err)
	}
	return nil
}

// WithStableWriteRoot runs one data-dir mutation through the live-Hub
// ownership and stable directory-handle boundary. The callback must use only
// the supplied Root with relative paths; finishWrite revalidates the directory
// identity after the operation. This is the narrow write capability used by
// foundation-owned subsystems (for example the managed credential store).
func (s *Store) WithStableWriteRoot(fn func(*os.Root) error) error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer done()
	if fn == nil {
		return fmt.Errorf("stable data directory writer callback is required")
	}
	return s.finishWrite(fn(s.dirHandle.root))
}

// ReadStableFile reads one file beneath the stable data directory through the
// stable root handle without requiring write ownership.
func (s *Store) ReadStableFile(relative string) ([]byte, error) {
	done, err := s.beginRead()
	if err != nil {
		return nil, err
	}
	defer done()
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("stable path escapes data directory: %s", relative)
	}
	return s.dirHandle.root.ReadFile(clean)
}

// StatStableFile stats one file beneath the stable data directory through the
// stable root handle without requiring write ownership.
func (s *Store) StatStableFile(relative string) (os.FileInfo, error) {
	done, err := s.beginRead()
	if err != nil {
		return nil, err
	}
	defer done()
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("stable path escapes data directory: %s", relative)
	}
	return s.dirHandle.root.Stat(clean)
}

// OpenStableFile opens one file beneath the stable data directory through the
// stable root handle without requiring write ownership. The returned handle
// remains bound to the stable root for the Store lifetime.
func (s *Store) OpenStableFile(relative string, flag int) (*os.File, error) {
	done, err := s.beginRead()
	if err != nil {
		return nil, err
	}
	defer done()
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("stable path escapes data directory: %s", relative)
	}
	return s.dirHandle.root.OpenFile(clean, flag, 0)
}

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

func (s *Store) agentSkillConfigFile() string {
	return filepath.Join(s.dir, "agent-skill-config.json")
}

func (s *Store) commsFile() string { return filepath.Join(s.dir, "comms.ndjson") }

func (s *Store) schedulesFile() string { return filepath.Join(s.dir, "schedules.json") }

func (s *Store) triggersFile() string { return filepath.Join(s.dir, "triggers.json") }

func (s *Store) topicsFile() string { return filepath.Join(s.dir, "topics.json") }

func (s *Store) profilesFile() string { return filepath.Join(s.dir, "profiles.json") }

func (s *Store) teamLinksFile() string { return filepath.Join(s.dir, "team-links.json") }

func (s *Store) collaborationGroupsFile() string {
	return filepath.Join(s.dir, "collaboration-groups.json")
}

func (s *Store) organizationLinksFile() string {
	return filepath.Join(s.dir, "organization-links.json")
}

func (s *Store) integrationsFile() string { return filepath.Join(s.dir, "integrations.json") }

func (s *Store) runtimeFoundationFile() string {
	return filepath.Join(s.dir, foundationFileName)
}

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

func (s *Store) relative(path string) (string, error) {
	rel, err := filepath.Rel(s.dir, filepath.Clean(path))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes stable data directory: %s", path)
	}
	return rel, nil
}

func (s *Store) readFile(path string) ([]byte, error) {
	done, err := s.beginRead()
	if err != nil {
		return nil, err
	}
	defer done()
	rel, err := s.relative(path)
	if err != nil {
		return nil, err
	}
	return s.dirHandle.root.ReadFile(rel)
}

func (s *Store) openFile(path string, flag int, mode os.FileMode) (*os.File, error) {
	rel, err := s.relative(path)
	if err != nil {
		return nil, err
	}
	return s.dirHandle.root.OpenFile(rel, flag, mode)
}

func (s *Store) stat(path string) (os.FileInfo, error) {
	rel, err := s.relative(path)
	if err != nil {
		return nil, err
	}
	return s.dirHandle.root.Stat(rel)
}

func (s *Store) readDir(path string) ([]os.DirEntry, error) {
	rel, err := s.relative(path)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(s.dirHandle.root.FS(), rel)
}

func (s *Store) remove(path string) error {
	rel, err := s.relative(path)
	if err != nil {
		return err
	}
	return s.dirHandle.root.Remove(rel)
}

func (s *Store) rename(oldPath, newPath string) error {
	oldRel, err := s.relative(oldPath)
	if err != nil {
		return err
	}
	newRel, err := s.relative(newPath)
	if err != nil {
		return err
	}
	return s.dirHandle.root.Rename(oldRel, newRel)
}

func (s *Store) syncDir(path string) error {
	rel, err := s.relative(path)
	if err != nil {
		return err
	}
	dir, err := s.dirHandle.root.Open(rel)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) loadJSON(path string, v any) error {
	data, err := s.readFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.replaceFile(path, data, 0o600)
}

func (s *Store) replaceFile(path string, data []byte, mode os.FileMode) error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer done()
	rel, err := s.relative(path)
	if err != nil {
		return err
	}
	dir, base := filepath.Dir(rel), filepath.Base(rel)
	var tmpName string
	var tmp *os.File
	for i := 0; i < 32; i++ {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		tmpName = filepath.Join(dir, "."+base+"-"+hex.EncodeToString(random)+".tmp")
		tmp, err = s.dirHandle.root.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if tmp == nil {
		return fmt.Errorf("create temporary file for %s", path)
	}
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = s.dirHandle.root.Remove(tmpName)
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
	if err := s.dirHandle.root.Rename(tmpName, rel); err != nil {
		return err
	}
	committed = true
	directory, err := s.dirHandle.root.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return s.finishWrite(directory.Sync())
}

// LoadAgents reads the canonical Agent registry, falling back to the legacy
// sessions.json name for an in-place migration.
func (s *Store) LoadAgents(v any) error {
	data, err := s.readFile(s.agentsFile())
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		data, err = s.readFile(s.sessionsFile())
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
	if err := s.saveJSON(s.sessionsFile(), v); err != nil {
		return err
	}
	return s.saveJSON(s.agentsFile(), v)
}

func (s *Store) LoadAgentSkillConfigs(v any) error {
	return s.loadJSON(s.agentSkillConfigFile(), v)
}

func (s *Store) SaveAgentSkillConfigs(v any) error {
	return s.saveJSON(s.agentSkillConfigFile(), v)
}

// Deprecated compatibility names.
func (s *Store) LoadSessions(v any) error { return s.LoadAgents(v) }

func (s *Store) SaveSessions(v any) error { return s.SaveAgents(v) }

func (s *Store) LoadSchedules(v any) error {
	data, err := s.readFile(s.schedulesFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) SaveSchedules(v any) error {
	return s.saveJSON(s.schedulesFile(), v)
}

func (s *Store) LoadTriggers(v any) error { return s.loadJSON(s.triggersFile(), v) }

func (s *Store) SaveTriggers(v any) error { return s.saveJSON(s.triggersFile(), v) }

func (s *Store) LoadTopics(v any) error { return s.loadJSON(s.topicsFile(), v) }

func (s *Store) SaveTopics(v any) error { return s.saveJSON(s.topicsFile(), v) }

func (s *Store) LoadProfiles(v any) error { return s.loadJSON(s.profilesFile(), v) }

func (s *Store) SaveProfiles(v any) error { return s.saveJSON(s.profilesFile(), v) }

func (s *Store) LoadTeamLinks(v any) error { return s.loadJSON(s.teamLinksFile(), v) }

func (s *Store) SaveTeamLinks(v any) error { return s.saveJSON(s.teamLinksFile(), v) }

func (s *Store) LoadCollaborationGroups(v any) error {
	return s.loadJSON(s.collaborationGroupsFile(), v)
}

func (s *Store) SaveCollaborationGroups(v any) error {
	return s.saveJSON(s.collaborationGroupsFile(), v)
}

func (s *Store) LoadOrganizationLinks(v any) error { return s.loadJSON(s.organizationLinksFile(), v) }

func (s *Store) SaveOrganizationLinks(v any) error { return s.saveJSON(s.organizationLinksFile(), v) }

func (s *Store) LoadIntegrations(v any) error { return s.loadJSON(s.integrationsFile(), v) }

func (s *Store) SaveIntegrations(v any) error { return s.saveJSON(s.integrationsFile(), v) }

type foundationEnvelopeState struct {
	envelope runtimeFoundationEnvelope
	state    foundationState
	exists   bool
}

// loadFoundationEnvelope reads the private Runtime foundation through the
// stable directory handle. Writers must preserve the other component's state
// so a Gateway write never drops the managed-credential floor and vice versa.
func (s *Store) loadFoundationEnvelope() (foundationEnvelopeState, error) {
	data, err := s.readFile(s.runtimeFoundationFile())
	if os.IsNotExist(err) {
		return foundationEnvelopeState{}, nil
	}
	if err != nil {
		return foundationEnvelopeState{}, err
	}
	var envelope runtimeFoundationEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return foundationEnvelopeState{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return foundationEnvelopeState{}, err
	}
	var state foundationState
	stateDecoder := json.NewDecoder(strings.NewReader(string(envelope.State)))
	stateDecoder.DisallowUnknownFields()
	if err := stateDecoder.Decode(&state); err != nil {
		return foundationEnvelopeState{}, err
	}
	if err := requireJSONEOF(stateDecoder); err != nil {
		return foundationEnvelopeState{}, err
	}
	return foundationEnvelopeState{envelope: envelope, state: state, exists: true}, nil
}

// LoadRuntimeGatewayState reads the private R0b/R1 payload. Absence and the S0
// empty envelope both mean that no Gateway control has ever been adopted.
// The stable directory open path has already validated the envelope from the
// same directory handle before any writable Hub mutation was possible.
func (s *Store) LoadRuntimeGatewayState(v any) (bool, error) {
	current, err := s.loadFoundationEnvelope()
	if err != nil {
		return false, err
	}
	if !current.exists {
		return false, nil
	}
	state := current.state
	if state.Version == 1 {
		return false, nil
	}
	if len(state.GatewayState) == 0 {
		return false, nil
	}
	if state.Version != 2 && state.Version != 3 && state.Version != 4 {
		return false, fmt.Errorf("unsupported Runtime Gateway foundation")
	}
	gatewayVersion, err := validateGatewayFoundationState(state.GatewayState)
	if err != nil {
		return false, err
	}
	if state.Version == 2 {
		if gatewayVersion == 3 {
			return false, fmt.Errorf("unsupported Runtime Gateway launch-proof foundation")
		}
		expectedFloor := runtimeWriterFloorGatewayState
		if gatewayVersion == 2 {
			expectedFloor = runtimeWriterFloorGatewayProcess
		}
		if current.envelope.MinimumWriter != expectedFloor {
			return false, fmt.Errorf("unsupported Runtime Gateway foundation")
		}
	} else if state.Version == 3 {
		if current.envelope.MinimumWriter != runtimeWriterFloorCredential || gatewayVersion == 3 {
			return false, fmt.Errorf("unsupported Runtime Gateway foundation")
		}
	} else if current.envelope.MinimumWriter != runtimeWriterFloorGatewayProof || gatewayVersion != 3 {
		return false, fmt.Errorf("unsupported Runtime Gateway launch-proof foundation")
	}
	gatewayDecoder := json.NewDecoder(strings.NewReader(string(state.GatewayState)))
	gatewayDecoder.DisallowUnknownFields()
	if err := gatewayDecoder.Decode(v); err != nil {
		return false, err
	}
	if err := requireJSONEOF(gatewayDecoder); err != nil {
		return false, err
	}
	return true, nil
}

// SaveRuntimeGatewayState is the only Gateway writer-floor transition. The
// state version and matching minimum writer are encoded in one atomic file
// replacement; ordinary Store/Hub open, Passive mode, and shutdown never call
// it.
func (s *Store) SaveRuntimeGatewayState(v any) error {
	if s == nil {
		return fmt.Errorf("store is unavailable")
	}
	s.closeMu.RLock()
	owned := !s.closed && !s.readOnly && s.ownerActive
	s.closeMu.RUnlock()
	if !owned {
		return fmt.Errorf("Runtime Gateway foundation requires a live writable Hub owner")
	}
	gateway, err := json.Marshal(v)
	if err != nil {
		return err
	}
	gatewayVersion, err := validateGatewayFoundationState(gateway)
	if err != nil {
		return err
	}
	current, err := s.loadFoundationEnvelope()
	if err != nil {
		return err
	}
	if current.exists && current.envelope.MinimumWriter == runtimeWriterFloorGatewayProof && gatewayVersion != 3 {
		return fmt.Errorf("Runtime Gateway launch-proof floor cannot be lowered")
	}
	if gatewayVersion == 3 && (!current.exists || current.envelope.MinimumWriter != runtimeWriterFloorGatewayProof) && !gatewayFoundationHasTypedLaunchPlan(gateway) {
		return fmt.Errorf("first L2a Gateway launch-proof commit requires a typed launch plan")
	}
	state := foundationState{Version: 2, GatewayState: gateway}
	minimumWriter := runtimeWriterFloorGatewayState
	if gatewayVersion == 2 {
		minimumWriter = runtimeWriterFloorGatewayProcess
	}
	if current.exists && current.state.CredentialManaged {
		state.Version = 3
		state.CredentialManaged = true
		minimumWriter = runtimeWriterFloorCredential
	}
	if gatewayVersion == 3 {
		if !current.exists || !current.state.CredentialManaged {
			return fmt.Errorf("L2a Gateway launch proof requires the managed credential floor")
		}
		state.Version = 4
		state.CredentialManaged = true
		minimumWriter = runtimeWriterFloorGatewayProof
	}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	envelope := runtimeFoundationEnvelope{
		SchemaVersion: runtimeFoundationSchemaVersion,
		MinimumWriter: minimumWriter,
		State:         stateBytes,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if len(data) >= runtimeFoundationMaxBytes {
		return fmt.Errorf("Runtime Gateway foundation exceeds %d bytes", runtimeFoundationMaxBytes)
	}
	return s.replaceFile(s.runtimeFoundationFile(), data, 0o600)
}

// SaveCredentialFloor atomically raises the credential minimum writer floor.
// Old builds that do not understand credential backup exclusion will reject
// the raised floor at writable open, closing the downgrade gate before the
// first managed credential Put. Gateway state, if present, is preserved.
func (s *Store) SaveCredentialFloor() error {
	if s == nil {
		return fmt.Errorf("store is unavailable")
	}
	s.closeMu.RLock()
	owned := !s.closed && !s.readOnly && s.ownerActive
	s.closeMu.RUnlock()
	if !owned {
		return fmt.Errorf("credential floor requires a live writable Hub owner")
	}
	current, err := s.loadFoundationEnvelope()
	if err != nil {
		return err
	}
	state := foundationState{Version: 3, CredentialManaged: true}
	if current.exists && len(current.state.GatewayState) != 0 {
		state.GatewayState = current.state.GatewayState
		if gatewayVersion, gatewayErr := validateGatewayFoundationState(state.GatewayState); gatewayErr != nil {
			return gatewayErr
		} else if gatewayVersion == 3 {
			state.Version = 4
		}
	}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	minimumWriter := runtimeWriterFloorCredential
	if state.Version == 4 {
		minimumWriter = runtimeWriterFloorGatewayProof
	}
	envelope := runtimeFoundationEnvelope{
		SchemaVersion: runtimeFoundationSchemaVersion,
		MinimumWriter: minimumWriter,
		State:         stateBytes,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if len(data) >= runtimeFoundationMaxBytes {
		return fmt.Errorf("Runtime foundation exceeds %d bytes", runtimeFoundationMaxBytes)
	}
	return s.replaceFile(s.runtimeFoundationFile(), data, 0o600)
}

// CredentialFloorPresent reports whether the credential minimum writer floor
// has been raised for this data directory. It performs no writes.
func (s *Store) CredentialFloorPresent() bool {
	current, err := s.loadFoundationEnvelope()
	return err == nil && current.exists && (current.state.Version == 3 || current.state.Version == 4) && current.state.CredentialManaged
}

func (s *Store) LoadRemote(v any) error { return s.loadJSON(s.remoteFile(), v) }

func (s *Store) SaveRemote(v any) error { return s.saveJSON(s.remoteFile(), v) }

func (s *Store) AppendComm(v any) error {
	return s.appendNDJSON(s.commsFile(), v)
}

func (s *Store) ReadComms(fn func(json.RawMessage)) error {
	return s.readNDJSON(s.commsFile(), fn)
}

func (s *Store) AppendMessage(v any) error { return s.appendNDJSON(s.messagesFile(), v) }

func (s *Store) ReadMessages(fn func(json.RawMessage)) error {
	return s.readNDJSON(s.messagesFile(), fn)
}

func (s *Store) AppendInbox(v any) error { return s.appendNDJSON(s.inboxFile(), v) }

func (s *Store) ReadInbox(fn func(json.RawMessage)) error { return s.readNDJSON(s.inboxFile(), fn) }

func (s *Store) AppendAttempt(v any) error { return s.appendNDJSON(s.attemptsFile(), v) }

func (s *Store) ReadAttempts(fn func(json.RawMessage)) error {
	return s.readNDJSON(s.attemptsFile(), fn)
}

func (s *Store) AppendOutbox(v any) error { return s.appendNDJSON(s.outboxFile(), v) }

func (s *Store) ReadOutbox(fn func(json.RawMessage)) error { return s.readNDJSON(s.outboxFile(), fn) }

func (s *Store) AppendProviderOperation(v any) error {
	return s.appendNDJSON(s.providerOperationsFile(), v)
}

func (s *Store) ReadProviderOperations(fn func(json.RawMessage)) error {
	return s.readNDJSON(s.providerOperationsFile(), fn)
}

func (s *Store) AppendHumanRequest(v any) error { return s.appendNDJSON(s.humanRequestsFile(), v) }

func (s *Store) ReadHumanRequests(fn func(json.RawMessage)) error {
	return s.readNDJSON(s.humanRequestsFile(), fn)
}

func (s *Store) appendNDJSON(path string, v any) error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer done()
	f, err := s.openFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
	return s.finishWrite(f.Sync())
}

func (s *Store) readNDJSON(path string, fn func(json.RawMessage)) error {
	done, err := s.beginRead()
	if err != nil {
		return err
	}
	defer done()
	f, err := s.openFile(path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	endsWithNewline := true
	if info, statErr := f.Stat(); statErr != nil {
		return statErr
	} else if info.Size() > 0 {
		last := []byte{0}
		if _, readErr := f.ReadAt(last, info.Size()-1); readErr != nil {
			return readErr
		}
		endsWithNewline = last[0] == '\n'
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	lineNumber := 0
	invalidLine := 0
	for sc.Scan() {
		lineNumber++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if invalidLine != 0 {
			return fmt.Errorf("%s:%d: invalid JSON record", path, invalidLine)
		}
		if !json.Valid([]byte(line)) {
			// A crash can leave the append-only file with one torn final
			// record. Defer the error until another non-empty line proves
			// that the corruption occurred in the middle of the log.
			invalidLine = lineNumber
			continue
		}
		fn(json.RawMessage(append([]byte(nil), line...)))
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if invalidLine != 0 && endsWithNewline {
		return fmt.Errorf("%s:%d: invalid JSON record", path, invalidLine)
	}
	if invalidLine != 0 {
		log.Printf("[codex-loom] ignored torn final NDJSON record %s:%d", path, invalidLine)
	}
	return nil
}

// ReplaceComms atomically compacts the communication index to one current
// snapshot per message. Codex Thread rollout history is intentionally untouched.
func (s *Store) ReplaceComms(records []json.RawMessage) error {
	if original, err := s.readFile(s.commsFile()); err == nil && len(original) > 0 {
		backup := filepath.Join(s.dir, "comms.v1-name-addressed.ndjson")
		rel, relErr := s.relative(backup)
		if relErr != nil {
			return relErr
		}
		if _, statErr := s.dirHandle.root.Stat(rel); os.IsNotExist(statErr) {
			if err := s.replaceFile(backup, original, 0o600); err != nil {
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
	return s.replaceFile(s.commsFile(), data, 0o600)
}
