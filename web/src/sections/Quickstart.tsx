import { Code } from '../components/Code'
import { IconCheck } from '../components/icons'

const QUICKSTART = `# 1. Install — a single binary, no agents
go install github.com/tobibamidele/toris/cmd/toris@latest

# 2. Write a starter config
toris init --out toris.yaml

# 3. Validate the config, then diagnose the environment
toris config validate
toris doctor

# 4. Run the daemon: proxy + lease + health + metrics
toris daemon --config toris.yaml`

const CHECKLIST = [
  ['Stable endpoint', 'The proxy listens on 127.0.0.1:5433 and forwards to the live primary.'],
  ['Drop-in ready', 'Point your existing connection string at toris — no client changes.'],
  ['Observable', 'Prometheus /metrics and /healthz out of the box.'],
  ['Safe by default', 'Every destructive command supports --dry-run.'],
]

export function Quickstart() {
  return (
    <section
      id="quickstart"
      className="border-y border-neutral-200 dark:border-neutral-800"
    >
      <div className="mx-auto grid max-w-6xl gap-12 px-4 py-20 sm:px-6 sm:py-28 lg:grid-cols-2 lg:items-start lg:gap-20 lg:px-8">
        <div>
          <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
            Quick start
          </p>
          <h2 className="mt-4 text-3xl font-semibold tracking-tight sm:text-4xl">
            Up and running in four commands
          </h2>
          <p className="mt-5 text-lg leading-relaxed text-neutral-600 dark:text-neutral-400">
            toris is one binary with no agents to install on your nodes. Point
            it at your cluster, give it a control database, and the daemon
            takes over the operations loop.
          </p>
          <ul className="mt-8 space-y-5">
            {CHECKLIST.map(([title, body]) => (
              <li key={title} className="flex gap-3">
                <span className="mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full border border-neutral-300 dark:border-neutral-700">
                  <IconCheck className="h-3 w-3" />
                </span>
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

        <div>
          <Code code={QUICKSTART} lang="bash" title="quickstart" />
          <div className="mt-6 grid grid-cols-3 divide-x divide-neutral-200 rounded-2xl border border-neutral-200 dark:divide-neutral-800 dark:border-neutral-800">
            {[
              ['1', 'binary'],
              ['5433', 'one port'],
              ['0', 'downtime'],
            ].map(([v, l]) => (
              <div key={l} className="px-4 py-4 text-center">
                <p className="font-mono text-xl font-semibold tracking-tight">{v}</p>
                <p className="mt-0.5 text-xs text-neutral-500 dark:text-neutral-400">{l}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
