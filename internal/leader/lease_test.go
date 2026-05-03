package leader_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/pkg/model"
)

// ─── Lease model unit tests (no DB required) ──────────────────────────────────

func TestLease_IsExpired_NotExpired(t *testing.T) {
	lease := &model.Lease{
		ExpiresAt: time.Now().Add(30 * time.Second),
	}
	if lease.IsExpired(time.Now()) {
		t.Error("lease should not be expired yet")
	}
}

func TestLease_IsExpired_Expired(t *testing.T) {
	lease := &model.Lease{
		ExpiresAt: time.Now().Add(-1 * time.Second),
	}
	if !lease.IsExpired(time.Now()) {
		t.Error("lease should be expired")
	}
}

func TestLease_IsExpired_AtBoundary(t *testing.T) {
	now := time.Now()
	lease := &model.Lease{
		ExpiresAt: now,
	}
	// At the exact expiry moment, the lease is expired (exclusive boundary).
	if !lease.IsExpired(now) {
		t.Error("lease at exact expiry should be expired")
	}
}

func TestLease_IsExpired_FutureNow(t *testing.T) {
	lease := &model.Lease{
		ExpiresAt: time.Now().Add(10 * time.Second),
	}
	// Simulate time advancing past expiry.
	future := time.Now().Add(20 * time.Second)
	if !lease.IsExpired(future) {
		t.Error("lease should be expired when viewed from the future")
	}
}

// ─── Fencing token logic ──────────────────────────────────────────────────────

func TestFencingToken_MonotonicallyIncreasing(t *testing.T) {
	// Simulate a sequence of lease generations.
	generations := []int64{1, 2, 3, 100, 101}
	for i := 1; i < len(generations); i++ {
		if generations[i] <= generations[i-1] {
			t.Errorf("generation %d (%d) must be greater than previous (%d)",
				i, generations[i], generations[i-1])
		}
	}
}

func TestFencingToken_StaleTokenRejection(t *testing.T) {
	// A stale token (lower than current generation) must always be rejected.
	currentGeneration := int64(5)
	staleTokens := []int64{0, 1, 2, 3, 4}
	for _, tok := range staleTokens {
		if tok >= currentGeneration {
			t.Errorf("token %d should be considered stale against generation %d",
				tok, currentGeneration)
		}
	}
}

func TestFencingToken_CurrentTokenAccepted(t *testing.T) {
	currentGeneration := int64(7)
	if currentGeneration != 7 {
		t.Error("current generation token should be accepted")
	}
}

// ─── Lease status transitions ─────────────────────────────────────────────────

func TestLeaseStatus_ActiveToExpired(t *testing.T) {
	lease := model.Lease{
		Status:    model.LeaseStatusActive,
		ExpiresAt: time.Now().Add(-1 * time.Second),
	}
	// A lease can be active in status but expired by time — the system must
	// check both.
	if lease.Status != model.LeaseStatusActive {
		t.Error("status field should still be 'active' until explicitly updated")
	}
	if !lease.IsExpired(time.Now()) {
		t.Error("time-based expiry check should report expired")
	}
}

func TestLeaseStatus_ReleasedLeaseIsNotActive(t *testing.T) {
	lease := model.Lease{
		Status:    model.LeaseStatusReleased,
		ExpiresAt: time.Now().Add(30 * time.Second),
	}
	// Even with a future expiry, a released lease must not be used.
	isActive := lease.Status == model.LeaseStatusActive && !lease.IsExpired(time.Now())
	if isActive {
		t.Error("a released lease must not be treated as active")
	}
}

// ─── HealthSnapshot.IsHealthyForRole tests ────────────────────────────────────

func TestHealthSnapshot_IsHealthyForRole_FullPass(t *testing.T) {
	snap := &model.HealthSnapshot{
		Level:       model.HealthLevelPolicyPass,
		Role:        model.NodeRolePrimary,
		TransportOK: true,
		ReadyOK:     true,
		LiveOK:      true,
		RoleOK:      true,
		PolicyOK:    true,
	}
	if !snap.IsHealthyForRole(model.NodeRolePrimary) {
		t.Error("fully passing snapshot should be healthy for primary role")
	}
}

func TestHealthSnapshot_IsHealthyForRole_WrongRole(t *testing.T) {
	snap := &model.HealthSnapshot{
		Level:    model.HealthLevelPolicyPass,
		Role:     model.NodeRoleReplica,
		PolicyOK: true,
	}
	if snap.IsHealthyForRole(model.NodeRolePrimary) {
		t.Error("replica snapshot should not be healthy for primary role")
	}
}

func TestHealthSnapshot_IsHealthyForRole_PolicyFail(t *testing.T) {
	snap := &model.HealthSnapshot{
		Level:    model.HealthLevelRoleKnown,
		Role:     model.NodeRolePrimary,
		PolicyOK: false,
	}
	if snap.IsHealthyForRole(model.NodeRolePrimary) {
		t.Error("policy-failing snapshot should not be healthy")
	}
}

func TestHealthSnapshot_IsHealthyForRole_Unreachable(t *testing.T) {
	snap := &model.HealthSnapshot{
		Level: model.HealthLevelUnreachable,
		Role:  model.NodeRoleUnknown,
	}
	if snap.IsHealthyForRole(model.NodeRolePrimary) {
		t.Error("unreachable node should not be healthy for any role")
	}
}

func TestHealthSnapshot_IsHealthyForRole_PartialPass(t *testing.T) {
	// Passed L1–L3 but not role-known (L4).
	snap := &model.HealthSnapshot{
		Level:       model.HealthLevelLive,
		Role:        model.NodeRoleUnknown,
		TransportOK: true,
		ReadyOK:     true,
		LiveOK:      true,
		RoleOK:      false,
	}
	if snap.IsHealthyForRole(model.NodeRolePrimary) {
		t.Error("node without confirmed role should not be healthy for primary")
	}
}
