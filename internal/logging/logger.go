// Package logging sets up the application-wide structured logger.
// Uses uber-go/zap for structured, leveled, high-performance logging.
// A correlation ID can be injected into context and automatically
// attached to every log call via WithContext.
package logging

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey is unexported to prevent collisions.
type contextKey struct{}

var correlationIDKey = contextKey{}

// Logger wraps *zap.Logger to add context-aware helpers.
type Logger struct {
	z *zap.Logger
}

// New constructs a Logger from the given level and format strings.
// level: "debug" | "info" | "warn" | "error"
// format: "json" | "console"
func New(level, format string) (*Logger, error) {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}

	var cfg zap.Config
	switch strings.ToLower(format) {
	case "console":
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	default: // "json"
		cfg = zap.NewProductionConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	// Always include caller in production for debuggability.
	cfg.DisableCaller = false

	z, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return nil, fmt.Errorf("building logger: %w", err)
	}
	return &Logger{z: z}, nil
}

// WithContext returns a child Logger annotated with any correlation ID
// stored in ctx. If no correlation ID is present, the logger is returned as-is.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	if id, ok := ctx.Value(correlationIDKey).(string); ok && id != "" {
		return &Logger{z: l.z.With(zap.String("correlation_id", id))}
	}
	return l
}

// WithField returns a child Logger with one additional key-value field.
func (l *Logger) WithField(key string, val any) *Logger {
	return &Logger{z: l.z.With(zap.Any(key, val))}
}

// WithFields returns a child Logger with multiple additional key-value fields.
// fields must be key, value, key, value pairs.
func (l *Logger) WithFields(fields ...any) *Logger {
	zfields := make([]zap.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		k, ok := fields[i].(string)
		if !ok {
			continue
		}
		zfields = append(zfields, zap.Any(k, fields[i+1]))
	}
	return &Logger{z: l.z.With(zfields...)}
}

// Debug logs at DEBUG level.
func (l *Logger) Debug(msg string, fields ...any) {
	l.z.Sugar().Debugw(msg, fields...)
}

// Info logs at INFO level.
func (l *Logger) Info(msg string, fields ...any) {
	l.z.Sugar().Infow(msg, fields...)
}

// Warn logs at WARN level.
func (l *Logger) Warn(msg string, fields ...any) {
	l.z.Sugar().Warnw(msg, fields...)
}

// Error logs at ERROR level.
func (l *Logger) Error(msg string, fields ...any) {
	l.z.Sugar().Errorw(msg, fields...)
}

// Fatal logs at FATAL level then calls os.Exit(1).
// Use only for unrecoverable startup errors.
func (l *Logger) Fatal(msg string, fields ...any) {
	l.z.Sugar().Fatalw(msg, fields...)
}

// Sync flushes buffered log entries. Call on shutdown.
func (l *Logger) Sync() {
	_ = l.z.Sync()
}

// ─── Context helpers ─────────────────────────────────────────────────────────

// WithCorrelationID returns a new context carrying the given correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationIDFromContext extracts the correlation ID from ctx, or "" if absent.
func CorrelationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// Nop returns a no-op logger for use in tests.
func Nop() *Logger {
	return &Logger{z: zap.NewNop()}
}
