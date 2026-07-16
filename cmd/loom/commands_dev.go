package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yan5xu/codex-loom/internal/devcanary"
	"github.com/yan5xu/codex-loom/internal/store"
)

type canaryState struct {
	PID        int               `json:"pid"`
	Port       int               `json:"port"`
	URL        string            `json:"url"`
	DataDir    string            `json:"dataDir"`
	SourceDir  string            `json:"sourceDir"`
	LogPath    string            `json:"logPath"`
	BinaryPath string            `json:"binaryPath"`
	StartedAt  string            `json:"startedAt"`
	Snapshot   devcanary.Summary `json:"snapshot"`
}

func cmdDev(a args) {
	if len(a.positional) == 0 || a.positional[0] != "canary" {
		usage("dev canary start|status|stop ...")
	}
	a.positional = a.positional[1:]
	if len(a.positional) == 0 {
		usage("dev canary start|status|stop ...")
	}
	action := a.positional[0]
	a.positional = a.positional[1:]
	switch action {
	case "start":
		cmdCanaryStart(a)
	case "status":
		cmdCanaryStatus(a)
	case "stop":
		cmdCanaryStop(a)
	default:
		usage("dev canary start|status|stop ...")
	}
}

func cmdCanaryStart(a args) {
	root := canaryRoot()
	if state, err := readCanaryState(root); err == nil {
		if canaryProcessAlive(state.PID) {
			fail(fmt.Errorf("canary already running at %s (pid %d); stop it first", state.URL, state.PID))
		}
		_ = os.RemoveAll(root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		fail(err)
	}

	source := strings.TrimSpace(a.flags["from"])
	if source == "" {
		source = store.DefaultDir()
	}
	source, _ = filepath.Abs(source)
	dataDir := filepath.Join(root, "data")
	summary, err := devcanary.CreateSnapshot(source, dataDir, devcanary.Options{Agents: a.flagValues["agent"]})
	if err != nil {
		_ = os.RemoveAll(root)
		fail(err)
	}
	port, err := canaryPort(a.flags["port"])
	if err != nil {
		_ = os.RemoveAll(root)
		fail(err)
	}
	binary, err := findCanaryBinary()
	if err != nil {
		_ = os.RemoveAll(root)
		fail(err)
	}
	logPath := filepath.Join(root, "canary.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.RemoveAll(root)
		fail(err)
	}
	command := exec.Command(binary, "-port", strconv.Itoa(port), "-data", dataDir, "-canary")
	command.Env = append(os.Environ(), "PINIX_EDGE_NAMES="+filepath.Join(root, "disabled-edge-registry.json"))
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		_ = os.RemoveAll(root)
		fail(err)
	}
	pid := command.Process.Pid
	_ = logFile.Close()
	_ = command.Process.Release()
	state := canaryState{
		PID: pid, Port: port, URL: fmt.Sprintf("http://127.0.0.1:%d", port),
		DataDir: dataDir, SourceDir: source, LogPath: logPath, BinaryPath: binary,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Snapshot: summary,
	}
	if err := writeCanaryState(root, state); err != nil {
		_ = syscall.Kill(state.PID, syscall.SIGTERM)
		fail(err)
	}
	build, err := waitForCanary(state, 12*time.Second)
	if err != nil {
		_ = syscall.Kill(state.PID, syscall.SIGTERM)
		fail(fmt.Errorf("canary failed to become ready: %w (log: %s)", err, logPath))
	}
	fmt.Printf("canary: %s\n", green("ready"))
	fmt.Printf("url: %s\n", state.URL)
	fmt.Printf("pid: %d · agents: %d · data: %s\n", state.PID, len(summary.AgentIDs), dataDir)
	fmt.Printf("build: %s %s · web %s\n", value(build, "version", "dev"), value(build, "commit", "unknown"), value(build, "webAsset", "unknown"))
	fmt.Printf("log: %s\n", logPath)
}

func cmdCanaryStatus(a args) {
	if len(a.positional) > 0 {
		usage("dev canary status")
	}
	state, err := readCanaryState(canaryRoot())
	if err != nil {
		fail(fmt.Errorf("no canary state: %w", err))
	}
	build, verifyErr := verifyCanary(state)
	status := green("ready")
	if verifyErr != nil {
		status = yellow("stale or unavailable")
	}
	fmt.Printf("canary: %s\n", status)
	fmt.Printf("url: %s\n", state.URL)
	fmt.Printf("pid: %d · agents: %d · started: %s\n", state.PID, len(state.Snapshot.AgentIDs), state.StartedAt)
	if verifyErr != nil {
		fmt.Printf("detail: %v\n", verifyErr)
		fmt.Printf("log: %s\n", state.LogPath)
		return
	}
	fmt.Printf("build: %s %s · web %s\n", value(build, "version", "dev"), value(build, "commit", "unknown"), value(build, "webAsset", "unknown"))
}

func cmdCanaryStop(a args) {
	if len(a.positional) > 0 {
		usage("dev canary stop")
	}
	root := canaryRoot()
	state, err := readCanaryState(root)
	if err != nil {
		fail(fmt.Errorf("no canary state: %w", err))
	}
	if !canaryProcessAlive(state.PID) {
		_ = os.RemoveAll(root)
		fmt.Println("canary: already stopped; stale state removed")
		return
	}
	if _, err := verifyCanary(state); err != nil {
		command, commandErr := processCommand(state.PID)
		if commandErr != nil || !strings.Contains(command, state.BinaryPath) || !strings.Contains(command, state.DataDir) || !strings.Contains(command, "-canary") {
			fail(fmt.Errorf("refusing to stop pid %d: process identity is not the recorded CodexLoom canary", state.PID))
		}
	}
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		fail(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for canaryProcessAlive(state.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if canaryProcessAlive(state.PID) {
		fail(fmt.Errorf("canary pid %d did not stop after SIGTERM; inspect %s", state.PID, state.LogPath))
	}
	if err := os.RemoveAll(root); err != nil {
		fail(err)
	}
	fmt.Println("canary: stopped")
}

func canaryRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("codexloom-canary-%d", os.Getuid()))
}

func canaryPort(value string) (int, error) {
	if value != "" && value != "auto" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("invalid canary port: %s", value)
		}
		return port, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func findCanaryBinary() (string, error) {
	executable, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(executable), "codex-loom"),
		filepath.Join("bin", "codex-loom"),
	}
	if found, err := exec.LookPath("codex-loom"); err == nil {
		candidates = append(candidates, found)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("codex-loom binary not found; run `make build` first")
}

func waitForCanary(state canaryState, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		build, err := verifyCanary(state)
		if err == nil {
			return build, nil
		}
		lastErr = err
		if !canaryProcessAlive(state.PID) {
			return nil, fmt.Errorf("process exited before readiness")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, lastErr
}

func verifyCanary(state canaryState) (map[string]any, error) {
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(state.URL + "/api/version")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version endpoint returned %s", response.Status)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	build, _ := payload["build"].(map[string]any)
	if value(build, "mode", "") != "canary" || !boolean(build, "readOnly") || int(buildNumber(build, "pid")) != state.PID {
		return nil, fmt.Errorf("runtime identity does not match recorded canary")
	}
	actualData, _ := filepath.Abs(value(build, "dataDir", ""))
	expectedData, _ := filepath.Abs(state.DataDir)
	if actualData != expectedData {
		return nil, fmt.Errorf("runtime data directory does not match recorded canary")
	}
	return build, nil
}

func canaryProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processCommand(pid int) (string, error) {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	return strings.TrimSpace(string(output)), err
}

func readCanaryState(root string) (canaryState, error) {
	data, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		return canaryState{}, err
	}
	var state canaryState
	if err := json.Unmarshal(data, &state); err != nil {
		return canaryState{}, err
	}
	return state, nil
}

func writeCanaryState(root string, state canaryState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "state.json"), append(data, '\n'), 0o600)
}
