// Package buildinfo exposes immutable build identity together with the runtime
// identity of a specific CodexLoom process.
package buildinfo

import (
	"io/fs"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Runtime struct {
	StartedAt time.Time
	DataDir   string
	Mode      string
	ReadOnly  bool
}

type Info struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
	DataDir   string `json:"dataDir"`
	Mode      string `json:"mode"`
	ReadOnly  bool   `json:"readOnly"`
	WebAsset  string `json:"webAsset,omitempty"`
}

var mainAssetPattern = regexp.MustCompile(`(?:src|href)="/?(assets/index-[^"]+\.(?:js|css))"`)

func Current(web fs.FS, rt Runtime) Info {
	mode := strings.TrimSpace(rt.Mode)
	if mode == "" {
		mode = "normal"
	}
	startedAt := rt.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return Info{
		Product: "CodexLoom", Version: resolvedVersion(), Commit: Commit, BuiltAt: BuiltAt,
		GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		PID: os.Getpid(), StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
		DataDir: rt.DataDir, Mode: mode, ReadOnly: rt.ReadOnly, WebAsset: mainWebAsset(web),
	}
}

func resolvedVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func mainWebAsset(web fs.FS) string {
	if web == nil {
		return ""
	}
	data, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return ""
	}
	for _, match := range mainAssetPattern.FindAllSubmatch(data, -1) {
		if len(match) > 1 && strings.HasSuffix(string(match[1]), ".js") {
			return string(match[1])
		}
	}
	return ""
}
