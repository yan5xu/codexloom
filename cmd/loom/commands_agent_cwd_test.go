package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRequestedAgentCwdRejectsRelativePath(t *testing.T) {
	if got, err := normalizeRequestedAgentCwd(""); err != nil || got != "" {
		t.Fatalf("default CLI cwd = %q, %v; want an omitted custom path", got, err)
	}
	if _, err := normalizeRequestedAgentCwd("relative/home"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative CLI cwd error = %v", err)
	}
	home := t.TempDir()
	got, err := normalizeRequestedAgentCwd(filepath.Join(home, "."))
	if err != nil || got != home {
		t.Fatalf("normalized CLI cwd = %q, %v; want %q", got, err, home)
	}
}

func TestCmdAgentUpdateWithoutCwdSelectsManagedDefaultHome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/agents/worker/cwd" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("default Agent Home body = %#v, want no custom cwd", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"update": map[string]any{
				"agentId": "agent-1", "agentName": "worker", "threadId": "thread-1",
				"oldCwd": "/old/home", "newCwd": "/Users/test/codexloom/agents/worker",
				"effectiveState": "next_thread_start_or_resume", "runtimeState": "not_loaded", "skillsState": "refreshed",
			},
		})
	}))
	defer server.Close()
	previousBase := base
	previousColor := useColor
	base = server.URL
	useColor = false
	defer func() { base, useColor = previousBase, previousColor }()

	output := captureStdout(t, func() {
		cmdAgentUpdate(args{positional: []string{"worker"}, flags: map[string]string{}, flagValues: map[string][]string{}})
	})
	for _, fragment := range []string{"updated worker (agent-1)", "/Users/test/codexloom/agents/worker", "not_loaded"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("default CLI output missing %q: %s", fragment, output)
		}
	}
}

func TestCmdAgentUpdateSendsDedicatedCwdOperationAndPrintsReceipt(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/agents/worker/cwd" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["cwd"] != home {
			t.Fatalf("cwd body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{"id": "agent-1", "threadId": "thread-1", "cwd": home},
			"update": map[string]any{
				"agentId": "agent-1", "agentName": "worker", "threadId": "thread-1",
				"oldCwd": "/old/home", "newCwd": home,
				"effectiveState": "next_thread_start_or_resume", "runtimeState": "cold_resume_required", "skillsState": "refreshed",
			},
		})
	}))
	defer server.Close()
	previousBase := base
	previousColor := useColor
	base = server.URL
	useColor = false
	defer func() { base, useColor = previousBase, previousColor }()

	output := captureStdout(t, func() {
		cmdAgentUpdate(args{positional: []string{"worker"}, flags: map[string]string{"cwd": home}, flagValues: map[string][]string{}})
	})
	for _, fragment := range []string{"updated worker (agent-1)", "thread-1", "/old/home", home, "next_thread_start_or_resume", "cold_resume_required", "refreshed"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("CLI output missing %q: %s", fragment, output)
		}
	}
}
