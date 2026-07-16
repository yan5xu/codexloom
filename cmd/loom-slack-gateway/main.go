// loom-slack-gateway keeps Slack tokens in the operating system credential
// store while running the JavaScript Socket Mode adapter.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	appID := flag.String("app-id", os.Getenv("SLACK_APP_ID"), "Slack App ID")
	teamID := flag.String("team-id", os.Getenv("SLACK_TEAM_ID"), "Slack Workspace ID")
	botUserID := flag.String("bot-user-id", os.Getenv("SLACK_BOT_USER_ID"), "Slack Bot User ID")
	node := flag.String("node", "", "Node.js executable")
	script := flag.String("script", "", "Slack gateway script")
	stateFile := flag.String("state-file", "", "gateway state file")
	flag.Parse()

	tokens, err := loomslack.LoadTokens(*appID, *teamID)
	if err != nil {
		fatalf("read Slack tokens from keychain: %v", err)
	}
	if tokens.Bot == "" || tokens.App == "" {
		fatalf("Slack credentials are missing for App %s", *appID)
	}
	if *node == "" {
		*node, err = exec.LookPath("node")
		if err != nil {
			fatalf("find Node.js: %v", err)
		}
	}
	if *script == "" {
		*script, err = findScript()
		if err != nil {
			fatalf("find Slack gateway: %v", err)
		}
	}
	arguments := []string{
		*node, *script, "--hub", *hubURL, "--connection", *connectionID,
		"--address", *addressID, "--bot-user-id", *botUserID, "--team-id", *teamID,
	}
	if *stateFile != "" {
		arguments = append(arguments, "--state-file", *stateFile)
	}
	environment := append(os.Environ(), "SLACK_BOT_TOKEN="+tokens.Bot, "SLACK_APP_TOKEN="+tokens.App)
	if err := syscall.Exec(*node, arguments, environment); err != nil {
		fatalf("start Slack gateway: %v", err)
	}
}

func findScript() (string, error) {
	current, err := os.Executable()
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{
		filepath.Join(filepath.Dir(filepath.Dir(current)), "gateway", "slack.mjs"),
		filepath.Join("gateway", "slack.mjs"),
	} {
		path, err := filepath.Abs(candidate)
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("gateway/slack.mjs not found")
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func fatalf(format string, values ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
