// loom-feishu-gateway connects one Feishu application identity to a
// CodexLoom Connection without requiring lark-cli at runtime.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/feishugw"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	appID := flag.String("app-id", os.Getenv("FEISHU_APP_ID"), "Feishu App ID")
	stateFile := flag.String("state-file", "", "gateway state file")
	flag.Parse()

	secret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	if secret == "" && strings.TrimSpace(*appID) != "" {
		var err error
		secret, err = feishu.LoadAppSecret(*appID)
		if err != nil {
			log.Fatalf("read Feishu App Secret from keychain: %v", err)
		}
	}
	if *stateFile == "" && *connectionID != "" {
		*stateFile = filepath.Join(dataDir(), "gateway", "feishu-"+*connectionID+".json")
	}
	gateway, err := feishugw.New(feishugw.Config{
		HubURL: *hubURL, ConnectionID: *connectionID, AddressID: *addressID,
		AppID: *appID, AppSecret: secret, ConnectorToken: os.Getenv("CODEX_LOOM_CONNECTOR_TOKEN"),
		StateFile: *stateFile,
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := gateway.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
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
