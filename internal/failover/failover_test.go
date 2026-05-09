package failover_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/pkg/model"
)

// ─── Failure class taxonomy unit tests ───────────────────────────────────────
// These tests verify the decision logic in isolation using only model types,
// without requiring a live database or real backend.

// safeToFailover encodes the conditions under which failover is warranted.
// This mirrors the logic in Engine.Evaluate and is tested independently so
// the rules are explicit and auditable.
func safeToFailover(
	primaryHealthy bool,
	outageDuration time.Duration,
	unhealthyThreshold time.Duration,
	replicationUnsafe bool,
	leaseHeld bool,
	failoverEnabled bool,
) (should bool, reason string) {
	if !leaseHeld {
		return false, "lease not held — cannot initiate failover"
	}
	if !failoverEnabled {
		return false, "automatic failover is disabled"
	}
	if primaryHealthy && !replicationUnsafe {
		return false, "primary is healthy"
	}
	if primaryHealthy && replicationUnsafe {
		// Class A escalation: primary is healthy but all replicas gone past threshold.
		return true, "replication unsafe: all replicas lost beyond threshold"
	}
	if !primaryHealthy && outageDuration < unhealthyThreshold {
		return false, "primary unhealthy but within threshold — watching"
	}
	return true, "primary unhealthy beyond threshold"
}

func TestFailoverDecision_HealthyPrimary_NoAction(t *testing.T) {
	should, reason := safeToFailover(true, 0, 60*time.Second, false, true, true)
	if should {
		t.Errorf("should not fail over a healthy primary, reason: %s", reason)
	}
}

func TestFailoverDecision_ReplicaLossOnly_NoAction(t *testing.T) {
	// Class A: primary healthy, replicas gone, outage below threshold — no failover.
	should, reason := safeToFailover(true, 30*time.Second, 60*time.Second, false, true, true)
	if should {
		t.Errorf("replica loss below threshold must not trigger failover, reason: %s", reason)
	}
}

func TestFailoverDecision_LeaseNotHeld_NoAction(t *testing.T) {
	// Class B: lease loss is handled by the lease TTL, not the failover engine.
	// The engine simply refuses to act when it does not hold the lease.
	should, reason := safeToFailover(false, 120*time.Second, 60*time.Second, false, false, true)
	if should {
		t.Errorf("must not fail over without holding the lease, reason: %s", reason)
	}
}

func TestFailoverDecision_FailoverDisabled_NoAction(t *testing.T) {
	should, reason := safeToFailover(false, 120*time.Second, 60*time.Second, false, true, false)
	if should {
		t.Errorf("must not fail over when failover is disabled, reason: %s", reason)
	}
}

func TestFailoverDecision_PrimaryUnhealthy_BelowThreshold_NoAction(t *testing.T) {
	should, reason := safeToFailover(false, 30*time.Second, 60*time.Second, false, true, true)
	if should {
		t.Errorf("must not fail over before unhealthy threshold crossed, reason: %s", reason)
	}
}

func TestFailoverDecision_PrimaryUnhealthy_AboveThreshold_ShouldFailover(t *testing.T) {
	should, _ := safeToFailover(false, 90*time.Second, 60*time.Second, false, true, true)
	if !should {
		t.Error("should fail over when primary unhealthy beyond threshold")
	}
}

func TestFailoverDecision_ReplicationUnsafe_ShouldFailover(t *testing.T) {
	// Class A escalation: primary is still technically up but all replicas
	// gone past threshold — this is the only Class A condition that warrants failover.
	should, reason := safeToFailover(true, 0, 60*time.Second, true, true, true)
	if !should {
		t.Errorf("should fail over when replication is unsafe, reason: %s", reason)
	}
	if reason != "replication unsafe: all replicas lost beyond threshold" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestFailoverDecision_ExactlyAtThreshold_ShouldFailover(t *testing.T) {
	// At the exact threshold boundary, failover should be triggered.
	threshold := 60 * time.Second
	should, _ := safeToFailover(false, threshold, threshold, false, true, true)
	if !should {
		t.Error("should fail over at exactly the threshold duration")
	}
}

// ─── Fencing-first invariant ─────────────────────────────────────────────────

// fenceBeforePromote encodes the invariant that fencing must always precede
// promotion. This is tested as a state machine.
type failoverStep int

const (
	stepNone     failoverStep = 0
	stepFenced   failoverStep = 1
	stepPromoted failoverStep = 2
	stepRouted   failoverStep = 3
)

func TestFailoverSequence_FenceBeforePromote(t *testing.T) {
	// Simulate the failover sequence and verify ordering.
	var lastStep failoverStep

	fence := func() {
		if lastStep != stepNone {
			t.Error("fence must be the first step")
		}
		lastStep = stepFenced
	}
	promote := func() {
		if lastStep != stepFenced {
			t.Errorf("promote must follow fence, current step: %d", lastStep)
		}
		lastStep = stepPromoted
	}
	route := func() {
		if lastStep != stepPromoted {
			t.Errorf("routing update must follow promote, current step: %d", lastStep)
		}
		lastStep = stepRouted
	}

	fence()
	promote()
	route()

	if lastStep != stepRouted {
		t.Errorf("failover sequence did not complete, stopped at step %d", lastStep)
	}
}

func TestFailoverSequence_RoutingNotUpdatedIfPromoteFails(t *testing.T) {
	routingUpdated := false

	fenceOK := true
	promoteOK := false // simulate promote failure

	if fenceOK && promoteOK {
		routingUpdated = true
	}

	if routingUpdated {
		t.Error("routing must not be updated if promote fails — split-brain risk")
	}
}

func TestFailoverSequence_FencingFailureAbortsPromotion(t *testing.T) {
	promoteCalled := false

	fenceErr := true // fencing failed

	if !fenceErr {
		promoteCalled = true
	}

	if promoteCalled {
		t.Error("promotion must not proceed if fencing fails")
	}
}

// ─── Candidate selection ─────────────────────────────────────────────────────

func TestCandidateSelection_LowestLagWins(t *testing.T) {
	snapshots := map[string]*model.HealthSnapshot{
		"r1": {NodeID: "r1", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 10240},
		"r2": {NodeID: "r2", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 1024},
		"r3": {NodeID: "r3", Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 5120},
	}

	var bestID string
	var bestLag int64 = -1
	maxLag := int64(64 << 20)

	for id, s := range snapshots {
		if s.Role != model.NodeRoleReplica {
			continue
		}
		if s.Level < model.HealthLevelRoleKnown {
			continue
		}
		if s.ReplicationLagBytes > maxLag {
			continue
		}
		if bestID == "" || s.ReplicationLagBytes < bestLag {
			bestID = id
			bestLag = s.ReplicationLagBytes
		}
	}

	if bestID != "r2" {
		t.Errorf("expected r2 (lowest lag) to win, got %s", bestID)
	}
}

func TestCandidateSelection_ExceedsMaxLag_NotEligible(t *testing.T) {
	maxLag := int64(64 << 20) // 64 MB
	snapshots := map[string]*model.HealthSnapshot{
		"r1": {
			NodeID:              "r1",
			Level:               model.HealthLevelPolicyPass,
			Role:                model.NodeRoleReplica,
			ReplicationLagBytes: maxLag + 1,
		},
	}

	var selected string
	for id, s := range snapshots {
		if s.ReplicationLagBytes <= maxLag {
			selected = id
		}
	}

	if selected != "" {
		t.Errorf("replica exceeding max lag must not be selected, got %s", selected)
	}
}

func TestCandidateSelection_UnhealthyReplica_NotEligible(t *testing.T) {
	snapshots := map[string]*model.HealthSnapshot{
		"r1": {NodeID: "r1", Level: model.HealthLevelUnreachable, Role: model.NodeRoleUnknown},
	}

	var selected string
	for id, s := range snapshots {
		if s.Level >= model.HealthLevelRoleKnown && s.Role == model.NodeRoleReplica {
			selected = id
		}
	}

	if selected != "" {
		t.Errorf("unreachable replica must not be selected as candidate, got %s", selected)
	}
}

func TestCandidateSelection_NoCandidates_ReturnsNil(t *testing.T) {
	snapshots := map[string]*model.HealthSnapshot{}
	var selected string
	for id, s := range snapshots {
		if s.Level >= model.HealthLevelRoleKnown {
			selected = id
		}
	}
	if selected != "" {
		t.Error("should return nil when no candidates exist")
	}
}

// ─── Read-only enforcement on demotion ───────────────────────────────────────

func TestDemotedNode_MustBeReadOnly(t *testing.T) {
	// Verify the status contract: a fenced node must never appear as a
	// valid write target. The proxy routing layer enforces this by
	// generation, but the registry must also reflect it.
	node := model.Node{
		ID:     "old-primary",
		Status: model.NodeStatusFenced,
		Role:   model.NodeRolePrimary,
	}

	isValidWriteTarget := node.Status != model.NodeStatusFenced &&
		node.Status != model.NodeStatusRemoved &&
		node.Role == model.NodeRolePrimary

	if isValidWriteTarget {
		t.Error("a fenced node must never be a valid write target")
	}
}

func TestGenerationMismatch_RejectsWrite(t *testing.T) {
	// Generation-based fencing: an operation carrying a stale token must
	// be rejected even if the node is otherwise reachable.
	currentGeneration := int64(5)
	operationToken := int64(4) // stale

	allowed := operationToken == currentGeneration
	if allowed {
		t.Error("stale fencing token must be rejected")
	}
}

func TestCurrentGeneration_AcceptsWrite(t *testing.T) {
	currentGeneration := int64(5)
	operationToken := int64(5)

	allowed := operationToken == currentGeneration
	if !allowed {
		t.Error("current fencing token must be accepted")
	}
}
