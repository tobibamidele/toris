// Package leader implements distributed lease-based leader election with fencing.
//
// Design:
//   - Leases are stored in a PostgreSQL control table (toris_control.leases).
//   - Every acquisition increments a monotonic generation (fencing token).
//   - The active leader must renew before TTL expiry or lose the lease.
//   - Any operation that mutates cluster state must pass the current generation.
//   - Stale workers with old generation tokens are rejected.
//
// Split-brain prevention:
//   - No in-memory election. State is durable in the control DB.
//   - A node cannot acquire a lease until the TTL of the previous holder expires.
//   - Lease acquisition uses a single atomic UPDATE with a WHERE clause that
//     checks expiry — no separate read-then-write race.
package leader

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

// Manager handles lease acquisition, renewal, and release for one cluster.
type Manager struct {
	log        *logging.Logger
	pool       *pgxpool.Pool
	clusterID  string
	instanceID string
	leaseTTL   time.Duration
	renewEvery time.Duration

	mu      sync.Mutex
	current *model.Lease // nil if not currently holding lease
}

// New creates a lease Manager.
// pool must be connected to the toris control database.
func New(
	log *logging.Logger,
	pool *pgxpool.Pool,
	clusterID, instanceID string,
	leaseTTL, renewEvery time.Duration,
) *Manager {
	return &Manager{
		log:        log,
		pool:       pool,
		clusterID:  clusterID,
		instanceID: instanceID,
		leaseTTL:   leaseTTL,
		renewEvery: renewEvery,
	}
}

// EnsureSchema creates the lease table if it doesn't exist.
// Safe to call multiple times (idempotent).
func (m *Manager) EnsureSchema(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS toris_control;

		CREATE TABLE IF NOT EXISTS toris_control.leases (
			id              TEXT        NOT NULL,
			cluster_id      TEXT        NOT NULL,
			instance_id     TEXT        NOT NULL,
			leader_id       TEXT        NOT NULL DEFAULT '',
			generation      BIGINT      NOT NULL DEFAULT 1,
			status          TEXT        NOT NULL DEFAULT 'active',
			acquired_at     TIMESTAMPTZ NOT NULL,
			expires_at      TIMESTAMPTZ NOT NULL,
			last_heartbeat  TIMESTAMPTZ NOT NULL,
			released_at     TIMESTAMPTZ,

			CONSTRAINT leases_pkey PRIMARY KEY (cluster_id)
		);

		CREATE INDEX IF NOT EXISTS leases_instance_idx
			ON toris_control.leases (instance_id);
	`)
	if err != nil {
		return fmt.Errorf("ensuring lease schema: %w", err)
	}
	return nil
}

// Acquire attempts to take the lease for this cluster.
// It is atomic: it either creates a new row or claims an expired one.
// Returns the acquired Lease or an error if another instance holds an active lease.
func (m *Manager) Acquire(ctx context.Context) (*model.Lease, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(m.leaseTTL)
	id := util.NewID()

	// Attempt 1: INSERT a new row (first-time acquisition).
	// Attempt 2: UPDATE an expired or released row (takeover).
	// Both must be atomic and mutually exclusive.
	var lease model.Lease

	err := m.pool.QueryRow(ctx, `
		INSERT INTO toris_control.leases
			(id, cluster_id, instance_id, leader_id, generation,
			 status, acquired_at, expires_at, last_heartbeat)
		VALUES
			($1, $2, $3, '', 1,
			 'active', $4, $5, $4)
		ON CONFLICT (cluster_id) DO UPDATE
			SET id             = EXCLUDED.id,
			    instance_id    = EXCLUDED.instance_id,
			    leader_id      = '',
			    generation     = toris_control.leases.generation + 1,
			    status         = 'active',
			    acquired_at    = EXCLUDED.acquired_at,
			    expires_at     = EXCLUDED.expires_at,
			    last_heartbeat = EXCLUDED.last_heartbeat,
			    released_at    = NULL
			WHERE
				toris_control.leases.status = 'released'
				OR toris_control.leases.expires_at < $4
		RETURNING id, cluster_id, instance_id, leader_id, generation,
		          status, acquired_at, expires_at, last_heartbeat, released_at
	`, id, m.clusterID, m.instanceID, now, expiresAt).Scan(
		&lease.ID, &lease.ClusterID, &lease.InstanceID, &lease.LeaderID,
		&lease.Generation, &lease.Status,
		&lease.AcquiredAt, &lease.ExpiresAt, &lease.LastHeartbeat, &lease.ReleasedAt,
	)
	if err != nil {
		// If no row was returned, another instance holds an active, non-expired lease.
		return nil, torerrors.Newf(torerrors.CodeLeaseConflict,
			"cannot acquire lease for cluster %s: another instance holds an active lease",
			m.clusterID)
	}

	m.mu.Lock()
	m.current = &lease
	m.mu.Unlock()

	m.log.Info("lease acquired",
		"cluster_id", m.clusterID,
		"instance_id", m.instanceID,
		"generation", lease.Generation,
		"expires_at", lease.ExpiresAt,
	)
	return &lease, nil
}

// Renew extends the lease TTL. Must be called before ExpiresAt.
// Returns an error if the lease was stolen by another instance.
func (m *Manager) Renew(ctx context.Context) (*model.Lease, error) {
	m.mu.Lock()
	cur := m.current
	m.mu.Unlock()

	if cur == nil {
		return nil, torerrors.New(torerrors.CodeLeaseNotHeld, "cannot renew: no lease is currently held")
	}

	now := time.Now().UTC()
	newExpiry := now.Add(m.leaseTTL)

	var lease model.Lease
	err := m.pool.QueryRow(ctx, `
		UPDATE toris_control.leases
		SET expires_at     = $1,
		    last_heartbeat = $2,
		    status         = 'active'
		WHERE cluster_id  = $3
		  AND instance_id = $4
		  AND generation  = $5
		  AND status      = 'active'
		  AND expires_at  > $2
		RETURNING id, cluster_id, instance_id, leader_id, generation,
		          status, acquired_at, expires_at, last_heartbeat, released_at
	`, newExpiry, now, m.clusterID, m.instanceID, cur.Generation).Scan(
		&lease.ID, &lease.ClusterID, &lease.InstanceID, &lease.LeaderID,
		&lease.Generation, &lease.Status,
		&lease.AcquiredAt, &lease.ExpiresAt, &lease.LastHeartbeat, &lease.ReleasedAt,
	)
	if err != nil {
		return nil, torerrors.Newf(torerrors.CodeLeaseExpired,
			"lease renewal failed for cluster %s generation %d: lease was stolen or expired",
			m.clusterID, cur.Generation)
	}

	m.mu.Lock()
	m.current = &lease
	m.mu.Unlock()

	return &lease, nil
}

// Release voluntarily surrenders the lease.
// After Release, another instance can immediately acquire it.
func (m *Manager) Release(ctx context.Context) error {
	m.mu.Lock()
	cur := m.current
	m.mu.Unlock()

	if cur == nil {
		return nil // nothing to release
	}

	now := time.Now().UTC()
	_, err := m.pool.Exec(ctx, `
		UPDATE toris_control.leases
		SET status      = 'released',
		    released_at = $1
		WHERE cluster_id  = $2
		  AND instance_id = $3
		  AND generation  = $4
	`, now, m.clusterID, m.instanceID, cur.Generation)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeLeaseNotHeld, err,
			"releasing lease for cluster %s", m.clusterID)
	}

	m.mu.Lock()
	m.current = nil
	m.mu.Unlock()

	m.log.Info("lease released",
		"cluster_id", m.clusterID,
		"generation", cur.Generation,
	)
	return nil
}

// Status returns the current lease state from the control database.
func (m *Manager) Status(ctx context.Context) (*model.Lease, error) {
	var lease model.Lease
	err := m.pool.QueryRow(ctx, `
		SELECT id, cluster_id, instance_id, leader_id, generation,
		       status, acquired_at, expires_at, last_heartbeat, released_at
		FROM toris_control.leases
		WHERE cluster_id = $1
	`, m.clusterID).Scan(
		&lease.ID, &lease.ClusterID, &lease.InstanceID, &lease.LeaderID,
		&lease.Generation, &lease.Status,
		&lease.AcquiredAt, &lease.ExpiresAt, &lease.LastHeartbeat, &lease.ReleasedAt,
	)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeNotFound, err,
			"no lease record found for cluster %s", m.clusterID)
	}
	return &lease, nil
}

// HoldingLease returns true if this instance currently holds an active, non-expired lease.
func (m *Manager) HoldingLease() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	return !m.current.IsExpired(time.Now().UTC()) &&
		m.current.Status == model.LeaseStatusActive
}

// CurrentGeneration returns the generation of the currently held lease, or 0.
func (m *Manager) CurrentGeneration() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return 0
	}
	return m.current.Generation
}

// AssertFencingToken returns an error if the given token does not match
// the current lease generation. Use this before any cluster-mutating operation.
func (m *Manager) AssertFencingToken(token int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return torerrors.New(torerrors.CodeLeaseNotHeld,
			"fencing check failed: no lease is held by this instance")
	}
	if token != m.current.Generation {
		return torerrors.Newf(torerrors.CodeFencingViolation,
			"fencing token mismatch: operation carries token %d but current generation is %d",
			token, m.current.Generation)
	}
	if m.current.IsExpired(time.Now().UTC()) {
		return torerrors.New(torerrors.CodeLeaseExpired,
			"fencing check failed: lease has expired")
	}
	return nil
}

// RunRenewLoop starts a background goroutine that renews the lease on schedule.
// It returns when ctx is canceled. The caller must have already acquired the lease.
func (m *Manager) RunRenewLoop(ctx context.Context) error {
	ticker := time.NewTicker(m.renewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := m.Renew(ctx); err != nil {
				m.log.Error("lease renewal failed — losing leadership",
					"cluster_id", m.clusterID,
					"error", err.Error(),
				)
				// Clear current so HoldingLease returns false.
				m.mu.Lock()
				m.current = nil
				m.mu.Unlock()
				return torerrors.Wrap(torerrors.CodeLeaseExpired, "lease renewal loop exiting", err)
			}
		}
	}
}
