// Package nodewatch implements the dynamic node discovery loop.
//
// The registry is seeded from the static config at daemon startup (SeedFromConfig).
// This package adds a background watcher that polls the control DB every
// watchInterval for node changes — additions and status updates — so operators
// can add or remove nodes via 'toris node add/remove' without restarting the daemon.
//
// Design constraints:
//   - The watcher is read-only with respect to the static config.
//   - It only syncs rows already present in toris_control.nodes into the in-memory registry.
//   - It does not auto-add nodes from external discovery sources.
//   - It runs in an errgroup goroutine and respects context cancellation.
package nodewatch

import (
	"context"
	"time"

	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/internal/logging"
)

const defaultWatchInterval = 30 * time.Second

// Watcher polls the control DB for registry changes and keeps the
// in-memory cluster.Registry in sync.
type Watcher struct {
	log      *logging.Logger
	registry *cluster.Registry
	interval time.Duration
}

// New creates a Watcher.
func New(log *logging.Logger, registry *cluster.Registry) *Watcher {
	return &Watcher{
		log:      log,
		registry: registry,
		interval: defaultWatchInterval,
	}
}

// WithInterval overrides the default poll interval.
func (w *Watcher) WithInterval(d time.Duration) *Watcher {
	w.interval = d
	return w
}

// Run starts the watch loop. It returns when ctx is canceled.
// Call in a dedicated goroutine (errgroup).
func (w *Watcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.sync(ctx)
		}
	}
}

// sync reloads the registry from the control DB.
// A failed sync is logged but does not return an error — a momentarily
// unreachable control DB should not crash the daemon.
func (w *Watcher) sync(ctx context.Context) {
	loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := w.registry.Load(loadCtx); err != nil {
		w.log.Warn("node watcher: failed to sync registry from control DB",
			"error", err.Error(),
		)
		return
	}
	w.log.Debug("node watcher: registry synced",
		"node_count", len(w.registry.All()),
	)
}
