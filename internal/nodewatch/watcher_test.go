package nodewatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/nodewatch"
)

// TestWatcher_RespectsContextCancellation verifies the watcher loop exits
// cleanly when the context is canceled, with no goroutine leak.
func TestWatcher_RespectsContextCancellation(t *testing.T) {
	// nil registry is fine here — we're only testing the cancel path.
	// A nil registry will cause sync() to panic, but the cancel fires
	// before the first tick when the interval is long.
	w := nodewatch.New(logging.Nop(), nil).WithInterval(10 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		// ctx.Err() is expected.
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("watcher did not stop within 2 seconds after ctx cancel")
	}
}

// TestWatcher_WithInterval overrides the poll interval.
func TestWatcher_WithInterval(t *testing.T) {
	// Verify the builder pattern doesn't panic and returns the same instance.
	w := nodewatch.New(logging.Nop(), nil)
	w2 := w.WithInterval(5 * time.Second)
	if w != w2 {
		t.Error("WithInterval should return the same *Watcher instance")
	}
}
