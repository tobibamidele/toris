//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/backup"
	pgtools "github.com/tobibamidele/toris/internal/db/postgres"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/restore"
	fsstorage "github.com/tobibamidele/toris/internal/storage/fs"
	"github.com/tobibamidele/toris/pkg/model"
)

// TestBackupPipeline_CreateAndVerify runs a full pg_basebackup against the
// test primary, builds a manifest, runs pg_verifybackup, and uploads to the
// local filesystem storage backend. Requires pg_* tools in PATH.
func TestBackupPipeline_CreateAndVerify(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := EnsureControlSchema(ctx, tc.Control); err != nil {
		t.Fatalf("EnsureControlSchema: %v", err)
	}

	// Check pg_* tools.
	tools, err := pgtools.CheckTools(ctx)
	if err != nil {
		t.Skipf("pg_* tools not in PATH: %v", err)
	}

	stagingDir := t.TempDir()
	storageDir := t.TempDir()

	store, err := fsstorage.New(storageDir)
	if err != nil {
		t.Fatalf("creating storage backend: %v", err)
	}

	bs := backup.NewStore(tc.Control)
	if err := bs.EnsureSchema(ctx); err != nil {
		t.Fatalf("backup store EnsureSchema: %v", err)
	}

	pl := backup.NewPipeline(logging.Nop(), nil, tools, store, nil, bs)

	node := &model.Node{
		ID:   "primary",
		Host: "localhost",
		Port: 5441,
	}

	b, err := pl.Create(ctx, backup.CreateOptions{
		Node:            node,
		ClusterID:       "integration-test-cluster",
		StagingDir:      stagingDir,
		DryRun:          false,
		OffSiteRequired: false,
		BackupTimeout:   4 * time.Minute,
		VerifyTimeout:   1 * time.Minute,
		PostgresVersion: "15",
	})
	if err != nil {
		t.Fatalf("backup.Create: %v (status=%s, msg=%s)", err, b.Status, b.FailureMsg)
	}

	if b.Status != model.BackupStatusVerified && b.Status != model.BackupStatusUploaded {
		t.Errorf("expected verified or uploaded status, got %s", b.Status)
	}
	if b.SizeBytes == 0 {
		t.Error("backup size should be non-zero")
	}
	if b.VerifiedAt == nil {
		t.Error("VerifiedAt should be set after successful backup")
	}

	t.Logf("backup %s: status=%s size=%d bytes", b.ID, b.Status, b.SizeBytes)

	// Verify the backup record was persisted to the control DB.
	persisted, err := bs.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("backup not persisted to control DB: %v", err)
	}
	if persisted.Status != b.Status {
		t.Errorf("persisted status %s != returned status %s", persisted.Status, b.Status)
	}
}

// TestRestore_RehearsalFromRealBackup performs a full backup then restores
// it in rehearsal mode, verifying the entire pipeline end-to-end.
func TestRestore_RehearsalFromRealBackup(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := EnsureControlSchema(ctx, tc.Control); err != nil {
		t.Fatalf("EnsureControlSchema: %v", err)
	}

	tools, err := pgtools.CheckTools(ctx)
	if err != nil {
		t.Skipf("pg_* tools not in PATH: %v", err)
	}

	stagingDir := t.TempDir()
	storageDir := t.TempDir()

	store, err := fsstorage.New(storageDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	// Step 1: Create a real backup.
	bs := backup.NewStore(tc.Control)
	_ = bs.EnsureSchema(ctx)
	pl := backup.NewPipeline(logging.Nop(), nil, tools, store, nil, bs)

	node := &model.Node{ID: "primary", Host: "localhost", Port: 5441}
	b, err := pl.Create(ctx, backup.CreateOptions{
		Node:          node,
		ClusterID:     "restore-integration-cluster",
		StagingDir:    stagingDir,
		BackupTimeout: 4 * time.Minute,
		VerifyTimeout: 1 * time.Minute,
	})
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	t.Logf("created backup %s (%d bytes)", b.ID, b.SizeBytes)

	// Step 2: Restore in rehearsal mode.
	engine := restore.New(logging.Nop(), store)
	job, err := engine.Run(ctx, restore.Options{
		BackupID:  b.ID,
		Mode:      model.RestoreModeRehearsal,
		TempDir:   t.TempDir(),
		Timeout:   5 * time.Minute,
		ClusterID: "restore-integration-cluster",
	})
	if err != nil {
		t.Fatalf("rehearsal restore failed: %v (job=%+v)", err, job)
	}

	if job.Status != model.RestoreStatusCompleted {
		t.Errorf("expected completed, got %s", job.Status)
	}

	t.Logf("rehearsal restore %s completed", job.ID)
}

// TestRestore_ReseedWritesStandbySignal creates a backup and reseeds into
// a temp data directory, verifying standby.signal is written.
func TestRestore_ReseedWritesStandbySignal(t *testing.T) {
	tc := NewTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := EnsureControlSchema(ctx, tc.Control); err != nil {
		t.Fatalf("EnsureControlSchema: %v", err)
	}

	tools, err := pgtools.CheckTools(ctx)
	if err != nil {
		t.Skipf("pg_* tools not in PATH: %v", err)
	}

	storageDir := t.TempDir()
	store, _ := fsstorage.New(storageDir)

	bs := backup.NewStore(tc.Control)
	_ = bs.EnsureSchema(ctx)
	pl := backup.NewPipeline(logging.Nop(), nil, tools, store, nil, bs)

	node := &model.Node{ID: "primary", Host: "localhost", Port: 5441}
	b, err := pl.Create(ctx, backup.CreateOptions{
		Node:          node,
		ClusterID:     "reseed-integration-cluster",
		StagingDir:    t.TempDir(),
		BackupTimeout: 4 * time.Minute,
		VerifyTimeout: 1 * time.Minute,
	})
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	targetDir := t.TempDir()
	r := restore.NewReseeder(logging.Nop(), store)
	job, err := r.Reseed(ctx, restore.ReseedOptions{
		BackupID:  b.ID,
		TargetDir: targetDir,
		TempDir:   t.TempDir(),
		ClusterID: "reseed-integration-cluster",
		Timeout:   5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("reseed failed: %v", err)
	}

	if job.Status != model.RestoreStatusCompleted {
		t.Errorf("expected completed, got %s", job.Status)
	}

	// Verify standby.signal was written.
	signalPath := filepath.Join(targetDir, "data", "standby.signal")
	if _, err := os.Stat(signalPath); os.IsNotExist(err) {
		t.Errorf("standby.signal not found at %s", signalPath)
	} else {
		t.Logf("standby.signal written at %s", signalPath)
	}
}
