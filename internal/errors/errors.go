// Package errors defines typed error codes and wrapping helpers for toris.
// All package boundaries should return *Error values so callers can
// programmatically inspect the error class without string matching.
package errors

import (
	"errors"
	"fmt"
)

// Code identifies the class of error.
type Code string

const (
	// Config errors
	CodeConfigInvalid Code = "CONFIG_INVALID"
	CodeConfigMissing Code = "CONFIG_MISSING"

	// Auth errors
	CodeAuthFailed Code = "AUTH_FAILED"
	CodeAuthDenied Code = "AUTH_DENIED"
	CodeTLSError   Code = "TLS_ERROR"

	// DB errors
	CodeDBUnreachable  Code = "DB_UNREACHABLE"
	CodeDBNotReady     Code = "DB_NOT_READY"
	CodeDBQueryFailed  Code = "DB_QUERY_FAILED"
	CodeDBRoleMismatch Code = "DB_ROLE_MISMATCH"

	// Lease errors
	CodeLeaseConflict    Code = "LEASE_CONFLICT"
	CodeLeaseExpired     Code = "LEASE_EXPIRED"
	CodeLeaseNotHeld     Code = "LEASE_NOT_HELD"
	CodeFencingViolation Code = "FENCING_VIOLATION"

	// Backup errors
	CodeBackupFailed     Code = "BACKUP_FAILED"
	CodeBackupVerifyFail Code = "BACKUP_VERIFY_FAILED"
	CodeBackupNotFound   Code = "BACKUP_NOT_FOUND"
	CodeBackupIncomplete Code = "BACKUP_INCOMPLETE"

	// Restore errors
	CodeRestoreFailed  Code = "RESTORE_FAILED"
	CodeRestoreNotSafe Code = "RESTORE_NOT_SAFE"

	// Failover errors
	CodeFailoverUnsafe Code = "FAILOVER_UNSAFE"
	CodeFailoverFailed Code = "FAILOVER_FAILED"
	CodeSplitBrainRisk Code = "SPLIT_BRAIN_RISK"

	// Tool errors
	CodeToolNotFound Code = "TOOL_NOT_FOUND"
	CodeToolFailed   Code = "TOOL_FAILED"

	// Storage errors
	CodeStorageFailed Code = "STORAGE_FAILED"
	CodeStorageFull   Code = "STORAGE_FULL"

	// General errors
	CodeNotFound Code = "NOT_FOUND"
	CodeConflict Code = "CONFLICT"
	CodeTimeout  Code = "TIMEOUT"
	CodeCanceled Code = "CANCELED"
	CodeInternal Code = "INTERNAL"
)

// Error is a toris-typed error carrying a code, a message, and an optional cause.
type Error struct {
	Code    Code
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// New creates a new *Error with no cause.
func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Newf creates a new *Error with a formatted message and no cause.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap wraps an existing error with a toris code and context message.
// If err is nil, Wrap returns nil.
func Wrap(code Code, msg string, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Message: msg, Cause: err}
}

// Wrapf wraps an existing error with a formatted context message.
func Wrapf(code Code, err error, format string, args ...any) *Error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: err}
}

// GetCode extracts the Code from an error if it is (or wraps) a *Error.
// Returns CodeInternal if the error is not a toris error.
func GetCode(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// Is allows errors.Is to match on Code.
func Is(err error, code Code) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

// IsTimeout reports whether the error represents a timeout condition.
func IsTimeout(err error) bool {
	return Is(err, CodeTimeout)
}

// IsNotFound reports whether the error is a not-found condition.
func IsNotFound(err error) bool {
	return Is(err, CodeNotFound)
}

// IsFencingViolation reports split-brain risk errors.
func IsFencingViolation(err error) bool {
	return Is(err, CodeFencingViolation)
}
