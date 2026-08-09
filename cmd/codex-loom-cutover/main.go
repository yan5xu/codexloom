// Command codex-loom-cutover is a one-shot external cutover supervisor and
// runbook runner. It lives outside the data directory and the Hub process so
// it can continue after the parent Hub stops. It executes bounded stages,
// writes a non-secret receipt, is idempotent and abortable, and only accepts
// exact expected hashes. It is not a resident supervisor platform.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type stage struct {
	Name    string            `json:"name"`
	Kind    string            `json:"kind"` // hash|snapshot|restore|exec|copy|launchctl
	Path    string            `json:"path,omitempty"`
	Expected string           `json:"expected,omitempty"`
	Src     string            `json:"src,omitempty"`
	Dst     string            `json:"dst,omitempty"`
	Label   string            `json:"label,omitempty"`
	Plist   string            `json:"plist,omitempty"`
	Action  string            `json:"action,omitempty"` // unload|load
	Command string            `json:"command,omitempty"`
	DataDir string            `json:"dataDir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type runbook struct {
	Stages    []stage `json:"stages"`
	Rollback  []stage `json:"rollback"`
}

type stateFile struct {
	Completed []string `json:"completed"`
	Status    string   `json:"status"` // running|ok|rolled_back|failed
	FailedAt  string   `json:"failedAt,omitempty"`
}

type receipt struct {
	Status    string            `json:"status"`
	FailedAt  string            `json:"failedAt,omitempty"`
	Completed []string          `json:"completed"`
	RolledBack bool             `json:"rolledBack"`
	Hashes    map[string]string `json:"hashes"`
	DataDir   string            `json:"dataDir"`
}

func main() {
	var (
		workdir   = flag.String("workdir", "", "working directory outside the data dir (state+receipt)")
		dataDir   = flag.String("data-dir", "", "data directory under cutover")
		runbookPath = flag.String("runbook", "", "JSON runbook path")
		snapshot  = flag.String("snapshot", "", "snapshot archive path (outside data dir)")
		receiptPath = flag.String("receipt", "", "receipt path (default workdir/receipt.json)")
		force     = flag.Bool("force", false, "re-run after a failed/rolled-back state")
	)
	flag.Parse()
	if err := run(*workdir, *dataDir, *runbookPath, *snapshot, *receiptPath, *force); err != nil {
		fmt.Fprintln(os.Stderr, "cutover failed:", err)
		os.Exit(1)
	}
}

func run(workdir, dataDir, runbookPath, snapshot, receiptPath string, force bool) error {
	if workdir == "" || dataDir == "" || runbookPath == "" {
		return fmt.Errorf("workdir, data-dir, and runbook are required")
	}
	if receiptPath == "" {
		receiptPath = filepath.Join(workdir, "receipt.json")
	}
	payload, err := os.ReadFile(runbookPath)
	if err != nil {
		return err
	}
	var book runbook
	if err := json.Unmarshal(payload, &book); err != nil {
		return fmt.Errorf("runbook is invalid: %w", err)
	}
	if len(book.Stages) == 0 || len(book.Rollback) == 0 {
		return fmt.Errorf("runbook requires stages and rollback")
	}
	statePath := filepath.Join(workdir, "state.json")
	state := stateFile{Status: "running"}
	if existing, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(existing, &state)
		if state.Status == "rolled_back" || state.Status == "failed" {
			if !force {
				return fmt.Errorf("previous cutover ended %s; use --force to re-run", state.Status)
			}
			state = stateFile{Status: "running"}
		}
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return err
	}
	done := map[string]bool{}
	for _, name := range state.Completed {
		done[name] = true
	}
	hashes := map[string]string{}
	executor := &executor{workdir: workdir, dataDir: dataDir, snapshot: snapshot, hashes: hashes, done: done}
	for _, s := range book.Stages {
		if done[s.Name] {
			state.Completed = append(state.Completed, s.Name)
			continue
		}
		if err := executor.execute(s); err != nil {
			state.Status = "failed"
			state.FailedAt = s.Name
			_ = saveJSON(statePath, state)
			rollbackErr := executor.rollback(book.Rollback)
			state.Status = "rolled_back"
			state.FailedAt = s.Name
			_ = saveJSON(statePath, state)
			rec := receipt{Status: "rolled_back", FailedAt: s.Name, Completed: state.Completed, RolledBack: rollbackErr == nil, Hashes: hashes, DataDir: dataDir}
			_ = saveJSON(receiptPath, rec)
			if rollbackErr != nil {
				return fmt.Errorf("stage %s failed: %w; rollback also failed: %v", s.Name, err, rollbackErr)
			}
			return fmt.Errorf("stage %s failed: %w; rolled back", s.Name, err)
		}
		state.Completed = append(state.Completed, s.Name)
		_ = saveJSON(statePath, state)
	}
	state.Status = "ok"
	_ = saveJSON(statePath, state)
	return saveJSON(receiptPath, receipt{Status: "ok", Completed: state.Completed, Hashes: hashes, DataDir: dataDir})
}

type executor struct {
	workdir  string
	dataDir  string
	snapshot string
	hashes   map[string]string
	done     map[string]bool
}

func (e *executor) execute(s stage) error {
	switch s.Kind {
	case "hash":
		got, err := sha256File(s.Path)
		if err != nil {
			return err
		}
		e.hashes[s.Path] = got
		if !strings.EqualFold(got, strings.TrimSpace(s.Expected)) {
			return fmt.Errorf("hash mismatch for %s: got %s want %s", s.Path, got, s.Expected)
		}
		return nil
	case "snapshot":
		if e.snapshot == "" {
			return fmt.Errorf("snapshot path is required")
		}
		return snapshotDir(e.dataDir, e.snapshot)
	case "restore":
		if e.snapshot == "" {
			return fmt.Errorf("snapshot path is required")
		}
		return restoreDir(e.dataDir, e.snapshot)
	case "copy":
		if err := copyFile(s.Src, s.Dst); err != nil {
			return err
		}
		got, _ := sha256File(s.Dst)
		if got != "" {
			e.hashes[s.Dst] = got
		}
		return nil
	case "exec":
		cmd := exec.Command("/bin/sh", "-c", s.Command)
		cmd.Env = append(os.Environ(), "CODEX_LOOM_CUTOVER_WORKDIR="+e.workdir, "CODEX_LOOM_CUTOVER_DATA="+e.dataDir)
		cmd.Dir = e.workdir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("exec failed (%s): %v", strings.TrimSpace(string(output)), err)
		}
		return nil
	case "launchctl":
		return launchctlAction(s.Action, s.Label, s.Plist)
	default:
		return fmt.Errorf("unsupported stage kind %q", s.Kind)
	}
}

func (e *executor) rollback(stages []stage) error {
	var first error
	for _, s := range stages {
		if err := e.execute(s); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func snapshotDir(dataDir, snapshot string) error {
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-cf", snapshot, "-C", filepath.Dir(dataDir), filepath.Base(dataDir))
	return cmd.Run()
}

func restoreDir(dataDir, snapshot string) error {
	if err := os.RemoveAll(dataDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dataDir), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-xf", snapshot, "-C", filepath.Dir(dataDir))
	return cmd.Run()
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
