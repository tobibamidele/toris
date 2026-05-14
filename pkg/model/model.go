// Package model defines all shared data types for toris.
// These types cross package boundaries and are used by both the control and data planes.
package model

import "time"

// ─── Cluster ────────────────────────────────────────────────────────────────

// ClusterStatus represents the operational state of a cluster.
type ClusterStatus string

const (
	ClusterStatusUnknown     ClusterStatus = "unknown"
	ClusterStatusHealthy     ClusterStatus = "healthy"
	ClusterStatusDegraded    ClusterStatus = "degraded"
	ClusterStatusUnhealthy   ClusterStatus = "unhealthy"
	ClusterStatusFailingOver ClusterStatus = "failing_over"
)

// Cluster is the logical unit managed by toris.
// One cluster = one primary + N replicas + one stable endpoint.
type Cluster struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     ClusterStatus     `json:"status"`
	PrimaryID  string            `json:"primary_id,omitempty"`
	Generation int64             `json:"generation"` // incremented on every leadership change
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ─── Node ────────────────────────────────────────────────────────────────────

// NodeRole indicates what role a DB node currently holds.
type NodeRole string

const (
	NodeRoleUnknown NodeRole = "unknown"
	NodeRolePrimary NodeRole = "primary"
	NodeRoleReplica NodeRole = "replica"
	NodeRoleStandby NodeRole = "standby" // cold standby, not streaming
)

// NodeStatus is the lifecycle state of a node within toris.
type NodeStatus string

const (
	NodeStatusUnknown   NodeStatus = "unknown"
	NodeStatusJoining   NodeStatus = "joining"
	NodeStatusHealthy   NodeStatus = "healthy"
	NodeStatusDegraded  NodeStatus = "degraded"
	NodeStatusUnhealthy NodeStatus = "unhealthy"
	NodeStatusDraining  NodeStatus = "draining"
	NodeStatusFenced    NodeStatus = "fenced"
	NodeStatusRemoved   NodeStatus = "removed"
)

// Node represents a single database server within a cluster.
type Node struct {
	ID        string     `json:"id"`
	ClusterID string     `json:"cluster_id"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Role      NodeRole   `json:"role"`
	Status    NodeStatus `json:"status"`
	// ReplicationLagBytes is only meaningful for replicas.
	ReplicationLagBytes int64             `json:"replication_lag_bytes,omitempty"`
	LastSeenAt          time.Time         `json:"last_seen_at,omitempty"`
	JoinedAt            time.Time         `json:"joined_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Labels              map[string]string `json:"labels,omitempty"`
}

// Addr returns "host:port" for this node.
func (n *Node) Addr() string {
	return n.Host + ":" + itoa(n.Port)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	b := make([]byte, 0, 6)
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ─── Lease ───────────────────────────────────────────────────────────────────

// LeaseStatus describes the state of a distributed lease.
type LeaseStatus string

const (
	LeaseStatusActive   LeaseStatus = "active"
	LeaseStatusExpired  LeaseStatus = "expired"
	LeaseStatusReleased LeaseStatus = "released"
)

// Lease is the distributed coordination record that prevents split-brain.
// Only the holder of an active lease may make cluster-altering decisions.
type Lease struct {
	ID            string      `json:"id"`
	ClusterID     string      `json:"cluster_id"`
	InstanceID    string      `json:"instance_id"` // toris daemon instance that holds the lease
	LeaderID      string      `json:"leader_id"`   // node ID of the elected primary
	Generation    int64       `json:"generation"`  // monotonically increasing fencing token
	Status        LeaseStatus `json:"status"`
	AcquiredAt    time.Time   `json:"acquired_at"`
	ExpiresAt     time.Time   `json:"expires_at"`
	LastHeartbeat time.Time   `json:"last_heartbeat"`
	ReleasedAt    *time.Time  `json:"released_at,omitempty"`
}

// IsExpired reports whether the lease TTL has passed.
func (l *Lease) IsExpired(now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// ─── Backup ──────────────────────────────────────────────────────────────────

// BackupStatus tracks the lifecycle of a single backup operation.
type BackupStatus string

const (
	BackupStatusPending  BackupStatus = "pending"
	BackupStatusRunning  BackupStatus = "running"
	BackupStatusVerified BackupStatus = "verified"
	BackupStatusUploaded BackupStatus = "uploaded"
	BackupStatusRetained BackupStatus = "retained"
	BackupStatusPruned   BackupStatus = "pruned"
	BackupStatusFailed   BackupStatus = "failed"
)

// Backup is the top-level record for a backup operation.
type Backup struct {
	ID          string            `json:"id"`
	ClusterID   string            `json:"cluster_id"`
	NodeID      string            `json:"node_id"`
	Generation  int64             `json:"generation"` // cluster generation at time of backup
	Status      BackupStatus      `json:"status"`
	StoragePath string            `json:"storage_path"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  *time.Time        `json:"finished_at,omitempty"`
	VerifiedAt  *time.Time        `json:"verified_at,omitempty"`
	UploadedAt  *time.Time        `json:"uploaded_at,omitempty"`
	PrunedAt    *time.Time        `json:"pruned_at,omitempty"`
	FailureMsg  string            `json:"failure_msg,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// BackupArtifact is an individual file that comprises a backup.
type BackupArtifact struct {
	ID        string    `json:"id"`
	BackupID  string    `json:"backup_id"`
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupManifest is the integrity record written alongside every backup.
// It is the ground truth for verification.
type BackupManifest struct {
	BackupID        string           `json:"backup_id"`
	ClusterID       string           `json:"cluster_id"`
	NodeID          string           `json:"node_id"`
	Generation      int64            `json:"generation"`
	CreatedAt       time.Time        `json:"created_at"`
	PostgresVersion string           `json:"postgres_version"`
	WALStart        string           `json:"wal_start"`
	WALStop         string           `json:"wal_stop"`
	Artifacts       []BackupArtifact `json:"artifacts"`
	TotalSizeBytes  int64            `json:"total_size_bytes"`
	ManifestSHA256  string           `json:"manifest_sha256"` // self-hash, computed last
}

// ─── Restore ─────────────────────────────────────────────────────────────────

// RestoreStatus tracks a restore operation through its lifecycle.
type RestoreStatus string

const (
	RestoreStatusQueued    RestoreStatus = "queued"
	RestoreStatusRunning   RestoreStatus = "running"
	RestoreStatusVerified  RestoreStatus = "verified"
	RestoreStatusPromoted  RestoreStatus = "promoted"
	RestoreStatusCompleted RestoreStatus = "completed"
	RestoreStatusFailed    RestoreStatus = "failed"
)

// RestoreJob represents a single restore operation.
type RestoreJob struct {
	ID           string        `json:"id"`
	BackupID     string        `json:"backup_id"`
	ClusterID    string        `json:"cluster_id"`
	TargetNodeID string        `json:"target_node_id"`
	Status       RestoreStatus `json:"status"`
	IsRehearsal  bool          `json:"is_rehearsal"` // true = dry-run into temp env
	StartedAt    time.Time     `json:"started_at"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	FailureMsg   string        `json:"failure_msg,omitempty"`
	ArtifactDir  string        `json:"artifact_dir,omitempty"` // for forensics on failure
}

// ─── Failover ────────────────────────────────────────────────────────────────

// FailoverStatus tracks the state of a failover event.
type FailoverStatus string

const (
	FailoverStatusDetected   FailoverStatus = "detected"
	FailoverStatusFenced     FailoverStatus = "fenced"
	FailoverStatusPromoted   FailoverStatus = "promoted"
	FailoverStatusRouted     FailoverStatus = "routed"
	FailoverStatusStabilized FailoverStatus = "stabilized"
	FailoverStatusReconciled FailoverStatus = "reconciled"
	FailoverStatusFailed     FailoverStatus = "failed"
)

// FailoverEvent is the audit record for a single failover.
type FailoverEvent struct {
	ID           string         `json:"id"`
	ClusterID    string         `json:"cluster_id"`
	OldPrimaryID string         `json:"old_primary_id"`
	NewPrimaryID string         `json:"new_primary_id"`
	Status       FailoverStatus `json:"status"`
	Generation   int64          `json:"generation"` // fencing token at time of failover
	Reason       string         `json:"reason"`
	InitiatedBy  string         `json:"initiated_by"` // "daemon" or "operator:<id>"
	DetectedAt   time.Time      `json:"detected_at"`
	FencedAt     *time.Time     `json:"fenced_at,omitempty"`
	PromotedAt   *time.Time     `json:"promoted_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	FailureMsg   string         `json:"failure_msg,omitempty"`
}

// ─── Health ──────────────────────────────────────────────────────────────────

// HealthLevel describes which checks a node has passed.
type HealthLevel int

const (
	HealthLevelUnreachable HealthLevel = 0 // failed L1
	HealthLevelTransport   HealthLevel = 1 // L1 passed (TCP connects)
	HealthLevelReady       HealthLevel = 2 // L2 passed (pg_isready ok)
	HealthLevelLive        HealthLevel = 3 // L3 passed (SELECT 1 ok)
	HealthLevelRoleKnown   HealthLevel = 4 // L4 passed (role detected)
	HealthLevelPolicyPass  HealthLevel = 5 // L5 passed (policy checks ok)
)

// HealthSnapshot is the result of evaluating all health levels for a node.
type HealthSnapshot struct {
	NodeID              string      `json:"node_id"`
	Level               HealthLevel `json:"level"`
	Role                NodeRole    `json:"role"`
	IsInRecovery        bool        `json:"is_in_recovery"` // true = standby/replica
	ReplicationLagBytes int64       `json:"replication_lag_bytes,omitempty"`
	DiskFreeBytes       int64       `json:"disk_free_bytes,omitempty"`
	Errors              []string    `json:"errors,omitempty"`
	CheckedAt           time.Time   `json:"checked_at"`
	// L1–L5 individual results
	TransportOK bool `json:"transport_ok"`
	ReadyOK     bool `json:"ready_ok"`
	LiveOK      bool `json:"live_ok"`
	RoleOK      bool `json:"role_ok"`
	PolicyOK    bool `json:"policy_ok"`
}

// IsHealthyForRole returns true if the snapshot satisfies all checks
// required for the given role.
func (h *HealthSnapshot) IsHealthyForRole(role NodeRole) bool {
	if h.Level < HealthLevelRoleKnown {
		return false
	}
	if h.Role != role {
		return false
	}
	return h.PolicyOK
}

// ─── Routing ─────────────────────────────────────────────────────────────────

// RoutingTarget is the current write target for the stable proxy endpoint.
type RoutingTarget struct {
	ClusterID  string    `json:"cluster_id"`
	NodeID     string    `json:"node_id"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Generation int64     `json:"generation"` // must match current lease generation
	UpdatedAt  time.Time `json:"updated_at"`
}

// ─── Auth ────────────────────────────────────────────────────────────────────

// AuthProfile defines the credentials for a specific operation class.
type AuthProfile struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	// Password is never serialized to JSON.
	Password    string `json:"-"`
	SSLMode     string `json:"ssl_mode"`
	SSLCertFile string `json:"ssl_cert_file,omitempty"`
	SSLKeyFile  string `json:"ssl_key_file,omitempty"`
	SSLRootCert string `json:"ssl_root_cert,omitempty"`
}

// ─── Retention ───────────────────────────────────────────────────────────────

// RetentionPolicy defines how many/how long backups are kept.
type RetentionPolicy struct {
	// Keep at least MinCount backups regardless of age.
	MinCount int `json:"min_count"`
	// Keep backups created within MaxAgeDays.
	MaxAgeDays int `json:"max_age_days"`
	// KeepFailed determines whether failed backups are auto-pruned.
	KeepFailed bool `json:"keep_failed"`
}

// ─── Audit ───────────────────────────────────────────────────────────────────

// AuditEventKind categorizes audit events.
type AuditEventKind string

const (
	AuditKindBackupCreated    AuditEventKind = "backup.created"
	AuditKindBackupVerified   AuditEventKind = "backup.verified"
	AuditKindBackupFailed     AuditEventKind = "backup.failed"
	AuditKindBackupPruned     AuditEventKind = "backup.pruned"
	AuditKindRestoreStarted   AuditEventKind = "restore.started"
	AuditKindRestoreCompleted AuditEventKind = "restore.completed"
	AuditKindRestoreFailed    AuditEventKind = "restore.failed"
	AuditKindLeaseAcquired    AuditEventKind = "lease.acquired"
	AuditKindLeaseRenewed     AuditEventKind = "lease.renewed"
	AuditKindLeaseReleased    AuditEventKind = "lease.released"
	AuditKindLeaseExpired     AuditEventKind = "lease.expired"
	AuditKindFailoverDetected AuditEventKind = "failover.detected"
	AuditKindFailoverComplete AuditEventKind = "failover.complete"
	AuditKindNodeAdded        AuditEventKind = "node.added"
	AuditKindNodeRemoved      AuditEventKind = "node.removed"
	AuditKindNodeFenced       AuditEventKind = "node.fenced"
	AuditKindNodePromoted     AuditEventKind = "node.promoted"
)

// AuditEvent is an immutable record of a significant system action.
type AuditEvent struct {
	ID         string            `json:"id"`
	ClusterID  string            `json:"cluster_id"`
	Kind       AuditEventKind    `json:"kind"`
	ActorID    string            `json:"actor_id"`   // instance ID that generated the event
	SubjectID  string            `json:"subject_id"` // node/backup/job ID the action was on
	Generation int64             `json:"generation"` // fencing token in effect
	Message    string            `json:"message"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
}

// ─── Restore modes ────────────────────────────────────────────────────────────

// RestoreMode distinguishes what kind of restore operation is being performed.
type RestoreMode string

const (
	// RestoreModeEmptyNode restores into a clean, empty data directory.
	RestoreModeEmptyNode RestoreMode = "empty_node"
	// RestoreModeReplacement restores to replace a failed node in the cluster.
	RestoreModeReplacement RestoreMode = "replacement"
	// RestoreModeRehearsal restores into a temp directory for verification only.
	RestoreModeRehearsal RestoreMode = "rehearsal"
	// RestoreModeReseed restores to reseed a replica from the latest backup.
	RestoreModeReseed RestoreMode = "reseed"
)

// RewindStatus tracks a post-failover pg_rewind or reseed operation.
type RewindStatus string

const (
	RewindStatusPending   RewindStatus = "pending"
	RewindStatusRunning   RewindStatus = "running"
	RewindStatusCompleted RewindStatus = "completed"
	RewindStatusFailed    RewindStatus = "failed"
	// RewindStatusFallback means pg_rewind failed and a full reseed was used instead.
	RewindStatusFallback RewindStatus = "fallback_reseed"
)

// RewindJob tracks a single post-failover rewind or reseed attempt for an old primary.
type RewindJob struct {
	ID           string       `json:"id"`
	ClusterID    string       `json:"cluster_id"`
	NodeID       string       `json:"node_id"` // old primary being rewound
	NewPrimaryID string       `json:"new_primary_id"`
	Generation   int64        `json:"generation"`
	Status       RewindStatus `json:"status"`
	UsedFallback bool         `json:"used_fallback"` // true if reseed was used instead of rewind
	StartedAt    time.Time    `json:"started_at"`
	FinishedAt   *time.Time   `json:"finished_at,omitempty"`
	FailureMsg   string       `json:"failure_msg,omitempty"`
}
