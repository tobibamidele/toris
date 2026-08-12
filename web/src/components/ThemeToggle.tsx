import { useTheme } from '../lib/theme'

function SunIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
    >
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
    </svg>
  )
}

function MoonIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
    >
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
    </svg>
  )
}

const EASE = 'cubic-bezier(0.2, 0, 0, 1)'

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, toggleTheme } = useTheme()
  const dark = theme === 'dark'

  return (
    <button
      type="button"
      onClick={toggleTheme}
      aria-label={dark ? 'Switch to light mode' : 'Switch to dark mode'}
      className={`pressable relative inline-flex h-9 w-9 items-center justify-center rounded-full border border-neutral-200 text-neutral-600 hover:text-black dark:border-neutral-800 dark:text-neutral-400 dark:hover:text-white ${className ?? ''}`}
    >
      <span className="relative inline-block h-5 w-5">
        <span
          aria-hidden="true"
          className="absolute inset-0 transition-[opacity,transform,filter] duration-300"
          style={{
            opacity: dark ? 0 : 1,
            transform: dark ? 'scale(0.25)' : 'scale(1)',
            filter: dark ? 'blur(4px)' : 'blur(0px)',
            transitionTimingFunction: EASE,
          }}
        >
          <SunIcon className="h-5 w-5" />
        </span>
        <span
          aria-hidden="true"
          className="absolute inset-0 transition-[opacity,transform,filter] duration-300"
          style={{
            opacity: dark ? 1 : 0,
            transform: dark ? 'scale(1)' : 'scale(0.25)',
            filter: dark ? 'blur(0px)' : 'blur(4px)',
            transitionTimingFunction: EASE,
          }}
        >
          <MoonIcon className="h-5 w-5" />
        </span>
      </span>
    </button>
  )
}
