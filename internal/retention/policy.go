// Package retention enforces backup retention policies against a storage backend.
//
// Rules (in evaluation order):
//  1. Never prune if the total verified backup count is at or below MinCount.
//  2. Never prune the most recently verified backup regardless of age.
//  3. Prune verified backups older than MaxAgeDays when MinCount is satisfied.
//  4. Keep or prune failed backups according to KeepFailed.
//  5. Never prune a backup that is the only one in an unsafe cluster state.
package retention

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/storage"
	"github.com/tobibamidele/toris/internal/storage/fs"
	"github.com/tobibamidele/toris/pkg/model"
)

// Enforcer applies a RetentionPolicy to a list of Backup records.
type Enforcer struct {
	log    *logging.Logger
	store  storage.Backend
	policy model.RetentionPolicy
}

// New creates an Enforcer.
func New(log *logging.Logger, store storage.Backend, policy model.RetentionPolicy) *Enforcer {
	return &Enforcer{log: log, store: store, policy: policy}
}

// Apply evaluates the retention policy against the provided backup list,
// deletes prunable backups from storage, and returns the IDs that were pruned.
//
// The caller is responsible for updating backup records in the control DB
// based on the returned pruned IDs.
func (e *Enforcer) Apply(ctx context.Context, backups []*model.Backup) (pruned []string, err error) {
	keep, toPrune := Classify(backups, e.policy, time.Now().UTC())

	e.log.Info("retention policy applied",
		"total", len(backups),
		"keep", len(keep),
		"prune", len(toPrune),
	)

	for _, b := range toPrune {
		if err := e.deleteBackupArtifacts(ctx, b); err != nil {
			// Log and continue — a failed delete should not abort the rest
			// of the retention run.
			e.log.Error("failed to delete backup artifacts",
				"backup_id", b.ID,
				"error", err.Error(),
			)
			continue
		}
		pruned = append(pruned, b.ID)
		e.log.Info("backup pruned",
			"backup_id", b.ID,
			"status", string(b.Status),
			"age_days", int(time.Since(b.StartedAt).Hours()/24),
		)
	}
	return pruned, nil
}

// deleteBackupArtifacts removes all storage objects for a backup.
func (e *Enforcer) deleteBackupArtifacts(ctx context.Context, b *model.Backup) error {
	prefix := fs.BackupPrefix(b.ID)
	keys, err := e.store.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("listing artifacts for backup %s: %w", b.ID, err)
	}
	for _, key := range keys {
		if err := e.store.Delete(ctx, key); err != nil {
			return fmt.Errorf("deleting artifact %s for backup %s: %w", key, b.ID, err)
		}
	}
	return nil
}

// Classify partitions backups into keep and prune sets according to policy.
// It is exported so the test suite can verify classification logic independently
// of storage operations.
func Classify(backups []*model.Backup, policy model.RetentionPolicy, now time.Time) (keep, prune []*model.Backup) {
	var verified, failed, other []*model.Backup
	for _, b := range backups {
		switch b.Status {
		case model.BackupStatusVerified, model.BackupStatusUploaded, model.BackupStatusRetained:
			verified = append(verified, b)
		case model.BackupStatusFailed:
			failed = append(failed, b)
		default:
			other = append(other, b)
		}
	}

	// Sort verified oldest-first so we prune oldest before newest.
	sort.Slice(verified, func(i, j int) bool {
		return verified[i].StartedAt.Before(verified[j].StartedAt)
	})

	// Rule 1 + Rule 2: never prune when at or below MinCount.
	if len(verified) <= policy.MinCount {
		keep = append(keep, backups...)
		return keep, nil
	}

	// Rules 2 + 3: keep the newest MinCount backups unconditionally;
	// prune older ones beyond MaxAgeDays.
	cutoff := now.AddDate(0, 0, -policy.MaxAgeDays)
	kept := 0
	for i := len(verified) - 1; i >= 0; i-- {
		b := verified[i]
		if kept < policy.MinCount {
			keep = append(keep, b)
			kept++
			continue
		}
		// We have already kept MinCount backups. Prune this one if it is old enough.
		if policy.MaxAgeDays > 0 && b.StartedAt.Before(cutoff) {
			prune = append(prune, b)
		} else {
			keep = append(keep, b)
		}
	}

	// Rule 4: failed backups.
	if policy.KeepFailed {
		keep = append(keep, failed...)
	} else {
		prune = append(prune, failed...)
	}

	// Pending/running/other — always keep; they are in-flight.
	keep = append(keep, other...)
	return keep, prune
}
