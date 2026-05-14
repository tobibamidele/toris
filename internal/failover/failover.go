// Package failover implements the failover decision engine.
//
// Failure class taxonomy (enforced here):
//
//	Class A — primary loses replica connectivity only
//	  Source: health.Tracker reports IsDegraded=true but IsUnsafe=false.
//	  Action: mark primary DEGRADED, record health, do nothing else.
//	  Escalation to failover: only when IsUnsafe=true (all replicas gone
//	  AND outage crossed replication_outage_threshold).
//
//	Class B — primary loses control-plane connectivity / lease renewal
//	  Source: leader.Manager.HoldingLease() returns false.
//	  Action: do not demote manually — the lease TTL mechanism handles it.
//	  Once the lease expires, a new toris instance acquires a higher
//	  generation and the old primary is fenced by the routing layer.
//	  This engine only acts after that generation advance is confirmed.
//
//	Class C — deliberate operator demotion
//	  Source: toris demote / toris promote CLI commands.
//	  Action: fence first, promote candidate, flip routing, update registry.
//
// Sequence for any failover (classes A-escalated or B):
//  1. Verify lease is held and fencing token is current.
//  2. Fence the old primary (force read-only, terminate connections).
//  3. Advance the routing target to the best candidate replica.
//  4. Call pg_promote on the candidate.
//  5. Update the node registry.
//  6. Emit audit events throughout.
//  7. Mark failover complete.
//
// Fence first. Route second. Never the other way around.
package failover

import (
	"context"
	"fmt"
	"time"

	"github.com/tobibamidele/toris/internal/audit"
	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/internal/db/postgres"
	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/health"
	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/routing"
	"github.com/tobibamidele/toris/internal/telemetry"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// Rewinder is the interface the failover engine uses to schedule post-failover
// rewind or reseed. Implemented by restore.Rewinder.
type Rewinder interface {
	RewindOrReseed(ctx context.Context, opts RewindOptions) (*model.RewindJob, error)
}

// RewindOptions is passed from the failover engine to the Rewinder.
// Kept here to avoid an import cycle between failover and restore.
type RewindOptions struct {
	OldPrimary         *model.Node
	NewPrimary         *model.Node
	OldPrimaryDataDir  string
	NewPrimaryDSN      string
	NewPrimaryPassword string
	FallbackBackupID   string
	FallbackTargetDir  string
	TempDir            string
	ClusterID          string
	Generation         int64
	RewindTimeout      time.Duration
	ReseedTimeout      time.Duration
}

// Engine evaluates cluster health snapshots and executes failover when warranted.
type Engine struct {
	log        *logging.Logger
	clusterID  string
	instanceID string

	registry *cluster.Registry
	lm       *leader.Manager
	backend  *postgres.Backend
	proxy    *routing.Proxy
	auditor  *audit.Writer
	tracker  *health.Tracker
	metrics  *telemetry.Metrics
	rewinder Rewinder // nil when auto-rewind is disabled

	// config thresholds
	unhealthyThreshold      time.Duration
	maxLagBytes             int64
	failoverEnabled         bool
	autoRewindAfterFailover bool
}

// Config holds all threshold values the Engine needs.
type Config struct {
	ClusterID               string
	InstanceID              string
	UnhealthyThreshold      time.Duration
	MaxLagBytes             int64
	FailoverEnabled         bool
	AutoRewindAfterFailover bool
}

// New creates a failover Engine.
// rewinder may be nil to disable automatic post-failover rewind scheduling.
func New(
	log *logging.Logger,
	cfg Config,
	registry *cluster.Registry,
	lm *leader.Manager,
	backend *postgres.Backend,
	proxy *routing.Proxy,
	auditor *audit.Writer,
	tracker *health.Tracker,
	metrics *telemetry.Metrics,
	rewinder Rewinder,
) *Engine {
	return &Engine{
		log:                     log,
		clusterID:               cfg.ClusterID,
		instanceID:              cfg.InstanceID,
		registry:                registry,
		lm:                      lm,
		backend:                 backend,
		proxy:                   proxy,
		auditor:                 auditor,
		tracker:                 tracker,
		metrics:                 metrics,
		rewinder:                rewinder,
		unhealthyThreshold:      cfg.UnhealthyThreshold,
		maxLagBytes:             cfg.MaxLagBytes,
		failoverEnabled:         cfg.FailoverEnabled,
		autoRewindAfterFailover: cfg.AutoRewindAfterFailover,
	}
}

// Evaluate is called by the health-check loop after every round of checks.
// It applies the failure class taxonomy and triggers failover if warranted.
// It is safe to call even when this instance does not hold the lease — it
// will return early without taking action.
func (e *Engine) Evaluate(ctx context.Context, snapshots map[string]*model.HealthSnapshot) error {
	// Only the lease holder may initiate failover.
	if !e.lm.HoldingLease() {
		return nil
	}

	primary := e.registry.Primary()
	if primary == nil {
		// No known primary — cluster is in an indeterminate state.
		// This can happen immediately after a fresh init before any health
		// checks have run. Log and wait.
		e.log.Warn("no primary node known — waiting for health checks to establish roles")
		return nil
	}

	snap := snapshots[primary.ID]
	if snap == nil {
		return nil
	}

	// ── Class A: replica connectivity loss ───────────────────────────────
	// Update the replication tracker regardless of primary health.
	replicas := e.registry.Replicas()
	replicaSnaps := make([]*model.HealthSnapshot, 0, len(replicas))
	for _, r := range replicas {
		if s, ok := snapshots[r.ID]; ok {
			replicaSnaps = append(replicaSnaps, s)
		}
	}
	rh := e.tracker.Record(primary.ID, replicaSnaps, len(replicas))

	// Record replication lag metrics.
	if e.metrics != nil {
		for _, r := range replicas {
			if s, ok := snapshots[r.ID]; ok {
				e.metrics.ReplicationLagBytes.WithLabelValues(r.ID).Set(float64(s.ReplicationLagBytes))
			}
		}
	}

	// If the primary is healthy (L5) but degraded on replicas:
	// mark it DEGRADED, do not demote, do not fail over (Class A rule).
	if snap.IsHealthyForRole(model.NodeRolePrimary) {
		if rh.IsDegraded && !rh.IsUnsafe {
			_ = e.registry.UpdateStatus(ctx, primary.ID,
				model.NodeStatusDegraded, model.NodeRolePrimary, 0)
			e.log.Warn("primary degraded: replica connectivity loss — holding primary, no failover",
				"node_id", primary.ID,
				"connected_replicas", rh.ConnectedReplicas,
				"total_replicas", rh.TotalReplicas,
			)
			return nil
		}
		// Primary is fully healthy — ensure status reflects that.
		_ = e.registry.UpdateStatus(ctx, primary.ID,
			model.NodeStatusHealthy, model.NodeRolePrimary, 0)
		return nil
	}

	// ── Primary is not healthy ────────────────────────────────────────────
	// Determine how long it has been unreachable.
	outage := cluster.OutageDuration(primary, time.Now())

	// Not yet past the threshold — mark unhealthy but do not act.
	if outage < e.unhealthyThreshold {
		_ = e.registry.UpdateStatus(ctx, primary.ID,
			model.NodeStatusUnhealthy, primary.Role, 0)
		e.log.Warn("primary unhealthy — within threshold, watching",
			"node_id", primary.ID,
			"outage_duration", outage,
			"threshold", e.unhealthyThreshold,
		)
		return nil
	}

	// ── Class B: lease loss is handled by the lease TTL, not here ────────
	// If we are here it means: this instance DOES hold the lease (checked
	// above), the primary IS unhealthy, and the outage has crossed the
	// threshold. This is a legitimate failover condition.

	if !e.failoverEnabled {
		e.log.Error("primary has been unhealthy beyond threshold but automatic failover is disabled — operator action required",
			"node_id", primary.ID,
			"outage_duration", outage,
		)
		return nil
	}

	return e.execute(ctx, primary, snapshots)
}

// execute runs the full failover sequence.
// Preconditions (all verified by Evaluate before calling):
//   - This instance holds the lease
//   - The primary has been unhealthy beyond UnhealthyThreshold
//   - failoverEnabled is true
func (e *Engine) execute(ctx context.Context, oldPrimary *model.Node, snapshots map[string]*model.HealthSnapshot) error {
	start := time.Now()
	generation := e.lm.CurrentGeneration()
	eventID := util.NewID()

	e.log.Info("failover sequence starting",
		"old_primary", oldPrimary.ID,
		"generation", generation,
	)

	if e.metrics != nil {
		e.metrics.FailoversTotal.Inc()
	}

	e.auditor.EmitNow(e.clusterID, model.AuditKindFailoverDetected,
		e.instanceID, oldPrimary.ID, generation,
		fmt.Sprintf("primary %s unhealthy beyond threshold — initiating failover", oldPrimary.ID),
	)
	_ = eventID

	// 1. Verify fencing token is still current before touching anything.
	if err := e.lm.AssertFencingToken(generation); err != nil {
		return torerrors.Wrap(torerrors.CodeFencingViolation, "failover aborted before fence", err)
	}

	// 2. Fence the old primary.
	// This must happen before any promotion attempt.
	// If fencing fails we abort — split-brain is worse than downtime.
	if err := e.fenceOldPrimary(ctx, oldPrimary, generation); err != nil {
		if e.metrics != nil {
			e.metrics.FailoversFailed.Inc()
		}
		return fmt.Errorf("failover aborted: fencing failed: %w", err)
	}

	// 3. Select the best promotion candidate.
	candidate := e.registry.BestPromotionCandidate(snapshots, e.maxLagBytes)
	if candidate == nil {
		// No suitable candidate — failover cannot proceed.
		// The cluster is down. Operator must intervene.
		e.log.Error("failover aborted: no suitable promotion candidate",
			"old_primary", oldPrimary.ID,
		)
		e.auditor.EmitNow(e.clusterID, model.AuditKindFailoverDetected,
			e.instanceID, oldPrimary.ID, generation,
			"failover aborted: no suitable promotion candidate — manual intervention required",
		)
		if e.metrics != nil {
			e.metrics.FailoversFailed.Inc()
		}
		return torerrors.Newf(torerrors.CodeFailoverFailed,
			"no suitable promotion candidate found; all replicas either unhealthy or lag exceeds %d bytes",
			e.maxLagBytes)
	}

	// 4. Promote the candidate.
	if err := e.promoteCandidate(ctx, candidate, generation); err != nil {
		if e.metrics != nil {
			e.metrics.FailoversFailed.Inc()
		}
		return fmt.Errorf("failover aborted: promotion failed: %w", err)
	}

	// 5. Flip the routing target atomically.
	e.proxy.SetTarget(&model.RoutingTarget{
		ClusterID:  e.clusterID,
		NodeID:     candidate.ID,
		Host:       candidate.Host,
		Port:       candidate.Port,
		Generation: generation,
		UpdatedAt:  util.NowUTC(),
	})
	e.log.Info("routing target updated to new primary",
		"new_primary", candidate.ID,
		"generation", generation,
	)

	// 6. Update the node registry.
	_ = e.registry.UpdateStatus(ctx, candidate.ID, model.NodeStatusHealthy, model.NodeRolePrimary, 0)

	// 7. Audit and metrics.
	e.auditor.EmitNow(e.clusterID, model.AuditKindFailoverComplete,
		e.instanceID, candidate.ID, generation,
		fmt.Sprintf("failover complete: %s promoted to primary (old primary: %s)",
			candidate.ID, oldPrimary.ID),
	)
	e.auditor.EmitNow(e.clusterID, model.AuditKindNodePromoted,
		e.instanceID, candidate.ID, generation,
		fmt.Sprintf("node %s promoted to primary", candidate.ID),
	)

	if e.metrics != nil {
		e.metrics.FailoverDuration.Observe(time.Since(start).Seconds())
		e.metrics.LeaseGeneration.Set(float64(generation))
	}

	e.log.Info("failover complete",
		"new_primary", candidate.ID,
		"old_primary", oldPrimary.ID,
		"duration", util.FormatDuration(time.Since(start)),
		"generation", generation,
	)

	// 8. Schedule post-failover rewind/reseed asynchronously.
	// This runs in a goroutine so it does not block the health loop.
	// The old primary remains fenced until rewind succeeds.
	if e.autoRewindAfterFailover && e.rewinder != nil {
		go e.scheduleRewind(oldPrimary, candidate, generation)
	}

	return nil
}

// scheduleRewind runs pg_rewind (or reseed fallback) on the old primary
// in a background goroutine after failover completes.
func (e *Engine) scheduleRewind(oldPrimary, newPrimary *model.Node, generation int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	e.log.Info("scheduling post-failover rewind",
		"old_primary", oldPrimary.ID,
		"new_primary", newPrimary.ID,
	)

	job, err := e.rewinder.RewindOrReseed(ctx, RewindOptions{
		OldPrimary:    oldPrimary,
		NewPrimary:    newPrimary,
		ClusterID:     e.clusterID,
		Generation:    generation,
		RewindTimeout: 30 * time.Minute,
		ReseedTimeout: 6 * time.Hour,
	})

	if err != nil {
		e.log.Error("post-failover rewind/reseed failed — old primary remains fenced",
			"old_primary", oldPrimary.ID,
			"error", err.Error(),
		)
		return
	}

	status := "rewound"
	if job.UsedFallback {
		status = "reseeded (pg_rewind fallback)"
	}
	e.log.Info("post-failover rewind complete",
		"old_primary", oldPrimary.ID,
		"status", status,
		"duration", util.FormatDuration(job.FinishedAt.Sub(job.StartedAt)),
	)
	// Update the node status so the health loop can re-evaluate it.
	updateCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_ = e.registry.UpdateStatus(updateCtx, oldPrimary.ID,
		model.NodeStatusJoining, model.NodeRoleReplica, 0)
}

// fenceOldPrimary forces the old primary read-only and terminates its
// active connections. Must succeed before any promotion is attempted.
func (e *Engine) fenceOldPrimary(ctx context.Context, node *model.Node, generation int64) error {
	e.log.Info("fencing old primary",
		"node_id", node.ID,
		"generation", generation,
	)

	// Mark fenced in the registry first — this prevents any concurrent
	// operation from treating this node as writable.
	if err := e.registry.MarkFenced(ctx, node.ID); err != nil {
		e.log.Warn("could not mark node fenced in registry — continuing anyway",
			"node_id", node.ID,
			"error", err.Error(),
		)
	}

	// Best-effort database-level fence.
	// If the node is completely unreachable this will fail, but that is
	// acceptable — an unreachable node cannot accept writes anyway.
	fenceErr := e.backend.Fence(ctx, node, generation)
	if fenceErr != nil {
		e.log.Warn("database fence returned an error — node may be unreachable",
			"node_id", node.ID,
			"error", fenceErr.Error(),
		)
		// Do not abort: an unreachable primary is already unable to serve writes.
		// The routing flip and generation advance provide the fencing guarantee.
	}

	e.auditor.EmitNow(e.clusterID, model.AuditKindNodeFenced,
		e.instanceID, node.ID, generation,
		fmt.Sprintf("node %s fenced before promotion of candidate", node.ID),
	)
	return nil
}

// promoteCandidate calls pg_promote on the chosen replica.
func (e *Engine) promoteCandidate(ctx context.Context, node *model.Node, generation int64) error {
	e.log.Info("promoting candidate replica",
		"node_id", node.ID,
		"generation", generation,
	)

	if err := e.backend.Promote(ctx, node, generation); err != nil {
		return torerrors.Wrapf(torerrors.CodeFailoverFailed, err,
			"pg_promote failed on candidate %s", node.ID)
	}

	e.auditor.EmitNow(e.clusterID, model.AuditKindNodePromoted,
		e.instanceID, node.ID, generation,
		fmt.Sprintf("pg_promote called on node %s", node.ID),
	)
	return nil
}

// ForcePromote is called by the CLI `toris promote` command.
// It skips the threshold wait and executes the failover sequence directly,
// provided the caller holds the lease and supplies the current generation.
func (e *Engine) ForcePromote(ctx context.Context, candidateID string) error {
	if !e.lm.HoldingLease() {
		return torerrors.New(torerrors.CodeLeaseNotHeld,
			"cannot force promote: this instance does not hold the cluster lease")
	}

	generation := e.lm.CurrentGeneration()
	if err := e.lm.AssertFencingToken(generation); err != nil {
		return err
	}

	candidate, ok := e.registry.Get(candidateID)
	if !ok {
		return torerrors.Newf(torerrors.CodeNotFound,
			"candidate node %s not found in registry", candidateID)
	}

	oldPrimary := e.registry.Primary()
	if oldPrimary != nil {
		if err := e.fenceOldPrimary(ctx, oldPrimary, generation); err != nil {
			return err
		}
	}

	if err := e.promoteCandidate(ctx, candidate, generation); err != nil {
		return err
	}

	e.proxy.SetTarget(&model.RoutingTarget{
		ClusterID:  e.clusterID,
		NodeID:     candidate.ID,
		Host:       candidate.Host,
		Port:       candidate.Port,
		Generation: generation,
		UpdatedAt:  util.NowUTC(),
	})

	_ = e.registry.UpdateStatus(ctx, candidate.ID, model.NodeStatusHealthy, model.NodeRolePrimary, 0)

	e.auditor.EmitNow(e.clusterID, model.AuditKindNodePromoted,
		e.instanceID, candidate.ID, generation,
		fmt.Sprintf("operator-initiated promotion of node %s", candidate.ID),
	)
	return nil
}
