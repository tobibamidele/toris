# toris — Agent guidance

## Entrypoints & boundaries

- **Entrypoint**: `cmd/toris/main.go` → `internal/cli.Execute()`
- **CLI layer** owns only cobra dispatch — no business logic. All implementation is in `internal/`
- **App wiring**: `internal/app/app.go` wires all subsystems (leader, health, failover, proxy, metrics, backup, restore, storage, audit, nodewatch) via errgroup
- **Four-plane architecture** (see `docs/architecture.md`): CLI → Control Plane → Data Plane → Storage/Metadata. Circular deps prohibited.
- **`pkg/model`**: shared types only, no business logic
- **`internal/db/interface.go`**: `Backend` interface; `internal/db/postgres/` is the only implementation
- **`internal/exec`**: all subprocess invocations (pg_basebackup, pg_verifybackup, pg_rewind, etc.)
- **Storage backends**: `internal/storage/storage.go` defines `Backend` interface; `internal/storage/fs/` (default) and `internal/storage/s3/` (build tag `s3`, stub without tag)
- **`internal/nodewatch`**: polls `toris_control.nodes` every 30s — allows `toris node add/remove` at runtime without daemon restart
- **`internal/audit`**: immutable append-only audit event log

## Build

```sh
make build              # bin/toris
make build-race         # with -race
make build-s3           # bin/toris-s3 (S3 support via -tags s3)
```

Version injected via ldflags from `internal/cli`. CI builds with `-trimpath -s -w`.

## Test

```sh
make test               # go test ./... -count=1 -timeout 120s
make test-race          # + -race
make test-cover         # coverage.out + coverage.html
```

All tests in CI are unit-only (`go test ./...`). Integration tests live in `tests/integration/` with a Docker Compose cluster, build tag `integration`, and 300s timeout:
```sh
make integration-up          # start cluster
make test-integration        # run integration tests (requires: integration-up)
make test-integration-ci     # up → test → down (one-shot for CI)
```
Test files use testify and standard `*_test.go` naming.

## Lint & format

```sh
make lint               # golangci-lint run ./... (v1.59.1 in CI)
make fmt                # go fmt ./...
make vet                # go vet ./...
make all                # fmt → vet → build → test
```

Config: `.golangci.yml`. Notable: `gosec` G304 excluded (file path from variable in backup/restore). Test files exempt from gosec and errcheck.

## CI pipeline (`.github/workflows/ci.yml`)

Runs on push to `main`/`release/**` and PRs to `main`:
1. **Lint** — golangci-lint
2. **Test** — `go test ./... -count=1 -timeout 120s -race -coverprofile=coverage.out`
3. **Build** — cross-platform matrix (linux amd64/arm64, darwin amd64/arm64, windows amd64)

## Release (`.github/workflows/release.yml`)

Tag push `vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH-rc.N`. Produces `.tar.gz` (Unix) / `.zip` (Windows) + `checksums.txt`. Changelog extracted from CHANGELOG.md.

## CLI v0.4.0 additions

- `toris node add --id <id> --host <host>` and `toris node remove --id <id>` — runtime node add/remove via control DB; daemon picks up within ~30s via nodewatch
- `toris doctor` expanded to 8 checks (tools, backup dir, control DSN, DB connect, schema, lease, node freshness, backup freshness); exits 3 on failure
- `toris promote` / `toris demote` CLI commands are stubs — actual logic lives in `internal/failover`

## Key conventions

- **Fencing tokens**: lease `generation` increments on every acquisition. All mutating operations receive the current generation and reject stale tokens.
- **Health layers L1-L5**: TCP → pg_isready → SELECT 1 → pg_is_in_recovery → policy checks
- **Class A failure** (replica connectivity lost): timer-based, does not trigger failover
- **Class B failure** (lease lost): daemon shuts down cleanly, lease expires naturally
- **Failover**: fence first, then promote, then update routing target
- **Backup pipeline**: pending → running → verified → uploaded → retained → pruned
- **Proxy**: plain TCP byte forwarding, no TLS MITM, no wire-protocol awareness
- **Control DB**: separate PostgreSQL instance from cluster nodes. Required for daemon operation.

## Config

`toris init --out toris.yaml` generates a starter. Default lookup: `$HOME/.toris/toris.yaml`, `/etc/toris/toris.yaml`, `./toris.yaml`. `toris.yaml` and `*.local.yaml` are gitignored (contain credentials).

## Misc

- `GONOSUMDB=*` is exported in Makefile to avoid checksum issues with replace directives
- Every destructive command supports `--dry-run`
- `--output json` for machine-readable output on supported commands
- Graceful shutdown on SIGTERM/SIGINT: lease release → proxy drain → audit flush
- Control DB schema bootstrap runs at daemon startup in `internal/app/app.go:bootstrap()`
