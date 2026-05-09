package health_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/health"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

func newTracker(threshold time.Duration, maxLag int64) *health.Tracker {
	return health.NewTracker(logging.Nop(), threshold, maxLag)
}

// ─── Class A: replica connectivity loss ──────────────────────────────────────

func TestTracker_HealthyPrimary_AllReplicasConnected(t *testing.T) {
	tr := newTracker(60*time.Second, 64<<20)
	replicas := []*model.HealthSnapshot{
		{NodeID: "r1", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 1024},
		{NodeID: "r2", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 2048},
	}

	rh := tr.Record("primary-1", replicas, 2)

	if rh.IsDegraded {
		t.Error("should not be degraded when all replicas are connected")
	}
	if rh.IsUnsafe {
		t.Error("should not be unsafe when all replicas are connected")
	}
	if rh.OutageSince != nil {
		t.Error("OutageSince should be nil when replicas are connected")
	}
	if rh.ConnectedReplicas != 2 {
		t.Errorf("expected 2 connected replicas, got %d", rh.ConnectedReplicas)
	}
}

func TestTracker_PartialReplicaLoss_DegradedNotUnsafe(t *testing.T) {
	// Class A rule: partial replica loss → DEGRADED, not unsafe, no demotion.
	tr := newTracker(60*time.Second, 64<<20)
	replicas := []*model.HealthSnapshot{
		{NodeID: "r1", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 0},
		// r2 is unreachable
		{NodeID: "r2", Level: model.HealthLevelUnreachable, Role: model.NodeRoleUnknown},
	}

	rh := tr.Record("primary-1", replicas, 2)

	if !rh.IsDegraded {
		t.Error("should be degraded when a replica is unreachable")
	}
	if rh.IsUnsafe {
		t.Error("partial replica loss must never be unsafe — Class A rule")
	}
}

func TestTracker_AllReplicasLost_StartsOutageTimer(t *testing.T) {
	tr := newTracker(60*time.Second, 64<<20)
	replicas := []*model.HealthSnapshot{
		{NodeID: "r1", Level: model.HealthLevelUnreachable},
		{NodeID: "r2", Level: model.HealthLevelUnreachable},
	}

	rh := tr.Record("primary-1", replicas, 2)

	if rh.OutageSince == nil {
		t.Error("OutageSince should be set when all replicas are lost")
	}
	// Has not crossed threshold yet.
	if rh.IsUnsafe {
		t.Error("should not be unsafe immediately — threshold has not been crossed")
	}
}

func TestTracker_AllReplicasLost_BelowThreshold_NotUnsafe(t *testing.T) {
	// Class A: even with all replicas gone, we do not escalate until the
	// outage crosses the configured threshold.
	tr := newTracker(5*time.Minute, 64<<20)
	empty := []*model.HealthSnapshot{}

	// First record sets OutageSince to now.
	tr.Record("primary-1", empty, 2)

	// Second record shortly after — still below threshold.
	rh := tr.Record("primary-1", empty, 2)

	if rh.IsUnsafe {
		t.Error("should not be unsafe before threshold is crossed")
	}
	if !rh.IsDegraded {
		t.Error("should be degraded with zero connected replicas")
	}
}

func TestTracker_OutageOutlastsThreshold_BecomesUnsafe(t *testing.T) {
	// Use a very short threshold so we can test without sleeping.
	tr := newTracker(1*time.Millisecond, 64<<20)
	empty := []*model.HealthSnapshot{}

	tr.Record("primary-1", empty, 2)
	time.Sleep(5 * time.Millisecond) // outlast the 1ms threshold
	rh := tr.Record("primary-1", empty, 2)

	if !rh.IsUnsafe {
		t.Error("should be unsafe after threshold is crossed")
	}
}

func TestTracker_OutageOutlastsThreshold_IsSafeToKeepPrimaryReturnsFalse(t *testing.T) {
	tr := newTracker(1*time.Millisecond, 64<<20)
	empty := []*model.HealthSnapshot{}

	tr.Record("primary-1", empty, 2)
	time.Sleep(5 * time.Millisecond)
	tr.Record("primary-1", empty, 2)

	if tr.IsSafeToKeepPrimary("primary-1") {
		t.Error("IsSafeToKeepPrimary should return false after threshold crossed")
	}
}

func TestTracker_ConnectivityRestored_ResetsOutageTimer(t *testing.T) {
	tr := newTracker(1*time.Millisecond, 64<<20)
	empty := []*model.HealthSnapshot{}
	healthy := []*model.HealthSnapshot{
		{NodeID: "r1", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica},
	}

	tr.Record("primary-1", empty, 1)
	time.Sleep(5 * time.Millisecond)
	tr.Record("primary-1", empty, 1) // crosses threshold

	// Now replica reconnects.
	rh := tr.Record("primary-1", healthy, 1)

	if rh.IsUnsafe {
		t.Error("should not be unsafe after connectivity is restored")
	}
	if rh.OutageSince != nil {
		t.Error("OutageSince should be nil after connectivity is restored")
	}
	if tr.IsSafeToKeepPrimary("primary-1") == false {
		t.Error("IsSafeToKeepPrimary should return true after connectivity restored")
	}
}

func TestTracker_LagExceedingMax_ReplicaNotCounted(t *testing.T) {
	maxLag := int64(64 << 20) // 64 MB
	tr := newTracker(60*time.Second, maxLag)

	replicas := []*model.HealthSnapshot{
		{
			NodeID:              "r1",
			Level:               model.HealthLevelPolicyPass,
			Role:                model.NodeRoleReplica,
			ReplicationLagBytes: maxLag + 1, // exceeds limit
		},
	}

	rh := tr.Record("primary-1", replicas, 1)

	// Replica is reachable but lag is too high — not counted as connected.
	if rh.ConnectedReplicas != 0 {
		t.Errorf("replica with lag exceeding max should not be counted as connected, got %d", rh.ConnectedReplicas)
	}
	if rh.OutageSince == nil {
		t.Error("OutageSince should be set when no replica is within lag tolerance")
	}
}

func TestTracker_GetReturnsNilForUnknownNode(t *testing.T) {
	tr := newTracker(60*time.Second, 64<<20)
	if tr.Get("nonexistent") != nil {
		t.Error("Get should return nil for an unknown node")
	}
}

func TestTracker_IsSafeToKeepPrimary_NoDataAssumeSafe(t *testing.T) {
	tr := newTracker(60*time.Second, 64<<20)
	// No data yet — must assume safe until evidence arrives.
	if !tr.IsSafeToKeepPrimary("primary-1") {
		t.Error("IsSafeToKeepPrimary should return true when no data is present")
	}
}

func TestTracker_Get_ReturnsCopy(t *testing.T) {
	tr := newTracker(60*time.Second, 64<<20)
	replicas := []*model.HealthSnapshot{
		{NodeID: "r1", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica},
	}
	tr.Record("primary-1", replicas, 1)

	rh1 := tr.Get("primary-1")
	rh2 := tr.Get("primary-1")

	if rh1 == rh2 {
		t.Error("Get should return independent copies, not the same pointer")
	}
}

// ─── Class B: lease loss is independent of replica health ────────────────────

func TestTracker_DoesNotTouchLease(t *testing.T) {
	// The Tracker must not have any dependency on or effect on the lease.
	// This is verified structurally: Tracker has no leader.Manager field.
	// The test documents the contract by confirming Tracker can be used
	// standalone without any lease machinery.
	tr := newTracker(60*time.Second, 64<<20)
	empty := []*model.HealthSnapshot{}

	// This must not panic or have any lease side effect.
	rh := tr.Record("primary-1", empty, 2)
	if rh == nil {
		t.Error("Record should always return a non-nil result")
	}
}
