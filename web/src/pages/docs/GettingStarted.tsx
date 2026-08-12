import {
  Callout,
  DocCode,
  DocH1,
  DocH2,
  DocList,
  DocP,
  Mono,
} from '../../components/doc'

const STARTER = `# 1. Install — a single binary
go install github.com/tobibamidele/toris/cmd/toris@latest

# 2. Write a starter config
toris init --out toris.yaml

# 3. Validate the config
toris config validate

# 4. Check your environment (tools, connectivity, schema)
toris doctor

# 5. Check cluster health
toris health

# 6. Start the daemon: proxy + lease + health loop + metrics
toris daemon --config toris.yaml`

const DAEMON = `$ toris daemon --config /etc/toris/toris.yaml
[toris] lease acquired  generation=1  node=node-01
[toris] health loop started  interval=10s
[toris] failover engine armed  threshold=60s
[toris] proxy listening on 127.0.0.1:5433 -> node-01:5432
[toris] metrics listening on 127.0.0.1:9090
[toris] node watcher started  poll=30s`

const CONFIG = `control_dsn: "host=localhost port=5432 user=toris dbname=toris_control sslmode=require"
cluster:
  id: "pg-main"
  nodes:
    - id: "node-01"
      host: "pg-primary.example.com"
      port: 5432
backup:
  storage_backend: "fs"
  base_dir: "/var/lib/toris/backups"
failover:
  enabled: false
  unhealthy_threshold: "60s"
  replication_outage_threshold: "5m"
  auto_rewind_after_failover: true`

export function GettingStarted() {
  return (
    <>
      <DocH1>Getting started</DocH1>
      <DocP>
        toris is a single binary that runs the full operational lifecycle of a
        PostgreSQL cluster: backups, replication, failover, and restore. You
        point it at your cluster, give it a control database, and start the
        daemon.
      </DocP>

      <DocH2 id="requirements">Requirements</DocH2>
      <DocList
        items={[
          <>
            Go 1.23+ or a prebuilt release binary
          </>,
          <>
            PostgreSQL client tools in <Mono>PATH</Mono> for the nodes toris
            manages — <Mono>pg_basebackup</Mono>, <Mono>pg_verifybackup</Mono>,{' '}
            <Mono>pg_rewind</Mono>, <Mono>pg_isready</Mono>
          </>,
          <>
            A <strong>control database</strong>: a separate PostgreSQL instance
            that toris uses for the lease, node registry, and audit log. It is
            distinct from the cluster nodes and is required for daemon
            operation.
          </>,
        ]}
      />

      <DocH2 id="install">Install</DocH2>
      <DocP>
        Install the toris CLI and daemon as one binary:
      </DocP>
      <DocCode
        code="go install github.com/tobibamidele/toris/cmd/toris@latest"
        lang="bash"
        title="install"
      />
      <DocP>
        Build from source, including the S3 storage backend:
      </DocP>
      <DocCode
        code={'make build          # bin/toris (filesystem storage)\nmake build GOFLAGS="-tags s3"   # bin/toris (S3-compatible storage)'}
        lang="bash"
        title="build"
      />

      <DocH2 id="configure">Configure</DocH2>
      <DocP>
        Generate a fully commented starter config, then edit the nodes, control
        DSN, and failover settings:
      </DocP>
      <DocCode code="toris init --out toris.yaml" lang="bash" title="init" />
      <DocP>A minimal config looks like this:</DocP>
      <DocCode code={CONFIG} lang="yaml" title="toris.yaml" />
      <DocP>
        toris looks for config at <Mono>$HOME/.toris/toris.yaml</Mono>,{' '}
        <Mono>/etc/toris/toris.yaml</Mono>, or <Mono>./toris.yaml</Mono>. You
        can always pass an explicit path with <Mono>--config</Mono>. Config
        files contain credentials, so keep them out of version control.
      </DocP>

      <DocH2 id="start">Start the daemon</DocH2>
      <DocP>
        The daemon runs the whole operations loop: lease renewal, the health
        check loop, the failover engine, the TCP proxy, the metrics server, and
        the node watcher:
      </DocP>
      <DocCode code={DAEMON} lang="bash" title="daemon log" />
      <DocP>
        From here on, <Mono>127.0.0.1:5433</Mono> is the only endpoint your
        applications ever need to know about.
      </DocP>

      <DocH2 id="full-quickstart">The full quick start</DocH2>
      <DocCode code={STARTER} lang="bash" title="quickstart" />
      <Callout tone="info">
        <strong>Graceful shutdown.</strong> On SIGTERM/SIGINT the daemon
        releases the lease, waits for active proxy connections, and flushes the
        audit queue before exiting.
      </Callout>
    </>
  )
}
