# Changelog

All notable changes to toris are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged as `vMAJOR.MINOR.PATCH`.
Release candidates are tagged as `vMAJOR.MINOR.PATCH-rc.N`.

---

## [v0.2.0] - 2026-05-04

### Added

- `internal/app/app.go` — full daemon wiring using `errgroup` fan-out. Runs
  five concurrent goroutines: lease renewal loop, health check loop, failover
  engine evaluation, TCP proxy, and metrics HTTP server. SIGTERM and SIGINT
  cancel the root context and trigger graceful shutdown. The lease is released
  on clean exit. The lease renewal loop and the health check loop are
  deliberately separate goroutines — replica connectivity loss (Class A) does
  not touch the lease renewal path.

- `internal/failover/failover.go` — failover decision engine implementing the
  agreed failure class taxonomy. Class A (replica connectivity loss alone) marks
  the primary degraded and starts a timer but never triggers demotion. Class B
  (lease renewal failure) is handled by the lease TTL expiry and generation
  advance — the engine simply refuses to act when it does not hold the lease.
  Failover is only initiated when the primary has been unhealthy beyond
  `unhealthy_threshold`, the lease is held, and `failover.enabled` is true.
  Fence-first ordering is enforced: `fenceOldPrimary` always completes before
  `promoteCandidate` is called, and the routing target is only updated after
  promotion succeeds. `ForcePromote` implements the `toris promote` CLI path
  with the same ordering guarantee.

- `internal/health/tracker.go` — replication health tracker (`Tracker`) that
  records per-primary replica connectivity state across health check rounds.
  Tracks `OutageSince` only when all replicas are unreachable, flips `IsUnsafe`
  only after `replication_outage_threshold` is crossed, and resets the timer
  when connectivity recovers. `IsSafeToKeepPrimary` returns `true` by default
  when no data is present. Operates independently of the lease — has no
  dependency on `leader.Manager`.

- `internal/cluster/cluster.go` — node registry (`Registry`) backed by
  `toris_control.nodes`. Persists role, status, replication lag, and last seen
  timestamp per node. `BestPromotionCandidate` selects the replica with the
  lowest lag that passes a minimum health bar, excluding fenced, removed, and
  unhealthy nodes. `SeedFromConfig` populates the registry from the static
  config on first start. `MarkFenced` and `Remove` provide explicit lifecycle
  transitions. `OutageDuration` computes how long a node has been continuously
  unreachable.

- `internal/audit/audit.go` — append-only audit event writer backed by
  `toris_control.audit_events`. Events are queued in a buffered channel (depth
  512) and flushed by a background goroutine so the hot path is never blocked
  by a slow control DB write. Drops events with a warning log if the queue is
  full. `drainRemaining` flushes any queued events during graceful shutdown
  using a background context with a 10-second deadline.

- `internal/telemetry/metrics.go` — Prometheus metrics using a dedicated
  registry (not the global default). Counters and histograms for lease
  acquisitions, renewals, and renewal errors; per-node health check latency
  and level; backup pipeline (created, verified, failed, duration, size);
  restore pipeline; failover (total, failed, duration); per-replica replication
  lag; and proxy connection count. Serves `/metrics` (OpenMetrics) and
  `/healthz` via a dedicated `http.Server` with read/write/idle timeouts.

- `internal/cli/commands.go` — daemon command (`toris daemon`) now calls
  `app.New` and `app.Run` instead of blocking on an empty select. The daemon
  is fully wired.

### Changed

- `internal/cli/commands.go` — added `internal/app` import for daemon wiring.

### Failure class rules enforced

- **Class A** (replica connectivity loss): primary is marked `DEGRADED`, the
  replication tracker starts timing the outage, no demotion is triggered. Only
  when all replicas are gone and the outage crosses `replication_outage_threshold`
  does the engine receive an unsafe signal and consider failover.

- **Class B** (lease renewal failure): the lease renewal goroutine exits and
  cancels the errgroup context, shutting the daemon down cleanly. The lease
  TTL then expires, allowing another instance to acquire a higher generation.
  The old primary cannot renew its generation and is blocked from writes by
  the routing layer's generation check.

- **Fence-first invariant**: `fenceOldPrimary` is always called and must return
  before `promoteCandidate` is attempted. Fencing failure aborts the entire
  failover sequence. Routing is only updated after promotion succeeds.

- **Read-only on demotion**: `Fence` calls `pg_terminate_backend` on active
  connections and `MarkFenced` in the registry blocks the node from appearing
  as a valid write target in candidate selection.

### Tests added

- `internal/health/tracker_test.go` — 12 tests covering: all replicas
  connected, partial replica loss (Class A — not unsafe), all replicas lost
  starts timer, below-threshold not unsafe, threshold crossed becomes unsafe,
  `IsSafeToKeepPrimary` flip, connectivity restored resets timer, lag
  exceeding max not counted, nil-for-unknown-node, no-data-assume-safe,
  Get returns copy, Tracker has no lease dependency.

- `internal/failover/failover_test.go` — 16 tests covering: healthy primary
  no action, replica loss only no action, lease not held no action, failover
  disabled no action, below threshold no action, above threshold failover,
  Class A escalation (replication unsafe), exact-threshold boundary, fence
  before promote invariant, routing not updated on promote failure, fencing
  failure aborts promotion, lowest-lag candidate selection, max-lag exclusion,
  unhealthy replica excluded, no candidates returns nil, read-only enforcement
  on fenced nodes, generation mismatch rejects write, current generation
  accepts write.

- `internal/cluster/cluster_test.go` — 14 tests covering: `NodeFromConfig`
  field values, `OutageDuration` for healthy/unhealthy/degraded/zero nodes,
  fenced and removed nodes not writable, healthy primary writable, candidate
  picks lowest lag, fenced excluded from candidates, no snapshot excluded,
  all excluded returns nil.

- `internal/audit/audit_test.go` — 5 tests covering: auto-ID on empty,
  auto-timestamp on zero, `EmitNow` does not panic, queue-full drops
  gracefully (600 events into 512-depth queue), all `AuditEventKind`
  constants are non-empty and unique.

---

## [v0.1.0] - 2026-05-03

### Added

- Initial scaffold: CLI skeleton with all 20 subcommands (`init`, `config validate`,
  `cluster status`, `node list`, `health`, `backup create/verify/list`, `restore`,
  `reseed`, `leader status/acquire/release`, `promote`, `demote`, `rewind`,
  `daemon`, `doctor`, `version`).
- Typed configuration loader (YAML + environment variables + CLI flag overrides)
  with full validation that collects all problems before returning.
- PostgreSQL backend implementing the `db.Backend` interface:
  five-level health model (transport, readiness, liveness, role, policy),
  `pg_is_in_recovery()` role detection, `pg_promote()` promotion,
  connection fencing via `pg_terminate_backend`.
- Safe subprocess wrapper (`internal/exec`) with context cancellation, timeout
  enforcement, stderr capture, and secret redaction in logs.
- Wrappers for `pg_isready`, `pg_basebackup`, `pg_verifybackup`, and `pg_rewind`
  built on the exec wrapper.
- Backup pipeline: preflight checks, `pg_basebackup`, per-artifact SHA-256 manifest,
  `pg_verifybackup`, verified-only marking.
- Manifest package: write with self-hash, read with tamper detection, artifact
  enumeration.
- Leader election using PostgreSQL-backed lease records in `toris_control.leases`:
  atomic acquisition, renewal loop, explicit release, fencing token assertion.
- TCP proxy routing layer with atomic target swap on failover.
- Typed error codes (`internal/errors`) with wrapping, unwrapping, and sentinel helpers.
- Structured logger (`internal/logging`) backed by `go.uber.org/zap` with
  context correlation IDs.
- Unit tests: config validation, lease model, health snapshot, manifest round-trip
  and tamper detection, retention policy, exec wrapper, typed errors.
- `toris init` generates a fully commented starter configuration file.
- `toris doctor` runs preflight checks without requiring a live cluster.
- `--dry-run` support on all destructive commands.
- `--output json` support on all commands.
- Makefile with `build`, `test`, `test-race`, `test-cover`, `lint`, `fmt`, `vet`.
- GitHub Actions CI (lint + test + cross-platform build on every push/PR).
- GitHub Actions release workflow (cross-platform binaries + SHA-256 checksums
  + auto-generated release notes on semver tags).

### Architecture decisions recorded

- Replica connectivity loss alone does not trigger demotion.
  Demotion requires lease loss or fencing-token invalidation.
- Applications connect only to the toris proxy endpoint, never to database nodes directly.
- Every cluster-mutating operation carries and validates the current fencing token.
- Demoted nodes are forced into `default_transaction_read_only = on` before
  the routing target is switched.

### Not yet implemented (planned for v0.2.0)

- `internal/app/app.go` daemon wiring and signal handling
- `internal/failover` decision engine (threshold-based, lease-aware)
- `internal/restore` restore and reseed pipelines
- `internal/cluster` node registry backed by the control database
- `internal/audit` append-only audit event writer
- `internal/telemetry` Prometheus metrics exposition
- Integration test suite using testcontainers-go
- Automatic `pg_rewind` after failover
- Offsite backup copy (S3 backend)
