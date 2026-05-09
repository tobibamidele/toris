// Package health provides the layered health evaluation and replication health
// tracking used by the control plane.
//
// Failure class separation (per operational rules):
//
//	Class A — replica connectivity loss
//	  The primary cannot reach one or more replicas.
//	  Action: mark primary DEGRADED, start a replication-outage timer,
//	          record replication health. Do NOT demote or fail over.
//	  Escalation: only if the outage crosses replication_outage_threshold
//	              AND replication is deemed unsafe (all replicas gone, lag
//	              beyond tolerance) does this feed into the failover engine.
//
//	Class B — control-plane connectivity / lease renewal failure
//	  The toris daemon cannot renew its lease in the control database.
//	  Action: treat the primary as stale immediately. Once the lease expires
//	          and a new instance acquires a higher fencing token, the old
//	          primary is blocked from accepting writes.
//	  This is handled in internal/leader — not here.
//
// This package owns Class A tracking only.
package health

import (
	"sync"
	"time"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// ReplicationHealth is the observed replication state of one primary node.
type ReplicationHealth struct {
	// NodeID is the primary node being observed.
	NodeID string

	// ConnectedReplicas is the count of replicas currently streaming.
	ConnectedReplicas int

	// TotalReplicas is the total number of configured replicas.
	TotalReplicas int

	// MaxLagBytes is the highest replication lag observed across all replicas.
	MaxLagBytes int64

	// OutageSince is the time the primary first lost all replica connectivity.
	// Nil if at least one replica is connected.
	OutageSince *time.Time

	// IsDegraded is true when ConnectedReplicas < TotalReplicas.
	IsDegraded bool

	// IsUnsafe is true when replication is considered unsafe:
	// all replicas are gone AND the outage has crossed the configured threshold.
	IsUnsafe bool

	// CheckedAt is when this snapshot was taken.
	CheckedAt time.Time
}

// ReplicationOutageThreshold is the duration the primary must have zero replica
// connectivity before replication is considered unsafe.
// Populated from config.FailoverConfig.ReplicationOutageThreshold.
type ReplicationOutageThreshold = time.Duration

// Tracker maintains the replication health history for the primary node.
// It is read by the failover engine to determine whether Class A escalation
// is warranted. It never triggers demotion itself.
type Tracker struct {
	log       *logging.Logger
	threshold time.Duration
	maxLag    int64

	mu      sync.Mutex
	current map[string]*ReplicationHealth // keyed by node ID
}

// NewTracker creates a Tracker.
// threshold is the replication outage duration before IsUnsafe flips true.
// maxLag is the maximum tolerated lag in bytes; exceeded = replica not counted as connected.
func NewTracker(log *logging.Logger, threshold time.Duration, maxLag int64) *Tracker {
	return &Tracker{
		log:       log,
		threshold: threshold,
		maxLag:    maxLag,
		current:   make(map[string]*ReplicationHealth),
	}
}

// Record updates the replication health for a primary node given a fresh set
// of replica health snapshots. Called by the health-check loop after every round.
func (t *Tracker) Record(primaryID string, replicas []*model.HealthSnapshot, totalConfigured int) *ReplicationHealth {
	now := time.Now().UTC()

	connected := 0
	var maxLag int64
	for _, r := range replicas {
		if r.Level >= model.HealthLevelRoleKnown && r.Role == model.NodeRoleReplica {
			lag := r.ReplicationLagBytes
			if lag <= t.maxLag {
				connected++
			}
			if lag > maxLag {
				maxLag = lag
			}
		}
	}

	t.mu.Lock()
	prev, exists := t.current[primaryID]

	rh := &ReplicationHealth{
		NodeID:            primaryID,
		ConnectedReplicas: connected,
		TotalReplicas:     totalConfigured,
		MaxLagBytes:       maxLag,
		IsDegraded:        connected < totalConfigured,
		CheckedAt:         now,
	}

	// Carry forward OutageSince from the previous record if we are still
	// in a full outage (zero connected replicas). Reset it if connectivity recovered.
	if connected == 0 && totalConfigured > 0 {
		if exists && prev.OutageSince != nil {
			rh.OutageSince = prev.OutageSince
		} else {
			rh.OutageSince = &now
			t.log.Warn("primary lost all replica connectivity — starting outage timer",
				"node_id", primaryID,
				"total_replicas", totalConfigured,
			)
		}
		// Escalate to unsafe only after the threshold is crossed.
		if now.Sub(*rh.OutageSince) >= t.threshold {
			rh.IsUnsafe = true
			t.log.Error("replication outage has crossed threshold — unsafe",
				"node_id", primaryID,
				"outage_duration", now.Sub(*rh.OutageSince),
				"threshold", t.threshold,
			)
		}
	} else {
		// At least one replica connected — reset the outage timer.
		if exists && prev.OutageSince != nil {
			t.log.Info("primary replica connectivity restored",
				"node_id", primaryID,
				"connected", connected,
			)
		}
		rh.OutageSince = nil
	}

	t.current[primaryID] = rh
	t.mu.Unlock()

	return rh
}

// Get returns the most recent replication health for a node, or nil.
func (t *Tracker) Get(nodeID string) *ReplicationHealth {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rh, ok := t.current[nodeID]; ok {
		// Return a copy to avoid data races.
		cp := *rh
		return &cp
	}
	return nil
}

// IsSafeToKeepPrimary returns true if Class A conditions do NOT warrant
// escalation to the failover engine.
//
// It returns false (unsafe to keep primary) only when:
//   - All replicas are unreachable AND
//   - The outage has persisted beyond the configured threshold
//
// A partial replica outage (some replicas reachable) is never unsafe on its own.
func (t *Tracker) IsSafeToKeepPrimary(nodeID string) bool {
	rh := t.Get(nodeID)
	if rh == nil {
		// No data yet — assume safe until we have evidence otherwise.
		return true
	}
	return !rh.IsUnsafe
}
