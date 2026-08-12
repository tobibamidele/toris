import {
  Callout,
  DocCode,
  DocH1,
  DocH2,
  DocList,
  DocP,
  DocTable,
  Mono,
} from '../../components/doc'

const PIPELINE = `preflight
  → pg_basebackup
    → manifest (SHA-256 per artifact + self-hash)
      → pg_verifybackup
        → storage upload`

const RESTORE = `toris restore --backup-id <id> --target-dir /var/lib/postgresql/data`

export function Operations() {
  return (
    <>
      <DocH1>Backups & restore</DocH1>
      <DocP>
        toris treats backups as the foundation of recovery: every backup is
        verified before it is trusted, and every restore re-verifies before it
        touches a data directory.
      </DocP>

      <DocH2 id="pipeline">The backup pipeline</DocH2>
      <DocP>
        A backup only reaches the <Mono>verified</Mono> state after{' '}
        <Mono>pg_verifybackup</Mono> exits 0:
      </DocP>
      <DocCode code={PIPELINE} lang="text" title="pipeline" />
      <DocP>
        The manifest embeds a SHA-256 hash of itself, so any post-write
        tampering is detected on restore. A backup whose manifest fails
        verification is never used for a restore.
      </DocP>

      <DocH2 id="create">Creating backups</DocH2>
      <DocCode
        code={`toris backup create
toris backup create --dry-run   # preflight only, no pg_basebackup
toris backup list
toris backup verify <backup-id>`}
        lang="bash"
        title="backup"
      />

      <DocH2 id="restore">Restoring</DocH2>
      <DocP>
        The restore path re-verifies everything before extraction:
      </DocP>
      <DocCode code={RESTORE} lang="bash" title="restore" />
      <DocList
        items={[
          'Download and verify the manifest self-hash',
          'Download and SHA-256-verify every artifact against the manifest',
          'Extract tar archives into the target data directory',
          'Start PostgreSQL if restore.start_after_restore is set',
        ]}
      />

      <DocH2 id="reseed-rewind">Reseed & rewind</DocH2>
      <DocP>
        After a failover, the demoted primary must rejoin the cluster:
      </DocP>
      <DocList
        items={[
          <>
            <Mono>toris reseed</Mono> — rebuild a replica from the latest
            verified backup.
          </>,
          <>
            <Mono>toris rewind --data-dir &lt;dir&gt;</Mono> — fast rejoin via{' '}
            <Mono>pg_rewind</Mono> when the old primary shares a timeline with
            the new one.
          </>,
          <>
            With <Mono>failover.auto_rewind_after_failover: true</Mono>, toris
            attempts rewind automatically and falls back to a full reseed if
            rewind fails.
          </>,
        ]}
      />

      <DocH2 id="retention">Retention</DocH2>
      <DocP>
        <Mono>toris backup prune</Mono> applies the configured retention
        policy (keep-last, keep-daily, keep-weekly) and removes expired
        backups.
      </DocP>
      <DocCode code="toris backup prune --dry-run" lang="bash" title="prune" />

      <DocH2 id="storage">Storage backends</DocH2>
      <DocTable
        head={['Backend', 'Build', 'When to use']}
        rows={[
          [
            'Filesystem (fs)',
            'Default',
            'Single server or NFS-mounted storage',
          ],
          [
            'S3-compatible',
            <>-tags s3</>,
            'Object storage, offsite copies',
          ],
        ]}
      />
      <DocCode code={'make build GOFLAGS="-tags s3"'} lang="bash" title="s3 build" />
      <Callout tone="info">
        <strong>Not a WAL archive.</strong> toris is not a replacement for WAL
        archiving — keep <Mono>archive_command</Mono> configured alongside
        toris for point-in-time recovery.
      </Callout>
    </>
  )
}
