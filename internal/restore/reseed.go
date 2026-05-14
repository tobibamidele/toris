package restore

import (
	"context"
	"fmt"
	"time"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/storage"
	"github.com/tobibamidele/toris/pkg/model"
)

// ReseedOptions configures a replica reseed operation.
type ReseedOptions struct {
	// BackupID is the verified backup to reseed from.
	// If empty, the caller must have already selected the latest verified backup.
	BackupID string
	// TargetDir is the PostgreSQL data directory to overwrite.
	TargetDir string
	// TempDir is used for staging.
	TempDir string
	// ClusterID and TargetNodeID are recorded in the RestoreJob.
	ClusterID    string
	TargetNodeID string
	// Timeout for the full reseed.
	Timeout time.Duration
}

// Reseeder reseeds a replica node from a verified backup.
type Reseeder struct {
	log    *logging.Logger
	store  storage.Backend
	engine *Engine
}

// NewReseeder creates a Reseeder.
func NewReseeder(log *logging.Logger, store storage.Backend) *Reseeder {
	return &Reseeder{
		log:    log,
		store:  store,
		engine: New(log, store),
	}
}

// Reseed restores the latest verified backup into a replica's data directory,
// writing standby.signal so the node starts in recovery mode.
//
// The caller is responsible for:
//   - Stopping the target PostgreSQL instance before calling Reseed.
//   - Starting the instance after Reseed returns successfully.
func (r *Reseeder) Reseed(ctx context.Context, opts ReseedOptions) (*model.RestoreJob, error) {
	if opts.BackupID == "" {
		return nil, fmt.Errorf("reseed requires a backup_id; select the latest verified backup before calling")
	}
	if opts.TargetDir == "" {
		return nil, fmt.Errorf("reseed requires a target data directory")
	}

	r.log.Info("reseeding replica from backup",
		"backup_id", opts.BackupID,
		"target_node", opts.TargetNodeID,
		"target_dir", opts.TargetDir,
	)

	job, err := r.engine.Run(ctx, Options{
		BackupID:     opts.BackupID,
		TargetDir:    opts.TargetDir,
		Mode:         model.RestoreModeReseed,
		TempDir:      opts.TempDir,
		Timeout:      opts.Timeout,
		ClusterID:    opts.ClusterID,
		TargetNodeID: opts.TargetNodeID,
	})
	if err != nil {
		return job, fmt.Errorf("reseed failed: %w", err)
	}

	r.log.Info("reseed complete",
		"backup_id", opts.BackupID,
		"target_node", opts.TargetNodeID,
		"duration", time.Since(job.StartedAt),
	)
	return job, nil
}
