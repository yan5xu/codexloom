// loom is the CodexLoom CLI. Agents and their Threads live in the service, not
// in this process: Ctrl-C on `loom thread watch` only detaches the observer.
//
// CODEX_LOOM_URL selects a non-default service. CHUB_URL remains a compatibility
// alias for existing Agent scripts.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	flagValues map[string][]string
}

func parseArgs(argv []string) args {
	a := args{flags: map[string]string{}, flagValues: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "--") {
			name := strings.TrimPrefix(argv[i], "--")
			if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "--") {
				a.flags[name] = argv[i+1]
				a.flagValues[name] = append(a.flagValues[name], argv[i+1])
				i++
			} else {
				a.flags[name] = "true"
				a.flagValues[name] = append(a.flagValues[name], "true")
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
		return map[string]any{}, 0, false, fmt.Errorf("cannot reach CodexLoom at %s (%v); start it with: codex-loom", base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	parsed := map[string]any{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return parsed, resp.StatusCode, false, nil
	}
	return parsed, resp.StatusCode, true, nil
}

func apiUpload(path, filePath string) (map[string]any, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect attachment: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 25<<20 {
		return nil, fmt.Errorf("attachment must be a regular file between 1 byte and 25 MB")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, base+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach CodexLoom at %s (%v); start it with: codex-loom", base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	parsed := map[string]any{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("CodexLoom returned a non-JSON response for %s", path)
	}
	if resp.StatusCode >= 400 {
		message := http.StatusText(resp.StatusCode)
		if value, ok := parsed["error"].(string); ok {
			message = value
		}
		return nil, fmt.Errorf("(%d) %s", resp.StatusCode, message)
	}
	return parsed, nil
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
	case "artifact":
		cmdArtifact(a)
	case "msg":
		cmdMsg(a)
	case "ask-user":
		cmdAskUser(a)
	case "inbox":
		cmdInbox(a)
	case "outbox":
		cmdOutbox(a)
	case "integration":
		cmdIntegration(a)
	case "prll":
		cmdPrll(a)
	case "lark", "feishu":
		cmdLark(a)
	case "conversation":
		cmdConversation(a)
	case "schedule":
		cmdSchedule(a)
	case "team":
		cmdTeam(a)
	case "workload":
		cmdWorkload(a)
	case "profile":
		cmdProfile(a)
	case "goal":
		cmdGoal(a)
	case "skills":
		cmdSkills(a)
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
		cmdBackups(a)
	case "version":
		cmdVersion(a)
	case "doctor":
		cmdDoctor(a)
	case "dev":
		cmdDev(a)
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
  chub create <name> --cwd <path> [--approval never|on-request] [--sandbox MODE] [--model gpt-5.6-sol|gpt-5.6-terra|gpt-5.6-luna|M] [--effort minimal|low|medium|high|xhigh]
  chub list
  chub get <name|id>
  chub rename <name|id> <new-name>
  chub send <name|id> ["<task>"] [--attachment PATH ...] [--timeout SEC]
  chub artifact publish --from AGENT --file PATH [--file PATH ...]
  chub msg <to> [body] --from <agent> --subject <text> [--response required|none]
  chub msg --reply-to <message-id> --from <agent> [--subject <text>] [body]
  chub msg --no-reply <message-id> --from <agent>
  chub msg status <message-id>
  chub msg wait <message-id> [--timeout SEC]
  chub msg retry <message-id>
  chub msg cancel <message-id>
  chub msg resolve <message-id> --from <sender> --resolution completed_elsewhere|superseded --reason <text>
  chub ask-user --from AGENT --question TEXT [--context TEXT] [--blocks TEXT] [--option "Label::description" ...] [--optional]
  chub inbox [list] [agent] [--state queued|handling|deferred|handled|failed] [--origin PROVIDER]
  chub inbox get <inbox-item-id>
  chub inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT] [--attachment PATH]
  chub inbox no-reply <inbox-item-id> --agent <agent> [--reason TEXT]
  chub inbox defer <inbox-item-id> --agent <agent> --until RFC3339 [--reason TEXT]
  chub inbox retry <inbox-item-id>
  chub outbox [list] [agent] [--state pending|sending|sent|failed]
  chub outbox send <agent> <address-id> <conversation-id> [body|--body TEXT] [--attachment PATH] [--thread ID] [--message-id ID] [--expectation none|optional|required] [--idempotency-key KEY]
  chub outbox retry <outbox-item-id>
  chub integration list
  chub integration send --from AGENT (--reply-to INBOX_ID|--to MEMBERSHIP_ID) [--message-id PROVIDER_MESSAGE_ID] [--thread-id PROVIDER_THREAD_ID] [--body TEXT|--body-file PATH] [--file PATH ...] [--expect-reply none|optional|required] [--idempotency-key KEY] [--async]
  chub integration connect <provider> [--account REF] [--credential-ref env:NAME]
  chub integration import parall --agent AGENT --org-id ORG --external-agent-id USER [--agent-key-file PATH] [--api-url URL] [--trust-domain NAME]
  chub integration bind <agent> <connection-id> --identity EXTERNAL_ID [--display-name NAME] [--trigger mention] [--reply-policy final_answer] [--dm-policy managed] [--trust-domain NAME] [--enabled true|false] [allow/block flags]
  chub integration update-address <address-id> [--identity ID] [--display-name NAME] [--trigger mention] [--reply-policy final_answer] [--dm-policy managed] [--trust-domain NAME] [--enabled true|false] [allow/block flags]
  chub integration enable|disable <connection-id|address-id>
  chub integration status [connection-id]
  loom prll chats list --address ADDRESS_ID [--limit N] [--cursor CURSOR]
  loom prll chats get CHAT_ID --address ADDRESS_ID
  loom prll chats discoverable --address ADDRESS_ID [--query TEXT] [--limit N]
  loom prll chats members list CHAT_ID --address ADDRESS_ID
  loom prll messages list CHAT_ID --address ADDRESS_ID [--limit N] [--before ID] [--after ID] [--since DATE] [--thread-root-id ID] [--top-level]
  loom prll messages get MESSAGE_ID --address ADDRESS_ID
  loom prll messages replies MESSAGE_ID --address ADDRESS_ID [--limit N] [--before ID] [--after ID]
  loom lark messages list CHAT_ID --address ADDRESS_ID [--limit N] [--page-token TOKEN] [--start-time UNIX] [--end-time UNIX] [--sort asc|desc] [--thread-id ID] [--thread-root-only]
  loom lark messages get MESSAGE_ID --chat-id CHAT_ID --address ADDRESS_ID
  loom lark messages replies MESSAGE_ID --chat-id CHAT_ID --address ADDRESS_ID [--limit N]
  chub conversation list [agent] [--address ADDRESS_ID]
  chub conversation discover [agent] [--address ADDRESS_ID] [--all]
  chub conversation get <membership-id>
  chub conversation set <address-id> <conversation-id> [--file membership.json] [--type group|dm] [--actor EXTERNAL_ID] [--name NAME] [--purpose TEXT] [--role TEXT] [--guidance TEXT] [--trigger mention] [--reply-policy final_answer] [--outbound-policy reply_only|proactive|none] [--enabled true|false] [--expected-version N]
  chub conversation enable|disable <membership-id>
  chub schedule add <name> --to <agent> --subject <text> (--at RFC3339|--cron "M H D M W") [--tz TZ] [--response required|none] [body]
  chub schedule list|get|run|enable|disable|delete ...
  chub profile get <agent>
  chub profile set <agent> [--identity TEXT] [--domain TEXT] [--scope TEXT] [--file profile.json]
  chub profile clear <agent>
  chub goal <agent> [show]
  chub goal <agent> set <objective> [--token-budget N|--clear-token-budget]
  chub goal <agent> pause|resume|clear
  chub skills list
  chub skills status [name]
  chub skills install [name] [--force]
  chub skills reload
  chub remote [status]
  chub remote enable
  chub remote disable
  chub remote pair
  chub remote devices
  chub remote revoke <client-id>
  chub team [agent]
  chub team links [agent]
  chub workload [agent] [--days 1|7|30|90] [--evidence] [--json]
  chub team organization add <parent> <child> --description <text>
  chub team organization update <id> --description <text>
  chub team organization delete <id>
  chub team collaboration add <from> <to> --description <text>
  chub team collaboration update <id> --description <text>
  chub team collaboration delete <id>
  chub watch <name|id> [--tail N]
  chub interrupt <name|id>
  chub history <name|id> [--count N]
  chub backup [--reason text]
  chub backups [prune]
  chub version [--running]
  chub doctor
  chub dev canary start [--agent NAME ...] [--port auto|N] [--from DATA_DIR]
  chub dev canary status|stop
  chub approve <name|id> <approvalId>   /  chub reject <name|id> <approvalId>
  chub thread interrupt <name|id>              stop the current Turn
  chub agent archive <name|id>                 archive the long-lived Agent
`, base)
	fmt.Print(strings.ReplaceAll(help, "chub", commandName))
}
