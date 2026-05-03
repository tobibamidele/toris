// Package exec provides a safe, testable wrapper around os/exec.
// Every external process call goes through this package — never raw exec.Command.
//
// Design rules:
//   - All calls require a context (for cancellation and timeout).
//   - stdout and stderr are always captured.
//   - Exit codes are surfaced as typed errors.
//   - Arguments are never interpolated through shell.
//   - Secrets must never appear in logged command lines.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	torerrors "github.com/tobibamidele/toris/internal/errors"
)

// Result holds the output of a completed process.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Cmd configures a single external process invocation.
type Cmd struct {
	// Binary is the full path or name of the executable.
	Binary string
	// Args are the command-line arguments (no shell expansion).
	Args []string
	// Env is appended to the current process environment.
	// Use "KEY=VALUE" format.
	Env []string
	// Dir sets the working directory. Empty = inherited.
	Dir string
	// Stdin is optional input piped to the process.
	Stdin string
	// RedactArgs lists argument values that should be replaced with "***" in logs.
	// This prevents DSNs, passwords, and keys from appearing in structured logs.
	RedactArgs []string
	// Timeout overrides the context deadline if positive.
	Timeout time.Duration
}

// Run executes the command and waits for it to complete.
// It returns a Result on success (exit code 0) and a typed error otherwise.
func Run(ctx context.Context, cmd Cmd) (*Result, error) {
	if cmd.Binary == "" {
		return nil, torerrors.New(torerrors.CodeToolFailed, "exec.Cmd.Binary must not be empty")
	}

	// Resolve binary to an absolute path via LookPath before executing.
	// This satisfies gosec G204: the binary is a verified filesystem path,
	// not a raw caller-supplied string passed directly to the shell.
	resolvedBinary, err := exec.LookPath(cmd.Binary)
	if err != nil {
		return nil, torerrors.Newf(torerrors.CodeToolNotFound,
			"binary %q not found in PATH: %v", cmd.Binary, err)
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if cmd.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	// G204: binary is resolved via LookPath above — not tainted input.
	c := exec.CommandContext(runCtx, resolvedBinary, cmd.Args...) //nolint:gosec

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if cmd.Stdin != "" {
		c.Stdin = strings.NewReader(cmd.Stdin)
	}
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if len(cmd.Env) > 0 {
		c.Env = append(c.Environ(), cmd.Env...)
	}

	start := time.Now()
	err = c.Run()
	dur := time.Since(start)

	res := &Result{
		ExitCode: 0,
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		Duration: dur,
	}

	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return res, torerrors.Wrapf(torerrors.CodeTimeout, err,
				"command %q timed out after %s", cmd.Binary, cmd.Timeout)
		}
		if runCtx.Err() == context.Canceled {
			return res, torerrors.Wrapf(torerrors.CodeCanceled, err,
				"command %q was canceled", cmd.Binary)
		}

		exitErr, ok := err.(*exec.ExitError)
		if ok {
			res.ExitCode = exitErr.ExitCode()
		}

		return res, torerrors.Wrapf(torerrors.CodeToolFailed, err,
			"command %q exited with code %d: %s",
			cmd.Binary, res.ExitCode, truncate(res.Stderr, 512))
	}

	return res, nil
}

// Which checks whether a binary is available in PATH.
// Returns the resolved path or an error.
func Which(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", torerrors.Newf(torerrors.CodeToolNotFound,
			"required tool %q not found in PATH; please install it before running this command", name)
	}
	return path, nil
}

// RequireTools verifies that all named tools exist in PATH.
// Returns a single error listing all missing tools if any are absent.
func RequireTools(names ...string) error {
	var missing []string
	for _, name := range names {
		if _, err := Which(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return torerrors.Newf(torerrors.CodeToolNotFound,
			"missing required tools: %s", strings.Join(missing, ", "))
	}
	return nil
}

// redact replaces secret values in an args slice with "***".
func redact(args, secrets []string) []string {
	if len(secrets) == 0 {
		return args
	}
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		for _, s := range secrets {
			if s != "" && strings.Contains(a, s) {
				out[i] = strings.ReplaceAll(a, s, "***")
			}
		}
	}
	return out
}

// SafeArgs returns args with secret values redacted, for use in logs.
func (c *Cmd) SafeArgs() []string {
	return redact(c.Args, c.RedactArgs)
}

// String returns a loggable representation of the command with secrets redacted.
func (c *Cmd) String() string {
	return c.Binary + " " + strings.Join(c.SafeArgs(), " ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

// ─── Version detection ───────────────────────────────────────────────────────

// PostgresVersion queries the version string of the postgres tool at path.
func PostgresVersion(ctx context.Context, toolPath string) (string, error) {
	res, err := Run(ctx, Cmd{
		Binary:  toolPath,
		Args:    []string{"--version"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("getting version of %s: %w", toolPath, err)
	}
	return res.Stdout, nil
}
