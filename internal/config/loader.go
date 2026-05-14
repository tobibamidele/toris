// Package config - loader.go
// Layered config loading without external dependencies:
//  1. Built-in defaults
//  2. YAML config file (encoding/json is stdlib; we use gopkg.in/yaml.v3 via replace)
//  3. Environment variables  (TORIS_* prefix, underscore-separated)
//  4. CLI flag overrides (passed in as map)
//
// This keeps the dependency tree minimal and the behavior explicit.
package config

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads the configuration from the given file path and environment,
// populates defaults, and returns a *Config ready for validation.
// flagOverrides is a flat map of dotted keys → values, e.g. {"log_level": "debug"}.
func Load(cfgFile string, flagOverrides map[string]any) (*Config, error) {
	cfg := defaultConfig()

	// ── Layer 2: YAML file ────────────────────────────────────────────────
	path := cfgFile
	if path == "" {
		path = findConfigFile()
	}
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening config file %s: %w", path, err)
		}
		defer f.Close()
		if err := decodeYAML(f, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	}

	// ── Layer 3: Environment variables ───────────────────────────────────
	applyEnv(cfg)

	// ── Layer 4: CLI flag overrides ───────────────────────────────────────
	for k, v := range flagOverrides {
		if err := applyFlagOverride(cfg, k, v); err != nil {
			return nil, fmt.Errorf("applying flag override %q: %w", k, err)
		}
	}

	// ── Password files ────────────────────────────────────────────────────
	for i := range cfg.Auth.Profiles {
		p := &cfg.Auth.Profiles[i]
		if p.PasswordFile != "" && p.Password == "" {
			data, err := os.ReadFile(p.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("reading password file for profile %q: %w", p.Name, err)
			}
			p.Password = strings.TrimSpace(string(data))
		}
	}

	return cfg, nil
}

// defaultConfig returns a Config pre-populated with safe production defaults.
func defaultConfig() *Config {
	return &Config{
		LogLevel:     "info",
		LogFormat:    "json",
		OutputFormat: "human",
		Database: DatabaseConfig{
			Engine:       "postgres",
			MaxOpenConns: 10,
			MaxIdleConns: 3,
		},
		Backup: BackupConfig{
			StorageBackend: "fs",
			BaseDir:        "/var/lib/toris/backups",
			Retention: RetentionConfig{
				MinCount:   3,
				MaxAgeDays: 30,
				KeepFailed: true,
			},
		},
		Restore: RestoreConfig{
			TempDir:           "/var/lib/toris/restore-tmp",
			StartAfterRestore: true,
		},
		Leader: LeaderConfig{
			LeaseTTL:             30 * time.Second,
			RenewInterval:        10 * time.Second,
			AcquireRetryInterval: 5 * time.Second,
		},
		Failover: FailoverConfig{
			Enabled:                    false,
			UnhealthyThreshold:         60 * time.Second,
			MaxReplicationLagBytes:     64 * 1024 * 1024,
			ReplicationOutageThreshold: 5 * time.Minute,
			AutoRewindAfterFailover:    true,
		},
		Proxy: ProxyConfig{
			Enabled:     true,
			ListenAddr:  "127.0.0.1:5433",
			DialTimeout: 5 * time.Second,
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			ListenAddr: ":9100",
		},
		Timeouts: TimeoutConfig{
			Connect:  5 * time.Second,
			Query:    10 * time.Second,
			Backup:   6 * time.Hour,
			Restore:  6 * time.Hour,
			Failover: 5 * time.Minute,
			ToolExec: 30 * time.Minute,
		},
		TLS: TLSConfig{
			Mode: "require",
		},
	}
}

// findConfigFile searches standard locations for toris.yaml.
func findConfigFile() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"toris.yaml",
		"toris.yml",
	}
	if home != "" {
		candidates = append(candidates,
			home+"/.toris/toris.yaml",
			home+"/.toris/toris.yml",
		)
	}
	candidates = append(candidates,
		"/etc/toris/toris.yaml",
		"/etc/toris/toris.yml",
	)
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// decodeYAML merges a YAML stream into an existing *Config.
// Only fields present in the YAML override the struct; absent fields keep defaults.
func decodeYAML(r io.Reader, cfg *Config) error {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(false) // don't error on unknown keys (forward compat)
	return dec.Decode(cfg)
}

// applyEnv reads TORIS_* environment variables and writes them into cfg.
// Mapping: TORIS_LOG_LEVEL → cfg.LogLevel, TORIS_CLUSTER_ID → cfg.Cluster.ID, etc.
func applyEnv(cfg *Config) {
	set := func(dest *string, key string) {
		if v := os.Getenv("TORIS_" + key); v != "" {
			*dest = v
		}
	}
	setBool := func(dest *bool, key string) {
		if v := os.Getenv("TORIS_" + key); v != "" {
			*dest = v == "true" || v == "1" || v == "yes"
		}
	}
	setDur := func(dest *time.Duration, key string) {
		if v := os.Getenv("TORIS_" + key); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				*dest = d
			}
		}
	}
	setInt := func(dest *int64, key string) {
		if v := os.Getenv("TORIS_" + key); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				*dest = n
			}
		}
	}

	set(&cfg.InstanceID, "INSTANCE_ID")
	set(&cfg.LogLevel, "LOG_LEVEL")
	set(&cfg.LogFormat, "LOG_FORMAT")
	set(&cfg.OutputFormat, "OUTPUT_FORMAT")
	set(&cfg.ControlDSN, "CONTROL_DSN")
	set(&cfg.Cluster.ID, "CLUSTER_ID")
	set(&cfg.Cluster.Name, "CLUSTER_NAME")
	set(&cfg.Database.Engine, "DATABASE_ENGINE")
	set(&cfg.Backup.StorageBackend, "BACKUP_STORAGE_BACKEND")
	set(&cfg.Backup.BaseDir, "BACKUP_BASE_DIR")
	setBool(&cfg.Backup.OffSiteRequired, "BACKUP_OFFSITE_REQUIRED")
	setBool(&cfg.Backup.RunRehearsalOnCreate, "BACKUP_RUN_REHEARSAL_ON_CREATE")
	setBool(&cfg.Failover.Enabled, "FAILOVER_ENABLED")
	setDur(&cfg.Failover.UnhealthyThreshold, "FAILOVER_UNHEALTHY_THRESHOLD")
	setInt(&cfg.Failover.MaxReplicationLagBytes, "FAILOVER_MAX_REPLICATION_LAG_BYTES")
	setDur(&cfg.Leader.LeaseTTL, "LEADER_LEASE_TTL")
	setDur(&cfg.Leader.RenewInterval, "LEADER_RENEW_INTERVAL")
	setBool(&cfg.Proxy.Enabled, "PROXY_ENABLED")
	set(&cfg.Proxy.ListenAddr, "PROXY_LISTEN_ADDR")
	setBool(&cfg.Metrics.Enabled, "METRICS_ENABLED")
	set(&cfg.Metrics.ListenAddr, "METRICS_LISTEN_ADDR")
	set(&cfg.TLS.Mode, "TLS_MODE")
	set(&cfg.TLS.CAFile, "TLS_CA_FILE")
	set(&cfg.TLS.CertFile, "TLS_CERT_FILE")
	set(&cfg.TLS.KeyFile, "TLS_KEY_FILE")
}

// applyFlagOverride applies a single dotted-key CLI override to the config.
// Supported keys are a subset of the most commonly overridden fields.
func applyFlagOverride(cfg *Config, key string, val any) error {
	s, _ := val.(string)
	switch key {
	case "log_level":
		cfg.LogLevel = s
	case "log_format":
		cfg.LogFormat = s
	case "output_format":
		cfg.OutputFormat = s
	case "control_dsn":
		cfg.ControlDSN = s
	case "cluster.id":
		cfg.Cluster.ID = s
	case "failover.enabled":
		cfg.Failover.Enabled = s == "true" || s == "1"
	default:
		// Unknown override keys are silently ignored to allow forward compatibility.
		// Use Validate() to catch real config problems.
		_ = reflect.TypeOf(cfg) // keep import used
	}
	return nil
}
