package util

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractPGDataArchive extracts a pg_basebackup tar archive into destDir,
// preserving the layout PostgreSQL expects:
//
//   - base.tar[.gz]   entries are relative to the data dir root → destDir
//   - pg_wal.tar[.gz] entries are bare WAL segment files → destDir/pg_wal/
//
// pg_basebackup -Ft writes the WAL files at the archive root (no pg_wal/
// prefix), so extracting them verbatim would scatter WAL files across the
// data directory and break both pg_verifybackup and crash recovery.
func ExtractPGDataArchive(ctx context.Context, archivePath, destDir string) error {
	dest := destDir
	if strings.HasPrefix(filepath.Base(archivePath), "pg_wal") {
		dest = filepath.Join(destDir, "pg_wal")
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return fmt.Errorf("creating pg_wal directory: %w", err)
		}
	}
	return ExtractTar(ctx, archivePath, dest)
}

// ExtractTar decompresses and extracts a .tar.gz (or plain .tar) archive into
// destDir. Path traversal entries are rejected, and symlinks are skipped, so
// the extraction cannot escape destDir.
func ExtractTar(ctx context.Context, archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive %s: %w", archivePath, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(archivePath, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("creating gzip reader: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extraction canceled: %w", err)
		}

		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar header: %w", err)
		}

		// Security: reject any path that escapes destDir.
		target := filepath.Join(destDir, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) &&
			target != filepath.Clean(destDir) {
			return fmt.Errorf("archive contains path traversal entry: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0o700); err != nil {
				return fmt.Errorf("creating directory %s: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("creating parent for %s: %w", target, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
			if err != nil {
				return fmt.Errorf("creating file %s: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("writing file %s: %w", target, err)
			}
			out.Close()
		case tar.TypeSymlink:
			// Skip symlinks for security.
			continue
		}
	}
	return nil
}
