package httpapi

import (
	"testing"
	"time"

	"github.com/yan5xu/codex-loom/internal/backup"
)

func TestBackupRetentionPolicyDefaults(t *testing.T) {
	clearBackupPolicyEnv(t)
	policy := backupRetentionPolicy()
	if policy.MinCount != backup.DefaultMinBackups || policy.MaxCount != backup.DefaultMaxBackups {
		t.Fatalf("unexpected count policy: %+v", policy)
	}
	if policy.MaxBytes != backup.DefaultMaxBytes || policy.MaxAge != backup.DefaultMaxAge {
		t.Fatalf("unexpected size/age policy: %+v", policy)
	}
}

func TestBackupRetentionPolicyReadsEnvironmentAndNormalizes(t *testing.T) {
	clearBackupPolicyEnv(t)
	t.Setenv("CODEX_LOOM_BACKUP_MIN_KEEP", "4")
	t.Setenv("CODEX_LOOM_BACKUP_KEEP", "3")
	t.Setenv("CODEX_LOOM_BACKUP_MAX_GB", "1.5")
	t.Setenv("CODEX_LOOM_BACKUP_MAX_AGE_DAYS", "7.5")

	policy := backupRetentionPolicy()
	if policy.MinCount != 3 || policy.MaxCount != 3 {
		t.Fatalf("count policy was not normalized: %+v", policy)
	}
	if policy.MaxBytes != int64(1.5*float64(1<<30)) {
		t.Fatalf("max bytes = %d", policy.MaxBytes)
	}
	if policy.MaxAge != time.Duration(7.5*float64(24*time.Hour)) {
		t.Fatalf("max age = %s", policy.MaxAge)
	}
}

func clearBackupPolicyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CODEX_LOOM_BACKUP_MIN_KEEP", "CODEX_HUB_BACKUP_MIN_KEEP",
		"CODEX_LOOM_BACKUP_KEEP", "CODEX_HUB_BACKUP_KEEP",
		"CODEX_LOOM_BACKUP_MAX_GB", "CODEX_HUB_BACKUP_MAX_GB",
		"CODEX_LOOM_BACKUP_MAX_AGE_DAYS", "CODEX_HUB_BACKUP_MAX_AGE_DAYS",
	} {
		t.Setenv(key, "")
	}
}
