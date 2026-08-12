package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	loomskills "github.com/yan5xu/codex-loom/skills"
)

const skillsUsage = "skills [status [name] [--root PATH]|list|install [name] [--force] [--root PATH]|reload|agent <agent> [disable|enable <absolute SKILL.md path>]]"

func cmdSkills(a args) {
	if a.flags["help"] == "true" {
		printSkillsHelp()
		return
	}
	action := "status"
	if len(a.positional) > 0 {
		action = a.positional[0]
	}
	if action == "help" || action == "-h" || action == "--help" {
		printSkillsHelp()
		return
	}
	if action == "list" {
		for _, definition := range loomskills.Definitions() {
			fmt.Printf("%s  %s\n", bold(definition.Name), dim(definition.Description))
		}
		return
	}
	if action == "agent" {
		cmdAgentSkills(a)
		return
	}

	root := strings.TrimSpace(a.flags["root"])
	if root == "" {
		var err error
		root, err = loomskills.UserRoot()
		if err != nil {
			fail(err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fail(err)
	}
	var names []string
	if len(a.positional) > 1 {
		names = a.positional[1:]
	}

	switch action {
	case "status":
		statuses, err := loomskills.Inspect(root, names)
		if err != nil {
			fail(err)
		}
		for _, status := range statuses {
			printSkillStatus(status.Name, string(status.State), status.Hash, status.Path, false)
		}
	case "install":
		results, err := loomskills.Install(root, names, a.flags["force"] == "true")
		if err != nil {
			fail(err)
		}
		for _, result := range results {
			printSkillStatus(result.Name, string(result.State), result.Hash, result.Path, result.Changed)
		}
		fmt.Printf("%s %s\n", dim("Codex user skill root:"), root)
		if _, err := api("POST", "/api/skills/reload", map[string]any{}); err != nil {
			fmt.Printf("%s %s\n", yellow("Hub inventory was not refreshed:"), err)
			fmt.Printf("%s %s\n", dim("After updating or starting the Hub, run:"), commandName+" skills reload")
		} else {
			fmt.Println(green("Running Hub skill inventory refreshed; new Turns use the updated catalog."))
		}
	case "reload":
		resp, err := api("POST", "/api/skills/reload", map[string]any{})
		if err != nil {
			fail(err)
		}
		inventory, _ := resp["inventory"].(map[string]any)
		entries := anySlice(inventory["data"])
		total := 0
		for _, value := range entries {
			entry, _ := value.(map[string]any)
			total += len(anySlice(entry["skills"]))
		}
		fmt.Printf("%s %d Agent workspaces, %d available Skill entries\n", green("reloaded"), len(entries), total)
	default:
		usage(skillsUsage)
	}
}

func printSkillsHelp() {
	fmt.Printf("usage: %s %s\n", commandName, skillsUsage)
	fmt.Println()
	fmt.Println("With no subcommand, status lists all shared Codex Skills.")
	fmt.Println("Agent disables are exact-path exceptions and do not modify the shared Skill.")
}

func cmdAgentSkills(a args) {
	if len(a.positional) < 2 {
		usage("skills agent <agent> [disable|enable <absolute SKILL.md path>]")
	}
	agent := a.positional[1]
	method := "GET"
	path := "/api/agents/" + url.PathEscape(agent) + "/skills"
	var body any
	if len(a.positional) > 2 {
		if len(a.positional) != 4 || (a.positional[2] != "disable" && a.positional[2] != "enable") {
			usage("skills agent <agent> [disable|enable <absolute SKILL.md path>]")
		}
		method = "PATCH"
		path += "/config"
		body = map[string]any{
			"path":    a.positional[3],
			"enabled": a.positional[2] == "enable",
		}
	}
	resp, err := api(method, path, body)
	if err != nil {
		fail(err)
	}
	config, _ := resp["config"].(map[string]any)
	inventory, _ := resp["inventory"].(map[string]any)
	fmt.Printf("%s %s (%s)\n", bold("Agent Skills"), str(inventory, "agentName"), str(inventory, "agentId"))
	for _, value := range anySlice(inventory["skills"]) {
		skill, _ := value.(map[string]any)
		state := green("enabled")
		if !boolean(skill, "enabled") {
			state = yellow("disabled")
		}
		fmt.Printf("  %-10s  %-28s  %s\n", state, str(skill, "name"), dim(str(skill, "path")))
	}
	for _, value := range anySlice(config["disabledPaths"]) {
		disabledPath, _ := value.(string)
		found := false
		for _, skillValue := range anySlice(inventory["skills"]) {
			skill, _ := skillValue.(map[string]any)
			if str(skill, "path") == disabledPath {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("  %-10s  %-28s  %s\n", yellow("disabled"), "(not discovered)", dim(disabledPath))
		}
	}
	if boolean(config, "restartRequired") {
		fmt.Println(yellow("Restart required: the loaded Codex Thread still has the previous Skill policy."))
	} else if boolean(config, "applied") {
		fmt.Println(green("Agent Skill policy is active for the next Turn."))
	} else {
		fmt.Println(dim("Agent Skill policy will apply when the Codex Thread is next loaded."))
	}
}

func printSkillStatus(name, state, hash, path string, changed bool) {
	label := state
	switch state {
	case string(loomskills.StateInstalled):
		label = green(state)
	case string(loomskills.StateModified):
		label = yellow(state)
	default:
		label = dim(state)
	}
	action := ""
	if changed {
		action = "  " + green("updated")
	}
	fmt.Printf("%s  %s  %s%s\n  %s\n", bold(name), label, dim(hash), action, dim(path))
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
		usage("create <name> --cwd <path> [--display-name TEXT]")
	}
	cwd, err := filepath.Abs(a.flags["cwd"])
	if err != nil {
		fail(err)
	}
	resp, err := api("POST", "/api/agents", map[string]any{
		"name":           a.positional[0],
		"displayName":    a.flags["display-name"],
		"cwd":            cwd,
		"approvalPolicy": a.flags["approval"],
		"sandbox":        a.flags["sandbox"],
		"providerId":     a.flags["provider"],
		"model":          a.flags["model"],
		"effort":         a.flags["effort"],
	})
	if err != nil {
		fail(err)
	}
	s, _ := resp["agent"].(map[string]any)
	providerID := str(s, "providerId")
	if providerID == "" {
		providerID = "openai"
	}
	fmt.Printf("%s %s (%s)\n  cwd:     %s\n  thread:  %s\n  provider: %s\n  model:    %s\n",
		green("created"), bold(str(s, "name")), str(s, "id"), str(s, "cwd"), str(s, "threadId"), providerID, str(s, "model"))
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

func cmdAgentProvider(a args) {
	if len(a.positional) < 1 || strings.TrimSpace(a.flags["provider"]) == "" {
		usage("agent provider <name|id> --provider PROVIDER [--model MODEL]")
	}
	providerID := strings.TrimSpace(a.flags["provider"])
	resp, err := api("POST", "/api/agents/"+url.PathEscape(a.positional[0])+"/provider", map[string]any{
		"providerId": providerID,
		"model":      strings.TrimSpace(a.flags["model"]),
	})
	if err != nil {
		fail(err)
	}
	agent, _ := resp["agent"].(map[string]any)
	actualProvider := str(agent, "providerId")
	if actualProvider == "" {
		actualProvider = "openai"
	}
	fmt.Printf("%s %s (%s)\n  thread:   %s\n  provider: %s\n  model:    %s\n",
		green("switched"), bold(str(agent, "name")), str(agent, "id"), str(agent, "threadId"), actualProvider, str(agent, "model"))
}

func cmdSend(a args) {
	attachments := append([]string(nil), a.flagValues["attachment"]...)
	attachments = append(attachments, a.flagValues["file"]...)
	if len(a.positional) < 1 || (len(a.positional) < 2 && len(attachments) == 0) {
		usage(`send <name|id> ["<task>"] [--attachment PATH ...]`)
	}
	text := ""
	if len(a.positional) > 1 {
		text = a.positional[1]
	}
	artifactIDs := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		absolute, err := filepath.Abs(attachment)
		if err != nil {
			fail(err)
		}
		resp, err := apiUpload("/api/agents/"+url.PathEscape(a.positional[0])+"/artifacts", absolute)
		if err != nil {
			fail(err)
		}
		artifact, _ := resp["artifact"].(map[string]any)
		if id := str(artifact, "id"); id != "" {
			artifactIDs = append(artifactIDs, id)
		}
	}
	body := map[string]any{"text": text, "artifactIds": artifactIDs}
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

func cmdArtifact(a args) {
	action := ""
	if len(a.positional) > 0 {
		action = a.positional[0]
	}
	if action != "publish" {
		usage("artifact publish --from AGENT --file PATH [--file PATH ...]")
	}
	from := strings.TrimSpace(a.flags["from"])
	files := append([]string(nil), a.flagValues["file"]...)
	if from == "" || len(files) == 0 {
		usage("artifact publish --from AGENT --file PATH [--file PATH ...]")
	}
	for _, source := range files {
		absolute, err := filepath.Abs(source)
		if err != nil {
			fail(err)
		}
		resp, err := apiUpload("/api/agents/"+url.PathEscape(from)+"/artifacts?publish=true", absolute)
		if err != nil {
			fail(err)
		}
		artifact, _ := resp["artifact"].(map[string]any)
		fmt.Printf("%s %s  %s\n  %s\n", green("published"), str(artifact, "id"), str(artifact, "name"), base+str(artifact, "url"))
	}
}

func cmdAskUser(a args) {
	from := strings.TrimSpace(a.flags["from"])
	question := strings.TrimSpace(a.flags["question"])
	if question == "" && len(a.positional) > 0 {
		question = strings.TrimSpace(strings.Join(a.positional, " "))
	}
	if from == "" || question == "" {
		usage(`ask-user --from AGENT --question TEXT [--context TEXT] [--blocks TEXT] [--topic TOPIC_ID] [--option "Label::description" ...] [--optional]`)
	}
	expectation := "required"
	if _, optional := a.flags["optional"]; optional {
		expectation = "optional"
	}
	if value := strings.TrimSpace(a.flags["expectation"]); value != "" {
		expectation = value
	}
	options := make([]map[string]any, 0, len(a.flagValues["option"]))
	for _, raw := range a.flagValues["option"] {
		parts := strings.SplitN(raw, "::", 2)
		option := map[string]any{"label": strings.TrimSpace(parts[0])}
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			option["description"] = strings.TrimSpace(parts[1])
		}
		options = append(options, option)
	}
	blockedWork := strings.TrimSpace(a.flags["blocks"])
	if blockedWork == "" {
		blockedWork = strings.TrimSpace(a.flags["blocked-work"])
	}
	resp, err := api("POST", "/api/human-requests", map[string]any{
		"agent": from, "expectation": expectation, "question": question,
		"context": strings.TrimSpace(a.flags["context"]), "blockedWork": blockedWork,
		"options": options,
		"topicId": strings.TrimSpace(a.flags["topic"]),
	})
	if err != nil {
		fail(err)
	}
	request, _ := resp["request"].(map[string]any)
	fmt.Printf("%s %s (%s)\n", green("created human request"), str(request, "id"), str(request, "expectation"))
	fmt.Printf("question: %s\n", str(request, "question"))
	fmt.Println(dim("The request is durable. End this Turn normally; CodexLoom will resume this Agent Thread when the human answers."))
}
