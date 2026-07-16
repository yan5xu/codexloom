package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultMinBackups = 2
	DefaultMaxBackups = 5
	DefaultMaxBytes   = int64(2 << 30)
	DefaultMaxAge     = 30 * 24 * time.Hour
)

// RetentionPolicy bounds local snapshot storage. MinCount is a recovery floor:
// the newest snapshots are retained even when they exceed the byte or age cap.
type RetentionPolicy struct {
	MinCount int
	MaxCount int
	MaxBytes int64
	MaxAge   time.Duration
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		MinCount: DefaultMinBackups,
		MaxCount: DefaultMaxBackups,
		MaxBytes: DefaultMaxBytes,
		MaxAge:   DefaultMaxAge,
	}
}

func (p RetentionPolicy) Normalize() RetentionPolicy {
	if p.MinCount < 0 {
		p.MinCount = 0
	}
	if p.MaxCount < 0 {
		p.MaxCount = 0
	}
	if p.MaxCount > 0 && p.MinCount > p.MaxCount {
		p.MinCount = p.MaxCount
	}
	if p.MaxBytes < 0 {
		p.MaxBytes = 0
	}
	if p.MaxAge < 0 {
		p.MaxAge = 0
	}
	return p
}

type PruneReport struct {
	BeforeCount  int        `json:"beforeCount"`
	AfterCount   int        `json:"afterCount"`
	BeforeBytes  int64      `json:"beforeBytes"`
	AfterBytes   int64      `json:"afterBytes"`
	RemovedCount int        `json:"removedCount"`
	RemovedBytes int64      `json:"removedBytes"`
	Removed      []Snapshot `json:"removed,omitempty"`
}

func (r *PruneReport) merge(other PruneReport) {
	if r.BeforeCount == 0 && r.BeforeBytes == 0 {
		r.BeforeCount = other.BeforeCount
		r.BeforeBytes = other.BeforeBytes
	}
	r.AfterCount = other.AfterCount
	r.AfterBytes = other.AfterBytes
	r.RemovedCount += other.RemovedCount
	r.RemovedBytes += other.RemovedBytes
	r.Removed = append(r.Removed, other.Removed...)
}

// ApplyRetention removes snapshots outside policy, oldest first. It always
// preserves MinCount newest snapshots and reports every file actually removed.
func ApplyRetention(dataDir string, policy RetentionPolicy) (PruneReport, error) {
	return applyRetentionAt(DefaultDir(dataDir), policy, time.Now().UTC())
}

func applyRetentionAt(backupDir string, policy RetentionPolicy, current time.Time) (PruneReport, error) {
	policy = policy.Normalize()
	items, err := listSnapshots(backupDir)
	if err != nil {
		return PruneReport{}, err
	}
	report := PruneReport{BeforeCount: len(items)}
	for _, item := range items {
		report.BeforeBytes += item.SizeBytes
	}

	keptCount := 0
	keptBytes := int64(0)
	var removeErrors []error
	for _, item := range items {
		keep := keptCount < policy.MinCount
		if !keep {
			keep = true
			if policy.MaxCount > 0 && keptCount >= policy.MaxCount {
				keep = false
			}
			if keep && policy.MaxBytes > 0 && keptBytes+item.SizeBytes > policy.MaxBytes {
				keep = false
			}
			if keep && policy.MaxAge > 0 && current.Sub(item.CreatedAt) > policy.MaxAge {
				keep = false
			}
		}
		if keep {
			keptCount++
			keptBytes += item.SizeBytes
			continue
		}
		if err := os.Remove(item.Path); err != nil {
			removeErrors = append(removeErrors, fmt.Errorf("remove %s: %w", filepath.Base(item.Path), err))
			keptCount++
			keptBytes += item.SizeBytes
			continue
		}
		report.Removed = append(report.Removed, item)
		report.RemovedCount++
		report.RemovedBytes += item.SizeBytes
	}
	report.AfterCount = keptCount
	report.AfterBytes = keptBytes
	return report, errors.Join(removeErrors...)
}
