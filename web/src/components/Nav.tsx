import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'

const GITHUB = 'https://github.com/tobibamidele/toris'

const NAV_LINKS = [
  { to: '/', label: 'Home', end: true },
  { to: '/docs', label: 'Docs', end: false },
]

function MenuIcon({ open }: { open: boolean }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      aria-hidden="true"
      className="h-5 w-5"
    >
      {open ? <path d="M6 6l12 12M18 6L6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
    </svg>
  )
}

export function Nav() {
  const [open, setOpen] = useState(false)
  const location = useLocation()

  useEffect(() => setOpen(false), [location.pathname])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  return (
    <header className="sticky top-0 z-50 border-b border-neutral-200/80 bg-white/85 backdrop-blur-md dark:border-neutral-800/80 dark:bg-neutral-950/80">
      <nav
        className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6 lg:px-8"
        aria-label="Primary"
      >
        <Link to="/" className="pressable -mx-2 rounded-lg p-2" aria-label="toris home">
          <Logo />
        </Link>

        <div className="hidden items-center gap-1 md:flex">
          {NAV_LINKS.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              end={l.end}
              className={({ isActive }) =>
                `rounded-full px-4 py-2 text-sm font-medium transition-colors duration-150 ${
                  isActive
                    ? 'bg-neutral-100 text-black dark:bg-neutral-800/70 dark:text-white'
                    : 'text-neutral-600 hover:text-black dark:text-neutral-400 dark:hover:text-white'
                }`
              }
            >
              {l.label}
            </NavLink>
          ))}
          <a
            href={GITHUB}
            target="_blank"
            rel="noreferrer"
            className="rounded-full px-4 py-2 text-sm font-medium text-neutral-600 transition-colors duration-150 hover:text-black dark:text-neutral-400 dark:hover:text-white"
          >
            GitHub
          </a>
        </div>

        <div className="hidden items-center gap-3 md:flex">
          <ThemeToggle />
          <Link
            to="/docs/getting-started"
            className="pressable rounded-full bg-black px-4 py-2 text-sm font-medium text-white hover:opacity-90 dark:bg-white dark:text-black dark:hover:opacity-90"
          >
            Get started
          </Link>
        </div>

        <div className="flex items-center gap-2 md:hidden">
          <ThemeToggle />
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            aria-controls="mobile-menu"
            aria-label={open ? 'Close menu' : 'Open menu'}
            className="pressable -mr-2 inline-flex h-9 w-9 items-center justify-center rounded-full border border-neutral-200 text-neutral-600 dark:border-neutral-800 dark:text-neutral-400"
          >
            <MenuIcon open={open} />
          </button>
        </div>
      </nav>

      {open && (
        <div
          id="mobile-menu"
          className="border-t border-neutral-200 bg-white px-4 pb-4 pt-2 md:hidden dark:border-neutral-800 dark:bg-neutral-950"
        >
          <div className="flex flex-col">
            {NAV_LINKS.map((l) => (
              <NavLink
                key={l.to}
                to={l.to}
                end={l.end}
                className={({ isActive }) =>
                  `rounded-lg px-3 py-3 text-[15px] font-medium ${
                    isActive
                      ? 'bg-neutral-100 text-black dark:bg-neutral-800/70 dark:text-white'
                      : 'text-neutral-700 dark:text-neutral-300'
                  }`
                }
              >
                {l.label}
              </NavLink>
            ))}
            <a
              href={GITHUB}
              target="_blank"
              rel="noreferrer"
              className="rounded-lg px-3 py-3 text-[15px] font-medium text-neutral-700 dark:text-neutral-300"
            >
              GitHub
            </a>
            <Link
              to="/docs/getting-started"
              className="pressable mt-2 rounded-full bg-black px-4 py-3 text-center text-[15px] font-medium text-white dark:bg-white dark:text-black"
            >
              Get started
            </Link>
          </div>
        </div>
      )}
    </header>
  )
}
