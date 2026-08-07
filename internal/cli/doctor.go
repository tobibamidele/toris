package cli

// doctor.go — expanded 'toris doctor' implementation for v0.4.0.
//
// v0.3 checks: pg_* tools, backup dir writability, control DSN configured.
// v0.4 adds:
//   - Connect to control DB and verify schema presence
//   - Check lease state (stale, missing, expired)
//   - Report nodes not seen in N minutes
//   - Warn if no verified backup within the retention window
//   - Report overall pass/fail with exit code 3 on any failure

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tobibamidele/toris/internal/backup"
	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"

	pgback "github.com/tobibamidele/toris/internal/db/postgres"
)

// DoctorResult holds the outcome of a single preflight check.
type DoctorResult struct {
	Name    string
	OK      bool
	Message string
	Warning bool // true = warn but don't fail
}

// RunDoctor executes all preflight checks and returns the results.
// It does NOT exit — the caller decides exit code.
func RunDoctor(cfg interface {
	GetControlDSN() string
	GetBackupBaseDir() string
	GetClusterID() string
	GetInstanceID() string
	GetRetentionMaxAgeDays() int
	GetRetentionMinCount() int
	GetNodes() []NodeInfo
	GetLeaseTTL() time.Duration
	GetRenewInterval() time.Duration
}, log *logging.Logger) []DoctorResult {
	var results []DoctorResult
	add := func(r DoctorResult) { results = append(results, r) }

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Check 1: pg_* tools ──────────────────────────────────────────────
	_, toolErr := pgback.CheckTools(ctx)
	if toolErr != nil {
		add(DoctorResult{
			Name:    "pg_* tools",
			OK:      false,
			Message: toolErr.Error(),
		})
	} else {
		add(DoctorResult{
			Name:    "pg_* tools",
			OK:      true,
			Message: "pg_isready, pg_basebackup, pg_verifybackup, pg_rewind, pg_ctl found in PATH",
		})
	}

	// ── Check 2: backup dir writable ─────────────────────────────────────
	baseDir := cfg.GetBackupBaseDir()
	if baseDir == "" {
		add(DoctorResult{Name: "backup dir", OK: false, Message: "backup.base_dir is not configured"})
	} else {
		probe, err := os.CreateTemp(baseDir, ".toris_doctor")
		if err != nil {
			add(DoctorResult{
				Name:    "backup dir",
				OK:      false,
				Message: fmt.Sprintf("cannot write to %s: %v", baseDir, err),
			})
		} else {
			probe.Close()
			os.Remove(probe.Name())
			add(DoctorResult{
				Name:    "backup dir",
				OK:      true,
				Message: fmt.Sprintf("%s is writable", baseDir),
			})
		}
	}

	// ── Check 3: control DSN configured ──────────────────────────────────
	controlDSN := cfg.GetControlDSN()
	if controlDSN == "" {
		add(DoctorResult{
			Name:    "control DSN",
			OK:      false,
			Message: "control_dsn is not set in config",
		})
		// Can't do DB checks without a DSN — return early.
		return results
	}
	add(DoctorResult{Name: "control DSN", OK: true, Message: "control_dsn is configured"})

	// ── Check 4: control DB connectivity ─────────────────────────────────
	pool, poolErr := connectControlDB(ctx, controlDSN)
	if poolErr != nil {
		add(DoctorResult{
			Name:    "control DB connect",
			OK:      false,
			Message: fmt.Sprintf("cannot connect: %v", poolErr),
		})
		// Can't do schema/lease checks.
		return results
	}
	defer pool.Close()
	add(DoctorResult{Name: "control DB connect", OK: true, Message: "connected to control database"})

	// ── Check 5: control DB schema present ───────────────────────────────
	var schemaExists bool
	schemaErr := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = 'toris_control')`).
		Scan(&schemaExists)
	if schemaErr != nil || !schemaExists {
		add(DoctorResult{
			Name:    "control DB schema",
			OK:      false,
			Message: "toris_control schema is missing — run 'toris daemon' once to create it",
		})
	} else {
		// Check for required tables.
		required := []string{"leases", "backups", "audit_events", "nodes"}
		var missing []string
		for _, tbl := range required {
			var exists bool
			pool.QueryRow(ctx,
				`SELECT EXISTS(
					SELECT 1 FROM information_schema.tables
					WHERE table_schema = 'toris_control' AND table_name = $1
				)`, tbl).Scan(&exists)
			if !exists {
				missing = append(missing, tbl)
			}
		}
		if len(missing) > 0 {
			add(DoctorResult{
				Name:    "control DB schema",
				OK:      false,
				Message: fmt.Sprintf("missing tables in toris_control: %s", strings.Join(missing, ", ")),
			})
		} else {
			add(DoctorResult{
				Name:    "control DB schema",
				OK:      true,
				Message: "all required tables present in toris_control",
			})
		}
	}

	// ── Check 6: lease state ──────────────────────────────────────────────
	clusterID := cfg.GetClusterID()
	instanceID := cfg.GetInstanceID()
	lm := leader.New(log, pool, clusterID, instanceID,
		cfg.GetLeaseTTL(), cfg.GetRenewInterval())

	lease, leaseErr := lm.Status(ctx)
	if leaseErr != nil {
		add(DoctorResult{
			Name:    "lease state",
			OK:      true,
			Warning: true,
			Message: "no lease record found — daemon has not been started yet",
		})
	} else {
		now := time.Now()
		if lease.IsExpired(now) {
			add(DoctorResult{
				Name:    "lease state",
				OK:      true,
				Warning: true,
				Message: fmt.Sprintf("lease is EXPIRED (generation=%d, expired=%s ago)",
					lease.Generation,
					now.Sub(lease.ExpiresAt).Round(time.Second)),
			})
		} else if lease.Status == model.LeaseStatusReleased {
			add(DoctorResult{
				Name:    "lease state",
				OK:      true,
				Warning: true,
				Message: fmt.Sprintf("lease is RELEASED (generation=%d)", lease.Generation),
			})
		} else {
			timeToExpiry := lease.ExpiresAt.Sub(now).Round(time.Second)
			add(DoctorResult{
				Name: "lease state",
				OK:   true,
				Message: fmt.Sprintf("active lease held by %s (generation=%d, expires in %s)",
					lease.InstanceID, lease.Generation, timeToExpiry),
			})
		}
	}

	// ── Check 7: stale nodes ─────────────────────────────────────────────
	staleThreshold := 5 * time.Minute
	var staleNodes []string
	var unseenNodes []string

	for _, n := range cfg.GetNodes() {
		var lastSeen *time.Time
		err := pool.QueryRow(ctx, `
			SELECT last_seen_at FROM toris_control.nodes
			WHERE id = $1 AND cluster_id = $2
		`, n.ID, clusterID).Scan(&lastSeen)

		if err != nil || lastSeen == nil {
			unseenNodes = append(unseenNodes, n.ID)
			continue
		}
		since := time.Since(*lastSeen)
		if since > staleThreshold {
			staleNodes = append(staleNodes,
				fmt.Sprintf("%s (last seen %s ago)", n.ID, since.Round(time.Second)))
		}
	}

	if len(unseenNodes) > 0 {
		add(DoctorResult{
			Name:    "node freshness",
			OK:      true,
			Warning: true,
			Message: fmt.Sprintf("nodes not yet in registry (daemon may not have started): %s",
				strings.Join(unseenNodes, ", ")),
		})
	} else if len(staleNodes) > 0 {
		add(DoctorResult{
			Name:    "node freshness",
			OK:      false,
			Message: fmt.Sprintf("stale nodes (not seen in >%s): %s", staleThreshold, strings.Join(staleNodes, "; ")),
		})
	} else {
		add(DoctorResult{
			Name:    "node freshness",
			OK:      true,
			Message: fmt.Sprintf("all %d node(s) seen recently", len(cfg.GetNodes())),
		})
	}

	// ── Check 8: backup freshness ─────────────────────────────────────────
	bs := backup.NewStore(pool)
	freshestAt, bsErr := bs.FreshestVerifiedAt(ctx, clusterID)
	if bsErr != nil {
		add(DoctorResult{
			Name:    "backup freshness",
			OK:      true,
			Warning: true,
			Message: "cannot query backup freshness (schema may not exist yet)",
		})
	} else if freshestAt.Year() <= 1970 {
		add(DoctorResult{
			Name:    "backup freshness",
			OK:      false,
			Message: "no verified backups found — run 'toris backup create'",
		})
	} else {
		age := time.Since(freshestAt).Round(time.Minute)
		maxAge := time.Duration(cfg.GetRetentionMaxAgeDays()) * 24 * time.Hour
		// Warn if last backup is older than half the max age, fail if older than max age.
		if maxAge > 0 && age > maxAge {
			add(DoctorResult{
				Name: "backup freshness",
				OK:   false,
				Message: fmt.Sprintf("most recent verified backup is %s old (retention max=%dd) — create a new backup",
					age, cfg.GetRetentionMaxAgeDays()),
			})
		} else if maxAge > 0 && age > maxAge/2 {
			add(DoctorResult{
				Name:    "backup freshness",
				OK:      true,
				Warning: true,
				Message: fmt.Sprintf("most recent verified backup is %s old — consider creating a new backup soon", age),
			})
		} else {
			add(DoctorResult{
				Name:    "backup freshness",
				OK:      true,
				Message: fmt.Sprintf("most recent verified backup: %s ago", age),
			})
		}
	}

	return results
}

// NodeInfo is a minimal node description used by the doctor.
type NodeInfo struct {
	ID string
}

// PrintDoctorResults renders results to stdout in human or JSON format.
// Returns true if all checks passed (no failures).
func PrintDoctorResults(results []DoctorResult, jsonMode bool) bool {
	if jsonMode {
		printJSON(results)
		allOK := true
		for _, r := range results {
			if !r.OK && !r.Warning {
				allOK = false
			}
		}
		return allOK
	}

	fmt.Println("── toris doctor ──────────────────────────────────────")
	allOK := true
	for _, r := range results {
		icon := "✓"
		if !r.OK {
			icon = "✗"
			allOK = false
		} else if r.Warning {
			icon = "⚠"
		}
		fmt.Printf("  %s %-24s %s\n", icon, r.Name, r.Message)
	}
	fmt.Println("──────────────────────────────────────────────────────")

	if allOK {
		fmt.Println("  All checks passed.")
	} else {
		fmt.Println("  Some checks failed. See output above.")
	}

	return allOK
}
