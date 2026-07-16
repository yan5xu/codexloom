package rollout

import (
	"os"
	"path/filepath"
	"strings"
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
{"timestamp":"2026-07-03T07:01:21.000Z","type":"response_item","payload":{"type":"function_call","name":"view_image","call_id":"img1","arguments":"{\"path\":\"/tmp/screenshot.png\",\"detail\":\"high\"}"}}
{"timestamp":"2026-07-03T07:05:44.694Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"c2","success":true,"changes":{"/repo/x.md":{"type":"add","content":"# x"}}}}
{"timestamp":"2026-07-03T07:06:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"UNIQUE-FINAL-ANSWER-42","phase":"final_answer"}}
{"timestamp":"2026-07-03T07:06:01.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
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
	// user, thinking(commentary), command, image, file_change, answer(final)
	for _, want := range []string{"user", "thinking", "command", "image", "file_change", "answer"} {
		if types[want] == 0 {
			t.Errorf("turn1 missing item type %q (got %v)", want, types)
		}
	}
	if got := t1.Items[0]["timestamp"]; got != "2026-07-03T07:01:13.545Z" {
		t.Errorf("user timestamp = %v, want rollout timestamp", got)
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

	var image map[string]any
	for _, it := range t1.Items {
		if it["type"] == "image" {
			image = it
		}
	}
	if image["path"] != "/tmp/screenshot.png" {
		t.Errorf("image path = %v, want /tmp/screenshot.png", image["path"])
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

func TestReadFilePreservesUserLocalImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-07-16T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-image"}}`,
		`{"timestamp":"2026-07-16T01:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Describe this","images":[],"local_images":["/tmp/loom/screenshot.png"]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript, err := ReadFile(path, "thread-image")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Turns) != 1 || len(transcript.Turns[0].Items) != 1 {
		t.Fatalf("transcript = %#v", transcript)
	}
	attachments, ok := transcript.Turns[0].Items[0]["attachments"].([]map[string]any)
	if !ok || len(attachments) != 1 || attachments[0]["path"] != "/tmp/loom/screenshot.png" || attachments[0]["mimeType"] != "image/png" {
		t.Fatalf("attachments = %#v", transcript.Turns[0].Items[0]["attachments"])
	}
}

func TestFindRolloutMissing(t *testing.T) {
	t.Setenv("CODEX_SESSIONS_DIR", t.TempDir())
	if _, err := FindRollout("nope"); err == nil {
		t.Fatal("expected error for missing rollout")
	}
}

func TestLatestTurnDetectsRunning(t *testing.T) {
	const threadID = "test-thread-running"
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "03")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(day, "rollout-2026-07-03T15-01-11-"+threadID+".jsonl")
	data := `{"timestamp":"2026-07-03T08:00:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-running"}}
{"timestamp":"2026-07-03T08:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"keep working"}}
{"timestamp":"2026-07-03T08:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"still doing it","phase":"commentary"}}
`
	if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	latest, err := LatestTurn(threadID)
	if err != nil {
		t.Fatalf("LatestTurn: %v", err)
	}
	if latest == nil {
		t.Fatal("latest is nil")
	}
	if latest.ID != "turn-running" || latest.Status != "running" || latest.Task != "keep working" {
		t.Fatalf("latest = %#v, want running turn with task", latest)
	}
}

func TestReadWindowPagesNewestTurnsAndTracksAppend(t *testing.T) {
	const threadID = "test-thread-window"
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-07-15T10-00-00-"+threadID+".jsonl")
	turn := func(id, task, status string) string {
		return `{"timestamp":"2026-07-15T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"` + id + `"}}` + "\n" +
			`{"timestamp":"2026-07-15T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"` + task + `"}}` + "\n" +
			`{"timestamp":"2026-07-15T10:00:02Z","type":"event_msg","payload":{"type":"` + status + `","turn_id":"` + id + `"}}` + "\n"
	}
	initial := turn("turn-1", "first", "task_complete") +
		turn("turn-2", "second", "task_complete") +
		turn("turn-3", "third", "turn_aborted")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	latest, total, err := ReadWindow(threadID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(latest.Turns) != 1 || latest.Turns[0].ID != "turn-3" || latest.Turns[0].Status != "interrupted" {
		t.Fatalf("latest page = total %d, turns %#v", total, latest.Turns)
	}
	middle, total, err := ReadWindow(threadID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(middle.Turns) != 1 || middle.Turns[0].ID != "turn-2" || middle.Turns[0].Status != "completed" {
		t.Fatalf("middle page = total %d, turns %#v", total, middle.Turns)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(turn("turn-4", "fourth", "task_complete")); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	appended, total, err := ReadWindow(threadID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(appended.Turns) != 1 || appended.Turns[0].ID != "turn-4" {
		t.Fatalf("appended page = total %d, turns %#v", total, appended.Turns)
	}
	summary, err := LatestTurn(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.ID != "turn-4" || summary.Task != "fourth" || summary.Status != "completed" {
		t.Fatalf("latest summary = %#v", summary)
	}
}

func TestReadWindowWaitsForCompleteTrailingLine(t *testing.T) {
	const threadID = "test-thread-partial-line"
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-07-15T10-00-00-"+threadID+".jsonl")
	complete := `{"timestamp":"2026-07-15T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n"
	partial := `{"timestamp":"2026-07-15T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"partial"`
	if err := os.WriteFile(path, []byte(complete+partial), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	summary, err := LatestTurn(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Task != "" {
		t.Fatalf("partial line was indexed: %#v", summary)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`}}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	summary, err = LatestTurn(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Task != "partial" {
		t.Fatalf("completed trailing line was not indexed: %#v", summary)
	}
}

func TestReadUsageUsesCumulativeDeltasAndIgnoresReconnectSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"timestamp":"2026-07-12T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-12T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6"}}
{"timestamp":"2026-07-12T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110},"model_context_window":1000}}}
{"timestamp":"2026-07-12T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":100,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":200},"last_token_usage":{"input_tokens":80,"cached_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":90},"model_context_window":1000}}}
{"timestamp":"2026-07-13T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-13T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-5.6-sol"}}
{"timestamp":"2026-07-13T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":100,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":200},"last_token_usage":{"input_tokens":80,"cached_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":90},"model_context_window":1000}}}
{"timestamp":"2026-07-13T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"cached_input_tokens":150,"output_tokens":30,"reasoning_output_tokens":9,"total_tokens":280},"last_token_usage":{"input_tokens":70,"cached_input_tokens":50,"output_tokens":10,"reasoning_output_tokens":4,"total_tokens":80},"model_context_window":1000}}}
{"timestamp":"2026-07-13T02:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-3"}}
{"timestamp":"2026-07-13T02:00:01Z","type":"turn_context","payload":{"turn_id":"turn-3","model":"gpt-5.6-terra"}}
{"timestamp":"2026-07-13T02:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"cached_input_tokens":150,"output_tokens":30,"reasoning_output_tokens":9,"total_tokens":280},"last_token_usage":{"input_tokens":70,"cached_input_tokens":50,"output_tokens":10,"reasoning_output_tokens":4,"total_tokens":80},"model_context_window":1000}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ReadUsageFile(path)
	if err != nil {
		t.Fatalf("ReadUsageFile: %v", err)
	}
	if report.Lifetime.TotalTokens != 280 || report.Lifetime.InputTokens != 250 || report.Lifetime.Calls != 3 {
		t.Fatalf("lifetime = %#v, want cumulative 280 tokens across 3 unique calls", report.Lifetime)
	}
	if len(report.Events) != 3 {
		t.Fatalf("events = %d, want duplicate reconnect snapshot omitted", len(report.Events))
	}
	if len(report.Turns) != 2 || report.Turns[0].Usage.TotalTokens != 200 || report.Turns[1].Usage.TotalTokens != 80 {
		t.Fatalf("turns = %#v", report.Turns)
	}
	if report.Turns[0].Model != "gpt-5.6" || report.Turns[1].Model != "gpt-5.6-sol" {
		t.Fatalf("turn models = %#v", report.Turns)
	}
	if report.ContextInputTokens != 70 || report.ModelContextWindow != 1000 {
		t.Fatalf("context = %d/%d", report.ContextInputTokens, report.ModelContextWindow)
	}
	if report.LatestModel != "gpt-5.6-sol" || report.LastUpdatedAt != "2026-07-13T01:00:03Z" {
		t.Fatalf("latest call was replaced by reconnect snapshot: model=%q updated=%q", report.LatestModel, report.LastUpdatedAt)
	}
}

func TestReadUsageIgnoresStaleCumulativeSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"timestamp":"2026-07-12T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-12T01:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110},"last_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}}
{"timestamp":"2026-07-12T02:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-12T02:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"output_tokens":5,"total_tokens":35},"last_token_usage":{"input_tokens":30,"output_tokens":5,"total_tokens":35}}}}
{"timestamp":"2026-07-12T02:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":125,"output_tokens":15,"total_tokens":140},"last_token_usage":{"input_tokens":40,"output_tokens":5,"total_tokens":45}}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Lifetime.TotalTokens != 140 || report.Lifetime.InputTokens != 125 || report.Lifetime.Calls != 2 {
		t.Fatalf("lifetime = %#v, want cumulative high-water usage", report.Lifetime)
	}
	if len(report.Events) != 2 || len(report.Turns) != 2 || report.Turns[1].Usage.TotalTokens != 30 {
		t.Fatalf("stale snapshot was counted: events=%#v turns=%#v", report.Events, report.Turns)
	}
}

func TestReadUsageIncrementallyParsesAppendedLines(t *testing.T) {
	const threadID = "incremental-usage-thread"
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "07", "13")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-07-13T10-00-00-"+threadID+".jsonl")
	firstLine := `{"timestamp":"2026-07-13T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-13T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110},"last_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}}
`
	if err := os.WriteFile(path, []byte(firstLine), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SESSIONS_DIR", dir)

	first, err := ReadUsage(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lifetime.TotalTokens != 110 || first.Lifetime.Calls != 1 {
		t.Fatalf("first report = %#v", first.Lifetime)
	}
	unchanged, err := ReadUsage(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != first {
		t.Fatal("unchanged rollout did not reuse its immutable usage snapshot")
	}

	appended := `{"timestamp":"2026-07-13T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":125,"output_tokens":15,"total_tokens":140},"last_token_usage":{"input_tokens":25,"output_tokens":5,"total_tokens":30}}}}`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(appended); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	partial, err := ReadUsage(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Lifetime.TotalTokens != 110 {
		t.Fatalf("partial line was parsed: %#v", partial.Lifetime)
	}
	partialAgain, err := ReadUsage(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if partialAgain != partial {
		t.Fatal("unchanged partial line invalidated the usage snapshot")
	}

	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	updated, err := ReadUsage(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Lifetime.TotalTokens != 140 || updated.Lifetime.Calls != 2 {
		t.Fatalf("updated report = %#v", updated.Lifetime)
	}
	if updated == first || first.Lifetime.TotalTokens != 110 {
		t.Fatalf("cached snapshot was mutated: first=%#v updated=%#v", first.Lifetime, updated.Lifetime)
	}
}

func TestReadUsageProjectsTurnActivityIntervals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	data := `{"timestamp":"2026-07-15T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-07-15T01:30:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
{"timestamp":"2026-07-15T02:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-07-15T02:10:00Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-2"}}
{"timestamp":"2026-07-15T03:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-3"}}
{"timestamp":"2026-07-15T04:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-4"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Activity) != 4 {
		t.Fatalf("activity = %#v", report.Activity)
	}
	if report.Activity[0].Status != "completed" || report.Activity[0].EndedAt != "2026-07-15T01:30:00Z" {
		t.Fatalf("completed activity = %#v", report.Activity[0])
	}
	if report.Activity[1].Status != "interrupted" || report.Activity[1].EndedAt != "2026-07-15T02:10:00Z" {
		t.Fatalf("aborted activity = %#v", report.Activity[1])
	}
	if !report.Activity[2].InferredEnd || report.Activity[2].EndedAt != "2026-07-15T04:00:00Z" {
		t.Fatalf("inferred activity = %#v", report.Activity[2])
	}
	if report.Activity[3].Status != "running" || report.Activity[3].EndedAt != "" {
		t.Fatalf("open activity = %#v", report.Activity[3])
	}
}
