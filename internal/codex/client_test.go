package codex

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestVersionUsesSelectedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 0.144.1\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	version, err := Version(path)
	if err != nil {
		t.Fatal(err)
	}
	if version != "codex-cli 0.144.1" {
		t.Fatalf("version = %q", version)
	}
}

func TestRequestTimeoutRetainsIndeterminateEffectType(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() { _, _ = io.Copy(io.Discard, reader) }()

	client := &Client{
		stdin: writer, done: make(chan struct{}), pending: map[int64]chan pendingResult{},
	}
	_, err := client.Request("thread/inject_items", map[string]any{"threadId": "thread-1"}, 5*time.Millisecond)
	if err == nil || !IsRequestTimeout(err) {
		t.Fatalf("Request error = %v, want typed timeout", err)
	}
	var timeout *RequestTimeoutError
	if !errors.As(err, &timeout) || timeout.Method != "thread/inject_items" || timeout.Timeout != 5*time.Millisecond {
		t.Fatalf("timeout = %#v", timeout)
	}
}
