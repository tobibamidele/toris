# Changelog

All notable changes to toris are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged as `vMAJOR.MINOR.PATCH`.
Release candidates are tagged as `vMAJOR.MINOR.PATCH-rc.N`.

---

## [v0.4.0] - 2026-05-23

### Added

- **Integration test suite** (`tests/integration/`). The biggest testing gap is
  now closed: a Docker Compose cluster (`pg-primary`, `pg-replica-1`,
  `pg-replica-2`, `pg-control`) with a Go test harness in `tests/integration/`
  that requires the `integration` build tag. Tests are gated behind the tag so
  they don't run in the unit test pass. The suite verifies:
  - L1–L5 health checks against real PostgreSQL instances
  - Correct role detection (`pg_is_in_recovery()`) for primary and replicas
  - Replication lag measurement under real WAL traffic
  - Full lease lifecycle (acquire → renew → release) against a live control DB
  - Lease conflict prevention (two instances cannot hold simultaneously)
  - Monotonically increasing generation on lease takeover
  - Audit writer drain on graceful shutdown
  - Renewal loop exit when lease is stolen (Class B)
  - Full backup pipeline via `pg_basebackup` → manifest → `pg_verifybackup`
  - Rehearsal and reseed restore modes against real backup artifacts

- **`tests/integration/docker-compose.yml`** — defines the four-container test
  cluster with health checks, shared network, and named volumes. Includes
  PostgreSQL config (`postgresql.conf`, `pg_hba.conf`) tuned for test speed
  (fsync=off, synchronous_commit=off) and configured for WAL streaming
  replication.

- **`tests/integration/harness.go`** — shared test helpers: `NewTestCluster`,
  `IsPrimary`, `IsReplica`, `WaitForReplication`, `WaitForPrimary`, `ExecSQL`,
  `QueryInt`, `EnsureControlSchema`, `ReplicationLag`.

- **`internal/nodewatch/`** — new package implementing the dynamic node
  discovery loop. `Watcher` polls `toris_control.nodes` every 30 seconds and
  syncs additions/removals into the in-memory `cluster.Registry`. Operators can
  now use `toris node add` or `toris node remove` and the daemon picks up the
  change on the next watcher tick without requiring a restart.

- **`toris node add`** — new subcommand. Inserts a node into
  `toris_control.nodes` with status `joining`. The daemon's node watcher picks
  it up within ~30 seconds and begins running health checks. Guards against
  duplicate node IDs.

  ```
  toris node add --id node-03 --host pg-replica-3.example.com --port 5432
  ```

- **`toris node remove`** — new subcommand. Marks a node as `removed` in the
  registry. Refuses to remove the active primary without `--force` to prevent
  accidental downtime.

  ```
  toris node remove --id node-02
  toris node remove --id node-02 --force   # bypasses primary guard
  ```

- **`toris node list` (extended)** — now queries `toris_control.nodes` for live
  role/status information and falls back to the static config when the control DB
  is unreachable. The output now shows ROLE and STATUS columns alongside ID, HOST,
  PORT, and AUTH_PROFILE.

- **`toris doctor` (expanded, 8 checks)** — replaced the 3-check v0.3 stub with
  a full preflight suite:
  1. pg_* tools in PATH
  2. Backup base directory writable
  3. `control_dsn` configured
  4. Control DB connectivity (connect + ping)
  5. `toris_control` schema and required tables present
  6. Lease state (active / expired / released / missing)
  7. Node freshness — warns if any configured node has not been seen in >5 minutes
  8. Backup freshness — warns at 50% of `max_age_days`, fails beyond it; errors if
     no verified backup exists at all

  Exits with code 3 when any check fails (code 0 for all-pass or warnings-only).
  Warnings (⚠) print but do not cause a non-zero exit.

  `internal/cli/doctor.go` — new file implementing `RunDoctor()`,
  `PrintDoctorResults()`, `DoctorResult`, and `NodeInfo`. The logic is
  decoupled from Cobra so it can be unit-tested without command scaffolding.
  `configDoctorAdapter` in `commands_v040.go` bridges `*config.Config` to the
  `RunDoctor` interface.

### Changed

- `internal/app/app.go`
  - `App` struct gains a `nodeWatcher *nodewatch.Watcher` field.
  - `New` constructs the watcher after the registry is initialised.
  - `Run` starts a sixth errgroup goroutine for the node watcher.
    The watcher goroutine is non-fatal: if it returns an unexpected error the
    daemon logs a warning and continues (dynamic discovery is degraded, not dead).
  - Import of `internal/nodewatch` added.

- `internal/cli/commands_v040.go` (new file, replaces inline stubs)
  - `newDoctorCmdV4` — delegates to `RunDoctor()` / `PrintDoctorResults()`.
  - `newNodeCmdV4` — extended `node list` with live DB query + fallback, plus
    `node add` and `node remove` subcommands.
  - `configDoctorAdapter` — bridges `*config.Config` to the `RunDoctor`
    interface without adding a `config` dependency to `doctor.go`.

- `internal/cli/root.go` — wires `newNodeCmdV4()` and `newDoctorCmdV4()`
  in place of the v0.3 stubs.

- `Makefile` — four new targets:
  - `integration-up` — `docker compose up -d` for the test cluster
  - `integration-down` — `docker compose down -v`
  - `integration-logs` — `docker compose logs -f`
  - `test-integration` — runs `./tests/integration/...` with `-tags integration`
  - `test-integration-ci` — full CI flow: up → test → down
  - `build-s3` — convenience target for `-tags s3` builds

- `.github/workflows/ci.yml` — new `test-integration` job:
  - Runs only on pushes to `main` and `release/**` (not on every PR) to conserve
    CI minutes.
  - Installs `postgresql-client-15` so `pg_*` tools are in PATH.
  - Starts the Docker Compose cluster, waits 20 seconds, runs the suite, captures
    logs on failure, tears down the cluster.

### Tests added

- `tests/integration/harness.go` — shared helpers (not a `_test.go`; imported by all integration tests).
- `tests/integration/health_test.go` — 4 tests: L1 transport, L4 role detection, L5 policy pass, replication lag measurement.
- `tests/integration/lease_test.go` — 3 tests: acquire/renew/release lifecycle, conflict prevention, generation advance on takeover.
- `tests/integration/backup_test.go` — 3 tests: full pipeline create+verify, rehearsal restore, reseed with standby.signal.
- `tests/integration/daemon_test.go` — 3 tests: lease release on graceful shutdown, audit queue drain, renewal loop exit when lease stolen.
- `internal/nodewatch/watcher_test.go` — 2 tests: context cancellation, WithInterval builder.
- `internal/cli/doctor_test.go` — 7 tests: no DSN, unreachable DB, missing backup dir, all-pass result, one-fail result, warning-only result, struct field accessibility.
- `internal/cli/node_commands_test.go` — 4 tests: port validation, ID required, active primary guard, node list fallback to config.

---

## [v0.3.1] - 2026-05-14


### Fixed

- **Backup records were never persisted to the control database.**
  `Pipeline.Create` returned a `*model.Backup` record and documented
  "the caller is responsible for persisting this," but no caller ever did.
  Backup history was stored only in the storage backend artifacts; the control
  DB had no `toris_control.backups` table and `toris backup list` was reading
  directory listings instead of real records. This patch adds:
  - `internal/backup/store.go` — `BackupStore` with `EnsureSchema`, `Insert`,
    `UpdateStatus`, `MarkPruned`, `Get`, `List`, `LatestVerified`,
    `FreshestVerifiedAt`, and `ListByStatus`.
  - `Pipeline` gains a `bstore *Store` field (injected; nil-safe in CLI mode).
  - `Insert` is called immediately after the backup struct is created so a
    crash mid-run leaves a `pending` record rather than no record.
  - `UpdateStatus` is called at every status transition: running, verified,
    uploaded, failed.
  - `app.bootstrap` calls `bstore.EnsureSchema` at daemon startup.

- **`toris backup list` read directory listings instead of the control DB.**
- **`toris backup prune` was registered but never implemented.**
- **`toris cluster status` showed config-derived static output.**
- **`toris reseed` printed a stub message instead of running.**
- **`toris backup create` passed `nil` as the backup store.**

---

## [v0.3.0] - 2026-05-13

### Added

- `internal/storage/storage.go` — `Backend` interface.
- `internal/storage/fs/fs.go` — Filesystem `Backend` with atomic writes.
- `internal/storage/s3/s3.go` — S3 `Backend` (requires `-tags s3`).
- `internal/restore/restore.go` — Full restore pipeline.
- `internal/restore/reseed.go` — `Reseeder` wraps the restore engine.
- `internal/restore/rewind.go` — `Rewinder` with `pg_rewind` + reseed fallback.
- `internal/retention/policy.go` — Production `Enforcer` and `Classify`.
- `internal/failover/failover.go` — `Rewinder` interface and post-failover scheduling.

---

## [v0.2.0] - 2026-05-04

### Added

- `internal/app/app.go` — full daemon wiring using `errgroup` fan-out.
- `internal/failover/failover.go` — failover decision engine.
- `internal/health/tracker.go` — replication health tracker.
- `internal/cluster/cluster.go` — node registry.
- `internal/audit/audit.go` — append-only audit event writer.
- `internal/telemetry/metrics.go` — Prometheus metrics.

---

## [v0.1.0] - 2026-05-03

### Added

- Initial scaffold: CLI skeleton with all 20 subcommands.
- Typed configuration loader (YAML + environment variables + CLI flag overrides).
- PostgreSQL backend implementing the `db.Backend` interface.
- Safe subprocess wrapper (`internal/exec`).
- Backup pipeline, manifest package, leader election, TCP proxy.
- Structured logger, typed errors, unit tests.
- GitHub Actions CI and release workflows.
