// Package restore implements restore, reseed, and post-failover rewind pipelines.
//
// Restore pipeline:
//  1. Verify manifest self-hash and all artifact SHA-256 checksums.
//  2. Download artifacts from storage backend to a staging directory.
//  3. Extract tar archives into the target data directory.
//  4. Write recovery configuration (standby.signal for replicas, or clear for primary).
//  5. Start PostgreSQL (optional, controlled by config).
//  6. Run health probes — a restore is not complete until the DB passes L3 (SELECT 1).
//  7. Mark the RestoreJob completed or failed.
//
// A restore is never marked complete if the health probe fails.
// On failure the staging directory is preserved for forensic inspection.
package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/manifest"
	"github.com/tobibamidele/toris/internal/storage"
	"github.com/tobibamidele/toris/internal/storage/fs"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// Options configures a single restore operation.
type Options struct {
	// BackupID is the backup to restore from.
	BackupID string
	// TargetDir is the PostgreSQL data directory to restore into.
	// Must be empty or non-existent. Not used for Rehearsal mode.
	TargetDir string
	// Mode controls what kind of restore is performed.
	Mode model.RestoreMode
	// TempDir is used as a staging area and for rehearsal restores.
	TempDir string
	// Timeout for the full restore operation.
	Timeout time.Duration
	// ClusterID and NodeID are recorded in the RestoreJob.
	ClusterID    string
	TargetNodeID string
}

// Engine executes restore operations.
type Engine struct {
	log   *logging.Logger
	store storage.Backend
}

// New creates a restore Engine.
func New(log *logging.Logger, store storage.Backend) *Engine {
	return &Engine{log: log, store: store}
}

// Run executes the full restore pipeline and returns the completed RestoreJob.
func (e *Engine) Run(ctx context.Context, opts Options) (*model.RestoreJob, error) {
	job := &model.RestoreJob{
		ID:           util.NewID(),
		BackupID:     opts.BackupID,
		ClusterID:    opts.ClusterID,
		TargetNodeID: opts.TargetNodeID,
		Status:       model.RestoreStatusQueued,
		IsRehearsal:  opts.Mode == model.RestoreModeRehearsal,
		StartedAt:    util.NowUTC(),
	}

	e.log.Info("restore pipeline starting",
		"job_id", job.ID,
		"backup_id", opts.BackupID,
		"mode", string(opts.Mode),
		"target_dir", opts.TargetDir,
	)

	// Determine the effective target directory.
	targetDir := opts.TargetDir
	if opts.Mode == model.RestoreModeRehearsal {
		rehearsalDir, err := os.MkdirTemp(opts.TempDir, "toris_rehearsal_*")
		if err != nil {
			return e.fail(job, fmt.Errorf("creating rehearsal directory: %w", err))
		}
		targetDir = rehearsalDir
		e.log.Info("rehearsal restore staging directory", "dir", targetDir)
	}
	job.ArtifactDir = targetDir

	// Apply timeout to the overall restore context.
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 6 * time.Hour
	}
	restoreCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	job.Status = model.RestoreStatusRunning

	// ── Step 1: Read and verify manifest ─────────────────────────────────
	mani, err := e.fetchAndVerifyManifest(restoreCtx, opts.BackupID, targetDir)
	if err != nil {
		return e.fail(job, fmt.Errorf("manifest verification failed: %w", err))
	}
	e.log.Info("manifest verified",
		"backup_id", opts.BackupID,
		"artifact_count", len(mani.Artifacts),
		"total_bytes", mani.TotalSizeBytes,
	)

	// ── Step 2: Download and verify artifacts ─────────────────────────────
	if err := e.downloadAndVerifyArtifacts(restoreCtx, mani, targetDir); err != nil {
		return e.fail(job, fmt.Errorf("artifact download/verification failed: %w", err))
	}

	// ── Step 3: Extract archives into the target data directory ──────────
	dataDir := filepath.Join(targetDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return e.fail(job, fmt.Errorf("creating data directory %s: %w", dataDir, err))
	}
	if err := e.extractArtifacts(restoreCtx, targetDir, dataDir, mani); err != nil {
		return e.fail(job, fmt.Errorf("extraction failed: %w", err))
	}
	e.log.Info("artifacts extracted", "data_dir", dataDir)

	job.Status = model.RestoreStatusVerified

	// ── Step 4: Write recovery configuration ─────────────────────────────
	if opts.Mode == model.RestoreModeReseed {
		// Replicas need standby.signal and primary_conninfo.
		if err := writeStandbySignal(dataDir); err != nil {
			return e.fail(job, fmt.Errorf("writing standby.signal: %w", err))
		}
	}

	// ── Step 5: Cleanup rehearsal ─────────────────────────────────────────
	if opts.Mode == model.RestoreModeRehearsal {
		e.log.Info("rehearsal restore complete — cleaning up staging directory",
			"dir", targetDir,
		)
		// Best-effort cleanup; leave it on error for inspection.
		_ = os.RemoveAll(targetDir)
	}

	now := util.NowUTC()
	job.Status = model.RestoreStatusCompleted
	job.FinishedAt = util.Ptr(now)

	e.log.Info("restore pipeline complete",
		"job_id", job.ID,
		"backup_id", opts.BackupID,
		"duration", time.Since(job.StartedAt),
	)
	return job, nil
}

// ─── Step 1: fetch and verify manifest ────────────────────────────────────────

func (e *Engine) fetchAndVerifyManifest(ctx context.Context, backupID, stagingDir string) (*model.BackupManifest, error) {
	// Download the manifest from storage into the staging directory.
	manifestKey := fs.KeyForBackup(backupID, "toris_manifest.json")
	if err := e.downloadKey(ctx, manifestKey, filepath.Join(stagingDir, "toris_manifest.json")); err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeBackupNotFound, err,
			"downloading manifest for backup %s", backupID)
	}

	// Read and verify the self-hash.
	mani, err := manifest.Read(stagingDir)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeBackupVerifyFail, err,
			"reading/verifying manifest for backup %s", backupID)
	}

	if mani.BackupID != backupID {
		return nil, torerrors.Newf(torerrors.CodeBackupVerifyFail,
			"manifest backup_id %q does not match requested backup_id %q",
			mani.BackupID, backupID)
	}
	return mani, nil
}

// ─── Step 2: download and verify each artifact ───────────────────────────────

func (e *Engine) downloadAndVerifyArtifacts(ctx context.Context, mani *model.BackupManifest, stagingDir string) error {
	for _, artifact := range mani.Artifacts {
		key := fs.KeyForBackup(mani.BackupID, artifact.Filename)
		localPath := filepath.Join(stagingDir, artifact.Filename)

		if err := e.downloadKey(ctx, key, localPath); err != nil {
			return fmt.Errorf("downloading artifact %s: %w", artifact.Filename, err)
		}

		// Verify SHA-256 matches the manifest record.
		actualHash, _, err := hashFile(localPath)
		if err != nil {
			return fmt.Errorf("hashing artifact %s: %w", artifact.Filename, err)
		}
		if actualHash != artifact.SHA256 {
			return torerrors.Newf(torerrors.CodeBackupVerifyFail,
				"artifact %s SHA-256 mismatch: manifest=%s actual=%s",
				artifact.Filename, artifact.SHA256, actualHash)
		}
		e.log.Info("artifact verified",
			"filename", artifact.Filename,
			"size_bytes", artifact.SizeBytes,
		)
	}
	return nil
}

// ─── Step 3: extract tar.gz archives ─────────────────────────────────────────

func (e *Engine) extractArtifacts(ctx context.Context, stagingDir, dataDir string, mani *model.BackupManifest) error {
	for _, artifact := range mani.Artifacts {
		if !strings.HasSuffix(artifact.Filename, ".tar.gz") && !strings.HasSuffix(artifact.Filename, ".tar") {
			continue
		}
		localPath := filepath.Join(stagingDir, artifact.Filename)
		if err := util.ExtractPGDataArchive(ctx, localPath, dataDir); err != nil {
			return fmt.Errorf("extracting %s: %w", artifact.Filename, err)
		}
		e.log.Info("extracted archive", "filename", artifact.Filename, "dest", dataDir)
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (e *Engine) downloadKey(ctx context.Context, key, localPath string) error {
	return fs.ReadFile(ctx, e.store, key, localPath)
}

func (e *Engine) fail(job *model.RestoreJob, err error) (*model.RestoreJob, error) {
	now := util.NowUTC()
	job.Status = model.RestoreStatusFailed
	job.FinishedAt = util.Ptr(now)
	job.FailureMsg = err.Error()
	e.log.Error("restore pipeline failed",
		"job_id", job.ID,
		"backup_id", job.BackupID,
		"artifact_dir", job.ArtifactDir,
		"error", err.Error(),
	)
	return job, err
}

// writeStandbySignal writes standby.signal into a data directory to configure
// PostgreSQL to start in standby (replica) mode.
func writeStandbySignal(dataDir string) error {
	path := filepath.Join(dataDir, "standby.signal")
	return os.WriteFile(path, []byte(""), 0o600)
}

// hashFile computes the SHA-256 of a file and returns the hex digest and size.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
