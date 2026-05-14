package restore

import (
	"context"
	"fmt"
	"time"

	pgtools "github.com/tobibamidele/toris/internal/db/postgres"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/storage"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// RewindOptions configures a post-failover rewind or reseed operation.
type RewindOptions struct {
	// OldPrimary is the node that was demoted.
	OldPrimary *model.Node
	// NewPrimary is the node that was promoted.
	NewPrimary *model.Node
	// OldPrimaryDataDir is the filesystem path to the old primary's data directory.
	// Required for pg_rewind.
	OldPrimaryDataDir string
	// NewPrimaryDSN is the connection string to the new primary.
	NewPrimaryDSN string
	// NewPrimaryPassword — never logged.
	NewPrimaryPassword string
	// FallbackBackupID is the backup to use if pg_rewind fails.
	// If empty, reseed fallback is disabled and the operation fails on rewind error.
	FallbackBackupID string
	// FallbackTargetDir is where the reseed will write the data directory.
	FallbackTargetDir string
	// TempDir for staging during fallback reseed.
	TempDir string
	// ClusterID and Generation for audit records.
	ClusterID  string
	Generation int64
	// Timeouts
	RewindTimeout time.Duration
	ReseedTimeout time.Duration
}

// Rewinder attempts pg_rewind on a demoted old primary, falling back to a
// full reseed if rewind is not possible or fails.
type Rewinder struct {
	log      *logging.Logger
	tools    *pgtools.Tools
	reseeder *Reseeder
}

// NewRewinder creates a Rewinder.
func NewRewinder(log *logging.Logger, tools *pgtools.Tools, store storage.Backend) *Rewinder {
	return &Rewinder{
		log:      log,
		tools:    tools,
		reseeder: NewReseeder(log, store),
	}
}

// RewindOrReseed attempts pg_rewind on the old primary. If rewind is not
// possible or fails, and FallbackBackupID is set, it falls back to a full
// reseed. Returns a RewindJob describing what happened.
func (r *Rewinder) RewindOrReseed(ctx context.Context, opts RewindOptions) (*model.RewindJob, error) {
	job := &model.RewindJob{
		ID:           util.NewID(),
		ClusterID:    opts.ClusterID,
		NodeID:       opts.OldPrimary.ID,
		NewPrimaryID: opts.NewPrimary.ID,
		Generation:   opts.Generation,
		Status:       model.RewindStatusRunning,
		StartedAt:    util.NowUTC(),
	}

	r.log.Info("post-failover rewind starting",
		"old_primary", opts.OldPrimary.ID,
		"new_primary", opts.NewPrimary.ID,
		"generation", opts.Generation,
	)

	// ── Attempt pg_rewind ─────────────────────────────────────────────────
	rewindErr := r.attemptRewind(ctx, opts)
	if rewindErr == nil {
		now := util.NowUTC()
		job.Status = model.RewindStatusCompleted
		job.FinishedAt = util.Ptr(now)
		r.log.Info("pg_rewind succeeded",
			"old_primary", opts.OldPrimary.ID,
			"duration", time.Since(job.StartedAt),
		)
		return job, nil
	}

	r.log.Warn("pg_rewind failed — evaluating fallback",
		"old_primary", opts.OldPrimary.ID,
		"error", rewindErr.Error(),
	)

	// ── Fallback: full reseed ─────────────────────────────────────────────
	if opts.FallbackBackupID == "" {
		now := util.NowUTC()
		job.Status = model.RewindStatusFailed
		job.FinishedAt = util.Ptr(now)
		job.FailureMsg = fmt.Sprintf("pg_rewind failed and no fallback backup is configured: %v", rewindErr)
		return job, fmt.Errorf("rewind failed, no fallback: %w", rewindErr)
	}

	r.log.Info("falling back to reseed",
		"old_primary", opts.OldPrimary.ID,
		"backup_id", opts.FallbackBackupID,
	)

	reseedJob, reseedErr := r.reseeder.Reseed(ctx, ReseedOptions{
		BackupID:     opts.FallbackBackupID,
		TargetDir:    opts.FallbackTargetDir,
		TempDir:      opts.TempDir,
		ClusterID:    opts.ClusterID,
		TargetNodeID: opts.OldPrimary.ID,
		Timeout:      opts.ReseedTimeout,
	})

	now := util.NowUTC()
	job.FinishedAt = util.Ptr(now)
	job.UsedFallback = true

	if reseedErr != nil {
		job.Status = model.RewindStatusFailed
		job.FailureMsg = fmt.Sprintf("pg_rewind failed (%v); reseed also failed: %v", rewindErr, reseedErr)
		return job, fmt.Errorf("both rewind and reseed failed: rewind=%w reseed=%v", rewindErr, reseedErr)
	}

	job.Status = model.RewindStatusFallback
	r.log.Info("fallback reseed complete",
		"old_primary", opts.OldPrimary.ID,
		"backup_id", opts.FallbackBackupID,
		"reseed_job_id", reseedJob.ID,
		"duration", time.Since(job.StartedAt),
	)
	return job, nil
}

func (r *Rewinder) attemptRewind(ctx context.Context, opts RewindOptions) error {
	if opts.OldPrimaryDataDir == "" {
		return fmt.Errorf("OldPrimaryDataDir is required for pg_rewind")
	}
	if opts.NewPrimaryDSN == "" {
		return fmt.Errorf("NewPrimaryDSN is required for pg_rewind")
	}

	timeout := opts.RewindTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}

	_, err := pgtools.PgRewind(ctx, r.log, r.tools, pgtools.PgRewindOptions{
		TargetDataDir: opts.OldPrimaryDataDir,
		SourceDSN:     opts.NewPrimaryDSN,
		Password:      opts.NewPrimaryPassword,
		Timeout:       timeout,
		DryRun:        false,
	})
	return err
}
