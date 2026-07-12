// Package backup creates local disaster-recovery snapshots for CodexLoom.
//
// A snapshot is a tar.gz archive containing CodexLoom's registry/log files and
// Codex rollout files for every known Agent. It intentionally remains a plain
// archive so restore does not depend on a running CodexLoom binary.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
}

type Snapshot struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	CreatedAt    time.Time `json:"createdAt"`
	Reason       string    `json:"reason"`
	SizeBytes    int64     `json:"sizeBytes"`
	FileCount    int       `json:"fileCount"`
	RolloutCount int       `json:"rolloutCount"`
	Warnings     []string  `json:"warnings,omitempty"`
}

type manifest struct {
	Version          int          `json:"version"`
	CreatedAt        string       `json:"createdAt"`
	Reason           string       `json:"reason"`
	DataDir          string       `json:"dataDir"`
	CodexSessionsDir string       `json:"codexSessionsDir"`
	EdgeNamesFile    string       `json:"edgeNamesFile,omitempty"`
	Agents           []AgentRef   `json:"agents"`
	Sessions         []SessionRef `json:"sessions,omitempty"`
	Files            []string     `json:"files"`
	Warnings         []string     `json:"warnings,omitempty"`
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
	if opts.MaxBackups <= 0 {
		opts.MaxBackups = 25
	}
	if len(opts.Agents) == 0 {
		opts.Agents = opts.Sessions
	}

	backupDir := DefaultDir(opts.DataDir)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}

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
		Version:          1,
		CreatedAt:        created.Format(time.RFC3339Nano),
		Reason:           opts.Reason,
		DataDir:          opts.DataDir,
		CodexSessionsDir: opts.CodexSessionsDir,
		EdgeNamesFile:    opts.EdgeNamesFile,
		Agents:           opts.Agents,
		Sessions:         opts.Agents,
	}

	add := func(src, dst string, required bool) {
		if src == "" {
			return
		}
		if err := addFile(tw, src, dst); err != nil {
			msg := fmt.Sprintf("%s: %v", src, err)
			if required {
				m.Warnings = append(m.Warnings, "required file skipped: "+msg)
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
		Name:         name,
		Path:         path,
		CreatedAt:    created,
		Reason:       opts.Reason,
		SizeBytes:    info.Size(),
		FileCount:    len(m.Files),
		RolloutCount: rolloutCount,
		Warnings:     m.Warnings,
	}
	_ = Prune(backupDir, opts.MaxBackups)
	return s, nil
}

func List(dataDir string) ([]Snapshot, error) {
	backupDir := DefaultDir(dataDir)
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
		out = append(out, Snapshot{
			Name:      e.Name(),
			Path:      filepath.Join(backupDir, e.Name()),
			CreatedAt: info.ModTime().UTC(),
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if out == nil {
		out = []Snapshot{}
	}
	return out, nil
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
	list, err := List(filepath.Dir(backupDir))
	if err != nil {
		return err
	}
	for i := keep; i < len(list); i++ {
		_ = os.Remove(list[i].Path)
	}
	return nil
}

func walkDataDir(dataDir, backupDir string, fn func(src, rel string)) error {
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	backupAbs, _ := filepath.Abs(backupDir)
	return filepath.WalkDir(dataAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == dataAbs {
			return nil
		}
		if d.IsDir() {
			pAbs, _ := filepath.Abs(path)
			if pAbs == backupAbs || strings.HasPrefix(pAbs, backupAbs+string(os.PathSeparator)) {
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
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(dst)
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(f, info.Size()))
	if err != nil {
		return err
	}
	if n < info.Size() {
		_, err = io.CopyN(tw, zeroReader{}, info.Size()-n)
	}
	return err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
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
