//go:build integration

// Package integration contains end-to-end tests that require a running
// PostgreSQL cluster. They are gated behind the "integration" build tag
// so they don't run in the normal unit test pass.
//
// Prerequisites:
//
//	docker compose up -d   (from this directory)
//	go test ./tests/integration/... -tags integration -timeout 300s -v
//
// Environment variables (all have defaults matching docker-compose.yml):
//
//	TORIS_TEST_CONTROL_DSN   DSN for the toris control database
//	TORIS_TEST_PRIMARY_DSN   DSN for the managed primary node
//	TORIS_TEST_REPLICA1_DSN  DSN for replica 1
//	TORIS_TEST_REPLICA2_DSN  DSN for replica 2
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── DSN defaults ────────────────────────────────────────────────────────────

const (
	defaultControlDSN  = "host=localhost port=5440 user=toris password=toris_control_pass dbname=toris_control sslmode=disable"
	defaultPrimaryDSN  = "host=localhost port=5441 user=postgres password=postgres_pass dbname=testdb sslmode=disable"
	defaultReplica1DSN = "host=localhost port=5442 user=postgres password=postgres_pass dbname=postgres sslmode=disable"
	defaultReplica2DSN = "host=localhost port=5443 user=postgres password=postgres_pass dbname=postgres sslmode=disable"
)

// TestCluster holds live connections to all nodes in the test cluster.
type TestCluster struct {
	Control  *pgxpool.Pool
	Primary  *pgxpool.Pool
	Replica1 *pgxpool.Pool
	Replica2 *pgxpool.Pool

	ControlDSN  string
	PrimaryDSN  string
	Replica1DSN string
	Replica2DSN string
}

// NewTestCluster connects to all cluster nodes. Skips the test if any
// node is unreachable (i.e. docker compose is not running).
func NewTestCluster(t *testing.T) *TestCluster {
	t.Helper()

	tc := &TestCluster{
		ControlDSN:  envOrDefault("TORIS_TEST_CONTROL_DSN", defaultControlDSN),
		PrimaryDSN:  envOrDefault("TORIS_TEST_PRIMARY_DSN", defaultPrimaryDSN),
		Replica1DSN: envOrDefault("TORIS_TEST_REPLICA1_DSN", defaultReplica1DSN),
		Replica2DSN: envOrDefault("TORIS_TEST_REPLICA2_DSN", defaultReplica2DSN),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	if tc.Control, err = connectPool(ctx, tc.ControlDSN); err != nil {
		t.Skipf("control DB unavailable (%v) — run: docker compose up -d", err)
	}
	if tc.Primary, err = connectPool(ctx, tc.PrimaryDSN); err != nil {
		t.Skipf("primary unavailable (%v) — run: docker compose up -d", err)
	}
	if tc.Replica1, err = connectPool(ctx, tc.Replica1DSN); err != nil {
		t.Skipf("replica-1 unavailable (%v) — run: docker compose up -d", err)
	}
	if tc.Replica2, err = connectPool(ctx, tc.Replica2DSN); err != nil {
		t.Skipf("replica-2 unavailable (%v) — run: docker compose up -d", err)
	}

	t.Cleanup(func() {
		tc.Control.Close()
		tc.Primary.Close()
		tc.Replica1.Close()
		tc.Replica2.Close()
	})

	return tc
}

// IsPrimary returns true if the node at pool is the writable primary.
func IsPrimary(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var inRecovery bool
	if err := pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery); err != nil {
		return false, err
	}
	return !inRecovery, nil
}

// IsReplica returns true if the node is in recovery (replica/standby).
func IsReplica(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	primary, err := IsPrimary(ctx, pool)
	return !primary, err
}

// WaitForReplication waits until the replica at pool is streaming from the
// primary, up to the given deadline. Used after cluster operations that
// require replication to catch up.
func WaitForReplication(t *testing.T, pool *pgxpool.Pool, maxWait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("replica did not start streaming within %s", maxWait)
		case <-time.After(500 * time.Millisecond):
			var streaming bool
			err := pool.QueryRow(ctx,
				"SELECT pg_is_in_recovery() AND pg_last_wal_receive_lsn() IS NOT NULL",
			).Scan(&streaming)
			if err == nil && streaming {
				return
			}
		}
	}
}

// WaitForPrimary polls pool until it becomes a writable primary, up to maxWait.
// Use after pg_promote() to wait for the promotion to complete.
func WaitForPrimary(t *testing.T, pool *pgxpool.Pool, maxWait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("node did not become primary within %s", maxWait)
		case <-time.After(500 * time.Millisecond):
			primary, err := IsPrimary(ctx, pool)
			if err == nil && primary {
				return
			}
		}
	}
}

// ExecSQL runs a SQL statement against pool. Fatals on error.
func ExecSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("ExecSQL %q: %v", sql, err)
	}
}

// QueryInt runs a single-column integer query and returns the value.
func QueryInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var v int64
	if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
		t.Fatalf("QueryInt %q: %v", sql, err)
	}
	return v
}

// EnsureControlSchema creates the toris_control schema and tables via
// the same code path the daemon uses.
func EnsureControlSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS toris_control`)
	return err
}

// ReplicationLag returns the replication lag in bytes for the replica at pool.
// Returns -1 if the node is not in recovery.
func ReplicationLag(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var lag int64
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			pg_wal_lsn_diff(
				pg_last_wal_receive_lsn(),
				pg_last_wal_replay_lsn()
			), 0
		)
	`).Scan(&lag)
	if err != nil {
		return -1, fmt.Errorf("querying replication lag: %w", err)
	}
	return lag, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func connectPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
