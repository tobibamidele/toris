// Package fs implements storage.Backend on the local filesystem.
//
// Atomicity guarantee:
//
//	Write creates a hidden temp file in the same directory as the target,
//	copies all data into it, syncs to disk, then renames it into place.
//	rename(2) is atomic on POSIX filesystems within the same mount point,
//	so a reader will see either the old file or the complete new file —
//	never a partial write.
//
// Key format:
//
//	Keys use forward slashes as separators and are mapped directly to
//	filesystem paths under BaseDir. A key "backups/abc/base.tar.gz"
//	maps to "<BaseDir>/backups/abc/base.tar.gz".
package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/storage"
)

// Backend is a storage.Backend backed by the local filesystem.
type Backend struct {
	baseDir string
}

// New creates a filesystem Backend rooted at baseDir.
// The directory is created with 0750 permissions if it does not exist.
func New(baseDir string) (*Backend, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating storage base directory %s", baseDir)
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"resolving absolute path for %s", baseDir)
	}
	return &Backend{baseDir: abs}, nil
}

// Name implements storage.Backend.
func (b *Backend) Name() string {
	return "fs:" + b.baseDir
}

// Write implements storage.Backend.
// Writes are atomic: data is written to a temp file then renamed into place.
func (b *Backend) Write(ctx context.Context, key string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return torerrors.Wrap(torerrors.CodeCanceled, "write canceled before start", err)
	}

	target := b.resolve(key)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating parent directory for key %s", key)
	}

	// Write to a temp file in the same directory so rename is atomic.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".toris_write_*")
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating temp file for key %s", key)
	}
	tmpName := tmp.Name()

	// Clean up the temp file on any failure path.
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	// Copy with context cancellation awareness using a small loop.
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return torerrors.Wrap(torerrors.CodeCanceled, "write canceled mid-stream", err)
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			if _, writeErr := tmp.Write(buf[:n]); writeErr != nil {
				return torerrors.Wrapf(torerrors.CodeStorageFailed, writeErr,
					"writing to temp file for key %s", key)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return torerrors.Wrapf(torerrors.CodeStorageFailed, readErr,
				"reading source data for key %s", key)
		}
	}

	// Sync before rename so data survives a crash after the rename.
	if err := tmp.Sync(); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"syncing temp file for key %s", key)
	}
	if err := tmp.Close(); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"closing temp file for key %s", key)
	}

	// Set permissions before rename.
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"setting permissions on temp file for key %s", key)
	}

	// Atomic rename.
	if err := os.Rename(tmpName, target); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"renaming temp file to target for key %s", key)
	}
	committed = true
	return nil
}

// Read implements storage.Backend.
func (b *Backend) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, torerrors.Wrap(torerrors.CodeCanceled, "read canceled before start", err)
	}
	path := b.resolve(key)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, torerrors.Newf(torerrors.CodeNotFound,
				"object not found: %s", key)
		}
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"opening object %s", key)
	}
	return f, nil
}

// Delete implements storage.Backend.
// Not an error if the key does not exist.
func (b *Backend) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return torerrors.Wrap(torerrors.CodeCanceled, "delete canceled", err)
	}
	path := b.resolve(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"deleting object %s", key)
	}
	// Also remove the parent directory if it is now empty (best-effort).
	_ = removeEmptyDirs(filepath.Dir(path), b.baseDir)
	return nil
}

// List implements storage.Backend.
// Returns all keys under prefix in lexicographic order.
func (b *Backend) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, torerrors.Wrap(torerrors.CodeCanceled, "list canceled", err)
	}

	searchDir := b.baseDir
	if prefix != "" {
		// If the prefix contains a directory component, start the walk there.
		prefixDir := filepath.Dir(b.resolve(prefix))
		if info, err := os.Stat(prefixDir); err == nil && info.IsDir() {
			searchDir = prefixDir
		}
	}

	var keys []string
	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		// Convert absolute path back to key.
		rel, relErr := filepath.Rel(b.baseDir, path)
		if relErr != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"listing objects with prefix %q", prefix)
	}
	sort.Strings(keys)
	return keys, nil
}

// Stat implements storage.Backend.
func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, torerrors.Wrap(torerrors.CodeCanceled, "stat canceled", err)
	}
	path := b.resolve(key)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.ObjectInfo{}, torerrors.Newf(torerrors.CodeNotFound,
				"object not found: %s", key)
		}
		return storage.ObjectInfo{}, torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"stating object %s", key)
	}
	return storage.ObjectInfo{
		Key:          key,
		SizeBytes:    info.Size(),
		LastModified: info.ModTime().UTC(),
	}, nil
}

// BaseDir returns the root directory of this backend.
func (b *Backend) BaseDir() string { return b.baseDir }

// resolve converts a logical key to an absolute filesystem path.
// Keys are always relative to baseDir and may not escape it via "..".
func (b *Backend) resolve(key string) string {
	// Sanitise: strip leading slashes, reject traversal attempts.
	clean := filepath.Clean(filepath.Join(b.baseDir, filepath.FromSlash(key)))
	if !strings.HasPrefix(clean, b.baseDir) {
		// Key attempts to escape the base directory — return an invalid path
		// that will naturally fail on Open/Create so callers see a clear error.
		return filepath.Join(b.baseDir, "__invalid__", key)
	}
	return clean
}

// KeyForBackup returns the canonical storage key for a backup artifact.
// Convention: "backups/<backupID>/<filename>"
func KeyForBackup(backupID, filename string) string {
	return fmt.Sprintf("backups/%s/%s", backupID, filename)
}

// BackupPrefix returns the key prefix for all objects in a backup.
func BackupPrefix(backupID string) string {
	return fmt.Sprintf("backups/%s/", backupID)
}

// removeEmptyDirs removes dir and its parents up to stopAt if they are empty.
func removeEmptyDirs(dir, stopAt string) error {
	for dir != stopAt && dir != filepath.Dir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return err
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

// WriteFile is a convenience helper that writes the contents of a local file
// into the storage backend under key.
func WriteFile(ctx context.Context, b storage.Backend, key, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"opening local file %s for upload", localPath)
	}
	defer f.Close()
	return b.Write(ctx, key, f)
}

// ReadFile downloads an object from the backend and writes it to localPath.
// The file is written atomically (temp + rename).
func ReadFile(ctx context.Context, b storage.Backend, key, localPath string) error {
	rc, err := b.Read(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating directory for %s", localPath)
	}

	tmp, err := os.CreateTemp(filepath.Dir(localPath), ".toris_dl_*")
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"creating temp file for download of key %s", key)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, rc); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"writing download of key %s", key)
	}
	if err := tmp.Sync(); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "syncing download")
	}
	tmp.Close()

	if err := os.Chmod(tmpName, 0o640); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "chmod download temp file")
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err,
			"renaming download temp to %s", localPath)
	}
	committed = true
	return nil
}
