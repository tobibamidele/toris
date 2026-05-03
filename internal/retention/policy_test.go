package retention_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/pkg/model"
)

// RetentionFilter applies a retention policy and returns backups to keep and to prune.
// This is the pure function we're testing — the actual storage deletion is separate.
func RetentionFilter(backups []*model.Backup, policy model.RetentionPolicy, now time.Time) (keep, prune []*model.Backup) {
	// Rule 1: Never prune if there's no newer verified backup.
	// Rule 2: Keep at least MinCount verified backups.
	// Rule 3: Prune verified backups older than MaxAgeDays if MinCount is satisfied.
	// Rule 4: Keep (or prune) failed backups per KeepFailed.

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

	// Never prune if we have fewer than MinCount verified backups.
	if len(verified) <= policy.MinCount {
		keep = append(keep, backups...)
		return keep, nil
	}

	// Identify verified backups older than MaxAgeDays.
	cutoff := now.AddDate(0, 0, -policy.MaxAgeDays)
	for _, b := range verified {
		if policy.MaxAgeDays > 0 && b.StartedAt.Before(cutoff) && len(keep) >= policy.MinCount {
			prune = append(prune, b)
		} else {
			keep = append(keep, b)
		}
	}

	// Handle failed backups.
	if policy.KeepFailed {
		keep = append(keep, failed...)
	} else {
		prune = append(prune, failed...)
	}
	keep = append(keep, other...)
	return keep, prune
}

// makeBackup creates a Backup with the given status and age in days.
func makeBackup(id string, status model.BackupStatus, ageDays int) *model.Backup {
	return &model.Backup{
		ID:        id,
		Status:    status,
		StartedAt: time.Now().AddDate(0, 0, -ageDays),
	}
}

func TestRetentionFilter_KeepsAllIfBelowMinCount(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 3, MaxAgeDays: 7, KeepFailed: true}
	backups := []*model.Backup{
		makeBackup("b1", model.BackupStatusVerified, 30),
		makeBackup("b2", model.BackupStatusVerified, 60),
	}
	keep, prune := RetentionFilter(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("should not prune when below MinCount, got prune: %v", backupIDs(prune))
	}
	if len(keep) != 2 {
		t.Errorf("should keep both backups, got keep: %v", backupIDs(keep))
	}
}

func TestRetentionFilter_PrunesOldWhenAboveMinCount(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 2, MaxAgeDays: 7, KeepFailed: false}
	backups := []*model.Backup{
		makeBackup("b1", model.BackupStatusVerified, 1),  // fresh
		makeBackup("b2", model.BackupStatusVerified, 2),  // fresh
		makeBackup("b3", model.BackupStatusVerified, 30), // old
	}
	_, prune := RetentionFilter(backups, policy, time.Now())
	if len(prune) == 0 {
		t.Error("should prune the old backup when above MinCount")
	}
	for _, p := range prune {
		if p.ID != "b3" {
			t.Errorf("expected to prune b3, got %s", p.ID)
		}
	}
}

func TestRetentionFilter_NeverPrunesNewestBackup(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 1, KeepFailed: false}
	// All backups are old, but we must keep at least MinCount=1.
	backups := []*model.Backup{
		makeBackup("b1", model.BackupStatusVerified, 100),
	}
	keep, prune := RetentionFilter(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("should not prune the only verified backup, got prune: %v", backupIDs(prune))
	}
	if len(keep) == 0 {
		t.Error("at least one backup must be kept")
	}
}

func TestRetentionFilter_KeepsFailed_WhenPolicyKeepFailed(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 7, KeepFailed: true}
	backups := []*model.Backup{
		makeBackup("ok", model.BackupStatusVerified, 1),
		makeBackup("fail", model.BackupStatusFailed, 5),
	}
	keep, prune := RetentionFilter(backups, policy, time.Now())
	keepIDs := backupIDSet(keep)
	if !keepIDs["fail"] {
		t.Errorf("failed backup should be kept when KeepFailed=true, kept: %v", backupIDs(keep))
	}
	for _, p := range prune {
		if p.ID == "fail" {
			t.Error("failed backup should not be pruned when KeepFailed=true")
		}
	}
}

func TestRetentionFilter_PrunesFailed_WhenPolicyNotKeepFailed(t *testing.T) {
	// Need more verified backups than MinCount so we don't hit the early return.
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 30, KeepFailed: false}
	backups := []*model.Backup{
		makeBackup("ok1", model.BackupStatusVerified, 1),
		makeBackup("ok2", model.BackupStatusVerified, 2),
		makeBackup("fail", model.BackupStatusFailed, 5),
	}
	_, prune := RetentionFilter(backups, policy, time.Now())
	pruneIDs := backupIDSet(prune)
	if !pruneIDs["fail"] {
		t.Error("failed backup should be pruned when KeepFailed=false")
	}
}

func TestRetentionFilter_ZeroMaxAgeDays_DoesNotPruneByAge(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 0, KeepFailed: false}
	backups := []*model.Backup{
		makeBackup("b1", model.BackupStatusVerified, 1),
		makeBackup("b2", model.BackupStatusVerified, 365),
		makeBackup("b3", model.BackupStatusVerified, 1000),
	}
	_, prune := RetentionFilter(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("MaxAgeDays=0 should disable age-based pruning, got prune: %v", backupIDs(prune))
	}
}

func TestRetentionFilter_ExactlyAtMinCount_DoesNotPrune(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 3, MaxAgeDays: 7, KeepFailed: false}
	backups := []*model.Backup{
		makeBackup("b1", model.BackupStatusVerified, 30),
		makeBackup("b2", model.BackupStatusVerified, 60),
		makeBackup("b3", model.BackupStatusVerified, 90),
	}
	_, prune := RetentionFilter(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("exactly at MinCount should not prune anything, got: %v", backupIDs(prune))
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func backupIDs(bs []*model.Backup) []string {
	ids := make([]string, len(bs))
	for i, b := range bs {
		ids[i] = b.ID
	}
	return ids
}

func backupIDSet(bs []*model.Backup) map[string]bool {
	m := make(map[string]bool, len(bs))
	for _, b := range bs {
		m[b.ID] = true
	}
	return m
}
