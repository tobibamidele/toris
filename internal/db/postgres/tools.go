// Package postgres - tools.go
// Wraps official pg_* tools safely using internal/exec.
// Every wrapper: checks tool availability, captures stdout/stderr,
// enforces timeouts, and returns structured errors.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	torexec "github.com/tobibamidele/toris/internal/exec"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// Tools holds resolved paths to required PostgreSQL utilities.
type Tools struct {
	PgIsReady      string
	PgBaseBackup   string
	PgVerifyBackup string
	PgRewind       string
	PgCtl          string
}

// CheckTools verifies all required pg_* tools exist and returns their paths.
func CheckTools(ctx context.Context) (*Tools, error) {
	required := []string{"pg_isready", "pg_basebackup", "pg_verifybackup", "pg_rewind", "pg_ctl"}
	paths := make(map[string]string, len(required))
	var missing []string

	for _, name := range required {
		path, err := torexec.Which(name)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		paths[name] = path
	}

	if len(missing) > 0 {
		return nil, torerrors.Newf(torerrors.CodeToolNotFound,
			"missing PostgreSQL tools: %s\n"+
				"Install postgresql-client (or ensure they are in PATH) before running toris.",
			strings.Join(missing, ", "))
	}

	return &Tools{
		PgIsReady:      paths["pg_isready"],
		PgBaseBackup:   paths["pg_basebackup"],
		PgVerifyBackup: paths["pg_verifybackup"],
		PgRewind:       paths["pg_rewind"],
		PgCtl:          paths["pg_ctl"],
	}, nil
}

// ─── pg_isready ───────────────────────────────────────────────────────────────

// IsReadyResult holds the outcome of a pg_isready probe.
type IsReadyResult struct {
	Ready    bool
	Message  string
	Duration time.Duration
}

// PgIsReady runs pg_isready against a node.
// pg_isready exit codes: 0=accepting, 1=rejecting, 2=no response, 3=no args.
func PgIsReady(ctx context.Context, tools *Tools, node *model.Node, timeout time.Duration) (*IsReadyResult, error) {
	cmd := torexec.Cmd{
		Binary: tools.PgIsReady,
		Args: []string{
			"-h", node.Host,
			"-p", fmt.Sprintf("%d", node.Port),
			"-t", fmt.Sprintf("%d", int(timeout.Seconds())),
		},
		Timeout: timeout + 2*time.Second,
	}

	res, err := torexec.Run(ctx, cmd)
	result := &IsReadyResult{Duration: res.Duration}

	if err != nil {
		// Exit code 1 = server rejecting connections (exists but not ready)
		// Exit code 2 = no response
		if res.ExitCode == 1 || res.ExitCode == 2 {
			result.Ready = false
			result.Message = res.Stdout
			return result, nil
		}
		return result, torerrors.Wrapf(torerrors.CodeDBNotReady, err,
			"pg_isready failed for node %s (%s)", node.ID, node.Addr())
	}

	result.Ready = true
	result.Message = res.Stdout
	return result, nil
}

// ─── pg_basebackup ───────────────────────────────────────────────────────────

// BaseBackupOptions configures a pg_basebackup invocation.
type BaseBackupOptions struct {
	// DestDir is where the backup will be written.
	DestDir string
	// Format: "plain" or "tar". Use "tar" for streaming.
	Format string
	// Compress: 0–9 (0 = no compression, 9 = max). Only for tar format.
	Compress int
	// WALMethod: "stream" or "fetch".
	WALMethod string
	// Checkpoint: "fast" or "spread".
	Checkpoint string
	// Label is embedded in the backup label file.
	Label string
	// Timeout for the entire backup operation.
	Timeout time.Duration
	// ReplicationUser for the backup connection.
	ReplicationUser string
	// Password — redacted from logs.
	Password string
}

// PgBaseBackup runs pg_basebackup.
// Returns the Result for stdout/stderr inspection by the caller.
func PgBaseBackup(ctx context.Context, log *logging.Logger, tools *Tools, node *model.Node, opts BaseBackupOptions) (*torexec.Result, error) {
	if opts.DestDir == "" {
		return nil, torerrors.New(torerrors.CodeBackupFailed, "DestDir must not be empty")
	}
	format := opts.Format
	if format == "" {
		format = "tar"
	}
	walMethod := opts.WALMethod
	if walMethod == "" {
		walMethod = "stream"
	}
	checkpoint := opts.Checkpoint
	if checkpoint == "" {
		checkpoint = "fast"
	}

	args := []string{
		"-h", node.Host,
		"-p", fmt.Sprintf("%d", node.Port),
		"-D", opts.DestDir,
		"--format=" + format,
		"--wal-method=" + walMethod,
		"--checkpoint=" + checkpoint,
		"--progress",
		"--verbose",
	}
	if opts.Label != "" {
		args = append(args, "--label="+opts.Label)
	}
	if format == "tar" && opts.Compress > 0 {
		args = append(args, fmt.Sprintf("--compress=%d", opts.Compress))
	}
	if opts.ReplicationUser != "" {
		args = append(args, "-U", opts.ReplicationUser)
	}

	var redact []string
	var env []string
	if opts.Password != "" {
		env = append(env, "PGPASSWORD="+opts.Password)
		redact = append(redact, opts.Password)
	}

	cmd := torexec.Cmd{
		Binary:     tools.PgBaseBackup,
		Args:       args,
		Env:        env,
		RedactArgs: redact,
		Timeout:    opts.Timeout,
	}

	log.Info("starting pg_basebackup",
		"node", node.Addr(),
		"dest", opts.DestDir,
		"format", format,
	)

	res, err := torexec.Run(ctx, cmd)
	if err != nil {
		return res, torerrors.Wrapf(torerrors.CodeBackupFailed, err,
			"pg_basebackup failed for node %s", node.ID)
	}

	log.Info("pg_basebackup complete",
		"node", node.Addr(),
		"duration", res.Duration,
		"dest", opts.DestDir,
	)
	return res, nil
}

// ─── pg_verifybackup ─────────────────────────────────────────────────────────

// PgVerifyBackup runs pg_verifybackup against a backup directory.
func PgVerifyBackup(ctx context.Context, log *logging.Logger, tools *Tools, backupDir string, timeout time.Duration) (*torexec.Result, error) {
	cmd := torexec.Cmd{
		Binary:  tools.PgVerifyBackup,
		Args:    []string{backupDir},
		Timeout: timeout,
	}

	log.Info("starting pg_verifybackup", "backup_dir", backupDir)
	res, err := torexec.Run(ctx, cmd)
	if err != nil {
		return res, torerrors.Wrapf(torerrors.CodeBackupVerifyFail, err,
			"pg_verifybackup failed for backup at %s: %s", backupDir, res.Stderr)
	}
	log.Info("pg_verifybackup passed", "backup_dir", backupDir, "duration", res.Duration)
	return res, nil
}

// ─── pg_rewind ────────────────────────────────────────────────────────────────

// PgRewindOptions configures pg_rewind for rejoining a diverged old primary.
type PgRewindOptions struct {
	// TargetDataDir is the old primary's data directory to rewind.
	TargetDataDir string
	// SourceDSN is the connection string to the new primary.
	// Password is injected via PGPASSWORD, not embedded in the DSN.
	SourceDSN string
	Password  string
	Timeout   time.Duration
	DryRun    bool
}

// PgRewind runs pg_rewind to synchronize an old primary with the new primary.
func PgRewind(ctx context.Context, log *logging.Logger, tools *Tools, opts PgRewindOptions) (*torexec.Result, error) {
	if opts.TargetDataDir == "" {
		return nil, torerrors.New(torerrors.CodeRestoreFailed, "TargetDataDir must not be empty")
	}
	if opts.SourceDSN == "" {
		return nil, torerrors.New(torerrors.CodeRestoreFailed, "SourceDSN must not be empty")
	}

	args := []string{
		"--target-pgdata=" + opts.TargetDataDir,
		"--source-server=" + opts.SourceDSN,
		"--progress",
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}

	var env []string
	var redact []string
	if opts.Password != "" {
		env = append(env, "PGPASSWORD="+opts.Password)
		redact = append(redact, opts.Password)
	}

	cmd := torexec.Cmd{
		Binary:     tools.PgRewind,
		Args:       args,
		Env:        env,
		RedactArgs: redact,
		Timeout:    opts.Timeout,
	}

	log.Info("running pg_rewind",
		"target_data_dir", opts.TargetDataDir,
		"dry_run", opts.DryRun,
	)

	res, err := torexec.Run(ctx, cmd)
	if err != nil {
		return res, torerrors.Wrapf(torerrors.CodeRestoreFailed, err,
			"pg_rewind failed: %s", res.Stderr)
	}
	log.Info("pg_rewind complete", "duration", res.Duration)
	return res, nil
}
