// Package manifest handles writing, reading, and verifying backup manifests.
// The manifest is the ground truth for backup integrity. A backup is only
// considered valid if its manifest exists and its self-hash verifies cleanly.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/pkg/model"
)

const manifestFilename = "toris_manifest.json"

// Write serializes the manifest to the given directory.
// It computes and embeds the self-hash (SHA-256 of the JSON without the hash field)
// so any tampering can be detected on read.
func Write(dir string, m *model.BackupManifest) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating manifest directory %s", dir)
	}

	// Compute self-hash: marshal with ManifestSHA256 = "" first.
	m.ManifestSHA256 = ""
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeInternal, err, "marshaling manifest")
	}

	h := sha256.Sum256(raw)
	m.ManifestSHA256 = hex.EncodeToString(h[:])

	// Re-marshal with the hash embedded.
	rawFinal, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeInternal, err, "marshaling manifest with hash")
	}

	path := filepath.Join(dir, manifestFilename)
	if err := os.WriteFile(path, rawFinal, 0o640); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"writing manifest to %s", path)
	}
	return nil
}

// Read deserializes the manifest from a backup directory and verifies
// its self-hash. Returns an error if the file is missing or tampered.
func Read(dir string) (*model.BackupManifest, error) {
	path := filepath.Join(dir, manifestFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, torerrors.Newf(torerrors.CodeBackupNotFound,
				"manifest not found at %s", path)
		}
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"reading manifest from %s", path)
	}

	var m model.BackupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeBackupVerifyFail, err,
			"parsing manifest at %s", path)
	}

	// Verify self-hash.
	storedHash := m.ManifestSHA256
	m.ManifestSHA256 = ""
	recomputed, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeInternal, err, "re-marshaling manifest for hash verification")
	}
	h := sha256.Sum256(recomputed)
	if hex.EncodeToString(h[:]) != storedHash {
		return nil, torerrors.Newf(torerrors.CodeBackupVerifyFail,
			"manifest at %s has an invalid self-hash: file may be corrupted or tampered", path)
	}
	// Restore the hash field.
	m.ManifestSHA256 = storedHash

	return &m, nil
}

// HashFile computes the SHA-256 of a file and returns the hex digest.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"opening file for hashing: %s", path)
	}
	defer f.Close()

	h := sha256.New()
	written, err := io.Copy(h, f)
	if err != nil {
		return "", 0, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"hashing file: %s", path)
	}
	return hex.EncodeToString(h.Sum(nil)), written, nil
}

// BuildArtifacts walks a backup directory and creates BackupArtifact records
// for every regular file. It excludes the manifest itself.
func BuildArtifacts(backupID, dir string) ([]model.BackupArtifact, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"reading backup directory %s", dir)
	}

	var artifacts []model.BackupArtifact
	var totalBytes int64

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == manifestFilename {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		hash, size, err := HashFile(fullPath)
		if err != nil {
			return nil, 0, fmt.Errorf("hashing artifact %s: %w", entry.Name(), err)
		}
		artifacts = append(artifacts, model.BackupArtifact{
			ID:        backupID + "_" + entry.Name(),
			BackupID:  backupID,
			Filename:  entry.Name(),
			SizeBytes: size,
			SHA256:    hash,
			CreatedAt: time.Now().UTC(),
		})
		totalBytes += size
	}
	return artifacts, totalBytes, nil
}
