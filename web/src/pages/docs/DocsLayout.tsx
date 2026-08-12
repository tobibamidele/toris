import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { DOCS, findDoc } from '../../lib/docs'
import { IconArrowRight } from '../../components/icons'

function Sidebar() {
  return (
    <nav
      aria-label="Documentation"
      className="w-full lg:w-60 lg:shrink-0"
    >
      <p className="px-3 text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
        Docs
      </p>
      <ul className="mt-3 space-y-1">
        {DOCS.map((d) => (
          <li key={d.to}>
            <NavLink
              to={d.to}
              className={({ isActive }) =>
                `block rounded-lg px-3 py-2 text-sm font-medium transition-colors duration-150 ${
                  isActive
                    ? 'bg-neutral-100 text-black dark:bg-neutral-800/70 dark:text-white'
                    : 'text-neutral-600 hover:text-black dark:text-neutral-400 dark:hover:text-white'
                }`
              }
            >
              {d.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}

export function DocsLayout() {
  const location = useLocation()
  const current = findDoc(location.pathname)

  return (
    <div className="mx-auto max-w-6xl px-4 py-12 sm:px-6 sm:py-16 lg:px-8">
      <div className="grid gap-10 lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-16">
        <div className="hidden lg:block">
          <div className="sticky top-24">
            <Sidebar />
          </div>
        </div>

        <div className="min-w-0">
          <div className="lg:hidden">
            <label htmlFor="docs-select" className="sr-only">
              Select a documentation page
            </label>
            <select
              id="docs-select"
              value={location.pathname}
              onChange={(e) => {
                window.location.href = e.target.value
              }}
              className="w-full rounded-xl border border-neutral-200 bg-white px-3 py-2.5 text-sm font-medium text-black dark:border-neutral-800 dark:bg-neutral-950 dark:text-white"
            >
              {DOCS.map((d) => (
                <option key={d.to} value={d.to}>
                  {d.label}
                </option>
              ))}
            </select>
          </div>

          <article className="mt-8 lg:mt-0">
            <Outlet />
          </article>

          {current && (
            <nav
              aria-label="Docs pagination"
              className="mt-16 flex items-center justify-between gap-4 border-t border-neutral-200 pt-6 dark:border-neutral-800"
            >
              {current.prev ? (
                <Link
                  to={current.prev}
                  className="group flex max-w-[45%] flex-col rounded-xl px-3 py-2 text-sm hover:bg-neutral-50 dark:hover:bg-neutral-900/60"
                >
                  <span className="text-xs text-neutral-500 dark:text-neutral-500">
                    Previous
                  </span>
                  <span className="mt-1 font-medium">
                    {findDoc(current.prev)?.label}
                  </span>
                </Link>
              ) : (
                <span />
              )}
              {current.next ? (
                <Link
                  to={current.next}
                  className="group flex max-w-[45%] flex-col rounded-xl px-3 py-2 text-right text-sm hover:bg-neutral-50 dark:hover:bg-neutral-900/60"
                >
                  <span className="text-xs text-neutral-500 dark:text-neutral-500">
                    Next
                  </span>
                  <span className="mt-1 flex items-center justify-end gap-1.5 font-medium">
                    {findDoc(current.next)?.label}
                    <IconArrowRight className="h-3.5 w-3.5 transition-transform duration-150 group-hover:translate-x-0.5" />
                  </span>
                </Link>
              ) : (
                <span />
              )}
            </nav>
          )}
        </div>
      </div>
    </div>
  )
}
