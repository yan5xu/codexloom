package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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
			usage("inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT] [--attachment PATH]")
		}
		body := strings.TrimSpace(a.flags["body"])
		if body == "" && len(a.positional) > 2 {
			body = strings.TrimSpace(a.positional[2])
		}
		content := map[string]any{"text": body}
		if attachment := strings.TrimSpace(a.flags["attachment"]); attachment != "" {
			staged, err := stageReplyAttachment(attachment)
			if err != nil {
				fail(err)
			}
			content["attachments"] = []map[string]any{staged}
		}
		if body == "" && content["attachments"] == nil {
			usage("inbox reply <inbox-item-id> --agent <agent> [body|--body TEXT] [--attachment PATH]")
		}
		resp, err := api("POST", "/api/inbox/"+url.PathEscape(a.positional[1])+"/reply", map[string]any{
			"agent": a.flags["agent"], "content": content,
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

func stageReplyAttachment(source string) (map[string]any, error) {
	return stageOutboundAttachment(source)
}

func stageOutboundAttachment(source string) (map[string]any, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil {
		return nil, fmt.Errorf("resolve attachment: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("attachment is not a regular file: %s", absolute)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(absolute)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if info.Size() <= 0 || info.Size() > 25<<20 {
		return nil, fmt.Errorf("attachment must be between 1 byte and 25 MB")
	}
	dataDir := strings.TrimSpace(os.Getenv("CODEX_LOOM_DATA"))
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve Loom data directory: %w", err)
		}
		dataDir = filepath.Join(home, ".codex-loom")
	}
	spoolDir := filepath.Join(dataDir, "attachments", "outbound")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return nil, fmt.Errorf("create attachment spool: %w", err)
	}
	src, err := os.Open(absolute)
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp(spoolDir, ".staging-*")
	if err != nil {
		return nil, fmt.Errorf("stage attachment: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		_ = tmp.Close()
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("secure attachment snapshot: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), src); err != nil {
		return nil, fmt.Errorf("copy attachment: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close attachment snapshot: %w", err)
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))[:16]
	destination := filepath.Join(spoolDir, digest+"-"+filepath.Base(absolute))
	if err := os.Rename(tmpPath, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr != nil {
			return nil, fmt.Errorf("publish attachment snapshot: %w", err)
		}
	}
	keepTemp = false
	return map[string]any{
		"id": "art_" + digest, "name": filepath.Base(absolute), "path": destination,
		"mimeType": mimeType, "size": info.Size(), "sha256": fmt.Sprintf("%x", hash.Sum(nil)),
	}, nil
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
			usage("outbox send <agent> <address-id> <conversation-id> [body|--body TEXT] [--attachment PATH] [--thread ID] [--message-id ID] [--expectation none|optional|required] [--idempotency-key KEY]")
		}
		body, err := readMsgBody(a, a.positional[4:])
		if err != nil {
			fail(err)
		}
		content := map[string]any{"text": body}
		if attachment := strings.TrimSpace(a.flags["attachment"]); attachment != "" {
			staged, err := stageReplyAttachment(attachment)
			if err != nil {
				fail(err)
			}
			content["attachments"] = []map[string]any{staged}
		}
		if strings.TrimSpace(body) == "" && content["attachments"] == nil {
			usage("outbox send <agent> <address-id> <conversation-id> [body|--body TEXT] [--attachment PATH]")
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
			"content": content, "responseExpectation": expectation,
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
		usage("integration list|send|connect|import|bind|update-address|enable|disable|status ...")
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
			archived := str(connection, "archivedAt") != ""
			state := str(connection, "status")
			switch {
			case archived:
				state = dim("archived → " + str(connection, "supersededBy"))
			case state == "connected":
				state = green(state)
			case state == "degraded":
				state = yellow(state)
			default:
				state = red(state)
			}
			fmt.Printf("%s %s  %s  %s\n", bold(str(connection, "id")), cyan(str(connection, "provider")), state, dim(str(connection, "accountRef")))
			for _, address := range addressesByConnection[str(connection, "id")] {
				addressState := enabledState(address)
				if str(address, "archivedAt") != "" {
					addressState = dim("archived → " + str(address, "supersededBy"))
				}
				fmt.Printf("  %s %s → %s  %s trigger=%s reply=%s dm=%s trust=%s\n", dim(str(address, "id")), str(address, "externalIdentity"), str(address, "agentId"), addressState, str(address, "triggerPolicy"), str(address, "replyPolicy"), str(address, "dmPolicy"), str(address, "trustDomain"))
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
	case "send":
		cmdIntegrationSend(a)
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
	case "import":
		if len(a.positional) < 2 || a.positional[1] != "parall" {
			usage("integration import parall --agent AGENT --org-id ORG --external-agent-id USER [--agent-key-file PATH] [--api-url URL] [--trust-domain NAME]")
		}
		if strings.TrimSpace(a.flags["agent-key-file"]) != "" {
			if err := requireSecureSecretTransport(base); err != nil {
				fail(err)
			}
		}
		body, err := parallImportRequest(a)
		if err != nil {
			fail(err)
		}
		resp, err := api("POST", "/api/integrations/providers/parall/import", body)
		body["agentApiKey"] = ""
		if err != nil {
			fail(err)
		}
		connection, _ := resp["connection"].(map[string]any)
		address, _ := resp["address"].(map[string]any)
		gateway, _ := resp["gateway"].(map[string]any)
		fmt.Printf("%s %s → %s\n", green("imported"), str(address, "externalIdentity"), a.flags["agent"])
		fmt.Printf("  connection: %s\n", str(connection, "id"))
		fmt.Printf("  address: %s\n", str(address, "id"))
		fmt.Printf("  gateway: %s (%s)\n", str(gateway, "service"), str(gateway, "manager"))
		if consolidation, ok := resp["consolidation"].(map[string]any); ok {
			fmt.Printf("  archived duplicates: %d connections, %d addresses, %d memberships\n", len(anySlice(consolidation["archivedConnectionIds"])), len(anySlice(consolidation["archivedAddressIds"])), len(anySlice(consolidation["archivedMembershipIds"])))
		}
		if warning := str(resp, "retirementWarning"); warning != "" {
			fmt.Printf("  %s %s\n", yellow("gateway cleanup warning:"), warning)
		}
		if strings.TrimSpace(a.flags["agent-key-file"]) != "" {
			fmt.Println(dim("  Agent key stored in Keychain; source key file was not modified."))
		} else {
			fmt.Println(dim("  Existing Keychain credential reused; no secret was read from a file."))
		}
	case "bind":
		if len(a.positional) < 3 || strings.TrimSpace(a.flags["identity"]) == "" {
			usage("integration bind <agent> <connection-id> --identity EXTERNAL_ID [--display-name NAME] [--trigger mention] [--reply-policy final_answer] [--dm-policy managed] [--trust-domain NAME] [--enabled true|false] [allow/block flags]")
		}
		body := map[string]any{
			"connectionId": a.positional[2], "externalIdentity": a.flags["identity"],
			"displayName": a.flags["display-name"], "triggerPolicy": a.flags["trigger"],
			"replyPolicy": a.flags["reply-policy"], "dmPolicy": a.flags["dm-policy"], "trustDomain": a.flags["trust-domain"],
			"allowActors": csvValues(a.flags["allow-actors"]), "allowConversations": csvValues(a.flags["allow-conversations"]),
			"blockActors": csvValues(a.flags["block-actors"]), "blockConversations": csvValues(a.flags["block-conversations"]),
		}
		if err := addBoolFlag(body, a.flags, "enabled", "enabled"); err != nil {
			fail(err)
		}
		resp, err := api("POST", "/api/agents/"+url.PathEscape(a.positional[1])+"/addresses", body)
		if err != nil {
			fail(err)
		}
		address, _ := resp["address"].(map[string]any)
		fmt.Printf("%s %s → %s (%s, %s)\n", green("bound"), str(address, "externalIdentity"), a.positional[1], str(address, "id"), enabledState(address))
	case "update-address":
		if len(a.positional) < 2 {
			usage("integration update-address <address-id> [policy flags]")
		}
		body := map[string]any{}
		for flag, field := range map[string]string{
			"identity": "externalIdentity", "display-name": "displayName", "trigger": "triggerPolicy",
			"reply-policy": "replyPolicy", "dm-policy": "dmPolicy", "trust-domain": "trustDomain",
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
		if err := addBoolFlag(body, a.flags, "enabled", "enabled"); err != nil {
			fail(err)
		}
		if len(body) == 0 {
			usage("integration update-address <address-id> [--display-name NAME] [--trigger POLICY] [--reply-policy POLICY] [--dm-policy open|managed|closed] [--trust-domain NAME] [--enabled true|false] [allow/block flags]")
		}
		resp, err := api("PATCH", "/api/integrations/addresses/"+url.PathEscape(a.positional[1]), body)
		if err != nil {
			fail(err)
		}
		address, _ := resp["address"].(map[string]any)
		fmt.Printf("%s %s  %s trigger=%s reply=%s dm=%s\n", green("updated"), str(address, "id"), enabledState(address), str(address, "triggerPolicy"), str(address, "replyPolicy"), str(address, "dmPolicy"))
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
		usage("integration list|send|connect|import|bind|update-address|enable|disable|status ...")
	}
}

func cmdIntegrationSend(a args) {
	agent := strings.TrimSpace(a.flags["from"])
	replyTo := strings.TrimSpace(a.flags["reply-to"])
	membershipID := strings.TrimSpace(a.flags["to"])
	messageID := strings.TrimSpace(a.flags["message-id"])
	threadID := strings.TrimSpace(a.flags["thread-id"])
	if agent == "" || (replyTo == "") == (membershipID == "") {
		usage("integration send --from AGENT (--reply-to INBOX_ID|--to MEMBERSHIP_ID) [--message-id PROVIDER_MESSAGE_ID] [--thread-id PROVIDER_THREAD_ID] [--body TEXT|--body-file PATH] [--file PATH ...] [--expect-reply none|optional|required] [--idempotency-key KEY] [--async]")
	}
	if replyTo != "" && (messageID != "" || threadID != "") {
		fail(fmt.Errorf("--message-id and --thread-id are only valid with --to MEMBERSHIP_ID"))
	}
	if membershipID != "" && strings.TrimSpace(a.flags["idempotency-key"]) == "" {
		fail(fmt.Errorf("--idempotency-key is required when sending proactively to a Membership"))
	}
	body, err := readMsgBody(a, a.positional[1:])
	if err != nil {
		fail(err)
	}
	attachments := make([]map[string]any, 0)
	paths := append([]string{}, a.flagValues["file"]...)
	paths = append(paths, a.flagValues["attachment"]...)
	for _, source := range paths {
		staged, err := stageOutboundAttachment(source)
		if err != nil {
			fail(err)
		}
		attachments = append(attachments, staged)
	}
	if strings.TrimSpace(body) == "" && len(attachments) == 0 {
		fail(fmt.Errorf("message body or at least one --file is required"))
	}
	content := map[string]any{"text": body}
	if len(attachments) > 0 {
		content["attachments"] = attachments
	}
	request := map[string]any{
		"agent": agent, "inboxItemId": replyTo, "membershipId": membershipID,
		"content": content, "responseExpectation": a.flags["expect-reply"],
		"idempotencyKey": a.flags["idempotency-key"],
	}
	if messageID != "" || threadID != "" {
		request["replyTarget"] = map[string]any{"messageId": messageID, "threadId": threadID}
	}
	resp, err := api("POST", "/api/integrations/send", request)
	if err != nil {
		fail(err)
	}
	item, _ := resp["outboxItem"].(map[string]any)
	if a.flags["async"] == "true" {
		fmt.Printf("%s %s  %s\n", green("delivery queued"), str(item, "id"), dim(str(item, "state")))
		return
	}
	timeout := 30 * time.Second
	if value := strings.TrimSpace(a.flags["timeout"]); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 {
			fail(fmt.Errorf("--timeout must be a positive number of seconds"))
		}
		timeout = time.Duration(seconds) * time.Second
	}
	item, err = waitForOutbox(str(item, "id"), timeout)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s %s", green("delivered"), str(item, "id"))
	if ids := stringValues(item["externalMessageIds"]); len(ids) > 0 {
		fmt.Printf("  %s", dim(strings.Join(ids, ", ")))
	} else if id := str(item, "externalMessageId"); id != "" {
		fmt.Printf("  %s", dim(id))
	}
	fmt.Println()
	for _, value := range anySlice(item["deliveryReceipts"]) {
		receipt, _ := value.(map[string]any)
		if str(receipt, "kind") != "attachment" {
			continue
		}
		fmt.Printf("  %s %s  %s\n", dim("attachment"), str(receipt, "artifactId"), dim(str(receipt, "externalAttachmentId")))
	}
}

func waitForOutbox(id string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := api("GET", "/api/outbox/"+url.PathEscape(id), nil)
		if err != nil {
			return nil, err
		}
		item, _ := resp["outboxItem"].(map[string]any)
		switch str(item, "state") {
		case "sent":
			return item, nil
		case "failed":
			return nil, fmt.Errorf("delivery %s failed: %s", id, str(item, "lastError"))
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("delivery %s is still %s after %s; inspect it with %s outbox", id, str(item, "state"), timeout, commandName)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

type prllOperationRequest struct {
	Resource  string
	Action    string
	Arguments map[string]any
}

func cmdPrll(a args) {
	addressID := strings.TrimSpace(a.flags["address"])
	if addressID == "" {
		usage("prll chats|messages ... --address ADDRESS_ID")
	}
	request, err := parsePrllOperation(a)
	if err != nil {
		fail(err)
	}
	resp, err := api("POST", "/api/integrations/providers/parall/operations", map[string]any{
		"addressId": addressID, "resource": request.Resource,
		"action": request.Action, "arguments": request.Arguments,
	})
	if err != nil {
		fail(err)
	}
	operation, _ := resp["operation"].(map[string]any)
	timeout, err := positiveDurationFlag(a.flags["timeout"], 30*time.Second)
	if err != nil {
		fail(err)
	}
	operation, err = waitForProviderOperation(str(operation, "id"), timeout, "Parall")
	if err != nil {
		fail(err)
	}
	encoded, err := json.MarshalIndent(operation["result"], "", "  ")
	if err != nil {
		fail(fmt.Errorf("format Parall response: %w", err))
	}
	fmt.Println(string(encoded))
}

func cmdLark(a args) {
	addressID := strings.TrimSpace(a.flags["address"])
	if addressID == "" {
		usage("lark messages ... --address ADDRESS_ID")
	}
	request, err := parseLarkOperation(a)
	if err != nil {
		fail(err)
	}
	resp, err := api("POST", "/api/integrations/providers/lark/operations", map[string]any{
		"addressId": addressID, "resource": request.Resource,
		"action": request.Action, "arguments": request.Arguments,
	})
	if err != nil {
		fail(err)
	}
	operation, _ := resp["operation"].(map[string]any)
	timeout, err := positiveDurationFlag(a.flags["timeout"], 30*time.Second)
	if err != nil {
		fail(err)
	}
	operation, err = waitForProviderOperation(str(operation, "id"), timeout, "Lark")
	if err != nil {
		fail(err)
	}
	encoded, err := json.MarshalIndent(operation["result"], "", "  ")
	if err != nil {
		fail(fmt.Errorf("format Lark response: %w", err))
	}
	fmt.Println(string(encoded))
}

func parseLarkOperation(a args) (prllOperationRequest, error) {
	if len(a.positional) < 2 {
		return prllOperationRequest{}, fmt.Errorf("usage: %s lark messages list|get|replies ... --address ADDRESS_ID", commandName)
	}
	resource, action := strings.ToLower(a.positional[0]), strings.ToLower(a.positional[1])
	request := prllOperationRequest{Resource: resource, Action: action, Arguments: map[string]any{}}
	allowed := map[string]bool{"address": true, "timeout": true}
	requirePositionals := func(count int, usageText string) error {
		if len(a.positional) != count {
			return fmt.Errorf("usage: %s lark %s --address ADDRESS_ID", commandName, usageText)
		}
		return nil
	}
	addFlag := func(name, argument string) {
		allowed[name] = true
		if value, exists := a.flags[name]; exists {
			request.Arguments[argument] = value
		}
	}

	switch resource + "/" + action {
	case "messages/list":
		if err := requirePositionals(3, "messages list CHAT_ID"); err != nil {
			return request, err
		}
		request.Arguments["chatId"] = strings.TrimSpace(a.positional[2])
		for _, pair := range [][2]string{{"page-token", "pageToken"}, {"start-time", "startTime"}, {"end-time", "endTime"}, {"thread-id", "threadId"}} {
			addFlag(pair[0], pair[1])
		}
		allowed["limit"] = true
		allowed["sort"] = true
		allowed["thread-root-only"] = true
		if value, exists := a.flags["thread-root-only"]; exists {
			if value != "true" {
				return request, fmt.Errorf("--thread-root-only does not take a value")
			}
			request.Arguments["threadRootOnly"] = true
		}
	case "messages/get", "messages/replies":
		if err := requirePositionals(3, "messages "+action+" MESSAGE_ID --chat-id CHAT_ID"); err != nil {
			return request, err
		}
		request.Arguments["messageId"] = strings.TrimSpace(a.positional[2])
		addFlag("chat-id", "chatId")
		if action == "replies" {
			allowed["limit"] = true
		}
	default:
		return request, fmt.Errorf("unsupported Lark command: %s %s", resource, action)
	}

	for name := range a.flags {
		if !allowed[name] {
			return request, fmt.Errorf("unsupported flag for %s %s: --%s", resource, action, name)
		}
	}
	chatID, _ := request.Arguments["chatId"].(string)
	if strings.TrimSpace(chatID) == "" {
		return request, fmt.Errorf("--chat-id is required for Lark messages %s", action)
	}
	if value, exists := a.flags["limit"]; exists {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 50 {
			return request, fmt.Errorf("--limit must be an integer from 1 to 50 for Lark message reads")
		}
		request.Arguments["limit"] = limit
	}
	if value, exists := a.flags["sort"]; exists {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "asc", "bycreatetimeasc":
			request.Arguments["sort"] = "ByCreateTimeAsc"
		case "desc", "bycreatetimedesc":
			request.Arguments["sort"] = "ByCreateTimeDesc"
		default:
			return request, fmt.Errorf("--sort must be asc, desc, ByCreateTimeAsc, or ByCreateTimeDesc")
		}
	}
	return request, nil
}

func parsePrllOperation(a args) (prllOperationRequest, error) {
	if len(a.positional) < 2 {
		return prllOperationRequest{}, fmt.Errorf("usage: %s prll chats|messages ... --address ADDRESS_ID", commandName)
	}
	resource, action := a.positional[0], a.positional[1]
	request := prllOperationRequest{Resource: resource, Action: action, Arguments: map[string]any{}}
	allowed := map[string]bool{"address": true, "timeout": true}
	addFlag := func(name, argument string) {
		allowed[name] = true
		if value, exists := a.flags[name]; exists {
			request.Arguments[argument] = value
		}
	}
	requirePositionals := func(count int, usageText string) error {
		if len(a.positional) != count {
			return fmt.Errorf("usage: %s prll %s --address ADDRESS_ID", commandName, usageText)
		}
		return nil
	}

	switch resource + "/" + action {
	case "chats/list":
		if err := requirePositionals(2, "chats list"); err != nil {
			return request, err
		}
		addFlag("limit", "limit")
		addFlag("cursor", "cursor")
	case "chats/get":
		if err := requirePositionals(3, "chats get CHAT_ID"); err != nil {
			return request, err
		}
		request.Arguments["chatId"] = a.positional[2]
	case "chats/discoverable":
		if err := requirePositionals(2, "chats discoverable"); err != nil {
			return request, err
		}
		addFlag("query", "query")
		addFlag("limit", "limit")
	case "chats/members":
		if len(a.positional) != 4 || a.positional[2] != "list" {
			return request, fmt.Errorf("usage: %s prll chats members list CHAT_ID --address ADDRESS_ID", commandName)
		}
		request.Action = "members-list"
		request.Arguments["chatId"] = a.positional[3]
	case "messages/list":
		if err := requirePositionals(3, "messages list CHAT_ID"); err != nil {
			return request, err
		}
		request.Arguments["chatId"] = a.positional[2]
		for _, pair := range [][2]string{{"limit", "limit"}, {"before", "before"}, {"after", "after"}, {"since", "since"}, {"thread-root-id", "threadRootId"}} {
			addFlag(pair[0], pair[1])
		}
		allowed["top-level"] = true
		if value, exists := a.flags["top-level"]; exists {
			if value != "true" {
				return request, fmt.Errorf("--top-level does not take a value")
			}
			request.Arguments["topLevel"] = true
		}
	case "messages/get":
		if err := requirePositionals(3, "messages get MESSAGE_ID"); err != nil {
			return request, err
		}
		request.Arguments["messageId"] = a.positional[2]
	case "messages/replies":
		if err := requirePositionals(3, "messages replies MESSAGE_ID"); err != nil {
			return request, err
		}
		request.Arguments["messageId"] = a.positional[2]
		for _, pair := range [][2]string{{"limit", "limit"}, {"before", "before"}, {"after", "after"}} {
			addFlag(pair[0], pair[1])
		}
	default:
		return request, fmt.Errorf("unsupported Parall command: %s %s", resource, action)
	}
	for name := range a.flags {
		if !allowed[name] {
			return request, fmt.Errorf("unsupported flag for %s %s: --%s", resource, action, name)
		}
	}
	if value, exists := a.flags["limit"]; exists {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return request, fmt.Errorf("--limit must be an integer from 1 to 100")
		}
	}
	return request, nil
}

func waitForProviderOperation(id string, timeout time.Duration, providerLabel string) (map[string]any, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("CodexLoom created no provider operation")
	}
	if strings.TrimSpace(providerLabel) == "" {
		providerLabel = "Provider"
	}
	deadline := time.Now().Add(timeout)
	for {
		resp, err := api("GET", "/api/integrations/provider-operations/"+url.PathEscape(id), nil)
		if err != nil {
			return nil, err
		}
		operation, _ := resp["operation"].(map[string]any)
		switch str(operation, "state") {
		case "succeeded":
			return operation, nil
		case "failed":
			return nil, fmt.Errorf("%s operation %s failed: %s", providerLabel, id, str(operation, "lastError"))
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s operation %s is still %s after %s; verify the managed gateway is connected", providerLabel, id, str(operation, "state"), timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func positiveDurationFlag(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive number of seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

func parallImportRequest(a args) (map[string]any, error) {
	required := []string{"agent", "org-id", "external-agent-id"}
	for _, name := range required {
		if strings.TrimSpace(a.flags[name]) == "" {
			return nil, fmt.Errorf("--%s is required", name)
		}
	}
	key := ""
	if path := strings.TrimSpace(a.flags["agent-key-file"]); path != "" {
		var err error
		key, err = readOwnerOnlySecretFile(path)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"agent": a.flags["agent"], "apiUrl": a.flags["api-url"], "orgId": a.flags["org-id"],
		"externalAgentId": a.flags["external-agent-id"], "agentApiKey": key, "trustDomain": a.flags["trust-domain"],
	}, nil
}

func requireSecureSecretTransport(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("invalid CodexLoom URL for credential import")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to send an Agent key over non-loopback %s; run the import on the Loom host or use HTTPS", parsed.Scheme)
}

func readOwnerOnlySecretFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read Agent key file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Agent key file must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 {
		return "", fmt.Errorf("Agent key file permissions must be owner-only (0600 or 0400), got %04o", info.Mode().Perm())
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return "", fmt.Errorf("Agent key file must be owned by the current user")
	}
	if info.Size() <= 0 || info.Size() > 64*1024 {
		return "", fmt.Errorf("Agent key file must contain between 1 byte and 64 KiB")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Agent key file: %w", err)
	}
	key := strings.TrimSpace(string(payload))
	if key == "" {
		return "", fmt.Errorf("Agent key file is empty")
	}
	return key, nil
}

func cmdConversation(a args) {
	if len(a.positional) == 0 {
		usage("conversation list|discover|get|set|enable|disable ...")
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
	case "discover":
		values := url.Values{}
		if len(a.positional) > 1 {
			values.Set("agent", a.positional[1])
		}
		if address := strings.TrimSpace(a.flags["address"]); address != "" {
			values.Set("address", address)
		}
		if _, includeUnavailable := a.flags["all"]; includeUnavailable {
			values.Set("available", "all")
		}
		path := "/api/integrations/conversation-candidates"
		if encoded := values.Encode(); encoded != "" {
			path += "?" + encoded
		}
		resp, err := api("GET", path, nil)
		if err != nil {
			fail(err)
		}
		candidates := anySlice(resp["candidates"])
		if len(candidates) == 0 {
			fmt.Println("no discovered conversations")
			return
		}
		for _, value := range candidates {
			candidate, _ := value.(map[string]any)
			fmt.Print(formatConversationCandidate(candidate))
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
			"type": "conversationType", "actor": "actorId",
			"name": "displayName", "purpose": "purpose", "role": "role", "guidance": "guidance",
			"trigger": "triggerPolicy", "reply-policy": "replyPolicy", "outbound-policy": "outboundPolicy", "trust-domain": "trustDomain",
		} {
			if value, ok := a.flags[flag]; ok {
				body[field] = value
			}
		}
		if err := addBoolFlag(body, a.flags, "enabled", "enabled"); err != nil {
			fail(err)
		}
		if err := addIntFlag(body, a.flags, "expected-version", "expectedVersion"); err != nil {
			fail(err)
		}
		if len(body) == 0 {
			usage("conversation set <address-id> <conversation-id> [--file membership.json] [--type group|dm] [--actor EXTERNAL_ID] [--name NAME] [--purpose TEXT] [--role TEXT] [--guidance TEXT] [--trigger POLICY] [--reply-policy POLICY] [--outbound-policy reply_only|proactive|none] [--enabled true|false] [--expected-version N]")
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
		usage("conversation list|discover|get|set|enable|disable ...")
	}
}

func printConversationMembership(value any) {
	membership, _ := value.(map[string]any)
	fmt.Print(formatConversationMembership(membership))
}

func formatConversationMembership(membership map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  %s  v%.0f  %s\n", bold(str(membership, "id")), str(membership, "conversationId"), enabledState(membership), num(membership, "version"), dim(str(membership, "addressId")))
	if name := str(membership, "displayName"); name != "" {
		fmt.Fprintf(&b, "  name: %s\n", name)
	}
	if conversationType := str(membership, "conversationType"); conversationType != "" {
		fmt.Fprintf(&b, "  conversation: type=%s", conversationType)
		if actorID := str(membership, "actorId"); actorID != "" {
			fmt.Fprintf(&b, " actor=%s", actorID)
		}
		b.WriteByte('\n')
	}
	for _, field := range []string{"purpose", "role", "guidance"} {
		if value := str(membership, field); value != "" {
			fmt.Fprintf(&b, "  %s:\n    %s\n", field, strings.ReplaceAll(value, "\n", "\n    "))
		}
	}
	outbound := str(membership, "outboundPolicy")
	if outbound == "" {
		outbound = "reply_only"
	}
	fmt.Fprintf(&b, "  policy: trigger=%s reply=%s outbound=%s trust=%s\n", str(membership, "triggerPolicy"), str(membership, "replyPolicy"), outbound, str(membership, "trustDomain"))
	return b.String()
}

func formatConversationCandidate(candidate map[string]any) string {
	state := "unavailable"
	if enabled, _ := candidate["available"].(bool); enabled {
		state = "joined"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  %s  %s\n", bold(str(candidate, "id")), str(candidate, "conversationId"), green(state), dim(str(candidate, "addressId")))
	if name := str(candidate, "displayName"); name != "" {
		fmt.Fprintf(&b, "  name: %s\n", name)
	}
	if description := str(candidate, "description"); description != "" {
		fmt.Fprintf(&b, "  description: %s\n", description)
	}
	fmt.Fprintf(&b, "  conversation: type=%s  observed=%s\n", str(candidate, "conversationType"), str(candidate, "lastSeenAt"))
	return b.String()
}

func enabledState(value map[string]any) string {
	if enabled, ok := value["enabled"].(bool); ok && !enabled {
		return "disabled"
	}
	return "enabled"
}

func addBoolFlag(body map[string]any, flags map[string]string, flag, field string) error {
	value, ok := flags[flag]
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("--%s must be true or false", flag)
	}
	body[field] = parsed
	return nil
}

func addIntFlag(body map[string]any, flags map[string]string, flag, field string) error {
	value, ok := flags[flag]
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return fmt.Errorf("--%s must be a non-negative integer", flag)
	}
	body[field] = parsed
	return nil
}
