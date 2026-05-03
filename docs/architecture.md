# toris — Architecture

## Overview

toris is structured as four layered planes with strict package boundaries:

```
┌─────────────────────────────────────────────────────────┐
│                     CLI Layer                           │
│   cmd/toris/main.go → internal/cli/{root,commands}.go  │
│   (parses flags, loads config, dispatches, formats)     │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                  Control Plane                          │
│   internal/leader    — lease acquisition and fencing    │
│   internal/cluster   — node registry and discovery      │
│   internal/failover  — failover decision engine         │
│   internal/health    — layered L1–L5 health checks      │
│   internal/routing   — stable endpoint selection        │
│   internal/audit     — immutable audit event log        │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                   Data Plane                            │
│   internal/backup    — backup pipeline                  │
│   internal/restore   — restore and reseed               │
│   internal/db/       — database backend abstraction     │
│   internal/exec      — safe subprocess wrapper          │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│              Storage / Metadata Layer                   │
│   internal/manifest  — backup manifest R/W/verify       │
│   internal/retention — retention policy enforcement     │
│   internal/storage/  — fs + s3 storage backends         │
│   toris_control DB   — lease records, manifests, audit  │
└─────────────────────────────────────────────────────────┘
```

---

## Key design decisions

### No in-memory leader election

The lease record lives in PostgreSQL (`toris_control.leases`). Every acquisition
is an atomic `INSERT ... ON CONFLICT DO UPDATE ... WHERE expires_at < now`. This
means:

- A toris restart never causes split-brain
- Two toris instances racing to acquire the lease will have exactly one winner
- The losing instance gets a `LEASE_CONFLICT` error, not a silent wrong answer

### Fencing tokens

Every lease has a `generation` field that increments monotonically on acquisition.
Every cluster-mutating operation (promote, fence, backup, restore) receives the
current generation and rejects any call that carries a stale token. This is the
**only** correct way to prevent stale operators from acting after a lease change.

```
generation 4 held by instance A
    ↓ A crashes
generation 5 acquired by instance B
    ↓ A recovers, tries to promote with token 4
    → FENCING_VIOLATION error — operation rejected
```

### Layered health model

```
L1  TCP connect     — is the host/port reachable?
L2  pg_isready      — is PostgreSQL accepting connections?
L3  SELECT 1        — is the SQL layer alive?
L4  pg_is_in_recovery() — what role is this node?
L5  policy checks   — lag, disk, WAL continuity, backup freshness
```

A node is only considered healthy for writes if it passes L1–L5 as a primary.
A replica is healthy if it passes L1–L4 and its lag is within tolerance.

### Backup pipeline is append-only until verified

```
pending → running → [verified or failed]
```

A backup in `failed` state is never used for restore. A backup is only marked
`verified` after `pg_verifybackup` exits successfully. The manifest self-hash
detects any post-write tampering.

### TCP proxy for single DSN

The routing layer is a plain TCP bidirectional bridge. It does not speak the
PostgreSQL wire protocol — it just forwards bytes. This means:

- TLS is handled end-to-end between the client and the primary (toris does not MITM)
- Authentication happens directly between the client and PostgreSQL
- The routing target can be swapped atomically without dropping the listener

---

## State machines

### Node lifecycle

```
unknown → joining → healthy → degraded → unhealthy → draining → fenced → removed
                       ↑           ↓          ↑
                       └─ (healed) ┘       (recovered)
```

### Backup lifecycle

```
pending → running → verified → [uploaded] → retained → pruned
                 ↓
              failed (kept for forensics unless explicitly pruned)
```

### Leader lease lifecycle

```
(no lease) → active → [expired | released]
                ↑           ↓
                └── (re-acquired after TTL)
```

### Failover lifecycle

```
detected → fenced → promoted → routed → stabilized → reconciled
                 ↓                                ↓
               failed                      (old primary reseeded or rewound)
```

---

## Control database schema

toris stores its own state in a dedicated schema within a PostgreSQL instance:

```sql
-- Cluster lease (one row per managed cluster)
CREATE TABLE toris_control.leases (
    cluster_id     TEXT PRIMARY KEY,
    instance_id    TEXT NOT NULL,    -- which toris daemon holds the lease
    leader_id      TEXT NOT NULL,    -- which DB node is the elected primary
    generation     BIGINT NOT NULL,  -- fencing token
    status         TEXT NOT NULL,
    acquired_at    TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    last_heartbeat TIMESTAMPTZ NOT NULL,
    released_at    TIMESTAMPTZ
);

-- Backup records
CREATE TABLE toris_control.backups ( ... );

-- Audit log (append-only)
CREATE TABLE toris_control.audit_events ( ... );
```

The control database must be separate from your cluster nodes so toris can
operate during node failures.

---

## Package boundaries

| Package | Owns |
|---|---|
| `internal/db/postgres` | All pgx/pg_* code |
| `internal/db/interface.go` | The `Backend` interface only |
| `internal/leader` | Lease acquisition, renewal, fencing |
| `internal/routing` | TCP proxy, target selection |
| `internal/backup` | Backup pipeline (no DB-specific code) |
| `internal/restore` | Restore and reseed (no DB-specific code) |
| `internal/manifest` | Manifest R/W, SHA-256 verification |
| `internal/exec` | All subprocess invocations |
| `internal/config` | Config struct, loader, validator |
| `internal/cli` | Cobra commands only — no business logic |
| `pkg/model` | Shared data types (no business logic) |

Circular dependencies are prohibited. The CLI layer never calls `internal/db` directly — it always goes through the control or data plane.
