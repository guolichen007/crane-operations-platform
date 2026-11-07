# H2-0-R1 local harness script (synthetic data only). Requires a portable PostgreSQL 14 and an env file
# providing PGBIN/pgup/pgdown/psqlq, plus schema.sql = concatenated goose Up sections of 00001–00012.
# Paths under /tmp/cc are the sandbox layout used for the recorded *.out.txt; adjust locally.
source /tmp/cc/env.sh
pgup >/dev/null 2>&1
psqlq -d postgres -qc "DROP DATABASE IF EXISTS h2perf" -c "CREATE DATABASE h2perf" >/dev/null
psqlq -d h2perf -q -f /tmp/cc/h2/schema.sql >/dev/null 2>&1
psqlq -d h2perf -q <<'SQL'
-- Synthetic volume: 200 projects, 2,000 assets, 3,000 runtime ids, 400,000 alarms over 30 days.
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
INSERT INTO roles VALUES ('viewer'),('operator'),('maintainer'),('platform_admin') ON CONFLICT DO NOTHING;
INSERT INTO users(id,subject,issuer,enabled) VALUES ('u1','s','https://idp',true);
INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) SELECT 'P'||g,'demo','p','PLANNED',330481 FROM generate_series(1,200) g;
INSERT INTO project_memberships(user_id,project_id,role) SELECT 'u1','P'||g,'viewer' FROM generate_series(1,20) g;  -- user sees 10% of projects
INSERT INTO asset_devices(id,project_id,display_name,lifecycle) SELECT 'A'||g,'P'||(1+g%200),'a','PLANNED' FROM generate_series(1,2000) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'R'||g,'P'||(1+g%200),'A'||g,'REAL' FROM generate_series(1,2000) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'S'||g,'P'||(1+g%200),'A'||g,'SIMULATION' FROM generate_series(1,1000) g;
INSERT INTO device_shadow SELECT id,'{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:50:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T07:50:00Z',1 FROM devices;
INSERT INTO alarms SELECT 'm'||g, CASE WHEN g%3=0 THEN 'S'||(1+g%1000) ELSE 'R'||(1+g%2000) END,
  timestamptz '2026-10-08T08:00:00Z' + (g * interval '6.48 seconds'), '{}' FROM generate_series(1,400000) g;
ANALYZE;
SQL
psqlq -d h2perf -At -c "SELECT 'alarms='||count(*)||' size='||pg_size_pretty(pg_total_relation_size('alarms')) FROM alarms"
Q="SELECT d.source_mode, count(*) FROM alarms al JOIN devices d ON d.id=al.device_id JOIN project_memberships m ON m.project_id=d.project_id AND m.user_id='u1' WHERE al.observed_at >= timestamptz '2026-11-06T08:00:00Z' AND al.observed_at < timestamptz '2026-11-07T08:00:00Z' GROUP BY 1"
L="SELECT a.payload_json FROM alarms a JOIN devices d ON d.id=a.device_id JOIN project_memberships m ON m.project_id=d.project_id WHERE m.user_id='u1' ORDER BY a.observed_at DESC LIMIT 100"
for label in "NO INDEX" ; do echo "=== $label: window count ==="; psqlq -d h2perf -At -c "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, TIMING ON) $Q" | grep -E "Scan|Join|Execution|Planning|Buffers: shared" | head -14; echo "=== $label: existing ListScoped (latest 100) ==="; psqlq -d h2perf -At -c "EXPLAIN (ANALYZE, COSTS OFF) $L" | grep -E "Scan|Sort|Execution" | head -8; done
echo "=== CREATE INDEX CONCURRENTLY alarms(observed_at) and alarms(device_id, observed_at DESC) ==="
psqlq -d h2perf -c "\timing on" -c "CREATE INDEX CONCURRENTLY alarms_observed_at ON alarms(observed_at)" -c "CREATE INDEX CONCURRENTLY alarms_device_observed ON alarms(device_id, observed_at DESC)" 2>&1 | grep -E "Time|ERROR"
psqlq -d h2perf -qc "ANALYZE alarms"
psqlq -d h2perf -At -c "SELECT relname||'='||pg_size_pretty(pg_relation_size(oid)) FROM pg_class WHERE relname IN ('alarms_observed_at','alarms_device_observed','alarms_pkey')"
echo "=== WITH INDEXES: window count ==="; psqlq -d h2perf -At -c "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) $Q" | grep -E "Scan|Join|Execution|Buffers: shared" | head -14
echo "=== WITH INDEXES: existing ListScoped ==="; psqlq -d h2perf -At -c "EXPLAIN (ANALYZE, COSTS OFF) $L" | grep -E "Scan|Sort|Execution|Limit" | head -8
echo "=== CONCURRENTLY inside a transaction (goose default) ==="
psqlq -d h2perf -c "BEGIN" -c "CREATE INDEX CONCURRENTLY x ON alarms(message_id)" -c "ROLLBACK" 2>&1 | grep -E "ERROR" 
pgdown
