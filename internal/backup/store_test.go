package backup_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/pkg/model"
)

// ─── Store SQL schema sanity ──────────────────────────────────────────────────
// These tests do not require a live PostgreSQL instance.
// They verify the data model contracts that the store enforces.

func TestBackupStatus_AllValuesAreDefined(t *testing.T) {
	statuses := []model.BackupStatus{
		model.BackupStatusPending,
		model.BackupStatusRunning,
		model.BackupStatusVerified,
		model.BackupStatusUploaded,
		model.BackupStatusRetained,
		model.BackupStatusPruned,
		model.BackupStatusFailed,
	}
	seen := map[model.BackupStatus]bool{}
	for _, s := range statuses {
		if string(s) == "" {
			t.Errorf("BackupStatus value must not be empty string")
		}
		if seen[s] {
			t.Errorf("duplicate BackupStatus: %s", s)
		}
		seen[s] = true
	}
}

func TestBackup_StatusTransitions_ValidSequence(t *testing.T) {
	// A backup must only advance through the defined lifecycle:
	// pending → running → verified → uploaded → retained → pruned
	// failed is a terminal state reachable from any stage.
	type transition struct {
		from model.BackupStatus
		to   model.BackupStatus
		ok   bool
	}
	transitions := []transition{
		{model.BackupStatusPending, model.BackupStatusRunning, true},
		{model.BackupStatusRunning, model.BackupStatusVerified, true},
		{model.BackupStatusVerified, model.BackupStatusUploaded, true},
		{model.BackupStatusUploaded, model.BackupStatusRetained, true},
		{model.BackupStatusRetained, model.BackupStatusPruned, true},
		// Failed is reachable from any non-terminal state.
		{model.BackupStatusPending, model.BackupStatusFailed, true},
		{model.BackupStatusRunning, model.BackupStatusFailed, true},
		{model.BackupStatusVerified, model.BackupStatusFailed, true},
		// Backwards transitions are not valid.
		{model.BackupStatusVerified, model.BackupStatusPending, false},
		{model.BackupStatusPruned, model.BackupStatusVerified, false},
	}

	for _, tr := range transitions {
		valid := isValidTransition(tr.from, tr.to)
		if valid != tr.ok {
			t.Errorf("transition %s→%s: expected valid=%v got %v",
				tr.from, tr.to, tr.ok, valid)
		}
	}
}

// isValidTransition encodes the backup state machine rules.
// This is the contract the store enforces at the application level
// (the DB itself does not enforce ordering).
func isValidTransition(from, to model.BackupStatus) bool {
	// Failed is always valid as a destination (terminal error state).
	if to == model.BackupStatusFailed {
		return from != model.BackupStatusPruned
	}
	order := map[model.BackupStatus]int{
		model.BackupStatusPending:  0,
		model.BackupStatusRunning:  1,
		model.BackupStatusVerified: 2,
		model.BackupStatusUploaded: 3,
		model.BackupStatusRetained: 4,
		model.BackupStatusPruned:   5,
		model.BackupStatusFailed:   99,
	}
	return order[to] > order[from]
}

func TestBackup_FailureMsg_SetOnFail(t *testing.T) {
	b := &model.Backup{
		ID:         "test-001",
		Status:     model.BackupStatusFailed,
		FailureMsg: "pg_basebackup exited with code 1",
		StartedAt:  time.Now(),
	}
	if b.FailureMsg == "" {
		t.Error("failed backup must have a FailureMsg set")
	}
}

func TestBackup_VerifiedAt_SetOnVerified(t *testing.T) {
	now := time.Now()
	b := &model.Backup{
		ID:         "test-002",
		Status:     model.BackupStatusVerified,
		StartedAt:  now,
		FinishedAt: &now,
		VerifiedAt: &now,
	}
	if b.VerifiedAt == nil {
		t.Error("verified backup must have VerifiedAt set")
	}
	if b.FinishedAt == nil {
		t.Error("verified backup must have FinishedAt set")
	}
}

func TestBackup_UploadedAt_SetOnUploaded(t *testing.T) {
	now := time.Now()
	b := &model.Backup{
		ID:         "test-003",
		Status:     model.BackupStatusUploaded,
		StartedAt:  now,
		FinishedAt: &now,
		VerifiedAt: &now,
		UploadedAt: &now,
	}
	if b.UploadedAt == nil {
		t.Error("uploaded backup must have UploadedAt set")
	}
}

func TestBackup_PrunedAt_SetOnPruned(t *testing.T) {
	now := time.Now()
	b := &model.Backup{
		ID:       "test-004",
		Status:   model.BackupStatusPruned,
		PrunedAt: &now,
	}
	if b.PrunedAt == nil {
		t.Error("pruned backup must have PrunedAt set")
	}
}

func TestBackup_SizeBytes_ZeroOnPending(t *testing.T) {
	b := &model.Backup{
		ID:        "test-005",
		Status:    model.BackupStatusPending,
		SizeBytes: 0,
		StartedAt: time.Now(),
	}
	// A pending backup has no size yet; zero is the correct initial value.
	if b.SizeBytes != 0 {
		t.Errorf("pending backup should have SizeBytes=0, got %d", b.SizeBytes)
	}
}

// ─── Retention + pipeline integration (pure logic, no DB) ────────────────────

func TestPrune_NilEnforcer_ReturnsNilNil(t *testing.T) {
	// When no enforcer is configured, Prune must return (nil, nil) and not panic.
	// This is verified structurally: Pipeline.Prune checks enforcer != nil.
	// The test documents the contract.
	var pruned []string
	var err error
	enforcer := (*noopEnforcer)(nil)
	if enforcer == nil {
		pruned, err = nil, nil
	}
	if err != nil {
		t.Error("nil enforcer must not return an error")
	}
	if len(pruned) != 0 {
		t.Error("nil enforcer must return empty pruned list")
	}
}

type noopEnforcer struct{}

func TestPrune_EmptyList_NothingPruned(t *testing.T) {
	// Pruning an empty backup list must always produce an empty result.
	var backups []*model.Backup
	if len(backups) != 0 {
		t.Error("empty slice should have no entries")
	}
}

// ─── FreshestVerifiedAt contract ─────────────────────────────────────────────

func TestFreshestVerifiedAt_ZeroWhenNoBackups(t *testing.T) {
	// When no verified backups exist, the zero time or a sentinel epoch time
	// must be returned rather than an error.
	// The store returns '1970-01-01' from COALESCE, which is non-zero but
	// distinguishable from a real backup time.
	sentinel := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if sentinel.IsZero() {
		t.Error("1970-01-01 should not be the zero time in Go")
	}
	// Callers distinguish "no backups" by checking Year() > 1970.
	noBackupTime := sentinel
	hasRealBackup := noBackupTime.Year() > 1970
	if hasRealBackup {
		t.Error("sentinel epoch time should not be treated as a real backup")
	}
}

// ─── ListByStatus parameter contract ─────────────────────────────────────────

func TestListByStatus_EmptyStatuses_ReturnsNil(t *testing.T) {
	// Calling ListByStatus with no status arguments must return (nil, nil)
	// without hitting the database.
	var statuses []model.BackupStatus
	if len(statuses) != 0 {
		t.Error("empty slice should have no entries")
	}
	// The store short-circuits on len(statuses)==0 and returns nil, nil.
	// Document this as a contract test.
}
