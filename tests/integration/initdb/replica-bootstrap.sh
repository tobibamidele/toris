#!/bin/bash
set -e

if [ ! -f "$PGDATA/PG_VERSION" ]; then
    echo "Replica data directory empty — bootstrapping from primary..."

    mkdir -p "$PGDATA"
    chown postgres:postgres "$PGDATA"

    for i in $(seq 1 30); do
        if pg_isready -h toris-pg-primary -U postgres >/dev/null 2>&1; then
            break
        fi
        echo "Waiting for primary (toris-pg-primary) ... attempt $i"
        sleep 2
    done

    pg_basebackup -h toris-pg-primary -D "$PGDATA" -U postgres -v -P --wal-method=stream --no-password

    touch "$PGDATA/standby.signal"

    echo "Replica bootstrap complete."
fi

exec docker-entrypoint.sh "$@"
