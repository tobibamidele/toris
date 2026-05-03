# Changelog

All notable changes to toris are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged as `vMAJOR.MINOR.PATCH`.
Release candidates are tagged as `vMAJOR.MINOR.PATCH-rc.N`.

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
