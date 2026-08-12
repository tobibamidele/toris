import { useState } from 'react'
import { IconCheck, IconCopy } from '../components/icons'

const DSN = 'host=127.0.0.1  port=5433  user=app  dbname=app'

export function DsnBar() {
  const [copied, setCopied] = useState(false)

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText('host=127.0.0.1 port=5433 user=app dbname=app')
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="mx-auto max-w-6xl px-4 pb-16 sm:px-6 lg:px-8 lg:pb-20">
      <div className="flex flex-col items-center gap-4 rounded-2xl border border-neutral-200 bg-neutral-50 px-6 py-6 sm:flex-row sm:justify-between sm:px-8 dark:border-neutral-800 dark:bg-neutral-900/50">
        <div className="flex flex-col items-center gap-3 text-center sm:flex-row sm:gap-5 sm:text-left">
          <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-black px-3 py-1 text-[11px] font-medium uppercase tracking-wider text-white dark:bg-white dark:text-black">
            One DSN
          </span>
          <div>
            <p className="font-mono text-sm text-neutral-700 dark:text-neutral-200">
              {DSN}
            </p>
            <p className="mt-1 text-xs text-neutral-500 dark:text-neutral-500">
              Your app connects here. The proxy routes to the live primary — no
              DNS changes, no downtime, ever.
            </p>
          </div>
        </div>
        <button
          type="button"
          onClick={onCopy}
          aria-label="Copy the connection string"
          className="pressable inline-flex shrink-0 items-center gap-2 rounded-full border border-neutral-300 px-4 py-2 text-sm font-medium hover:border-black dark:border-neutral-700 dark:hover:border-white"
        >
          {copied ? (
            <IconCheck className="h-4 w-4" />
          ) : (
            <IconCopy className="h-4 w-4" />
          )}
          {copied ? 'Copied' : 'Copy DSN'}
        </button>
      </div>
    </div>
  )
}
