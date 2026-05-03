// Package db defines the interface that every database backend must satisfy.
// All postgres-specific logic lives in internal/db/postgres.
// This file contains only the interface and shared types.
package db

import (
	"context"

	"github.com/tobibamidele/toris/pkg/model"
)

// Backend is the interface every database engine adapter must implement.
// v1 has one implementation: postgres. Future adapters (MySQL, etc.) implement this.
type Backend interface {
	// Name returns the engine name, e.g. "postgres".
	Name() string

	// Ping verifies the database is reachable and the credentials work.
	Ping(ctx context.Context, node *model.Node) error

	// Health performs the full layered health check and returns a HealthSnapshot.
	Health(ctx context.Context, node *model.Node) (*model.HealthSnapshot, error)

	// IsPrimary returns true if the node is the writable primary.
	IsPrimary(ctx context.Context, node *model.Node) (bool, error)

	// ReplicationLag returns the replication lag in bytes for a standby node.
	// Returns 0 for primary nodes.
	ReplicationLag(ctx context.Context, node *model.Node) (int64, error)

	// Promote promotes a replica to primary.
	// The caller must hold the current lease and pass the fencing token.
	Promote(ctx context.Context, node *model.Node, fencingToken int64) error

	// Fence marks the node as read-only and terminates active connections.
	Fence(ctx context.Context, node *model.Node, fencingToken int64) error

	// DiskFree returns available disk bytes on the node's data directory filesystem.
	DiskFree(ctx context.Context, node *model.Node) (int64, error)

	// Close releases all resources held by the backend for a given node.
	Close(ctx context.Context, node *model.Node) error
}

// ToolChecker verifies external tool availability.
// Backends that wrap CLI tools (pg_basebackup etc.) implement this.
type ToolChecker interface {
	// CheckTools verifies all required external tools are present and returns
	// their resolved paths. Returns an error if any are missing.
	CheckTools(ctx context.Context) (map[string]string, error)
}
