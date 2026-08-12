export function LogoMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
      className={className}
    >
      <circle
        cx="16"
        cy="16"
        r="11.5"
        stroke="currentColor"
        strokeWidth="1.75"
        opacity="0.45"
      />
      <circle cx="16" cy="7.5" r="4" fill="currentColor" />
      <circle cx="7" cy="22" r="2.5" fill="currentColor" opacity="0.55" />
      <circle cx="25" cy="22" r="2.5" fill="currentColor" opacity="0.55" />
    </svg>
  )
}

export function Logo({
  className,
  markClass,
}: {
  className?: string
  markClass?: string
}) {
  return (
    <span className={`inline-flex items-center gap-2 ${className ?? ''}`}>
      <LogoMark className={`h-6 w-6 ${markClass ?? ''}`} />
      <span className="text-[1.05rem] font-semibold tracking-tight">toris</span>
    </span>
  )
}
