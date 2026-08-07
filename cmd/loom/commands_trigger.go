package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func cmdTrigger(a args) {
	if len(a.positional) == 0 {
		usage("trigger add|list|get|wait|pause|resume|cancel|source ...")
	}
	switch a.positional[0] {
	case "add":
		cmdTriggerAdd(a)
	case "list":
		query := url.Values{}
		if len(a.positional) > 1 {
			query.Set("agent", a.positional[1])
		}
		if state := strings.TrimSpace(a.flags["state"]); state != "" {
			query.Set("state", state)
		}
		path := "/api/triggers"
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		resp, err := api("GET", path, nil)
		if err != nil {
			fail(err)
		}
		values := anySlice(resp["triggers"])
		if len(values) == 0 {
			fmt.Println("no triggers")
			return
		}
		for _, value := range values {
			trigger, _ := value.(map[string]any)
			printTriggerLine(trigger)
		}
	case "get":
		trigger := getTriggerArgument(a)
		printTriggerDetail(trigger)
	case "wait":
		if len(a.positional) < 2 {
			usage("trigger wait <trigger-id> [--timeout SEC]")
		}
		timeout, err := positiveDurationFlag(a.flags["timeout"], 30*time.Second)
		if err != nil {
			fail(err)
		}
		deadline := time.Now().Add(timeout)
		for {
			trigger := getTriggerArgument(a)
			state := str(trigger, "state")
			if state != "pending" {
				printTriggerDetail(trigger)
				return
			}
			if !time.Now().Before(deadline) {
				fail(fmt.Errorf("trigger %s is still pending after %s", a.positional[1], timeout))
			}
			time.Sleep(time.Second)
		}
	case "pause", "resume", "cancel":
		if len(a.positional) < 2 {
			usage("trigger " + a.positional[0] + " <trigger-id>")
		}
		resp, err := api("POST", "/api/triggers/"+url.PathEscape(a.positional[1])+"/"+a.positional[0], map[string]any{})
		if err != nil {
			fail(err)
		}
		trigger, _ := resp["trigger"].(map[string]any)
		printTriggerLine(trigger)
	case "source":
		cmdTriggerSource(a)
	default:
		usage("trigger add|list|get|wait|pause|resume|cancel|source ...")
	}
}

func cmdTriggerAdd(a args) {
	if len(a.positional) < 4 || strings.TrimSpace(a.flags["from"]) == "" || strings.TrimSpace(a.flags["resume"]) == "" {
		usage("trigger add github pull-request OWNER/REPO#NUMBER --from AGENT --on EVENT [--on EVENT ...] --resume TEXT [--expect-head SHA] [--expires 14d] [--connection ID]")
	}
	provider := strings.ToLower(a.positional[1])
	resource := strings.ToLower(a.positional[2])
	subject, err := parseTriggerSubject(provider, resource, a.positional[3], a.flags["expect-head"])
	if err != nil {
		fail(err)
	}
	events := a.flagValues["on"]
	if len(events) == 0 {
		fail(fmt.Errorf("at least one --on event is required"))
	}
	conditions := make([]map[string]any, 0, len(events))
	for _, event := range events {
		conditions = append(conditions, map[string]any{"event": event})
	}
	payload := map[string]any{
		"agent": a.flags["from"], "provider": provider, "resourceKind": resource,
		"connectionId": a.flags["connection"], "subject": subject, "conditions": conditions,
		"resumeInstruction": a.flags["resume"], "expiresAt": a.flags["expires"],
		"topicId": a.flags["topic"],
	}
	resp, err := api("POST", "/api/triggers", payload)
	if err != nil {
		fail(err)
	}
	trigger, _ := resp["trigger"].(map[string]any)
	fmt.Printf("%s %s\n", green("trigger created"), str(trigger, "id"))
	printTriggerDetail(trigger)
}

func parseTriggerSubject(provider, resource, raw, expectedHead string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if provider != "github" {
		return nil, fmt.Errorf("unsupported trigger provider: %s", provider)
	}
	switch resource {
	case "pull-request":
		hash := strings.LastIndex(raw, "#")
		slash := strings.Index(raw, "/")
		if slash <= 0 || slash != strings.LastIndex(raw, "/") || hash <= slash+1 || hash == len(raw)-1 {
			return nil, fmt.Errorf("pull request must be OWNER/REPO#NUMBER")
		}
		if number, err := strconv.Atoi(raw[hash+1:]); err != nil || number <= 0 {
			return nil, fmt.Errorf("pull request number must be positive")
		}
		result := map[string]string{"owner": raw[:slash], "repo": raw[slash+1 : hash], "number": raw[hash+1:]}
		if expectedHead = strings.TrimSpace(expectedHead); expectedHead != "" {
			result["expectedHead"] = expectedHead
		}
		return result, nil
	case "workflow-run":
		parts := strings.Split(strings.Trim(raw, "/"), "/")
		if len(parts) != 3 || strings.Contains(parts[0], ":") {
			return nil, fmt.Errorf("workflow run must be OWNER/REPO/RUN_ID")
		}
		runID := parts[len(parts)-1]
		if value, err := strconv.ParseInt(runID, 10, 64); err != nil || value <= 0 {
			return nil, fmt.Errorf("workflow run ID must be positive")
		}
		return map[string]string{"owner": parts[0], "repo": parts[1], "runId": runID}, nil
	default:
		return nil, fmt.Errorf("unsupported GitHub trigger resource: %s", resource)
	}
}

func getTriggerArgument(a args) map[string]any {
	if len(a.positional) < 2 {
		usage("trigger " + a.positional[0] + " <trigger-id>")
	}
	resp, err := api("GET", "/api/triggers/"+url.PathEscape(a.positional[1]), nil)
	if err != nil {
		fail(err)
	}
	trigger, _ := resp["trigger"].(map[string]any)
	return trigger
}

func cmdTriggerSource(a args) {
	if len(a.positional) < 2 || a.positional[1] != "list" && a.positional[1] != "status" {
		usage("trigger source list|status")
	}
	resp, err := api("GET", "/api/triggers/sources", nil)
	if err != nil {
		fail(err)
	}
	values := anySlice(resp["connections"])
	if len(values) == 0 {
		fmt.Println("no Trigger-capable connections")
		return
	}
	for _, value := range values {
		connection, _ := value.(map[string]any)
		scope := str(connection, "scopeRef")
		if scope == "" {
			scope = "legacy"
		}
		fmt.Printf("%s %s %s account=%s scope=%s heartbeat=%s\n", bold(str(connection, "id")), str(connection, "provider"), str(connection, "status"), str(connection, "accountRef"), scope, str(connection, "lastHeartbeatAt"))
		if lastError := str(connection, "lastError"); lastError != "" {
			fmt.Printf("  %s %s\n", red("error:"), lastError)
		}
	}
}

func printTriggerLine(trigger map[string]any) {
	subject, _ := trigger["subject"].(map[string]any)
	key := str(subject, "owner") + "/" + str(subject, "repo")
	if number := str(subject, "number"); number != "" {
		key += "#" + number
	} else if runID := str(subject, "runId"); runID != "" {
		key += "/" + runID
	}
	state := str(trigger, "state")
	stateText := state
	if state == "armed" || state == "triggered" {
		stateText = green(state)
	} else if state == "failed" {
		stateText = red(state)
	} else if state == "paused" {
		stateText = yellow(state)
	}
	fmt.Printf("%s %-9s %s %s -> %s\n", bold(str(trigger, "id")), stateText, str(trigger, "provider"), key, str(trigger, "agent"))
}

func printTriggerDetail(trigger map[string]any) {
	printTriggerLine(trigger)
	fmt.Printf("  resource: %s  connection: %s\n", str(trigger, "resourceKind"), str(trigger, "connectionId"))
	fmt.Printf("  resume: %s\n", str(trigger, "resumeInstruction"))
	if expiry := str(trigger, "expiresAt"); expiry != "" {
		fmt.Printf("  expires: %s\n", expiry)
	}
	if observed := str(trigger, "lastObservedAt"); observed != "" {
		fmt.Printf("  observed: %s\n", observed)
	}
	if message := str(trigger, "lastMessageId"); message != "" {
		fmt.Printf("  message: %s\n", message)
	}
	if lastError := str(trigger, "lastError"); lastError != "" {
		fmt.Printf("  %s %s\n", red("error:"), lastError)
	}
}

func cmdConnectGitHub(a args) {
	resourceOwner := strings.TrimSpace(a.flags["resource-owner"])
	if credentialRef := strings.TrimSpace(a.flags["credential-ref"]); credentialRef != "" {
		if resourceOwner == "" {
			usage("integration connect github --credential-ref env:NAME --resource-owner OWNER")
		}
		resp, err := api("POST", "/api/integrations/providers/github/credential", map[string]any{"credentialRef": credentialRef, "resourceOwner": resourceOwner})
		if err != nil {
			fail(err)
		}
		connection, _ := resp["connection"].(map[string]any)
		fmt.Printf("%s %s (%s)\n", green("GitHub connected"), bold(str(resp, "login")), str(connection, "id"))
		fmt.Println(dim("  Credential remains behind the configured managed, environment, or legacy Keychain reference."))
		return
	}
	if path := strings.TrimSpace(a.flags["token-file"]); path != "" {
		if resourceOwner == "" {
			usage("integration connect github --token-file PATH --resource-owner OWNER")
		}
		if err := requireSecureSecretTransport(base); err != nil {
			fail(err)
		}
		token, err := readOwnerOnlySecretFile(path)
		if err != nil {
			fail(err)
		}
		resp, err := api("POST", "/api/integrations/providers/github/token", map[string]any{"token": token, "resourceOwner": resourceOwner})
		token = ""
		if err != nil {
			fail(err)
		}
		connection, _ := resp["connection"].(map[string]any)
		fmt.Printf("%s %s (%s)\n", green("GitHub connected"), bold(str(resp, "login")), str(connection, "id"))
		fmt.Println(dim("  Token stored in the owner-only managed store; source file was not modified."))
		return
	}
	publicOnly := strings.EqualFold(a.flags["public-only"], "true")
	resp, err := api("POST", "/api/integrations/providers/github/device", map[string]any{"publicOnly": publicOnly})
	if err != nil {
		fail(err)
	}
	device, _ := resp["device"].(map[string]any)
	fmt.Printf("Open %s and enter code %s\n", bold(str(device, "verificationUri")), bold(str(device, "userCode")))
	for {
		wait := int(num(device, "pollAfterSeconds"))
		if wait < 1 {
			wait = 5
		}
		time.Sleep(time.Duration(wait) * time.Second)
		resp, err = api("GET", "/api/integrations/providers/github/device/"+url.PathEscape(str(device, "id")), nil)
		if err != nil {
			fail(err)
		}
		device, _ = resp["device"].(map[string]any)
		switch str(device, "status") {
		case "pending":
			continue
		case "connected":
			connection, _ := device["connection"].(map[string]any)
			fmt.Printf("%s %s (%s)\n", green("GitHub connected"), bold(str(device, "login")), str(connection, "id"))
			return
		default:
			fail(fmt.Errorf("GitHub authorization %s: %s", str(device, "status"), str(device, "error")))
		}
	}
}
