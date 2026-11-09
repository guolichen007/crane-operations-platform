# usage: wall.sh <db> <variant> <user> ; 7 executions of the prepared statement, prints median server-side wall time (no EXPLAIN instrumentation)
source /tmp/cc/env17.sh
Q=$(cat /tmp/cc/h2/v/$2.sql)
psqlq -d $1 -At <<SQL | grep -E "^Time:" | awk '{print $2}' | sort -n | awk '{a[NR]=$1} END{printf "median=%.1f min=%.1f max=%.1f n=%d\n", a[int((NR+1)/2)], a[1], a[NR], NR}'
SET plan_cache_mode = force_custom_plan;
PREPARE s(text,timestamptz,timestamptz,int,int,int[]) AS $Q;
\o /dev/null
\timing on
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
EXECUTE s('$3','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);
SQL
