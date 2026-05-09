// Package app wires together all subsystems for the toris daemon.
//
// The daemon runs the following concurrent goroutines via errgroup:
//
//  1. Lease renewal loop (internal/leader)
//     Renews the control-plane lease every RenewInterval.
//     If renewal fails, this goroutine exits and the errgroup cancels
//     all other goroutines — the daemon shuts down cleanly.
//     This is Class B failure handling: lease loss = controlled shutdown,
//     not a silent split-brain.
//
//  2. Health check loop
//     Checks every node every HealthCheckInterval.
//     Feeds snapshots to the failover engine.
//     Updates the cluster registry.
//     Updates the replication health tracker (Class A).
//     Runs independently of the lease renewal loop.
//
//  3. Failover engine evaluation
//     Invoked by the health loop after every check round.
//     Applies the failure class taxonomy.
//     Only acts when this instance holds the lease.
//
//  4. TCP proxy
//     Forwards client connections to the current routing target.
//     Continues serving during brief health-check failures.
//
//  5. Metrics HTTP server
//     Exposes /metrics and /healthz.
//
// Signal handling:
//
//	SIGTERM and SIGINT cancel the root context, triggering graceful shutdown
//	of all goroutines. The lease is released on clean exit.
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/tobibamidele/toris/internal/audit"
	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/internal/config"
	"github.com/tobibamidele/toris/internal/db/postgres"
	"github.com/tobibamidele/toris/internal/failover"
	"github.com/tobibamidele/toris/internal/health"
	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/routing"
	"github.com/tobibamidele/toris/internal/telemetry"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

const (
	defaultHealthCheckInterval = 10 * time.Second
	controlDBConnectTimeout    = 15 * time.Second
)

// App is the fully wired toris daemon.
type App struct {
	cfg     *config.Config
	log     *logging.Logger
	metrics *telemetry.Metrics

	controlPool *pgxpool.Pool
	backend     *postgres.Backend
	registry    *cluster.Registry
	lm          *leader.Manager
	proxy       *routing.Proxy
	auditor     *audit.Writer
	tracker     *health.Tracker
	engine      *failover.Engine
}

// New constructs and wires all daemon subsystems.
// It does not start any goroutines — call Run.
func New(cfg *config.Config, log *logging.Logger) (*App, error) {
	a := &App{cfg: cfg, log: log}

	// ── Metrics ──────────────────────────────────────────────────────────
	a.metrics = telemetry.New(cfg.Cluster.ID)

	// ── Control database pool ─────────────────────────────────────────────
	connectCtx, cancel := context.WithTimeout(context.Background(), controlDBConnectTimeout)
	defer cancel()

	pool, err := pgxpool.New(connectCtx, cfg.ControlDSN)
	if err != nil {
		return nil, fmt.Errorf("connecting to control database: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		return nil, fmt.Errorf("pinging control database: %w", err)
	}
	a.controlPool = pool

	// ── PostgreSQL backend ────────────────────────────────────────────────
	a.backend = postgres.New(log)

	// ── Audit writer ──────────────────────────────────────────────────────
	a.auditor = audit.New(log, pool)

	// ── Cluster registry ──────────────────────────────────────────────────
	a.registry = cluster.New(log, pool, cfg.Cluster.ID)

	// ── Leader manager ────────────────────────────────────────────────────
	a.lm = leader.New(
		log, pool,
		cfg.Cluster.ID, cfg.InstanceID,
		cfg.Leader.LeaseTTL, cfg.Leader.RenewInterval,
	)

	// ── Replication health tracker ────────────────────────────────────────
	a.tracker = health.NewTracker(
		log,
		cfg.Failover.UnhealthyThreshold, // outage threshold before IsUnsafe
		cfg.Failover.MaxReplicationLagBytes,
	)

	// ── Routing proxy ─────────────────────────────────────────────────────
	if cfg.Proxy.Enabled {
		a.proxy = routing.NewProxy(log, cfg.Proxy.ListenAddr, cfg.Proxy.DialTimeout)
	}

	// ── Failover engine ───────────────────────────────────────────────────
	a.engine = failover.New(
		log,
		failover.Config{
			ClusterID:          cfg.Cluster.ID,
			InstanceID:         cfg.InstanceID,
			UnhealthyThreshold: cfg.Failover.UnhealthyThreshold,
			MaxLagBytes:        cfg.Failover.MaxReplicationLagBytes,
			FailoverEnabled:    cfg.Failover.Enabled,
		},
		a.registry,
		a.lm,
		a.backend,
		a.proxy,
		a.auditor,
		a.tracker,
		a.metrics,
	)

	return a, nil
}

// Run starts all daemon goroutines and blocks until shutdown.
// Shutdown is triggered by SIGTERM, SIGINT, or any goroutine returning
// a non-nil error.
func (a *App) Run() error {
	// Root context canceled on signal or fatal error.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Signal handling — cancel root context on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigCh:
			a.log.Info("received signal — initiating graceful shutdown", "signal", sig.String())
			rootCancel()
		case <-rootCtx.Done():
		}
	}()

	// ── Schema bootstrap ─────────────────────────────────────────────────
	bootstrapCtx, bootstrapCancel := context.WithTimeout(rootCtx, 30*time.Second)
	if err := a.bootstrap(bootstrapCtx); err != nil {
		bootstrapCancel()
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	bootstrapCancel()

	// ── Lease acquisition ─────────────────────────────────────────────────
	// Attempt to acquire the lease before starting goroutines.
	// If another instance holds it, keep retrying.
	if err := a.acquireLeaseWithRetry(rootCtx); err != nil {
		return fmt.Errorf("could not acquire cluster lease: %w", err)
	}

	a.metrics.LeaseAcquisitions.Inc()
	a.metrics.LeaseGeneration.Set(float64(a.lm.CurrentGeneration()))

	a.log.Info("daemon started",
		"instance_id", a.cfg.InstanceID,
		"cluster_id", a.cfg.Cluster.ID,
		"generation", a.lm.CurrentGeneration(),
	)

	// ── Errgroup fan-out ──────────────────────────────────────────────────
	// All goroutines share the root context. If any returns a non-nil error,
	// the group cancels the context and all others shut down.
	g, gCtx := errgroup.WithContext(rootCtx)

	// Goroutine 1: audit writer drain loop
	g.Go(func() error {
		return a.auditor.Run(gCtx)
	})

	// Goroutine 2: lease renewal loop
	// This is the Class B handler: if renewal fails, the daemon exits
	// cleanly and the lease expires naturally, allowing another instance
	// to take over with a higher generation.
	g.Go(func() error {
		err := a.lm.RunRenewLoop(gCtx)
		if err != nil && gCtx.Err() == nil {
			// Renewal failed for a reason other than ctx cancel.
			// This is a Class B failure event.
			a.log.Error("lease renewal loop exited — daemon is stepping down",
				"cluster_id", a.cfg.Cluster.ID,
				"error", err.Error(),
			)
			a.metrics.LeaseRenewalErrors.Inc()
		}
		return err
	})

	// Goroutine 3: health check loop
	// Runs independently of the lease renewal loop.
	// Replica connectivity loss (Class A) is handled here without
	// touching the lease or triggering demotion.
	g.Go(func() error {
		return a.runHealthLoop(gCtx)
	})

	// Goroutine 4: TCP proxy
	if a.proxy != nil {
		g.Go(func() error {
			return a.proxy.Run(gCtx)
		})
	}

	// Goroutine 5: metrics HTTP server
	if a.cfg.Metrics.Enabled {
		g.Go(func() error {
			return a.metrics.Serve(gCtx, a.cfg.Metrics.ListenAddr, a.log)
		})
	}

	// Block until all goroutines exit.
	if err := g.Wait(); err != nil && rootCtx.Err() == nil {
		// An error that was not caused by the root context being canceled
		// is a real problem.
		return err
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────
	a.log.Info("shutting down — releasing lease")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := a.lm.Release(shutdownCtx); err != nil {
		a.log.Warn("error releasing lease during shutdown", "error", err.Error())
	}

	a.auditor.Wait()
	a.backend.CloseAll()
	a.controlPool.Close()

	a.log.Info("daemon stopped cleanly")
	return nil
}

// bootstrap creates all required control DB schemas in dependency order.
func (a *App) bootstrap(ctx context.Context) error {
	if err := a.lm.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("lease schema: %w", err)
	}
	if err := a.auditor.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}
	if err := a.registry.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("cluster registry schema: %w", err)
	}
	if err := a.registry.Load(ctx); err != nil {
		return fmt.Errorf("loading cluster registry: %w", err)
	}

	// Seed the registry from the static config if empty.
	var configNodes []model.Node
	for _, nc := range a.cfg.Cluster.Nodes {
		configNodes = append(configNodes,
			*cluster.NodeFromConfig(a.cfg.Cluster.ID, nc.ID, nc.Host, nc.Port))
	}
	if err := a.registry.SeedFromConfig(ctx, configNodes); err != nil {
		return fmt.Errorf("seeding cluster registry: %w", err)
	}

	a.log.Info("bootstrap complete",
		"nodes_loaded", len(a.registry.All()),
	)
	return nil
}

// acquireLeaseWithRetry keeps attempting lease acquisition until it succeeds
// or the context is canceled.
func (a *App) acquireLeaseWithRetry(ctx context.Context) error {
	retryInterval := a.cfg.Leader.AcquireRetryInterval
	for {
		_, err := a.lm.Acquire(ctx)
		if err == nil {
			return nil
		}
		a.log.Info("waiting to acquire lease — another instance may be active",
			"retry_in", retryInterval,
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

// runHealthLoop checks every node on a fixed interval and feeds results to
// the failover engine. It is entirely decoupled from the lease renewal loop.
func (a *App) runHealthLoop(ctx context.Context) error {
	ticker := time.NewTicker(defaultHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.runHealthRound(ctx)
		}
	}
}

// runHealthRound performs one full check cycle across all nodes.
func (a *App) runHealthRound(ctx context.Context) {
	nodes := a.registry.All()
	snapshots := make(map[string]*model.HealthSnapshot, len(nodes))

	for _, node := range nodes {
		checkCtx, cancel := context.WithTimeout(ctx, a.cfg.Timeouts.Connect*3)

		start := time.Now()
		snap, err := a.backend.Health(checkCtx, node)
		elapsed := time.Since(start)
		cancel()

		if err != nil || snap == nil {
			snap = &model.HealthSnapshot{
				NodeID:    node.ID,
				Level:     model.HealthLevelUnreachable,
				CheckedAt: util.NowUTC(),
			}
		}

		snapshots[node.ID] = snap

		// Update registry status from snapshot.
		status := snapshotToNodeStatus(snap, node)
		_ = a.registry.UpdateStatus(ctx, node.ID, status, snap.Role, snap.ReplicationLagBytes)

		// Record metrics.
		if a.metrics != nil {
			a.metrics.HealthCheckLatency.WithLabelValues(node.ID).Observe(elapsed.Seconds())
			a.metrics.HealthCheckTotal.WithLabelValues(
				node.ID, fmt.Sprintf("L%d", snap.Level),
			).Inc()
		}
	}

	// Feed the full snapshot set to the failover engine.
	if err := a.engine.Evaluate(ctx, snapshots); err != nil {
		a.log.Error("failover engine evaluation error", "error", err.Error())
	}
}

// snapshotToNodeStatus maps a health snapshot to the appropriate NodeStatus
// for the registry, applying the failure class taxonomy.
func snapshotToNodeStatus(snap *model.HealthSnapshot, node *model.Node) model.NodeStatus {
	if node.Status == model.NodeStatusFenced || node.Status == model.NodeStatusRemoved {
		// Never downgrade a fenced/removed node via health checks.
		return node.Status
	}
	switch {
	case snap.Level >= model.HealthLevelPolicyPass:
		return model.NodeStatusHealthy
	case snap.Level >= model.HealthLevelRoleKnown:
		// Reached the DB but policy checks failed — degraded, not unhealthy.
		return model.NodeStatusDegraded
	case snap.Level >= model.HealthLevelTransport:
		// TCP connects but SQL does not — unhealthy.
		return model.NodeStatusUnhealthy
	default:
		return model.NodeStatusUnhealthy
	}
}
