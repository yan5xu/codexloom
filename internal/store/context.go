package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) loomAgentPromptFile() string {
	return filepath.Join(s.dir, "loom-agent-prompt.json")
}

func (s *Store) contextCoverageDir() string {
	return filepath.Join(s.dir, "context-coverage")
}

func (s *Store) contextCoverageFile(threadID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return filepath.Join(s.contextCoverageDir(), hex.EncodeToString(sum[:])+".json")
}

func (s *Store) LoadLoomAgentPrompt(v any) error {
	return loadJSON(s.loomAgentPromptFile(), v)
}

func (s *Store) SaveLoomAgentPrompt(v any) error {
	return s.saveJSON(s.loomAgentPromptFile(), v)
}

func (s *Store) DeleteLoomAgentPrompt() error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer done()
	err = s.rootedRemove(s.loomAgentPromptFile())
	if os.IsNotExist(err) {
		return nil
	}
	return s.finishWrite(err)
}

func (s *Store) LoadContextCoverage(threadID string, v any) error {
	return loadJSON(s.contextCoverageFile(threadID), v)
}

func (s *Store) SaveContextCoverage(threadID string, v any) error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer done()
	if err := s.rootedMkdirAll(s.contextCoverageDir(), 0o700); err != nil {
		return err
	}
	return s.finishWrite(s.saveJSONUnlocked(s.contextCoverageFile(threadID), v))
}

func (s *Store) ReadContextCoverage(threadID string) (json.RawMessage, error) {
	data, err := os.ReadFile(s.contextCoverageFile(threadID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}
