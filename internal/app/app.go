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
//  6. Node watcher
//     Polls toris_control.nodes every 30 seconds and syncs any operator-added
//     or operator-removed nodes into the in-memory registry.
//     Allows 'toris node add/remove' to take effect without a daemon restart.
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
	"github.com/tobibamidele/toris/internal/backup"
	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/internal/config"
	"github.com/tobibamidele/toris/internal/db/postgres"
	"github.com/tobibamidele/toris/internal/failover"
	"github.com/tobibamidele/toris/internal/health"
	"github.com/tobibamidele/toris/internal/leader"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/nodewatch"
	"github.com/tobibamidele/toris/internal/restore"
	"github.com/tobibamidele/toris/internal/retention"
	"github.com/tobibamidele/toris/internal/routing"
	"github.com/tobibamidele/toris/internal/storage"
	fsstorage "github.com/tobibamidele/toris/internal/storage/fs"
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
	store       storage.Backend
	bstore      *backup.Store
	backupPL    *backup.Pipeline
	rewinder    *restore.Rewinder
	enforcer    *retention.Enforcer
	engine      *failover.Engine
	nodeWatcher *nodewatch.Watcher // v0.4.0
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
		cfg.Failover.UnhealthyThreshold,
		cfg.Failover.MaxReplicationLagBytes,
	)

	// ── Routing proxy ─────────────────────────────────────────────────────
	if cfg.Proxy.Enabled {
		a.proxy = routing.NewProxy(log, cfg.Proxy.ListenAddr, cfg.Proxy.DialTimeout)
	}

	// ── Storage backend ────────────────────────────────────────────────────
	store, err := fsstorage.New(cfg.Backup.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("initialising storage backend: %w", err)
	}
	a.store = store

	a.bstore = backup.NewStore(pool)

	// ── Retention enforcer ─────────────────────────────────────────────────
	a.enforcer = retention.New(log, a.store, model.RetentionPolicy{
		MinCount:   cfg.Backup.Retention.MinCount,
		MaxAgeDays: cfg.Backup.Retention.MaxAgeDays,
		KeepFailed: cfg.Backup.Retention.KeepFailed,
	})

	// ── Backup pipeline ────────────────────────────────────────────────────
	a.backupPL = backup.NewPipeline(log, a.lm, nil, a.store, a.enforcer, a.bstore)

	// ── Rewinder ───────────────────────────────────────────────────────────
	a.rewinder = restore.NewRewinder(log, nil, a.store)

	// ── Failover engine ───────────────────────────────────────────────────
	a.engine = failover.New(
		log,
		failover.Config{
			ClusterID:               cfg.Cluster.ID,
			InstanceID:              cfg.InstanceID,
			UnhealthyThreshold:      cfg.Failover.UnhealthyThreshold,
			MaxLagBytes:             cfg.Failover.MaxReplicationLagBytes,
			FailoverEnabled:         cfg.Failover.Enabled,
			AutoRewindAfterFailover: cfg.Failover.AutoRewindAfterFailover,
		},
		a.registry,
		a.lm,
		a.backend,
		a.proxy,
		a.auditor,
		a.tracker,
		a.metrics,
		nil,
	)

	// ── Node watcher (v0.4.0) ─────────────────────────────────────────────
	// Polls toris_control.nodes every 30s so 'toris node add/remove' takes
	// effect without a daemon restart.
	a.nodeWatcher = nodewatch.New(log, a.registry)

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
	if err := a.acquireLeaseWithRetry(rootCtx); err != nil {
		return fmt.Errorf("could not acquire cluster lease: %w", err)
	}

	// ── Seed node registry ────────────────────────────────────────────────
	// Runs after lease acquisition: toris_control.nodes has a foreign key to
	// toris_control.leases, so the lease row must exist before node inserts.
	if err := a.seedRegistry(rootCtx); err != nil {
		return fmt.Errorf("seeding cluster registry: %w", err)
	}

	// ── Initial routing target ────────────────────────────────────────────
	// The proxy must serve connections immediately at startup, not only after
	// the first failover. Prefer a known non-fenced primary from the registry
	// (persisted across restarts); otherwise fall back to the configured
	// primary, which by convention is the first node in cluster.nodes.
	a.setInitialProxyTarget()

	a.metrics.LeaseAcquisitions.Inc()
	a.metrics.LeaseGeneration.Set(float64(a.lm.CurrentGeneration()))

	a.log.Info("daemon started",
		"instance_id", a.cfg.InstanceID,
		"cluster_id", a.cfg.Cluster.ID,
		"generation", a.lm.CurrentGeneration(),
	)

	// ── Errgroup fan-out ──────────────────────────────────────────────────
	g, gCtx := errgroup.WithContext(rootCtx)

	// Goroutine 1: audit writer drain loop
	g.Go(func() error {
		return a.auditor.Run(gCtx)
	})

	// Goroutine 2: lease renewal loop
	g.Go(func() error {
		err := a.lm.RunRenewLoop(gCtx)
		if err != nil && gCtx.Err() == nil {
			a.log.Error("lease renewal loop exited — daemon is stepping down",
				"cluster_id", a.cfg.Cluster.ID,
				"error", err.Error(),
			)
			a.metrics.LeaseRenewalErrors.Inc()
		}
		return err
	})

	// Goroutine 3: health check loop
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

	// Goroutine 6: node watcher (v0.4.0)
	// A sync failure is logged but never fatal — the watcher is best-effort.
	// It returns ctx.Err() on cancellation, which the errgroup treats as a
	// clean exit when rootCtx is done.
	g.Go(func() error {
		err := a.nodeWatcher.Run(gCtx)
		if err != nil && gCtx.Err() == nil {
			// An unexpected error from the watcher is non-fatal.
			a.log.Warn("node watcher exited unexpectedly — dynamic node discovery disabled",
				"error", err.Error(),
			)
			return nil // don't kill the daemon over a watch failure
		}
		return nil
	})

	// Block until all goroutines exit.
	if err := g.Wait(); err != nil && rootCtx.Err() == nil {
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
	if err := a.bstore.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("backup store schema: %w", err)
	}
	if err := a.registry.Load(ctx); err != nil {
		return fmt.Errorf("loading cluster registry: %w", err)
	}

	a.log.Info("bootstrap complete",
		"nodes_loaded", len(a.registry.All()),
	)
	return nil
}

// seedRegistry populates the registry from the static config if it is empty.
// This must run after lease acquisition because toris_control.nodes has a
// foreign key to toris_control.leases (see bootstrap comment in Run).
func (a *App) seedRegistry(ctx context.Context) error {
	var configNodes []model.Node
	for _, nc := range a.cfg.Cluster.Nodes {
		configNodes = append(configNodes,
			*cluster.NodeFromConfig(a.cfg.Cluster.ID, nc.ID, nc.Host, nc.Port))
	}
	if err := a.registry.SeedFromConfig(ctx, configNodes); err != nil {
		return fmt.Errorf("seeding cluster registry: %w", err)
	}
	a.log.Info("registry seeded from config",
		"nodes_loaded", len(a.registry.All()),
	)
	return nil
}

// setInitialProxyTarget points the proxy at a node that can serve writes as
// soon as the daemon starts. It prefers a known primary from the registry
// (roles survive restarts via the control DB) and otherwise falls back to the
// configured primary — the first node in cluster.nodes, matching the
// convention used by 'toris backup create'.
func (a *App) setInitialProxyTarget() {
	if a.proxy == nil {
		return
	}

	var node *model.Node
	if p := a.registry.Primary(); p != nil {
		node = p
	} else if len(a.cfg.Cluster.Nodes) > 0 {
		nc := a.cfg.Cluster.Nodes[0]
		node = &model.Node{ID: nc.ID, Host: nc.Host, Port: nc.Port}
	}
	if node == nil {
		return
	}

	a.proxy.SetTarget(&model.RoutingTarget{
		ClusterID:  a.cfg.Cluster.ID,
		NodeID:     node.ID,
		Host:       node.Host,
		Port:       node.Port,
		Generation: a.lm.CurrentGeneration(),
		UpdatedAt:  util.NowUTC(),
	})
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
// the failover engine.
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

		status := snapshotToNodeStatus(snap, node)
		_ = a.registry.UpdateStatus(ctx, node.ID, status, snap.Role, snap.ReplicationLagBytes)

		if a.metrics != nil {
			a.metrics.HealthCheckLatency.WithLabelValues(node.ID).Observe(elapsed.Seconds())
			a.metrics.HealthCheckTotal.WithLabelValues(
				node.ID, fmt.Sprintf("L%d", snap.Level),
			).Inc()
		}
	}

	if err := a.engine.Evaluate(ctx, snapshots); err != nil {
		a.log.Error("failover engine evaluation error", "error", err.Error())
	}
}

// snapshotToNodeStatus maps a health snapshot to the appropriate NodeStatus.
func snapshotToNodeStatus(snap *model.HealthSnapshot, node *model.Node) model.NodeStatus {
	if node.Status == model.NodeStatusFenced || node.Status == model.NodeStatusRemoved {
		return node.Status
	}
	switch {
	case snap.Level >= model.HealthLevelPolicyPass:
		return model.NodeStatusHealthy
	case snap.Level >= model.HealthLevelRoleKnown:
		return model.NodeStatusDegraded
	case snap.Level >= model.HealthLevelTransport:
		return model.NodeStatusUnhealthy
	default:
		return model.NodeStatusUnhealthy
	}
}
