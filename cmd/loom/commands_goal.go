package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func cmdGoal(a args) {
	if len(a.positional) < 1 {
		usage("goal <agent> [show|set|pause|resume|clear]")
	}
	agent := a.positional[0]
	action := "show"
	if len(a.positional) > 1 {
		action = a.positional[1]
	}
	path := "/api/agents/" + url.PathEscape(agent) + "/goal"

	switch action {
	case "show", "get", "status":
		response, err := api("GET", path, nil)
		if err != nil {
			fail(err)
		}
		goal, _ := response["goal"].(map[string]any)
		fmt.Print(formatGoal(goal))
	case "set", "edit":
		objective := strings.TrimSpace(a.flags["objective"])
		if objective == "" && len(a.positional) > 2 {
			objective = strings.TrimSpace(strings.Join(a.positional[2:], " "))
		}
		if objective == "" {
			usage("goal <agent> set <objective> [--token-budget N|--clear-token-budget]")
		}
		body := map[string]any{"objective": objective}
		if value := strings.TrimSpace(a.flags["token-budget"]); value != "" {
			budget, err := strconv.ParseInt(value, 10, 64)
			if err != nil || budget <= 0 {
				fail(fmt.Errorf("token budget must be a positive integer"))
			}
			body["tokenBudget"] = budget
		}
		if a.flags["clear-token-budget"] == "true" {
			body["clearTokenBudget"] = true
		}
		response, err := api("PUT", path, body)
		if err != nil {
			fail(err)
		}
		goal, _ := response["goal"].(map[string]any)
		fmt.Print(formatGoal(goal))
	case "pause", "resume":
		status := "paused"
		if action == "resume" {
			status = "active"
		}
		response, err := api("PUT", path, map[string]any{"status": status})
		if err != nil {
			fail(err)
		}
		goal, _ := response["goal"].(map[string]any)
		fmt.Print(formatGoal(goal))
	case "clear":
		response, err := api("DELETE", path, nil)
		if err != nil {
			fail(err)
		}
		if response["cleared"] == true {
			fmt.Printf("%s %s\n", green("cleared"), agent)
		} else {
			fmt.Printf("%s %s\n", dim("no Goal to clear for"), agent)
		}
	default:
		usage("goal <agent> [show|set|pause|resume|clear]")
	}
}

func formatGoal(goal map[string]any) string {
	if goal == nil || str(goal, "objective") == "" {
		return "no Goal\n"
	}
	status := str(goal, "status")
	statusLabel := status
	switch status {
	case "active":
		statusLabel = green(status)
	case "paused", "blocked", "usageLimited", "budgetLimited":
		statusLabel = yellow(status)
	case "complete":
		statusLabel = dim(status)
	}
	used := int64(num(goal, "tokensUsed"))
	budget := int64(num(goal, "tokenBudget"))
	tokenSummary := formatGoalNumber(used)
	if budget > 0 {
		tokenSummary += " / " + formatGoalNumber(budget)
	}
	duration := time.Duration(int64(num(goal, "timeUsedSeconds"))) * time.Second
	return fmt.Sprintf("Goal %s\nobjective:\n%s\nusage: %s tokens · %s\n",
		statusLabel, "  "+strings.ReplaceAll(str(goal, "objective"), "\n", "\n  "), tokenSummary, duration.Round(time.Second))
}

func formatGoalNumber(value int64) string {
	if value >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(value)/1_000)
	}
	return strconv.FormatInt(value, 10)
}
