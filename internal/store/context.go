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
	return s.loadJSON(s.loomAgentPromptFile(), v)
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
	rel, err := s.relative(s.loomAgentPromptFile())
	if err != nil {
		return err
	}
	err = s.dirHandle.root.Remove(rel)
	if os.IsNotExist(err) {
		return nil
	}
	return s.finishWrite(err)
}

func (s *Store) LoadContextCoverage(threadID string, v any) error {
	return s.loadJSON(s.contextCoverageFile(threadID), v)
}

func (s *Store) SaveContextCoverage(threadID string, v any) error {
	done, err := s.beginWrite()
	if err != nil {
		return err
	}
	rel, err := s.relative(s.contextCoverageDir())
	if err != nil {
		done()
		return err
	}
	if err := s.dirHandle.root.MkdirAll(rel, 0o700); err != nil {
		done()
		return err
	}
	done()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.replaceFile(s.contextCoverageFile(threadID), data, 0o600)
}

func (s *Store) ReadContextCoverage(threadID string) (json.RawMessage, error) {
	data, err := s.readFile(s.contextCoverageFile(threadID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}
