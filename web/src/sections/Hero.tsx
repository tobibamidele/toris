import { Link } from 'react-router-dom'
import { ClusterGlobe } from '../components/Globe'
import { IconArrowRight, IconCheck } from '../components/icons'
import { DsnBar } from './DsnBar'

const HERO_POINTS = ['Automatic failover', 'Verified backups', 'Single binary']

export function Hero() {
  return (
    <section className="relative overflow-hidden">
      <div className="mx-auto grid max-w-6xl items-center gap-12 px-4 pb-16 pt-16 sm:px-6 sm:pt-24 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] lg:gap-8 lg:pb-24 lg:pt-20 lg:px-8">
        <div className="max-w-xl">
          <p
            className="rise-in inline-flex items-center gap-2 rounded-full border border-neutral-200 px-3.5 py-1.5 text-xs font-medium text-neutral-600 dark:border-neutral-800 dark:text-neutral-400"
            style={{ animationDelay: '0ms' }}
          >
            <span className="inline-block h-1.5 w-1.5 rounded-full bg-black dark:bg-white" />
            Open source · PostgreSQL orchestration
          </p>

          <h1
            className="rise-in mt-6 text-5xl font-semibold leading-[1.02] tracking-tight sm:text-6xl lg:text-[4.25rem]"
            style={{ animationDelay: '80ms' }}
          >
            One DSN.
            <br />
            <span className="text-transparent [-webkit-text-stroke:1.5px_currentColor]">
              That&rsquo;s it.
            </span>
          </h1>

          <p
            className="rise-in mt-6 text-lg leading-relaxed text-neutral-600 dark:text-neutral-400"
            style={{ animationDelay: '160ms' }}
          >
            toris is a single binary that runs your PostgreSQL cluster —
            streaming replication, automatic failover, verified backups, and
            restores. Your application connects to one endpoint and never
            thinks about which node is primary. Everything else happens in the
            background.
          </p>

          <div
            className="rise-in mt-8 flex flex-wrap items-center gap-3"
            style={{ animationDelay: '240ms' }}
          >
            <Link
              to="/docs/getting-started"
              className="pressable inline-flex h-11 items-center gap-2 rounded-full bg-black px-6 text-sm font-medium text-white hover:opacity-90 dark:bg-white dark:text-black"
            >
              Get started
              <IconArrowRight className="h-4 w-4" />
            </Link>
            <Link
              to="/docs"
              className="pressable inline-flex h-11 items-center rounded-full border border-neutral-300 px-6 text-sm font-medium hover:border-black dark:border-neutral-700 dark:hover:border-white"
            >
              Read the docs
            </Link>
          </div>

          <ul
            className="rise-in mt-10 flex flex-wrap items-center gap-x-5 gap-y-2"
            style={{ animationDelay: '320ms' }}
          >
            {HERO_POINTS.map((point) => (
              <li
                key={point}
                className="flex items-center gap-1.5 text-sm text-neutral-600 dark:text-neutral-400"
              >
                <IconCheck className="h-3.5 w-3.5" />
                {point}
              </li>
            ))}
          </ul>
        </div>

        <div className="rise-in relative" style={{ animationDelay: '200ms' }}>
          <div className="absolute inset-0 -z-10 rounded-full bg-[radial-gradient(closest-side,rgba(0,0,0,0.06),transparent)] dark:bg-[radial-gradient(closest-side,rgba(255,255,255,0.07),transparent)]" />
          <ClusterGlobe className="h-[360px] w-full cursor-grab active:cursor-grabbing sm:h-[480px] lg:h-[560px]" />
        </div>
      </div>

      <DsnBar />
    </section>
  )
}
