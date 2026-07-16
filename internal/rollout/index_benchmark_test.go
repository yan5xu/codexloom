package rollout

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const benchmarkTurnCount = 50_000

func BenchmarkReadWindowLongThread(b *testing.B) {
	threadID, path, cacheKey := writeBenchmarkRollout(b, benchmarkTurnCount)

	b.Run("cold-index-latest-10", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			indexCache.Lock()
			delete(indexCache.entries, path)
			indexCache.Unlock()
			rolloutPathCache.Delete(cacheKey)
			transcript, total, err := ReadWindow(threadID, 10, 0)
			if err != nil {
				b.Fatal(err)
			}
			if total != benchmarkTurnCount || len(transcript.Turns) != 10 {
				b.Fatalf("unexpected window: total=%d turns=%d", total, len(transcript.Turns))
			}
		}
		b.ReportMetric(benchmarkTurnCount, "turns")
	})

	if _, _, err := ReadWindow(threadID, 10, 0); err != nil {
		b.Fatal(err)
	}
	b.Run("warm-latest-10", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			transcript, total, err := ReadWindow(threadID, 10, 0)
			if err != nil {
				b.Fatal(err)
			}
			if total != benchmarkTurnCount || len(transcript.Turns) != 10 {
				b.Fatalf("unexpected window: total=%d turns=%d", total, len(transcript.Turns))
			}
		}
		b.ReportMetric(benchmarkTurnCount, "turns")
	})
}

func writeBenchmarkRollout(b *testing.B, turns int) (threadID, path, cacheKey string) {
	b.Helper()
	threadID = "benchmark-long-thread"
	dir := b.TempDir()
	day := filepath.Join(dir, "2026", "07", "15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		b.Fatal(err)
	}
	path = filepath.Join(day, "rollout-2026-07-15T12-00-00-"+threadID+".jsonl")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	for i := 0; i < turns; i++ {
		if _, err := fmt.Fprintf(writer,
			"{\"timestamp\":\"2026-07-15T12:00:00Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"turn-%d\"}}\n"+
				"{\"timestamp\":\"2026-07-15T12:00:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\",\"message\":\"task %d\"}}\n"+
				"{\"timestamp\":\"2026-07-15T12:00:02Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"turn_id\":\"turn-%d\"}}\n",
			i, i, i); err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	b.Setenv("CODEX_SESSIONS_DIR", dir)
	cacheKey = dir + "\x00" + threadID
	return threadID, path, cacheKey
}
