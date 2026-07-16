package hub

import (
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

func TestShutdownWaitsForOwnedWorkersAndRejectsNewWork(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := testHub(st)
	started := make(chan struct{})
	release := make(chan struct{})
	if !h.startWorker(func() {
		close(started)
		<-release
	}) {
		t.Fatal("worker was not admitted")
	}
	<-started
	done := make(chan struct{})
	go func() {
		h.Shutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Shutdown returned before the owned worker completed")
	case <-time.After(20 * time.Millisecond):
	}
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		stopping := h.stopping
		h.mu.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Shutdown did not enter stopping state")
		}
		time.Sleep(time.Millisecond)
	}
	if h.startWorker(func() {}) {
		t.Fatal("worker was admitted after shutdown began")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after the worker completed")
	}
}
