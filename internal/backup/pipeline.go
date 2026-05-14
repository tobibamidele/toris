// Package backup implements the full backup pipeline:
// preflight → snapshot → manifest → verification → storage upload → retention.
//
// A backup is not marked successful until pg_verifybackup passes.
// A backup is not marked uploaded until all artifacts are in the storage backend.
// A backup is not marked complete if offsite_required is true and the upload fails.
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pgtools "github.com/tobibamidele/toris/internal/db/postgres"
	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/manifest"
	"github.com/tobibamidele/toris/internal/retention"
	"github.com/tobibamidele/toris/internal/storage"
	"github.com/tobibamidele/toris/internal/storage/fs"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// CreateOptions configures a single backup run.
type CreateOptions struct {
	Node                *model.Node
	ClusterID           string
	StagingDir          string
	Label               string
	DryRun              bool
	OffSiteRequired     bool
	BackupTimeout       time.Duration
	VerifyTimeout       time.Duration
	ReplicationUser     string
	ReplicationPassword string
	PostgresVersion     string
}

// Pipeline orchestrates the full backup lifecycle.
type Pipeline struct {
	log      *logging.Logger
	lm       *leader.Manager
	tools    *pgtools.Tools
	store    storage.Backend
	enforcer *retention.Enforcer
}

// NewPipeline creates a backup Pipeline.
// lm may be nil when invoked from the CLI outside daemon mode.
func NewPipeline(
	log *logging.Logger,
	lm *leader.Manager,
	tools *pgtools.Tools,
	store storage.Backend,
	enforcer *retention.Enforcer,
) *Pipeline {
	return &Pipeline{log: log, lm: lm, tools: tools, store: store, enforcer: enforcer}
}

// Create runs the full backup pipeline and returns the completed Backup record.
func (p *Pipeline) Create(ctx context.Context, opts CreateOptions) (*model.Backup, error) {
	backupID := util.NewID()
	var generation int64
	if p.lm != nil {
		generation = p.lm.CurrentGeneration()
	}

	backup := &model.Backup{
		ID:         backupID,
		ClusterID:  opts.ClusterID,
		NodeID:     opts.Node.ID,
		Generation: generation,
		Status:     model.BackupStatusPending,
		StartedAt:  util.NowUTC(),
	}

	p.log.Info("backup pipeline starting",
		"backup_id", backupID,
		"node", opts.Node.Addr(),
		"dry_run", opts.DryRun,
	)

	// ── Step 1: Preflight ─────────────────────────────────────────────────
	if err := p.preflight(ctx, opts, generation); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = err.Error()
		return backup, fmt.Errorf("preflight failed: %w", err)
	}
	if opts.DryRun {
		p.log.Info("dry-run: preflight passed, stopping before snapshot")
		backup.Status = model.BackupStatusPending
		return backup, nil
	}

	// ── Step 2: Staging directory ──────────────────────────────────────────
	stagingDir := filepath.Join(opts.StagingDir, backupID)
	if err := os.MkdirAll(stagingDir, 0o750); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = err.Error()
		return backup, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating staging directory %s", stagingDir)
	}
	backup.StoragePath = stagingDir
	backup.Status = model.BackupStatusRunning

	// ── Step 3: pg_basebackup ─────────────────────────────────────────────
	timeout := opts.BackupTimeout
	if timeout == 0 {
		timeout = 6 * time.Hour
	}
	label := opts.Label
	if label == "" {
		label = fmt.Sprintf("toris-%s-%s", backupID[:8], time.Now().UTC().Format("20060102T150405Z"))
	}
	if _, err := pgtools.PgBaseBackup(ctx, p.log, p.tools, opts.Node, pgtools.BaseBackupOptions{
		DestDir:         stagingDir,
		Format:          "tar",
		Compress:        1,
		WALMethod:       "stream",
		Checkpoint:      "fast",
		Label:           label,
		Timeout:         timeout,
		ReplicationUser: opts.ReplicationUser,
		Password:        opts.ReplicationPassword,
	}); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = err.Error()
		return backup, err
	}

	// ── Step 4: Manifest ───────────────────────────────────────────────────
	artifacts, totalBytes, err := manifest.BuildArtifacts(backupID, stagingDir)
	if err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = fmt.Sprintf("manifest build failed: %v", err)
		return backup, err
	}
	m := &model.BackupManifest{
		BackupID:        backupID,
		ClusterID:       opts.ClusterID,
		NodeID:          opts.Node.ID,
		Generation:      generation,
		CreatedAt:       util.NowUTC(),
		PostgresVersion: opts.PostgresVersion,
		Artifacts:       artifacts,
		TotalSizeBytes:  totalBytes,
	}
	if err := manifest.Write(stagingDir, m); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = fmt.Sprintf("manifest write failed: %v", err)
		return backup, err
	}
	backup.SizeBytes = totalBytes

	// ── Step 5: pg_verifybackup ────────────────────────────────────────────
	verifyTimeout := opts.VerifyTimeout
	if verifyTimeout == 0 {
		verifyTimeout = 30 * time.Minute
	}
	if _, err := pgtools.PgVerifyBackup(ctx, p.log, p.tools, stagingDir, verifyTimeout); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = fmt.Sprintf("verification failed: %v", err)
		return backup, err
	}
	now := util.NowUTC()
	backup.Status = model.BackupStatusVerified
	backup.FinishedAt = util.Ptr(now)
	backup.VerifiedAt = util.Ptr(now)

	// ── Step 6: Upload to storage backend ─────────────────────────────────
	if err := p.uploadToStorage(ctx, backupID, stagingDir); err != nil {
		if opts.OffSiteRequired {
			backup.Status = model.BackupStatusFailed
			backup.FailureMsg = fmt.Sprintf("storage upload failed: %v", err)
			return backup, err
		}
		p.log.Warn("storage upload failed (offsite_required=false)",
			"backup_id", backupID,
			"error", err.Error(),
		)
	} else {
		uploadedAt := util.NowUTC()
		backup.Status = model.BackupStatusUploaded
		backup.UploadedAt = util.Ptr(uploadedAt)
	}

	p.log.Info("backup pipeline complete",
		"backup_id", backupID,
		"size_bytes", totalBytes,
		"status", string(backup.Status),
		"duration", time.Since(backup.StartedAt),
	)
	return backup, nil
}

func (p *Pipeline) preflight(ctx context.Context, opts CreateOptions, generation int64) error {
	if p.lm != nil {
		if !p.lm.HoldingLease() {
			return torerrors.New(torerrors.CodeLeaseNotHeld,
				"cannot create backup: this instance does not hold the cluster lease")
		}
		if err := p.lm.AssertFencingToken(generation); err != nil {
			return err
		}
	}
	readyRes, err := pgtools.PgIsReady(ctx, p.tools, opts.Node, 10*time.Second)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBNotReady, err,
			"pg_isready failed for node %s", opts.Node.ID)
	}
	if !readyRes.Ready {
		return torerrors.Newf(torerrors.CodeDBNotReady,
			"node %s (%s) is not ready: %s", opts.Node.ID, opts.Node.Addr(), readyRes.Message)
	}
	if err := ensureDirWritable(opts.StagingDir); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"staging dir %s is not writable", opts.StagingDir)
	}
	if p.tools == nil {
		return torerrors.New(torerrors.CodeToolNotFound, "pg_* tools not initialized")
	}
	p.log.Info("backup preflight passed", "node", opts.Node.Addr())
	return nil
}

func (p *Pipeline) uploadToStorage(ctx context.Context, backupID, stagingDir string) error {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return fmt.Errorf("reading staging directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		localPath := filepath.Join(stagingDir, entry.Name())
		key := fs.KeyForBackup(backupID, entry.Name())
		if err := fs.WriteFile(ctx, p.store, key, localPath); err != nil {
			return fmt.Errorf("uploading %s: %w", entry.Name(), err)
		}
		p.log.Info("artifact uploaded to storage", "key", key)
	}
	return nil
}

// Prune applies the retention policy and removes pruned artifacts from storage.
func (p *Pipeline) Prune(ctx context.Context, backups []*model.Backup) ([]string, error) {
	if p.enforcer == nil {
		return nil, nil
	}
	return p.enforcer.Apply(ctx, backups)
}

func ensureDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".toris_write_probe")
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("directory %s is not writable: %w", dir, err)
	}
	f.Close()
	os.Remove(probe)
	return nil
}
