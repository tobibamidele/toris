import type { ReactNode } from 'react'
import { Code } from './Code'

export function DocH1({ children }: { children: ReactNode }) {
  return (
    <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
      {children}
    </h1>
  )
}

export function DocH2({
  children,
  id,
}: {
  children: ReactNode
  id?: string
}) {
  return (
    <h2
      id={id}
      className="mt-12 scroll-mt-24 text-xl font-semibold tracking-tight first:mt-0"
    >
      {children}
    </h2>
  )
}

export function DocH3({ children }: { children: ReactNode }) {
  return <h3 className="mt-8 text-base font-semibold tracking-tight">{children}</h3>
}

export function DocP({ children }: { children: ReactNode }) {
  return (
    <p className="mt-4 text-[15px] leading-relaxed text-neutral-700 dark:text-neutral-300">
      {children}
    </p>
  )
}

export function DocList({ items }: { items: ReactNode[] }) {
  return (
    <ul className="mt-4 space-y-2.5">
      {items.map((item, i) => (
        <li
          key={i}
          className="flex gap-3 text-[15px] leading-relaxed text-neutral-700 dark:text-neutral-300"
        >
          <span className="mt-2 inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-black dark:bg-white" />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  )
}

export function Callout({
  children,
  tone = 'default',
}: {
  children: ReactNode
  tone?: 'default' | 'warn' | 'info'
}) {
  return (
    <div
      className={`mt-5 rounded-xl border px-4 py-3 text-sm leading-relaxed ${
        tone === 'warn'
          ? 'border-neutral-300 bg-neutral-100 text-neutral-800 dark:border-neutral-700 dark:bg-neutral-800/50 dark:text-neutral-200'
          : tone === 'info'
            ? 'border-neutral-300 bg-neutral-100 text-neutral-800 dark:border-neutral-700 dark:bg-neutral-800/50 dark:text-neutral-200'
            : 'border-neutral-200 bg-neutral-50 text-neutral-700 dark:border-neutral-800 dark:bg-neutral-900/60 dark:text-neutral-300'
      }`}
    >
      {children}
    </div>
  )
}

export function DocCode({
  code,
  lang,
  title,
}: {
  code: string
  lang?: string
  title?: string
}) {
  return (
    <div className="mt-5">
      <Code code={code} lang={lang ?? 'text'} title={title} />
    </div>
  )
}

export function DocTable({
  head,
  rows,
}: {
  head: string[]
  rows: ReactNode[][]
}) {
  return (
    <div className="mt-5 overflow-x-auto rounded-xl border border-neutral-200 dark:border-neutral-800">
      <table className="w-full min-w-[560px] border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-neutral-200 bg-neutral-50 dark:border-neutral-800 dark:bg-neutral-900/60">
            {head.map((h) => (
              <th
                key={h}
                scope="col"
                className="px-4 py-2.5 text-xs font-medium uppercase tracking-wider text-neutral-500 dark:text-neutral-400"
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-neutral-200 dark:divide-neutral-800">
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td
                  key={j}
                  className="px-4 py-2.5 align-top leading-relaxed text-neutral-700 dark:text-neutral-300"
                >
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function Mono({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-md bg-neutral-100 px-1.5 py-0.5 font-mono text-[13px] text-neutral-800 dark:bg-neutral-800/70 dark:text-neutral-100">
      {children}
    </code>
  )
}
