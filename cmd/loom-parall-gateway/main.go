// loom-parall-gateway keeps Parall Agent credentials in the operating system
// credential store while running the JavaScript WebSocket adapter.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yan5xu/codex-loom/internal/parall"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	orgID := flag.String("org-id", os.Getenv("PRLL_ORG_ID"), "Parall organization ID")
	agentID := flag.String("agent-id", os.Getenv("PRLL_AGENT_ID"), "Parall external Agent ID")
	node := flag.String("node", "", "Node.js executable")
	script := flag.String("script", "", "Parall gateway script")
	stateFile := flag.String("state-file", "", "gateway state file")
	flag.Parse()

	credentials, err := parall.LoadAgentCredentials(*orgID, *agentID)
	if err != nil {
		fatalf("read Parall Agent credentials from keychain: %v", err)
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
		*node, *script, "--hub", *hubURL, "--connection", *connectionID,
		"--address", *addressID, "--agent-id", *agentID,
	}
	if *stateFile != "" {
		arguments = append(arguments, "--state-file", *stateFile)
	}
	environment := append(os.Environ(),
		"PRLL_API_URL="+credentials.APIURL,
		"PRLL_API_KEY="+credentials.APIKey,
		"PRLL_ORG_ID="+strings.TrimSpace(*orgID),
		"PRLL_AGENT_ID="+strings.TrimSpace(*agentID),
	)
	if err := syscall.Exec(*node, arguments, environment); err != nil {
		fatalf("start Parall gateway: %v", err)
	}
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
