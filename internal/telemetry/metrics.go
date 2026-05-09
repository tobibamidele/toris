// Package telemetry exposes Prometheus metrics for the toris daemon.
//
// All metrics are registered on a dedicated prometheus.Registry (not the
// global default) so toris can be embedded in other programs without
// polluting their metrics namespace.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tobibamidele/toris/internal/logging"
)

// Metrics holds all instrumentation for the toris daemon.
type Metrics struct {
	reg *prometheus.Registry
	srv *http.Server

	// ── Lease ────────────────────────────────────────────────────────────
	LeaseAcquisitions  prometheus.Counter
	LeaseRenewals      prometheus.Counter
	LeaseRenewalErrors prometheus.Counter
	LeaseGeneration    prometheus.Gauge

	// ── Health checks ────────────────────────────────────────────────────
	HealthCheckTotal   *prometheus.CounterVec   // labels: node_id, level
	HealthCheckLatency *prometheus.HistogramVec // labels: node_id

	// ── Backup ───────────────────────────────────────────────────────────
	BackupsCreated  prometheus.Counter
	BackupsVerified prometheus.Counter
	BackupsFailed   prometheus.Counter
	BackupDuration  prometheus.Histogram // seconds
	BackupSizeBytes prometheus.Histogram // bytes

	// ── Restore ──────────────────────────────────────────────────────────
	RestoresStarted   prometheus.Counter
	RestoresCompleted prometheus.Counter
	RestoresFailed    prometheus.Counter
	RestoreDuration   prometheus.Histogram

	// ── Failover ─────────────────────────────────────────────────────────
	FailoversTotal   prometheus.Counter
	FailoversFailed  prometheus.Counter
	FailoverDuration prometheus.Histogram

	// ── Replication ──────────────────────────────────────────────────────
	ReplicationLagBytes *prometheus.GaugeVec // labels: node_id

	// ── Proxy ────────────────────────────────────────────────────────────
	ProxyConnectionsActive prometheus.Gauge
	ProxyConnectionsTotal  prometheus.Counter
}

// New creates and registers all metrics on a fresh registry.
func New(clusterID string) *Metrics {
	reg := prometheus.NewRegistry()
	constLabels := prometheus.Labels{"cluster_id": clusterID}

	m := &Metrics{reg: reg}

	// ── Lease ────────────────────────────────────────────────────────────
	m.LeaseAcquisitions = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "leader", Name: "lease_acquisitions_total",
		Help:        "Total number of successful lease acquisitions.",
		ConstLabels: constLabels,
	})
	m.LeaseRenewals = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "leader", Name: "lease_renewals_total",
		Help:        "Total number of successful lease renewals.",
		ConstLabels: constLabels,
	})
	m.LeaseRenewalErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "leader", Name: "lease_renewal_errors_total",
		Help:        "Total number of failed lease renewal attempts.",
		ConstLabels: constLabels,
	})
	m.LeaseGeneration = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "toris", Subsystem: "leader", Name: "lease_generation",
		Help:        "Current lease generation (fencing token).",
		ConstLabels: constLabels,
	})

	// ── Health checks ────────────────────────────────────────────────────
	m.HealthCheckTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "health", Name: "checks_total",
		Help:        "Total health checks performed, labelled by node_id and result level.",
		ConstLabels: constLabels,
	}, []string{"node_id", "level"})
	m.HealthCheckLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "toris", Subsystem: "health", Name: "check_duration_seconds",
		Help:        "Duration of health check round trips in seconds.",
		Buckets:     []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		ConstLabels: constLabels,
	}, []string{"node_id"})

	// ── Backup ───────────────────────────────────────────────────────────
	m.BackupsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "backup", Name: "created_total",
		Help:        "Total number of backup operations started.",
		ConstLabels: constLabels,
	})
	m.BackupsVerified = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "backup", Name: "verified_total",
		Help:        "Total number of backups that passed verification.",
		ConstLabels: constLabels,
	})
	m.BackupsFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "backup", Name: "failed_total",
		Help:        "Total number of backups that failed at any pipeline stage.",
		ConstLabels: constLabels,
	})
	m.BackupDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "toris", Subsystem: "backup", Name: "duration_seconds",
		Help:        "Duration of completed backup operations in seconds.",
		Buckets:     prometheus.ExponentialBuckets(60, 2, 10), // 1m–17h
		ConstLabels: constLabels,
	})
	m.BackupSizeBytes = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "toris", Subsystem: "backup", Name: "size_bytes",
		Help:        "Size of completed backups in bytes.",
		Buckets:     prometheus.ExponentialBuckets(1<<20, 4, 12), // 1MB–4TB
		ConstLabels: constLabels,
	})

	// ── Restore ──────────────────────────────────────────────────────────
	m.RestoresStarted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "restore", Name: "started_total",
		Help:        "Total number of restore operations started.",
		ConstLabels: constLabels,
	})
	m.RestoresCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "restore", Name: "completed_total",
		Help:        "Total number of restore operations completed successfully.",
		ConstLabels: constLabels,
	})
	m.RestoresFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "restore", Name: "failed_total",
		Help:        "Total number of restore operations that failed.",
		ConstLabels: constLabels,
	})
	m.RestoreDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "toris", Subsystem: "restore", Name: "duration_seconds",
		Help:        "Duration of completed restore operations in seconds.",
		Buckets:     prometheus.ExponentialBuckets(60, 2, 10),
		ConstLabels: constLabels,
	})

	// ── Failover ─────────────────────────────────────────────────────────
	m.FailoversTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "failover", Name: "total",
		Help:        "Total number of failover operations initiated.",
		ConstLabels: constLabels,
	})
	m.FailoversFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "failover", Name: "failed_total",
		Help:        "Total number of failover operations that did not complete successfully.",
		ConstLabels: constLabels,
	})
	m.FailoverDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "toris", Subsystem: "failover", Name: "duration_seconds",
		Help:        "Duration of completed failover operations in seconds.",
		Buckets:     prometheus.ExponentialBuckets(1, 2, 10), // 1s–17m
		ConstLabels: constLabels,
	})

	// ── Replication ──────────────────────────────────────────────────────
	m.ReplicationLagBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "toris", Subsystem: "replication", Name: "lag_bytes",
		Help:        "Current replication lag in bytes for each replica node.",
		ConstLabels: constLabels,
	}, []string{"node_id"})

	// ── Proxy ────────────────────────────────────────────────────────────
	m.ProxyConnectionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "toris", Subsystem: "proxy", Name: "connections_active",
		Help:        "Number of currently open proxy connections.",
		ConstLabels: constLabels,
	})
	m.ProxyConnectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "toris", Subsystem: "proxy", Name: "connections_total",
		Help:        "Total number of proxy connections accepted.",
		ConstLabels: constLabels,
	})

	// Register all collectors.
	for _, c := range []prometheus.Collector{
		m.LeaseAcquisitions, m.LeaseRenewals, m.LeaseRenewalErrors, m.LeaseGeneration,
		m.HealthCheckTotal, m.HealthCheckLatency,
		m.BackupsCreated, m.BackupsVerified, m.BackupsFailed, m.BackupDuration, m.BackupSizeBytes,
		m.RestoresStarted, m.RestoresCompleted, m.RestoresFailed, m.RestoreDuration,
		m.FailoversTotal, m.FailoversFailed, m.FailoverDuration,
		m.ReplicationLagBytes,
		m.ProxyConnectionsActive, m.ProxyConnectionsTotal,
	} {
		reg.MustRegister(c)
	}

	return m
}

// Serve starts the Prometheus HTTP exposition endpoint on listenAddr.
// It returns when ctx is canceled.
func (m *Metrics) Serve(ctx context.Context, listenAddr string, log *logging.Logger) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	m.srv = &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	log.Info("metrics server starting", "addr", listenAddr)

	errCh := make(chan error, 1)
	go func() {
		if err := m.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return m.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("metrics server error: %w", err)
	}
}
