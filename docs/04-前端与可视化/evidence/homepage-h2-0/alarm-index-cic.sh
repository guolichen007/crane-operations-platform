# H2-0-R1 local harness script (synthetic data only). Requires a portable PostgreSQL 14 and an env file
# providing PGBIN/pgup/pgdown/psqlq, plus schema.sql = concatenated goose Up sections of 00001–00012.
# Paths under /tmp/cc are the sandbox layout used for the recorded *.out.txt; adjust locally.
source /tmp/cc/env.sh
pgup >/dev/null 2>&1
Q="SELECT d.source_mode, count(*) FROM alarms al JOIN devices d ON d.id=al.device_id JOIN project_memberships m ON m.project_id=d.project_id AND m.user_id='u1' WHERE al.observed_at >= timestamptz '2026-11-06T08:00:00Z' AND al.observed_at < timestamptz '2026-11-07T08:00:00Z' GROUP BY 1"
t() { psqlq -d h2perf -At -c "EXPLAIN (ANALYZE, COSTS OFF) $Q" | grep -E "Execution Time|using|Seq Scan on alarms"; }
echo "== both indexes =="; for i in 1 2 3; do t | grep Execution; done; t | grep -E "using|Seq Scan on alarms"
psqlq -d h2perf -qc "DROP INDEX alarms_observed_at"
echo "== only (device_id, observed_at DESC) =="; for i in 1 2 3; do t | grep Execution; done; t | grep -E "using|Seq Scan on alarms"
psqlq -d h2perf -qc "DROP INDEX alarms_device_observed"
echo "== no index =="; for i in 1 2 3; do t | grep Execution; done
echo "== CIC while alarms are being inserted (writer inserts 20k rows in 1k-row commits) =="
( for b in $(seq 1 20); do psqlq -d h2perf -qc "INSERT INTO alarms SELECT 'c$b-'||g,'R'||(1+g%2000), now(), '{}' FROM generate_series(1,1000) g" ; done; echo "writer done" ) &
sleep 0.2
psqlq -d h2perf -c "\timing on" -c "CREATE INDEX CONCURRENTLY alarms_observed_at ON alarms(observed_at)" 2>&1 | grep -E "Time|ERROR|CREATE"
wait
psqlq -d h2perf -At -c "SELECT 'rows='||count(*) FROM alarms" -c "SELECT 'valid='||indisvalid FROM pg_index WHERE indexrelid='alarms_observed_at'::regclass" -c "SET enable_seqscan=off; SELECT 'index_count='||count(*) FROM alarms WHERE observed_at > '1900-01-01'"
echo "== failed CIC leaves an INVALID index; re-run without IF NOT EXISTS fails, recovery = DROP INDEX CONCURRENTLY + re-create =="
psqlq -d h2perf -qc "INSERT INTO alarms VALUES ('dup1','R1',now(),'{}')"
psqlq -d h2perf -c "CREATE UNIQUE INDEX CONCURRENTLY bad_uniq ON alarms(device_id)" 2>&1 | grep ERROR | head -1
psqlq -d h2perf -At -c "SELECT 'bad_uniq valid='||indisvalid FROM pg_index WHERE indexrelid='bad_uniq'::regclass"
psqlq -d h2perf -c "CREATE INDEX CONCURRENTLY IF NOT EXISTS bad_uniq ON alarms(device_id)" 2>&1 | grep -E "NOTICE|ERROR" | head -1
psqlq -d h2perf -qc "DROP INDEX CONCURRENTLY bad_uniq" && echo "dropped invalid index"
pgdown
