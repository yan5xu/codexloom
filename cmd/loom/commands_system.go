package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
)

func cmdVersion(a args) {
	if a.flags["running"] != "" {
		response, err := api("GET", "/api/version", nil)
		if err != nil {
			fail(err)
		}
		build, _ := response["build"].(map[string]any)
		fmt.Print(formatBuild("running", build))
		return
	}
	info := buildinfo.Current(nil, buildinfo.Runtime{})
	fmt.Print(formatBuild("cli", buildMap(info)))
}

func cmdDoctor(a args) {
	if len(a.positional) > 0 {
		usage("doctor")
	}
	versionResponse, err := api("GET", "/api/version", nil)
	if err != nil {
		fail(err)
	}
	health, err := api("GET", "/api/health", nil)
	if err != nil {
		fail(err)
	}
	running, _ := versionResponse["build"].(map[string]any)
	local := buildMap(buildinfo.Current(nil, buildinfo.Runtime{}))

	fmt.Printf("CodexLoom doctor\n")
	fmt.Printf("endpoint: %s\n", base)
	fmt.Print(formatBuild("running", running))
	fmt.Printf("health: ok · %.0f agents\n", num(health, "agents"))
	if mismatch := buildMismatch(local, running); mismatch != "" {
		fmt.Printf("status: %s\n", yellow(mismatch))
	} else {
		fmt.Printf("status: %s\n", green("CLI and running service match"))
	}
}

func formatBuild(label string, build map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s %s (%s)\n", label, value(build, "product", "CodexLoom"), value(build, "version", "dev"), value(build, "commit", "unknown"))
	fmt.Fprintf(&b, "  built: %s · go %s · %s/%s\n", value(build, "builtAt", "unknown"), value(build, "goVersion", runtime.Version()), value(build, "os", runtime.GOOS), value(build, "arch", runtime.GOARCH))
	if label == "running" {
		fmt.Fprintf(&b, "  process: pid %.0f · started %s · mode %s · read-only %t\n", buildNumber(build, "pid"), value(build, "startedAt", "unknown"), value(build, "mode", "normal"), boolean(build, "readOnly"))
		fmt.Fprintf(&b, "  data: %s\n", value(build, "dataDir", "unknown"))
		fmt.Fprintf(&b, "  web: %s\n", value(build, "webAsset", "unknown"))
	}
	return b.String()
}

func buildMap(info buildinfo.Info) map[string]any {
	data, _ := json.Marshal(info)
	result := map[string]any{}
	_ = json.Unmarshal(data, &result)
	return result
}

func buildMismatch(local, running map[string]any) string {
	localCommit := value(local, "commit", "unknown")
	runningCommit := value(running, "commit", "unknown")
	if localCommit != "unknown" && runningCommit != "unknown" && localCommit != runningCommit {
		return fmt.Sprintf("restart required: CLI commit %s, running commit %s", localCommit, runningCommit)
	}
	localVersion := value(local, "version", "dev")
	runningVersion := value(running, "version", "dev")
	if localVersion != runningVersion {
		return fmt.Sprintf("version mismatch: CLI %s, running %s", localVersion, runningVersion)
	}
	return ""
}

func value(record map[string]any, key, fallback string) string {
	if text, ok := record[key].(string); ok && text != "" {
		return text
	}
	return fallback
}

func buildNumber(record map[string]any, key string) float64 {
	value, _ := record[key].(float64)
	return value
}

func boolean(record map[string]any, key string) bool {
	value, _ := record[key].(bool)
	return value
}
