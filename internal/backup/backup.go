// Package backup creates local disaster-recovery snapshots for CodexLoom.
//
// A snapshot is a tar.gz archive containing CodexLoom's registry/log files and
// Codex rollout files for every known Agent. It intentionally remains a plain
// archive so restore does not depend on a running CodexLoom binary.
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
)

const (
	CurrentManifestVersion  = 3
	maxManifestBytes        = 1 << 20
	maxSnapshotEntries      = 200_000
	maxSnapshotBytes        = int64(8 << 30)
	maxSnapshotDecodedBytes = int64(16 << 30)
)

type AgentRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd"`
	Source   string `json:"source,omitempty"`
}

// SessionRef keeps source compatibility with pre-CodexLoom callers.
type SessionRef = AgentRef

type Options struct {
	Reason           string
	DataDir          string
	CodexSessionsDir string
	EdgeNamesFile    string
	Agents           []AgentRef
	Sessions         []SessionRef // legacy input alias
	MaxBackups       int
	Retention        RetentionPolicy
	Build            string
}

type Snapshot struct {
	Name                string       `json:"name"`
	Path                string       `json:"path"`
	CreatedAt           time.Time    `json:"createdAt"`
	Reason              string       `json:"reason"`
	SizeBytes           int64        `json:"sizeBytes"`
	FileCount           int          `json:"fileCount"`
	RolloutCount        int          `json:"rolloutCount"`
	CredentialsIncluded bool         `json:"credentialsIncluded"`
	RunnableRestore     bool         `json:"runnableRestore"`
	BackupStatus        string       `json:"backupStatus"`
	Build               string       `json:"build,omitempty"`
	Warnings            []string     `json:"warnings,omitempty"`
	Prune               *PruneReport `json:"prune,omitempty"`
}

type manifest struct {
	Version             int          `json:"version"`
	CreatedAt           string       `json:"createdAt"`
	Reason              string       `json:"reason"`
	DataDir             string       `json:"dataDir"`
	CodexSessionsDir    string       `json:"codexSessionsDir"`
	EdgeNamesFile       string       `json:"edgeNamesFile,omitempty"`
	Agents              []AgentRef   `json:"agents"`
	Sessions            []SessionRef `json:"sessions,omitempty"`
	Files               []string     `json:"files"`
	Excluded            []string     `json:"excluded,omitempty"`
	Warnings            []string     `json:"warnings,omitempty"`
	CredentialsIncluded bool         `json:"credentialsIncluded"`
	RunnableRestore     bool         `json:"runnableRestore"`
	BackupStatus        string       `json:"backupStatus"`
	Build               string       `json:"build,omitempty"`
}

// Verification is an internal, non-secret proof obtained by bounded parsing of
// a snapshot manifest. API callers continue to use Snapshot.BackupStatus.
type Verification struct {
	Status              string
	ManifestVersion     int
	Build               string
	CredentialsIncluded bool
	RunnableRestore     bool
}

// ValidateRollbackFloor proves that a snapshot was produced by the
// credential-excluding manifest format and by the explicitly accepted build.
// Commit hashes are identities, so acceptedBuild is an exact proof.
func (v Verification) ValidateRollbackFloor(minimumManifestVersion int, acceptedBuild string) error {
	if v.Status != "credentials_excluded" || v.CredentialsIncluded || v.RunnableRestore || v.ManifestVersion < minimumManifestVersion {
		return fmt.Errorf("backup is not a verified credential-excluding rollback anchor")
	}
	acceptedBuild = strings.TrimSpace(acceptedBuild)
	build := strings.TrimSpace(v.Build)
	if !buildinfo.ValidBuildIdentity(acceptedBuild) || !buildinfo.ValidBuildIdentity(build) || build != acceptedBuild {
		return fmt.Errorf("backup build does not satisfy the accepted rollback build")
	}
	return nil
}

func DefaultDir(dataDir string) string {
	return filepath.Join(dataDir, "backups")
}

func Create(opts Options) (*Snapshot, error) {
	if opts.DataDir == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if opts.CodexSessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		opts.CodexSessionsDir = filepath.Join(home, ".codex", "sessions")
	}
	if opts.Reason == "" {
		opts.Reason = "manual"
	}
	if len(opts.Agents) == 0 {
		opts.Agents = opts.Sessions
	}
	policy := opts.Retention
	if policy == (RetentionPolicy{}) {
		policy = DefaultRetentionPolicy()
		if opts.MaxBackups > 0 {
			policy.MaxCount = opts.MaxBackups
		}
	}

	backupDir := DefaultDir(opts.DataDir)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}
	var pruneReport PruneReport

	created := time.Now().UTC()
	stamp := created.Format("20060102T150405Z")
	name := fmt.Sprintf("codex-loom-%s-%s.tar.gz", stamp, safeName(opts.Reason))
	path := filepath.Join(backupDir, name)
	tmp := path + ".tmp"

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	m := manifest{
		Version:          CurrentManifestVersion,
		CreatedAt:        created.Format(time.RFC3339Nano),
		Reason:           opts.Reason,
		DataDir:          opts.DataDir,
		CodexSessionsDir: opts.CodexSessionsDir,
		EdgeNamesFile:    opts.EdgeNamesFile,
		Agents:           opts.Agents,
		Sessions:         opts.Agents,
		Excluded: []string{
			"codex-loom/events/** (derived SSE replay cache)",
			"codex-loom/credentials/** (secret-bearing managed credentials; ordinary backup is not a complete runnable restore)",
		},
		CredentialsIncluded: false, RunnableRestore: false, BackupStatus: "credentials_excluded",
		Build: strings.TrimSpace(opts.Build),
	}
	if m.Build == "" {
		m.Build = strings.TrimSpace(buildinfo.Commit)
	}

	var requiredErrors []error
	add := func(src, dst string, required bool) {
		if src == "" {
			return
		}
		if err := addFile(tw, src, dst); err != nil {
			msg := fmt.Sprintf("%s: %v", src, err)
			if required {
				m.Warnings = append(m.Warnings, "required file skipped: "+msg)
				requiredErrors = append(requiredErrors, fmt.Errorf("required file %s: %w", src, err))
			} else if !os.IsNotExist(err) {
				m.Warnings = append(m.Warnings, "optional file skipped: "+msg)
			}
			return
		}
		m.Files = append(m.Files, filepath.ToSlash(dst))
	}

	if err := walkDataDir(opts.DataDir, backupDir, func(src, rel string) {
		add(src, filepath.Join("codex-loom", rel), true)
	}); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}

	if opts.EdgeNamesFile != "" {
		add(opts.EdgeNamesFile, filepath.Join("pinix-edge", "names.json"), false)
	}
	add(filepath.Join(filepath.Dir(opts.CodexSessionsDir), "config.toml"), filepath.Join("codex", "config.toml"), false)

	rollouts, warnings := findRollouts(opts.CodexSessionsDir, opts.Agents)
	m.Warnings = append(m.Warnings, warnings...)
	rolloutCount := 0
	for _, src := range rollouts {
		rel, err := filepath.Rel(opts.CodexSessionsDir, src)
		if err != nil {
			m.Warnings = append(m.Warnings, fmt.Sprintf("rollout outside sessions dir skipped: %s", src))
			continue
		}
		before := len(m.Files)
		add(src, filepath.Join("codex-sessions", rel), true)
		if len(m.Files) > before {
			rolloutCount++
		}
	}
	if len(requiredErrors) > 0 {
		_ = tw.Close()
		_ = gz.Close()
		return nil, errors.Join(requiredErrors...)
	}

	sort.Strings(m.Files)
	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	hdr := &tar.Header{
		Name:    "manifest.json",
		Mode:    0o644,
		Size:    int64(len(manifestBytes)),
		ModTime: created,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	m.Files = append(m.Files, "manifest.json")

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	ok = true

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{
		Name: name, Path: path, CreatedAt: created, Reason: opts.Reason,
		SizeBytes: info.Size(), FileCount: len(m.Files), RolloutCount: rolloutCount,
		CredentialsIncluded: false, RunnableRestore: false, BackupStatus: "credentials_excluded",
		Build:    m.Build,
		Warnings: m.Warnings,
	}
	postPrune, pruneErr := ApplyRetention(opts.DataDir, policy)
	pruneReport.merge(postPrune)
	s.Prune = &pruneReport
	if pruneErr != nil {
		s.Warnings = append(s.Warnings, "snapshot created but retention cleanup failed: "+pruneErr.Error())
	}
	return s, nil
}

func List(dataDir string) ([]Snapshot, error) {
	return listSnapshots(DefaultDir(dataDir))
}

func listSnapshots(backupDir string) ([]Snapshot, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() || !isSnapshotName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(backupDir, e.Name())
		verification := Verify(path)
		out = append(out, Snapshot{
			Name: e.Name(), Path: path, CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size(),
			CredentialsIncluded: verification.CredentialsIncluded, RunnableRestore: verification.RunnableRestore,
			BackupStatus: verification.Status, Build: verification.Build,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if out == nil {
		out = []Snapshot{}
	}
	return out, nil
}

// Verify reads a snapshot with explicit archive, entry, and manifest bounds.
// Legacy or malformed snapshots remain visible but never inherit a verified
// credential-exclusion claim from their filename.
func Verify(path string) Verification {
	return verify(path, maxSnapshotDecodedBytes)
}

func verify(path string, maxDecodedBytes int64) Verification {
	unknown := Verification{Status: "corrupt_unverified"}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSnapshotBytes {
		return unknown
	}
	file, err := os.Open(path)
	if err != nil {
		return unknown
	}
	defer file.Close()
	compressed := bufio.NewReader(file)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return unknown
	}
	// Verification is for one exact archive, not for the first valid member of
	// an ambiguous concatenated stream. Keeping multistream disabled also lets
	// us prove that no raw bytes follow the verified gzip trailer.
	gz.Multistream(false)
	defer gz.Close()
	if maxDecodedBytes <= 0 {
		return unknown
	}
	decodedReader := &io.LimitedReader{R: gz, N: maxDecodedBytes + 1}
	reader := tar.NewReader(decodedReader)
	var decoded manifest
	manifestSeen := false
	credentialEntrySeen := false
	entries := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return unknown
		}
		entries++
		if entries > maxSnapshotEntries {
			return unknown
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return unknown
		}
		rawName := strings.ReplaceAll(header.Name, "\\", "/")
		name := pathpkg.Clean(rawName)
		if name == "." || strings.HasPrefix(rawName, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return unknown
		}
		if name == "codex-loom/credentials" || strings.HasPrefix(name, "codex-loom/credentials/") {
			credentialEntrySeen = true
		}
		if name != "manifest.json" {
			continue
		}
		if manifestSeen || header.Size <= 0 || header.Size > maxManifestBytes {
			return unknown
		}
		manifestSeen = true
		data, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
		if err != nil || int64(len(data)) != header.Size || len(data) > maxManifestBytes {
			return unknown
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(&decoded); err != nil {
			return unknown
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return unknown
		}
	}
	// tar.Reader reports EOF at the archive terminator. Drain the containing
	// gzip member to force CRC/trailer validation and reject any decoded bytes
	// after the tar terminator. Then prove that the compressed stream itself has
	// no second member or trailing payload.
	trailingDecoded, err := io.Copy(io.Discard, decodedReader)
	if err != nil || trailingDecoded != 0 || decodedReader.N <= 0 {
		return unknown
	}
	if _, err := compressed.Peek(1); err != io.EOF {
		return unknown
	}
	if !manifestSeen {
		return Verification{Status: "legacy_unverified"}
	}
	if decoded.Version < CurrentManifestVersion {
		return Verification{Status: "legacy_unverified", ManifestVersion: decoded.Version, Build: strings.TrimSpace(decoded.Build)}
	}
	verification := Verification{
		Status: decoded.BackupStatus, ManifestVersion: decoded.Version, Build: strings.TrimSpace(decoded.Build),
		CredentialsIncluded: decoded.CredentialsIncluded, RunnableRestore: decoded.RunnableRestore,
	}
	if decoded.Version != CurrentManifestVersion || credentialEntrySeen || decoded.CredentialsIncluded || decoded.RunnableRestore || decoded.BackupStatus != "credentials_excluded" || !manifestExcludesCredentials(decoded) {
		return unknown
	}
	return verification
}

func manifestExcludesCredentials(value manifest) bool {
	for _, item := range value.Excluded {
		if strings.Contains(item, "codex-loom/credentials/**") && strings.Contains(item, "not a complete runnable restore") {
			return true
		}
	}
	return false
}

func isSnapshotName(name string) bool {
	if !strings.HasSuffix(name, ".tar.gz") {
		return false
	}
	return strings.HasPrefix(name, "codex-loom-") || strings.HasPrefix(name, "codex-hub-")
}

func Prune(backupDir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := applyRetentionAt(backupDir, RetentionPolicy{MaxCount: keep}, time.Now().UTC())
	return err
}

func walkDataDir(dataDir, backupDir string, fn func(src, rel string)) error {
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	backupAbs, _ := filepath.Abs(backupDir)
	eventsAbs, _ := filepath.Abs(filepath.Join(dataAbs, "events"))
	credentialsAbs, _ := filepath.Abs(filepath.Join(dataAbs, "credentials"))
	return filepath.WalkDir(dataAbs, func(path string, d os.DirEntry, err error) error {
		pathAbs, _ := filepath.Abs(path)
		if pathAbs == credentialsAbs || strings.HasPrefix(pathAbs, credentialsAbs+string(os.PathSeparator)) {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err != nil {
			return nil
		}
		if path == dataAbs {
			return nil
		}
		if d.IsDir() {
			pAbs, _ := filepath.Abs(path)
			if pAbs == backupAbs || strings.HasPrefix(pAbs, backupAbs+string(os.PathSeparator)) || pAbs == eventsAbs {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(dataAbs, path)
		if err != nil {
			return nil
		}
		fn(path, rel)
		return nil
	})
}

func addFile(tw *tar.Writer, src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	size := info.Size()
	if strings.HasSuffix(strings.ToLower(src), ".ndjson") {
		size, err = completeNDJSONSize(f, size)
		if err != nil {
			return err
		}
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(dst)
	hdr.Size = size
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(f, size))
	if err != nil {
		return err
	}
	if n != size {
		return io.ErrUnexpectedEOF
	}
	return err
}

func completeNDJSONSize(file *os.File, size int64) (int64, error) {
	const blockSize = int64(64 << 10)
	buffer := make([]byte, blockSize)
	for end := size; end > 0; {
		start := end - blockSize
		if start < 0 {
			start = 0
		}
		n, err := file.ReadAt(buffer[:end-start], start)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if index := bytes.LastIndexByte(buffer[:n], '\n'); index >= 0 {
			return start + int64(index) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func findRollouts(sessionsDir string, sessions []SessionRef) ([]string, []string) {
	threadSet := map[string]string{}
	for _, s := range sessions {
		if s.ThreadID != "" {
			threadSet[s.ThreadID] = s.Name
		}
	}
	if len(threadSet) == 0 {
		return nil, nil
	}
	found := map[string][]string{}
	var out []string
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		for threadID := range threadSet {
			if strings.HasSuffix(name, "-"+threadID+".jsonl") {
				out = append(out, path)
				found[threadID] = append(found[threadID], path)
				break
			}
		}
		return nil
	})
	var warnings []string
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("walk codex sessions failed: %v", err))
	}
	for threadID, name := range threadSet {
		if len(found[threadID]) == 0 {
			warnings = append(warnings, fmt.Sprintf("rollout not found for %s (%s)", name, threadID))
		}
	}
	sort.Strings(out)
	return out, warnings
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "snapshot"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
