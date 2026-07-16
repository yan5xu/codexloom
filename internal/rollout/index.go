package rollout

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
)

type rolloutIndex struct {
	offset  int64
	modTime int64
	turns   []int64
	latest  *LatestTurnSummary
}

var indexCache = struct {
	sync.Mutex
	entries map[string]*rolloutIndex
}{entries: map[string]*rolloutIndex{}}

// ReadWindow parses only the requested Turn window. A small, rebuildable
// offset index is updated incrementally beside the canonical rollout file.
func ReadWindow(threadID string, count, offset int) (*Transcript, int, error) {
	path, err := FindRollout(threadID)
	if err != nil {
		return nil, 0, err
	}
	index, err := readRolloutIndex(path)
	if err != nil {
		return nil, 0, err
	}
	total := len(index.turns)
	if total == 0 {
		transcript, err := ReadFile(path, threadID)
		if err != nil {
			return nil, 0, err
		}
		return transcript, len(transcript.Turns), nil
	}
	if count <= 0 {
		count = 10
	}
	if offset < 0 {
		offset = 0
	}
	endTurn := total - offset
	if endTurn < 0 {
		endTurn = 0
	}
	startTurn := endTurn - count
	if startTurn < 0 {
		startTurn = 0
	}
	if startTurn == endTurn {
		return &Transcript{ThreadID: threadID, Path: path, Turns: []Turn{}}, total, nil
	}
	startByte := index.turns[startTurn]
	endByte := index.offset
	if endTurn < total {
		endByte = index.turns[endTurn]
	}
	transcript, err := readFileRange(path, threadID, startByte, endByte)
	return transcript, total, err
}

// LatestTurn returns the incrementally maintained status projection of the
// newest rollout Turn without rescanning the Thread's full history.
func LatestTurn(threadID string) (*LatestTurnSummary, error) {
	path, err := FindRollout(threadID)
	if err != nil {
		return nil, err
	}
	index, err := readRolloutIndex(path)
	if err != nil || index.latest == nil {
		return nil, err
	}
	copy := *index.latest
	return &copy, nil
}

func readRolloutIndex(path string) (*rolloutIndex, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	indexCache.Lock()
	defer indexCache.Unlock()

	index := indexCache.entries[path]
	if index == nil || info.Size() < index.offset || info.Size() == index.offset && info.ModTime().UnixNano() != index.modTime {
		index = &rolloutIndex{turns: []int64{}}
	}
	if info.Size() > index.offset {
		if err := extendRolloutIndex(path, index); err != nil {
			return nil, err
		}
		info, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
	}
	index.modTime = info.ModTime().UnixNano()
	indexCache.entries[path] = index
	copy := *index
	copy.turns = append([]int64(nil), index.turns...)
	if index.latest != nil {
		latest := *index.latest
		copy.latest = &latest
	}
	return &copy, nil
}

func extendRolloutIndex(path string, index *rolloutIndex) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(index.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 1<<20)
	position := index.offset
	for {
		lineStart := position
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			position += int64(len(line))
			consumeIndexLine(index, lineStart, line)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	index.offset = position
	return nil
}

func consumeIndexLine(index *rolloutIndex, lineStart int64, raw []byte) {
	if !bytes.Contains(raw, []byte(`"type":"event_msg"`)) {
		return
	}
	var line struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &line) != nil || line.Type != "event_msg" {
		return
	}
	var event struct {
		Type    string `json:"type"`
		TurnID  string `json:"turn_id"`
		Message string `json:"message"`
	}
	if json.Unmarshal(line.Payload, &event) != nil {
		return
	}
	switch event.Type {
	case "task_started":
		index.turns = append(index.turns, lineStart)
		index.latest = &LatestTurnSummary{ID: event.TurnID, Status: "running", UpdatedAt: line.Timestamp}
	case "user_message":
		if index.latest != nil {
			if index.latest.Task == "" {
				index.latest.Task = event.Message
			}
			index.latest.UpdatedAt = line.Timestamp
		}
	case "task_complete":
		if index.latest != nil && (event.TurnID == "" || event.TurnID == index.latest.ID) {
			index.latest.Status = "completed"
			index.latest.UpdatedAt = line.Timestamp
		}
	case "turn_aborted":
		if index.latest != nil && (event.TurnID == "" || event.TurnID == index.latest.ID) {
			index.latest.Status = "interrupted"
			index.latest.UpdatedAt = line.Timestamp
		}
	default:
		if index.latest != nil {
			index.latest.UpdatedAt = line.Timestamp
		}
	}
}
