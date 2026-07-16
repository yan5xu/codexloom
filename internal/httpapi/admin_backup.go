package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/backup"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/rollout"
	"github.com/yan5xu/codex-loom/internal/store"
)

func (s *Server) adminListBackups(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin backup is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}
	items, err := backup.List(s.st.Dir())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backupStatus(items, backupRetentionPolicy(), s.st.Dir()))
}

func (s *Server) adminBackup(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin backup is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Reason == "" {
		body.Reason = "manual"
	}
	snapshot, err := s.createBackup(body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, listErr := backup.List(s.st.Dir())
	if listErr != nil {
		writeErr(w, listErr)
		return
	}
	response := backupStatus(items, backupRetentionPolicy(), s.st.Dir())
	response["backup"] = snapshot
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) adminPruneBackups(w http.ResponseWriter, r *http.Request) {
	if !allowAdminRequest(r) {
		writeErr(w, &hub.HubError{Status: 403, Message: "admin backup is only allowed from localhost unless CODEX_LOOM_ADMIN_TOKEN is configured"})
		return
	}
	policy := backupRetentionPolicy()
	report, err := backup.ApplyRetention(s.st.Dir(), policy)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := backup.List(s.st.Dir())
	if err != nil {
		writeErr(w, err)
		return
	}
	response := backupStatus(items, policy, s.st.Dir())
	response["prune"] = report
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createBackup(reason string) (*backup.Snapshot, error) {
	agents := s.hub.ListAgents()
	refs := make([]backup.AgentRef, 0, len(agents))
	for _, agent := range agents {
		refs = append(refs, backup.AgentRef{
			ID:       agent.ID,
			Name:     agent.Name,
			ThreadID: agent.ThreadID,
			Cwd:      agent.Cwd,
			Source:   agent.Source,
		})
	}
	return backup.Create(backup.Options{
		Reason:           reason,
		DataDir:          s.st.Dir(),
		CodexSessionsDir: rollout.DefaultSessionsDir(),
		EdgeNamesFile:    store.EdgeNamesFile(),
		Agents:           refs,
		Retention:        backupRetentionPolicy(),
	})
}

func backupRetentionPolicy() backup.RetentionPolicy {
	policy := backup.DefaultRetentionPolicy()
	policy.MinCount = positiveEnvInt("CODEX_LOOM_BACKUP_MIN_KEEP", "CODEX_HUB_BACKUP_MIN_KEEP", policy.MinCount)
	policy.MaxCount = positiveEnvInt("CODEX_LOOM_BACKUP_KEEP", "CODEX_HUB_BACKUP_KEEP", policy.MaxCount)
	if value := positiveEnvFloat("CODEX_LOOM_BACKUP_MAX_GB", "CODEX_HUB_BACKUP_MAX_GB"); value > 0 {
		policy.MaxBytes = int64(value * float64(1<<30))
	}
	if value := positiveEnvFloat("CODEX_LOOM_BACKUP_MAX_AGE_DAYS", "CODEX_HUB_BACKUP_MAX_AGE_DAYS"); value > 0 {
		policy.MaxAge = time.Duration(value * float64(24*time.Hour))
	}
	return policy.Normalize()
}

func positiveEnvInt(primary, legacy string, fallback int) int {
	raw := strings.TrimSpace(envCompat(primary, legacy))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveEnvFloat(primary, legacy string) float64 {
	raw := strings.TrimSpace(envCompat(primary, legacy))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func backupStatus(items []backup.Snapshot, policy backup.RetentionPolicy, dataDir string) map[string]any {
	var totalBytes int64
	for _, item := range items {
		totalBytes += item.SizeBytes
	}
	return map[string]any{
		"backups":    items,
		"dir":        backup.DefaultDir(dataDir),
		"count":      len(items),
		"totalBytes": totalBytes,
		"retention": map[string]any{
			"minCount":   policy.MinCount,
			"maxCount":   policy.MaxCount,
			"maxBytes":   policy.MaxBytes,
			"maxAgeDays": policy.MaxAge.Hours() / 24,
		},
	}
}
