// Package util provides small, reusable helpers that don't belong to any
// specific domain package. Keep this package minimal — no giant utils dumps.
package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID generates a cryptographically random 16-byte hex string.
// Suitable for use as record IDs, correlation IDs, and operation IDs.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failure is catastrophic on any sane OS.
		panic(fmt.Sprintf("util.NewID: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// FormatDuration formats a duration in a human-readable, compact form.
// e.g. 2h3m4s, 45s, 300ms
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh%dm%ds", h, m, s)
}

// NowUTC returns the current UTC time truncated to milliseconds.
// Use for all timestamps stored in the DB to avoid nanosecond precision issues.
func NowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// Ptr returns a pointer to a copy of v. Useful for optional struct fields.
func Ptr[T any](v T) *T {
	return &v
}

// Clamp returns v clamped to [min, max].
func Clamp[T interface{ ~int | ~int64 | ~float64 }](v, min, max T) T {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
