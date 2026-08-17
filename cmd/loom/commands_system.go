package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/launchagent"
	"github.com/yan5xu/codex-loom/internal/proxyenv"
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
	providerResponse, providerErr := api("GET", "/api/model-providers", nil)

	fmt.Printf("CodexLoom doctor\n")
	fmt.Printf("endpoint: %s\n", base)
	fmt.Print(formatBuild("running", running))
	fmt.Printf("health: ok · %.0f agents\n", num(health, "agents"))
	if providerErr != nil {
		fmt.Printf("catalog: %s\n", yellow("unavailable: "+providerErr.Error()))
	} else if catalog, ok := providerResponse["catalog"].(map[string]any); ok {
		compatibility := value(catalog, "compatibility", "unverified")
		status := compatibility
		if boolean(catalog, "restartRequired") {
			status = "restart required"
		}
		line := fmt.Sprintf("%s · Codex %s · baseline %s · %.0f models", value(catalog, "version", "unknown"), value(catalog, "codexVersion", "unknown"), value(catalog, "codexBaseline", "unknown"), num(catalog, "modelCount"))
		if compatibility == "verified" && !boolean(catalog, "restartRequired") {
			fmt.Printf("catalog: %s · %s\n", line, green(status))
		} else {
			fmt.Printf("catalog: %s · %s\n", line, yellow(status))
		}
	}
	fmt.Printf("proxy: %s\n", proxyDoctorStatus(launchAgentPlistPath(args{}), versionResponse))
	if mismatch := buildMismatch(local, running); mismatch != "" {
		fmt.Printf("status: %s\n", yellow(mismatch))
	} else {
		fmt.Printf("status: %s\n", green("CLI and running service match"))
	}
}

func proxyDoctorStatus(plistPath string, versionResponse map[string]any) string {
	inspection, err := launchagent.InspectFile(plistPath)
	if err != nil {
		return yellow("LaunchAgent preflight unavailable: " + err.Error())
	}
	proxy, ok := versionResponse["proxy"].(map[string]any)
	if !ok {
		return yellow("running service does not expose proxy readback")
	}
	if valid, _ := proxy["valid"].(bool); !valid {
		message, _ := proxy["error"].(string)
		if message == "" {
			message = "invalid runtime proxy configuration"
		}
		return red(message)
	}
	hubRecord, _ := proxy["hub"].(map[string]any)
	childRecord, _ := proxy["codexHost"].(map[string]any)
	hubSummary := proxySummaryFromRecord(hubRecord)
	childSummary := proxySummaryFromRecord(childRecord)
	if !proxyenv.Same(inspection.Proxy, hubSummary) {
		return red(fmt.Sprintf("mismatch: LaunchAgent %s; Hub %s", formatProxySummary(inspection.Proxy), formatProxySummary(hubSummary)))
	}
	loaded, _ := proxy["codexHostLoaded"].(bool)
	if !loaded {
		return yellow("LaunchAgent and Hub match; CodexHost is not loaded yet")
	}
	matching, _ := proxy["matching"].(bool)
	if !matching || !proxyenv.Same(hubSummary, childSummary) {
		return red(fmt.Sprintf("mismatch: Hub %s; CodexHost %s", formatProxySummary(hubSummary), formatProxySummary(childSummary)))
	}
	return green("verified · LaunchAgent, Hub, and CodexHost " + formatProxySummary(hubSummary))
}

func proxySummaryFromRecord(record map[string]any) proxyenv.Summary {
	configured, _ := record["configured"].(bool)
	digest, _ := record["sha256"].(string)
	return proxyenv.Summary{
		Configured: configured,
		EntryCount: int(num(record, "entryCount")),
		SHA256:     digest,
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
