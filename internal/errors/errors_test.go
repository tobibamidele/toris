package errors_test

import (
	"errors"
	"fmt"
	"testing"

	torerrors "github.com/tobibamidele/toris/internal/errors"
)

func TestNew_ErrorString(t *testing.T) {
	err := torerrors.New(torerrors.CodeDBUnreachable, "cannot connect")
	got := err.Error()
	want := "[DB_UNREACHABLE] cannot connect"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNewf_FormattedMessage(t *testing.T) {
	err := torerrors.Newf(torerrors.CodeNotFound, "backup %s not found", "abc123")
	if err.Error() != "[NOT_FOUND] backup abc123 not found" {
		t.Errorf("unexpected error string: %s", err.Error())
	}
}

func TestWrap_NilCauseReturnsNil(t *testing.T) {
	result := torerrors.Wrap(torerrors.CodeInternal, "message", nil)
	if result != nil {
		t.Errorf("Wrap(nil) should return nil, got %v", result)
	}
}

func TestWrap_WithCause(t *testing.T) {
	cause := fmt.Errorf("underlying io error")
	err := torerrors.Wrap(torerrors.CodeStorageFailed, "writing backup", cause)
	if err == nil {
		t.Fatal("Wrap should return non-nil when cause is non-nil")
	}
	if !errors.Is(err, cause) {
		t.Error("wrapped error should satisfy errors.Is for the cause")
	}
	if err.Code != torerrors.CodeStorageFailed {
		t.Errorf("expected code %s, got %s", torerrors.CodeStorageFailed, err.Code)
	}
}

func TestGetCode_TorisError(t *testing.T) {
	err := torerrors.New(torerrors.CodeLeaseConflict, "lease held by other")
	code := torerrors.GetCode(err)
	if code != torerrors.CodeLeaseConflict {
		t.Errorf("expected %s, got %s", torerrors.CodeLeaseConflict, code)
	}
}

func TestGetCode_StdlibError(t *testing.T) {
	err := fmt.Errorf("plain stdlib error")
	code := torerrors.GetCode(err)
	if code != torerrors.CodeInternal {
		t.Errorf("expected CodeInternal for stdlib error, got %s", code)
	}
}

func TestIs_MatchesCode(t *testing.T) {
	err := torerrors.New(torerrors.CodeTimeout, "operation timed out")
	if !torerrors.Is(err, torerrors.CodeTimeout) {
		t.Error("Is should match on the error's own code")
	}
	if torerrors.Is(err, torerrors.CodeNotFound) {
		t.Error("Is should not match on a different code")
	}
}

func TestIs_WrappedError(t *testing.T) {
	inner := torerrors.New(torerrors.CodeFencingViolation, "stale token")
	outer := fmt.Errorf("operation failed: %w", inner)
	if !torerrors.Is(outer, torerrors.CodeFencingViolation) {
		t.Error("Is should match even when the toris error is wrapped by a stdlib error")
	}
}

func TestIsTimeout(t *testing.T) {
	err := torerrors.New(torerrors.CodeTimeout, "timed out")
	if !torerrors.IsTimeout(err) {
		t.Error("IsTimeout should return true for CodeTimeout error")
	}
	other := torerrors.New(torerrors.CodeNotFound, "not found")
	if torerrors.IsTimeout(other) {
		t.Error("IsTimeout should return false for non-timeout error")
	}
}

func TestIsNotFound(t *testing.T) {
	err := torerrors.New(torerrors.CodeNotFound, "record missing")
	if !torerrors.IsNotFound(err) {
		t.Error("IsNotFound should return true for CodeNotFound error")
	}
}

func TestIsFencingViolation(t *testing.T) {
	err := torerrors.New(torerrors.CodeFencingViolation, "stale generation")
	if !torerrors.IsFencingViolation(err) {
		t.Error("IsFencingViolation should return true for CodeFencingViolation error")
	}
}

func TestUnwrap_Chain(t *testing.T) {
	root := fmt.Errorf("root cause")
	mid := torerrors.Wrap(torerrors.CodeStorageFailed, "storage layer", root)
	top := torerrors.Wrap(torerrors.CodeBackupFailed, "backup layer", mid)

	if !errors.Is(top, root) {
		t.Error("errors.Is should traverse the full chain to find root cause")
	}
}

func TestError_NilCode(t *testing.T) {
	// Creating an error with an empty code should still work.
	err := torerrors.New("", "no code error")
	if err.Error() == "" {
		t.Error("Error() should not return empty string")
	}
}

func TestWrapf_FormattedContext(t *testing.T) {
	cause := fmt.Errorf("disk full")
	err := torerrors.Wrapf(torerrors.CodeStorageFull, cause, "writing artifact %s at offset %d", "base.tar", 1024)
	if err == nil {
		t.Fatal("Wrapf should return non-nil")
	}
	if err.Message != "writing artifact base.tar at offset 1024" {
		t.Errorf("unexpected message: %s", err.Message)
	}
	if !errors.Is(err, cause) {
		t.Error("Wrapf should preserve the cause chain")
	}
}
