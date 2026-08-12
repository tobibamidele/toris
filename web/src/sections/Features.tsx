import type { ComponentType, SVGProps } from 'react'
import {
  IconBackup,
  IconFailover,
  IconLeader,
  IconObservability,
  IconReplication,
  IconRestore,
} from '../components/icons'

interface Feature {
  icon: ComponentType<SVGProps<SVGSVGElement>>
  title: string
  body: string
}

const FEATURES: Feature[] = [
  {
    icon: IconFailover,
    title: 'Automatic failover',
    body: 'Detects primary failure across five health layers and promotes the best replica — fence first, route second, so only one node ever accepts writes.',
  },
  {
    icon: IconLeader,
    title: 'Leader election',
    body: 'Lease-based election with fencing tokens. A stale generation is rejected outright — split-brain is designed out, not patched around.',
  },
  {
    icon: IconReplication,
    title: 'Streaming replication',
    body: 'Reseeds replicas from verified backups, rewinds demoted primaries with pg_rewind, and re-joins nodes automatically after recovery.',
  },
  {
    icon: IconBackup,
    title: 'Verified backups',
    body: 'pg_basebackup plus pg_verifybackup, with a tamper-evident SHA-256 manifest per backup. A backup only counts once it verifies.',
  },
  {
    icon: IconRestore,
    title: 'Restore & retention',
    body: 'Manifest-verified restores, artifact SHA-256 checks before extraction, and retention policies that prune on a schedule.',
  },
  {
    icon: IconObservability,
    title: 'Observability & audit',
    body: 'Structured logs, Prometheus metrics, a /healthz endpoint, and an append-only audit trail of every operational action.',
  },
]

export function Features() {
  return (
    <section id="features" className="mx-auto max-w-6xl px-4 py-20 sm:px-6 sm:py-28 lg:px-8">
      <div className="max-w-2xl">
        <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
          What toris does
        </p>
        <h2 className="mt-4 text-3xl font-semibold tracking-tight sm:text-4xl">
          The operational lifecycle of a PostgreSQL cluster, in one binary
        </h2>
        <p className="mt-5 text-lg leading-relaxed text-neutral-600 dark:text-neutral-400">
          toris runs on your VPS or bare metal and takes care of the parts of
          Postgres operations that are easy to get wrong.
        </p>
      </div>

      <div className="mt-14 grid gap-px overflow-hidden rounded-2xl border border-neutral-200 bg-neutral-200 sm:grid-cols-2 lg:grid-cols-3 dark:border-neutral-800 dark:bg-neutral-800">
        {FEATURES.map((f) => (
          <div
            key={f.title}
            className="group bg-white p-7 transition-colors duration-150 hover:bg-neutral-50 dark:bg-neutral-950 dark:hover:bg-neutral-900/60"
          >
            <div className="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-neutral-200 text-neutral-700 transition-colors duration-150 group-hover:border-black group-hover:text-black dark:border-neutral-800 dark:text-neutral-300 dark:group-hover:border-white dark:group-hover:text-white">
              <f.icon className="h-5 w-5" />
            </div>
            <h3 className="mt-5 text-lg font-semibold tracking-tight">{f.title}</h3>
            <p className="mt-2 text-[15px] leading-relaxed text-neutral-600 dark:text-neutral-400">
              {f.body}
            </p>
          </div>
        ))}
      </div>
    </section>
  )
}
