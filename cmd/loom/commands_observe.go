package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func cmdHistory(a args) {
	if len(a.positional) < 1 {
		usage("history <name|id>")
	}
	count := a.flags["count"]
	if count == "" {
		count = "10"
	}
	resp, err := api("GET", "/api/agents/"+url.PathEscape(a.positional[0])+"/thread/history?count="+count, nil)
	if err != nil {
		fail(err)
	}
	turns, _ := resp["turns"].([]any)
	fmt.Printf("%s %s — %d turn(s)\n\n", bold(str(resp, "name")), dim(str(resp, "threadId")), len(turns))
	for _, tv := range turns {
		t, _ := tv.(map[string]any)
		fmt.Printf("%s %s %s\n", magenta("── turn"), str(t, "id"), dim("["+str(t, "status")+"]"))
		items, _ := t["items"].([]any)
		for _, iv := range items {
			printHistoryItem(iv.(map[string]any))
		}
		fmt.Println()
	}
}

func printHistoryItem(item map[string]any) {
	switch str(item, "type") {
	case "user":
		fmt.Printf("  %s %s\n", cyan("user>"), oneline(str(item, "text"), 200))
	case "answer":
		fmt.Printf("  %s %s\n", green("codex>"), indent(strings.TrimSpace(str(item, "text"))))
	case "thinking":
		fmt.Printf("  %s\n", dim("think: "+oneline(str(item, "text"), 160)))
	case "reasoning":
		fmt.Printf("  %s\n", dim("reason: "+oneline(str(item, "text"), 160)))
	case "command":
		exitCode := "?"
		if v, ok := item["exitCode"].(float64); ok {
			exitCode = fmt.Sprintf("%.0f", v)
		}
		dur := "?"
		if v, ok := item["durationMs"].(float64); ok {
			dur = fmt.Sprintf("%.0fms", v)
		}
		code := red("exit " + exitCode)
		if exitCode == "0" {
			code = green("exit 0")
		}
		fmt.Printf("  %s %s %s\n", yellow("$"), oneline(str(item, "command"), 160), dim("[")+code+dim(", "+dur+"]"))
	case "file_change":
		changes, _ := item["changes"].([]any)
		for _, cv := range changes {
			c, _ := cv.(map[string]any)
			fmt.Printf("  %s %s %s\n", magenta("edit"), str(c, "kind"), str(c, "path"))
		}
	}
}

// ---- watch (SSE) ----

func cmdWatch(a args) {
	if len(a.positional) < 1 {
		usage("watch <name|id>")
	}
	key := a.positional[0]
	tail := a.flags["tail"]
	if tail == "" {
		tail = "50"
	}
	resp, err := api("GET", "/api/agents/"+url.PathEscape(key), nil)
	if err != nil {
		fail(err)
	}
	s, _ := resp["agent"].(map[string]any)
	fmt.Println(dim(fmt.Sprintf("watching %s (%s) — status: %s — Ctrl-C detaches (task keeps running)\n",
		str(s, "name"), str(s, "id"), str(s, "status"))))

	eventsPath := "/api/agents/" + url.PathEscape(key) + "/thread/events?tail=" + tail
	if legacyAgentAPI {
		eventsPath = legacyAgentPath(eventsPath)
	}
	req, _ := http.NewRequest("GET", base+eventsPath, nil)
	req.Header.Set("Accept", "text/event-stream")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != 200 {
		fail(fmt.Errorf("events stream: %s", httpResp.Status))
	}

	state := &watchState{}
	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Seq  int64           `json:"seq"`
			TS   string          `json:"ts"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(line[6:]), &ev); err != nil {
			continue
		}
		renderEvent(state, ev.Seq, ev.TS, ev.Type, ev.Data)
	}
	fmt.Println(dim("\nstream closed by hub"))
}

type watchState struct {
	streamOpen  bool
	streamThink bool
	streamed    map[string]bool // itemIds whose text already streamed via deltas
}

func (st *watchState) closeStream() {
	if st.streamOpen {
		fmt.Println()
		st.streamOpen = false
	}
}

func tsShort(ts string) string {
	if len(ts) >= 19 {
		return dim(ts[11:19])
	}
	return dim(ts)
}

func renderEvent(st *watchState, seq int64, ts, typ string, data json.RawMessage) {
	var d map[string]any
	_ = json.Unmarshal(data, &d)
	t := tsShort(ts)
	typ = canonicalWatchEventType(typ)

	// Streaming deltas: agent text inline, reasoning dimmed.
	if strings.HasSuffix(typ, "/delta") && (strings.Contains(typ, "agentMessage") || strings.Contains(typ, "reasoning")) {
		text := deltaText(d["delta"])
		if text == "" {
			return
		}
		isThink := strings.Contains(typ, "reasoning")
		if st.streamed == nil {
			st.streamed = map[string]bool{}
		}
		if id, ok := d["itemId"].(string); ok && !isThink {
			st.streamed[id] = true
		}
		if !st.streamOpen || st.streamThink != isThink {
			st.closeStream()
			if isThink {
				fmt.Print(dim("think "))
			} else {
				fmt.Print(green("codex> "))
			}
			st.streamOpen = true
			st.streamThink = isThink
		}
		if isThink {
			fmt.Print(dim(text))
		} else {
			fmt.Print(text)
		}
		return
	}
	st.closeStream()

	switch typ {
	case "loom/live":
		fmt.Println(dim(fmt.Sprintf("─── live (replayed up to seq %d) ───", seq)))
	case "loom/agent-created":
		fmt.Printf("%s %s %v @ %v\n", t, dim("agent created:"), d["name"], d["cwd"])
	case "loom/user-message":
		fmt.Printf("%s %s %v\n", t, cyan("user>"), d["text"])
	case "loom/turn-started":
		fmt.Printf("%s %s\n", t, dim(fmt.Sprintf("turn started %v", d["turnId"])))
	case "loom/turn-completed":
		secs := 0.0
		if v, ok := d["durationMs"].(float64); ok {
			secs = v / 1000
		}
		fmt.Printf("%s %s %s\n", t, green("✔ turn completed"), dim(fmt.Sprintf("(%.0fs)", secs)))
	case "loom/turn-interrupted":
		reason, _ := d["reason"].(string)
		if reason == "" {
			reason, _ = d["error"].(string)
		}
		fmt.Printf("%s %s %s\n", t, yellow("■ turn interrupted"), dim(reason))
	case "loom/turn-failed":
		fmt.Printf("%s %s\n", t, red(fmt.Sprintf("✖ turn failed: %v", d["error"])))
	case "loom/agent-archived":
		fmt.Printf("%s %s\n", t, yellow("agent archived"))
	case "loom/approval-requested":
		params, _ := json.Marshal(d["params"])
		fmt.Printf("%s %s %v %s\n", t, red("⚠ approval requested"), d["method"], dim(clip(string(params), 120)))
		fmt.Printf("%s %s\n", t, dim(fmt.Sprintf("  resolve: %s approve <agent> %v — or via web console", commandName, d["approvalId"])))
	case "loom/approval-resolved":
		fmt.Printf("%s %s %s\n", t, green(fmt.Sprintf("approval %v", d["decision"])), dim(fmt.Sprintf("%v", d["approvalId"])))
	case "loom/error", "loom/host-error":
		fmt.Printf("%s %s %v\n", t, red("CodexLoom error:"), d["message"])
	case "item/started", "item/completed":
		item, _ := d["item"].(map[string]any)
		renderItem(st, t, typ, item)
	default:
		if os.Getenv("CODEX_LOOM_DEBUG") != "" || os.Getenv("CHUB_DEBUG") != "" {
			raw, _ := json.Marshal(d)
			fmt.Printf("%s %s %s\n", t, dim(typ), dim(clip(string(raw), 160)))
		}
	}
}

func canonicalWatchEventType(typ string) string {
	if typ == "hub/session-created" {
		return "loom/agent-created"
	}
	if typ == "hub/session-killed" {
		return "loom/agent-archived"
	}
	if strings.HasPrefix(typ, "hub/") {
		return "loom/" + strings.TrimPrefix(typ, "hub/")
	}
	return typ
}

func renderItem(st *watchState, t, phase string, item map[string]any) {
	switch str(item, "type") {
	case "commandExecution":
		cmd := oneline(str(item, "command"), 160)
		if phase == "item/started" {
			fmt.Printf("%s %s %s %s\n", t, yellow("$"), cmd, dim("..."))
			return
		}
		exitCode := "?"
		if v, ok := item["exitCode"].(float64); ok {
			exitCode = fmt.Sprintf("%.0f", v)
		}
		dur := "?"
		if v, ok := item["durationMs"].(float64); ok {
			dur = fmt.Sprintf("%.0fms", v)
		}
		code := red("exit " + exitCode)
		if exitCode == "0" {
			code = green("exit 0")
		}
		fmt.Printf("%s %s %s %s\n", t, yellow("$"), cmd, dim("[")+code+dim(" "+dur+"]"))
		if out := strings.TrimSpace(str(item, "aggregatedOutput")); out != "" {
			for _, line := range strings.Split(clip(out, 600), "\n") {
				fmt.Println(dim("    │ " + line))
			}
		}
	case "agentMessage":
		if phase == "item/completed" {
			if st.streamed[str(item, "id")] {
				return // already streamed via deltas
			}
			ph := str(item, "phase")
			if ph == "final_answer" || ph == "" {
				fmt.Printf("%s %s %s\n", t, green("codex>"), strings.TrimSpace(str(item, "text")))
			}
		}
	case "fileChange":
		if phase != "item/completed" {
			return
		}
		changes, _ := item["changes"].([]any)
		for _, cv := range changes {
			c, _ := cv.(map[string]any)
			kind := ""
			if k, ok := c["kind"].(map[string]any); ok {
				kind = str(k, "type")
			} else {
				kind = str(c, "kind")
			}
			fmt.Printf("%s %s %s %s\n", t, magenta("edit"), kind, str(c, "path"))
		}
	}
}

func deltaText(v any) string {
	switch d := v.(type) {
	case string:
		return d
	case map[string]any:
		return str(d, "text")
	}
	return ""
}

// ---- text helpers ----

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func oneline(s string, n int) string {
	return clip(strings.Join(strings.Fields(s), " "), n)
}

func indent(s string) string {
	return strings.ReplaceAll(s, "\n", "\n         ")
}

func anySlice(v any) []any {
	items, _ := v.([]any)
	return items
}

func stringValues(v any) []string {
	values := []string{}
	for _, item := range anySlice(v) {
		if value, ok := item.(string); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func csvValues(raw string) []string {
	values := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
