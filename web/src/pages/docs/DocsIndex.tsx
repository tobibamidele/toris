import { Link } from 'react-router-dom'
import { DocH1, DocP } from '../../components/doc'
import { DOCS } from '../../lib/docs'
import { IconArrowRight } from '../../components/icons'

export function DocsIndex() {
  return (
    <>
      <DocH1>Documentation</DocH1>
      <DocP>
        Everything you need to run toris: installation, the operational model,
        every CLI command, and the full configuration reference.
      </DocP>
      <div className="mt-10 grid gap-4 sm:grid-cols-2">
        {DOCS.map((d) => (
          <Link
            key={d.to}
            to={d.to}
            className="pressable group rounded-2xl border border-neutral-200 p-6 transition-colors duration-150 hover:border-black dark:border-neutral-800 dark:hover:border-white"
          >
            <h2 className="text-base font-semibold tracking-tight">{d.label}</h2>
            <p className="mt-2 text-sm leading-relaxed text-neutral-600 dark:text-neutral-400">
              {d.desc}
            </p>
            <span className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium">
              Read
              <IconArrowRight className="h-3.5 w-3.5 transition-transform duration-150 group-hover:translate-x-0.5" />
            </span>
          </Link>
        ))}
      </div>
    </>
  )
}
