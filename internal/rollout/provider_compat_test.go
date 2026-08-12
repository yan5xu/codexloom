package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reasoningRepairSample = `{"timestamp":"2026-08-06T01:00:00Z","type":"session_meta","payload":{"cwd":"/repo","originator":"codex-loom"}}
{"timestamp":"2026-08-06T01:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"keep developer message"}]}}
{"timestamp":"2026-08-06T01:00:02Z","type":"response_item","payload":{"type":"reasoning","id":"reason-1","summary":[],"content":[{"type":"reasoning_text","text":"plaintext reasoning"}],"encrypted_content":null}}
{"timestamp":"2026-08-06T01:00:03Z","type":"response_item","payload":{"type":"reasoning","id":"reason-2","summary":[],"content":[],"encrypted_content":null}}
{"timestamp":"2026-08-06T01:00:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
`

func writeReasoningRepairSample(t *testing.T, threadID string) string {
	t.Helper()
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "08", "06")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-08-06T01-00-00-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte(reasoningRepairSample), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)
	return path
}

func TestSanitizeReasoningContentClearsOnlyReasoningContent(t *testing.T) {
	const threadID = "reasoning-repair-0001"
	path := writeReasoningRepairSample(t, threadID)

	result, err := SanitizeReasoningContent(threadID, t.TempDir())
	if err != nil {
		t.Fatalf("SanitizeReasoningContent: %v", err)
	}
	if result.Changed != 1 {
		t.Fatalf("changed = %d, want 1", result.Changed)
	}
	if result.OriginalPath != path {
		t.Fatalf("original path = %q, want %q", result.OriginalPath, path)
	}
	if result.BackupPath == "" {
		t.Fatal("backup path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(strings.Split(strings.TrimSpace(reasoningRepairSample), "\n")) {
		t.Fatalf("line count changed: got %d", len(lines))
	}
	for _, line := range lines {
		switch {
		case strings.Contains(line, `"id":"reason-1"`):
			if !strings.Contains(line, `"content":[]`) || !strings.Contains(line, `"summary":[]`) {
				t.Fatalf("reason-1 not sanitized: %s", line)
			}
			if strings.Contains(line, "plaintext reasoning") {
				t.Fatalf("reason-1 plaintext content survived: %s", line)
			}
		case strings.Contains(line, `"id":"reason-2"`):
			if !strings.Contains(line, `"content":[]`) {
				t.Fatalf("reason-2 empty content changed unexpectedly: %s", line)
			}
		case strings.Contains(line, "keep developer message"):
			if !strings.Contains(line, `"type":"message"`) || !strings.Contains(line, `"content":[{"type":"input_text"`) {
				t.Fatalf("developer message changed: %s", line)
			}
		}
	}

	second, err := SanitizeReasoningContent(threadID, t.TempDir())
	if err != nil {
		t.Fatalf("second SanitizeReasoningContent: %v", err)
	}
	if second.Changed != 0 || second.BackupPath != "" {
		t.Fatalf("second repair changed=%d backup=%q, want 0/empty", second.Changed, second.BackupPath)
	}

	if err := RestoreRolloutBackup(result.BackupPath, result.OriginalPath); err != nil {
		t.Fatalf("RestoreRolloutBackup: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":[{"type":"reasoning_text","text":"plaintext reasoning"}]`) {
		t.Fatal("backup restore did not restore reasoning content")
	}
}

func TestSanitizeReasoningContentMissingRolloutIsNoop(t *testing.T) {
	t.Setenv("CODEX_SESSIONS_DIR", t.TempDir())
	result, err := SanitizeReasoningContent("missing-thread", t.TempDir())
	if err != nil {
		t.Fatalf("SanitizeReasoningContent missing rollout: %v", err)
	}
	if result.Changed != 0 || result.BackupPath != "" || result.OriginalPath != "" {
		t.Fatalf("missing rollout result = %#v, want no-op", result)
	}
}
