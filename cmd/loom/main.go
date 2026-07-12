// loom is the CodexLoom CLI. Agents and their Threads live in the service, not
// in this process: Ctrl-C on `loom thread watch` only detaches the observer.
//
// CODEX_LOOM_URL selects a non-default service. CHUB_URL remains a compatibility
// alias for existing Agent scripts.
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
	"time"
)

var commandName = "loom"

var base = func() string {
	if u := os.Getenv("CODEX_LOOM_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
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

var legacyAgentAPI bool

func api(method, path string, body any) (map[string]any, error) {
	parsed, status, validJSON, err := apiRequest(method, path, body)
	legacyPath := legacyAgentPath(path)
	if legacyPath != "" && (status == http.StatusNotFound || status < 400 && !validJSON) {
		legacyAgentAPI = true
		parsed, status, validJSON, err = apiRequest(method, legacyPath, body)
		if parsed["agent"] == nil && parsed["session"] != nil {
			parsed["agent"] = parsed["session"]
		}
		if parsed["agents"] == nil && parsed["sessions"] != nil {
			parsed["agents"] = parsed["sessions"]
		}
	}
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		msg := http.StatusText(status)
		if e, ok := parsed["error"].(string); ok {
			msg = e
		}
		return nil, fmt.Errorf("(%d) %s", status, msg)
	}
	if !validJSON {
		return nil, fmt.Errorf("CodexLoom returned a non-JSON response for %s", path)
	}
	return parsed, nil
}

func apiRequest(method, path string, body any) (map[string]any, int, bool, error) {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, base+path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach CodexLoom at %s (%v)\nstart it with: codex-loom\n", base, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	parsed := map[string]any{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return parsed, resp.StatusCode, false, nil
	}
	return parsed, resp.StatusCode, true, nil
}

func legacyAgentPath(path string) string {
	if !strings.HasPrefix(path, "/api/agents") {
		return ""
	}
	rest := strings.TrimPrefix(path, "/api/agents")
	switch {
	case strings.Contains(rest, "/turns/current/interrupt"):
		rest = strings.Replace(rest, "/turns/current/interrupt", "/interrupt", 1)
	case strings.Contains(rest, "/thread/approvals/"):
		rest = strings.Replace(rest, "/thread/approvals/", "/approvals/", 1)
	case strings.Contains(rest, "/thread/history"):
		rest = strings.Replace(rest, "/thread/history", "/history", 1)
	case strings.Contains(rest, "/thread/events"):
		rest = strings.Replace(rest, "/thread/events", "/events", 1)
	case strings.HasSuffix(strings.Split(rest, "?")[0], "/turns"):
		rest = strings.Replace(rest, "/turns", "/messages", 1)
	}
	return "/api/sessions" + rest
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, red("error: ")+err.Error())
	os.Exit(1)
}

func usage(u string) {
	fmt.Fprintln(os.Stderr, "usage: "+commandName+" "+u)
	os.Exit(1)
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func num(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// ---- main ----

func main() {
	if filepath.Base(os.Args[0]) == "chub" {
		commandName = "chub"
	}
	if len(os.Args) < 2 {
		printHelp()
		return
	}
	cmd := os.Args[1]
	argv := os.Args[2:]
	if cmd == "agent" || cmd == "thread" {
		if len(argv) == 0 {
			usage(cmd + " <command> ...")
		}
		subcommand := argv[0]
		argv = argv[1:]
		if cmd == "agent" {
			switch subcommand {
			case "create", "list", "ls", "get", "rename":
				cmd = subcommand
			case "archive", "delete":
				cmd = "archive"
			case "kill":
				cmd = "kill"
			default:
				usage("agent create|list|get|rename|archive ...")
			}
		} else {
			switch subcommand {
			case "send", "watch", "history", "interrupt":
				cmd = subcommand
			default:
				usage("thread send|watch|history|interrupt ...")
			}
		}
	}
	a := parseArgs(argv)

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
	case "msg":
		cmdMsg(a)
	case "inbox":
		cmdInbox(a)
	case "outbox":
		cmdOutbox(a)
	case "integration":
		cmdIntegration(a)
	case "conversation":
		cmdConversation(a)
	case "schedule":
		cmdSchedule(a)
	case "team":
		cmdTeam(a)
	case "profile":
		cmdProfile(a)
	case "remote":
		cmdRemote(a)
	case "watch":
		cmdWatch(a)
	case "interrupt":
		cmdInterrupt(a)
	case "history":
		cmdHistory(a)
	case "archive":
		cmdArchive(a)
	case "kill":
		fmt.Fprintln(os.Stderr, "error: 'kill' is disabled because it is ambiguous")
		fmt.Fprintf(os.Stderr, "stop current work: %s thread interrupt <agent>\n", commandName)
		fmt.Fprintf(os.Stderr, "archive an agent:  %s agent archive <agent>\n", commandName)
		os.Exit(2)
	case "backup":
		cmdBackup(a)
	case "backups":
		cmdBackups()
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
	help := fmt.Sprintf(`chub — CodexLoom CLI (service: %s)

  chub agent create|list|get|rename|archive ...
  chub thread send|watch|history|interrupt ...

Compatibility shortcuts:
  chub create <name> --cwd <path> [--approval never|on-request] [--sandbox MODE] [--model gpt-5.6|gpt-5.6-sol|gpt-5.6-terra|gpt-5.6-luna|M] [--effort minimal|low|medium|high|xhigh]
  chub list
  chub get <name|id>
  chub rename <name|id> <new-name>
  chub send <name|id> "<task>" [--timeout SEC]
  chub msg <to> [body] --from <agent> --subject <text> [--response required|none]
  chub msg --reply-to <message-id> --from <agent> [--subject <text>] [body]
  chub msg --no-reply <message-id> --from <agent>
  chub msg status <message-id>
  chub msg wait <message-id> [--timeout SEC]
  chub msg cancel <message-id>
  chub inbox [list] [agent] [--state queued|handling|deferred|handled|failed] [--origin PROVIDER]
  chub inbox get <inbox-item-id>
  chub inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT]
  chub inbox no-reply <inbox-item-id> --agent <agent> [--reason TEXT]
  chub inbox defer <inbox-item-id> --agent <agent> --until RFC3339 [--reason TEXT]
  chub inbox retry <inbox-item-id>
  chub outbox [list] [agent] [--state pending|sending|sent|failed]
  chub outbox send <agent> <address-id> <conversation-id> [body|--body TEXT] [--thread ID] [--message-id ID] [--expectation none|optional|required] [--idempotency-key KEY]
  chub outbox retry <outbox-item-id>
  chub integration list
  chub integration connect <provider> [--account REF] [--credential-ref env:NAME]
  chub integration bind <agent> <connection-id> --identity EXTERNAL_ID [--trigger mention] [--reply-policy final_answer] [--trust-domain NAME] [--allow-actors CSV] [--allow-conversations CSV] [--block-actors CSV] [--block-conversations CSV]
  chub integration update-address <address-id> [--identity ID] [--display-name NAME] [--trigger mention] [--reply-policy final_answer] [--trust-domain NAME] [allow/block flags]
  chub integration enable|disable <connection-id|address-id>
  chub integration status [connection-id]
  chub conversation list [agent] [--address ADDRESS_ID]
  chub conversation get <membership-id>
  chub conversation set <address-id> <conversation-id> [--file membership.json] [--name NAME] [--purpose TEXT] [--role TEXT] [--guidance TEXT] [--trigger mention] [--reply-policy final_answer]
  chub conversation enable|disable <membership-id>
  chub schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [--tz TZ] [--response required|none] [body]
  chub schedule list|get|run|enable|disable|delete ...
  chub profile get <agent>
  chub profile set <agent> [--identity TEXT] [--domain TEXT] [--scope TEXT] [--file profile.json]
  chub profile clear <agent>
  chub remote [status]
  chub remote enable
  chub remote disable
  chub remote pair
  chub remote devices
  chub remote revoke <client-id>
  chub team [agent]
  chub team links [agent]
  chub team link add <from> <to> --description <text>
  chub team link update <id> --description <text>
  chub team link delete <id>
  chub watch <name|id> [--tail N]
  chub interrupt <name|id>
  chub history <name|id> [--count N]
  chub backup [--reason text]
  chub backups
  chub approve <name|id> <approvalId>   /  chub reject <name|id> <approvalId>
  chub thread interrupt <name|id>              stop the current Turn
  chub agent archive <name|id>                 archive the long-lived Agent
`, base)
	fmt.Print(strings.ReplaceAll(help, "chub", commandName))
}

func cmdRemote(a args) {
	subcommand := "status"
	if len(a.positional) > 0 {
		subcommand = a.positional[0]
	}
	switch subcommand {
	case "status", "list":
		resp, err := api("GET", "/api/remote", nil)
		if err != nil {
			fail(err)
		}
		printRemoteStatus(resp)
	case "enable":
		resp, err := api("POST", "/api/remote/enable", map[string]any{})
		if err != nil {
			fail(err)
		}
		printRemoteStatus(resp)
	case "disable":
		resp, err := api("POST", "/api/remote/disable", map[string]any{})
		if err != nil {
			fail(err)
		}
		printRemoteStatus(resp)
	case "pair":
		resp, err := api("POST", "/api/remote/pairing", map[string]any{})
		if err != nil {
			fail(err)
		}
		pairing, _ := resp["pairing"].(map[string]any)
		fmt.Printf("%s %s\n", green("pairing ready"), str(pairing, "manualPairingCode"))
		fmt.Printf("  url:     %s\n", str(pairing, "pairingCode"))
		fmt.Printf("  expires: %s\n", time.Unix(int64(num(pairing, "expiresAt")), 0).Format(time.RFC3339))
	case "devices":
		resp, err := api("GET", "/api/remote/devices", nil)
		if err != nil {
			fail(err)
		}
		devices := anySlice(resp["devices"])
		if len(devices) == 0 {
			fmt.Println("no paired devices")
			return
		}
		for _, value := range devices {
			device, _ := value.(map[string]any)
			name := str(device, "displayName")
			if name == "" {
				name = str(device, "deviceModel")
			}
			if name == "" {
				name = str(device, "clientId")
			}
			fmt.Printf("%s %s  %s %s\n", bold(name), str(device, "clientId"), str(device, "platform"), str(device, "appVersion"))
		}
	case "revoke":
		if len(a.positional) < 2 {
			usage("remote revoke <client-id>")
		}
		if _, err := api("DELETE", "/api/remote/devices/"+url.PathEscape(a.positional[1]), nil); err != nil {
			fail(err)
		}
		fmt.Printf("%s %s\n", green("revoked"), a.positional[1])
	default:
		usage("remote [status|enable|disable|pair|devices|revoke]")
	}
}

func printRemoteStatus(resp map[string]any) {
	remote, _ := resp["remote"].(map[string]any)
	status, _ := remote["status"].(map[string]any)
	state := str(status, "state")
	colored := state
	switch state {
	case "connected":
		colored = green(state)
	case "starting", "connecting":
		colored = yellow(state)
	case "error":
		colored = red(state)
	}
	name := str(status, "serverName")
	if name == "" {
		name = str(status, "systemHostname")
	}
	fmt.Printf("%s %s\n", bold(name), colored)
	if environmentID := str(status, "environmentId"); environmentID != "" {
		fmt.Printf("  environment: %s\n", environmentID)
	}
	if lastError := str(status, "lastError"); lastError != "" {
		fmt.Printf("  %s %s\n", red("error:"), lastError)
	}
}

func cmdCreate(a args) {
	if len(a.positional) < 1 || a.flags["cwd"] == "" {
		usage("create <name> --cwd <path>")
	}
	cwd, err := filepath.Abs(a.flags["cwd"])
	if err != nil {
		fail(err)
	}
	resp, err := api("POST", "/api/agents", map[string]any{
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
	s, _ := resp["agent"].(map[string]any)
	fmt.Printf("%s %s (%s)\n  cwd:    %s\n  thread: %s\n",
		green("created"), bold(str(s, "name")), str(s, "id"), str(s, "cwd"), str(s, "threadId"))
}

func cmdList() {
	resp, err := api("GET", "/api/agents", nil)
	if err != nil {
		fail(err)
	}
	agents, _ := resp["agents"].([]any)
	teamResp, _ := api("GET", "/api/team", nil)
	domains := map[string]string{}
	if team, ok := teamResp["team"].(map[string]any); ok {
		for _, value := range anySlice(team["agents"]) {
			agent, _ := value.(map[string]any)
			profile, _ := agent["profile"].(map[string]any)
			domains[str(agent, "id")] = firstLine(str(profile, "domain"))
		}
	}
	if len(agents) == 0 {
		fmt.Println("no agents")
		return
	}
	for _, v := range agents {
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
		if domain := domains[str(s, "id")]; domain != "" {
			line += "  " + cyan(clip(domain, 70))
		}
		fmt.Println(line)
	}
}

func cmdGet(a args) {
	if len(a.positional) < 1 {
		usage("get <name|id>")
	}
	resp, err := api("GET", "/api/agents/"+url.PathEscape(a.positional[0]), nil)
	if err != nil {
		fail(err)
	}
	profileResp, err := api("GET", "/api/agents/"+url.PathEscape(a.positional[0])+"/profile", nil)
	if err != nil {
		fail(err)
	}
	relResp, err := api("GET", "/api/team/relationships?agent="+url.QueryEscape(a.positional[0]), nil)
	if err != nil {
		fail(err)
	}
	out, _ := json.MarshalIndent(map[string]any{
		"agent":         resp["agent"],
		"profile":       profileResp["profile"],
		"relationships": relResp["relationships"],
	}, "", "  ")
	fmt.Println(string(out))
}

func cmdRename(a args) {
	if len(a.positional) < 2 {
		usage("rename <name|id> <new-name>")
	}
	resp, err := api("PATCH", "/api/agents/"+url.PathEscape(a.positional[0])+"/config", map[string]any{
		"name": a.positional[1],
	})
	if err != nil {
		fail(err)
	}
	s, _ := resp["agent"].(map[string]any)
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
	resp, err := api("POST", "/api/agents/"+url.PathEscape(a.positional[0])+"/turns", body)
	if err != nil {
		fail(err)
	}
	turnID := str(resp, "turnId")
	if turnID == "" {
		turnID = "(pending)"
	}
	fmt.Printf("%s turn %s — follow with: %s thread watch %s\n", green("dispatched"), turnID, commandName, a.positional[0])
}

func cmdInbox(a args) {
	action := "list"
	if len(a.positional) > 0 {
		switch a.positional[0] {
		case "list", "get", "reply", "no-reply", "defer", "retry":
			action = a.positional[0]
		default:
			action = "list"
		}
	}
	switch action {
	case "list":
		agent := ""
		if len(a.positional) > 0 {
			if a.positional[0] == "list" {
				if len(a.positional) > 1 {
					agent = a.positional[1]
				}
			} else {
				agent = a.positional[0]
			}
		}
		path := "/api/inbox?agent=" + url.QueryEscape(agent) + "&state=" + url.QueryEscape(a.flags["state"]) + "&origin=" + url.QueryEscape(a.flags["origin"])
		resp, err := api("GET", path, nil)
		if err != nil {
			fail(err)
		}
		entries := anySlice(resp["entries"])
		if len(entries) == 0 {
			fmt.Println("inbox empty")
			return
		}
		for _, value := range entries {
			entry, _ := value.(map[string]any)
			item, _ := entry["item"].(map[string]any)
			message, _ := entry["message"].(map[string]any)
			sender, _ := message["sender"].(map[string]any)
			content, _ := message["content"].(map[string]any)
			state := str(item, "state")
			switch state {
			case "queued", "deferred":
				state = yellow(state)
			case "handled":
				state = green(state)
			case "failed":
				state = red(state)
			}
			fmt.Printf("%s %s  %s  %s → %s  %s\n",
				bold(str(item, "id")), state, cyan(str(message, "origin")),
				str(sender, "displayName"), str(entry, "agentName"), clip(oneline(str(content, "text"), 90), 90))
			if outcome := str(item, "outcome"); outcome != "" {
				fmt.Printf("  %s %s\n", dim("outcome:"), outcome)
			}
			if lastErr := str(item, "lastError"); lastErr != "" {
				fmt.Printf("  %s %s\n", dim("detail:"), lastErr)
			}
		}
	case "get":
		if len(a.positional) < 2 {
			usage("inbox get <inbox-item-id>")
		}
		resp, err := api("GET", "/api/inbox/"+url.PathEscape(a.positional[1]), nil)
		if err != nil {
			fail(err)
		}
		out, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(out))
	case "reply":
		if len(a.positional) < 2 || strings.TrimSpace(a.flags["agent"]) == "" {
			usage("inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT]")
		}
		body := strings.TrimSpace(a.flags["body"])
		if body == "" && len(a.positional) > 2 {
			body = strings.TrimSpace(a.positional[2])
		}
		if body == "" {
			usage("inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT]")
		}
		resp, err := api("POST", "/api/inbox/"+url.PathEscape(a.positional[1])+"/reply", map[string]any{
			"agent": a.flags["agent"], "content": map[string]any{"text": body},
		})
		if err != nil {
			fail(err)
		}
		outbox, _ := resp["outboxItem"].(map[string]any)
		fmt.Printf("%s %s  %s\n", green("reply queued"), str(outbox, "id"), dim(str(outbox, "state")))
	case "no-reply":
		if len(a.positional) < 2 || strings.TrimSpace(a.flags["agent"]) == "" {
			usage("inbox no-reply <inbox-item-id> --agent <agent> [--reason TEXT]")
		}
		resp, err := api("POST", "/api/inbox/"+url.PathEscape(a.positional[1])+"/no-reply", map[string]any{
			"agent": a.flags["agent"], "reason": a.flags["reason"],
		})
		if err != nil {
			fail(err)
		}
		item, _ := resp["item"].(map[string]any)
		fmt.Printf("%s %s  %s\n", green("handled without reply"), str(item, "id"), dim(str(item, "note")))
	case "defer":
		if len(a.positional) < 2 || strings.TrimSpace(a.flags["agent"]) == "" || strings.TrimSpace(a.flags["until"]) == "" {
			usage("inbox defer <inbox-item-id> --agent <agent> --until RFC3339 [--reason TEXT]")
		}
		resp, err := api("POST", "/api/inbox/"+url.PathEscape(a.positional[1])+"/defer", map[string]any{
			"agent": a.flags["agent"], "until": a.flags["until"], "reason": a.flags["reason"],
		})
		if err != nil {
			fail(err)
		}
		item, _ := resp["item"].(map[string]any)
		fmt.Printf("%s %s until %s\n", yellow("deferred"), str(item, "id"), str(item, "availableAt"))
	case "retry":
		if len(a.positional) < 2 {
			usage("inbox retry <inbox-item-id>")
		}
		resp, err := api("POST", "/api/inbox/"+url.PathEscape(a.positional[1])+"/retry", map[string]any{})
		if err != nil {
			fail(err)
		}
		item, _ := resp["item"].(map[string]any)
		fmt.Printf("%s %s\n", green("queued"), str(item, "id"))
	}
}

func cmdOutbox(a args) {
	action := "list"
	if len(a.positional) > 0 {
		switch a.positional[0] {
		case "list", "send", "retry":
			action = a.positional[0]
		}
	}
	switch action {
	case "list":
		agent := ""
		if len(a.positional) > 0 {
			if a.positional[0] == "list" {
				if len(a.positional) > 1 {
					agent = a.positional[1]
				}
			} else {
				agent = a.positional[0]
			}
		}
		resp, err := api("GET", "/api/outbox?agent="+url.QueryEscape(agent)+"&state="+url.QueryEscape(a.flags["state"]), nil)
		if err != nil {
			fail(err)
		}
		items := anySlice(resp["items"])
		if len(items) == 0 {
			fmt.Println("outbox empty")
			return
		}
		for _, value := range items {
			item, _ := value.(map[string]any)
			conversation, _ := item["conversation"].(map[string]any)
			content, _ := item["content"].(map[string]any)
			state := str(item, "state")
			switch state {
			case "sent":
				state = green(state)
			case "failed":
				state = red(state)
			default:
				state = yellow(state)
			}
			text := oneline(str(content, "text"), 90)
			if text == "" && len(anySlice(content["attachments"])) > 0 {
				text = "attachment"
			}
			fmt.Printf("%s %s  %s/%s  %s\n", bold(str(item, "id")), state, cyan(str(conversation, "provider")), str(conversation, "conversationId"), text)
			if lastErr := str(item, "lastError"); lastErr != "" {
				fmt.Printf("  %s %s\n", red("error:"), lastErr)
			}
		}
	case "send":
		if len(a.positional) < 4 {
			usage("outbox send <agent> <address-id> <conversation-id> [body|--body TEXT] [--thread ID] [--message-id ID] [--expectation none|optional|required] [--idempotency-key KEY]")
		}
		body, err := readMsgBody(a, a.positional[4:])
		if err != nil {
			fail(err)
		}
		if strings.TrimSpace(body) == "" {
			usage("outbox send <agent> <address-id> <conversation-id> [body|--body TEXT]")
		}
		expectation := strings.TrimSpace(a.flags["expectation"])
		if expectation == "" {
			expectation = "none"
		}
		resp, err := api("POST", "/api/outbox", map[string]any{
			"agent": a.positional[1], "addressId": a.positional[2],
			"conversation": map[string]any{
				"conversationId": a.positional[3], "threadId": a.flags["thread"],
				"messageId": a.flags["message-id"], "conversationType": a.flags["conversation-type"],
			},
			"content": map[string]any{"text": body}, "responseExpectation": expectation,
			"idempotencyKey": a.flags["idempotency-key"],
		})
		if err != nil {
			fail(err)
		}
		item, _ := resp["outboxItem"].(map[string]any)
		fmt.Printf("%s %s  %s\n", green("queued"), str(item, "id"), dim(str(item, "state")))
	case "retry":
		if len(a.positional) < 2 {
			usage("outbox retry <outbox-item-id>")
		}
		resp, err := api("POST", "/api/outbox/"+url.PathEscape(a.positional[1])+"/retry", map[string]any{})
		if err != nil {
			fail(err)
		}
		item, _ := resp["outboxItem"].(map[string]any)
		fmt.Printf("%s %s  %s\n", green("queued"), str(item, "id"), dim(str(item, "state")))
	}
}

func cmdIntegration(a args) {
	if len(a.positional) == 0 {
		usage("integration list|connect|bind|update-address|enable|disable|status ...")
	}
	switch a.positional[0] {
	case "list":
		connectionsResp, err := api("GET", "/api/integrations/connections", nil)
		if err != nil {
			fail(err)
		}
		addressesResp, err := api("GET", "/api/integrations/addresses", nil)
		if err != nil {
			fail(err)
		}
		addressesByConnection := map[string][]map[string]any{}
		for _, value := range anySlice(addressesResp["addresses"]) {
			address, _ := value.(map[string]any)
			connectionID := str(address, "connectionId")
			addressesByConnection[connectionID] = append(addressesByConnection[connectionID], address)
		}
		connections := anySlice(connectionsResp["connections"])
		if len(connections) == 0 {
			fmt.Println("no integrations")
			return
		}
		for _, value := range connections {
			connection, _ := value.(map[string]any)
			state := str(connection, "status")
			switch state {
			case "connected":
				state = green(state)
			case "degraded":
				state = yellow(state)
			default:
				state = red(state)
			}
			fmt.Printf("%s %s  %s  %s\n", bold(str(connection, "id")), cyan(str(connection, "provider")), state, dim(str(connection, "accountRef")))
			for _, address := range addressesByConnection[str(connection, "id")] {
				fmt.Printf("  %s %s → %s  trigger=%s reply=%s trust=%s\n", dim(str(address, "id")), str(address, "externalIdentity"), str(address, "agentId"), str(address, "triggerPolicy"), str(address, "replyPolicy"), str(address, "trustDomain"))
				for _, key := range []string{"allowActors", "allowConversations", "blockActors", "blockConversations"} {
					if values := stringValues(address[key]); len(values) > 0 {
						fmt.Printf("    %s %s\n", dim(key+":"), strings.Join(values, ", "))
					}
				}
			}
			if lastErr := str(connection, "lastError"); lastErr != "" {
				fmt.Printf("  %s %s\n", red("error:"), lastErr)
			}
		}
	case "connect":
		if len(a.positional) < 2 {
			usage("integration connect <provider> [--account REF] [--credential-ref env:NAME]")
		}
		capabilities := []string{}
		for _, value := range strings.Split(a.flags["capabilities"], ",") {
			if strings.TrimSpace(value) != "" {
				capabilities = append(capabilities, strings.TrimSpace(value))
			}
		}
		resp, err := api("POST", "/api/integrations/connections", map[string]any{
			"provider": a.positional[1], "accountRef": a.flags["account"],
			"credentialRef": a.flags["credential-ref"], "capabilities": capabilities,
		})
		if err != nil {
			fail(err)
		}
		connection, _ := resp["connection"].(map[string]any)
		fmt.Printf("%s %s (%s)\n", green("connected config"), bold(str(connection, "provider")), str(connection, "id"))
	case "bind":
		if len(a.positional) < 3 || strings.TrimSpace(a.flags["identity"]) == "" {
			usage("integration bind <agent> <connection-id> --identity EXTERNAL_ID [--trigger mention] [--reply-policy final_answer] [--trust-domain NAME] [--allow-actors CSV] [--allow-conversations CSV] [--block-actors CSV] [--block-conversations CSV]")
		}
		resp, err := api("POST", "/api/agents/"+url.PathEscape(a.positional[1])+"/addresses", map[string]any{
			"connectionId": a.positional[2], "externalIdentity": a.flags["identity"],
			"displayName": a.flags["display-name"], "triggerPolicy": a.flags["trigger"],
			"replyPolicy": a.flags["reply-policy"], "trustDomain": a.flags["trust-domain"],
			"allowActors": csvValues(a.flags["allow-actors"]), "allowConversations": csvValues(a.flags["allow-conversations"]),
			"blockActors": csvValues(a.flags["block-actors"]), "blockConversations": csvValues(a.flags["block-conversations"]),
		})
		if err != nil {
			fail(err)
		}
		address, _ := resp["address"].(map[string]any)
		fmt.Printf("%s %s → %s (%s)\n", green("bound"), str(address, "externalIdentity"), a.positional[1], str(address, "id"))
	case "update-address":
		if len(a.positional) < 2 {
			usage("integration update-address <address-id> [policy flags]")
		}
		body := map[string]any{}
		for flag, field := range map[string]string{
			"identity": "externalIdentity", "display-name": "displayName", "trigger": "triggerPolicy",
			"reply-policy": "replyPolicy", "trust-domain": "trustDomain",
		} {
			if value, exists := a.flags[flag]; exists {
				body[field] = value
			}
		}
		for flag, field := range map[string]string{
			"allow-actors": "allowActors", "allow-conversations": "allowConversations",
			"block-actors": "blockActors", "block-conversations": "blockConversations",
		} {
			if value, exists := a.flags[flag]; exists {
				body[field] = csvValues(value)
			}
		}
		if len(body) == 0 {
			usage("integration update-address <address-id> [policy flags]")
		}
		resp, err := api("PATCH", "/api/integrations/addresses/"+url.PathEscape(a.positional[1]), body)
		if err != nil {
			fail(err)
		}
		address, _ := resp["address"].(map[string]any)
		fmt.Printf("%s %s  trigger=%s reply=%s\n", green("updated"), str(address, "id"), str(address, "triggerPolicy"), str(address, "replyPolicy"))
	case "enable", "disable":
		if len(a.positional) < 2 {
			usage("integration enable|disable <connection-id|address-id>")
		}
		id := a.positional[1]
		path := "/api/integrations/connections/" + url.PathEscape(id)
		if strings.HasPrefix(id, "addr_") {
			path = "/api/integrations/addresses/" + url.PathEscape(id)
		}
		resp, err := api("PATCH", path, map[string]any{"enabled": a.positional[0] == "enable"})
		if err != nil {
			fail(err)
		}
		resource := "connection"
		if _, ok := resp["address"]; ok {
			resource = "address"
		}
		result := "enabled"
		if a.positional[0] == "disable" {
			result = "disabled"
		}
		fmt.Printf("%s %s %s\n", green(result), resource, id)
	case "status":
		resp, err := api("GET", "/api/integrations/connections", nil)
		if err != nil {
			fail(err)
		}
		for _, value := range anySlice(resp["connections"]) {
			connection, _ := value.(map[string]any)
			if len(a.positional) > 1 && str(connection, "id") != a.positional[1] {
				continue
			}
			out, _ := json.MarshalIndent(connection, "", "  ")
			fmt.Println(string(out))
		}
	default:
		usage("integration list|connect|bind|update-address|enable|disable|status ...")
	}
}

func cmdConversation(a args) {
	if len(a.positional) == 0 {
		usage("conversation list|get|set|enable|disable ...")
	}
	switch a.positional[0] {
	case "list":
		values := url.Values{}
		if len(a.positional) > 1 {
			values.Set("agent", a.positional[1])
		}
		if address := strings.TrimSpace(a.flags["address"]); address != "" {
			values.Set("address", address)
		}
		path := "/api/integrations/conversations"
		if encoded := values.Encode(); encoded != "" {
			path += "?" + encoded
		}
		resp, err := api("GET", path, nil)
		if err != nil {
			fail(err)
		}
		memberships := anySlice(resp["memberships"])
		if len(memberships) == 0 {
			fmt.Println("no conversation memberships")
			return
		}
		for _, value := range memberships {
			printConversationMembership(value)
		}
	case "get":
		if len(a.positional) < 2 {
			usage("conversation get <membership-id>")
		}
		resp, err := api("GET", "/api/integrations/conversations/"+url.PathEscape(a.positional[1]), nil)
		if err != nil {
			fail(err)
		}
		printConversationMembership(resp["membership"])
	case "set":
		if len(a.positional) < 3 {
			usage("conversation set <address-id> <conversation-id> [--file membership.json] [fields]")
		}
		body := map[string]any{}
		if path := strings.TrimSpace(a.flags["file"]); path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				fail(err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				fail(fmt.Errorf("parse conversation membership JSON: %w", err))
			}
		}
		for flag, field := range map[string]string{
			"name": "displayName", "purpose": "purpose", "role": "role", "guidance": "guidance",
			"trigger": "triggerPolicy", "reply-policy": "replyPolicy", "trust-domain": "trustDomain",
		} {
			if value, ok := a.flags[flag]; ok {
				body[field] = value
			}
		}
		if len(body) == 0 {
			usage("conversation set <address-id> <conversation-id> [--file membership.json] [--name NAME] [--purpose TEXT] [--role TEXT] [--guidance TEXT]")
		}
		resp, err := api("PUT", "/api/integrations/addresses/"+url.PathEscape(a.positional[1])+"/conversations/"+url.PathEscape(a.positional[2]), body)
		if err != nil {
			fail(err)
		}
		printConversationMembership(resp["membership"])
	case "enable", "disable":
		if len(a.positional) < 2 {
			usage("conversation enable|disable <membership-id>")
		}
		resp, err := api("PATCH", "/api/integrations/conversations/"+url.PathEscape(a.positional[1]), map[string]any{"enabled": a.positional[0] == "enable"})
		if err != nil {
			fail(err)
		}
		printConversationMembership(resp["membership"])
	default:
		usage("conversation list|get|set|enable|disable ...")
	}
}

func printConversationMembership(value any) {
	membership, _ := value.(map[string]any)
	fmt.Print(formatConversationMembership(membership))
}

func formatConversationMembership(membership map[string]any) string {
	var b strings.Builder
	state := "enabled"
	if enabled, ok := membership["enabled"].(bool); ok && !enabled {
		state = "disabled"
	}
	fmt.Fprintf(&b, "%s %s  %s  v%.0f  %s\n", bold(str(membership, "id")), str(membership, "conversationId"), state, num(membership, "version"), dim(str(membership, "addressId")))
	if name := str(membership, "displayName"); name != "" {
		fmt.Fprintf(&b, "  name: %s\n", name)
	}
	for _, field := range []string{"purpose", "role", "guidance"} {
		if value := str(membership, field); value != "" {
			fmt.Fprintf(&b, "  %s:\n    %s\n", field, strings.ReplaceAll(value, "\n", "\n    "))
		}
	}
	fmt.Fprintf(&b, "  policy: trigger=%s reply=%s trust=%s\n", str(membership, "triggerPolicy"), str(membership, "replyPolicy"), str(membership, "trustDomain"))
	return b.String()
}

func cmdMsg(a args) {
	if id := strings.TrimSpace(a.flags["no-reply"]); id != "" {
		from := strings.TrimSpace(a.flags["from"])
		if from == "" || id == "true" {
			usage("msg --no-reply <message-id> --from <agent>")
		}
		resp, err := api("POST", "/api/comms/messages/"+url.PathEscape(id)+"/no-reply", map[string]any{"from": from})
		if err != nil {
			fail(err)
		}
		msg, _ := resp["message"].(map[string]any)
		fmt.Printf("%s %s\n", green("closed without reply"), str(msg, "id"))
		return
	}
	if len(a.positional) > 0 && a.flags["from"] == "" && a.flags["reply-to"] == "" {
		switch a.positional[0] {
		case "status":
			cmdMsgStatus(a)
			return
		case "wait":
			cmdMsgWait(a)
			return
		case "cancel":
			cmdMsgCancel(a)
			return
		}
	}

	from := strings.TrimSpace(a.flags["from"])
	replyTo := strings.TrimSpace(a.flags["reply-to"])
	subject := strings.TrimSpace(a.flags["subject"])
	response := strings.TrimSpace(a.flags["response"])
	if response == "" {
		response = "required"
	}
	if from == "" {
		usage(`msg <to> [body] --from <agent> --subject <text> [--response required|none]`)
	}
	if response != "required" && response != "none" {
		fail(fmt.Errorf("--response must be required or none"))
	}

	to := ""
	bodyArgs := a.positional
	if replyTo == "" {
		if len(a.positional) < 1 {
			usage(`msg <to> [body] --from <agent> --subject <text> [--response required|none]`)
		}
		to = a.positional[0]
		bodyArgs = a.positional[1:]
		if subject == "" {
			usage(`msg <to> [body] --from <agent> --subject <text> [--response required|none]`)
		}
	} else if len(a.positional) > 0 {
		bodyArgs = a.positional
	}

	body, err := readMsgBody(a, bodyArgs)
	if err != nil {
		fail(err)
	}
	if strings.TrimSpace(body) == "" {
		usage(`msg <to> [body] --from <agent> --subject <text> [--body <text>|--body-file <path>]`)
	}

	payload := map[string]any{
		"from":     from,
		"to":       to,
		"subject":  subject,
		"body":     body,
		"response": response,
		"replyTo":  replyTo,
	}
	if t := a.flags["timeout"]; t != "" {
		var sec int
		fmt.Sscanf(t, "%d", &sec)
		payload["timeoutSec"] = sec
	}
	resp, err := api("POST", "/api/comms/messages", payload)
	if err != nil {
		fail(err)
	}
	msg, _ := resp["message"].(map[string]any)
	printMessageDelivery(msg)
	if str(msg, "deliveryStatus") == "queued" {
		id := str(msg, "id")
		fmt.Printf("check: %s msg status %s\n", commandName, id)
		fmt.Printf("watch: %s msg wait %s\n", commandName, id)
	}
	if str(msg, "response") == "required" {
		fmt.Printf("reply with: %s msg --reply-to %s --from %s --body \"...\"\n", commandName, str(msg, "id"), str(msg, "to"))
	}
}

func cmdMsgStatus(a args) {
	if len(a.positional) < 2 {
		usage("msg status <message-id>")
	}
	msg := fetchMessage(a.positional[1])
	printMessageDelivery(msg)
	printMessageDetail(msg)
}

func cmdMsgCancel(a args) {
	if len(a.positional) < 2 {
		usage("msg cancel <message-id>")
	}
	resp, err := api("POST", "/api/comms/messages/"+url.PathEscape(a.positional[1])+"/cancel", map[string]any{})
	if err != nil {
		fail(err)
	}
	msg, _ := resp["message"].(map[string]any)
	printMessageDelivery(msg)
}

func cmdMsgWait(a args) {
	if len(a.positional) < 2 {
		usage("msg wait <message-id> [--timeout SEC]")
	}
	deadline := time.Time{}
	if raw := strings.TrimSpace(a.flags["timeout"]); raw != "" {
		var sec int
		fmt.Sscanf(raw, "%d", &sec)
		if sec > 0 {
			deadline = time.Now().Add(time.Duration(sec) * time.Second)
		}
	}
	for {
		msg := fetchMessage(a.positional[1])
		switch str(msg, "deliveryStatus") {
		case "delivered", "failed", "cancelled":
			printMessageDelivery(msg)
			printMessageDetail(msg)
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			printMessageDelivery(msg)
			fail(fmt.Errorf("timed out waiting for %s", a.positional[1]))
		}
		time.Sleep(time.Second)
	}
}

func fetchMessage(id string) map[string]any {
	resp, err := api("GET", "/api/comms/messages/"+url.PathEscape(id), nil)
	if err != nil {
		fail(err)
	}
	msg, _ := resp["message"].(map[string]any)
	return msg
}

func printMessageDelivery(msg map[string]any) {
	id := str(msg, "id")
	toName := str(msg, "to")
	switch str(msg, "deliveryStatus") {
	case "delivered":
		turnID := str(msg, "deliveredTurnId")
		if turnID == "" {
			turnID = "(unknown)"
		}
		fmt.Printf("%s %s %s %s — turn %s\n", green("delivered"), id, dim("to"), bold(toName), turnID)
	case "queued":
		fmt.Printf("%s %s %s %s — target busy\n", yellow("queued"), id, dim("to"), bold(toName))
	case "delivering":
		fmt.Printf("%s %s %s %s\n", cyan("delivering"), id, dim("to"), bold(toName))
	case "failed":
		errMsg := str(msg, "lastDeliveryError")
		if errMsg == "" {
			errMsg = "delivery failed"
		}
		fmt.Printf("%s %s %s %s — %s\n", red("failed"), id, dim("to"), bold(toName), errMsg)
	case "cancelled":
		fmt.Printf("%s %s %s %s\n", yellow("cancelled"), id, dim("to"), bold(toName))
	default:
		fmt.Printf("%s %s %s %s\n", green("message"), id, dim("to"), bold(toName))
	}
}

func printMessageDetail(msg map[string]any) {
	fmt.Printf("from: %s\n", str(msg, "from"))
	fmt.Printf("subject: %s\n", str(msg, "subject"))
	fmt.Printf("status: %s\n", str(msg, "status"))
	if str(msg, "replyTo") != "" {
		fmt.Printf("reply-to: %s\n", str(msg, "replyTo"))
	}
}

func readMsgBody(a args, bodyArgs []string) (string, error) {
	if body := a.flags["body"]; body != "" {
		return body, nil
	}
	if path := a.flags["body-file"]; path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(bodyArgs) > 0 {
		return strings.Join(bodyArgs, " "), nil
	}
	info, err := os.Stdin.Stat()
	if err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return "", nil
}

func cmdSchedule(a args) {
	if len(a.positional) < 1 {
		usage(`schedule add|list|get|run|enable|disable|delete ...`)
	}
	switch a.positional[0] {
	case "add":
		cmdScheduleAdd(a)
	case "list", "ls":
		cmdScheduleList()
	case "get":
		cmdScheduleGet(a)
	case "run":
		cmdScheduleAction(a, "run", "POST")
	case "enable":
		cmdScheduleAction(a, "enable", "POST")
	case "disable":
		cmdScheduleAction(a, "disable", "POST")
	case "delete", "rm":
		cmdScheduleAction(a, "", "DELETE")
	default:
		usage(`schedule add|list|get|run|enable|disable|delete ...`)
	}
}

func cmdScheduleAdd(a args) {
	if len(a.positional) < 2 {
		usage(`schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [body]`)
	}
	name := a.positional[1]
	if a.flags["to"] == "" || a.flags["subject"] == "" {
		usage(`schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [body]`)
	}
	if (a.flags["at"] == "") == (a.flags["cron"] == "") {
		usage(`schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [body]`)
	}
	response := strings.TrimSpace(a.flags["response"])
	if response == "" {
		response = "required"
	}
	if response != "required" && response != "none" {
		fail(fmt.Errorf("--response must be required or none"))
	}
	body, err := readMsgBody(a, a.positional[2:])
	if err != nil {
		fail(err)
	}
	if strings.TrimSpace(body) == "" {
		usage(`schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [--body <text>|--body-file <path>]`)
	}
	payload := map[string]any{
		"name":     name,
		"to":       a.flags["to"],
		"subject":  a.flags["subject"],
		"body":     body,
		"response": response,
		"at":       a.flags["at"],
		"cron":     a.flags["cron"],
		"timezone": a.flags["tz"],
	}
	resp, err := api("POST", "/api/schedules", payload)
	if err != nil {
		fail(err)
	}
	s, _ := resp["schedule"].(map[string]any)
	fmt.Printf("%s %s (%s)\n", green("schedule created"), bold(str(s, "name")), str(s, "id"))
	printScheduleLine(s)
}

func cmdScheduleList() {
	resp, err := api("GET", "/api/schedules", nil)
	if err != nil {
		fail(err)
	}
	schedules, _ := resp["schedules"].([]any)
	if len(schedules) == 0 {
		fmt.Println("no schedules")
		return
	}
	for _, v := range schedules {
		s, _ := v.(map[string]any)
		printScheduleLine(s)
	}
}

func cmdScheduleGet(a args) {
	if len(a.positional) < 2 {
		usage("schedule get <schedule-id>")
	}
	resp, err := api("GET", "/api/schedules/"+url.PathEscape(a.positional[1]), nil)
	if err != nil {
		fail(err)
	}
	s, _ := resp["schedule"].(map[string]any)
	printScheduleDetail(s)
}

func cmdScheduleAction(a args, action, method string) {
	if len(a.positional) < 2 {
		name := action
		if name == "" {
			name = a.positional[0]
		}
		usage("schedule " + name + " <schedule-id>")
	}
	path := "/api/schedules/" + url.PathEscape(a.positional[1])
	if action != "" {
		path += "/" + action
	}
	resp, err := api(method, path, map[string]any{})
	if err != nil {
		fail(err)
	}
	s, _ := resp["schedule"].(map[string]any)
	if action == "" {
		fmt.Printf("%s %s (%s)\n", red("schedule deleted"), str(s, "name"), str(s, "id"))
		return
	}
	fmt.Printf("%s %s (%s)\n", green("schedule "+action), str(s, "name"), str(s, "id"))
	printScheduleLine(s)
}

func printScheduleLine(s map[string]any) {
	enabled := "disabled"
	if b, _ := s["enabled"].(bool); b {
		enabled = "enabled"
	}
	next := str(s, "nextRunAt")
	if next == "" {
		next = "-"
	}
	trigger := str(s, "at")
	if trigger == "" {
		trigger = str(s, "cron")
	}
	fmt.Printf("%s %s -> %s %s next=%s %s\n",
		bold(pad(str(s, "name"), 18)),
		str(s, "id"),
		str(s, "to"),
		dim("["+enabled+" "+trigger+"]"),
		next,
		dim(str(s, "lastError")),
	)
}

func printScheduleDetail(s map[string]any) {
	printScheduleLine(s)
	fmt.Printf("subject: %s\n", str(s, "subject"))
	fmt.Printf("response: %s\n", str(s, "response"))
	fmt.Printf("timezone: %s\n", str(s, "timezone"))
	fmt.Printf("lastRunAt: %s\n", str(s, "lastRunAt"))
	fmt.Printf("lastMessageId: %s\n", str(s, "lastMessageId"))
	if errMsg := str(s, "lastError"); errMsg != "" {
		fmt.Printf("lastError: %s\n", errMsg)
	}
	fmt.Printf("body:\n%s\n", indent(str(s, "body")))
}

func cmdTeam(a args) {
	if len(a.positional) > 0 && a.positional[0] == "link" {
		cmdTeamLink(a)
		return
	}
	resp, err := api("GET", "/api/team", nil)
	if err != nil {
		fail(err)
	}
	team, _ := resp["team"].(map[string]any)
	if len(a.positional) > 0 && a.positional[0] == "links" {
		agent := ""
		if len(a.positional) > 1 {
			agent = a.positional[1]
		}
		printTeamLinks(team, agent)
		return
	}
	if len(a.positional) > 0 {
		printTeamAgent(team, a.positional[0])
		printTeamLinks(team, a.positional[0])
		return
	}
	printTeamAgents(team)
	fmt.Println()
	printTeamLinks(team, "")
}

func printTeamAgents(team map[string]any) {
	agents, _ := team["agents"].([]any)
	fmt.Println(bold("Agents"))
	if len(agents) == 0 {
		fmt.Println("  no agents")
		return
	}
	for _, v := range agents {
		agent, _ := v.(map[string]any)
		total := int(num(agent, "messageIn") + num(agent, "messageOut"))
		line := fmt.Sprintf("  %s %s in=%.0f out=%.0f open=%.0f total=%d",
			bold(pad(str(agent, "name"), 18)),
			dim(str(agent, "status")),
			num(agent, "messageIn"),
			num(agent, "messageOut"),
			num(agent, "openIn"),
			total,
		)
		if n := num(agent, "scheduledIn"); n > 0 {
			line += fmt.Sprintf(" scheduled=%.0f", n)
		}
		if profile, ok := agent["profile"].(map[string]any); ok {
			if domain := firstLine(str(profile, "domain")); domain != "" {
				line += "  " + cyan(clip(domain, 70))
			}
		}
		fmt.Println(line)
	}
}

func printTeamAgent(team map[string]any, key string) {
	for _, value := range anySlice(team["agents"]) {
		agent, _ := value.(map[string]any)
		if str(agent, "name") != key && str(agent, "id") != key {
			continue
		}
		fmt.Printf("%s %s (%s)\n", bold("Agent"), bold(str(agent, "name")), str(agent, "id"))
		fmt.Printf("status: %s\n", str(agent, "status"))
		if profile, ok := agent["profile"].(map[string]any); ok {
			printProfile(profile)
		}
		return
	}
	fail(fmt.Errorf("agent not found: %s", key))
}

func printTeamLinks(team map[string]any, agent string) {
	fmt.Println(bold("Explicit relationships"))
	explicitCount := 0
	for _, v := range anySlice(team["explicitLinks"]) {
		link, _ := v.(map[string]any)
		if !matchesAgentLink(link, agent) {
			continue
		}
		explicitCount++
		fmt.Printf("  %s  %s -> %s\n    %s\n", str(link, "id"), bold(str(link, "from")), bold(str(link, "to")), indent(str(link, "description")))
	}
	if explicitCount == 0 {
		fmt.Println("  no explicit relationships")
	}
	fmt.Println()
	links, _ := team["observedLinks"].([]any)
	fmt.Println(bold("Observed links"))
	observedCount := 0
	for _, v := range links {
		link, _ := v.(map[string]any)
		if !matchesAgentLink(link, agent) {
			continue
		}
		observedCount++
		fmt.Printf("  %s -> %s  messages=%.0f replies=%.0f open=%.0f answered=%.0f last=%s\n",
			bold(str(link, "from")),
			bold(str(link, "to")),
			num(link, "messageCount"),
			num(link, "replyCount"),
			num(link, "openCount"),
			num(link, "answeredCount"),
			str(link, "lastMessageAt"),
		)
		if subjects, ok := link["subjects"].([]any); ok && len(subjects) > 0 {
			names := []string{}
			for _, s := range subjects {
				if text, ok := s.(string); ok {
					names = append(names, text)
				}
			}
			if len(names) > 0 {
				fmt.Printf("    %s\n", dim(strings.Join(names, " · ")))
			}
		}
	}
	if observedCount == 0 {
		fmt.Println("  no observed links")
	}
}

func matchesAgentLink(link map[string]any, agent string) bool {
	return agent == "" || str(link, "from") == agent || str(link, "to") == agent || str(link, "fromAgentId") == agent || str(link, "toAgentId") == agent
}

func cmdTeamLink(a args) {
	if len(a.positional) < 2 {
		usage("team link add|update|delete ...")
	}
	action := a.positional[1]
	switch action {
	case "add":
		if len(a.positional) < 4 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team link add <from> <to> --description <text>")
		}
		resp, err := api("POST", "/api/team/relationships", map[string]any{"from": a.positional[2], "to": a.positional[3], "description": a.flags["description"]})
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	case "update":
		if len(a.positional) < 3 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team link update <id> --description <text>")
		}
		resp, err := api("PATCH", "/api/team/relationships/"+url.PathEscape(a.positional[2]), map[string]any{"description": a.flags["description"]})
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	case "delete", "rm":
		if len(a.positional) < 3 {
			usage("team link delete <id>")
		}
		resp, err := api("DELETE", "/api/team/relationships/"+url.PathEscape(a.positional[2]), nil)
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	default:
		usage("team link add|update|delete ...")
	}
}

func printRelationship(value any) {
	rel, _ := value.(map[string]any)
	fmt.Printf("%s %s -> %s (%s)\n%s\n", green("relationship"), bold(str(rel, "from")), bold(str(rel, "to")), str(rel, "id"), indent(str(rel, "description")))
}

func cmdProfile(a args) {
	if len(a.positional) < 2 {
		usage("profile get|set|clear <agent> ...")
	}
	action, key := a.positional[0], a.positional[1]
	profileResp, err := api("GET", "/api/agents/"+url.PathEscape(key)+"/profile", nil)
	if err != nil {
		fail(err)
	}
	current, _ := profileResp["profile"].(map[string]any)
	if action == "get" {
		printProfile(current)
		return
	}
	params := map[string]any{
		"identity": str(current, "identity"), "domain": str(current, "domain"), "scope": str(current, "scope"),
		"expectedVersion": num(current, "version"),
	}
	switch action {
	case "set":
		changed := false
		if path := a.flags["file"]; path != "" {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				fail(readErr)
			}
			var fileProfile struct {
				Identity string `json:"identity"`
				Domain   string `json:"domain"`
				Scope    string `json:"scope"`
			}
			if err := json.Unmarshal(data, &fileProfile); err != nil {
				fail(fmt.Errorf("parse profile JSON: %w", err))
			}
			params["identity"], params["domain"], params["scope"] = fileProfile.Identity, fileProfile.Domain, fileProfile.Scope
			params["expectedVersion"] = num(current, "version")
			changed = true
		}
		for _, field := range []string{"identity", "domain", "scope"} {
			if value, ok := a.flags[field]; ok {
				params[field] = value
				changed = true
			}
		}
		if !changed {
			usage("profile set <agent> [--identity TEXT] [--domain TEXT] [--scope TEXT] [--file profile.json]")
		}
	case "clear":
		params["identity"], params["domain"], params["scope"] = "", "", ""
	default:
		usage("profile get|set|clear <agent> ...")
	}
	resp, err := api("PUT", "/api/agents/"+url.PathEscape(key)+"/profile", params)
	if err != nil {
		fail(err)
	}
	profile, _ := resp["profile"].(map[string]any)
	printProfile(profile)
}

func printProfile(profile map[string]any) {
	fmt.Print(formatProfile(profile))
}

func formatProfile(profile map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "profile v%.0f\n", num(profile, "version"))
	for _, field := range []string{"identity", "domain", "scope"} {
		fmt.Fprintf(&b, "%s:\n", field)
		if value := str(profile, field); value != "" {
			fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(value, "\n", "\n  "))
		} else {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func cmdInterrupt(a args) {
	if len(a.positional) < 1 {
		usage("interrupt <name|id>")
	}
	resp, err := api("POST", "/api/agents/"+url.PathEscape(a.positional[0])+"/turns/current/interrupt", map[string]any{})
	if err != nil {
		fail(err)
	}
	if interrupted, _ := resp["interrupted"].(bool); interrupted {
		fmt.Println(yellow("interrupt requested"))
	} else {
		fmt.Printf("nothing to interrupt (%s)\n", str(resp, "message"))
	}
}

func cmdArchive(a args) {
	if len(a.positional) < 1 {
		usage("agent archive <name|id>")
	}
	resp, err := api("DELETE", "/api/agents/"+url.PathEscape(a.positional[0]), nil)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s %s (%s)\n", yellow("archived"), str(resp, "name"), str(resp, "id"))
}

func cmdBackup(a args) {
	reason := a.flags["reason"]
	if reason == "" {
		reason = "manual"
	}
	resp, err := api("POST", "/api/admin/backup", map[string]any{"reason": reason})
	if err != nil {
		fail(err)
	}
	b, _ := resp["backup"].(map[string]any)
	fmt.Printf("%s %s\n", green("backup created"), str(b, "name"))
	fmt.Printf("  path:    %s\n", str(b, "path"))
	if n, ok := b["rolloutCount"].(float64); ok {
		fmt.Printf("  rollouts: %.0f\n", n)
	}
	if warnings, ok := b["warnings"].([]any); ok && len(warnings) > 0 {
		fmt.Printf("  warnings: %d\n", len(warnings))
	}
}

func cmdBackups() {
	resp, err := api("GET", "/api/admin/backups", nil)
	if err != nil {
		fail(err)
	}
	if dir := str(resp, "dir"); dir != "" {
		fmt.Println(dim(dir))
	}
	backups, _ := resp["backups"].([]any)
	if len(backups) == 0 {
		fmt.Println("no backups")
		return
	}
	for _, v := range backups {
		b, _ := v.(map[string]any)
		size := ""
		if n, ok := b["sizeBytes"].(float64); ok {
			size = fmt.Sprintf(" %.1f KB", n/1024)
		}
		fmt.Printf("%s%s\n  %s\n", bold(str(b, "name")), dim(size), str(b, "path"))
	}
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
		"/api/agents/"+url.PathEscape(a.positional[0])+"/thread/approvals/"+url.PathEscape(a.positional[1]),
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
