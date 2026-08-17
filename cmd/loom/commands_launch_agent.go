package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yan5xu/codex-loom/internal/launchagent"
	"github.com/yan5xu/codex-loom/internal/proxyenv"
)

func cmdLaunchAgent(a args) {
	if len(a.positional) != 1 {
		usage("launch-agent install|preflight ...")
	}
	switch strings.ToLower(strings.TrimSpace(a.positional[0])) {
	case "install":
		path, inspection, err := installManagedLaunchAgent(a)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s managed LaunchAgent plist\n", green("installed"))
		fmt.Printf("  path: %s\n", path)
		fmt.Printf("  label: %s\n", inspection.Label)
		fmt.Printf("  proxy: %s\n", formatProxySummary(inspection.Proxy))
		fmt.Println("  launchd: unchanged (load or restart remains an explicit operator action)")
	case "preflight":
		path := launchAgentPlistPath(a)
		inspection, err := launchagent.InspectFile(path)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s managed LaunchAgent plist\n", green("preflight passed"))
		fmt.Printf("  path: %s\n", path)
		fmt.Printf("  executable: %s\n", inspection.Executable)
		fmt.Printf("  working directory: %s\n", inspection.WorkingDirectory)
		fmt.Printf("  proxy: %s\n", formatProxySummary(inspection.Proxy))
	default:
		usage("launch-agent install|preflight ...")
	}
}

func installManagedLaunchAgent(a args) (string, launchagent.Inspection, error) {
	canonical, err := proxyenv.Current()
	if err != nil {
		return "", launchagent.Inspection{}, err
	}
	path := launchAgentPlistPath(a)
	var data []byte
	var expected launchagent.Inspection
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil && info.Mode()&os.ModeSymlink != 0:
		return "", launchagent.Inspection{}, fmt.Errorf("refusing to update a symlinked LaunchAgent plist")
	case statErr == nil:
		existing, err := os.ReadFile(path)
		if err != nil {
			return "", launchagent.Inspection{}, fmt.Errorf("read existing LaunchAgent plist: %w", err)
		}
		data, expected, err = launchagent.UpdateProxy(existing, canonical)
		if err != nil {
			return "", launchagent.Inspection{}, err
		}
		if executable := strings.TrimSpace(a.flags["executable"]); executable != "" && filepath.Clean(executable) != expected.Executable {
			return "", launchagent.Inspection{}, fmt.Errorf("--executable does not match the existing LaunchAgent")
		}
		if workingDirectory := strings.TrimSpace(a.flags["working-directory"]); workingDirectory != "" && filepath.Clean(workingDirectory) != expected.WorkingDirectory {
			return "", launchagent.Inspection{}, fmt.Errorf("--working-directory does not match the existing LaunchAgent")
		}
	case os.IsNotExist(statErr):
		executable := filepath.Clean(strings.TrimSpace(a.flags["executable"]))
		workingDirectory := filepath.Clean(strings.TrimSpace(a.flags["working-directory"]))
		if executable == "." || workingDirectory == "." {
			return "", launchagent.Inspection{}, fmt.Errorf("--executable and --working-directory are required for a new LaunchAgent")
		}
		data, expected, err = launchagent.Render(launchagent.Config{
			Executable: executable, WorkingDirectory: workingDirectory,
			Path: os.Getenv("PATH"), NoProxy: canonical,
		})
		if err != nil {
			return "", launchagent.Inspection{}, err
		}
	default:
		return "", launchagent.Inspection{}, fmt.Errorf("inspect existing LaunchAgent plist: %w", statErr)
	}
	if err := launchagent.Write(path, data); err != nil {
		return "", launchagent.Inspection{}, err
	}
	actual, err := launchagent.InspectFile(path)
	if err != nil {
		return "", launchagent.Inspection{}, fmt.Errorf("read back installed LaunchAgent plist: %w", err)
	}
	if actual != expected {
		return "", launchagent.Inspection{}, fmt.Errorf("installed LaunchAgent plist readback does not match rendered identity")
	}
	return path, actual, nil
}

func launchAgentPlistPath(a args) string {
	path := strings.TrimSpace(a.flags["plist"])
	if path == "" {
		path = strings.TrimSpace(a.flags["output"])
	}
	if path == "" {
		path = strings.TrimSpace(os.Getenv("CODEX_LOOM_LAUNCH_AGENT_PLIST"))
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, "Library", "LaunchAgents", launchagent.Label+".plist")
		}
	}
	return filepath.Clean(path)
}

func formatProxySummary(summary proxyenv.Summary) string {
	if !summary.Configured {
		return "empty (0 entries)"
	}
	return fmt.Sprintf("%d entries · sha256 %s", summary.EntryCount, summary.SHA256)
}
