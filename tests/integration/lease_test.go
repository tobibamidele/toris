//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// TestLease_AcquireAndRenew verifies the full lease lifecycle against a
// real PostgreSQL control database.
func TestLease_AcquireAndRenew(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := EnsureControlSchema(ctx, tc.Control); err != nil {
		t.Fatalf("EnsureControlSchema: %v", err)
	}

	lm := leader.New(
		logging.Nop(),
		tc.Control,
		"integration-test-cluster",
		"test-instance-01",
		10*time.Second,
		3*time.Second,
	)

	if err := lm.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// Acquire.
	lease, err := lm.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.Generation < 1 {
		t.Errorf("expected generation >= 1, got %d", lease.Generation)
	}
	if lease.Status != model.LeaseStatusActive {
		t.Errorf("expected status active, got %s", lease.Status)
	}
	if !lm.HoldingLease() {
		t.Error("HoldingLease should be true after Acquire")
	}

	t.Logf("acquired lease generation=%d expires=%s", lease.Generation, lease.ExpiresAt.Format(time.RFC3339))

	// Renew.
	renewed, err := lm.Renew(ctx)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewed.Generation != lease.Generation {
		t.Errorf("generation should not change on renewal: %d vs %d", renewed.Generation, lease.Generation)
	}
	if renewed.ExpiresAt.Before(lease.ExpiresAt) {
		t.Error("renewed expiry should be >= original expiry")
	}

	// Release.
	if err := lm.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if lm.HoldingLease() {
		t.Error("HoldingLease should be false after Release")
	}

	// Cleanup.
	tc.Control.Exec(ctx, `DELETE FROM toris_control.leases WHERE cluster_id = $1`,
		"integration-test-cluster")
}

// TestLease_ConflictPrevention verifies that two instances cannot hold
// the lease simultaneously. The second instance must get a conflict error.
func TestLease_ConflictPrevention(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clusterID := "conflict-test-cluster"

	lm1 := leader.New(logging.Nop(), tc.Control, clusterID, "instance-A",
		30*time.Second, 10*time.Second)
	lm2 := leader.New(logging.Nop(), tc.Control, clusterID, "instance-B",
		30*time.Second, 10*time.Second)

	if err := lm1.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// Instance A acquires.
	if _, err := lm1.Acquire(ctx); err != nil {
		t.Fatalf("instance-A Acquire: %v", err)
	}
	defer lm1.Release(ctx)
	defer tc.Control.Exec(ctx,
		`DELETE FROM toris_control.leases WHERE cluster_id = $1`, clusterID)

	// Instance B should fail.
	_, err := lm2.Acquire(ctx)
	if err == nil {
		lm2.Release(ctx)
		t.Fatal("instance-B should not be able to acquire an active lease")
	}
	t.Logf("instance-B correctly rejected: %v", err)
}

// TestLease_GenerationAdvancesOnTakeover verifies that when an expired
// lease is claimed by a new instance, the generation monotonically increases.
func TestLease_GenerationAdvancesOnTakeover(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clusterID := "generation-test-cluster"

	// Instance A acquires with a very short TTL.
	lm1 := leader.New(logging.Nop(), tc.Control, clusterID, "gen-instance-A",
		2*time.Second, // short TTL
		1*time.Second,
	)
	if err := lm1.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	lease1, err := lm1.Acquire(ctx)
	if err != nil {
		t.Fatalf("gen-instance-A Acquire: %v", err)
	}
	defer tc.Control.Exec(ctx,
		`DELETE FROM toris_control.leases WHERE cluster_id = $1`, clusterID)

	gen1 := lease1.Generation
	t.Logf("instance-A generation: %d", gen1)

	// Wait for the lease to expire.
	time.Sleep(3 * time.Second)

	// Instance B takes over.
	lm2 := leader.New(logging.Nop(), tc.Control, clusterID, "gen-instance-B",
		10*time.Second, 3*time.Second)
	lease2, err := lm2.Acquire(ctx)
	if err != nil {
		t.Fatalf("gen-instance-B Acquire: %v", err)
	}
	defer lm2.Release(ctx)

	gen2 := lease2.Generation
	t.Logf("instance-B generation: %d", gen2)

	if gen2 <= gen1 {
		t.Errorf("generation must advance on takeover: gen1=%d gen2=%d", gen1, gen2)
	}
}
