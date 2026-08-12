import { IconArrowRight, IconGlobe } from '../components/icons'

function DownArrow() {
  return (
    <div className="flex justify-center" aria-hidden="true">
      <svg
        viewBox="0 0 16 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        className="h-6 w-4 text-neutral-400 dark:text-neutral-600"
      >
        <path d="M8 1v20M2 15l6 6 6-6" />
      </svg>
    </div>
  )
}

function NodeCard({
  name,
  role,
  active,
}: {
  name: string
  role: string
  active?: boolean
}) {
  return (
    <div
      className={`flex items-center gap-3 rounded-xl border px-4 py-3 ${
        active
          ? 'border-black bg-black text-white dark:border-white dark:bg-white dark:text-black'
          : 'border-neutral-200 bg-white text-black dark:border-neutral-800 dark:bg-neutral-900 dark:text-white'
      }`}
    >
      <span
        className={`inline-block h-2 w-2 rounded-full ${
          active ? 'bg-white dark:bg-black' : 'bg-neutral-400 dark:bg-neutral-600'
        }`}
      />
      <span className="font-mono text-sm">{name}</span>
      <span
        className={`ml-auto font-mono text-[11px] uppercase tracking-wider ${
          active ? 'opacity-80' : 'text-neutral-500 dark:text-neutral-500'
        }`}
      >
        {role}
      </span>
    </div>
  )
}

export function OneDsn() {
  return (
    <section id="one-dsn" className="border-y border-neutral-200 dark:border-neutral-800">
      <div className="mx-auto grid max-w-6xl items-center gap-14 px-4 py-20 sm:px-6 sm:py-28 lg:grid-cols-2 lg:gap-20 lg:px-8">
        <div>
          <p className="flex items-center gap-2 text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
            <IconGlobe className="h-4 w-4" />
            The single endpoint
          </p>
          <h2 className="mt-4 text-3xl font-semibold tracking-tight sm:text-4xl">
            Connect once.
            <br />
            Forget the rest.
          </h2>
          <p className="mt-5 text-lg leading-relaxed text-neutral-600 dark:text-neutral-400">
            toris publishes exactly one DSN and keeps it stable through
            failovers, rewinds, and reseeds. The proxy forwards every
            connection to whichever node is primary right now, and swaps the
            target atomically when a failover happens.
          </p>
          <ul className="mt-8 space-y-4">
            {[
              ['No DNS changes', 'The endpoint never moves, even when the primary changes.'],
              ['No connection logic', 'Your app talks to one host and port — no failover-aware clients.'],
              ['No split-brain', 'Fencing tokens make sure only one primary accepts writes at a time.'],
            ].map(([title, body]) => (
              <li key={title} className="flex gap-3">
                <span className="mt-2 inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-black dark:bg-white" />
                <div>
                  <p className="font-medium">{title}</p>
                  <p className="text-sm text-neutral-600 dark:text-neutral-400">
                    {body}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <div className="mx-auto w-full max-w-sm">
          <NodeCard name="app" role="application" />
          <div className="py-1 text-center font-mono text-[11px] text-neutral-400 dark:text-neutral-600">
            port 5433
          </div>
          <DownArrow />
          <NodeCard name="toris proxy" role="routing layer" />
          <div className="relative py-1 text-center">
            <span className="inline-flex items-center gap-1.5 rounded-full border border-neutral-200 px-2.5 py-0.5 font-mono text-[11px] text-neutral-500 dark:border-neutral-800 dark:text-neutral-400">
              atomic swap on failover
            </span>
          </div>
          <DownArrow />
          <NodeCard name="node-01" role="primary" active />
          <div className="grid grid-cols-2 gap-4 pt-4">
            <NodeCard name="node-02" role="replica" />
            <NodeCard name="node-03" role="replica" />
          </div>
          <p className="mt-6 flex items-center justify-center gap-1.5 text-sm text-neutral-500 dark:text-neutral-400">
            replicas stay in sync in the background
            <IconArrowRight className="h-3.5 w-3.5" />
          </p>
        </div>
      </div>
    </section>
  )
}
