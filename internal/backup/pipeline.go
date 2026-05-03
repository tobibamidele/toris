// Package backup implements the full backup pipeline:
// preflight → snapshot → manifest → verification → retention → offsite.
//
// A backup is not marked successful until pg_verifybackup passes.
// A backup is not uploaded/complete until the offsite copy is confirmed (if enabled).
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
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

// CreateOptions configures a single backup run.
type CreateOptions struct {
	// Node is the source PostgreSQL node to back up.
	Node *model.Node
	// BackupBaseDir is the root directory for backup storage.
	BackupBaseDir string
	// Label is embedded in the PostgreSQL backup label.
	Label string
	// DryRun: perform preflight checks only, no actual backup.
	DryRun bool
	// BackupTimeout overrides the default backup timeout.
	BackupTimeout time.Duration
	// VerifyTimeout overrides the default verify timeout.
	VerifyTimeout time.Duration
	// ReplicationUser for pg_basebackup connection.
	ReplicationUser string
	// ReplicationPassword — never logged.
	ReplicationPassword string
	// PostgresVersion string (e.g. "PostgreSQL 15.3") for the manifest.
	PostgresVersion string
}

// Pipeline orchestrates the full backup lifecycle.
type Pipeline struct {
	log   *logging.Logger
	lm    *leader.Manager
	tools *pgtools.Tools
}

// NewPipeline creates a backup Pipeline.
func NewPipeline(log *logging.Logger, lm *leader.Manager, tools *pgtools.Tools) *Pipeline {
	return &Pipeline{log: log, lm: lm, tools: tools}
}

// Create runs the full backup pipeline and returns the completed Backup record.
// The caller is responsible for persisting the returned Backup to their store.
func (p *Pipeline) Create(ctx context.Context, opts CreateOptions) (*model.Backup, error) {
	backupID := util.NewID()
	generation := p.lm.CurrentGeneration()

	backup := &model.Backup{
		ID:         backupID,
		ClusterID:  "", // set by caller who knows cluster context
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

	// ── Step 1: Preflight ──────────────────────────────────────────────────
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

	// ── Step 2: Create destination directory ──────────────────────────────
	destDir := filepath.Join(opts.BackupBaseDir, backupID)
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = err.Error()
		return backup, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating backup destination %s", destDir)
	}
	backup.StoragePath = destDir
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

	_, err := pgtools.PgBaseBackup(ctx, p.log, p.tools, opts.Node, pgtools.BaseBackupOptions{
		DestDir:         destDir,
		Format:          "tar",
		Compress:        1, // light compression, fast
		WALMethod:       "stream",
		Checkpoint:      "fast",
		Label:           label,
		Timeout:         timeout,
		ReplicationUser: opts.ReplicationUser,
		Password:        opts.ReplicationPassword,
	})
	if err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = err.Error()
		// Keep the failed backup dir for forensics.
		return backup, err
	}

	// ── Step 4: Build and write manifest ──────────────────────────────────
	artifacts, totalBytes, err := manifest.BuildArtifacts(backupID, destDir)
	if err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = fmt.Sprintf("manifest build failed: %v", err)
		return backup, err
	}

	m := &model.BackupManifest{
		BackupID:        backupID,
		NodeID:          opts.Node.ID,
		Generation:      generation,
		CreatedAt:       util.NowUTC(),
		PostgresVersion: opts.PostgresVersion,
		Artifacts:       artifacts,
		TotalSizeBytes:  totalBytes,
	}
	if err := manifest.Write(destDir, m); err != nil {
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
	if _, err := pgtools.PgVerifyBackup(ctx, p.log, p.tools, destDir, verifyTimeout); err != nil {
		backup.Status = model.BackupStatusFailed
		backup.FailureMsg = fmt.Sprintf("verification failed: %v", err)
		// Do NOT delete the backup. Keep it for debugging.
		return backup, err
	}

	now := util.NowUTC()
	backup.Status = model.BackupStatusVerified
	backup.FinishedAt = util.Ptr(now)
	backup.VerifiedAt = util.Ptr(now)

	p.log.Info("backup pipeline complete",
		"backup_id", backupID,
		"size_bytes", totalBytes,
		"duration", time.Since(backup.StartedAt),
	)
	return backup, nil
}

// ─── Preflight ────────────────────────────────────────────────────────────────

func (p *Pipeline) preflight(ctx context.Context, opts CreateOptions, generation int64) error {
	// 1. Lease must be held.
	if !p.lm.HoldingLease() {
		return torerrors.New(torerrors.CodeLeaseNotHeld,
			"cannot create backup: this instance does not hold the cluster lease")
	}

	// 2. Fencing token must be current.
	if err := p.lm.AssertFencingToken(generation); err != nil {
		return err
	}

	// 3. Node must be reachable (pg_isready check via tools).
	readyRes, err := pgtools.PgIsReady(ctx, p.tools, opts.Node, 10*time.Second)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeDBNotReady, err,
			"pg_isready failed for node %s", opts.Node.ID)
	}
	if !readyRes.Ready {
		return torerrors.Newf(torerrors.CodeDBNotReady,
			"node %s (%s) is not ready for backup: %s",
			opts.Node.ID, opts.Node.Addr(), readyRes.Message)
	}

	// 4. Storage directory must be writable.
	if err := checkDirWritable(opts.BackupBaseDir); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"backup base dir %s is not writable", opts.BackupBaseDir)
	}

	// 5. Required tools must be available (already checked at startup, but verify).
	if p.tools == nil {
		return torerrors.New(torerrors.CodeToolNotFound, "pg_* tools not initialized")
	}

	p.log.Info("backup preflight passed",
		"node", opts.Node.Addr(),
		"dest", opts.BackupBaseDir,
	)
	return nil
}

// checkDirWritable creates the dir if needed and checks write access.
func checkDirWritable(dir string) error {
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
