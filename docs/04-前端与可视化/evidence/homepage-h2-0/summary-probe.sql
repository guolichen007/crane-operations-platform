-- H2-0-R3 summary-draft.sql probe. SYNTHETIC fixture only — not production data, not a production load test.
-- Run on an empty database migrated to 00012 (all goose Up sections), from this directory so that
-- `cat summary-draft.sql` resolves. Every expectation is asserted in SQL: any mismatch aborts with an error.
\set ON_ERROR_STOP 1
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
INSERT INTO roles VALUES ('viewer'),('operator'),('maintainer'),('platform_admin') ON CONFLICT DO NOTHING;
INSERT INTO users(id,subject,issuer,enabled) VALUES
 ('u-admin-p1','s1','https://idp',true),('u-viewer-p1','s2','https://idp',true),('u-none','s3','https://idp',true),('u-disabled','s4','https://idp',false);
INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) VALUES
 ('P1','demo','P1 浙江','PLANNED',330481),('P2','demo','P2 广东','PLANNED',440300),('P3','demo','P3 无位置','PLANNED',NULL);
INSERT INTO projects(id,tenant_id,name) VALUES ('PL','demo','遗留');
INSERT INTO project_memberships(user_id,project_id,role) VALUES
 ('u-admin-p1','P1','platform_admin'),('u-admin-p1','P3','platform_admin'),('u-admin-p1','PL','platform_admin'),
 ('u-viewer-p1','P1','viewer'),('u-disabled','P1','viewer');

-- Cross matrix: asset_status × runtime identities (REAL / SIM / REAL+SIM / none) × connection state.
INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) VALUES
 ('A1','P1','1号行车','PLANNED','ACTIVE'),          -- REAL explicit offline + SIM fresh alarm
 ('A2','P1','2号行车','PLANNED','ACTIVE'),          -- REAL fresh, LIVE, alarm
 ('A3','P1','3号行车','PLANNED','ACTIVE'),          -- REAL connected but observation 1 h old → stale
 ('A4','P1','4号行车','PLANNED','DRAFT'),           -- DRAFT, no runtime
 ('A5','P1','5号行车','PLANNED','RETIRED'),         -- RETIRED, REAL fresh alarm → excluded everywhere
 ('A6','P1','6号行车','PLANNED','ACTIVE'),          -- REAL connected, clock 1 h ahead → stale
 ('A7','P1','7号行车','PLANNED','ACTIVE'),          -- REAL connected, no lastSeenAt → unknown time
 ('A8','P1','8号行车','PLANNED','DRAFT'),           -- DRAFT + REAL fresh → pending review
 ('A9','P1','9号行车','PLANNED','OUT_OF_SERVICE'),  -- OUT_OF_SERVICE + REAL fresh alarm → pending review, no alarm device
 ('A10','P1','10号行车','PLANNED','ACTIVE'),        -- REAL fresh alarm + SIM fresh alarm → ONE alarm device (all), REAL 1 + SIM 1
 ('A11','P1','11号行车','PLANNED','RETIRED'),       -- RETIRED + REAL + SIM, both alarming → excluded
 ('A12','P1','12号行车','PLANNED','OUT_OF_SERVICE'),-- OUT_OF_SERVICE + SIM fresh alarm only → no alarm device, not pending (no REAL)
 ('A13','P1','13号行车','PLANNED','DRAFT'),         -- DRAFT + REAL + SIM both fresh → pending review once
 ('B1','P2','1号行车','PLANNED','ACTIVE'),          -- unauthorized project, same display name
 ('N1','P3','无位置','PLANNED','ACTIVE'),
 ('L1','PL','遗留','PLANNED','ACTIVE');              -- legacy (lifecycle NULL) project
INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES
 ('EDGE-A1','P1','A1','REAL'),('SIM-A1','P1','A1','SIMULATION'),('EDGE-A2','P1','A2','REAL'),('EDGE-A3','P1','A3','REAL'),
 ('EDGE-A5','P1','A5','REAL'),('EDGE-A6','P1','A6','REAL'),('EDGE-A7','P1','A7','REAL'),('EDGE-A8','P1','A8','REAL'),('EDGE-A9','P1','A9','REAL'),
 ('EDGE-A10','P1','A10','REAL'),('SIM-A10','P1','A10','SIMULATION'),
 ('EDGE-A11','P1','A11','REAL'),('SIM-A11','P1','A11','SIMULATION'),
 ('SIM-A12','P1','A12','SIMULATION'),
 ('EDGE-A13','P1','A13','REAL'),('SIM-A13','P1','A13','SIMULATION'),
 ('EDGE-B1','P2','B1','REAL'),('EDGE-L1','PL','L1','REAL'),
 ('ORPH-P1-TEST','P1',NULL,'TEST'),('ORPH-P1-REAL','P1',NULL,'REAL'),('ORPH-P2-REAL','P2',NULL,'REAL');
-- sampledAt = 2026-11-07T08:00:00Z
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
 ('EDGE-A10','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('SIM-A10','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('EDGE-A11','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('SIM-A11','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('SIM-A12','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('EDGE-A13','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('SIM-A13','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:58:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T07:58:00Z',2),
 ('EDGE-B1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-L1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2);
-- Window [2026-11-06T08:00Z, 2026-11-07T08:00Z); maxSkew default 120 s → futureSkew starts at 08:02:00Z.
INSERT INTO alarms VALUES
 ('w-start','SIM-A1','2026-11-06T08:00:00Z','{}'),          -- in window (SIM attributed)
 ('w-before','SIM-A1','2026-11-06T07:59:59.999Z','{}'),     -- before window
 ('w-end','EDGE-A2','2026-11-07T08:00:00Z','{}'),           -- = sampledAt: not in window, < skew boundary: next sample
 ('w-real','EDGE-A2','2026-11-07T07:10:00Z','{}'),          -- REAL attributed
 ('w-oos','EDGE-A9','2026-11-07T07:11:00Z','{}'),           -- OUT_OF_SERVICE asset: still an in-scope asset → REAL attributed
 ('w-retired','EDGE-A5','2026-11-07T07:10:00Z','{}'),       -- RETIRED asset: excluded
 ('w-retired-sim','SIM-A11','2026-11-07T07:10:00Z','{}'),   -- RETIRED asset: excluded
 ('w-orph-test','ORPH-P1-TEST','2026-11-07T07:20:00Z','{}'),-- TEST unattributed
 ('w-orph-real','ORPH-P1-REAL','2026-11-07T07:21:00Z','{}'),-- REAL unattributed
 ('w-orph-p2','ORPH-P2-REAL','2026-11-07T07:22:00Z','{}'),  -- unauthorized project orphan: excluded
 ('w-p2','EDGE-B1','2026-11-07T07:30:00Z','{}'),            -- unauthorized project: excluded
 ('w-legacy','EDGE-L1','2026-11-07T07:30:00Z','{}'),        -- legacy project: excluded
 ('f-scoped','EDGE-A2','2026-11-07T09:00:00Z','{}'),        -- future skew, in scope
 ('f-orphan','ORPH-P1-REAL','2026-11-07T09:00:00Z','{}'),   -- future skew, in-scope orphan (kept)
 ('f-retired','EDGE-A5','2026-11-07T09:00:00Z','{}'),       -- future skew on RETIRED asset: excluded (same scope as window)
 ('f-p2','EDGE-B1','2026-11-07T09:00:00Z','{}'),            -- future skew in unauthorized project: excluded
 ('f-legacy','EDGE-L1','2026-11-07T09:00:00Z','{}');        -- future skew in legacy project: excluded

\set summary `cat summary-draft.sql`
PREPARE s(text,timestamptz,timestamptz,int,int,int[]) AS :summary;
-- assert(label, summary json, expected subset json): fails the run on any mismatch.
CREATE FUNCTION pg_temp.expect(label text, got json, want jsonb) RETURNS text LANGUAGE plpgsql AS $$
DECLARE k text; cells jsonb := got::jsonb->'recordsCells'; csum int;
BEGIN
  FOR k IN SELECT jsonb_object_keys(want) LOOP
    IF (got::jsonb->k) IS DISTINCT FROM (want->k) THEN
      RAISE EXCEPTION '% %: got %, want %', label, k, got::jsonb->k, want->k;
    END IF;
  END LOOP;
  SELECT sum((v->>'attributed')::int + (v->>'unattributed')::int) INTO csum FROM jsonb_each(cells) e(src, v);
  IF csum <> (got::jsonb->>'recordsTotal')::int THEN RAISE EXCEPTION '% cells % <> total %', label, csum, got::jsonb->>'recordsTotal'; END IF;
  IF (got::jsonb->>'alarmDevicesAll')::int > (got::jsonb->>'alarmDevicesReal')::int + (got::jsonb->>'alarmDevicesSim')::int + (got::jsonb->>'alarmDevicesTest')::int
    THEN RAISE EXCEPTION '% alarmDevicesAll exceeds the source sum', label; END IF;
  RETURN label || ' OK';
END $$;

\echo '--- R1 platform_admin (P1+P3+PL), country, stale 2100 s, skew 120 s ---'
EXECUTE s('u-admin-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL) \gset r1_
\echo :r1_summary
SELECT pg_temp.expect('R1', :'r1_summary', '{
  "projects":2, "assetsRegistered":12, "assetsDraft":3, "assetsOutOfService":2, "assetsNoRuntime":2,
  "boundREAL":9, "boundSIMULATION":4, "boundTEST":0,
  "realConnected":2, "realPendingReview":3, "realStale":2, "realUnknownTime":1, "realOffline":1, "realLive":1,
  "alarmDevicesReal":2, "alarmDevicesSim":2, "alarmDevicesTest":0, "alarmDevicesAll":3,
  "recordsCells":{"REAL":{"attributed":2,"unattributed":1},"SIMULATION":{"attributed":1,"unattributed":0},"TEST":{"attributed":0,"unattributed":1}},
  "recordsTotal":5, "recordsFutureSkew":2}');

\echo '--- R2 same user, region 330000 (descendant admin codes supplied by the service: 330481) ---'
EXECUTE s('u-admin-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,ARRAY[330481]) \gset r2_
\echo :r2_summary
SELECT pg_temp.expect('R2', :'r2_summary', '{"projects":1, "assetsRegistered":11, "assetsNoRuntime":1, "realConnected":2, "recordsTotal":5, "recordsFutureSkew":2}');

\echo '--- R3 same user, region 440000 (only unauthorized P2 there) ---'
EXECUTE s('u-admin-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,ARRAY[440300]) \gset r3_
\echo :r3_summary
SELECT pg_temp.expect('R3', :'r3_summary', '{"projects":0, "assetsRegistered":0, "realConnected":0, "alarmDevicesAll":0, "recordsTotal":0, "recordsFutureSkew":0}');

\echo '--- R4 no membership ---'
EXECUTE s('u-none','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL) \gset r4_
SELECT pg_temp.expect('R4', :'r4_summary', '{"projects":0, "assetsRegistered":0, "recordsTotal":0, "recordsFutureSkew":0}');

\echo '--- R5 disabled user with P1 membership ---'
EXECUTE s('u-disabled','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL) \gset r5_
SELECT pg_temp.expect('R5', :'r5_summary', '{"projects":0, "assetsRegistered":0, "recordsTotal":0, "recordsFutureSkew":0}');

\echo '--- R6 viewer P1, stale 600 s (configurable): A2 (observed 10 min ago) becomes stale, A10 (2 min) stays fresh ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',600,120,NULL) \gset r6_
\echo :r6_summary
SELECT pg_temp.expect('R6', :'r6_summary', '{"realConnected":1, "realStale":3, "alarmDevicesReal":1}');

\echo '--- R7 viewer P1, maxSkew 7200 s (configurable): A6 (+1 h) becomes fresh, future records drop to 0 ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,7200,NULL) \gset r7_
\echo :r7_summary
SELECT pg_temp.expect('R7', :'r7_summary', '{"realConnected":3, "realStale":1, "recordsFutureSkew":0}');
