// loom-parall-gateway resolves a managed or legacy Parall reference and passes
// credentials to the JavaScript WebSocket adapter through an anonymous FD.
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
	"github.com/yan5xu/codex-loom/internal/parall"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	orgID := flag.String("org-id", os.Getenv("PRLL_ORG_ID"), "Parall organization ID")
	agentID := flag.String("agent-id", os.Getenv("PRLL_AGENT_ID"), "Parall external Agent ID")
	credentialRef := flag.String("credential-ref", os.Getenv("CODEX_LOOM_CREDENTIAL_REF"), "opaque credential reference")
	node := flag.String("node", "", "Node.js executable")
	script := flag.String("script", "", "Parall gateway script")
	stateFile := flag.String("state-file", "", "gateway state file")
	generation := flag.String("generation", envFirst("CODEX_LOOM_GATEWAY_GENERATION"), "non-secret managed gateway generation")
	flag.Parse()
	evidence, err := buildinfo.CurrentExecutableEvidence()
	if err != nil {
		fatalf("observe Parall gateway executable: %v", err)
	}

	if strings.TrimSpace(*credentialRef) == "" {
		*credentialRef = "keychain:" + parall.AgentCredentialService(*orgID, *agentID)
	}
	var store *credentialstore.Store
	if strings.HasPrefix(strings.TrimSpace(*credentialRef), credentialstore.ManagedReferencePrefix) {
		store, err = credentialstore.Open(dataDir())
		if err != nil {
			fatalf("open managed Parall credential store: %v", err)
		}
	}
	credentials, err := parall.LoadAgentCredentialsReference(store, *credentialRef, *orgID, *agentID)
	if err != nil {
		fatalf("read Parall Agent credential: %v", err)
	}
	if credentials.APIURL == "" || credentials.APIKey == "" {
		fatalf("Parall Agent credentials are missing for %s", *agentID)
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
			fatalf("find Parall gateway: %v", err)
		}
	}
	arguments := []string{
		*script, "--hub", *hubURL, "--connection", *connectionID,
		"--address", *addressID, "--agent-id", *agentID,
		"--gateway-generation", *generation, "--gateway-build", evidence.Build,
		"--gateway-executable-sha256", evidence.SHA256,
	}
	if *stateFile != "" {
		arguments = append(arguments, "--state-file", *stateFile)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := credentialpipe.Run(ctx, *node, arguments, map[string]string{
		"apiURL": credentials.APIURL, "apiKey": credentials.APIKey,
		"orgID": strings.TrimSpace(*orgID), "agentID": strings.TrimSpace(*agentID),
	}, "PRLL_API_URL", "PRLL_API_KEY", "PRLL_ORG_ID", "PRLL_AGENT_ID"); err != nil {
		fatalf("start Parall gateway: %v", err)
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
		filepath.Join(filepath.Dir(filepath.Dir(current)), "gateway", "parall.mjs"),
		filepath.Join("gateway", "parall.mjs"),
	} {
		path, err := filepath.Abs(candidate)
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("gateway/parall.mjs not found")
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
