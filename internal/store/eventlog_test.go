package store

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEventLogTailAndLastSeqAreBounded(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.SetEventLogPolicy(EventLogPolicy{
		MaxActiveBytes: 1 << 20, ReplayEvents: 10, MaxReplayBytes: 1 << 20,
		MaxArchives: 1, MaxArchiveBytes: 1 << 20, MaxArchiveAge: time.Hour,
	})
	for seq := int64(1); seq <= 8; seq++ {
		if err := st.AppendEvent("agent", Event{Seq: seq, Type: "test", Data: json.RawMessage(`{"value":true}`)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.ReadEvents("agent", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 7 || events[1].Seq != 8 {
		t.Fatalf("tail events = %#v", events)
	}
	if got := st.LastSeq("agent"); got != 8 {
		t.Fatalf("LastSeq = %d, want 8", got)
	}
}

func TestEventMaintenanceRotatesCompressesAndPrunes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.SetEventLogPolicy(EventLogPolicy{
		MaxActiveBytes: 300, ReplayEvents: 2, MaxReplayBytes: 4 << 10,
		MaxArchives: 1, MaxArchiveBytes: 1 << 20, MaxArchiveAge: time.Hour,
	})
	payload, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", 180)})
	for seq := int64(1); seq <= 4; seq++ {
		if err := st.AppendEvent("agent", Event{Seq: seq, Type: "test", Data: payload}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := st.MaintainEventLogs()
	if err != nil {
		t.Fatal(err)
	}
	if first.Rotated != 1 || first.Compressed != 1 {
		t.Fatalf("first maintenance report = %#v, want one rotation and compression", first)
	}
	for seq := int64(5); seq <= 8; seq++ {
		if err := st.AppendEvent("agent", Event{Seq: seq, Type: "test", Data: payload}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := st.MaintainEventLogs()
	if err != nil {
		t.Fatal(err)
	}
	if report.Compressed == 0 || report.Removed == 0 {
		t.Fatalf("maintenance report = %#v, want compression and pruning", report)
	}
	pending, err := filepath.Glob(filepath.Join(st.Dir(), "events", "*.pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending segments remain: %v", pending)
	}
	archives, err := filepath.Glob(filepath.Join(st.Dir(), "events", "*.ndjson.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("archives = %v, want exactly one retained", archives)
	}
	f, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	if data, err := io.ReadAll(gz); err != nil || len(data) == 0 {
		_ = gz.Close()
		_ = f.Close()
		t.Fatalf("compressed segment unreadable: bytes=%d err=%v", len(data), err)
	}
	_ = gz.Close()
	_ = f.Close()

	events, err := st.ReadEvents("agent", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || len(events) > 2 || events[len(events)-1].Seq != 8 {
		t.Fatalf("active replay window = %#v", events)
	}
	if got := st.LastSeq("agent"); got != 8 {
		t.Fatalf("LastSeq after rotation = %d, want 8", got)
	}
}
