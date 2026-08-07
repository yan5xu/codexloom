// loom-slack-gateway resolves a managed or legacy Slack reference and passes
// tokens to the JavaScript Socket Mode adapter through an anonymous FD.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/credentialpipe"
	"github.com/yan5xu/codex-loom/internal/credentialstore"
	loomslack "github.com/yan5xu/codex-loom/internal/slack"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	appID := flag.String("app-id", os.Getenv("SLACK_APP_ID"), "Slack App ID")
	teamID := flag.String("team-id", os.Getenv("SLACK_TEAM_ID"), "Slack Workspace ID")
	botUserID := flag.String("bot-user-id", os.Getenv("SLACK_BOT_USER_ID"), "Slack Bot User ID")
	credentialRef := flag.String("credential-ref", os.Getenv("CODEX_LOOM_CREDENTIAL_REF"), "opaque credential reference")
	node := flag.String("node", "", "Node.js executable")
	script := flag.String("script", "", "Slack gateway script")
	stateFile := flag.String("state-file", "", "gateway state file")
	generation := flag.String("generation", envFirst("CODEX_LOOM_GATEWAY_GENERATION"), "non-secret managed gateway generation")
	flag.Parse()
	evidence, err := buildinfo.CurrentExecutableEvidence()
	if err != nil {
		fatalf("observe Slack gateway executable: %v", err)
	}

	if strings.TrimSpace(*credentialRef) == "" {
		*credentialRef = "keychain:" + loomslack.CredentialService(*appID)
	}
	var credentials *credentialstore.Store
	if strings.HasPrefix(strings.TrimSpace(*credentialRef), credentialstore.ManagedReferencePrefix) {
		credentials, err = credentialstore.Open(dataDir())
		if err != nil {
			fatalf("open managed Slack credential store: %v", err)
		}
	}
	resolvedAppID, tokens, err := loomslack.LoadTokensAndAppReference(credentials, *credentialRef, *appID, *teamID)
	if err != nil {
		fatalf("read Slack credential: %v", err)
	}
	*appID = resolvedAppID
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
		*script, "--hub", *hubURL, "--connection", *connectionID,
		"--address", *addressID, "--bot-user-id", *botUserID, "--team-id", *teamID,
		"--gateway-generation", *generation, "--gateway-build", evidence.Build,
		"--gateway-executable-sha256", evidence.SHA256,
	}
	if *stateFile != "" {
		arguments = append(arguments, "--state-file", *stateFile)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := credentialpipe.Run(ctx, *node, arguments, map[string]string{
		"botToken": tokens.Bot, "appToken": tokens.App,
	}, "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"); err != nil {
		fatalf("start Slack gateway: %v", err)
	}
}

func dataDir() string {
	if value := envFirst("CODEX_LOOM_DATA", "CODEX_HUB_DATA"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, ".codex-loom")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	return filepath.Join(home, ".codex-hub")
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
