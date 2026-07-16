package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

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
	if len(a.positional) > 0 && (a.positional[0] == "collaboration" || a.positional[0] == "link") {
		cmdTeamLink(a)
		return
	}
	if len(a.positional) > 0 && a.positional[0] == "organization" {
		cmdTeamOrganization(a)
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
	fmt.Println(bold("Organization"))
	organizationCount := 0
	for _, value := range anySlice(team["organizationLinks"]) {
		link, _ := value.(map[string]any)
		if !matchesOrganizationLink(link, agent) {
			continue
		}
		organizationCount++
		fmt.Printf("  %s  %s -> %s\n    %s\n", str(link, "id"), bold(str(link, "parent")), bold(str(link, "child")), indent(str(link, "description")))
	}
	if organizationCount == 0 {
		fmt.Println("  no organization relationships")
	}
	fmt.Println()
	fmt.Println(bold("Collaboration"))
	explicitCount := 0
	links := anySlice(team["collaborationLinks"])
	if len(links) == 0 {
		links = anySlice(team["explicitLinks"])
	}
	for _, v := range links {
		link, _ := v.(map[string]any)
		if !matchesAgentLink(link, agent) {
			continue
		}
		explicitCount++
		fmt.Printf("  %s  %s -> %s\n    %s\n", str(link, "id"), bold(str(link, "from")), bold(str(link, "to")), indent(str(link, "description")))
	}
	if explicitCount == 0 {
		fmt.Println("  no collaboration relationships")
	}
	fmt.Println()
	observedLinks, _ := team["observedLinks"].([]any)
	fmt.Println(bold("Activity evidence"))
	observedCount := 0
	for _, v := range observedLinks {
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

func matchesOrganizationLink(link map[string]any, agent string) bool {
	return agent == "" || str(link, "parent") == agent || str(link, "child") == agent || str(link, "parentAgentId") == agent || str(link, "childAgentId") == agent
}

func matchesAgentLink(link map[string]any, agent string) bool {
	return agent == "" || str(link, "from") == agent || str(link, "to") == agent || str(link, "fromAgentId") == agent || str(link, "toAgentId") == agent
}

func cmdTeamLink(a args) {
	if len(a.positional) < 2 {
		usage("team collaboration add|update|delete ...")
	}
	action := a.positional[1]
	switch action {
	case "add":
		if len(a.positional) < 4 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team collaboration add <from> <to> --description <text>")
		}
		resp, err := api("POST", "/api/team/relationships", map[string]any{"from": a.positional[2], "to": a.positional[3], "description": a.flags["description"]})
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	case "update":
		if len(a.positional) < 3 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team collaboration update <id> --description <text>")
		}
		resp, err := api("PATCH", "/api/team/relationships/"+url.PathEscape(a.positional[2]), map[string]any{"description": a.flags["description"]})
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	case "delete", "rm":
		if len(a.positional) < 3 {
			usage("team collaboration delete <id>")
		}
		resp, err := api("DELETE", "/api/team/relationships/"+url.PathEscape(a.positional[2]), nil)
		if err != nil {
			fail(err)
		}
		printRelationship(resp["relationship"])
	default:
		usage("team collaboration add|update|delete ...")
	}
}

func cmdTeamOrganization(a args) {
	if len(a.positional) < 2 {
		usage("team organization add|update|delete ...")
	}
	action := a.positional[1]
	switch action {
	case "add":
		if len(a.positional) < 4 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team organization add <parent> <child> --description <text>")
		}
		response, err := api("POST", "/api/team/organization", map[string]any{
			"parent": a.positional[2], "child": a.positional[3], "description": a.flags["description"],
		})
		if err != nil {
			fail(err)
		}
		printOrganizationRelationship(response["relationship"])
	case "update":
		if len(a.positional) < 3 || strings.TrimSpace(a.flags["description"]) == "" {
			usage("team organization update <id> --description <text>")
		}
		response, err := api("PATCH", "/api/team/organization/"+url.PathEscape(a.positional[2]), map[string]any{"description": a.flags["description"]})
		if err != nil {
			fail(err)
		}
		printOrganizationRelationship(response["relationship"])
	case "delete", "rm":
		if len(a.positional) < 3 {
			usage("team organization delete <id>")
		}
		response, err := api("DELETE", "/api/team/organization/"+url.PathEscape(a.positional[2]), nil)
		if err != nil {
			fail(err)
		}
		printOrganizationRelationship(response["relationship"])
	default:
		usage("team organization add|update|delete ...")
	}
}

func printOrganizationRelationship(value any) {
	relationship, _ := value.(map[string]any)
	fmt.Printf("%s %s -> %s (%s)\n%s\n", green("organization"), bold(str(relationship, "parent")), bold(str(relationship, "child")), str(relationship, "id"), indent(str(relationship, "description")))
}

func printRelationship(value any) {
	rel, _ := value.(map[string]any)
	fmt.Printf("%s %s -> %s (%s)\n%s\n", green("collaboration"), bold(str(rel, "from")), bold(str(rel, "to")), str(rel, "id"), indent(str(rel, "description")))
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
		if messageID := str(resp, "heldMessageId"); messageID != "" {
			subject := str(resp, "heldSubject")
			if subject != "" {
				fmt.Printf("%s %s — %s\n", yellow("held"), messageID, subject)
			} else {
				fmt.Printf("%s %s\n", yellow("held"), messageID)
			}
			fmt.Printf("continue: %s msg retry %s\n", commandName, messageID)
			fmt.Printf("inspect:  %s msg status %s\n", commandName, messageID)
		}
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
	if prune, ok := b["prune"].(map[string]any); ok {
		if removed, ok := prune["removedCount"].(float64); ok && removed > 0 {
			fmt.Printf("  pruned:   %.0f snapshots (%s)\n", removed, formatByteCount(number(prune, "removedBytes")))
		}
	}
}

func cmdBackups(a args) {
	method := "GET"
	path := "/api/admin/backups"
	if len(a.positional) > 0 {
		if a.positional[0] != "prune" || len(a.positional) > 1 {
			usage("backups [prune]")
		}
		method = "POST"
		path = "/api/admin/backups/prune"
	}
	resp, err := api(method, path, nil)
	if err != nil {
		fail(err)
	}
	if prune, ok := resp["prune"].(map[string]any); ok {
		fmt.Printf("%s %.0f snapshots (%s)\n", green("pruned"), number(prune, "removedCount"), formatByteCount(number(prune, "removedBytes")))
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
			size = " " + formatByteCount(n)
		}
		fmt.Printf("%s%s\n  %s\n", bold(str(b, "name")), dim(size), str(b, "path"))
	}
	if retention, ok := resp["retention"].(map[string]any); ok {
		fmt.Printf("retention: keep %.0f-%.0f, max %s, %.0f days\n",
			number(retention, "minCount"), number(retention, "maxCount"),
			formatByteCount(number(retention, "maxBytes")), number(retention, "maxAgeDays"))
	}
}

func number(value map[string]any, key string) float64 {
	n, _ := value[key].(float64)
	return n
}

func formatByteCount(bytes float64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B", bytes)
	}
	labels := []string{"KiB", "MiB", "GiB", "TiB"}
	value := bytes
	for _, label := range labels {
		value /= unit
		if value < unit || label == labels[len(labels)-1] {
			return fmt.Sprintf("%.1f %s", value, label)
		}
	}
	return fmt.Sprintf("%.0f B", bytes)
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
