package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

// larkGatewayBinaryPath locates the accepted loom-feishu-gateway executable
// installed next to this CLI. Tests override it with an isolated binary.
var larkGatewayBinaryPath = func() (string, error) {
	current, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join(filepath.Dir(current), "loom-feishu-gateway")}
	if path, err := exec.LookPath("loom-feishu-gateway"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("loom-feishu-gateway is not built next to %s", filepath.Base(current))
}

// cmdLarkMigrate is the narrow local operator flow for one Lark Connection.
// It runs in-process against an isolated data directory (maintenance mode:
// the codex-loom server must be stopped so this process is the single
// writer). No secret ever enters arguments, output, logs, ordinary backups,
// or integrations durable state.
func cmdLarkMigrate(a args) {
	if len(a.positional) == 0 {
		usage("lark-migrate preflight|dry-run|migrate|verify|rollback --data DIR --connection ID [--source PATH]")
	}
	action := strings.ToLower(strings.TrimSpace(a.positional[0]))
	dataDir := strings.TrimSpace(a.flags["data"])
	if dataDir == "" {
		dataDir = store.DefaultDir()
	}
	connectionID := strings.TrimSpace(a.flags["connection"])
	if connectionID == "" {
		usage("lark-migrate ... --connection ID")
	}
	st, err := store.Open(dataDir)
	if err != nil {
		fail(fmt.Errorf("open data directory (is the server stopped?): %w", err))
	}
	h, err := hub.Open(st)
	if err != nil {
		_ = st.Close()
		fail(fmt.Errorf("open Hub: %w", err))
	}
	defer func() {
		h.Shutdown()
		_ = st.Close()
	}()
	ctx := context.Background()
	switch action {
	case "preflight", "dry-run":
		result, err := h.MigrateLarkCredential(ctx, connectionID, nil, true)
		if err != nil {
			fail(err)
		}
		executable, err := larkGatewayBinaryPath()
		if err != nil {
			fail(err)
		}
		if err := h.PreflightLarkGatewayLaunch(connectionID, executable); err != nil {
			fail(fmt.Errorf("launch plan preflight: %w", err))
		}
		printJSON(map[string]any{
			"action": action, "connectionId": result.ConnectionID, "currentRef": result.CurrentRef,
			"floorRaised": result.FloorRaised, "wouldRaiseFloor": !result.FloorRaised,
			"alreadyMigrated": result.AlreadyMigrated, "launchPlan": "ready",
		})
	case "migrate":
		executable, err := larkGatewayBinaryPath()
		if err != nil {
			fail(fmt.Errorf("locate loom-feishu-gateway for launch plan: %w", err))
		}
		if err := h.PreflightLarkGatewayLaunch(connectionID, executable); err != nil {
			fail(fmt.Errorf("launch plan preflight: %w", err))
		}
		result, err := h.MigrateLarkCredential(ctx, connectionID, nil, false)
		if err != nil {
			if !hub.IsLarkCredentialSecretRequired(err) {
				fail(err)
			}
			source := strings.TrimSpace(a.flags["source"])
			if source == "" {
				usage("lark-migrate migrate --source PATH")
			}
			secretText, readErr := readOwnerOnlySecretFile(source)
			if readErr != nil {
				fail(readErr)
			}
			result, err = h.MigrateLarkCredential(ctx, connectionID, []byte(secretText), false)
			if err != nil {
				fail(err)
			}
		}
		fmt.Printf("%s Lark connection %s now uses %s\n", green("migrated"), bold(result.ConnectionID), result.CurrentRef)
		if result.FloorRaised {
			fmt.Println("  credential writer floor raised; old builds are blocked from this data directory")
		}
		if result.AlreadyMigrated && !result.PlanPending && h.LarkGatewayLaunchPlanRef(connectionID) == result.CurrentRef {
			fmt.Println("  launch plan already frozen; Hub startup will consume it")
		} else {
			if err := h.ConfigureLarkGatewayLaunch(connectionID, executable); err != nil {
				fail(fmt.Errorf("credential migrated but the Lark Gateway launch plan is pending: %w", err))
			}
			if err := h.FinishLarkGatewayLaunchPlan(connectionID); err != nil {
				fail(fmt.Errorf("launch plan frozen but migration completion failed: %w", err))
			}
			fmt.Println("  launch plan frozen (dormant); Hub startup will consume it")
		}
	case "verify":
		result, err := h.VerifyLarkCredential(connectionID)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s Lark connection %s credential reference %s resolves\n", green("verified"), bold(result.ConnectionID), result.CurrentRef)
	case "rollback":
		result, err := h.RollbackLarkCredential(connectionID)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s Lark connection %s restored to %s\n", green("rolled back"), bold(result.ConnectionID), result.CurrentRef)
		fmt.Println("  managed launch plan revoked; next startup returns to the legacy path")
	default:
		usage("lark-migrate preflight|dry-run|migrate|verify|rollback ...")
	}
}
