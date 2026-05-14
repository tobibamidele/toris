// Package storage defines the interface every storage backend must satisfy.
// v1 ships a filesystem backend. S3 is a real but non-default implementation.
//
// Design rules:
//   - All methods accept a context for cancellation and timeout.
//   - Keys are slash-separated logical paths, e.g. "backups/abc123/base.tar.gz".
//   - Implementations must be safe for concurrent use.
//   - Write must be atomic: a partial write must never be visible to Read.
package storage

import (
	"context"
	"io"
	"time"
)

// Backend is the interface every storage implementation must satisfy.
type Backend interface {
	// Write stores data from r under key.
	// The write must be atomic: either the full object is visible or nothing is.
	Write(ctx context.Context, key string, r io.Reader) error

	// Read returns a ReadCloser for the object at key.
	// The caller must close the returned ReadCloser.
	Read(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at key. Not an error if it does not exist.
	Delete(ctx context.Context, key string) error

	// List returns all keys with the given prefix, in lexicographic order.
	// An empty prefix lists everything.
	List(ctx context.Context, prefix string) ([]string, error)

	// Stat returns metadata for the object at key without reading its content.
	Stat(ctx context.Context, key string) (ObjectInfo, error)

	// Name returns a human-readable identifier for this backend instance.
	// Used in log messages and error context.
	Name() string
}

// ObjectInfo holds metadata about a stored object.
type ObjectInfo struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
	// ContentHash is a hex-encoded SHA-256 or ETag depending on the backend.
	ContentHash string
}
