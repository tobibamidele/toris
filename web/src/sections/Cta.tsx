import { Link } from 'react-router-dom'
import { IconArrowRight } from '../components/icons'

export function Cta() {
  return (
    <section className="border-t border-neutral-200 dark:border-neutral-800">
      <div className="mx-auto max-w-6xl px-4 py-24 text-center sm:px-6 sm:py-32 lg:px-8">
        <h2 className="text-4xl font-semibold tracking-tight sm:text-5xl">
          One DSN.
          <span className="text-transparent [-webkit-text-stroke:1.5px_currentColor]">
            {' '}
            That&rsquo;s it.
          </span>
        </h2>
        <p className="mx-auto mt-5 max-w-xl text-lg leading-relaxed text-neutral-600 dark:text-neutral-400">
          Give your applications one endpoint, and let toris handle the
          replication, failover, and backups in the background.
        </p>
        <div className="mt-9 flex flex-wrap items-center justify-center gap-3">
          <Link
            to="/docs/getting-started"
            className="pressable inline-flex h-12 items-center gap-2 rounded-full bg-black px-7 text-sm font-medium text-white hover:opacity-90 dark:bg-white dark:text-black"
          >
            Get started
            <IconArrowRight className="h-4 w-4" />
          </Link>
          <Link
            to="/docs"
            className="pressable inline-flex h-12 items-center rounded-full border border-neutral-300 px-7 text-sm font-medium hover:border-black dark:border-neutral-700 dark:hover:border-white"
          >
            Read the docs
          </Link>
        </div>
      </div>
    </section>
  )
}
