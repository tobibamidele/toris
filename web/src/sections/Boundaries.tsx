const IS = [
  'A production-grade Postgres orchestration layer',
  'A stable single-DSN proxy endpoint',
  'Automatic failover and leader election',
  'Verified backups, restores, reseeds, and rewinds',
  'Self-hosted: runs on your VPS or bare metal',
]

const IS_NOT = [
  'Not a connection pooler — pair it with pgBouncer if you need one',
  'Not multi-primary — one writable primary per cluster in v1',
  'Not a replacement for WAL archiving — keep archive_command for PITR',
  'Not a cloud-managed service',
]

export function Boundaries() {
  return (
    <section className="mx-auto max-w-6xl px-4 py-20 sm:px-6 sm:py-24 lg:px-8">
      <div className="grid gap-10 md:grid-cols-2">
        <div className="rounded-2xl border border-neutral-200 p-8 dark:border-neutral-800">
          <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
            What toris is
          </p>
          <ul className="mt-6 space-y-3">
            {IS.map((item) => (
              <li key={item} className="flex gap-3 text-[15px] leading-relaxed">
                <span className="mt-2 inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-black dark:bg-white" />
                {item}
              </li>
            ))}
          </ul>
        </div>
        <div className="rounded-2xl border border-neutral-200 p-8 dark:border-neutral-800">
          <p className="text-xs font-medium uppercase tracking-widest text-neutral-500 dark:text-neutral-400">
            What toris is not
          </p>
          <ul className="mt-6 space-y-3">
            {IS_NOT.map((item) => (
              <li key={item} className="flex gap-3 text-[15px] leading-relaxed text-neutral-600 dark:text-neutral-400">
                <span className="mt-2 inline-block h-1.5 w-1.5 shrink-0 rounded-full border border-neutral-400 dark:border-neutral-600" />
                {item}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  )
}
