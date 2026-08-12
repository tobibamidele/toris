import { useMemo, useState, type ReactNode } from 'react'

function escapeHtml(s: string): string {
  return s
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
}

const TOKEN_RE =
  /(\/\/(.*)|\/\*(.|\s)*?\*\/|#[^\n]*|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|(?:^|\b)(?:func|return|if|else|for|range|const|var|type|struct|interface|package|import|go|switch|case|default|defer|select|chan|map|string|int|bool|uint|error|nil|true|false)(?=\b)|\b\d+(?:\.\d+)?\b)/g

export function highlight(code: string): ReactNode {
  const html = escapeHtml(code)
  const parts = html.split(TOKEN_RE)
  return parts.map((part, i) => {
    if (part === undefined) return null
    const isComment =
      part.startsWith('#') ||
      part.startsWith('//') ||
      part.startsWith('/*') ||
      part.startsWith('*')
    const isString = part.startsWith('"') || part.startsWith("'")
    const isNumber = /^\d/.test(part) && !isString
    const isKeyword =
      !isComment &&
      !isString &&
      /^(func|return|if|else|for|range|const|var|type|struct|interface|package|import|go|switch|case|default|defer|select|chan|map|string|int|bool|uint|error|nil|true|false)$/.test(
        part,
      )
    let cls = ''
    if (isComment) cls = 'text-neutral-400 dark:text-neutral-500 italic'
    else if (isString) cls = 'text-neutral-600 dark:text-neutral-300'
    else if (isNumber) cls = 'text-neutral-500 dark:text-neutral-400'
    else if (isKeyword) cls = 'font-semibold text-black dark:text-white'
    return cls ? (
      <span key={i} className={cls}>
        {part}
      </span>
    ) : (
      part
    )
  })
}

export function Code({
  code,
  lang = 'text',
  title,
  className,
}: {
  code: string
  lang?: string
  title?: string
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  const trimmed = code.replace(/^\n+|\n+$/g, '')

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(trimmed)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      /* ignore */
    }
  }

  const labeled = useMemo(
    () => `${title ? `${title} — ` : ''}${lang}`,
    [title, lang],
  )

  return (
    <div
      className={`overflow-hidden rounded-xl border border-neutral-200 bg-neutral-50 dark:border-neutral-800 dark:bg-neutral-900/60 ${className ?? ''}`}
    >
      <div className="flex items-center justify-between border-b border-neutral-200 px-4 py-2 dark:border-neutral-800">
        <span className="font-mono text-[11px] uppercase tracking-wider text-neutral-500 dark:text-neutral-400">
          {labeled}
        </span>
        <button
          type="button"
          onClick={onCopy}
          className="pressable -mr-1 rounded-md px-2 py-1 text-[11px] font-medium text-neutral-500 hover:text-black dark:text-neutral-400 dark:hover:text-white"
          aria-label="Copy code to clipboard"
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre className="overflow-x-auto px-4 py-4 text-[13px] leading-relaxed">
        <code className="font-mono text-neutral-700 dark:text-neutral-200">
          {highlight(trimmed)}
        </code>
      </pre>
    </div>
  )
}
