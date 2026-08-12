export interface DocMeta {
  to: string
  label: string
  desc: string
  next?: string
  prev?: string
}

export const DOCS: DocMeta[] = [
  {
    to: '/docs/getting-started',
    label: 'Getting started',
    desc: 'Install toris, write a config, and run the daemon.',
  },
  {
    to: '/docs/concepts',
    label: 'Concepts & failover',
    desc: 'The single endpoint, leader election, fencing tokens, and failure classes.',
  },
  {
    to: '/docs/cli',
    label: 'CLI reference',
    desc: 'Every toris command and its exit codes.',
  },
  {
    to: '/docs/configuration',
    label: 'Configuration',
    desc: 'The full configuration reference with key fields.',
  },
  {
    to: '/docs/operations',
    label: 'Backups & restore',
    desc: 'Backup pipelines, restores, reseeds, rewinds, and storage backends.',
  },
]

export function findDoc(to: string): DocMeta | undefined {
  return DOCS.find((d) => d.to === to)
}
