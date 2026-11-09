-- H2-1B synthetic performance dataset for summary-draft.sql. SYNTHETIC — not production data.
-- psql variables: :alarms (total alarm rows), :recent_pct (share in the last 24 h, 0–100), :hot_pct (share on 10 hot devices)
-- Shape: 200 Inventory projects (+ 5 legacy), 2,000 assets (1 % RETIRED, 2 % DRAFT, 2 % OUT_OF_SERVICE),
-- 2,000 REAL + 1,000 SIM + 100 TEST asset identities, 200 orphan identities, 3,300 shadows,
-- alarms over 30 days before sampledAt 2026-11-07T08:00Z. Users: u-p1 (1 project), u-p20 (20), u-p200 (all 200).
\set ON_ERROR_STOP 1
SELECT setseed(0.42);
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
INSERT INTO roles VALUES ('viewer'),('operator'),('maintainer'),('platform_admin') ON CONFLICT DO NOTHING;
INSERT INTO users(id,subject,issuer,enabled) VALUES ('u-p1','s1','https://idp',true),('u-p20','s20','https://idp',true),('u-p200','s200','https://idp',true);
INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code)
  SELECT 'P'||g,'demo','p'||g,'PLANNED', (ARRAY[330481,330402,440300,440106,510104,320102,110105,310115])[1+g%8] FROM generate_series(1,200) g;
INSERT INTO projects(id,tenant_id,name) SELECT 'PL'||g,'demo','legacy' FROM generate_series(1,5) g;
INSERT INTO project_memberships(user_id,project_id,role) VALUES ('u-p1','P1','viewer');
INSERT INTO project_memberships(user_id,project_id,role) SELECT 'u-p20','P'||g,'viewer' FROM generate_series(1,20) g;
INSERT INTO project_memberships(user_id,project_id,role) SELECT 'u-p200','P'||g,'platform_admin' FROM generate_series(1,200) g;
INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status)
  SELECT 'A'||g,'P'||(1+g%200),'a','PLANNED', CASE WHEN g%100=0 THEN 'RETIRED' WHEN g%50=1 THEN 'DRAFT' WHEN g%50=2 THEN 'OUT_OF_SERVICE' ELSE 'ACTIVE' END
  FROM generate_series(1,2000) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'R'||g,'P'||(1+g%200),'A'||g,'REAL' FROM generate_series(1,2000) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'S'||g,'P'||(1+g%200),'A'||g,'SIMULATION' FROM generate_series(1,1000) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'T'||g,'P'||(1+g%200),'A'||g,'TEST' FROM generate_series(1001,1100) g;
INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'O'||g,'P'||(1+g%200),NULL,(ARRAY['REAL','SIMULATION','TEST'])[1+g%3] FROM generate_series(1,200) g;
INSERT INTO device_shadow(device_id,payload_json,updated_at,version)
  SELECT id, json_build_object('presence', json_build_object('connectivity', CASE WHEN random()<0.85 THEN 'MQTT_CONNECTED' ELSE 'OFFLINE' END,
           'lastSeenAt', to_char((timestamptz '2026-11-07T08:00:00Z' - (random()*3600)*interval '1 second') AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')),
         'reported', json_build_object('alarmState', CASE WHEN random()<0.05 THEN 'ACTIVE' ELSE 'NONE' END, 'operatingState','SILENT')),
         timestamptz '2026-11-07T07:30:00Z', 1
  FROM devices;
-- alarms: device choice (hot 10 devices vs uniform over all 3,300 identities), time (recent 24 h vs older 29 days)
WITH ids AS (SELECT array_agg(id ORDER BY id) AS a FROM devices)
INSERT INTO alarms(message_id,device_id,observed_at,payload_json)
  SELECT 'm'||g,
         ids.a[CASE WHEN random()*100 < :hot_pct THEN 1 + (g % 10) ELSE 1 + floor(random()*array_length(ids.a,1))::int END],
         CASE WHEN random()*100 < :recent_pct
              THEN timestamptz '2026-11-07T08:00:00Z' - random()*interval '24 hours'
              ELSE timestamptz '2026-11-06T08:00:00Z' - random()*interval '29 days' END,
         '{}'
  FROM generate_series(1, :alarms) g, ids;
ANALYZE;
SELECT 'dataset alarms=' || count(*) || ' recent24h=' || count(*) FILTER (WHERE observed_at >= '2026-11-06T08:00:00Z' AND observed_at < '2026-11-07T08:00:00Z')
       || ' alarms_size=' || pg_size_pretty(pg_total_relation_size('alarms')) FROM alarms;
