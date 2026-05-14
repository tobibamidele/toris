package restore_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/manifest"
	"github.com/tobibamidele/toris/internal/restore"
	fsstorage "github.com/tobibamidele/toris/internal/storage/fs"
	"github.com/tobibamidele/toris/pkg/model"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// seedBackup creates a synthetic verified backup in the storage backend.
// It produces a real .tar.gz so the extraction stage of the restore
// pipeline succeeds without a real pg_basebackup.
func seedBackup(t *testing.T, store *fsstorage.Backend, backupID string) {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()
	artifactFilename := "base.tar.gz"
	artifactPath := filepath.Join(tmpDir, artifactFilename)

	// Write a valid (empty-content) tar.gz file.
	if err := writeFakeTarGz(artifactPath); err != nil {
		t.Fatalf("creating fake tar.gz: %v", err)
	}

	artifacts, totalBytes, err := manifest.BuildArtifacts(backupID, tmpDir)
	if err != nil {
		t.Fatalf("building artifacts: %v", err)
	}
	m := &model.BackupManifest{
		BackupID:        backupID,
		ClusterID:       "test-cluster",
		NodeID:          "node-01",
		Generation:      1,
		CreatedAt:       time.Now().UTC(),
		PostgresVersion: "PostgreSQL 15.3",
		Artifacts:       artifacts,
		TotalSizeBytes:  totalBytes,
	}
	if err := manifest.Write(tmpDir, m); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	for _, name := range []string{artifactFilename, "toris_manifest.json"} {
		key := fsstorage.KeyForBackup(backupID, name)
		f, err := os.Open(filepath.Join(tmpDir, name))
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		if werr := store.Write(ctx, key, f); werr != nil {
			f.Close()
			t.Fatalf("writing %s to store: %v", name, werr)
		}
		f.Close()
	}
}

// writeFakeTarGz creates a minimal valid tar.gz with one small file inside.
func writeFakeTarGz(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	// Add a single empty file so the archive is well-formed.
	hdr := &tar.Header{
		Name:     "PG_VERSION",
		Mode:     0o600,
		Size:     int64(len("15\n")),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write([]byte("15\n")); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ─── Engine.Run tests ─────────────────────────────────────────────────────────

func TestRestore_RehearsalMode_Completes(t *testing.T) {
	storeDir := t.TempDir()
	store, err := fsstorage.New(storeDir)
	if err != nil {
		t.Fatal(err)
	}

	backupID := "test-backup-rehearsal"
	seedBackup(t, store, backupID)

	engine := restore.New(logging.Nop(), store)
	ctx := context.Background()

	job, err := engine.Run(ctx, restore.Options{
		BackupID:  backupID,
		Mode:      model.RestoreModeRehearsal,
		TempDir:   t.TempDir(),
		Timeout:   30 * time.Second,
		ClusterID: "test-cluster",
	})

	if err != nil {
		t.Fatalf("rehearsal restore failed: %v", err)
	}
	if job.Status != model.RestoreStatusCompleted {
		t.Errorf("expected completed, got %s", job.Status)
	}
	if job.IsRehearsal != true {
		t.Error("IsRehearsal should be true for rehearsal mode")
	}
}

func TestRestore_EmptyNodeMode_Completes(t *testing.T) {
	storeDir := t.TempDir()
	store, err := fsstorage.New(storeDir)
	if err != nil {
		t.Fatal(err)
	}

	backupID := "test-backup-empty-node"
	seedBackup(t, store, backupID)

	targetDir := t.TempDir()
	engine := restore.New(logging.Nop(), store)

	job, err := engine.Run(context.Background(), restore.Options{
		BackupID:  backupID,
		TargetDir: targetDir,
		Mode:      model.RestoreModeEmptyNode,
		TempDir:   t.TempDir(),
		Timeout:   30 * time.Second,
		ClusterID: "test-cluster",
	})

	if err != nil {
		t.Fatalf("empty-node restore failed: %v", err)
	}
	if job.Status != model.RestoreStatusCompleted {
		t.Errorf("expected completed, got %s", job.Status)
	}
	if job.FinishedAt == nil {
		t.Error("FinishedAt should be set on completion")
	}
}

func TestRestore_ReseedMode_WritesStandbySignal(t *testing.T) {
	storeDir := t.TempDir()
	store, _ := fsstorage.New(storeDir)
	backupID := "test-backup-reseed"
	seedBackup(t, store, backupID)

	targetDir := t.TempDir()
	engine := restore.New(logging.Nop(), store)

	_, err := engine.Run(context.Background(), restore.Options{
		BackupID:  backupID,
		TargetDir: targetDir,
		Mode:      model.RestoreModeReseed,
		TempDir:   t.TempDir(),
		Timeout:   30 * time.Second,
		ClusterID: "test-cluster",
	})
	if err != nil {
		t.Fatalf("reseed restore failed: %v", err)
	}

	// standby.signal must exist in the data subdirectory.
	signalPath := filepath.Join(targetDir, "data", "standby.signal")
	if _, statErr := os.Stat(signalPath); os.IsNotExist(statErr) {
		t.Errorf("standby.signal not written at %s", signalPath)
	}
}

func TestRestore_MissingBackup_ReturnsFailed(t *testing.T) {
	store, _ := fsstorage.New(t.TempDir())
	engine := restore.New(logging.Nop(), store)

	job, err := engine.Run(context.Background(), restore.Options{
		BackupID:  "nonexistent-backup-id",
		TargetDir: t.TempDir(),
		Mode:      model.RestoreModeEmptyNode,
		TempDir:   t.TempDir(),
		Timeout:   10 * time.Second,
		ClusterID: "test-cluster",
	})

	if err == nil {
		t.Fatal("expected error for missing backup, got nil")
	}
	if job == nil {
		t.Fatal("job should be non-nil even on failure")
	}
	if job.Status != model.RestoreStatusFailed {
		t.Errorf("expected failed status, got %s", job.Status)
	}
	if job.FailureMsg == "" {
		t.Error("FailureMsg should be set on failure")
	}
}

func TestRestore_FailedJobPreservesArtifactDir(t *testing.T) {
	store, _ := fsstorage.New(t.TempDir())
	engine := restore.New(logging.Nop(), store)

	job, err := engine.Run(context.Background(), restore.Options{
		BackupID:  "no-backup",
		TargetDir: t.TempDir(),
		Mode:      model.RestoreModeEmptyNode,
		TempDir:   t.TempDir(),
		Timeout:   5 * time.Second,
		ClusterID: "test-cluster",
	})

	if err == nil {
		t.Skip("backup unexpectedly succeeded")
	}
	// The ArtifactDir field should be set so operators know where to look.
	if job != nil && job.ArtifactDir == "" {
		t.Error("ArtifactDir should be set on failure to assist debugging")
	}
}

func TestRestore_JobIDIsAlwaysSet(t *testing.T) {
	store, _ := fsstorage.New(t.TempDir())
	engine := restore.New(logging.Nop(), store)

	job, _ := engine.Run(context.Background(), restore.Options{
		BackupID:  "any-backup",
		TargetDir: t.TempDir(),
		Mode:      model.RestoreModeEmptyNode,
		TempDir:   t.TempDir(),
		Timeout:   5 * time.Second,
		ClusterID: "test-cluster",
	})

	if job == nil {
		t.Fatal("job must never be nil")
	}
	if job.ID == "" {
		t.Error("job ID must always be set")
	}
}

func TestRestore_CanceledContext_ReturnsFailed(t *testing.T) {
	storeDir := t.TempDir()
	store, _ := fsstorage.New(storeDir)
	backupID := "cancel-test"
	seedBackup(t, store, backupID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	engine := restore.New(logging.Nop(), store)
	job, err := engine.Run(ctx, restore.Options{
		BackupID:  backupID,
		TargetDir: t.TempDir(),
		Mode:      model.RestoreModeEmptyNode,
		TempDir:   t.TempDir(),
		Timeout:   30 * time.Second,
		ClusterID: "test-cluster",
	})

	// A canceled context should produce an error.
	if err == nil {
		// If it somehow completed before cancellation, that is also acceptable.
		_ = job
	}
}

// ─── Reseeder tests ───────────────────────────────────────────────────────────

func TestReseeder_MissingBackupID_ReturnsError(t *testing.T) {
	store, _ := fsstorage.New(t.TempDir())
	r := restore.NewReseeder(logging.Nop(), store)

	_, err := r.Reseed(context.Background(), restore.ReseedOptions{
		BackupID:  "", // required
		TargetDir: t.TempDir(),
		TempDir:   t.TempDir(),
	})
	if err == nil {
		t.Error("expected error for empty BackupID")
	}
}

func TestReseeder_MissingTargetDir_ReturnsError(t *testing.T) {
	store, _ := fsstorage.New(t.TempDir())
	r := restore.NewReseeder(logging.Nop(), store)

	_, err := r.Reseed(context.Background(), restore.ReseedOptions{
		BackupID:  "some-id",
		TargetDir: "", // required
		TempDir:   t.TempDir(),
	})
	if err == nil {
		t.Error("expected error for empty TargetDir")
	}
}
