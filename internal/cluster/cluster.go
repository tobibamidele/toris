// Package cluster owns the node registry — the authoritative record of which
// nodes belong to the cluster, their current roles, and their last known status.
//
// The registry is persisted in toris_control.nodes so that the daemon can
// resume with full cluster knowledge after a restart without re-discovering
// everything from scratch.
package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// Registry maintains the set of nodes for one cluster.
type Registry struct {
	log       *logging.Logger
	pool      *pgxpool.Pool
	clusterID string

	mu    sync.RWMutex
	nodes map[string]*model.Node // keyed by node ID
}

// New creates a Registry. pool must be the toris control database connection.
func New(log *logging.Logger, pool *pgxpool.Pool, clusterID string) *Registry {
	return &Registry{
		log:       log,
		pool:      pool,
		clusterID: clusterID,
		nodes:     make(map[string]*model.Node),
	}
}

// EnsureSchema creates the nodes table if it does not exist. Idempotent.
func (r *Registry) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS toris_control.nodes (
			id                    TEXT        NOT NULL,
			cluster_id            TEXT        NOT NULL,
			host                  TEXT        NOT NULL,
			port                  INTEGER     NOT NULL,
			role                  TEXT        NOT NULL DEFAULT 'unknown',
			status                TEXT        NOT NULL DEFAULT 'unknown',
			replication_lag_bytes BIGINT      NOT NULL DEFAULT 0,
			last_seen_at          TIMESTAMPTZ,
			joined_at             TIMESTAMPTZ NOT NULL,
			updated_at            TIMESTAMPTZ NOT NULL,

			CONSTRAINT nodes_pkey PRIMARY KEY (id),
			CONSTRAINT nodes_cluster_fk FOREIGN KEY (cluster_id)
				REFERENCES toris_control.leases (cluster_id)
				DEFERRABLE INITIALLY DEFERRED
		);

		CREATE INDEX IF NOT EXISTS nodes_cluster_idx
			ON toris_control.nodes (cluster_id);
	`)
	if err != nil {
		return fmt.Errorf("ensuring nodes schema: %w", err)
	}
	return nil
}

// Load fetches all nodes for this cluster from the control DB into the in-memory
// map. Call once at daemon startup after EnsureSchema.
func (r *Registry) Load(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		SELECT id, cluster_id, host, port, role, status,
		       replication_lag_bytes, last_seen_at, joined_at, updated_at
		FROM toris_control.nodes
		WHERE cluster_id = $1
	`, r.clusterID)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"loading nodes for cluster %s", r.clusterID)
	}
	defer rows.Close()

	r.mu.Lock()
	defer r.mu.Unlock()

	for rows.Next() {
		var n model.Node
		if err := rows.Scan(
			&n.ID, &n.ClusterID, &n.Host, &n.Port,
			&n.Role, &n.Status, &n.ReplicationLagBytes,
			&n.LastSeenAt, &n.JoinedAt, &n.UpdatedAt,
		); err != nil {
			return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err, "scanning node row")
		}
		cp := n
		r.nodes[n.ID] = &cp
	}
	return rows.Err()
}

// Upsert adds or updates a node record in both the in-memory map and the
// control database. Safe to call from the health-check loop on every tick.
func (r *Registry) Upsert(ctx context.Context, n *model.Node) error {
	now := util.NowUTC()
	n.UpdatedAt = now
	if n.JoinedAt.IsZero() {
		n.JoinedAt = now
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO toris_control.nodes
			(id, cluster_id, host, port, role, status,
			 replication_lag_bytes, last_seen_at, joined_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			host                  = EXCLUDED.host,
			port                  = EXCLUDED.port,
			role                  = EXCLUDED.role,
			status                = EXCLUDED.status,
			replication_lag_bytes = EXCLUDED.replication_lag_bytes,
			last_seen_at          = EXCLUDED.last_seen_at,
			updated_at            = EXCLUDED.updated_at
	`, n.ID, n.ClusterID, n.Host, n.Port,
		string(n.Role), string(n.Status),
		n.ReplicationLagBytes, n.LastSeenAt, n.JoinedAt, n.UpdatedAt,
	)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"upserting node %s", n.ID)
	}

	r.mu.Lock()
	cp := *n
	r.nodes[n.ID] = &cp
	r.mu.Unlock()

	return nil
}

// UpdateStatus updates only the status and last_seen_at for a node.
// Used by the health loop to mark nodes degraded/unhealthy without a full upsert.
func (r *Registry) UpdateStatus(ctx context.Context, nodeID string, status model.NodeStatus, role model.NodeRole, lag int64) error {
	now := util.NowUTC()

	_, err := r.pool.Exec(ctx, `
		UPDATE toris_control.nodes
		SET status                = $1,
		    role                  = $2,
		    replication_lag_bytes = $3,
		    last_seen_at          = $4,
		    updated_at            = $4
		WHERE id = $5
	`, string(status), string(role), lag, now, nodeID)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"updating status for node %s", nodeID)
	}

	r.mu.Lock()
	if n, ok := r.nodes[nodeID]; ok {
		n.Status = status
		n.Role = role
		n.ReplicationLagBytes = lag
		n.LastSeenAt = now
		n.UpdatedAt = now
	}
	r.mu.Unlock()
	return nil
}

// Get returns a copy of the node with the given ID, or false if not found.
func (r *Registry) Get(id string) (*model.Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return nil, false
	}
	cp := *n
	return &cp, true
}

// All returns a snapshot of all nodes. The returned slice is safe to iterate
// without holding any lock.
func (r *Registry) All() []*model.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*model.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		cp := *n
		out = append(out, &cp)
	}
	return out
}

// Primary returns the current primary node, or nil if none is known.
func (r *Registry) Primary() *model.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.nodes {
		if n.Role == model.NodeRolePrimary && n.Status != model.NodeStatusFenced && n.Status != model.NodeStatusRemoved {
			cp := *n
			return &cp
		}
	}
	return nil
}

// Replicas returns all nodes currently acting as replicas.
func (r *Registry) Replicas() []*model.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Node
	for _, n := range r.nodes {
		if n.Role == model.NodeRoleReplica && n.Status != model.NodeStatusRemoved {
			cp := *n
			out = append(out, &cp)
		}
	}
	return out
}

// BestPromotionCandidate returns the replica with the lowest replication lag
// that passes a minimum health bar. Returns nil if no suitable candidate exists.
func (r *Registry) BestPromotionCandidate(snapshots map[string]*model.HealthSnapshot, maxLagBytes int64) *model.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best *model.Node
	var bestLag int64 = -1

	for _, n := range r.nodes {
		if n.Role != model.NodeRoleReplica {
			continue
		}
		if n.Status == model.NodeStatusFenced || n.Status == model.NodeStatusRemoved || n.Status == model.NodeStatusUnhealthy {
			continue
		}
		snap, ok := snapshots[n.ID]
		if !ok || snap.Level < model.HealthLevelRoleKnown {
			continue
		}
		lag := snap.ReplicationLagBytes
		if lag > maxLagBytes {
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

// MarkFenced sets a node's status to fenced in both memory and the control DB.
// This is a write the failover engine calls before any promotion.
func (r *Registry) MarkFenced(ctx context.Context, nodeID string) error {
	return r.UpdateStatus(ctx, nodeID, model.NodeStatusFenced, model.NodeRolePrimary, 0)
}

// Remove marks a node as removed and persists that state.
func (r *Registry) Remove(ctx context.Context, nodeID string) error {
	now := util.NowUTC()
	_, err := r.pool.Exec(ctx, `
		UPDATE toris_control.nodes
		SET status = 'removed', updated_at = $1
		WHERE id = $2
	`, now, nodeID)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"removing node %s", nodeID)
	}
	r.mu.Lock()
	if n, ok := r.nodes[nodeID]; ok {
		n.Status = model.NodeStatusRemoved
		n.UpdatedAt = now
	}
	r.mu.Unlock()
	return nil
}

// SeedFromConfig populates the registry from static config if the nodes table
// is empty. This is called once at daemon startup so a fresh install
// has nodes to work with before any health checks run.
func (r *Registry) SeedFromConfig(ctx context.Context, nodes []model.Node) error {
	for _, n := range nodes {
		if _, exists := r.Get(n.ID); !exists {
			cp := n
			if err := r.Upsert(ctx, &cp); err != nil {
				return fmt.Errorf("seeding node %s: %w", n.ID, err)
			}
		}
	}
	return nil
}

// NodeFromConfig converts a config.NodeConfig-style value into a model.Node.
// Callers use this to bridge the config layer and the registry.
func NodeFromConfig(clusterID, id, host string, port int) *model.Node {
	return &model.Node{
		ID:        id,
		ClusterID: clusterID,
		Host:      host,
		Port:      port,
		Role:      model.NodeRoleUnknown,
		Status:    model.NodeStatusJoining,
		JoinedAt:  util.NowUTC(),
		UpdatedAt: util.NowUTC(),
	}
}

// OutageDuration returns how long a node has been continuously unreachable,
// or zero if it is currently reachable.
func OutageDuration(n *model.Node, now time.Time) time.Duration {
	if n.Status != model.NodeStatusUnhealthy && n.Status != model.NodeStatusDegraded {
		return 0
	}
	if n.LastSeenAt.IsZero() {
		return 0
	}
	return now.Sub(n.LastSeenAt)
}
