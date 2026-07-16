package httpapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func TestGlobalEventsReplayAfterCursor(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	h.EmitGlobal("loom/test-one", map[string]any{"value": 1})
	firstSeq := h.LastGlobalSeq()
	h.EmitGlobal("loom/test-two", map[string]any{"value": 2})
	secondSeq := h.LastGlobalSeq()

	types := readGlobalSSE(t, h, fmt.Sprintf("/api/events?since=%d", firstSeq), 1)
	event := types[0]
	if event.Type != "loom/test-two" || event.Seq != secondSeq {
		t.Fatalf("replayed event = %#v, want loom/test-two seq %d", event, secondSeq)
	}
}

func TestGlobalEventsRequestsReconcileWhenCursorWasCompacted(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(st)
	defer h.Shutdown()
	st.SetEventLogPolicy(store.EventLogPolicy{
		MaxActiveBytes: 300, ReplayEvents: 2, MaxReplayBytes: 4 << 10,
		MaxArchives: 0, MaxArchiveBytes: 0, MaxArchiveAge: 0,
	})
	for index := 0; index < 8; index++ {
		h.EmitGlobal("loom/large", map[string]any{"index": index, "text": strings.Repeat("x", 180)})
	}
	if _, err := st.MaintainEventLogs(); err != nil {
		t.Fatal(err)
	}

	events := readGlobalSSE(t, h, "/api/events?since=1", 1)
	if events[0].Type != "loom/reconcile" {
		t.Fatalf("first event after compacted cursor = %#v", events[0])
	}
}

func readGlobalSSE(t *testing.T, h *hub.Hub, path string, count int) []store.Event {
	t.Helper()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o644)}}
	server := httptest.NewServer(New(h, nil, web).Handler())
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	events := make([]store.Event, 0, count)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event store.Event
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		events = append(events, event)
		if len(events) == count {
			return events
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("SSE ended after %d events, want %d", len(events), count)
	return nil
}
