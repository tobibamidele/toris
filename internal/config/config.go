// Package config provides the typed configuration struct for toris.
// All subsystems receive a *Config — no global config state.
package config

import "time"

// Config is the root configuration for a toris instance.
// It is loaded once at startup and treated as read-only after validation.
type Config struct {
	// Global identity
	InstanceID   string `mapstructure:"instance_id" yaml:"instance_id"`
	LogLevel     string `mapstructure:"log_level"   yaml:"log_level"`
	LogFormat    string `mapstructure:"log_format"  yaml:"log_format"`      // "json" | "console"
	OutputFormat string `mapstructure:"output_format" yaml:"output_format"` // "human" | "json"

	// Cluster
	Cluster ClusterConfig `mapstructure:"cluster" yaml:"cluster"`

	// Database backend
	Database DatabaseConfig `mapstructure:"database" yaml:"database"`

	// Control DSN — where toris stores its own lease/manifest tables.
	// Distinct from the user cluster nodes so toris can operate even during failover.
	ControlDSN string `mapstructure:"control_dsn" yaml:"control_dsn"`

	// Backup
	Backup BackupConfig `mapstructure:"backup" yaml:"backup"`

	// Restore
	Restore RestoreConfig `mapstructure:"restore" yaml:"restore"`

	// Leader election
	Leader LeaderConfig `mapstructure:"leader" yaml:"leader"`

	// Failover
	Failover FailoverConfig `mapstructure:"failover" yaml:"failover"`

	// Routing / stable endpoint proxy
	Proxy ProxyConfig `mapstructure:"proxy" yaml:"proxy"`

	// Observability
	Metrics MetricsConfig `mapstructure:"metrics" yaml:"metrics"`

	// Timeouts (global defaults, can be overridden per subsystem)
	Timeouts TimeoutConfig `mapstructure:"timeouts" yaml:"timeouts"`

	// TLS
	TLS TLSConfig `mapstructure:"tls" yaml:"tls"`

	// Auth profiles
	Auth AuthConfig `mapstructure:"auth" yaml:"auth"`
}

// ClusterConfig describes the logical cluster this toris instance manages.
type ClusterConfig struct {
	ID   string `mapstructure:"id"   yaml:"id"`
	Name string `mapstructure:"name" yaml:"name"`
	// Nodes is the initial static node list.
	// Dynamic discovery can augment this later.
	Nodes []NodeConfig `mapstructure:"nodes" yaml:"nodes"`
}

// NodeConfig is a static node declaration in the config file.
type NodeConfig struct {
	ID   string `mapstructure:"id"   yaml:"id"`
	Host string `mapstructure:"host" yaml:"host"`
	Port int    `mapstructure:"port" yaml:"port"`
	// AuthProfile references a named profile in AuthConfig.
	AuthProfile string `mapstructure:"auth_profile" yaml:"auth_profile"`
}

// DatabaseConfig selects and configures the database backend engine.
type DatabaseConfig struct {
	// Engine is the backend type. Only "postgres" is supported in v1.
	Engine string `mapstructure:"engine" yaml:"engine"`

	// MaxOpenConns for the control connection pool.
	MaxOpenConns int `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	// MaxIdleConns for the control connection pool.
	MaxIdleConns int `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
}

// BackupConfig controls backup behavior.
type BackupConfig struct {
	// StorageBackend is "fs" or "s3".
	StorageBackend string `mapstructure:"storage_backend" yaml:"storage_backend"`
	// BaseDir is the local filesystem root for "fs" storage.
	BaseDir string `mapstructure:"base_dir" yaml:"base_dir"`
	// S3 settings (only used when StorageBackend == "s3").
	S3 S3Config `mapstructure:"s3" yaml:"s3"`

	// Retention
	Retention RetentionConfig `mapstructure:"retention" yaml:"retention"`

	// RunRehearsalOnCreate makes every backup trigger a restore rehearsal.
	RunRehearsalOnCreate bool `mapstructure:"run_rehearsal_on_create" yaml:"run_rehearsal_on_create"`

	// OffSiteRequired: if true, backup is only marked complete after offsite copy.
	OffSiteRequired bool `mapstructure:"offsite_required" yaml:"offsite_required"`
}

// RetentionConfig defines backup retention rules.
type RetentionConfig struct {
	MinCount   int  `mapstructure:"min_count"    yaml:"min_count"`
	MaxAgeDays int  `mapstructure:"max_age_days" yaml:"max_age_days"`
	KeepFailed bool `mapstructure:"keep_failed" yaml:"keep_failed"`
}

// S3Config holds object storage credentials and settings.
type S3Config struct {
	Bucket    string `mapstructure:"bucket"     yaml:"bucket"`
	Region    string `mapstructure:"region"     yaml:"region"`
	Endpoint  string `mapstructure:"endpoint"   yaml:"endpoint"` // for minio etc.
	AccessKey string `mapstructure:"access_key" yaml:"access_key"`
	// SecretKey is never logged.
	SecretKey string `mapstructure:"secret_key" yaml:"secret_key"`
}

// LeaderConfig controls lease-based leader election.
type LeaderConfig struct {
	// LeaseTTL is how long a lease is valid without renewal.
	LeaseTTL time.Duration `mapstructure:"lease_ttl" yaml:"lease_ttl"`
	// RenewInterval is how often the leader renews the lease.
	RenewInterval time.Duration `mapstructure:"renew_interval" yaml:"renew_interval"`
	// AcquireRetryInterval is how long to wait between acquisition attempts.
	AcquireRetryInterval time.Duration `mapstructure:"acquire_retry_interval" yaml:"acquire_retry_interval"`
}

// FailoverConfig controls when automatic failover triggers.
type FailoverConfig struct {
	// Enabled: if false, toris detects but does NOT automatically fail over.
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// UnhealthyThreshold: how long primary must be unhealthy before failover.
	UnhealthyThreshold time.Duration `mapstructure:"unhealthy_threshold" yaml:"unhealthy_threshold"`
	// MaxReplicationLagBytes: replica with more lag is not promoted.
	MaxReplicationLagBytes int64 `mapstructure:"max_replication_lag_bytes" yaml:"max_replication_lag_bytes"`
	// ReplicationOutageThreshold is how long all replicas must be unreachable
	// before the outage is classified as unsafe (Class A escalation).
	// Defaults to UnhealthyThreshold when unset.
	ReplicationOutageThreshold time.Duration `mapstructure:"replication_outage_threshold" yaml:"replication_outage_threshold"`
	// AutoRewindAfterFailover: attempt pg_rewind on the old primary after failover.
	// If rewind fails, falls back to full reseed.
	AutoRewindAfterFailover bool `mapstructure:"auto_rewind_after_failover" yaml:"auto_rewind_after_failover"`
}

// RestoreConfig controls restore defaults.
type RestoreConfig struct {
	// TempDir is where rehearsal restores are staged.
	TempDir string `mapstructure:"temp_dir" yaml:"temp_dir"`
	// StartAfterRestore: if true, attempt to start the DB after restoring.
	StartAfterRestore bool `mapstructure:"start_after_restore" yaml:"start_after_restore"`
	// DataDir overrides the default PostgreSQL data directory for restores.
	DataDir string `mapstructure:"data_dir" yaml:"data_dir"`
}

// ProxyConfig controls the stable TCP proxy listener.
type ProxyConfig struct {
	// Enabled: if false, toris does not start the proxy listener.
	Enabled    bool   `mapstructure:"enabled" yaml:"enabled"`
	ListenAddr string `mapstructure:"listen_addr" yaml:"listen_addr"` // e.g. "0.0.0.0:5433"
	// DialTimeout for upstream connections.
	DialTimeout time.Duration `mapstructure:"dial_timeout" yaml:"dial_timeout"`
}

// MetricsConfig controls the Prometheus exposition endpoint.
type MetricsConfig struct {
	Enabled    bool   `mapstructure:"enabled"     yaml:"enabled"`
	ListenAddr string `mapstructure:"listen_addr" yaml:"listen_addr"` // e.g. ":9100"
}

// TimeoutConfig sets global timeouts. Subsystems may override individually.
type TimeoutConfig struct {
	Connect  time.Duration `mapstructure:"connect"   yaml:"connect"`
	Query    time.Duration `mapstructure:"query"     yaml:"query"`
	Backup   time.Duration `mapstructure:"backup"    yaml:"backup"`
	Restore  time.Duration `mapstructure:"restore"   yaml:"restore"`
	Failover time.Duration `mapstructure:"failover"  yaml:"failover"`
	ToolExec time.Duration `mapstructure:"tool_exec" yaml:"tool_exec"`
}

// TLSConfig holds TLS settings for database connections.
type TLSConfig struct {
	// Mode: "disable", "require", "verify-ca", "verify-full"
	Mode     string `mapstructure:"mode"      yaml:"mode"`
	CertFile string `mapstructure:"cert_file" yaml:"cert_file"`
	KeyFile  string `mapstructure:"key_file"  yaml:"key_file"`
	CAFile   string `mapstructure:"ca_file"   yaml:"ca_file"`
}

// AuthConfig holds named authentication profiles.
type AuthConfig struct {
	Profiles []AuthProfile `mapstructure:"profiles" yaml:"profiles"`
}

// AuthProfile maps a name to a set of credentials.
type AuthProfile struct {
	Name     string `mapstructure:"name"     yaml:"name"`
	Username string `mapstructure:"username" yaml:"username"`
	// Password is read from the file/env but never serialized to output.
	Password string `mapstructure:"password" yaml:"password"`
	// PasswordFile: read password from this file path at runtime.
	PasswordFile string `mapstructure:"password_file" yaml:"password_file"`
	Database     string `mapstructure:"database"      yaml:"database"`
}
