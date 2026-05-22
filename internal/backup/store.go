// Package backup - store.go
// BackupStore persists Backup records to toris_control.backups.
// It is the authoritative history of every backup operation.
// The storage backend holds the artifacts; this table holds the metadata.
package backup

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// Store persists and queries Backup records in the control database.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a Store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureSchema creates the backups table if it does not exist. Idempotent.
func (s *Store) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS toris_control.backups (
			id           TEXT        NOT NULL,
			cluster_id   TEXT        NOT NULL,
			node_id      TEXT        NOT NULL,
			generation   BIGINT      NOT NULL DEFAULT 0,
			status       TEXT        NOT NULL,
			storage_path TEXT        NOT NULL DEFAULT '',
			size_bytes   BIGINT      NOT NULL DEFAULT 0,
			started_at   TIMESTAMPTZ NOT NULL,
			finished_at  TIMESTAMPTZ,
			verified_at  TIMESTAMPTZ,
			uploaded_at  TIMESTAMPTZ,
			pruned_at    TIMESTAMPTZ,
			failure_msg  TEXT        NOT NULL DEFAULT '',

			CONSTRAINT backups_pkey PRIMARY KEY (id)
		);

		CREATE INDEX IF NOT EXISTS backups_cluster_idx
			ON toris_control.backups (cluster_id, started_at DESC);

		CREATE INDEX IF NOT EXISTS backups_status_idx
			ON toris_control.backups (cluster_id, status);
	`)
	if err != nil {
		return fmt.Errorf("ensuring backups schema: %w", err)
	}
	return nil
}

// Insert writes a new Backup record. Called immediately after the pipeline
// creates the backup struct, before pg_basebackup runs, so the backup is
// visible in the control DB even if the daemon crashes mid-run.
func (s *Store) Insert(ctx context.Context, b *model.Backup) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO toris_control.backups
			(id, cluster_id, node_id, generation, status,
			 storage_path, size_bytes, started_at,
			 finished_at, verified_at, uploaded_at, pruned_at, failure_msg)
		VALUES
			($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO NOTHING
	`,
		b.ID, b.ClusterID, b.NodeID, b.Generation, string(b.Status),
		b.StoragePath, b.SizeBytes, b.StartedAt,
		b.FinishedAt, b.VerifiedAt, b.UploadedAt, b.PrunedAt, b.FailureMsg,
	)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"inserting backup record %s", b.ID)
	}
	return nil
}

// UpdateStatus updates the mutable fields of a Backup record after a status
// transition. Called at each pipeline stage: running, verified, uploaded, failed.
func (s *Store) UpdateStatus(ctx context.Context, b *model.Backup) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE toris_control.backups
		SET status       = $1,
		    storage_path = $2,
		    size_bytes   = $3,
		    finished_at  = $4,
		    verified_at  = $5,
		    uploaded_at  = $6,
		    pruned_at    = $7,
		    failure_msg  = $8
		WHERE id = $9
	`,
		string(b.Status),
		b.StoragePath, b.SizeBytes,
		b.FinishedAt, b.VerifiedAt, b.UploadedAt, b.PrunedAt,
		b.FailureMsg,
		b.ID,
	)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"updating backup record %s", b.ID)
	}
	return nil
}

// MarkPruned stamps a backup as pruned without deleting the row.
// The row is retained for audit history.
func (s *Store) MarkPruned(ctx context.Context, backupID string) error {
	now := util.NowUTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE toris_control.backups
		SET status    = $1,
		    pruned_at = $2
		WHERE id = $3
	`, string(model.BackupStatusPruned), now, backupID)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"marking backup %s pruned", backupID)
	}
	return nil
}

// Get returns a single Backup by ID.
func (s *Store) Get(ctx context.Context, id string) (*model.Backup, error) {
	var b model.Backup
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, node_id, generation, status,
		       storage_path, size_bytes, started_at,
		       finished_at, verified_at, uploaded_at, pruned_at, failure_msg
		FROM toris_control.backups
		WHERE id = $1
	`, id).Scan(
		&b.ID, &b.ClusterID, &b.NodeID, &b.Generation, &status,
		&b.StoragePath, &b.SizeBytes, &b.StartedAt,
		&b.FinishedAt, &b.VerifiedAt, &b.UploadedAt, &b.PrunedAt, &b.FailureMsg,
	)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeNotFound, err,
			"backup %s not found", id)
	}
	b.Status = model.BackupStatus(status)
	return &b, nil
}

// List returns backups for a cluster ordered newest first.
// If limit is 0, all records are returned.
func (s *Store) List(ctx context.Context, clusterID string, limit int) ([]*model.Backup, error) {
	query := `
		SELECT id, cluster_id, node_id, generation, status,
		       storage_path, size_bytes, started_at,
		       finished_at, verified_at, uploaded_at, pruned_at, failure_msg
		FROM toris_control.backups
		WHERE cluster_id = $1
		ORDER BY started_at DESC
	`
	args := []any{clusterID}
	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"listing backups for cluster %s", clusterID)
	}
	defer rows.Close()

	var backups []*model.Backup
	for rows.Next() {
		var b model.Backup
		var status string
		if err := rows.Scan(
			&b.ID, &b.ClusterID, &b.NodeID, &b.Generation, &status,
			&b.StoragePath, &b.SizeBytes, &b.StartedAt,
			&b.FinishedAt, &b.VerifiedAt, &b.UploadedAt, &b.PrunedAt, &b.FailureMsg,
		); err != nil {
			return nil, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err, "scanning backup row")
		}
		b.Status = model.BackupStatus(status)
		backups = append(backups, &b)
	}
	return backups, rows.Err()
}

// LatestVerified returns the most recently verified backup for a cluster.
// Used by reseed and rewind to auto-select the fallback backup.
func (s *Store) LatestVerified(ctx context.Context, clusterID string) (*model.Backup, error) {
	var b model.Backup
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, node_id, generation, status,
		       storage_path, size_bytes, started_at,
		       finished_at, verified_at, uploaded_at, pruned_at, failure_msg
		FROM toris_control.backups
		WHERE cluster_id = $1
		  AND status IN ('verified', 'uploaded', 'retained')
		ORDER BY verified_at DESC NULLS LAST
		LIMIT 1
	`, clusterID).Scan(
		&b.ID, &b.ClusterID, &b.NodeID, &b.Generation, &status,
		&b.StoragePath, &b.SizeBytes, &b.StartedAt,
		&b.FinishedAt, &b.VerifiedAt, &b.UploadedAt, &b.PrunedAt, &b.FailureMsg,
	)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeNotFound, err,
			"no verified backup found for cluster %s", clusterID)
	}
	b.Status = model.BackupStatus(status)
	return &b, nil
}

// FreshestVerifiedAt returns the timestamp of the most recent verified backup.
// Returns the zero time if none exist. Used by cluster status and L5 health checks.
func (s *Store) FreshestVerifiedAt(ctx context.Context, clusterID string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(verified_at), '1970-01-01'::timestamptz)
		FROM toris_control.backups
		WHERE cluster_id = $1
		  AND status IN ('verified', 'uploaded', 'retained')
	`, clusterID).Scan(&t)
	if err != nil {
		return time.Time{}, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"querying freshest verified backup for cluster %s", clusterID)
	}
	return t, nil
}

// ListByStatus returns backups matching any of the given statuses.
func (s *Store) ListByStatus(ctx context.Context, clusterID string, statuses ...model.BackupStatus) ([]*model.Backup, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	args := []any{clusterID}
	placeholders := ""
	for i, st := range statuses {
		if i > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("$%d", i+2)
		args = append(args, string(st))
	}

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, cluster_id, node_id, generation, status,
		       storage_path, size_bytes, started_at,
		       finished_at, verified_at, uploaded_at, pruned_at, failure_msg
		FROM toris_control.backups
		WHERE cluster_id = $1
		  AND status IN (%s)
		ORDER BY started_at DESC
	`, placeholders), args...)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err,
			"listing backups by status for cluster %s", clusterID)
	}
	defer rows.Close()

	var backups []*model.Backup
	for rows.Next() {
		var b model.Backup
		var status string
		if err := rows.Scan(
			&b.ID, &b.ClusterID, &b.NodeID, &b.Generation, &status,
			&b.StoragePath, &b.SizeBytes, &b.StartedAt,
			&b.FinishedAt, &b.VerifiedAt, &b.UploadedAt, &b.PrunedAt, &b.FailureMsg,
		); err != nil {
			return nil, torerrors.Wrapf(torerrors.CodeDBQueryFailed, err, "scanning backup row")
		}
		b.Status = model.BackupStatus(status)
		backups = append(backups, &b)
	}
	return backups, rows.Err()
}
