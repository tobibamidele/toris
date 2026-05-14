# toris

Production-grade PostgreSQL backup, synchronization, failover, and restoration orchestration.

---

## What toris is

toris is a single binary that manages the full operational lifecycle of a
PostgreSQL cluster:

- Creates and verifies backups using `pg_basebackup` and `pg_verifybackup`
- Restores backups with full manifest verification and artifact SHA-256 checking
- Reseeds replicas and rewinds demoted primaries using `pg_rewind`
- Maintains a stable single-DSN proxy endpoint so applications never need to
  track which node is primary
- Runs lease-based leader election with fencing tokens to prevent split-brain
- Detects primary failures and promotes the best replica (when failover is enabled)
- Enforces retention policies and keeps a tamper-evident manifest per backup
- Exposes structured logs, Prometheus metrics, and a full audit trail

## What toris is not

- Not a connection pooler (use pgBouncer in front of the toris proxy if needed)
- Not a multi-primary write system — v1 supports one writable primary per cluster
- Not a replacement for WAL archiving — configure `archive_command` alongside
  toris for point-in-time recovery
- Not a cloud-managed service — toris runs on your VPS or bare metal

---

## How the single endpoint works

```
application --> localhost:5433 (toris proxy)
                     |
              toris routing layer
              (atomic swap on failover)
                     |
              pg-primary.example.com:5432
```

`toris daemon` listens on a configurable address (default `127.0.0.1:5433`) and
forwards all TCP connections to the current primary. When failover occurs:

1. The old primary is fenced: active connections terminated, writes blocked
2. The routing target is atomically swapped to the new primary
3. New connections go to the new primary immediately
4. The old primary is scheduled for `pg_rewind` or full reseed

Applications need only one DSN: `host=127.0.0.1 port=5433`. toris handles
everything else.

---

## How backup integrity is guaranteed

Every backup follows a strict pipeline:

```
preflight --> pg_basebackup --> manifest (SHA-256 per artifact)
         --> pg_verifybackup --> storage upload
```

A backup is only marked `verified` after `pg_verifybackup` exits 0. The manifest
embeds a self-hash (SHA-256 of the manifest itself) so any post-write tampering
is detected on restore. A backup is never used for restore if its manifest
verification fails.

On restore, every artifact's SHA-256 is re-verified against the manifest before
extraction begins.

---

## How restore and failover are performed

### Restore

```bash
toris restore --backup-id <id> --target-dir /var/lib/postgresql/data
```

1. Downloads and verifies the manifest self-hash
2. Downloads and SHA-256-verifies every artifact
3. Extracts tar archives into the target data directory
4. Starts PostgreSQL (if `restore.start_after_restore: true`)

### Failover

```bash
# Automatic (if failover.enabled: true in config)
# Manual:
toris promote --node <replica-id> --force
```

1. Lease generation is advanced (fencing token incremented)
2. Old primary is fenced: connections terminated, routing removed
3. Best candidate replica promoted via `pg_promote()`
4. Routing target atomically updated
5. Old primary scheduled for `pg_rewind`; falls back to reseed if rewind fails

---

## Failure class rules

**Class A — primary loses replica connectivity only**
The primary is marked degraded and a replication outage timer starts.
Failover is not triggered. Only if all replicas remain unreachable beyond
`replication_outage_threshold` is the situation escalated.

**Class B — primary loses lease renewal (control-plane connectivity)**
The lease TTL expires naturally. Another toris instance acquires a higher
generation. The old primary's generation is stale and it is blocked from writes
by the routing layer's generation check. The daemon shuts down cleanly.

**Fence first, route second**
The old primary is always fenced and its connections terminated before any
promotion attempt. The routing target is only updated after promotion succeeds.

---

## Quick start

```bash
# 1. Write a starter config
toris init --out toris.yaml

# 2. Edit toris.yaml (nodes, control_dsn, auth profiles)

# 3. Validate the config
toris config validate

# 4. Check environment
toris doctor

# 5. Check cluster health
toris health

# 6. Create a backup
toris backup create

# 7. Verify the backup
toris backup verify <backup-id>

# 8. Start the daemon (proxy + health loop + lease + metrics)
toris daemon
```

---

## Running the daemon

```bash
toris daemon --config /etc/toris/toris.yaml
```

The daemon runs five concurrent goroutines:

| Goroutine | Responsibility |
|---|---|
| Lease renewal | Renews the control-plane lease every `renew_interval` |
| Health check loop | Checks all nodes every 10 seconds, updates registry |
| Failover engine | Evaluates health snapshots, triggers failover when warranted |
| TCP proxy | Forwards client connections to the current primary |
| Metrics server | Exposes `/metrics` and `/healthz` on `metrics.listen_addr` |

Graceful shutdown on SIGTERM/SIGINT: releases the lease, waits for active proxy
connections, flushes the audit queue.

---

## Dry run

Every destructive command supports `--dry-run`:

```bash
toris backup create --dry-run     # preflight only, no pg_basebackup
toris promote --node node-02 --dry-run
toris rewind --data-dir /var/lib/postgresql/data --dry-run
```

---

## Storage backends

| Backend | Build | When to use |
|---|---|---|
| Filesystem (`fs`) | Default | Single-server or NFS-mounted storage |
| S3-compatible | `-tags s3` | Object storage, offsite copies |

```bash
# Build with S3 support
go build -tags s3 -o bin/toris ./cmd/toris
```

---

## Configuration

Run `toris init` for a fully commented starter config.
See `docs/config.md` for the complete reference.

Key fields:

```yaml
control_dsn: "host=localhost port=5432 user=toris dbname=toris_control sslmode=require"
cluster:
  id: "pg-main"
  nodes:
    - id: "node-01"
      host: "pg-primary.example.com"
      port: 5432
backup:
  storage_backend: "fs"
  base_dir: "/var/lib/toris/backups"
failover:
  enabled: false
  unhealthy_threshold: "60s"
  replication_outage_threshold: "5m"
  auto_rewind_after_failover: true
```

---

## CLI reference

| Command | What it does |
|---|---|
| `toris init` | Write a starter config file |
| `toris config validate` | Validate the config and report all problems |
| `toris cluster status` | Show cluster summary |
| `toris node list` | List configured nodes |
| `toris health` | Run L1-L5 health checks on all nodes |
| `toris backup create` | Create and verify a backup |
| `toris backup verify <path>` | Verify an existing backup |
| `toris backup list` | List all backups in storage |
| `toris backup prune` | Apply retention policy |
| `toris restore` | Restore a backup into a data directory |
| `toris reseed` | Reseed a replica from the latest backup |
| `toris rewind` | Rewind a demoted primary using pg_rewind |
| `toris leader status` | Show current lease holder and generation |
| `toris leader acquire` | Manually acquire the cluster lease |
| `toris leader release` | Release the cluster lease |
| `toris promote --node <id>` | Promote a replica to primary |
| `toris demote --node <id>` | Demote the primary |
| `toris daemon` | Run the full daemon |
| `toris doctor` | Diagnose configuration and connectivity problems |
| `toris version` | Print version and build info |

---

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error |
| 2 | Config validation failed |
| 3 | Health check failed |
| 4 | Backup failed |
| 5 | Restore failed |
| 6 | Failover failed |
| 7 | Lease conflict |

---

## Development

```bash
make build        # compile bin/toris
make test         # run all unit tests
make test-race    # run with race detector
make test-cover   # generate coverage report
make lint         # run golangci-lint
make fmt          # gofmt
```

Requirements: Go 1.22+, PostgreSQL client tools in PATH for integration tests.

Build with S3 support:
```bash
make build GOFLAGS="-tags s3"
```
