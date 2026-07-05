package rollout

import (
	"os"
	"path/filepath"
	"testing"
)

// A minimal synthetic rollout exercising every branch the parser cares about:
// two turns, clean user/agent text, an exec_command with paired output, and a
// patch_apply_end file change.
const sample = `{"timestamp":"2026-07-03T07:01:11.489Z","type":"session_meta","payload":{"cwd":"/repo","originator":"test"}}
{"timestamp":"2026-07-03T07:01:11.489Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-03T07:01:13.545Z","type":"event_msg","payload":{"type":"user_message","message":"first task please"}}
{"timestamp":"2026-07-03T07:01:19.873Z","type":"event_msg","payload":{"type":"agent_message","message":"thinking out loud","phase":"commentary"}}
{"timestamp":"2026-07-03T07:01:19.877Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"echo hi\",\"workdir\":\"/repo\"}"}}
{"timestamp":"2026-07-03T07:01:20.063Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"hi\nProcess exited with code 0\n"}}
{"timestamp":"2026-07-03T07:05:44.694Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"c2","success":true,"changes":{"/repo/x.md":{"type":"add","content":"# x"}}}}
{"timestamp":"2026-07-03T07:06:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"UNIQUE-FINAL-ANSWER-42","phase":"final_answer"}}
{"timestamp":"2026-07-03T08:00:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-03T08:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"second task"}}
{"timestamp":"2026-07-03T08:00:02.000Z","type":"event_msg","payload":{"type":"turn_aborted"}}
`

func writeSample(t *testing.T, threadID string) {
	t.Helper()
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "03")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(day, "rollout-2026-07-03T15-01-11-"+threadID+".jsonl")
	if err := os.WriteFile(f, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)
}

func TestReadParsesTurnsAndItems(t *testing.T) {
	const threadID = "test-thread-0001"
	writeSample(t, threadID)

	tr, err := Read(threadID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tr.Cwd != "/repo" {
		t.Errorf("cwd = %q, want /repo", tr.Cwd)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(tr.Turns))
	}

	t1 := tr.Turns[0]
	if t1.ID != "turn-1" || t1.Status != "completed" {
		t.Errorf("turn1 id/status = %q/%q", t1.ID, t1.Status)
	}
	types := map[string]int{}
	for _, it := range t1.Items {
		types[it["type"].(string)]++
	}
	// user, thinking(commentary), command, file_change, answer(final)
	for _, want := range []string{"user", "thinking", "command", "file_change", "answer"} {
		if types[want] == 0 {
			t.Errorf("turn1 missing item type %q (got %v)", want, types)
		}
	}

	// exec_command output + exit code attached.
	var cmd map[string]any
	for _, it := range t1.Items {
		if it["type"] == "command" {
			cmd = it
		}
	}
	if cmd["command"] != "echo hi" {
		t.Errorf("command = %v", cmd["command"])
	}
	if cmd["exitCode"] != 0 {
		t.Errorf("exitCode = %v, want 0", cmd["exitCode"])
	}

	// final answer text preserved verbatim.
	var final string
	for _, it := range t1.Items {
		if it["type"] == "answer" {
			final = it["text"].(string)
		}
	}
	if final != "UNIQUE-FINAL-ANSWER-42" {
		t.Errorf("final answer = %q", final)
	}

	// turn_aborted marks the second turn interrupted.
	if tr.Turns[1].Status != "interrupted" {
		t.Errorf("turn2 status = %q, want interrupted", tr.Turns[1].Status)
	}
}

func TestFindRolloutMissing(t *testing.T) {
	t.Setenv("CODEX_SESSIONS_DIR", t.TempDir())
	if _, err := FindRollout("nope"); err == nil {
		t.Fatal("expected error for missing rollout")
	}
}
