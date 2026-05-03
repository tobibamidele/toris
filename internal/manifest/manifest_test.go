package manifest_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/manifest"
	"github.com/tobibamidele/toris/pkg/model"
)

func TestWrite_CreatesManifestFile(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest()
	if err := manifest.Write(dir, m); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	_, err := os.Stat(filepath.Join(dir, "toris_manifest.json"))
	if err != nil {
		t.Errorf("manifest file not created: %v", err)
	}
}

func TestWrite_EmbedsSelfHash(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest()
	if err := manifest.Write(dir, m); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if m.ManifestSHA256 == "" {
		t.Error("ManifestSHA256 should be set after Write")
	}
	if len(m.ManifestSHA256) != 64 {
		t.Errorf("ManifestSHA256 should be 64 hex chars (SHA-256), got %d", len(m.ManifestSHA256))
	}
}

func TestRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := sampleManifest()
	original.BackupID = "roundtrip-test-id"
	original.PostgresVersion = "PostgreSQL 15.3"

	if err := manifest.Write(dir, original); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	read, err := manifest.Read(dir)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if read.BackupID != original.BackupID {
		t.Errorf("BackupID mismatch: got %q, want %q", read.BackupID, original.BackupID)
	}
	if read.PostgresVersion != original.PostgresVersion {
		t.Errorf("PostgresVersion mismatch: got %q, want %q", read.PostgresVersion, original.PostgresVersion)
	}
	if read.ManifestSHA256 == "" {
		t.Error("ManifestSHA256 should be preserved after round-trip")
	}
}

func TestRead_DetectsTampering(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest()
	if err := manifest.Write(dir, m); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Tamper with the manifest file by appending a byte.
	path := filepath.Join(dir, "toris_manifest.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Inject a space near the end (won't break JSON but will change the hash).
	// We need to actually corrupt the content.
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Change a byte in the backup_id value.
	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	for i, b := range corrupted {
		if b == '"' {
			corrupted[i+1] ^= 0x01 // flip a bit in the next byte
			break
		}
	}
	if err := os.WriteFile(path, corrupted, 0o640); err != nil {
		t.Fatal(err)
	}

	_, err = manifest.Read(dir)
	if err == nil {
		t.Error("Read should return an error when manifest is tampered")
	}
}

func TestRead_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := manifest.Read(dir)
	if err == nil {
		t.Error("Read should return an error when manifest file does not exist")
	}
}

func TestHashFile_KnownContent(t *testing.T) {
	// SHA-256("hello\n") = 5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03
	f, err := os.CreateTemp(t.TempDir(), "hash-test")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("hello\n")
	f.Close()

	hash, size, err := manifest.HashFile(f.Name())
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}
	expected := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if hash != expected {
		t.Errorf("hash mismatch:\n  got  %s\n  want %s", hash, expected)
	}
	if size != 6 {
		t.Errorf("size mismatch: got %d, want 6", size)
	}
}

func TestHashFile_NonexistentFile(t *testing.T) {
	_, _, err := manifest.HashFile("/nonexistent/path/file.tar")
	if err == nil {
		t.Error("HashFile should return error for nonexistent file")
	}
}

func TestBuildArtifacts_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	arts, total, err := manifest.BuildArtifacts("backup-001", dir)
	if err != nil {
		t.Fatalf("BuildArtifacts failed: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("expected 0 artifacts, got %d", len(arts))
	}
	if total != 0 {
		t.Errorf("expected 0 total bytes, got %d", total)
	}
}

func TestBuildArtifacts_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	// Create 3 fake backup files.
	files := map[string]string{
		"base.tar.gz":   "fake tar content",
		"pg_wal.tar.gz": "fake wal content",
	}
	var expectedSize int64
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
		expectedSize += int64(len(content))
	}

	arts, total, err := manifest.BuildArtifacts("backup-multifile", dir)
	if err != nil {
		t.Fatalf("BuildArtifacts failed: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(arts))
	}
	if total != expectedSize {
		t.Errorf("total size mismatch: got %d, want %d", total, expectedSize)
	}

	// Verify each artifact has a SHA256.
	for _, a := range arts {
		if a.SHA256 == "" {
			t.Errorf("artifact %s is missing SHA256", a.Filename)
		}
		if a.BackupID != "backup-multifile" {
			t.Errorf("artifact %s has wrong BackupID: %s", a.Filename, a.BackupID)
		}
	}
}

func TestBuildArtifacts_ExcludesManifest(t *testing.T) {
	dir := t.TempDir()

	// Write a manifest file (should be excluded) and a real artifact.
	os.WriteFile(filepath.Join(dir, "toris_manifest.json"), []byte("{}"), 0o640)
	os.WriteFile(filepath.Join(dir, "base.tar"), []byte("data"), 0o640)

	arts, _, err := manifest.BuildArtifacts("bk", dir)
	if err != nil {
		t.Fatalf("BuildArtifacts failed: %v", err)
	}
	for _, a := range arts {
		if a.Filename == "toris_manifest.json" {
			t.Error("manifest file should not appear in artifacts list")
		}
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 artifact (not the manifest), got %d", len(arts))
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func sampleManifest() *model.BackupManifest {
	return &model.BackupManifest{
		BackupID:        "test-backup-001",
		ClusterID:       "pg-test",
		NodeID:          "node-01",
		Generation:      1,
		CreatedAt:       time.Now().UTC(),
		PostgresVersion: "PostgreSQL 15.3",
		WALStart:        "0/1000000",
		WALStop:         "0/2000000",
		Artifacts: []model.BackupArtifact{
			{
				ID:        "test-backup-001_base.tar",
				BackupID:  "test-backup-001",
				Filename:  "base.tar",
				SizeBytes: 1024,
				SHA256:    "deadbeef",
				CreatedAt: time.Now().UTC(),
			},
		},
		TotalSizeBytes: 1024,
	}
}
