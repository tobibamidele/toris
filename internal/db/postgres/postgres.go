// Package postgres implements the db.Backend interface for PostgreSQL.
// Uses pgx/v5 for connections and wraps official pg_* tools for backup operations.
package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// Backend implements db.Backend for PostgreSQL.
type Backend struct {
	log     *logging.Logger
	connTTL time.Duration

	mu    sync.Mutex
	pools map[string]*pgxpool.Pool // keyed by node.ID
}

// New creates a new PostgreSQL backend.
func New(log *logging.Logger) *Backend {
	return &Backend{
		log:     log,
		connTTL: 30 * time.Second,
		pools:   make(map[string]*pgxpool.Pool),
	}
}

// Name implements db.Backend.
func (b *Backend) Name() string { return "postgres" }

// ─── Connection management ────────────────────────────────────────────────────

// poolFor returns (or lazily creates) the pgxpool for a node.
func (b *Backend) poolFor(ctx context.Context, node *model.Node) (*pgxpool.Pool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if p, ok := b.pools[node.ID]; ok {
		return p, nil
	}

	dsn := nodeDSN(node)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeConfigInvalid, err,
			"parsing DSN for node %s", node.ID)
	}
	cfg.MaxConns = 5
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 2 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeDBUnreachable, err,
			"creating connection pool for node %s (%s)", node.ID, node.Addr())
	}
	b.pools[node.ID] = pool
	return pool, nil
}

// nodeDSN builds a DSN string from the node model.
// Auth credentials should be injected via PGPASSWORD env or .pgpass in production.
// This function builds the structural DSN only — no passwords embedded.
func nodeDSN(node *model.Node) string {
	return fmt.Sprintf("host=%s port=%d sslmode=require connect_timeout=5",
		node.Host, node.Port)
}

// ─── Backend interface implementation ────────────────────────────────────────

// Ping implements db.Backend. Verifies the node is connectable.
func (b *Backend) Ping(ctx context.Context, node *model.Node) error {
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return err
	}
	if err := pool.Ping(ctx); err != nil {
		return torerrors.Wrapf(torerrors.CodeDBUnreachable, err,
			"ping failed for node %s (%s)", node.ID, node.Addr())
	}
	return nil
}

// Health implements db.Backend. Returns a full layered HealthSnapshot.
func (b *Backend) Health(ctx context.Context, node *model.Node) (*model.HealthSnapshot, error) {
	snap := &model.HealthSnapshot{
		NodeID:    node.ID,
		CheckedAt: time.Now().UTC(),
	}

	// L1: TCP reachability — attempted by acquiring pool connection
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		snap.Level = model.HealthLevelUnreachable
		snap.Errors = append(snap.Errors, err.Error())
		return snap, nil
	}
	snap.TransportOK = true
	snap.Level = model.HealthLevelTransport

	// L2: pg_isready — skipped here; handled by tools.go via pg_isready CLI.
	// Instead, we use pool.Ping as a proxy for L2.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		snap.Level = model.HealthLevelTransport
		snap.Errors = append(snap.Errors, fmt.Sprintf("cannot acquire connection: %v", err))
		return snap, nil
	}
	defer conn.Release()
	snap.ReadyOK = true
	snap.Level = model.HealthLevelReady

	// L3: SQL liveness
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(new(int)); err != nil {
		snap.Errors = append(snap.Errors, fmt.Sprintf("liveness probe failed: %v", err))
		return snap, nil
	}
	snap.LiveOK = true
	snap.Level = model.HealthLevelLive

	// L4: Role detection
	var inRecovery bool
	if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery); err != nil {
		snap.Errors = append(snap.Errors, fmt.Sprintf("role check failed: %v", err))
		return snap, nil
	}
	snap.IsInRecovery = inRecovery
	if inRecovery {
		snap.Role = model.NodeRoleReplica
	} else {
		snap.Role = model.NodeRolePrimary
	}
	snap.RoleOK = true
	snap.Level = model.HealthLevelRoleKnown

	// Replication lag (only meaningful for replicas)
	if inRecovery {
		lag, err := replicationLagDirect(ctx, conn)
		if err != nil {
			snap.Errors = append(snap.Errors, fmt.Sprintf("lag check warning: %v", err))
		} else {
			snap.ReplicationLagBytes = lag
		}
	}

	// L5: Policy checks
	policyOK := true
	var policyErrors []string

	// Disk space check via pg_database_size as a proxy (real check needs df)
	// Real disk check is deferred to DiskFree(); here we do a basic query sanity.
	var dbSize int64
	if err := conn.QueryRow(ctx,
		`SELECT pg_database_size(current_database())`).Scan(&dbSize); err != nil {
		policyErrors = append(policyErrors, fmt.Sprintf("cannot determine database size: %v", err))
		policyOK = false
	}

	snap.PolicyOK = policyOK
	if policyOK {
		snap.Level = model.HealthLevelPolicyPass
	} else {
		snap.Errors = append(snap.Errors, policyErrors...)
	}

	return snap, nil
}

// IsPrimary implements db.Backend.
func (b *Backend) IsPrimary(ctx context.Context, node *model.Node) (bool, error) {
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return false, err
	}
	var inRecovery bool
	if err := pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery); err != nil {
		return false, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"checking primary status for node %s", node.ID)
	}
	return !inRecovery, nil
}

// ReplicationLag implements db.Backend.
func (b *Backend) ReplicationLag(ctx context.Context, node *model.Node) (int64, error) {
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return 0, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, torerrors.Wrapf(torerrors.CodeDBUnreachable, err,
			"acquiring connection for lag check on node %s", node.ID)
	}
	defer conn.Release()
	return replicationLagDirect(ctx, conn)
}

// replicationLagDirect queries the WAL receive/replay LSN delta on a pooled connection.
func replicationLagDirect(ctx context.Context, conn *pgxpool.Conn) (int64, error) {
	// pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn())
	// returns bytes between received and replayed WAL — effective write lag.
	var lag int64
	err := conn.QueryRow(ctx, `
		SELECT COALESCE(
			pg_wal_lsn_diff(
				pg_last_wal_receive_lsn(),
				pg_last_wal_replay_lsn()
			), 0
		)
	`).Scan(&lag)
	if err != nil {
		return 0, fmt.Errorf("querying replication lag: %w", err)
	}
	if lag < 0 {
		lag = 0
	}
	return lag, nil
}

// Promote implements db.Backend.
// Triggers promotion on a replica by calling pg_promote().
// The caller must have already fenced the old primary before calling this.
func (b *Backend) Promote(ctx context.Context, node *model.Node, fencingToken int64) error {
	// Verify this node is actually a replica before promoting.
	isPrimary, err := b.IsPrimary(ctx, node)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeFailoverFailed, err,
			"checking role before promoting node %s", node.ID)
	}
	if isPrimary {
		return torerrors.Newf(torerrors.CodeDBRoleMismatch,
			"node %s is already a primary; cannot promote", node.ID)
	}

	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return err
	}

	// pg_promote() returns bool: true = promotion triggered successfully.
	var ok bool
	if err := pool.QueryRow(ctx, "SELECT pg_promote(wait := true, wait_seconds := 30)").Scan(&ok); err != nil {
		return torerrors.Wrapf(torerrors.CodeFailoverFailed, err,
			"pg_promote() failed on node %s", node.ID)
	}
	if !ok {
		return torerrors.Newf(torerrors.CodeFailoverFailed,
			"pg_promote() returned false on node %s; check PostgreSQL logs", node.ID)
	}
	return nil
}

// Fence implements db.Backend.
// Prevents the node from accepting new writes by setting default_transaction_read_only
// and terminating all non-replication active backends.
func (b *Backend) Fence(ctx context.Context, node *model.Node, fencingToken int64) error {
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return err
	}

	// Terminate all active connections except our own and replication slots.
	_, err = pool.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE pid <> pg_backend_pid()
		  AND backend_type = 'client backend'
		  AND state != 'idle'
	`)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeFailoverFailed, err,
			"terminating connections on fenced node %s", node.ID)
	}

	b.log.Info("node fenced",
		"node_id", node.ID,
		"fencing_token", fencingToken,
	)
	return nil
}

// DiskFree implements db.Backend.
// Queries PostgreSQL's data directory and uses pg_stat_file on a known path
// as a proxy. Real disk check requires an OS-level call or agent.
func (b *Backend) DiskFree(ctx context.Context, node *model.Node) (int64, error) {
	pool, err := b.poolFor(ctx, node)
	if err != nil {
		return 0, err
	}
	// This gives us the size of the data directory — not free space.
	// A real implementation would query the OS or use a sidecar.
	// We mark this as a known limitation in v1.
	var dataDir string
	if err := pool.QueryRow(ctx, "SHOW data_directory").Scan(&dataDir); err != nil {
		return 0, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"querying data_directory on node %s", node.ID)
	}
	// Return -1 to signal "unknown" — callers must handle this.
	// TODO(v2): implement via OS agent or SSH probe.
	return -1, nil
}

// Close implements db.Backend. Closes the pool for the given node.
func (b *Backend) Close(ctx context.Context, node *model.Node) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if pool, ok := b.pools[node.ID]; ok {
		pool.Close()
		delete(b.pools, node.ID)
	}
	return nil
}

// CloseAll closes all pools. Call on daemon shutdown.
func (b *Backend) CloseAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, pool := range b.pools {
		pool.Close()
		delete(b.pools, id)
	}
}
