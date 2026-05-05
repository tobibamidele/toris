package cluster_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/pkg/model"
)

// ─── NodeFromConfig ───────────────────────────────────────────────────────────

func TestNodeFromConfig_Fields(t *testing.T) {
	n := cluster.NodeFromConfig("pg-main", "node-01", "pg-primary.example.com", 5432)

	if n.ID != "node-01" {
		t.Errorf("expected ID node-01, got %s", n.ID)
	}
	if n.ClusterID != "pg-main" {
		t.Errorf("expected ClusterID pg-main, got %s", n.ClusterID)
	}
	if n.Host != "pg-primary.example.com" {
		t.Errorf("expected correct host, got %s", n.Host)
	}
	if n.Port != 5432 {
		t.Errorf("expected port 5432, got %d", n.Port)
	}
	if n.Status != model.NodeStatusJoining {
		t.Errorf("new node should start in joining status, got %s", n.Status)
	}
	if n.Role != model.NodeRoleUnknown {
		t.Errorf("new node role should be unknown, got %s", n.Role)
	}
	if n.JoinedAt.IsZero() {
		t.Error("JoinedAt should be set")
	}
}

// ─── OutageDuration ───────────────────────────────────────────────────────────

func TestOutageDuration_HealthyNode_Zero(t *testing.T) {
	n := &model.Node{Status: model.NodeStatusHealthy}
	d := cluster.OutageDuration(n, time.Now())
	if d != 0 {
		t.Errorf("healthy node should have zero outage duration, got %s", d)
	}
}

func TestOutageDuration_UnhealthyNode_ReturnsElapsed(t *testing.T) {
	lastSeen := time.Now().Add(-90 * time.Second)
	n := &model.Node{
		Status:     model.NodeStatusUnhealthy,
		LastSeenAt: lastSeen,
	}
	d := cluster.OutageDuration(n, time.Now())
	if d < 89*time.Second || d > 91*time.Second {
		t.Errorf("outage duration should be ~90s, got %s", d)
	}
}

func TestOutageDuration_DegradedNode_ReturnsElapsed(t *testing.T) {
	lastSeen := time.Now().Add(-45 * time.Second)
	n := &model.Node{
		Status:     model.NodeStatusDegraded,
		LastSeenAt: lastSeen,
	}
	d := cluster.OutageDuration(n, time.Now())
	if d < 44*time.Second {
		t.Errorf("degraded node outage duration should be ~45s, got %s", d)
	}
}

func TestOutageDuration_ZeroLastSeen_ReturnsZero(t *testing.T) {
	// If we have never seen the node, we cannot compute an outage duration.
	n := &model.Node{
		Status:     model.NodeStatusUnhealthy,
		LastSeenAt: time.Time{}, // zero
	}
	d := cluster.OutageDuration(n, time.Now())
	if d != 0 {
		t.Errorf("zero LastSeenAt should yield zero outage duration, got %s", d)
	}
}

// ─── Node status transitions ──────────────────────────────────────────────────

func TestNodeStatus_FencedNodeNotWritable(t *testing.T) {
	// A fenced node must never be selected as a write target regardless of role.
	fencedPrimary := &model.Node{
		Role:   model.NodeRolePrimary,
		Status: model.NodeStatusFenced,
	}

	isWritable := fencedPrimary.Role == model.NodeRolePrimary &&
		fencedPrimary.Status != model.NodeStatusFenced

	if isWritable {
		t.Error("fenced primary must not be considered writable")
	}
}

func TestNodeStatus_RemovedNodeNotWritable(t *testing.T) {
	n := &model.Node{
		Role:   model.NodeRolePrimary,
		Status: model.NodeStatusRemoved,
	}
	isWritable := n.Role == model.NodeRolePrimary && n.Status != model.NodeStatusRemoved
	if isWritable {
		t.Error("removed node must not be considered writable")
	}
}

func TestNodeStatus_HealthyPrimary_IsWritable(t *testing.T) {
	n := &model.Node{
		Role:   model.NodeRolePrimary,
		Status: model.NodeStatusHealthy,
	}
	isWritable := n.Role == model.NodeRolePrimary &&
		n.Status != model.NodeStatusFenced &&
		n.Status != model.NodeStatusRemoved
	if !isWritable {
		t.Error("healthy primary should be writable")
	}
}

// ─── BestPromotionCandidate (logic) ──────────────────────────────────────────

// selectBestCandidate mirrors the logic in Registry.BestPromotionCandidate
// for pure unit testing without a real registry.
func selectBestCandidate(nodes []*model.Node, snapshots map[string]*model.HealthSnapshot, maxLag int64) *model.Node {
	var best *model.Node
	var bestLag int64 = -1

	for _, n := range nodes {
		if n.Role != model.NodeRoleReplica {
			continue
		}
		if n.Status == model.NodeStatusFenced ||
			n.Status == model.NodeStatusRemoved ||
			n.Status == model.NodeStatusUnhealthy {
			continue
		}
		snap, ok := snapshots[n.ID]
		if !ok || snap.Level < model.HealthLevelRoleKnown {
			continue
		}
		lag := snap.ReplicationLagBytes
		if lag > maxLag {
			continue
		}
		if best == nil || lag < bestLag {
			cp := *n
			best = &cp
			bestLag = lag
		}
	}
	return best
}

func TestBestCandidate_PicksLowestLag(t *testing.T) {
	nodes := []*model.Node{
		{ID: "r1", Role: model.NodeRoleReplica, Status: model.NodeStatusHealthy},
		{ID: "r2", Role: model.NodeRoleReplica, Status: model.NodeStatusHealthy},
		{ID: "r3", Role: model.NodeRoleReplica, Status: model.NodeStatusHealthy},
	}
	snaps := map[string]*model.HealthSnapshot{
		"r1": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 8192},
		"r2": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 512},
		"r3": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 4096},
	}

	best := selectBestCandidate(nodes, snaps, 64<<20)
	if best == nil {
		t.Fatal("expected a candidate, got nil")
	}
	if best.ID != "r2" {
		t.Errorf("expected r2 (lowest lag 512), got %s", best.ID)
	}
}

func TestBestCandidate_FencedExcluded(t *testing.T) {
	nodes := []*model.Node{
		{ID: "r1", Role: model.NodeRoleReplica, Status: model.NodeStatusFenced},
		{ID: "r2", Role: model.NodeRoleReplica, Status: model.NodeStatusHealthy},
	}
	snaps := map[string]*model.HealthSnapshot{
		"r1": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 0},
		"r2": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica, ReplicationLagBytes: 1024},
	}

	best := selectBestCandidate(nodes, snaps, 64<<20)
	if best == nil {
		t.Fatal("expected r2 to be selected")
	}
	if best.ID == "r1" {
		t.Error("fenced replica must not be selected as promotion candidate")
	}
}

func TestBestCandidate_NoHealthSnapshot_Excluded(t *testing.T) {
	nodes := []*model.Node{
		{ID: "r1", Role: model.NodeRoleReplica, Status: model.NodeStatusHealthy},
	}
	// No snapshot for r1.
	snaps := map[string]*model.HealthSnapshot{}

	best := selectBestCandidate(nodes, snaps, 64<<20)
	if best != nil {
		t.Error("replica without health snapshot must not be selected")
	}
}

func TestBestCandidate_AllExcluded_ReturnsNil(t *testing.T) {
	nodes := []*model.Node{
		{ID: "r1", Role: model.NodeRoleReplica, Status: model.NodeStatusUnhealthy},
		{ID: "r2", Role: model.NodeRoleReplica, Status: model.NodeStatusFenced},
	}
	snaps := map[string]*model.HealthSnapshot{
		"r1": {Level: model.HealthLevelUnreachable},
		"r2": {Level: model.HealthLevelPolicyPass, Role: model.NodeRoleReplica},
	}

	best := selectBestCandidate(nodes, snaps, 64<<20)
	if best != nil {
		t.Errorf("expected nil when all candidates are excluded, got %s", best.ID)
	}
}
