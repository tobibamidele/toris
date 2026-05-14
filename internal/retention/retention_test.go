package retention_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/retention"
	"github.com/tobibamidele/toris/pkg/model"
)

func backup(id string, status model.BackupStatus, ageDays int) *model.Backup {
	return &model.Backup{
		ID:        id,
		Status:    status,
		StartedAt: time.Now().AddDate(0, 0, -ageDays),
	}
}

func TestClassify_BelowMinCount_KeepsAll(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 3, MaxAgeDays: 7, KeepFailed: true}
	backups := []*model.Backup{
		backup("b1", model.BackupStatusVerified, 30),
		backup("b2", model.BackupStatusVerified, 60),
	}
	keep, prune := retention.Classify(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("should prune nothing below MinCount, got %v", ids(prune))
	}
	if len(keep) != 2 {
		t.Errorf("expected 2 kept, got %d", len(keep))
	}
}

func TestClassify_ExactlyAtMinCount_KeepsAll(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 3, MaxAgeDays: 1, KeepFailed: false}
	backups := []*model.Backup{
		backup("b1", model.BackupStatusVerified, 100),
		backup("b2", model.BackupStatusVerified, 200),
		backup("b3", model.BackupStatusVerified, 300),
	}
	_, prune := retention.Classify(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("should not prune at MinCount, got: %v", ids(prune))
	}
}

func TestClassify_PrunesOldBeyondMinCount(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 2, MaxAgeDays: 7, KeepFailed: false}
	backups := []*model.Backup{
		backup("b1", model.BackupStatusVerified, 1),
		backup("b2", model.BackupStatusVerified, 2),
		backup("b3", model.BackupStatusVerified, 30), // old
		backup("b4", model.BackupStatusVerified, 60), // old
	}
	keep, prune := retention.Classify(backups, policy, time.Now())
	if len(prune) != 2 {
		t.Errorf("expected 2 pruned, got %d: %v", len(prune), ids(prune))
	}
	if len(keep) != 2 {
		t.Errorf("expected 2 kept, got %d: %v", len(keep), ids(keep))
	}
	for _, p := range prune {
		if p.ID != "b3" && p.ID != "b4" {
			t.Errorf("wrong backup pruned: %s", p.ID)
		}
	}
}

func TestClassify_NewestMinCountAlwaysKept(t *testing.T) {
	// Even with MaxAgeDays=1, the MinCount newest backups must survive.
	policy := model.RetentionPolicy{MinCount: 2, MaxAgeDays: 1, KeepFailed: false}
	backups := []*model.Backup{
		backup("newest1", model.BackupStatusVerified, 500),
		backup("newest2", model.BackupStatusVerified, 600),
		backup("old1", model.BackupStatusVerified, 700),
	}
	keep, prune := retention.Classify(backups, policy, time.Now())
	keepIDs := idSet(keep)
	// The two newest (by StartedAt) should be kept.
	if !keepIDs["newest1"] || !keepIDs["newest2"] {
		t.Errorf("newest MinCount backups must always be kept; kept: %v", ids(keep))
	}
	if len(prune) != 1 || prune[0].ID != "old1" {
		t.Errorf("expected old1 pruned, got: %v", ids(prune))
	}
}

func TestClassify_KeepsFailed_WhenPolicyTrue(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 30, KeepFailed: true}
	backups := []*model.Backup{
		backup("ok1", model.BackupStatusVerified, 1),
		backup("ok2", model.BackupStatusVerified, 2),
		backup("fail1", model.BackupStatusFailed, 5),
	}
	keep, prune := retention.Classify(backups, policy, time.Now())
	if idSet(prune)["fail1"] {
		t.Error("failed backup should not be pruned when KeepFailed=true")
	}
	if !idSet(keep)["fail1"] {
		t.Error("failed backup should be kept when KeepFailed=true")
	}
}

func TestClassify_PrunesFailed_WhenPolicyFalse(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 30, KeepFailed: false}
	backups := []*model.Backup{
		backup("ok1", model.BackupStatusVerified, 1),
		backup("ok2", model.BackupStatusVerified, 2),
		backup("fail1", model.BackupStatusFailed, 5),
	}
	_, prune := retention.Classify(backups, policy, time.Now())
	if !idSet(prune)["fail1"] {
		t.Error("failed backup should be pruned when KeepFailed=false")
	}
}

func TestClassify_ZeroMaxAgeDays_NoAgePruning(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 0, KeepFailed: false}
	backups := []*model.Backup{
		backup("b1", model.BackupStatusVerified, 1),
		backup("b2", model.BackupStatusVerified, 365),
		backup("b3", model.BackupStatusVerified, 3650),
	}
	_, prune := retention.Classify(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Errorf("MaxAgeDays=0 should disable age pruning; got prune: %v", ids(prune))
	}
}

func TestClassify_PendingRunning_AlwaysKept(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 1, KeepFailed: false}
	backups := []*model.Backup{
		backup("v1", model.BackupStatusVerified, 1),
		backup("v2", model.BackupStatusVerified, 2),
		backup("pending", model.BackupStatusPending, 90),
		backup("running", model.BackupStatusRunning, 90),
	}
	keep, prune := retention.Classify(backups, policy, time.Now())
	keepSet := idSet(keep)
	if !keepSet["pending"] {
		t.Error("pending backup must never be pruned")
	}
	if !keepSet["running"] {
		t.Error("running backup must never be pruned")
	}
	_ = prune
}

func TestClassify_UploadedStatus_CountsAsVerified(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 7, KeepFailed: false}
	backups := []*model.Backup{
		backup("u1", model.BackupStatusUploaded, 1),
		backup("u2", model.BackupStatusUploaded, 2),
		backup("old", model.BackupStatusUploaded, 30),
	}
	_, prune := retention.Classify(backups, policy, time.Now())
	if !idSet(prune)["old"] {
		t.Error("uploaded backup beyond MaxAgeDays should be pruned when MinCount is satisfied")
	}
}

func TestClassify_SingleBackup_NeverPruned(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 1, MaxAgeDays: 1, KeepFailed: false}
	backups := []*model.Backup{
		backup("only-one", model.BackupStatusVerified, 9999),
	}
	_, prune := retention.Classify(backups, policy, time.Now())
	if len(prune) != 0 {
		t.Error("the only backup must never be pruned regardless of age")
	}
}

func TestClassify_Empty_NoError(t *testing.T) {
	policy := model.RetentionPolicy{MinCount: 3, MaxAgeDays: 7, KeepFailed: true}
	keep, prune := retention.Classify(nil, policy, time.Now())
	if len(keep) != 0 || len(prune) != 0 {
		t.Error("empty input should produce empty output")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func ids(bs []*model.Backup) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func idSet(bs []*model.Backup) map[string]bool {
	m := make(map[string]bool, len(bs))
	for _, b := range bs {
		m[b.ID] = true
	}
	return m
}
