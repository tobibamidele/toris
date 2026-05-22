# Changelog

All notable changes to toris are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged as `vMAJOR.MINOR.PATCH`.
Release candidates are tagged as `vMAJOR.MINOR.PATCH-rc.N`.

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
  Replaced the `os.ReadDir` implementation with a query to
  `toris_control.backups` via `BackupStore.List`. Output now shows ID, status,
  size, and start time in a formatted table. Falls back gracefully if the
  control DB is unreachable.

- **`toris backup prune` was registered in the brief but never implemented.**
  `cmd.AddCommand(createCmd, verifyCmd, listCmd)` was missing `pruneCmd`.
  The `pruneCmd` is now implemented: it fetches all backup records from the
  control DB, passes them to `retention.Enforcer.Apply`, deletes artifacts
  from the storage backend, and calls `BackupStore.MarkPruned` for each
  pruned record. Requires `--force` or interactive confirmation.

- **`toris cluster status` showed config-derived static output.**
  Replaced with a real control DB query: loads the node registry via
  `cluster.Registry`, queries the current lease via `leader.Manager.Status`,
  and shows the freshest verified backup timestamp via
  `BackupStore.FreshestVerifiedAt`. Degrades gracefully to config-derived
  output if the control DB is unreachable, with a warning log.

- **`toris reseed` printed a stub message instead of running.**
  Replaced with a real implementation calling `restore.Reseeder.Reseed`.
  If `--backup-id` is omitted, the latest verified backup is resolved
  automatically from the control DB. Prints the job ID, backup ID, status,
  duration, and the `pg_ctl start` command to use after reseeding completes.

- **`toris backup create` passed `nil` as the backup store.**
  Now attempts to open a control DB connection before creating the backup.
  If the control DB is reachable, the backup record is persisted throughout
  the pipeline. If it is unreachable, the backup proceeds with a warning log
  and no control DB persistence (same behaviour as before, but now explicit).

### Added

- `internal/backup/store.go` — new file, documented above.
- `internal/backup/store_test.go` — unit tests for status transition contracts,
  timestamp invariants, and the `FreshestVerifiedAt` sentinel epoch convention.

### Changed

- `internal/backup/pipeline.go`
  - `Pipeline` struct gains `bstore *Store` field.
  - `NewPipeline` gains `bstore *Store` parameter (sixth argument; nil-safe).
  - `persistStatus` helper added: calls `bstore.UpdateStatus` if bstore is set.
  - `Prune` now calls `bstore.MarkPruned` for each pruned backup ID.

- `internal/app/app.go`
  - `App` struct gains `bstore *backup.Store` field.
  - `bstore` is initialised after `controlPool` is created.
  - `bootstrap` calls `bstore.EnsureSchema`.
  - `NewPipeline` call updated with `a.bstore` as the sixth argument.

- `internal/cli/commands.go`
  - `newBackupCmd` — `createCmd` wires `BackupStore` when control DB is
    reachable; `listCmd` replaced with control DB query; `pruneCmd` added.
  - `newClusterCmd` — `cluster status` queries the control DB for live node
    state, lease, and backup freshness.
  - `newReseedCmd` — replaced stub with real `Reseeder.Reseed` call; added
    `--backup-id` and `--target-dir` flags; auto-resolves latest verified
    backup from control DB when `--backup-id` is omitted.
  - New imports: `"github.com/tobibamidele/toris/internal/cluster"`,
    `"github.com/tobibamidele/toris/internal/retention"`.
  - `NewPipeline` call sites updated to pass `bstore` as sixth argument.

---

## [v0.3.0] - 2026-05-13

### Added

- `internal/storage/storage.go` — `Backend` interface with `Write`, `Read`,
  `Delete`, `List`, `Stat`, and `Name`. All storage operations accept a context.
  `ObjectInfo` carries key, size, last-modified, and content hash.

- `internal/storage/fs/fs.go` — Filesystem `Backend` implementation. Writes are
  atomic: data is written to a hidden temp file in the same directory, synced to
  disk, then renamed into place. `rename(2)` on POSIX is atomic within a mount
  point so readers never see a partial write. Path traversal attempts are silently
  redirected to an invalid path that fails on open. Helper functions `KeyForBackup`
  and `BackupPrefix` provide canonical key conventions for backup artifacts.
  `WriteFile` and `ReadFile` convenience helpers bridge local paths and the backend.

- `internal/storage/s3/s3.go` — S3 `Backend` implementation using AWS SDK v2,
  compiled only with `-tags s3`. Objects below 100 MB use `PutObject`; larger
  objects use the multipart upload API with 64 MB parts and automatic abort on
  part failure. `UsePathStyle` is set for MinIO/Localstack compatibility.

- `internal/storage/s3/s3_stub.go` — Stub compiled without `-tags s3` that
  returns a clear error on every call, directing operators to rebuild with
  `-tags s3` to enable S3 storage. Prevents accidental silent no-ops.

- `internal/restore/restore.go` — Full restore pipeline: (1) fetch and verify
  manifest self-hash from storage, (2) download and SHA-256-verify every artifact,
  (3) extract tar/tar.gz archives with path-traversal protection, (4) write
  `standby.signal` for reseed mode, (5) clean up rehearsal staging directories.
  A restore is never marked complete if any step fails. Failed jobs preserve their
  `ArtifactDir` for forensic inspection.

- `internal/restore/reseed.go` — `Reseeder` wraps the restore engine for the
  specific case of reseeding a replica: calls `Run` in `RestoreModeReseed` and
  validates that both `BackupID` and `TargetDir` are supplied before starting.

- `internal/restore/rewind.go` — `Rewinder` attempts `pg_rewind` on a demoted
  old primary using the existing `PgRewind` tool wrapper. If `pg_rewind` fails
  and `FallbackBackupID` is set, it falls back to a full reseed via `Reseeder`.
  The `RewindJob` record captures whether rewind or the fallback path was used.

- `internal/retention/policy.go` — Production `Enforcer` that calls
  `Classify` and deletes prunable backup artifacts from the storage backend via
  `store.List` + `store.Delete`. Logs each pruned backup with its age. Failed
  deletes are logged and skipped so a single bad artifact cannot abort the entire
  retention run.

- `internal/retention/policy.go` — `Classify` function (production code,
  previously only present as a test helper): sort verified backups oldest-first,
  keep the newest `MinCount` unconditionally, prune older ones beyond `MaxAgeDays`,
  honour `KeepFailed`, and always keep pending/running backups.

- `internal/failover/failover.go` — `Rewinder` interface and `RewindOptions`
  type defined in this package to avoid an import cycle with `internal/restore`.
  `Engine` gains a `rewinder` field and `autoRewindAfterFailover` flag. After a
  successful failover, `scheduleRewind` runs in a background goroutine, calls
  `RewindOrReseed`, and updates the old primary's registry status to `joining`
  on success so the health loop can re-evaluate it.

- `internal/config/config.go` — `FailoverConfig.ReplicationOutageThreshold`
  (default 5 minutes) and `FailoverConfig.AutoRewindAfterFailover` (default
  true). `RestoreConfig.DataDir` field for explicit data directory override.

- `pkg/model/model.go` — `RestoreMode` enum (`empty_node`, `replacement`,
  `rehearsal`, `reseed`). `RewindJob` model with `RewindStatus` enum
  (`pending`, `running`, `completed`, `failed`, `fallback_reseed`) and
  `UsedFallback` field.

### Changed

- `internal/backup/pipeline.go` — Rewrote to inject `storage.Backend` and
  `retention.Enforcer`. Staging directory is now `StagingDir/<backupID>` (was
  `BackupBaseDir/<backupID>`). After `pg_verifybackup` passes, artifacts are
  uploaded to the storage backend. If `offsite_required` is false and upload
  fails, the backup is retained locally with a warning. `Prune` method added
  to invoke the retention enforcer. `BackupBaseDir` field replaced with
  `StagingDir` and `ClusterID`.

- `internal/app/app.go` — Wired `fsstorage.Backend`, `retention.Enforcer`,
  `backup.Pipeline`, and `restore.Rewinder` into the dependency graph. Daemon
  now initialises the storage backend at startup and injects it into the backup
  pipeline and rewinder.

- `internal/cli/commands.go` — Removed all TODO stubs for the following
  commands; all are now fully implemented:
  - `toris backup list` — queries the storage backend for backup IDs.
  - `toris restore` — calls `restore.Engine.Run` with `RestoreModeEmptyNode`.
  - `toris leader status` — opens a control DB connection and calls
    `leader.Manager.Status`; prints generation, expiry, and an EXPIRED flag.
  - `toris leader acquire` — calls `leader.Manager.Acquire` and prints the
    resulting lease with generation and expiry.
  - `toris leader release` — calls `leader.Manager.Release` with confirmation
    prompt unless `--force`.
  - `toris backup create` — now injects a real `fsstorage.Backend` rather than
    passing `nil`; the lease manager is still `nil` in CLI mode (documented).
  - `connectControlDB` helper opens and pings a pgxpool connection.
  - `splitKey` helper splits storage keys on `/`.
  - `restoreEngine` and `restoreOptions` helpers bridge CLI args and the restore
    package.

### Tests added

- `internal/storage/fs/fs_test.go` — 16 tests: write/read round-trip, large
  payload (5 MB), atomic write leaves no temp files, overwrite is atomic, read
  not-found error, delete removes object, delete non-existent is no error, list
  returns all keys, list filters by prefix, list on empty dir, list is sorted,
  stat returns size, stat not-found, path traversal is safe, KeyForBackup,
  BackupPrefix, canceled context on Write and Read.

- `internal/restore/restore_test.go` — 10 tests: rehearsal mode completes,
  empty-node mode completes, reseed mode writes `standby.signal`, missing backup
  returns failed job, failed job preserves ArtifactDir, job ID always set,
  canceled context, reseeder missing BackupID error, reseeder missing TargetDir
  error. Tests use a `writeFakeTarGz` helper that produces a real gzip+tar
  archive so the extraction stage executes genuine decompression code.

- `internal/retention/retention_test.go` — 10 tests: below MinCount keeps all,
  exactly at MinCount keeps all, prunes old beyond MinCount, newest MinCount
  always kept, keeps failed when KeepFailed=true, prunes failed when false,
  zero MaxAgeDays disables age pruning, pending/running always kept, uploaded
  status counts as verified for pruning purposes, single backup never pruned.

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

