# H2-0-R1 local harness script (synthetic data only). Requires a portable PostgreSQL 14 and an env file
# providing PGBIN/pgup/pgdown/psqlq, plus schema.sql = concatenated goose Up sections of 00001–00012.
# Paths under /tmp/cc are the sandbox layout used for the recorded *.out.txt; adjust locally.
# Counter-proof: multi-statement READ COMMITTED vs REPEATABLE READ vs single statement.
source /tmp/cc/env.sh
pgup >/dev/null 2>&1
psqlq -d postgres -qc "DROP DATABASE IF EXISTS h2snap" -c "CREATE DATABASE h2snap" >/dev/null
psqlq -d h2snap -q -f /tmp/cc/h2/schema.sql >/dev/null 2>&1
psqlq -d h2snap -q <<'SQL'
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES ('P1','demo','P1','PLANNED');
INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES ('A1','P1','a','PLANNED');
INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES ('E1','P1','A1','REAL');
SQL
writer() { sleep 1; psqlq -d h2snap -qc "BEGIN; INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES ('A$1','P1','x','PLANNED'); INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES ('E$1','P1','A$1','REAL'); COMMIT;"; }
reader() { psqlq -d h2snap -At -c "BEGIN ISOLATION LEVEL $1 READ ONLY;" -c "SELECT 'assets='||count(*) FROM asset_devices;" -c "SELECT pg_sleep(2);" -c "SELECT 'boundREAL='||count(DISTINCT asset_device_id) FROM devices WHERE source_mode='REAL';" -c "COMMIT;" | grep "="; }
echo "== READ COMMITTED, two statements (writer commits in between) =="; writer 2 & reader "READ COMMITTED"; wait
echo "== REPEATABLE READ READ ONLY, two statements =="; writer 3 & reader "REPEATABLE READ"; wait
echo "== single statement (implicit READ COMMITTED, sleep between the two scans) =="; writer 4 & psqlq -d h2snap -At -c "SELECT 'assets='||a.n||' boundREAL='||b.n FROM (SELECT count(*) n FROM asset_devices) a, (SELECT pg_sleep(2)) z, LATERAL (SELECT count(DISTINCT asset_device_id) n FROM devices WHERE source_mode='REAL' AND z IS NOT NULL) b"; wait
psqlq -d h2snap -At -c "SELECT 'final assets='||count(*) FROM asset_devices"
pgdown
