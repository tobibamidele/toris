import { Link } from 'react-router-dom'
import { Logo } from './Logo'
import { IconGitHub } from './icons'

const DOCS_LINKS = [
  { to: '/docs/getting-started', label: 'Getting started' },
  { to: '/docs/concepts', label: 'Concepts & failover' },
  { to: '/docs/cli', label: 'CLI reference' },
  { to: '/docs/configuration', label: 'Configuration' },
  { to: '/docs/operations', label: 'Backups & restore' },
]

export function Footer() {
  return (
    <footer className="border-t border-neutral-200 bg-neutral-50 dark:border-neutral-800 dark:bg-neutral-900/40">
      <div className="mx-auto max-w-6xl px-4 py-16 sm:px-6 lg:px-8">
        <div className="grid gap-12 md:grid-cols-[1.4fr_1fr_1fr]">
          <div>
            <Link to="/" className="inline-block" aria-label="toris home">
              <Logo />
            </Link>
            <p className="mt-4 max-w-sm text-sm leading-relaxed text-neutral-600 dark:text-neutral-400">
              Production-grade PostgreSQL backup, replication, failover, and
              restoration orchestration. One DSN, one binary, everything else
              in the background.
            </p>
            <a
              href="https://github.com/tobibamidele/toris"
              target="_blank"
              rel="noreferrer"
              className="pressable mt-5 inline-flex items-center gap-2 rounded-full border border-neutral-300 px-4 py-2 text-sm font-medium hover:border-black dark:border-neutral-700 dark:hover:border-white"
            >
              <IconGitHub className="h-4 w-4" />
              toris on GitHub
            </a>
          </div>

          <div>
            <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
              Documentation
            </p>
            <ul className="mt-4 space-y-2.5">
              {DOCS_LINKS.map((l) => (
                <li key={l.to}>
                  <Link
                    to={l.to}
                    className="text-sm text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
                  >
                    {l.label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>

          <div>
            <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
              Highlights
            </p>
            <ul className="mt-4 space-y-2.5">
              <li>
                <Link
                  to="/#one-dsn"
                  className="text-sm text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
                >
                  The single endpoint
                </Link>
              </li>
              <li>
                <Link
                  to="/#features"
                  className="text-sm text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
                >
                  Features
                </Link>
              </li>
              <li>
                <Link
                  to="/#failover"
                  className="text-sm text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
                >
                  How failover works
                </Link>
              </li>
              <li>
                <Link
                  to="/#quickstart"
                  className="text-sm text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
                >
                  Quick start
                </Link>
              </li>
            </ul>
          </div>
        </div>

        <div className="mt-14 flex flex-col items-center justify-between gap-3 border-t border-neutral-200 pt-8 text-sm text-neutral-500 sm:flex-row dark:border-neutral-800 dark:text-neutral-500">
          <p>© {new Date().getFullYear()} toris. Open source.</p>
          <p className="font-mono text-xs">
            one DSN · zero split-brain · verified everything
          </p>
        </div>
      </div>
    </footer>
  )
}
