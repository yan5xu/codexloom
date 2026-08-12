package rollout

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReasoningSanitizeResult describes a bounded provider-switch compatibility
// repair applied to one Codex rollout.
type ReasoningSanitizeResult struct {
	Changed      int    `json:"changed"`
	OriginalPath string `json:"originalPath,omitempty"`
	BackupPath   string `json:"backupPath,omitempty"`
}

// SanitizeReasoningContent clears plaintext content from reasoning response
// items. OpenAI Responses rejects reasoning input items whose content array is
// non-empty; DeepSeek sessions can persist such content and later replay fails
// after switching back to OpenAI. Other rollout data is preserved byte-for-byte
// at the semantic JSON level.
func SanitizeReasoningContent(threadID, backupDir string) (ReasoningSanitizeResult, error) {
	if strings.TrimSpace(threadID) == "" {
		return ReasoningSanitizeResult{}, fmt.Errorf("empty threadId")
	}
	if strings.TrimSpace(backupDir) == "" {
		return ReasoningSanitizeResult{}, fmt.Errorf("backupDir is required")
	}
	path, err := FindRollout(threadID)
	if err != nil {
		if errors.Is(err, ErrRolloutNotFound) {
			return ReasoningSanitizeResult{}, nil
		}
		return ReasoningSanitizeResult{}, err
	}
	needsRepair, err := reasoningContentNeedsRepair(path)
	if err != nil {
		return ReasoningSanitizeResult{}, err
	}
	if !needsRepair {
		return ReasoningSanitizeResult{OriginalPath: path}, nil
	}

	backupPath, err := copyRolloutBackup(path, backupDir, threadID)
	if err != nil {
		return ReasoningSanitizeResult{}, fmt.Errorf("backup rollout before provider switch: %w", err)
	}
	changed, err := rewriteReasoningContent(path)
	if err != nil {
		return ReasoningSanitizeResult{}, err
	}
	invalidateRolloutPath(path)
	return ReasoningSanitizeResult{
		Changed:      changed,
		OriginalPath: path,
		BackupPath:   backupPath,
	}, nil
}

// RestoreRolloutBackup atomically restores a provider-switch backup and drops
// any in-memory rollout index for the restored file.
func RestoreRolloutBackup(backupPath, originalPath string) error {
	if strings.TrimSpace(backupPath) == "" || strings.TrimSpace(originalPath) == "" {
		return fmt.Errorf("backupPath and originalPath are required")
	}
	if err := replaceFile(backupPath, originalPath); err != nil {
		return err
	}
	invalidateRolloutPath(originalPath)
	return nil
}

func reasoningContentNeedsRepair(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<26)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var ln line
		if err := json.Unmarshal(raw, &ln); err != nil {
			return false, fmt.Errorf("decode rollout line: %w", err)
		}
		if ln.Type != "response_item" {
			continue
		}
		var payload struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(ln.Payload, &payload); err != nil {
			return false, fmt.Errorf("decode response item: %w", err)
		}
		if payload.Type != "reasoning" || len(bytes.TrimSpace(payload.Content)) == 0 {
			continue
		}
		var content []any
		if err := json.Unmarshal(payload.Content, &content); err == nil && len(content) > 0 {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func rewriteReasoningContent(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	src, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".rollout-provider-switch-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	reader := bufio.NewReaderSize(src, 1<<20)
	writer := bufio.NewWriterSize(tmp, 1<<20)
	changed := 0
	for {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 {
				if _, err := writer.Write(raw); err != nil {
					return 0, err
				}
			} else {
				next, lineChanged, err := sanitizeRolloutLine(trimmed)
				if err != nil {
					return 0, err
				}
				if lineChanged {
					changed++
				}
				if _, err := writer.Write(append(next, '\n')); err != nil {
					return 0, err
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if err := writer.Flush(); err != nil {
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return 0, err
	}
	removeTmp = false
	return changed, nil
}

func sanitizeRolloutLine(raw []byte) ([]byte, bool, error) {
	var ln line
	if err := json.Unmarshal(raw, &ln); err != nil {
		return nil, false, fmt.Errorf("decode rollout line: %w", err)
	}
	if ln.Type != "response_item" {
		return raw, false, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(ln.Payload, &payload); err != nil {
		return nil, false, fmt.Errorf("decode response item: %w", err)
	}
	if payload["type"] != "reasoning" {
		return raw, false, nil
	}
	content, ok := payload["content"].([]any)
	if !ok || len(content) == 0 {
		return raw, false, nil
	}
	payload["content"] = []any{}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	ln.Payload = payloadRaw
	out, err := json.Marshal(ln)
	return out, true, err
}

func copyRolloutBackup(path, backupDir, threadID string) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	safeID := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(threadID)
	name := fmt.Sprintf("provider-switch-%s-%s.jsonl", time.Now().UTC().Format("20060102T150405Z"), safeID)
	backupPath := filepath.Join(backupDir, name)
	if err := copyFile(path, backupPath); err != nil {
		return "", err
	}
	return backupPath, nil
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	syncErr := dst.Sync()
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func replaceFile(srcPath, dstPath string) error {
	info, err := os.Stat(dstPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	tmp := dstPath + ".restore.tmp"
	if err := copyFile(srcPath, tmp); err != nil {
		return err
	}
	if info != nil {
		if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, dstPath)
}

func invalidateRolloutPath(path string) {
	indexCache.Lock()
	delete(indexCache.entries, path)
	indexCache.Unlock()
}
