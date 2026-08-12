import {
  Callout,
  DocH1,
  DocH2,
  DocP,
  DocTable,
  Mono,
} from '../../components/doc'

const CLUSTER_CMDS: [string, string][] = [
  ['toris init', 'Write a starter config file'],
  ['toris config validate', 'Validate the config and report all problems'],
  ['toris cluster status', 'Show cluster summary'],
  ['toris node list', 'List configured nodes'],
  ['toris node add --id <id> --host <host>', 'Add a node at runtime'],
  ['toris node remove --id <id>', 'Remove a node at runtime'],
  ['toris health', 'Run L1–L5 health checks on all nodes'],
  ['toris doctor', 'Diagnose configuration and connectivity problems'],
]

const BACKUP_CMDS: [string, string][] = [
  ['toris backup create', 'Create and verify a backup'],
  ['toris backup verify <path>', 'Verify an existing backup'],
  ['toris backup list', 'List all backups in storage'],
  ['toris backup prune', 'Apply retention policy'],
  ['toris restore', 'Restore a backup into a data directory'],
  ['toris reseed', 'Reseed a replica from the latest backup'],
  ['toris rewind --data-dir <dir>', 'Rewind a demoted primary using pg_rewind'],
]

const LEADER_CMDS: [string, string][] = [
  ['toris leader status', 'Show current lease holder and generation'],
  ['toris leader acquire', 'Manually acquire the cluster lease'],
  ['toris leader release', 'Release the cluster lease'],
  ['toris promote --node <id>', 'Promote a replica to primary'],
  ['toris demote --node <id>', 'Demote the primary'],
  ['toris daemon', 'Run the full daemon'],
  ['toris version', 'Print version and build info'],
]

const EXIT_CODES: [string, string][] = [
  ['0', 'Success'],
  ['1', 'General error'],
  ['2', 'Config validation failed'],
  ['3', 'Health check failed'],
  ['4', 'Backup failed'],
  ['5', 'Restore failed'],
  ['6', 'Failover failed'],
  ['7', 'Lease conflict'],
]

export function CliReference() {
  return (
    <>
      <DocH1>CLI reference</DocH1>
      <DocP>
        All commands support <Mono>--help</Mono>. Machine-readable output is
        available on supported commands with <Mono>--output json</Mono>.
      </DocP>

      <DocH2 id="cluster">Cluster & configuration</DocH2>
      <DocTable
        head={['Command', 'What it does']}
        rows={CLUSTER_CMDS.map(([c, d]) => [<Mono key={c}>{c}</Mono>, d])}
      />

      <DocH2 id="backup">Backup & recovery</DocH2>
      <DocTable
        head={['Command', 'What it does']}
        rows={BACKUP_CMDS.map(([c, d]) => [<Mono key={c}>{c}</Mono>, d])}
      />

      <DocH2 id="leader">Lease & failover</DocH2>
      <DocTable
        head={['Command', 'What it does']}
        rows={LEADER_CMDS.map(([c, d]) => [<Mono key={c}>{c}</Mono>, d])}
      />

      <DocH2 id="dry-run">Dry runs</DocH2>
      <DocP>
        Every destructive command supports <Mono>--dry-run</Mono>:
      </DocP>
      <DocTable
        head={['Command', 'Effect']}
        rows={[
          [
            <Mono key="a">toris backup create --dry-run</Mono>,
            'Preflight only, no pg_basebackup',
          ],
          [
            <Mono key="b">toris promote --node node-02 --dry-run</Mono>,
            'Validate the promotion plan without acting',
          ],
          [
            <Mono key="c">toris rewind --data-dir /var/lib/postgresql/data --dry-run</Mono>,
            'Check rewind feasibility without touching data',
          ],
        ]}
      />

      <DocH2 id="exit-codes">Exit codes</DocH2>
      <DocTable
        head={['Code', 'Meaning']}
        rows={EXIT_CODES.map(([c, d]) => [<Mono key={c}>{c}</Mono>, d])}
      />
      <Callout>
        <Mono>toris doctor</Mono> exits with code 3 when any of its eight checks
        fail: tools present, backup dir writable, control DSN reachable,
        database connect, schema present, lease sane, nodes fresh, backups
        fresh.
      </Callout>
    </>
  )
}
