//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/db/postgres"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// TestHealth_L1_Transport verifies that the health checker detects
// TCP reachability for all cluster nodes.
func TestHealth_L1_Transport(t *testing.T) {
	tc := NewTestCluster(t)
	_ = tc // connections verified by NewTestCluster

	backend := postgres.New(logging.Nop())
	defer backend.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	nodes := []*model.Node{
		{ID: "primary", Host: "localhost", Port: 5441},
		{ID: "replica-1", Host: "localhost", Port: 5442},
		{ID: "replica-2", Host: "localhost", Port: 5443},
	}

	for _, node := range nodes {
		snap, err := backend.Health(ctx, node)
		if err != nil {
			t.Errorf("Health(%s) returned error: %v", node.ID, err)
			continue
		}
		if snap == nil {
			t.Errorf("Health(%s) returned nil snapshot", node.ID)
			continue
		}
		if !snap.TransportOK {
			t.Errorf("node %s: expected TransportOK=true, got false (errors: %v)", node.ID, snap.Errors)
		}
		if snap.Level < model.HealthLevelTransport {
			t.Errorf("node %s: expected level >= %d, got %d", node.ID, model.HealthLevelTransport, snap.Level)
		}
		t.Logf("node %s: level=%d role=%s", node.ID, snap.Level, snap.Role)
	}
}

// TestHealth_L4_RoleDetection verifies that the health checker correctly
// identifies the primary and replicas using pg_is_in_recovery().
func TestHealth_L4_RoleDetection(t *testing.T) {
	tc := NewTestCluster(t)

	backend := postgres.New(logging.Nop())
	defer backend.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	primaryNode := &model.Node{ID: "primary", Host: "localhost", Port: 5441}
	replicaNode := &model.Node{ID: "replica-1", Host: "localhost", Port: 5442}

	// Verify primary role.
	primarySnap, err := backend.Health(ctx, primaryNode)
	if err != nil {
		t.Fatalf("Health(primary) error: %v", err)
	}
	if primarySnap.Level < model.HealthLevelRoleKnown {
		t.Fatalf("primary did not reach L4, level=%d errors=%v", primarySnap.Level, primarySnap.Errors)
	}
	if primarySnap.Role != model.NodeRolePrimary {
		t.Errorf("expected primary role, got %s", primarySnap.Role)
	}
	if primarySnap.IsInRecovery {
		t.Error("primary should not be in recovery")
	}

	// Verify replica role.
	// Give replica time to connect if it just started.
	WaitForReplication(t, tc.Replica1, 30*time.Second)

	replicaSnap, err := backend.Health(ctx, replicaNode)
	if err != nil {
		t.Fatalf("Health(replica-1) error: %v", err)
	}
	if replicaSnap.Level < model.HealthLevelRoleKnown {
		t.Fatalf("replica-1 did not reach L4, level=%d errors=%v", replicaSnap.Level, replicaSnap.Errors)
	}
	if replicaSnap.Role != model.NodeRoleReplica {
		t.Errorf("expected replica role, got %s", replicaSnap.Role)
	}
	if !replicaSnap.IsInRecovery {
		t.Error("replica should be in recovery")
	}
}

// TestHealth_L5_PolicyPass verifies that both primary and replica pass
// the full L5 health check under normal conditions.
func TestHealth_L5_PolicyPass(t *testing.T) {
	tc := NewTestCluster(t)
	_ = tc

	WaitForReplication(t, tc.Replica1, 30*time.Second)
	WaitForReplication(t, tc.Replica2, 30*time.Second)

	backend := postgres.New(logging.Nop())
	defer backend.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nodes := []*model.Node{
		{ID: "primary", Host: "localhost", Port: 5441},
		{ID: "replica-1", Host: "localhost", Port: 5442},
		{ID: "replica-2", Host: "localhost", Port: 5443},
	}

	for _, node := range nodes {
		snap, err := backend.Health(ctx, node)
		if err != nil {
			t.Errorf("Health(%s) error: %v", node.ID, err)
			continue
		}
		if snap.Level < model.HealthLevelPolicyPass {
			t.Errorf("node %s did not reach L5 (level=%d, errors=%v)", node.ID, snap.Level, snap.Errors)
		}
		t.Logf("node %s: L%d role=%s lag=%d", node.ID, snap.Level, snap.Role, snap.ReplicationLagBytes)
	}
}

// TestHealth_ReplicationLag verifies that replica lag is measurable
// and within acceptable bounds under normal conditions.
func TestHealth_ReplicationLag(t *testing.T) {
	tc := NewTestCluster(t)

	WaitForReplication(t, tc.Replica1, 30*time.Second)

	// Write a batch to the primary to create some WAL.
	ExecSQL(t, tc.Primary, `
		CREATE TABLE IF NOT EXISTS lag_test (id SERIAL, data TEXT);
	`)
	for i := 0; i < 100; i++ {
		ExecSQL(t, tc.Primary, `INSERT INTO lag_test(data) VALUES($1)`, fmt.Sprintf("row-%d", i))
	}

	// Wait for replica to catch up.
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	lag, err := ReplicationLag(ctx, tc.Replica1)
	if err != nil {
		t.Fatalf("querying replica lag: %v", err)
	}

	maxAcceptableLag := int64(10 * 1024 * 1024) // 10 MB
	if lag > maxAcceptableLag {
		t.Errorf("replica lag %d bytes exceeds acceptable limit %d", lag, maxAcceptableLag)
	}
	t.Logf("replica-1 lag: %d bytes", lag)
}
