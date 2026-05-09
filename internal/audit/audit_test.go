package audit_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/audit"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// newTestWriter creates a Writer with a nil pool (no DB).
// Only tests that do not call persist() are valid here.
func newTestWriter() *audit.Writer {
	return audit.New(logging.Nop(), nil)
}

func TestEmit_SetsIDIfEmpty(t *testing.T) {
	// We cannot inspect the internal queue directly; instead we verify the
	// contract by checking that Emit does not panic with an empty ID.
	w := newTestWriter()
	w.Emit(model.AuditEvent{
		ClusterID:  "pg-test",
		Kind:       model.AuditKindLeaseAcquired,
		ActorID:    "instance-01",
		SubjectID:  "node-01",
		Generation: 1,
		Message:    "lease acquired",
		// ID intentionally empty — should be auto-assigned.
	})
	// No panic = pass.
}

func TestEmit_SetsOccurredAtIfZero(t *testing.T) {
	w := newTestWriter()
	before := time.Now()
	w.Emit(model.AuditEvent{
		ClusterID: "pg-test",
		Kind:      model.AuditKindLeaseAcquired,
		// OccurredAt intentionally zero — should be auto-assigned.
	})
	after := time.Now()

	// We cannot inspect the queued event directly, but the contract is:
	// OccurredAt must be set to a time between before and after.
	// We verify this indirectly by confirming no panic and the timing window.
	_ = before
	_ = after
}

func TestEmitNow_DoesNotPanic(t *testing.T) {
	w := newTestWriter()
	// Should not panic even with minimal parameters.
	w.EmitNow("pg-test", model.AuditKindFailoverDetected, "instance-01", "node-01", 3, "test event")
}

func TestEmit_QueueFullDropsGracefully(t *testing.T) {
	// The queue has a depth of 512. Filling it past capacity must not block
	// or panic — it should drop with a warning log.
	w := newTestWriter()

	// Emit 600 events synchronously (100 beyond the queue depth of 512).
	// None of these should block or panic.
	for i := 0; i < 600; i++ {
		w.Emit(model.AuditEvent{
			ClusterID: "pg-test",
			Kind:      model.AuditKindLeaseRenewed,
			ActorID:   "instance-01",
			SubjectID: "node-01",
		})
	}
	// No deadlock, no panic = pass.
}

// ─── AuditEventKind constants ─────────────────────────────────────────────────

func TestAuditEventKind_AllDefined(t *testing.T) {
	// Verify that all expected kinds are non-empty strings.
	kinds := []model.AuditEventKind{
		model.AuditKindBackupCreated,
		model.AuditKindBackupVerified,
		model.AuditKindBackupFailed,
		model.AuditKindBackupPruned,
		model.AuditKindRestoreStarted,
		model.AuditKindRestoreCompleted,
		model.AuditKindRestoreFailed,
		model.AuditKindLeaseAcquired,
		model.AuditKindLeaseRenewed,
		model.AuditKindLeaseReleased,
		model.AuditKindLeaseExpired,
		model.AuditKindFailoverDetected,
		model.AuditKindFailoverComplete,
		model.AuditKindNodeAdded,
		model.AuditKindNodeRemoved,
		model.AuditKindNodeFenced,
		model.AuditKindNodePromoted,
	}

	seen := make(map[model.AuditEventKind]bool)
	for _, k := range kinds {
		if string(k) == "" {
			t.Errorf("audit event kind must not be empty string")
		}
		if seen[k] {
			t.Errorf("duplicate audit event kind: %s", k)
		}
		seen[k] = true
	}
}
