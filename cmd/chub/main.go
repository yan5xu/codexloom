// chub — codex-hub CLI. Sessions live in the hub, not in this process:
// Ctrl-C on `chub watch` detaches the observer, the task keeps running.
//
// Set CHUB_URL to point at a non-default hub (default http://127.0.0.1:4870).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var base = func() string {
	if u := os.Getenv("CHUB_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:4870"
}()

// ---- ANSI ----

var useColor = os.Getenv("NO_COLOR") == ""

func color(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func dim(s string) string     { return color("2", s) }
func bold(s string) string    { return color("1", s) }
func green(s string) string   { return color("32", s) }
func red(s string) string     { return color("31", s) }
func yellow(s string) string  { return color("33", s) }
func cyan(s string) string    { return color("36", s) }
func magenta(s string) string { return color("35", s) }

// ---- arg parsing ----

type args struct {
	positional []string
	flags      map[string]string
}

func parseArgs(argv []string) args {
	a := args{flags: map[string]string{}}
	for i := 0; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "--") {
			name := strings.TrimPrefix(argv[i], "--")
			if i+1 < len(argv) {
				a.flags[name] = argv[i+1]
				i++
			} else {
				a.flags[name] = "true"
			}
		} else {
			a.positional = append(a.positional, argv[i])
		}
	}
	return a
}

// ---- HTTP ----

func api(method, path string, body any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, base+path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach codex-hub at %s (%v)\nstart it with: codex-hub\n", base, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	if resp.StatusCode >= 400 {
		msg := resp.Status
		if e, ok := parsed["error"].(string); ok {
			msg = e
		}
		return nil, fmt.Errorf("(%d) %s", resp.StatusCode, msg)
	}
	return parsed, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, red("error: ")+err.Error())
	os.Exit(1)
}

func usage(u string) {
	fmt.Fprintln(os.Stderr, "usage: chub "+u)
	os.Exit(1)
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ---- main ----

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}
	cmd := os.Args[1]
	a := parseArgs(os.Args[2:])

	switch cmd {
	case "create":
		cmdCreate(a)
	case "list", "ls":
		cmdList()
	case "get":
		cmdGet(a)
	case "rename":
		cmdRename(a)
	case "send":
		cmdSend(a)
	case "watch":
		cmdWatch(a)
	case "interrupt":
		cmdInterrupt(a)
	case "history":
		cmdHistory(a)
	case "kill":
		cmdKill(a)
	case "approve", "reject":
		cmdApproval(cmd, a)
	case "help", "-h", "--help":
		printHelp()
	default:
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Printf(`chub — codex-hub CLI (hub: %s)

  chub create <name> --cwd <path> [--approval never|on-request] [--sandbox MODE] [--model M] [--effort minimal|low|medium|high]
  chub list
  chub get <name|id>
  chub rename <name|id> <new-name>
  chub send <name|id> "<task>" [--timeout SEC]
  chub watch <name|id> [--tail N]
  chub interrupt <name|id>
  chub history <name|id> [--count N]
  chub approve <name|id> <approvalId>   /  chub reject <name|id> <approvalId>
  chub kill <name|id>
`, base)
}

func cmdCreate(a args) {
	if len(a.positional) < 1 || a.flags["cwd"] == "" {
		usage("create <name> --cwd <path>")
	}
	cwd, err := filepath.Abs(a.flags["cwd"])
	if err != nil {
		fail(err)
	}
	resp, err := api("POST", "/api/sessions", map[string]any{
		"name":           a.positional[0],
		"cwd":            cwd,
		"approvalPolicy": a.flags["approval"],
		"sandbox":        a.flags["sandbox"],
		"model":          a.flags["model"],
		"effort":         a.flags["effort"],
	})
	if err != nil {
		fail(err)
	}
	s, _ := resp["session"].(map[string]any)
	fmt.Printf("%s %s (%s)\n  cwd:    %s\n  thread: %s\n",
		green("created"), bold(str(s, "name")), str(s, "id"), str(s, "cwd"), str(s, "threadId"))
}

func cmdList() {
	resp, err := api("GET", "/api/sessions", nil)
	if err != nil {
		fail(err)
	}
	sessions, _ := resp["sessions"].([]any)
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return
	}
	for _, v := range sessions {
		s, _ := v.(map[string]any)
		status := str(s, "status")
		switch status {
		case "running":
			status = yellow(status)
		case "idle":
			status = green(status)
		default:
			status = red(status)
		}
		line := fmt.Sprintf("%s %s  %s  %s", bold(pad(str(s, "name"), 16)), str(s, "id"), status, dim(str(s, "cwd")))
		if task := str(s, "currentTask"); task != "" {
			line += "  " + dim("task: ") + clip(task, 60)
		}
		if e := str(s, "lastError"); e != "" {
			line += "  " + red(clip(e, 60))
		}
		fmt.Println(line)
	}
}

func cmdGet(a args) {
	if len(a.positional) < 1 {
		usage("get <name|id>")
	}
	resp, err := api("GET", "/api/sessions/"+url.PathEscape(a.positional[0]), nil)
	if err != nil {
		fail(err)
	}
	out, _ := json.MarshalIndent(resp["session"], "", "  ")
	fmt.Println(string(out))
}

func cmdRename(a args) {
	if len(a.positional) < 2 {
		usage("rename <name|id> <new-name>")
	}
	resp, err := api("PATCH", "/api/sessions/"+url.PathEscape(a.positional[0])+"/config", map[string]any{
		"name": a.positional[1],
	})
	if err != nil {
		fail(err)
	}
	s, _ := resp["session"].(map[string]any)
	fmt.Printf("%s %s -> %s (%s)\n", green("renamed"), a.positional[0], bold(str(s, "name")), str(s, "id"))
}

func cmdSend(a args) {
	if len(a.positional) < 2 {
		usage(`send <name|id> "<task>"`)
	}
	body := map[string]any{"text": a.positional[1]}
	if t := a.flags["timeout"]; t != "" {
		var sec int
		fmt.Sscanf(t, "%d", &sec)
		body["timeoutSec"] = sec
	}
	resp, err := api("POST", "/api/sessions/"+url.PathEscape(a.positional[0])+"/messages", body)
	if err != nil {
		fail(err)
	}
	turnID := str(resp, "turnId")
	if turnID == "" {
		turnID = "(pending)"
	}
	fmt.Printf("%s turn %s — follow with: chub watch %s\n", green("dispatched"), turnID, a.positional[0])
}

func cmdInterrupt(a args) {
	if len(a.positional) < 1 {
		usage("interrupt <name|id>")
	}
	resp, err := api("POST", "/api/sessions/"+url.PathEscape(a.positional[0])+"/interrupt", map[string]any{})
	if err != nil {
		fail(err)
	}
	if interrupted, _ := resp["interrupted"].(bool); interrupted {
		fmt.Println(yellow("interrupt requested"))
	} else {
		fmt.Printf("nothing to interrupt (%s)\n", str(resp, "message"))
	}
}

func cmdKill(a args) {
	if len(a.positional) < 1 {
		usage("kill <name|id>")
	}
	resp, err := api("DELETE", "/api/sessions/"+url.PathEscape(a.positional[0]), nil)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s %s (%s)\n", red("killed"), str(resp, "name"), str(resp, "id"))
}

func cmdApproval(cmd string, a args) {
	if len(a.positional) < 2 {
		usage(cmd + " <name|id> <approvalId>")
	}
	decision := "accept"
	if cmd == "reject" {
		decision = "reject"
	}
	resp, err := api("POST",
		"/api/sessions/"+url.PathEscape(a.positional[0])+"/approvals/"+url.PathEscape(a.positional[1]),
		map[string]any{"decision": decision})
	if err != nil {
		fail(err)
	}
	fmt.Printf("approval %s: %s\n", str(resp, "approvalId"), green(str(resp, "decision")))
}

func cmdHistory(a args) {
	if len(a.positional) < 1 {
		usage("history <name|id>")
	}
	count := a.flags["count"]
	if count == "" {
		count = "10"
	}
	resp, err := api("GET", "/api/sessions/"+url.PathEscape(a.positional[0])+"/history?count="+count, nil)
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
	resp, err := api("GET", "/api/sessions/"+url.PathEscape(key), nil)
	if err != nil {
		fail(err)
	}
	s, _ := resp["session"].(map[string]any)
	fmt.Println(dim(fmt.Sprintf("watching %s (%s) — status: %s — Ctrl-C detaches (task keeps running)\n",
		str(s, "name"), str(s, "id"), str(s, "status"))))

	req, _ := http.NewRequest("GET", base+"/api/sessions/"+url.PathEscape(key)+"/events?tail="+tail, nil)
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
	case "hub/live":
		fmt.Println(dim(fmt.Sprintf("─── live (replayed up to seq %d) ───", seq)))
	case "hub/session-created":
		fmt.Printf("%s %s %v @ %v\n", t, dim("session created:"), d["name"], d["cwd"])
	case "hub/user-message":
		fmt.Printf("%s %s %v\n", t, cyan("user>"), d["text"])
	case "hub/turn-started":
		fmt.Printf("%s %s\n", t, dim(fmt.Sprintf("turn started %v", d["turnId"])))
	case "hub/turn-completed":
		secs := 0.0
		if v, ok := d["durationMs"].(float64); ok {
			secs = v / 1000
		}
		fmt.Printf("%s %s %s\n", t, green("✔ turn completed"), dim(fmt.Sprintf("(%.0fs)", secs)))
	case "hub/turn-interrupted":
		reason, _ := d["reason"].(string)
		if reason == "" {
			reason, _ = d["error"].(string)
		}
		fmt.Printf("%s %s %s\n", t, yellow("■ turn interrupted"), dim(reason))
	case "hub/turn-failed":
		fmt.Printf("%s %s\n", t, red(fmt.Sprintf("✖ turn failed: %v", d["error"])))
	case "hub/approval-requested":
		params, _ := json.Marshal(d["params"])
		fmt.Printf("%s %s %v %s\n", t, red("⚠ approval requested"), d["method"], dim(clip(string(params), 120)))
		fmt.Printf("%s %s\n", t, dim(fmt.Sprintf("  resolve: chub approve <session> %v — or via web console", d["approvalId"])))
	case "hub/approval-resolved":
		fmt.Printf("%s %s %s\n", t, green(fmt.Sprintf("approval %v", d["decision"])), dim(fmt.Sprintf("%v", d["approvalId"])))
	case "hub/error":
		fmt.Printf("%s %s %v\n", t, red("hub error:"), d["message"])
	case "item/started", "item/completed":
		item, _ := d["item"].(map[string]any)
		renderItem(st, t, typ, item)
	default:
		if os.Getenv("CHUB_DEBUG") != "" {
			raw, _ := json.Marshal(d)
			fmt.Printf("%s %s %s\n", t, dim(typ), dim(clip(string(raw), 160)))
		}
	}
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
