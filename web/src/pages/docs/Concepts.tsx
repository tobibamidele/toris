import {
  Callout,
  DocCode,
  DocH1,
  DocH2,
  DocH3,
  DocList,
  DocP,
  Mono,
} from '../../components/doc'

const DSN = `application --> 127.0.0.1:5433 (toris proxy)
                     |
              toris routing layer
              (atomic swap on failover)
                     |
              pg-primary.example.com:5432`

export function Concepts() {
  return (
    <>
      <DocH1>Concepts & failover</DocH1>
      <DocP>
        toris is organized around a single idea: your application should never
        have to track which node is primary. Everything below exists to keep
        that promise.
      </DocP>

      <DocH2 id="single-endpoint">The single endpoint</DocH2>
      <DocP>
        <Mono>toris daemon</Mono> listens on a configurable address (default{' '}
        <Mono>127.0.0.1:5433</Mono>) and forwards every TCP connection to the
        current primary:
      </DocP>
      <DocCode code={DSN} lang="text" title="routing" />
      <DocP>When failover occurs:</DocP>
      <DocList
        items={[
          <>
            The old primary is <strong>fenced</strong>: active connections
            terminated, writes blocked.
          </>,
          <>
            The routing target is <strong>atomically swapped</strong> to the
            new primary.
          </>,
          <>
            New connections go to the new primary{' '}
            <strong>immediately</strong>.
          </>,
          <>
            The old primary is scheduled for <Mono>pg_rewind</Mono> or a full
            reseed.
          </>,
        ]}
      />
      <Callout tone="info">
        <strong>One DSN, always:</strong> <Mono>host=127.0.0.1 port=5433</Mono>.
        Applications need nothing else — no failover-aware drivers, no DNS
        tricks.
      </Callout>

      <DocH2 id="architecture">Architecture</DocH2>
      <DocP>
        The codebase follows a four-plane layout, from the CLI down to storage.
        Dependencies flow one way; circular dependencies between planes are
        prohibited.
      </DocP>
      <DocCode
        code={`CLI (cobra dispatch)
  → Control Plane  (leader election, health checks, failover engine)
    → Data Plane   (proxy, backup/restore/reseed/rewind execution)
      → Storage & Metadata (filesystem or S3, control database)`}
        lang="text"
        title="planes"
      />

      <DocH2 id="leader-election">Leader election & fencing tokens</DocH2>
      <DocP>
        toris uses <strong>lease-based leader election</strong> against the
        control database. The lease has a TTL; the holder renews it on every{' '}
        <Mono>renew_interval</Mono>. Every acquisition increments the lease{' '}
        <strong>generation</strong> — this is the fencing token.
      </DocP>
      <DocP>
        All mutating operations receive the current generation and reject stale
        tokens. If an old primary resurfaces after a failover, its stale
        generation is rejected by the routing layer, and its writes are blocked.
        This is how split-brain is prevented by design.
      </DocP>
      <DocCode
        code={`# See the current lease holder and generation
toris leader status

# Manually acquire or release the cluster lease
toris leader acquire
toris leader release`}
        lang="bash"
        title="lease"
      />

      <DocH2 id="health">Health checks: L1–L5</DocH2>
      <DocP>
        The health loop checks every node on a 10-second interval, working
        through five increasingly deep layers. A node is only marked unhealthy
        once the relevant threshold is exceeded.
      </DocP>
      <DocCode
        code={`L1  TCP connect
L2  pg_isready
L3  SELECT 1
L4  pg_is_in_recovery
L5  policy checks (replication lag, retention, etc.)`}
        lang="text"
        title="layers"
      />

      <DocH2 id="failure-classes">Failure classes</DocH2>
      <DocH3>Class A — primary loses replica connectivity only</DocH3>
      <DocP>
        The primary is marked degraded and a replication outage timer starts.{' '}
        <strong>Failover is not triggered.</strong> Only if all replicas remain
        unreachable beyond <Mono>replication_outage_threshold</Mono> is the
        situation escalated.
      </DocP>
      <DocH3>Class B — primary loses lease renewal (control-plane connectivity)</DocH3>
      <DocP>
        The lease TTL expires naturally. Another instance acquires a higher
        generation; the old primary&rsquo;s generation is now stale and it is
        blocked from writes. The daemon shuts down cleanly.
      </DocP>

      <DocH2 id="failover-flow">The failover flow</DocH2>
      <DocP>
        Fence first, route second — the old primary is always fenced and its
        connections terminated before any promotion attempt, and the routing
        target is only updated after promotion succeeds.
      </DocP>
      <DocCode
        code={`1. Detect      L1–L5 checks exceed unhealthy_threshold
2. Fence       connections terminated, writes blocked,
               lease generation advanced
3. Promote     best candidate replica promoted via pg_promote()
4. Route       proxy target atomically updated
5. Recover     old primary scheduled for pg_rewind;
               falls back to reseed if rewind fails`}
        lang="text"
        title="flow"
      />
      <Callout>
        With <Mono>failover.enabled: false</Mono>, toris still handles backups,
        reseeds, rewinds, and routing — it just never promotes on its own.
      </Callout>
    </>
  )
}
