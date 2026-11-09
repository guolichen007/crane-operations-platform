-- H2-0-R1 draft summary SQL probe. Synthetic fixture, run on an empty DB migrated to 00012.
-- psql variables: run from this directory so `cat summary-draft.sql` resolves.
\set ON_ERROR_STOP 1
-- Synthetic fixture for the DRAFT summary SQL. Not production data.
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
INSERT INTO roles VALUES ('viewer'),('operator'),('maintainer'),('platform_admin') ON CONFLICT DO NOTHING;
INSERT INTO users(id,subject,issuer,enabled) VALUES ('u-admin-p1','s1','https://idp',true),('u-viewer-p1','s2','https://idp',true),('u-none','s3','https://idp',true),('u-disabled','s4','https://idp',false);
INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) VALUES
 ('P1','demo','P1 浙江','PLANNED',330481),('P2','demo','P2 广东','PLANNED',440300),('P3','demo','P3 无位置','PLANNED',NULL);
INSERT INTO projects(id,tenant_id,name) VALUES ('PL','demo','遗留');
INSERT INTO project_memberships(user_id,project_id,role) VALUES
 ('u-admin-p1','P1','platform_admin'),('u-admin-p1','P3','platform_admin'),('u-admin-p1','PL','platform_admin'),
 ('u-viewer-p1','P1','viewer'),('u-disabled','P1','viewer');
INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) VALUES
 ('A1','P1','1号行车','PLANNED','ACTIVE'),   -- REAL offline(explicit) + SIM connected alarm
 ('A2','P1','2号行车','PLANNED','ACTIVE'),   -- REAL connected fresh, LIVE, alarm
 ('A3','P1','3号行车','PLANNED','ACTIVE'),   -- REAL says connected but stale observation
 ('A4','P1','4号行车','PLANNED','DRAFT'),    -- draft, no runtime
 ('A5','P1','5号行车','PLANNED','RETIRED'),  -- retired with REAL connected (must not count)
 ('A6','P1','6号行车','PLANNED','ACTIVE'),   -- REAL connected, future timestamp beyond skew
 ('A7','P1','7号行车','PLANNED','ACTIVE'),   -- REAL connected but no lastSeenAt
 ('A8','P1','8号行车','PLANNED','DRAFT'),    -- DRAFT with a fresh REAL connection → pending review
 ('A9','P1','9号行车','PLANNED','OUT_OF_SERVICE'), -- OUT_OF_SERVICE with fresh REAL connection + alarm → pending review
 ('B1','P2','1号行车','PLANNED','ACTIVE'),   -- other project, same name, REAL connected alarm
 ('N1','P3','无位置','PLANNED','ACTIVE'),
 ('L1','PL','遗留','PLANNED','ACTIVE');
INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES
 ('EDGE-A1','P1','A1','REAL'),('SIM-A1','P1','A1','SIMULATION'),('EDGE-A2','P1','A2','REAL'),('EDGE-A3','P1','A3','REAL'),
 ('EDGE-A5','P1','A5','REAL'),('EDGE-A6','P1','A6','REAL'),('EDGE-A7','P1','A7','REAL'),('EDGE-A8','P1','A8','REAL'),('EDGE-A9','P1','A9','REAL'),('EDGE-B1','P2','B1','REAL'),('EDGE-L1','PL','L1','REAL'),
 ('ORPH-P1','P1',NULL,'TEST');
-- sampled_at for the probe = 2026-11-07T08:00:00Z; stale = 2100 s (35 min)
INSERT INTO device_shadow VALUES
 ('EDGE-A1','{"presence":{"connectivity":"OFFLINE","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"NONE","operatingState":"DISCONNECTED"}}','2026-11-07T07:59:30Z',2),
 ('SIM-A1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:50Z"},"reported":{"alarmState":"ACTIVE","operatingState":"LIVE"}}','2026-11-07T07:59:50Z',2),
 ('EDGE-A2','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:50:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"LIVE"}}','2026-11-07T07:50:00Z',2),
 ('EDGE-A3','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:00:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:00:00Z',2),
 ('EDGE-A5','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-A6','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T09:00:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T09:00:00Z',2),
 ('EDGE-A7','{"presence":{"connectivity":"MQTT_CONNECTED"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-A8','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-A9','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-B1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-L1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2);
-- window [2026-11-06T08:00Z, 2026-11-07T08:00Z): boundary records at both ends
INSERT INTO alarms VALUES
 ('w-start','SIM-A1','2026-11-06T08:00:00Z','{}'),   -- included (>= start)
 ('w-before','SIM-A1','2026-11-06T07:59:59.999Z','{}'), -- excluded
 ('w-end','EDGE-A2','2026-11-07T08:00:00Z','{}'),    -- excluded (< sampled_at)
 ('w-real','EDGE-A2','2026-11-07T07:10:00Z','{}'),
 ('w-retired','EDGE-A5','2026-11-07T07:10:00Z','{}'),-- excluded: retired asset
 ('w-orphan','ORPH-P1','2026-11-07T07:20:00Z','{}'), -- unattributed, TEST
 ('w-p2','EDGE-B1','2026-11-07T07:30:00Z','{}'),     -- other project
 ('w-legacy','EDGE-L1','2026-11-07T07:30:00Z','{}'), -- legacy project
 ('w-future','EDGE-A2','2026-11-07T09:00:00Z','{}');  -- device clock 1 h ahead: recordsFutureSkew
\set summary `cat summary-draft.sql`
PREPARE s(text,text,int,timestamptz,timestamptz,int,int[]) AS :summary;
\echo '--- R1 platform_admin with P1+P3+PL grants, country ---'
EXECUTE s('u-admin-p1','all',NULL,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,NULL);
\echo '--- R2 same user, region 330000 (descendant codes supplied: 330481) ---'
EXECUTE s('u-admin-p1','all',330000,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,ARRAY[330481]);
\echo '--- R3 same user, region 440000 (only P2 there, not granted) ---'
EXECUTE s('u-admin-p1','all',440000,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,ARRAY[440300]);
\echo '--- R4 no membership ---'
EXECUTE s('u-none','all',NULL,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,NULL);
\echo '--- R5 disabled user with P1 membership ---'
EXECUTE s('u-disabled','all',NULL,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,NULL);
\echo '--- R6 threshold configurable: 600 s makes A2 (10 min old) stale ---'
EXECUTE s('u-viewer-p1','all',NULL,'2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',600,NULL);
