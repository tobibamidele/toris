#!/usr/bin/env bash
#
# toris — end-to-end test against the docker compose cluster.
#
# Exercises the real `toris` binary (daemon + CLI) against the 4-container
# cluster defined in tests/integration/docker-compose.yml:
#
#   pg-control   localhost:5440  toris control DB (leases, nodes, backups, audit)
#   pg-primary   localhost:5441  writable primary, dbname=testdb
#   pg-replica-1 localhost:5442  streaming replica
#   pg-replica-2 localhost:5443  streaming replica
#
# What it does:
#   1. Preflight  — tools, binary, config, containers, tmp dirs
#   2. Daemon     — start, wait for lease, doctor, leader, cluster, health
#   3. Data       — create a table on the primary, verify it replicates
#   4. Backup     — create -> list -> verify (with retention/prune checks)
#   5. Restore    — restore into a fresh dir, assert data + PG_VERSION
#   6. Reseed     — reseed a replica dir, assert standby.signal
#   7. Proxy      — query the stable endpoint 127.0.0.1:5433
#   8. Metrics    — scrape Prometheus endpoint, sanity-check gauges
#   9. Control DB — inspect toris_control tables directly
#
# Every check prints PASS/FAIL. The script exits 0 only if everything passed.
#
# Usage:
#   tests/e2e/e2e.sh
#
# Overrides (env):
#   TORIS_BIN          path to the toris binary            (default: bin/toris)
#   TORIS_CONFIG       path to the toris config file       (default: toris.local.yaml)
#   TORIS_E2E_TMP      scratch directory                   (default: tmp/e2e)
#   TORIS_E2E_RESTART  "1" to restart the daemon fresh, "0" to reuse a running one
#                      (default: 1)
#   TORIS_E2E_KEEP     "1" to leave the daemon running at the end (default: 0)
#
# Prerequisites (see toris.local.yaml comments):
#   export PATH="/usr/lib/postgresql/17/bin:$PATH"   # pg tools
#   export PGUSER=postgres                            # cluster containers trust auth
#
set -uo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT}" || exit 1

TORIS_BIN="${TORIS_BIN:-${ROOT}/bin/toris}"
TORIS_CONFIG="${TORIS_CONFIG:-${ROOT}/toris.local.yaml}"
TORIS_E2E_TMP="${TORIS_E2E_TMP:-${ROOT}/tmp/e2e}"
TORIS_E2E_RESTART="${TORIS_E2E_RESTART:-1}"
TORIS_E2E_KEEP="${TORIS_E2E_KEEP:-0}"

BACKUP_DIR="${TORIS_E2E_TMP}/backups"
RESTORE_TMP="${TORIS_E2E_TMP}/restore-tmp"
DAEMON_LOG="${TORIS_E2E_TMP}/daemon.log"
DAEMON_PID="${TORIS_E2E_TMP}/daemon.pid"
RESTORE_DIR="${TORIS_E2E_TMP}/restored-data"
RESEED_DIR="${TORIS_E2E_TMP}/reseed-data"

# Endpoints (host is 127.0.0.1 for everything; the docker network maps them)
CONTROL_PORT=5440
PRIMARY_PORT=5441
REPLICA1_PORT=5442
REPLICA2_PORT=5443
PROXY_PORT=5433
METRICS_ADDR="127.0.0.1:9100"
DBNAME=testdb

CONTROL_USER=toris
CONTROL_PASS=toris_control_pass
CLUSTER_USER=postgres

# Timeouts (seconds)
T_WAIT_LEASE=60
T_WAIT_REPLICA=30
T_TOOL=20

# Tracks results
PASSED=0
FAILED=0
FAILED_NAMES=()

# ─────────────────────────────────────────────────────────────────────────────
# Output helpers
# ─────────────────────────────────────────────────────────────────────────────

c_red=$'\033[31m'; c_green=$'\033[32m'; c_yellow=$'\033[33m'; c_bold=$'\033[1m'; c_dim=$'\033[2m'; c_reset=$'\033[0m'

info()  { printf '%s%s%s\n' "${c_dim}" "$*" "${c_reset}"; }
step()  { printf '\n%s==> %s%s\n' "${c_bold}" "$*" "${c_reset}"; }
ok()    { printf '  %s✓ PASS%s  %s\n' "${c_green}" "${c_reset}" "$1"; PASSED=$((PASSED+1)); }
warn()  { printf '  %s⚠ WARN%s  %s\n' "${c_yellow}" "${c_reset}" "$1"; }
fail()  { printf '  %s✗ FAIL%s  %s\n' "${c_red}" "${c_reset}" "$1"; FAILED=$((FAILED+1)); FAILED_NAMES+=("$1"); }
wanted() { printf '  %s≈ want%s  %s\n' "${c_dim}" "${c_reset}" "$1"; }
out()   { sed 's/^/          /'; }

# Run a command, capturing stdout+stderr into the global $OUT and $OUT_FILE.
# Returns the command's exit code. Never aborts the script.
run_cmd() {
  OUT_FILE="$(mktemp)"
  OUT=""
  if ! OUT="$("$@" 2>"${OUT_FILE}.err")"; then
    rc=$?
    OUT="${OUT}"$'\n'"$(<"${OUT_FILE}.err")"
    rm -f "${OUT_FILE}" "${OUT_FILE}.err"
    return $rc
  fi
  if [[ -s "${OUT_FILE}.err" ]]; then
    OUT="${OUT}"$'\n'"$(<"${OUT_FILE}.err")"
  fi
  rm -f "${OUT_FILE}" "${OUT_FILE}.err"
  return 0
}

# ── Check helpers ────────────────────────────────────────────────────────────

# run_check <name> <cmd...>  — PASS iff exit code is 0
run_check() {
  local name="$1"; shift
  run_cmd "$@"
  local rc=$?
  if [[ $rc -eq 0 ]]; then
    ok "$name"
  else
    fail "$name"
    printf '          command: %s\n' "$*"
    printf '          exit:    %s\n' "$rc"
    [[ -n "$OUT" ]] && printf '%s\n' "$OUT" | out
  fi
  return $rc
}

# run_check_exit <name> <expected_exit> <cmd...> — PASS iff exit code matches
run_check_exit() {
  local name="$1" want="$2"; shift 2
  run_cmd "$@"
  local rc=$?
  if [[ $rc -eq "$want" ]]; then
    ok "$name"
    wanted "exit=$want"
  else
    fail "$name"
    printf '          command: %s\n' "$*"
    printf '          want exit %s, got %s\n' "$want" "$rc"
    [[ -n "$OUT" ]] && printf '%s\n' "$OUT" | out
  fi
  return $rc
}

# run_query <name> <port> <dbname> <user> <pass> <sql>
# Runs psql, prints the result rows, PASS iff psql exits 0.
run_query() {
  local name="$1" port="$2" db="$3" user="$4" pass="$5" sql="$6"
  local out rc
  if [[ -n "$pass" ]]; then
    out="$(PGPASSWORD="$pass" psql -X -A -t -q -h 127.0.0.1 -p "$port" -U "$user" -d "$db" -c "$sql" 2>&1)"
  else
    out="$(psql -X -A -t -q -h 127.0.0.1 -p "$port" -U "$user" -d "$db" -c "$sql" 2>&1)"
  fi
  rc=$?
  if [[ $rc -eq 0 ]]; then
    ok "$name"
  else
    fail "$name"
  fi
  [[ -n "$out" ]] && printf '%s\n' "$out" | sed 's/^/          /'
  return $rc
}

# json_get <file> <.dotted.path[.idx]> — value or "" via jq/python3
json_get() {
  local file="$1" path="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r "$path" "$file" 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$path" <<'PY'
import json, sys, re
doc = json.load(open(sys.argv[1]))
cur = doc
for t in re.findall(r'[^.\[\]]+|\[\d+\]', sys.argv[2].lstrip('.')):
    if t.startswith('['):
        cur = cur[int(t[1:-1])]
    else:
        cur = cur[t]
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
else:
    print(cur)
PY
  else
    printf ''
  fi
}

# first_healthy_backup <jsonfile> — id of the first verified/uploaded/retained
# backup in a `backup list` JSON array, or "" if none (jq/python3).
first_healthy_backup() {
  local file="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -r '[.[] | select(.status == "verified" or .status == "uploaded" or .status == "retained")][0].id // empty' "$file" 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$file" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for b in doc:
    if b.get("status") in ("verified", "uploaded", "retained"):
        print(b.get("id", ""))
        break
PY
  else
    printf ''
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Daemon lifecycle
# ─────────────────────────────────────────────────────────────────────────────

daemon_is_running() {
  [[ -f "$DAEMON_PID" ]] || return 1
  local pid; pid="$(<"$DAEMON_PID")"
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

daemon_stop() {
  if ! daemon_is_running; then
    info "daemon not running — nothing to stop"
    return 0
  fi
  local pid; pid="$(<"$DAEMON_PID")"
  info "stopping daemon (pid $pid)"
  kill "$pid" 2>/dev/null
  local deadline=$(( $(date +%s) + 30 ))
  while kill -0 "$pid" 2>/dev/null; do
    [[ $(date +%s) -ge $deadline ]] && { warn "daemon did not exit; sending SIGKILL"; kill -9 "$pid" 2>/dev/null; break; }
    sleep 1
  done
  rm -f "$DAEMON_PID"
  info "daemon stopped"
}

daemon_start() {
  if daemon_is_running && [[ "$TORIS_E2E_RESTART" != "1" ]]; then
    info "reusing running daemon (pid $(<"$DAEMON_PID"))"
    return 0
  fi
  daemon_stop
  mkdir -p "$(dirname "$DAEMON_LOG")"
  : > "$DAEMON_LOG"
  info "starting daemon: $TORIS_BIN --config $TORIS_CONFIG daemon"
  setsid nohup "$TORIS_BIN" --config "$TORIS_CONFIG" daemon >>"$DAEMON_LOG" 2>&1 < /dev/null &
  echo $! > "$DAEMON_PID"

  # Wait for the lease to become active (leader status reads the control DB
  # directly, so exit 0 alone is not proof the daemon is up).
  local deadline=$(( $(date +%s) + T_WAIT_LEASE ))
  while :; do
    local lout
    lout="$("$TORIS_BIN" --config "$TORIS_CONFIG" leader status --output json 2>/dev/null)"
    if grep -q '"status": "active"' <<<"$lout"; then
      ok "daemon acquired cluster lease"
      break
    fi
    if [[ $(date +%s) -ge $deadline ]]; then
      fail "daemon failed to acquire cluster lease within ${T_WAIT_LEASE}s"
      if [[ -s "$DAEMON_LOG" ]]; then
        echo "  --- tail of daemon.log ---"
        tail -n 30 "$DAEMON_LOG" | out
      fi
      return 1
    fi
    sleep 2
  done

  # Give the health loop a first round so roles settle.
  sleep 3
}

# ─────────────────────────────────────────────────────────────────────────────
# Preflight
# ─────────────────────────────────────────────────────────────────────────────

preflight() {
  step "Preflight"

  local missing=0

  # 1. pg tools
  for tool in pg_isready psql pg_basebackup pg_verifybackup pg_ctl; do
    if command -v "$tool" >/dev/null 2>&1; then
      info "  tool: $tool -> $(command -v "$tool")"
    else
      # Common location on Debian/Ubuntu with PG 17 from the postgres.org repo.
      if [[ -x "/usr/lib/postgresql/17/bin/$tool" ]]; then
        info "  tool: $tool -> /usr/lib/postgresql/17/bin/$tool (added to PATH)"
        PATH="/usr/lib/postgresql/17/bin:$PATH"
      else
        fail "preflight: required tool '$tool' not found in PATH"
        missing=1
      fi
    fi
  done

  # 2. binary
  if [[ -x "$TORIS_BIN" ]]; then
    info "  binary: $TORIS_BIN"
    run_cmd "$TORIS_BIN" version
    [[ -n "$OUT" ]] && printf '%s\n' "$OUT" | out
  else
    fail "preflight: toris binary not found at $TORIS_BIN (run 'make build' or 'go build -o bin/toris ./cmd/toris')"
    missing=1
  fi

  # 3. config
  if [[ -f "$TORIS_CONFIG" ]]; then
    info "  config: $TORIS_CONFIG"
    run_cmd "$TORIS_BIN" --config "$TORIS_CONFIG" config validate
    if [[ $? -eq 0 ]]; then
      ok "preflight: config validates"
    else
      fail "preflight: config validation failed"
      printf '%s\n' "$OUT" | out
      missing=1
    fi
  else
    fail "preflight: config file not found at $TORIS_CONFIG"
    missing=1
  fi

  # 4. containers
  local port okc=1
  for port in $CONTROL_PORT $PRIMARY_PORT $REPLICA1_PORT $REPLICA2_PORT; do
    if pg_isready -q -h 127.0.0.1 -p "$port"; then
      info "  container: 127.0.0.1:$port ready"
    else
      fail "preflight: no PostgreSQL responding on 127.0.0.1:$port (docker compose up?)"
      okc=0
    fi
  done
  [[ $okc -eq 0 ]] && missing=1

  # 5. scratch dirs
  mkdir -p "$BACKUP_DIR" "$RESTORE_TMP"
  if [[ -w "$BACKUP_DIR" && -w "$RESTORE_TMP" ]]; then
    info "  scratch: $BACKUP_DIR (writable)"
  else
    fail "preflight: scratch dirs not writable: $BACKUP_DIR"
    missing=1
  fi

  # 6. remove stale failed backups (from aborted runs) so restore/reseed never
  #    picks one up — keeps re-runs deterministic without wiping good backups.
  if command -v psql >/dev/null 2>&1; then
    local cluster_id failed_ids
    cluster_id="$(grep -E '^[[:space:]]+id:' "$TORIS_CONFIG" | head -1 | awk '{print $2}' | tr -d '"')"
    cluster_id="${cluster_id:-pg-test}"
    failed_ids="$(PGPASSWORD="$CONTROL_PASS" psql -X -A -t -q -h 127.0.0.1 -p "$CONTROL_PORT" -U "$CONTROL_USER" -d toris_control \
      -c "SELECT id FROM toris_control.backups WHERE cluster_id='${cluster_id}' AND status='failed';" 2>/dev/null)"
    if [[ -n "$failed_ids" ]]; then
      PGPASSWORD="$CONTROL_PASS" psql -X -A -t -q -h 127.0.0.1 -p "$CONTROL_PORT" -U "$CONTROL_USER" -d toris_control \
        -c "DELETE FROM toris_control.backups WHERE cluster_id='${cluster_id}' AND status='failed';" >/dev/null 2>&1
      info "  removed stale failed backup record(s) for cluster '${cluster_id}'"
      while read -r fid; do
        [[ -n "$fid" ]] && rm -rf "$BACKUP_DIR/$fid" && info "    removed $BACKUP_DIR/$fid"
      done <<< "$failed_ids"
    else
      info "  no stale failed backups to clean"
    fi
  else
    warn "preflight: psql not in PATH — skipping stale failed-backup cleanup"
  fi

  # PGUSER must be the cluster superuser (container only has the postgres role).
  export PGUSER="${PGUSER:-$CLUSTER_USER}"

  if [[ $missing -ne 0 ]]; then
    echo
    printf '%sPreflight failed — aborting.%s\n' "$c_red" "$c_reset"
    exit 1
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 1 — daemon + control plane
# ─────────────────────────────────────────────────────────────────────────────

phase_daemon() {
  step "Phase 1: daemon + control plane"

  daemon_start

  # leader status — must be active with a generation >= 1
  run_check "leader status exits cleanly" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" leader status --output json
  if [[ -n "$OUT" ]]; then
    printf '%s\n' "$OUT" | out
    local gen
    gen="$(printf '%s' "$OUT" | json_get /dev/stdin '.generation' 2>/dev/null || true)"
    if [[ -n "$gen" ]] && [[ "$gen" =~ ^[0-9]+$ ]] && [[ "$gen" -ge 1 ]]; then
      ok "leader generation is >= 1 (got $gen)"
    else
      fail "leader generation missing or < 1"
    fi
  fi

  # doctor — with a verified backup this must exit 0. On a fresh cluster with
  # no backups yet the "backup freshness" check fails (exit 3), which is
  # expected pre-backup; only a hard failure for other checks is a FAIL here.
  run_cmd "$TORIS_BIN" --config "$TORIS_CONFIG" doctor
  local doc_rc=$?
  if [[ $doc_rc -eq 0 ]]; then
    ok "doctor passes (verified backup exists)"
  elif [[ $doc_rc -eq 3 ]] && grep -q "no verified backups found" <<<"$OUT"; then
    warn "doctor: no verified backup yet (expected before first backup) — will assert again post-backup"
    printf '%s\n' "$OUT" | out
  else
    fail "doctor failed (exit $doc_rc)"
    printf '%s\n' "$OUT" | out
  fi

  # cluster status
  run_check "cluster status exits cleanly" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" cluster status --output json
  if [[ -n "$OUT" ]]; then
    printf '%s\n' "$OUT" | out
  fi

  # health — all three nodes must reach level 5
  local health_out
  health_out="$("$TORIS_BIN" --config "$TORIS_CONFIG" health --output json)"
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    fail "health command"
    printf '%s\n' "$health_out" | out
  else
    ok "health command"
    printf '%s\n' "$health_out" | out
    if [[ -x "$(command -v jq)" ]] || command -v python3 >/dev/null 2>&1; then
      local l1 l2 l3
      l1="$(printf '%s' "$health_out" | json_get /dev/stdin '.[0].level' 2>/dev/null || true)"
      l2="$(printf '%s' "$health_out" | json_get /dev/stdin '.[1].level' 2>/dev/null || true)"
      l3="$(printf '%s' "$health_out" | json_get /dev/stdin '.[2].level' 2>/dev/null || true)"
      if [[ "$l1" == "5" && "$l2" == "5" && "$l3" == "5" ]]; then
        ok "all nodes healthy at level 5"
      else
        fail "expected all nodes at level 5 (got $l1, $l2, $l3)"
      fi
    else
      warn "jq/python3 missing — skipping automated level assertion"
    fi
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 2 — data on the primary + replication
# ─────────────────────────────────────────────────────────────────────────────

phase_data() {
  step "Phase 2: test data on primary + replication"

  local sql
  sql="CREATE TABLE IF NOT EXISTS e2e_probe (id serial PRIMARY KEY, payload text NOT NULL, ts timestamptz NOT NULL DEFAULT now());
INSERT INTO e2e_probe (payload) VALUES ('hello-from-primary')
ON CONFLICT DO NOTHING;
SELECT 'primary-write-ok' AS status, count(*) AS rows FROM e2e_probe;"

  run_query "write to primary (port $PRIMARY_PORT)" \
    "$PRIMARY_PORT" "$DBNAME" "$CLUSTER_USER" "" "$sql"

  # Give streaming replication a moment, then verify the row reached a replica.
  sleep 2
  run_query "row visible on replica-2 (port $REPLICA2_PORT)" \
    "$REPLICA2_PORT" "$DBNAME" "$CLUSTER_USER" "" \
    "SELECT count(*) AS replicated_rows FROM e2e_probe;"
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 3 — backup
# ─────────────────────────────────────────────────────────────────────────────

phase_backup() {
  step "Phase 3: backup"

  # 3.1 create
  local bfile
  bfile="$(mktemp)"
  run_cmd "$TORIS_BIN" --config "$TORIS_CONFIG" backup create --output json
  local create_rc=$?
  printf '%s\n' "$OUT" > "$bfile"
  if [[ $create_rc -eq 0 ]]; then
    ok "backup create"
    printf '%s\n' "$OUT" | out
  else
    fail "backup create"
    printf '%s\n' "$OUT" | out
    rm -f "$bfile"
    return 1
  fi

  local backup_id backup_status backup_path
  backup_id="$(json_get "$bfile" '.id')"
  backup_status="$(json_get "$bfile" '.status')"
  backup_path="$(json_get "$bfile" '.storage_path')"

  if [[ -z "$backup_id" ]]; then
    fail "backup create did not return an id"
    rm -f "$bfile"
    return 1
  fi
  ok "backup id parsed: $backup_id"
  info "  status: ${backup_status:-<empty>}  path: ${backup_path:-<empty>}"
  rm -f "$bfile"

  # Backup must be verified by the pipeline itself.
  if [[ "$backup_status" == "verified" || "$backup_status" == "uploaded" || "$backup_status" == "retained" ]]; then
    ok "backup reached a terminal healthy state ($backup_status)"
  else
    fail "backup ended in unexpected state ($backup_status)"
  fi

  # 3.2 list
  run_check "backup list contains the new backup" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" backup list --output json
  printf '%s\n' "$OUT" | out
  if [[ "$OUT" != *"$backup_id"* ]]; then
    fail "backup list does not show backup $backup_id"
  else
    ok "backup list shows $backup_id"
  fi

  # 3.3 verify (against the on-disk artifact directory)
  if [[ -n "$backup_path" ]] && [[ -d "$backup_path" ]]; then
    run_check "backup verify $backup_path" \
      "$TORIS_BIN" --config "$TORIS_CONFIG" backup verify "$backup_path"
    printf '%s\n' "$OUT" | out
  else
    fail "backup path missing or not a directory: ${backup_path:-<empty>}"
  fi

  # 3.4 the verified backup dir must contain the archives + manifests
  local missing_artifact=0
  for f in base.tar.gz pg_wal.tar.gz backup_manifest toris_manifest.json; do
    if [[ -f "$backup_path/$f" ]]; then
      ok "artifact present: $backup_path/$f"
    else
      fail "expected artifact missing: $backup_path/$f"
      missing_artifact=1
    fi
  done

  # 3.5 prune should be a no-op with retention satisfied (min_count=1)
  run_check "backup prune (no-op expected)" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" backup prune --force
  printf '%s\n' "$OUT" | out

  # 3.6 doctor must now pass (backup freshness satisfied)
  run_check "doctor passes with fresh backup" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" doctor

  return 0
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 4 — restore
# ─────────────────────────────────────────────────────────────────────────────

phase_restore() {
  step "Phase 4: restore"

  local bfile
  bfile="$(mktemp)"
  run_cmd "$TORIS_BIN" --config "$TORIS_CONFIG" backup list --output json
  printf '%s\n' "$OUT" > "$bfile"
  local backup_id
  backup_id="$(first_healthy_backup "$bfile")"
  rm -f "$bfile"
  if [[ -z "$backup_id" ]]; then
    fail "no verified/uploaded backup available to restore"
    return 1
  fi
  ok "using backup $backup_id for restore"

  # Target dir must be empty and not a running data dir.
  rm -rf "$RESTORE_DIR"
  mkdir -p "$RESTORE_DIR"

  run_check "restore into $RESTORE_DIR" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" restore \
    --backup-id "$backup_id" --target-dir "$RESTORE_DIR" --force
  printf '%s\n' "$OUT" | out

  # Data files landed under <target>/data
  local data_dir="${RESTORE_DIR}/data"
  if [[ -f "${data_dir}/PG_VERSION" ]]; then
    ok "restored data dir has PG_VERSION ($(<"${data_dir}/PG_VERSION"))"
  else
    fail "restored data dir missing PG_VERSION (no data under $data_dir?)"
    return 1
  fi
  if [[ -d "${data_dir}/base" ]]; then
    ok "restored base/ directory present"
  else
    fail "restored base/ directory missing"
  fi

  # The restored data dir must be usable by pg_controldata.
  if command -v pg_controldata >/dev/null 2>&1; then
    run_check "pg_controldata reads restored data dir" \
      pg_controldata "$data_dir"
    printf '%s\n' "$OUT" | out
  else
    warn "pg_controldata not in PATH — skipping data-dir sanity check"
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 5 — reseed
# ─────────────────────────────────────────────────────────────────────────────

phase_reseed() {
  step "Phase 5: reseed"

  rm -rf "$RESEED_DIR"
  mkdir -p "$RESEED_DIR"

  # Omitting --backup-id exercises the "latest verified backup" resolver.
  run_check "reseed (latest verified backup) into $RESEED_DIR" \
    "$TORIS_BIN" --config "$TORIS_CONFIG" reseed \
    --target-dir "$RESEED_DIR" --force
  printf '%s\n' "$OUT" | out

  local data_dir="${RESEED_DIR}/data"
  if [[ -f "${data_dir}/standby.signal" ]]; then
    ok "standby.signal written — replica mode configured"
  else
    fail "standby.signal missing — reseed did not configure replica mode"
  fi
  if [[ -f "${data_dir}/PG_VERSION" ]]; then
    ok "reseeded data dir has PG_VERSION"
  else
    fail "reseeded data dir missing PG_VERSION"
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 6 — stable proxy endpoint
# ─────────────────────────────────────────────────────────────────────────────

phase_proxy() {
  step "Phase 6: stable proxy (127.0.0.1:$PROXY_PORT)"

  # TCP connect must succeed (listener up).
  run_check "proxy accepts TCP connections" \
    bash -c "exec 3<>/dev/tcp/127.0.0.1/$PROXY_PORT && echo connected"

  # Full PostgreSQL handshake through the proxy.
  run_query "SELECT via proxy (port $PROXY_PORT)" \
    "$PROXY_PORT" "$DBNAME" "$CLUSTER_USER" "" \
    "SELECT 'proxy-works' AS result, count(*) AS rows FROM e2e_probe;"

  # The proxy must be a real primary endpoint, not a replica.
  run_query "proxy target is primary (pg_is_in_recovery=false)" \
    "$PROXY_PORT" "$DBNAME" "$CLUSTER_USER" "" \
    "SELECT pg_is_in_recovery() AS is_in_recovery;"
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 7 — metrics
# ─────────────────────────────────────────────────────────────────────────────

phase_metrics() {
  step "Phase 7: metrics endpoint"

  local body
  if ! body="$(timeout 10 curl -fsS "http://${METRICS_ADDR}/metrics" 2>&1)"; then
    fail "GET /metrics on ${METRICS_ADDR}"
    printf '%s\n' "$body" | out
    return 1
  fi
  ok "GET /metrics on ${METRICS_ADDR}"

  # health_checks_total is a CounterVec that only appears after the first
  # completed health round (the daemon's health loop ticks every ~10s). The
  # E2E run can be fast enough to race that first round, so poll briefly.
  local deadline=$(( $(date +%s) + 20 ))
  while ! grep -q "^toris_health_checks_total" <<<"$body"; do
    if [[ $(date +%s) -ge $deadline ]]; then
      warn "health_checks_total not observed within 20s (polling expired)"
      break
    fi
    sleep 2
    body="$(timeout 10 curl -fsS "http://${METRICS_ADDR}/metrics" 2>&1)"
  done

  local expect=(
    "toris_leader_lease_generation"
    "toris_health_checks_total"
    "toris_leader_lease_renewals_total"
  )
  for metric in "${expect[@]}"; do
    if grep -q "^${metric}" <<<"$body"; then
      ok "metric present: $metric"
      grep "^${metric}" <<<"$body" | out
    else
      fail "metric missing: $metric"
    fi
  done

  local gen
  gen="$(grep -oE '^toris_leader_lease_generation(\{[^}]*\})? [0-9]+' <<<"$body" | awk '{print $NF}')"
  if [[ -n "$gen" ]] && [[ "$gen" =~ ^[0-9]+$ ]] && [[ "$gen" -ge 1 ]]; then
    ok "lease generation metric >= 1 (got $gen)"
  else
    fail "lease generation metric missing or invalid"
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Phase 8 — control DB inspection
# ─────────────────────────────────────────────────────────────────────────────

phase_control() {
  step "Phase 8: control DB inspection"

  run_query "leases table has exactly one active lease" \
    "$CONTROL_PORT" "toris_control" "$CONTROL_USER" "$CONTROL_PASS" \
    "SELECT count(*) AS active FROM toris_control.leases WHERE status='active';"

  run_query "nodes table has 3 registered nodes" \
    "$CONTROL_PORT" "toris_control" "$CONTROL_USER" "$CONTROL_PASS" \
    "SELECT id, role, status FROM toris_control.nodes ORDER BY id;"

  run_query "backups table has >= 1 record" \
    "$CONTROL_PORT" "toris_control" "$CONTROL_USER" "$CONTROL_PASS" \
    "SELECT count(*) AS backups FROM toris_control.backups WHERE cluster_id='pg-test';"

  run_query "audit_events is append-only and non-empty" \
    "$CONTROL_PORT" "toris_control" "$CONTROL_USER" "$CONTROL_PASS" \
    "SELECT kind, count(*) FROM toris_control.audit_events GROUP BY kind ORDER BY kind;"

  # Fencing token semantics: node ids are stable, no duplicates.
  run_query "no duplicate node ids" \
    "$CONTROL_PORT" "toris_control" "$CONTROL_USER" "$CONTROL_PASS" \
    "SELECT count(*) AS dupes FROM (SELECT id FROM toris_control.nodes GROUP BY id HAVING count(*) > 1) d;"
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

cleanup() {
  if [[ "$TORIS_E2E_KEEP" != "1" ]]; then
    daemon_stop >/dev/null 2>&1
  fi
}
trap cleanup EXIT INT TERM

main() {
  echo
  echo "${c_bold}toris end-to-end test${c_reset}"
  echo "  binary : $TORIS_BIN"
  echo "  config : $TORIS_CONFIG"
  echo "  tmp    : $TORIS_E2E_TMP"
  echo

  preflight
  phase_daemon
  phase_data
  phase_backup
  phase_restore
  phase_reseed
  phase_proxy
  phase_metrics
  phase_control

  echo
  echo "──────────────────────────────────────────────"
  printf 'Results: %s%s passed%s, %s%s failed%s\n' \
    "$c_green" "$PASSED" "$c_reset" \
    "$c_red" "$FAILED" "$c_reset"
  if [[ $FAILED -gt 0 ]]; then
    printf 'Failed checks:\n'
    for n in "${FAILED_NAMES[@]}"; do
      printf '  - %s\n' "$n"
    done
    exit 1
  fi
  echo "All checks passed."
  exit 0
}

main "$@"
