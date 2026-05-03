# toris

Production-grade PostgreSQL backup, synchronization, failover, and restoration orchestration.

---

## What toris is

toris is a single binary that manages the full operational lifecycle of a PostgreSQL cluster:

- Creates and verifies backups using `pg_basebackup` and `pg_verifybackup`
- Maintains a stable single-DSN proxy endpoint so applications never need to track which node is primary
- Runs lease-based leader election with fencing tokens to prevent split-brain
- Detects primary failures and promotes the best replica automatically (when enabled)
- Reseeds replicas and rewinds old primaries after failover using `pg_rewind`
- Enforces retention policies and keeps a manifest for every backup
- Exposes structured logs, Prometheus metrics, and a full audit trail

## What toris is not

- Not a connection pooler (use pgBouncer in front of the toris proxy if needed)
- Not a multi-primary write system — v1 supports exactly one writable primary per cluster
- Not a replacement for proper WAL archiving — use `archive_command` alongside toris for PITR
- Not a cloud-managed service — toris runs on your VPS or bare metal alongside your cluster

---

## How the single endpoint works

```
application → localhost:5433 (toris proxy)
                    ↓
              toris routing layer (atomic target update on failover)
                    ↓
              pg-primary.example.com:5432 (current primary)
```

`toris daemon` listens on a configurable address (default `127.0.0.1:5433`) and forwards
all TCP connections to the current primary. When failover occurs:

1. The old primary is fenced (connections terminated, write access blocked)
2. The routing target is atomically swapped to the
3. New connections go to the new primary immediately
4. In-flight connections on the old primary are terminated gracefully

Applications need only one DSN: `host=127.0.0.1 port=5433`. toris handles everything else.

---

## How leader election works

toris uses **PostgreSQL-backed lease records** stored in a dedicated control database.

```
toris instance → acquires/renews lease in toris_control.leases
                 ↕
             generation (fencing token) increments on every acquisition
                 ↕
             all cluster-mutating operations must carry the current generation
```

Key properties:
- **Durable**: lease state survives toris restarts (stored in PostgreSQL, not memory)
- **Fenced**: every state-changing operation checks the fencing token — stale workers are rejected
- **Single writer**: only one toris instance holds the lease per cluster at any time
- **TTL-gated takeover**: a new instance can only take over after the TTL expires or explicit release

---

## How backup integrity is guaranteed

Every backup follows a strict pipeline:

```
preflight → pg_basebackup → manifest (SHA-256 per artifact) → pg_verifybackup → [offsite copy]
```

A backup is only marked `verified` if `pg_verifybackup` exits 0. The manifest file includes:
- SHA-256 hash of every artifact
- WAL start/stop LSN
- PostgreSQL version
- Cluster and node identity
- A **self-hash** (SHA-256 of the manifest itself) to detect tampering

A backup in `failed` state is **never used for restore** and never automatically pruned (configurable).

---

## How restore and failover are performed

### Restore

```
toris restore --backup-id <id> --target-dir /var/lib/postgresql/data
```

1. Verifies the backup manifest and self-hash
2. Extracts artifacts to the target directory
3. Starts PostgreSQL (if `restore.start_after_restore: true`)
4. Runs health probes to confirm the restore succeeded

### Failover

```
# Automatic (if failover.enabled: true in config)
# Manual:
toris promote --node <replica-id> --force
```

1. Health checks confirm primary has been unhealthy beyond `unhealthy_threshold`
2. Lease generation is advanced (fencing token incremented)
3. Old primary is fenced: active connections terminated, write access blocked
4. Best candidate replica is selected (lowest lag, passing health checks)
5. `pg_promote()` is called on the replica
6. Routing target is atomically updated
7. Old primary is flagged for `pg_rewind` or full reseed before rejoining

---

## Quick start

```bash
# 1. Write a starter config
toris init --out toris.yaml

# 2. Edit toris.yaml (set nodes, control_dsn, auth profiles)

# 3. Validate the config
toris config validate

# 4. Run the doctor check
toris doctor

# 5. Check cluster health
toris health --config toris.yaml

# 6. Create a backup
toris backup create --config toris.yaml

# 7. Verify the backup
toris backup verify <backup-id>

# 8. Start the daemon (proxy + health loop + lease)
toris daemon --config toris.yaml
```

---

## Running the daemon

```bash
toris daemon --config /etc/toris/toris.yaml --log-level info
```

The daemon:
- Acquires the cluster lease on startup
- Runs the renewal loop every `leader.renew_interval`
- Starts the TCP proxy on `proxy.listen_addr`
- Runs health checks on all nodes on a configurable schedule
- Triggers failover if enabled and conditions are met
- Exposes Prometheus metrics on `metrics.listen_addr`

Graceful shutdown on SIGTERM/SIGINT: releases the lease, closes the proxy, waits for in-flight connections.

---

## Dry run

Every destructive command supports `--dry-run`:

```bash
toris backup create --dry-run   # runs preflight only, no pg_basebackup
toris promote --node node-02 --dry-run
toris rewind --data-dir /var/lib/postgresql/data --dry-run
```

---

## Configuration

See `toris init` for a fully commented starter config, and `docs/config.md` for
the complete reference.

---

## Exit codes

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | General error (see stderr) |
| 2    | Config validation failed |
| 3    | Health check failed |
| 4    | Backup failed |
| 5    | Restore failed |
| 6    | Failover failed |
| 7    | Lease conflict |

---

## Development

```bash
make build        # compile bin/toris
make test         # run all tests
make lint         # run golangci-lint
make fmt          # gofmt + goimports
```

Requirements: Go 1.22+, PostgreSQL client tools in PATH for integration tests.
