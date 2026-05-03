// Package config - validator.go
// Validates a loaded *Config before the application starts.
// Returns actionable errors — never silent failures.
package config

import (
	"fmt"
	"os"
	"strings"
)

// ValidationError aggregates all config problems found during validation.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("config validation failed (%d problems):\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// Validate checks the config for correctness and completeness.
// It collects all problems before returning so operators see everything at once.
func Validate(cfg *Config) error {
	var problems []string
	add := func(msg string) { problems = append(problems, msg) }

	// Instance identity
	if cfg.InstanceID == "" {
		add("instance_id is required")
	}

	// Cluster
	if cfg.Cluster.ID == "" {
		add("cluster.id is required")
	}
	if cfg.Cluster.Name == "" {
		add("cluster.name is required")
	}
	if len(cfg.Cluster.Nodes) == 0 {
		add("cluster.nodes must contain at least one node")
	}
	seenNodeIDs := map[string]bool{}
	for i, n := range cfg.Cluster.Nodes {
		prefix := fmt.Sprintf("cluster.nodes[%d]", i)
		if n.ID == "" {
			add(prefix + ".id is required")
		} else if seenNodeIDs[n.ID] {
			add(prefix + ".id is duplicate: " + n.ID)
		} else {
			seenNodeIDs[n.ID] = true
		}
		if n.Host == "" {
			add(prefix + ".host is required")
		}
		if n.Port <= 0 || n.Port > 65535 {
			add(fmt.Sprintf("%s.port must be 1–65535, got %d", prefix, n.Port))
		}
	}

	// Database engine
	if cfg.Database.Engine != "postgres" {
		add(fmt.Sprintf("database.engine %q is not supported; only \"postgres\" is supported in v1", cfg.Database.Engine))
	}

	// Control DSN
	if cfg.ControlDSN == "" {
		add("control_dsn is required (toris needs its own DB for lease/manifest storage)")
	}

	// Backup storage
	switch cfg.Backup.StorageBackend {
	case "fs":
		if cfg.Backup.BaseDir == "" {
			add("backup.base_dir is required when storage_backend is \"fs\"")
		}
	case "s3":
		if cfg.Backup.S3.Bucket == "" {
			add("backup.s3.bucket is required when storage_backend is \"s3\"")
		}
		if cfg.Backup.S3.Region == "" {
			add("backup.s3.region is required when storage_backend is \"s3\"")
		}
	default:
		add(fmt.Sprintf("backup.storage_backend %q is not supported; use \"fs\" or \"s3\"", cfg.Backup.StorageBackend))
	}

	if cfg.Backup.Retention.MinCount < 1 {
		add("backup.retention.min_count must be >= 1")
	}

	// Leader election
	if cfg.Leader.LeaseTTL <= 0 {
		add("leader.lease_ttl must be positive")
	}
	if cfg.Leader.RenewInterval <= 0 {
		add("leader.renew_interval must be positive")
	}
	if cfg.Leader.RenewInterval >= cfg.Leader.LeaseTTL {
		add("leader.renew_interval must be less than leader.lease_ttl to prevent lease expiry under load")
	}

	// Failover
	if cfg.Failover.UnhealthyThreshold <= 0 {
		add("failover.unhealthy_threshold must be positive")
	}

	// Proxy
	if cfg.Proxy.Enabled && cfg.Proxy.ListenAddr == "" {
		add("proxy.listen_addr is required when proxy is enabled")
	}

	// Metrics
	if cfg.Metrics.Enabled && cfg.Metrics.ListenAddr == "" {
		add("metrics.listen_addr is required when metrics is enabled")
	}

	// TLS
	validTLSModes := map[string]bool{
		"disable": true, "require": true, "verify-ca": true, "verify-full": true,
	}
	if !validTLSModes[cfg.TLS.Mode] {
		add(fmt.Sprintf("tls.mode %q is invalid; must be one of: disable, require, verify-ca, verify-full", cfg.TLS.Mode))
	}
	if (cfg.TLS.Mode == "verify-ca" || cfg.TLS.Mode == "verify-full") && cfg.TLS.CAFile == "" {
		add("tls.ca_file is required when tls.mode is verify-ca or verify-full")
	}
	if cfg.TLS.CertFile != "" {
		if err := checkFileReadable(cfg.TLS.CertFile); err != nil {
			add("tls.cert_file: " + err.Error())
		}
	}
	if cfg.TLS.KeyFile != "" {
		if err := checkFileReadable(cfg.TLS.KeyFile); err != nil {
			add("tls.key_file: " + err.Error())
		}
		if err := checkFilePermissions(cfg.TLS.KeyFile, 0600); err != nil {
			add("tls.key_file: " + err.Error())
		}
	}
	if cfg.TLS.CAFile != "" {
		if err := checkFileReadable(cfg.TLS.CAFile); err != nil {
			add("tls.ca_file: " + err.Error())
		}
	}

	// Auth profiles
	seenProfiles := map[string]bool{}
	for i, p := range cfg.Auth.Profiles {
		prefix := fmt.Sprintf("auth.profiles[%d]", i)
		if p.Name == "" {
			add(prefix + ".name is required")
		} else if seenProfiles[p.Name] {
			add(prefix + ".name is duplicate: " + p.Name)
		} else {
			seenProfiles[p.Name] = true
		}
		if p.Username == "" {
			add(prefix + ".username is required")
		}
		if p.Password == "" && p.PasswordFile == "" {
			add(prefix + ": either password or password_file is required")
		}
	}

	// Log settings
	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLogLevels[strings.ToLower(cfg.LogLevel)] {
		add(fmt.Sprintf("log_level %q is invalid; must be one of: debug, info, warn, error", cfg.LogLevel))
	}
	validLogFormats := map[string]bool{"json": true, "console": true}
	if !validLogFormats[strings.ToLower(cfg.LogFormat)] {
		add(fmt.Sprintf("log_format %q is invalid; must be \"json\" or \"console\"", cfg.LogFormat))
	}
	validOutputFormats := map[string]bool{"human": true, "json": true}
	if !validOutputFormats[strings.ToLower(cfg.OutputFormat)] {
		add(fmt.Sprintf("output_format %q is invalid; must be \"human\" or \"json\"", cfg.OutputFormat))
	}

	// Timeouts sanity
	if cfg.Timeouts.Connect <= 0 {
		add("timeouts.connect must be positive")
	}
	if cfg.Timeouts.Query <= 0 {
		add("timeouts.query must be positive")
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// checkFileReadable returns an error if the path doesn't exist or isn't readable.
func checkFileReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read %q: %w", path, err)
	}
	f.Close()
	return nil
}

// checkFilePermissions returns an error if the file's mode is more permissive than want.
func checkFilePermissions(path string, want os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	actual := info.Mode().Perm()
	if actual&^want != 0 {
		return fmt.Errorf("%q has permissions %04o; must be %04o or stricter to protect secrets",
			path, actual, want)
	}
	return nil
}
