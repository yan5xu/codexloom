package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yan5xu/codex-loom/internal/store"
)

const restartGoalIntentFile = ".restart-goals.json"

type restartGoalIntent struct {
	Version   int      `json:"version"`
	AgentIDs  []string `json:"agentIds"`
	CreatedAt string   `json:"createdAt"`
}

func (s *Server) prepareGoalsForRestart() error {
	if s.st == nil {
		return nil
	}
	if _, found, err := readRestartGoalIntent(s.st.Dir()); err != nil {
		return fmt.Errorf("read existing restart Goal intent: %w", err)
	} else if found {
		return fmt.Errorf("a previous restart Goal recovery is still pending")
	}
	agentIDs := s.hub.ActiveGoalAgentIDs()
	if len(agentIDs) == 0 {
		return nil
	}
	intent := restartGoalIntent{
		Version: 1, AgentIDs: agentIDs, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeRestartGoalIntent(s.st, intent); err != nil {
		return fmt.Errorf("persist restart Goal intent: %w", err)
	}
	paused, err := s.hub.PauseGoalsForRestart(agentIDs)
	if err == nil && len(paused) > 0 {
		return nil
	}
	if err == nil {
		return clearRestartGoalIntent(s.st)
	}
	resumeErr := s.hub.ResumeGoalsAfterRestart(agentIDs)
	if resumeErr == nil {
		_ = clearRestartGoalIntent(s.st)
	}
	return errors.Join(err, resumeErr)
}

// ResumeRestartPausedGoals completes the second half of a graceful restart.
// The intent is kept when Codex is unavailable so a later process start can
// safely retry the idempotent recovery.
func (s *Server) ResumeRestartPausedGoals() {
	if s.st == nil || s.readOnly {
		return
	}
	intent, found, err := readRestartGoalIntent(s.st.Dir())
	if err != nil {
		log.Printf("[codex-loom] read restart Goal intent: %v", err)
		return
	}
	if !found {
		return
	}
	if err := s.hub.ResumeGoalsAfterRestart(intent.AgentIDs); err != nil {
		log.Printf("[codex-loom] resume Goals after restart: %v", err)
		return
	}
	if err := clearRestartGoalIntent(s.st); err != nil {
		log.Printf("[codex-loom] clear restart Goal intent: %v", err)
		return
	}
	log.Printf("[codex-loom] resumed %d Goal candidate(s) after restart", len(intent.AgentIDs))
}

func (s *Server) restoreGoalsAfterFailedRestart() error {
	if s.st == nil {
		return nil
	}
	intent, found, err := readRestartGoalIntent(s.st.Dir())
	if err != nil || !found {
		return err
	}
	if err := s.hub.ResumeGoalsAfterRestart(intent.AgentIDs); err != nil {
		return err
	}
	return clearRestartGoalIntent(s.st)
}

func restartGoalIntentPath(dataDir string) string {
	return filepath.Join(dataDir, restartGoalIntentFile)
}

func readRestartGoalIntent(dataDir string) (restartGoalIntent, bool, error) {
	path := restartGoalIntentPath(dataDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return restartGoalIntent{}, false, nil
	}
	if err != nil {
		return restartGoalIntent{}, false, err
	}
	var intent restartGoalIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return restartGoalIntent{}, false, err
	}
	if intent.Version != 1 {
		return restartGoalIntent{}, false, fmt.Errorf("unsupported restart Goal intent version %d", intent.Version)
	}
	intent.AgentIDs = uniqueRestartGoalIDs(intent.AgentIDs)
	if len(intent.AgentIDs) == 0 {
		return restartGoalIntent{}, false, fmt.Errorf("restart Goal intent has no Agent IDs")
	}
	return intent, true, nil
}

func writeRestartGoalIntent(st *store.Store, intent restartGoalIntent) error {
	if st == nil {
		return fmt.Errorf("restart Goal Store is unavailable")
	}
	intent.AgentIDs = uniqueRestartGoalIDs(intent.AgentIDs)
	if len(intent.AgentIDs) == 0 {
		return fmt.Errorf("restart Goal intent has no Agent IDs")
	}
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	return st.WithStableWriteRoot(func(root *os.Root) error {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		tmpPath := ".restart-goals-" + hex.EncodeToString(random) + ".tmp"
		tmp, err := root.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			_ = tmp.Close()
			if !committed {
				_ = root.Remove(tmpPath)
			}
		}()
		if _, err := tmp.Write(data); err != nil {
			return err
		}
		if err := tmp.Sync(); err != nil {
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := root.Rename(tmpPath, restartGoalIntentFile); err != nil {
			return err
		}
		committed = true
		return syncRestartGoalRoot(root)
	})
}

func clearRestartGoalIntent(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("restart Goal Store is unavailable")
	}
	return st.WithStableWriteRoot(func(root *os.Root) error {
		err := root.Remove(restartGoalIntentFile)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return syncRestartGoalRoot(root)
	})
}

func syncRestartGoalRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func uniqueRestartGoalIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
