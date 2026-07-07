// Package rollout reads a codex session's real history directly from the
// rollout file that `codex app-server` itself writes. codex-hub does NOT keep
// a parallel history log: the single source of truth for what happened in a
// session is its rollout file on disk. Any session — created by codex-hub,
// imported from pinix-edge, or started by any other codex client — shows its
// full history the moment we can find its rollout by threadId.
//
// Rollout layout (written by codex, we only ever read it):
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-<ISO8601>-<threadId>.jsonl
//
// Each line is one JSON object: {"timestamp","type","payload"}. Relevant
// top-level types:
//
//	session_meta   once, session metadata (cwd, originator, model)
//	turn_context   per-turn context (turn_id, cwd, model, approval policy)
//	event_msg      streamed events; payload.type in {task_started, user_message,
//	               agent_message, patch_apply_end, task_complete, turn_aborted, ...}
//	response_item  canonical conversation items; payload.type in {message,
//	               function_call, function_call_output, reasoning, custom_tool_call, ...}
//	compacted      history compaction marker
//
// We reconstruct a clean transcript from the human-facing streams:
//   - user text   ← event_msg/user_message   (clean prompt, no injected AGENTS.md noise)
//   - agent text  ← event_msg/agent_message   (phase final_answer → answer, else thinking)
//   - commands    ← response_item/function_call name=exec_command + its function_call_output
//   - file edits  ← event_msg/patch_apply_end  (full change set with paths + kinds)
//
// Turns are delimited by event_msg/task_started (one per user task).
package rollout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultSessionsDir is where codex writes rollout files. Override with
// CODEX_SESSIONS_DIR (mainly for tests).
func DefaultSessionsDir() string {
	if d := os.Getenv("CODEX_SESSIONS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex/sessions"
	}
	return filepath.Join(home, ".codex", "sessions")
}

// Turn is one user task and everything codex did in response.
type Turn struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Items  []map[string]any `json:"items"`
}

// Transcript is the full parsed history of one session.
type Transcript struct {
	ThreadID string
	Cwd      string
	Path     string // rollout file we read
	Turns    []Turn
}

// FindRollout locates the rollout file for a threadId by recursively globbing
// the codex sessions dir for rollout-*-<threadId>.jsonl. If several match
// (rare), the lexically-last one (newest ISO timestamp in the name) wins.
func FindRollout(threadID string) (string, error) {
	if threadID == "" {
		return "", fmt.Errorf("empty threadId")
	}
	dir := DefaultSessionsDir()
	var best string
	suffix := "-" + threadID + ".jsonl"
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, suffix) {
			if path > best {
				best = path
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("no rollout file for threadId %s under %s", threadID, dir)
	}
	return best, nil
}

var exitCodeRe = regexp.MustCompile(`(?m)(?:Process exited with code|Exit code:)\s+(-?\d+)`)

type line struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Read parses the whole rollout for threadID into a Transcript.
func Read(threadID string) (*Transcript, error) {
	path, err := FindRollout(threadID)
	if err != nil {
		return nil, err
	}
	return ReadFile(path, threadID)
}

// ReadFile parses a specific rollout file.
func ReadFile(path, threadID string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tr := &Transcript{ThreadID: threadID, Path: path}

	// Pass 1: index command outputs by call_id so we can attach them to their
	// function_call (the output line follows the call line, but a two-pass keeps
	// the join order-independent).
	outputs := map[string]cmdOutput{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	var raw [][]byte
	for sc.Scan() {
		b := strings.TrimSpace(sc.Text())
		if b == "" {
			continue
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		raw = append(raw, cp)
		var ln line
		if json.Unmarshal(cp, &ln) != nil {
			continue
		}
		if ln.Type == "response_item" {
			var p struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Output string `json:"output"`
			}
			if json.Unmarshal(ln.Payload, &p) == nil &&
				(p.Type == "function_call_output" || p.Type == "custom_tool_call_output") {
				outputs[p.CallID] = parseCmdOutput(p.Output)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Pass 2: build turns.
	for _, cp := range raw {
		var ln line
		if json.Unmarshal(cp, &ln) != nil {
			continue
		}
		switch ln.Type {
		case "session_meta":
			var p struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(ln.Payload, &p) == nil && tr.Cwd == "" {
				tr.Cwd = p.Cwd
			}
		case "event_msg":
			tr.handleEvent(ln.Payload, outputs)
		case "response_item":
			tr.handleResponseItem(ln.Payload, outputs)
		}
	}
	return tr, nil
}

type cmdOutput struct {
	text     string
	exitCode *int
}

func parseCmdOutput(out string) cmdOutput {
	co := cmdOutput{text: out}
	if m := exitCodeRe.FindStringSubmatch(out); m != nil {
		var code int
		if _, err := fmt.Sscanf(m[1], "%d", &code); err == nil {
			co.exitCode = &code
		}
	}
	return co
}

func (tr *Transcript) cur() *Turn {
	if len(tr.Turns) == 0 {
		tr.Turns = append(tr.Turns, Turn{Status: "completed"})
	}
	return &tr.Turns[len(tr.Turns)-1]
}

func (tr *Transcript) handleEvent(payload json.RawMessage, outputs map[string]cmdOutput) {
	var p struct {
		Type    string `json:"type"`
		TurnID  string `json:"turn_id"`
		Message string `json:"message"`
		Phase   string `json:"phase"`
		Changes map[string]struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"changes"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return
	}
	switch p.Type {
	case "task_started":
		// New turn boundary.
		tr.Turns = append(tr.Turns, Turn{ID: p.TurnID, Status: "completed"})
	case "user_message":
		if strings.TrimSpace(p.Message) == "" {
			return
		}
		t := tr.cur()
		t.Items = append(t.Items, map[string]any{"type": "user", "text": p.Message})
	case "agent_message":
		if strings.TrimSpace(p.Message) == "" {
			return
		}
		typ := "thinking"
		if p.Phase == "final_answer" || p.Phase == "" {
			typ = "answer"
		}
		t := tr.cur()
		t.Items = append(t.Items, map[string]any{"type": typ, "text": p.Message})
	case "patch_apply_end":
		changes := []map[string]any{}
		for path, ch := range p.Changes {
			changes = append(changes, map[string]any{
				"path": path, "kind": ch.Type, "diff": truncate(ch.Content, 4000),
			})
		}
		if len(changes) == 0 {
			return
		}
		t := tr.cur()
		t.Items = append(t.Items, map[string]any{"type": "file_change", "changes": changes})
	case "turn_aborted":
		tr.cur().Status = "interrupted"
	}
}

func (tr *Transcript) handleResponseItem(payload json.RawMessage, outputs map[string]cmdOutput) {
	var p struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return
	}
	// Generated images: carry the base64 PNG as a data URI so the UI can render
	// it inline (the rollout stores it in `result`).
	if p.Type == "image_generation_call" {
		var img struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(payload, &img) == nil && img.Result != "" {
			tr.cur().Items = append(tr.cur().Items, map[string]any{
				"type": "image",
				"data": "data:image/png;base64," + img.Result,
			})
		}
		return
	}
	if p.Type == "function_call" && p.Name == "view_image" {
		var argv struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(p.Arguments), &argv)
		if argv.Path != "" {
			tr.cur().Items = append(tr.cur().Items, map[string]any{
				"type": "image",
				"path": argv.Path,
			})
		}
		return
	}
	// Only exec_command function calls render as shell commands; apply_patch is
	// covered by event_msg/patch_apply_end, other tools are internal.
	if p.Type != "function_call" || p.Name != "exec_command" {
		return
	}
	var argv struct {
		Cmd     string `json:"cmd"`
		Workdir string `json:"workdir"`
	}
	_ = json.Unmarshal([]byte(p.Arguments), &argv)
	item := map[string]any{
		"type":    "command",
		"command": argv.Cmd,
		"cwd":     argv.Workdir,
		"status":  "completed",
	}
	if co, ok := outputs[p.CallID]; ok {
		item["output"] = truncate(co.text, 4000)
		if co.exitCode != nil {
			item["exitCode"] = *co.exitCode
		}
	}
	t := tr.cur()
	t.Items = append(t.Items, item)
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n[... %d chars truncated]", len(s)-limit)
}
