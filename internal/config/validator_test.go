package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/config"
)

// validConfig returns a minimal valid *Config for test mutation.
func validConfig() *config.Config {
	return &config.Config{
		InstanceID:   "test-instance",
		LogLevel:     "info",
		LogFormat:    "json",
		OutputFormat: "human",
		ControlDSN:   "host=localhost port=5432 user=toris dbname=toris_control sslmode=require",
		Cluster: config.ClusterConfig{
			ID:   "pg-test",
			Name: "Test Cluster",
			Nodes: []config.NodeConfig{
				{ID: "node-01", Host: "localhost", Port: 5432},
			},
		},
		Database: config.DatabaseConfig{
			Engine:       "postgres",
			MaxOpenConns: 5,
			MaxIdleConns: 2,
		},
		Backup: config.BackupConfig{
			StorageBackend: "fs",
			BaseDir:        "/tmp/toris-test-backups",
			Retention: config.RetentionConfig{
				MinCount:   1,
				MaxAgeDays: 7,
				KeepFailed: true,
			},
		},
		Restore: config.RestoreConfig{
			TempDir: "/tmp/toris-test-restore",
		},
		Leader: config.LeaderConfig{
			LeaseTTL:             30 * time.Second,
			RenewInterval:        10 * time.Second,
			AcquireRetryInterval: 5 * time.Second,
		},
		Failover: config.FailoverConfig{
			Enabled:            false,
			UnhealthyThreshold: 60 * time.Second,
		},
		Proxy: config.ProxyConfig{
			Enabled:     true,
			ListenAddr:  "127.0.0.1:5433",
			DialTimeout: 5 * time.Second,
		},
		Metrics: config.MetricsConfig{
			Enabled:    true,
			ListenAddr: ":9100",
		},
		TLS: config.TLSConfig{Mode: "require"},
		Auth: config.AuthConfig{
			Profiles: []config.AuthProfile{
				{Name: "default", Username: "toris", Password: "secret"},
			},
		},
		Timeouts: config.TimeoutConfig{
			Connect: 5 * time.Second,
			Query:   10 * time.Second,
		},
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := validConfig()
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidate_MissingInstanceID(t *testing.T) {
	cfg := validConfig()
	cfg.InstanceID = ""
	assertValidationProblem(t, cfg, "instance_id")
}

func TestValidate_MissingClusterID(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ID = ""
	assertValidationProblem(t, cfg, "cluster.id")
}

func TestValidate_EmptyNodeList(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes = nil
	assertValidationProblem(t, cfg, "cluster.nodes")
}

func TestValidate_DuplicateNodeIDs(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes = []config.NodeConfig{
		{ID: "dup", Host: "host1", Port: 5432},
		{ID: "dup", Host: "host2", Port: 5432},
	}
	assertValidationProblem(t, cfg, "duplicate")
}

func TestValidate_InvalidNodePort(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Nodes[0].Port = 99999
	assertValidationProblem(t, cfg, "port")
}

func TestValidate_UnsupportedDatabaseEngine(t *testing.T) {
	cfg := validConfig()
	cfg.Database.Engine = "mysql"
	assertValidationProblem(t, cfg, "mysql")
}

func TestValidate_MissingControlDSN(t *testing.T) {
	cfg := validConfig()
	cfg.ControlDSN = ""
	assertValidationProblem(t, cfg, "control_dsn")
}

func TestValidate_InvalidStorageBackend(t *testing.T) {
	cfg := validConfig()
	cfg.Backup.StorageBackend = "gcs"
	assertValidationProblem(t, cfg, "gcs")
}

func TestValidate_S3BackendMissingBucket(t *testing.T) {
	cfg := validConfig()
	cfg.Backup.StorageBackend = "s3"
	cfg.Backup.S3.Bucket = ""
	cfg.Backup.S3.Region = "us-east-1"
	assertValidationProblem(t, cfg, "bucket")
}

func TestValidate_RenewIntervalExceedsLeaseTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Leader.LeaseTTL = 10 * time.Second
	cfg.Leader.RenewInterval = 15 * time.Second
	assertValidationProblem(t, cfg, "renew_interval")
}

func TestValidate_ZeroLeaseTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Leader.LeaseTTL = 0
	assertValidationProblem(t, cfg, "lease_ttl")
}

func TestValidate_InvalidTLSMode(t *testing.T) {
	cfg := validConfig()
	cfg.TLS.Mode = "insecure"
	assertValidationProblem(t, cfg, "insecure")
}

func TestValidate_VerifyFullRequiresCAFile(t *testing.T) {
	cfg := validConfig()
	cfg.TLS.Mode = "verify-full"
	cfg.TLS.CAFile = ""
	assertValidationProblem(t, cfg, "ca_file")
}

func TestValidate_TLSCertFileReadable(t *testing.T) {
	cfg := validConfig()
	cfg.TLS.CertFile = "/nonexistent/path/cert.pem"
	assertValidationProblem(t, cfg, "cert_file")
}

func TestValidate_KeyFileTooPermissive(t *testing.T) {
	// Create a temp key file with bad permissions.
	f, err := os.CreateTemp(t.TempDir(), "key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := validConfig()
	cfg.TLS.CertFile = f.Name()
	cfg.TLS.KeyFile = f.Name()

	assertValidationProblem(t, cfg, "permissions")
}

func TestValidate_AuthProfileMissingUsername(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.Profiles[0].Username = ""
	assertValidationProblem(t, cfg, "username")
}

func TestValidate_AuthProfileNoPassword(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.Profiles[0].Password = ""
	cfg.Auth.Profiles[0].PasswordFile = ""
	assertValidationProblem(t, cfg, "password")
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := validConfig()
	cfg.LogLevel = "verbose"
	assertValidationProblem(t, cfg, "log_level")
}

func TestValidate_InvalidOutputFormat(t *testing.T) {
	cfg := validConfig()
	cfg.OutputFormat = "xml"
	assertValidationProblem(t, cfg, "output_format")
}

func TestValidate_MultipleProblems(t *testing.T) {
	cfg := validConfig()
	cfg.InstanceID = ""
	cfg.Cluster.ID = ""
	cfg.ControlDSN = ""

	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected errors, got nil")
	}
	ve, ok := err.(*config.ValidationError)
	if !ok {
		t.Fatalf("expected *config.ValidationError, got %T", err)
	}
	if len(ve.Problems) < 3 {
		t.Errorf("expected at least 3 problems, got %d: %v", len(ve.Problems), ve.Problems)
	}
}

func TestValidate_ProxyEnabledRequiresListenAddr(t *testing.T) {
	cfg := validConfig()
	cfg.Proxy.Enabled = true
	cfg.Proxy.ListenAddr = ""
	assertValidationProblem(t, cfg, "listen_addr")
}

func TestValidate_ZeroConnectTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Timeouts.Connect = 0
	assertValidationProblem(t, cfg, "timeouts.connect")
}

// assertValidationProblem calls Validate and asserts the error contains substr.
func assertValidationProblem(t *testing.T, cfg *config.Config, substr string) {
	t.Helper()
	err := config.Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", substr)
	}
	if ve, ok := err.(*config.ValidationError); ok {
		for _, p := range ve.Problems {
			if contains(p, substr) {
				return
			}
		}
		t.Fatalf("expected a problem containing %q, got problems: %v", substr, ve.Problems)
	} else {
		if !contains(err.Error(), substr) {
			t.Fatalf("expected error containing %q, got: %v", substr, err)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
