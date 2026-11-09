#!/usr/bin/env bash
# Operations summary capacity benchmark (H2-1C). Re-runnable from a fresh checkout.
#
# Requirements: a PostgreSQL 17 server you own (NOT production), psql, node, python3.
# Configuration (environment):
#   PGHOST / PGPORT / PGUSER   server to use (default 127.0.0.1 / 5432 / postgres); the user must be able to CREATE DATABASE
#   PSQL                        psql binary (default: psql on PATH)
#   BENCH_DB                    scratch database name (default crane_ops_bench); it is DROPPED and recreated
#   BENCH_ALARMS                synthetic alarm rows (default 2000000)
#   BENCH_RECENT_PCT            share of alarms in the last 24 h (default 3.3)
#   BENCH_HOT_PCT               share of alarms on 10 hot identities (default 0)
#   BENCH_RESTART_CMD           optional command that restarts PostgreSQL between cold runs (else cold = first run)
# Usage:  tools/perf/operations-summary/bench.sh > bench.out.txt
# The SQL under test is the production statement internal/operations/summary.sql, bound through
# tools/perf/operations-summary/params.mjs exactly like internal/operations.Summary binds it.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HERE="$ROOT/tools/perf/operations-summary"
PSQL="${PSQL:-psql}"
DB="${BENCH_DB:-crane_ops_bench}"
export PGHOST="${PGHOST:-127.0.0.1}" PGPORT="${PGPORT:-5432}" PGUSER="${PGUSER:-postgres}"
q() { "$PSQL" -v ON_ERROR_STOP=1 -X "$@"; }

echo "# operations summary benchmark — $(q -d postgres -Atc 'select version()')"
echo "# SYNTHETIC data; capacity evidence only, not a production SLA."
q -d postgres -qc "DROP DATABASE IF EXISTS $DB" -c "CREATE DATABASE $DB"
node "$HERE/schema.mjs" "$ROOT" | q -d "$DB" -q
q -d "$DB" -At -v alarms="${BENCH_ALARMS:-2000000}" -v recent_pct="${BENCH_RECENT_PCT:-3.3}" -v hot_pct="${BENCH_HOT_PCT:-0}" -f "$HERE/dataset.sql"

for ix in none devobs; do
  q -d "$DB" -qc "DROP INDEX IF EXISTS bench_alarms_dev_obs"
  [ "$ix" = devobs ] && q -d "$DB" -qc "CREATE INDEX bench_alarms_dev_obs ON alarms(device_id, observed_at DESC)"
  q -d "$DB" -qc "VACUUM ANALYZE alarms"
  for u in u-p1 u-p20 u-p200; do
    for region in country 330000; do
      [ -n "${BENCH_RESTART_CMD:-}" ] && eval "$BENCH_RESTART_CMD" >/dev/null
      node "$HERE/run.mjs" "$ROOT" "$DB" "$u" "$region" "ix=$ix"
    done
  done
done
q -d "$DB" -qc "DROP INDEX IF EXISTS bench_alarms_dev_obs"
