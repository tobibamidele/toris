package exec_test

import (
	"context"
	"strings"
	"testing"
	"time"

	torexec "github.com/tobibamidele/toris/internal/exec"
)

func TestRun_SuccessfulCommand(t *testing.T) {
	ctx := context.Background()
	res, err := torexec.Run(ctx, torexec.Cmd{
		Binary: "echo",
		Args:   []string{"hello"},
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %q", res.Stdout)
	}
}

func TestRun_FailingCommand(t *testing.T) {
	ctx := context.Background()
	res, err := torexec.Run(ctx, torexec.Cmd{
		Binary: "false", // exits with code 1
	})
	if err == nil {
		t.Fatal("expected error for failing command, got nil")
	}
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit code")
	}
}

func TestRun_CapturesStdout(t *testing.T) {
	ctx := context.Background()
	res, err := torexec.Run(ctx, torexec.Cmd{
		Binary: "printf",
		Args:   []string{"line1\nline2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Stdout, "line1") {
		t.Errorf("expected stdout to contain 'line1', got %q", res.Stdout)
	}
}

func TestRun_CapturesStderr(t *testing.T) {
	ctx := context.Background()
	res, err := torexec.Run(ctx, torexec.Cmd{
		Binary: "bash",
		Args:   []string{"-c", "echo 'stderr output' >&2; exit 1"},
	})
	if err == nil {
		t.Fatal("expected error from failing script")
	}
	if !strings.Contains(res.Stderr, "stderr output") {
		t.Errorf("expected stderr to contain 'stderr output', got %q", res.Stderr)
	}
}

func TestRun_Timeout(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	_, err := torexec.Run(ctx, torexec.Cmd{
		Binary:  "sleep",
		Args:    []string{"10"},
		Timeout: 100 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should have been killed well before 10s.
	if elapsed > 2*time.Second {
		t.Errorf("timeout did not trigger fast enough: elapsed %s", elapsed)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := torexec.Run(ctx, torexec.Cmd{
		Binary: "sleep",
		Args:   []string{"10"},
	})
	if err == nil {
		t.Fatal("expected error after context cancellation")
	}
}

func TestRun_EmptyBinary(t *testing.T) {
	_, err := torexec.Run(context.Background(), torexec.Cmd{Binary: ""})
	if err == nil {
		t.Fatal("expected error for empty binary")
	}
}

func TestRun_NonExistentBinary(t *testing.T) {
	_, err := torexec.Run(context.Background(), torexec.Cmd{
		Binary: "/definitely/not/a/real/binary",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}

func TestRun_MeasuresDuration(t *testing.T) {
	ctx := context.Background()
	res, _ := torexec.Run(ctx, torexec.Cmd{
		Binary: "echo",
		Args:   []string{"hi"},
	})
	if res.Duration <= 0 {
		t.Error("duration should be positive")
	}
}

func TestWhich_ExistingBinary(t *testing.T) {
	path, err := torexec.Which("echo")
	if err != nil {
		t.Fatalf("expected 'echo' to be found in PATH, got: %v", err)
	}
	if path == "" {
		t.Error("path should not be empty")
	}
}

func TestWhich_MissingBinary(t *testing.T) {
	_, err := torexec.Which("definitely_not_a_real_binary_xyz")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRequireTools_AllPresent(t *testing.T) {
	err := torexec.RequireTools("echo", "true", "false")
	if err != nil {
		t.Errorf("expected no error for present tools, got: %v", err)
	}
}

func TestRequireTools_SomeMissing(t *testing.T) {
	err := torexec.RequireTools("echo", "definitely_missing_tool_abc123")
	if err == nil {
		t.Fatal("expected error when some tools are missing")
	}
	if !strings.Contains(err.Error(), "definitely_missing_tool_abc123") {
		t.Errorf("error should name the missing tool, got: %v", err)
	}
}

func TestCmd_SafeArgs_RedactsSecrets(t *testing.T) {
	cmd := torexec.Cmd{
		Binary:     "pg_basebackup",
		Args:       []string{"-h", "localhost", "-U", "replicator", "--password=supersecret123"},
		RedactArgs: []string{"supersecret123"},
	}
	safe := cmd.SafeArgs()
	for _, a := range safe {
		if strings.Contains(a, "supersecret123") {
			t.Errorf("secret should be redacted, but found in safe args: %q", a)
		}
	}
	if !strings.Contains(strings.Join(safe, " "), "***") {
		t.Error("redacted value should be replaced with ***")
	}
}

func TestCmd_String_ContainsBinary(t *testing.T) {
	cmd := torexec.Cmd{
		Binary: "pg_isready",
		Args:   []string{"-h", "localhost", "-p", "5432"},
	}
	s := cmd.String()
	if !strings.Contains(s, "pg_isready") {
		t.Errorf("String() should include binary name, got: %q", s)
	}
}
