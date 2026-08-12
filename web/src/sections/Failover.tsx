import { IconFailover } from '../components/icons'

const STEPS = [
  {
    n: '01',
    title: 'Detect',
    body: 'L1–L5 health checks probe TCP, pg_isready, queries, recovery state, and policy. A primary is only declared unhealthy after the threshold is exceeded.',
  },
  {
    n: '02',
    title: 'Fence',
    body: 'The old primary is fenced: active connections terminated, writes blocked, and the lease generation advanced so its tokens go stale.',
  },
  {
    n: '03',
    title: 'Promote',
    body: 'The best candidate replica is promoted via pg_promote(). toris picks the freshest replica by replication lag.',
  },
  {
    n: '04',
    title: 'Route',
    body: 'The proxy target is swapped atomically. New connections land on the new primary immediately — your app’s DSN never changes.',
  },
]

export function Failover() {
  return (
    <section
      id="failover"
      className="border-y border-neutral-200 bg-neutral-50 dark:border-neutral-800 dark:bg-neutral-900/40"
    >
      <div className="mx-auto max-w-6xl px-4 py-20 sm:px-6 sm:py-28 lg:px-8">
        <div className="flex flex-col gap-6 sm:flex-row sm:items-end sm:justify-between">
          <div className="max-w-2xl">
            <p className="flex items-center gap-2 text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
              <IconFailover className="h-4 w-4" />
              Automatic failover
            </p>
            <h2 className="mt-4 text-3xl font-semibold tracking-tight sm:text-4xl">
              When the primary dies,
              <br />
              nobody notices
            </h2>
          </div>
          <p className="max-w-sm text-[15px] leading-relaxed text-neutral-600 dark:text-neutral-400">
            A lost replica? No failover — toris marks it degraded and keeps
            going. A lost primary? Fence, promote, route. Seconds, not
            fire drills.
          </p>
        </div>

        <ol className="mt-14 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          {STEPS.map((s) => (
            <li
              key={s.n}
              className="rounded-2xl border border-neutral-200 bg-white p-6 dark:border-neutral-800 dark:bg-neutral-950"
            >
              <span className="font-mono text-sm text-neutral-400 dark:text-neutral-600">
                {s.n}
              </span>
              <h3 className="mt-3 text-base font-semibold tracking-tight">{s.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-neutral-600 dark:text-neutral-400">
                {s.body}
              </p>
            </li>
          ))}
        </ol>
      </div>
    </section>
  )
}
