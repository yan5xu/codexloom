package main

import (
	"fmt"
	"net/url"
)

func cmdCompact(a args) {
	if len(a.positional) < 1 {
		usage("compact <agent>")
	}
	agent := a.positional[0]
	response, err := api("POST", "/api/agents/"+url.PathEscape(agent)+"/compact", nil)
	if err != nil {
		fail(err)
	}
	compaction, _ := response["compaction"].(map[string]any)
	fmt.Printf("%s %s (%s)\n", green("compaction started"), str(compaction, "agentName"), str(compaction, "threadId"))
}
