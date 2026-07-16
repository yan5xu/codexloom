package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

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
	if subcommand := msgSubcommand(a); subcommand != "" {
		switch subcommand {
		case "status":
			cmdMsgStatus(a)
			return
		case "wait":
			cmdMsgWait(a)
			return
		case "retry":
			cmdMsgRetry(a)
			return
		case "cancel":
			cmdMsgCancel(a)
			return
		case "resolve":
			cmdMsgResolve(a)
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

func msgSubcommand(a args) string {
	if len(a.positional) == 0 {
		return ""
	}
	if a.positional[0] == "resolve" {
		return "resolve"
	}
	if a.flags["from"] != "" || a.flags["reply-to"] != "" {
		return ""
	}
	switch a.positional[0] {
	case "status", "wait", "retry", "cancel":
		return a.positional[0]
	default:
		return ""
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

func cmdMsgRetry(a args) {
	if len(a.positional) < 2 {
		usage("msg retry <message-id>")
	}
	resp, err := api("POST", "/api/comms/messages/"+url.PathEscape(a.positional[1])+"/retry", map[string]any{})
	if err != nil {
		fail(err)
	}
	msg, _ := resp["message"].(map[string]any)
	printMessageDelivery(msg)
}

func cmdMsgResolve(a args) {
	if len(a.positional) < 2 {
		usage("msg resolve <message-id> --from <sender> --resolution completed_elsewhere|superseded --reason <text>")
	}
	from := strings.TrimSpace(a.flags["from"])
	resolution := strings.TrimSpace(a.flags["resolution"])
	reason := strings.TrimSpace(a.flags["reason"])
	if from == "" || reason == "" || (resolution != "completed_elsewhere" && resolution != "superseded") {
		usage("msg resolve <message-id> --from <sender> --resolution completed_elsewhere|superseded --reason <text>")
	}
	resp, err := api("POST", "/api/comms/messages/"+url.PathEscape(a.positional[1])+"/resolve", map[string]any{
		"from": from, "resolution": resolution, "reason": reason,
	})
	if err != nil {
		fail(err)
	}
	msg, _ := resp["message"].(map[string]any)
	fmt.Printf("%s %s — %s\n", green("resolved"), str(msg, "id"), resolution)
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
		if handling := str(msg, "handlingStatus"); handling == "interrupted" || handling == "failed" {
			reason := str(msg, "lastHandlingError")
			if reason == "" {
				reason = "handling " + handling
			}
			fmt.Printf("%s %s %s %s — %s\n", yellow("held"), id, dim("by"), bold(toName), reason)
			return
		}
		turnID := str(msg, "deliveredTurnId")
		if turnID == "" {
			turnID = "(unknown)"
		}
		turnKind := "new turn"
		if str(msg, "deliveryMode") == "turn_steer" {
			turnKind = "active turn"
		}
		fmt.Printf("%s %s %s %s — %s %s\n", green("delivered"), id, dim("to"), bold(toName), turnKind, turnID)
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
	if str(msg, "resolution") != "" {
		fmt.Printf("resolution: %s\n", str(msg, "resolution"))
	}
	if str(msg, "resolutionReason") != "" {
		fmt.Printf("resolution-reason: %s\n", str(msg, "resolutionReason"))
	}
	if str(msg, "resolvedBy") != "" {
		fmt.Printf("resolved-by: %s\n", str(msg, "resolvedBy"))
	}
	if str(msg, "replyTo") != "" {
		fmt.Printf("reply-to: %s\n", str(msg, "replyTo"))
	}
	if str(msg, "sourceTurnId") != "" {
		fmt.Printf("source-turn: %s\n", str(msg, "sourceTurnId"))
	}
	if str(msg, "deliveryMode") != "" {
		fmt.Printf("delivery-mode: %s\n", str(msg, "deliveryMode"))
	}
	if str(msg, "handlingStatus") != "" {
		fmt.Printf("handling: %s\n", str(msg, "handlingStatus"))
	}
	if str(msg, "lastHandlingError") != "" {
		fmt.Printf("handling-error: %s\n", str(msg, "lastHandlingError"))
	}
	if attempts, ok := msg["handlingAttempts"].([]any); ok && len(attempts) > 0 {
		fmt.Printf("handling-attempts: %d\n", len(attempts))
	}
}

func readMsgBody(a args, bodyArgs []string) (string, error) {
	if body := a.flags["body"]; body != "" {
		return body, nil
	}
	if path := a.flags["body-file"]; path != "" {
		var data []byte
		var err error
		if path == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(path)
		}
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
