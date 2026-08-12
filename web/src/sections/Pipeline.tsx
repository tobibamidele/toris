const STAGES = [
  'preflight',
  'pg_basebackup',
  'manifest · SHA-256',
  'pg_verifybackup',
  'upload',
  'retained',
  'pruned',
]

export function Pipeline() {
  const doubled = [...STAGES, ...STAGES]
  return (
    <section className="border-b border-neutral-200 py-16 dark:border-neutral-800">
      <p className="text-center text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
        The backup pipeline — verified end to end
      </p>
      <div
        className="relative mt-10 overflow-hidden"
        aria-hidden="true"
      >
        <div className="ticker flex w-max items-center gap-6 pr-6">
          {doubled.map((stage, i) => (
            <span key={i} className="flex items-center gap-6">
              <span className="rounded-full border border-neutral-200 bg-neutral-50 px-4 py-2 font-mono text-sm text-neutral-600 dark:border-neutral-800 dark:bg-neutral-900/60 dark:text-neutral-300">
                {stage}
              </span>
              <svg
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                className="h-4 w-4 text-neutral-300 dark:text-neutral-700"
              >
                <path d="M2 8h11M9 4l4 4-4 4" />
              </svg>
            </span>
          ))}
        </div>
        <div className="pointer-events-none absolute inset-y-0 left-0 w-24 bg-gradient-to-r from-white to-transparent dark:from-neutral-950" />
        <div className="pointer-events-none absolute inset-y-0 right-0 w-24 bg-gradient-to-l from-white to-transparent dark:from-neutral-950" />
      </div>
    </section>
  )
}
