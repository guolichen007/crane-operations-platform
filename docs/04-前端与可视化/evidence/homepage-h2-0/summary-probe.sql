-- H2-1B summary-draft.sql probe (PostgreSQL 17). SYNTHETIC fixture only — not production data, not a production load test.
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
 -- H2-1B-3: abnormal observation times (all ACTIVE, REAL MQTT_CONNECTED, alarm ACTIVE) → per-device UNKNOWN, never online.
 ('T1','P1','坏时间-空串','PLANNED','ACTIVE'),
 ('T2','P1','坏时间-非法','PLANNED','ACTIVE'),
 ('T3','P1','坏时间-无时区','PLANNED','ACTIVE'),
 ('T4','P1','坏时间-零值','PLANNED','ACTIVE'),
 ('T5','P1','坏时间-非字符串','PLANNED','ACTIVE'),
 ('T6','P1','正常-带偏移时区','PLANNED','ACTIVE'),
 -- H2-1B-3: DRAFT / OUT_OF_SERVICE field anomalies
 ('D1','P1','草稿-REAL告警','PLANNED','DRAFT'),
 ('D2','P1','草稿-双身份告警','PLANNED','DRAFT'),
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
 ('EDGE-B1','P2','B1','REAL'),('EDGE-L1','PL','L1','REAL'),('EDGE-N1','P3','N1','REAL'),
 ('EDGE-T1','P1','T1','REAL'),('EDGE-T2','P1','T2','REAL'),('EDGE-T3','P1','T3','REAL'),('EDGE-T4','P1','T4','REAL'),('EDGE-T5','P1','T5','REAL'),('EDGE-T6','P1','T6','REAL'),
 ('EDGE-D1','P1','D1','REAL'),('EDGE-D2','P1','D2','REAL'),('SIM-D2','P1','D2','SIMULATION'),
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
 ('EDGE-T1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":""},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-T2','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"yesterday-ish"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-T3','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-T4','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"0001-01-01T00:00:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-T5','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":1730966340},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-T6','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T15:59:00+08:00"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-D1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('EDGE-D2','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
 ('SIM-D2','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"ACTIVE","operatingState":"SILENT"}}','2026-11-07T07:59:00Z',2),
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
 ('f-legacy','EDGE-L1','2026-11-07T09:00:00Z','{}'),        -- future skew in legacy project: excluded
 -- one asset, several records: A10 has REAL ×2 and SIM ×1 in window → 3 records, 1 distinct asset
 ('m-a10-r1','EDGE-A10','2026-11-07T06:00:00Z','{}'),('m-a10-r2','EDGE-A10','2026-11-07T06:30:00Z','{}'),('m-a10-s1','SIM-A10','2026-11-07T06:45:00Z','{}'),
 -- unlocated project P3 asset record (country scope only)
 ('m-n1','EDGE-N1','2026-11-07T06:50:00Z','{}'),
 -- skew boundaries relative to sampledAt 08:00:00Z
 ('b-0','EDGE-A2','2026-11-07T08:00:00Z','{}'),             -- = sampledAt (also w-end above): never in window
 ('b-120','EDGE-A2','2026-11-07T08:02:00Z','{}'),           -- = sampledAt + 120 s
 ('b-600','EDGE-A2','2026-11-07T08:10:00Z','{}');           -- = sampledAt + 600 s

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

CREATE FUNCTION pg_temp.fail(msg text) RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '%', msg; END $$;
-- must_fail(statement, expected message fragment): the statement must raise an error containing the fragment.
CREATE FUNCTION pg_temp.must_fail(stmt text, fragment text) RETURNS text LANGUAGE plpgsql AS $$
BEGIN
  BEGIN EXECUTE stmt; EXCEPTION WHEN OTHERS THEN
    IF position(fragment IN SQLERRM) > 0 THEN RETURN 'REFUSED OK: ' || fragment; END IF;
    RAISE EXCEPTION 'unexpected error for %: %', stmt, SQLERRM;
  END;
  RAISE EXCEPTION 'statement unexpectedly succeeded: %', stmt;
END $$;

\echo '--- R1 platform_admin (P1+P3+PL), country, stale 2100 s, skew 120 s ---'
EXECUTE s('u-admin-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL) \gset r1_
\echo :r1_summary
-- registered: A1–A4,A6–A10,A12,A13 (11) + T1–T6 (6) + D1,D2 (2) + N1 (1) = 20; RETIRED A5/A11 and legacy L1 excluded.
-- boundREAL: A1,A2,A3,A6,A7,A8,A9,A10,A13 + T1–T6 + D1,D2 + N1 (REAL identity without shadow → unknown, not offline) = 18.
-- realConnected: A2, A10, T6 (offset-zone time parses correctly) = 3. T1–T5 bad time → realUnknownTime (A7 + 5 = 6).
-- realPendingReview: A8, A9, A13, D1, D2 = 5; pendingReviewAlarm: A9, D1, D2 = 3.
-- alarm devices (ACTIVE only): REAL A2, A10 = 2; SIM A1, A10 = 2; All = A1, A2, A10 = 3 (A10 counted once).
-- records in window: REAL attributed w-real, w-oos, m-a10-r1, m-a10-r2, m-n1 = 5; REAL unattributed w-orph-real = 1;
--   SIM attributed w-start, m-a10-s1 = 2; TEST unattributed w-orph-test = 1 → total 9; distinct assets A1, A2, A9, A10, N1 = 5.
-- futureSkew (≥ 08:02:00): f-scoped, f-orphan, b-120, b-600 = 4.
SELECT pg_temp.expect('R1', :'r1_summary', '{
  "projects":2, "assetsRegistered":20, "assetsDraft":5, "assetsOutOfService":2, "assetsNoRuntime":1,
  "boundREAL":18, "boundSIMULATION":5, "boundTEST":0,
  "realConnected":3, "realPendingReview":5, "pendingReviewAlarm":3, "realStale":2, "realUnknownTime":6, "realOffline":1, "realLive":1,
  "alarmDevicesReal":2, "alarmDevicesSim":2, "alarmDevicesTest":0, "alarmDevicesAll":3,
  "recordsCells":{"REAL":{"attributed":5,"unattributed":1},"SIMULATION":{"attributed":2,"unattributed":0},"TEST":{"attributed":0,"unattributed":1}},
  "recordsTotal":9, "recordsAssets":5, "recordsFutureSkew":4}');
SELECT 'R1 pendingReviewAssets ' || CASE WHEN (:'r1_summary'::jsonb->'pendingReviewAssets') = '[{"id":"A13","alarm":false,"assetStatus":"DRAFT"},{"id":"A8","alarm":false,"assetStatus":"DRAFT"},{"id":"A9","alarm":true,"assetStatus":"OUT_OF_SERVICE"},{"id":"D1","alarm":true,"assetStatus":"DRAFT"},{"id":"D2","alarm":true,"assetStatus":"DRAFT"}]'::jsonb THEN 'OK' ELSE pg_temp.fail('R1 pendingReviewAssets ' || (:'r1_summary'::jsonb->>'pendingReviewAssets')) END;

\echo '--- R2 same user, region 330000 (descendant admin codes supplied by the service: 330481): P3 (unlocated) leaves ---'
EXECUTE s('u-admin-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,ARRAY[330481]) \gset r2_
\echo :r2_summary
SELECT pg_temp.expect('R2', :'r2_summary', '{"projects":1, "assetsRegistered":19, "assetsNoRuntime":1, "realConnected":3, "recordsTotal":8, "recordsAssets":4, "recordsFutureSkew":4}');

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

\echo '--- R6 viewer P1, stale 600 s: A2 (observed 10 min ago) becomes stale, A10/T6 (1–2 min) stay fresh ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',600,120,NULL) \gset r6_
\echo :r6_summary
SELECT pg_temp.expect('R6', :'r6_summary', '{"realConnected":2, "realStale":3, "alarmDevicesReal":1}');

\echo '--- K0 skew 0 s (contract minimum): A6 (+1 h) stale; futureSkew counts ≥ 08:00:00 → w-end/b-0 (08:00) + b-120 + b-600 + f-scoped + f-orphan ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,0,NULL) \gset k0_
\echo :k0_summary
SELECT pg_temp.expect('K0', :'k0_summary', '{"maxFutureSkewSeconds":0, "realConnected":3, "recordsFutureSkew":6}');

\echo '--- K120 skew 120 s (default): b-120 is exactly on the boundary and IS future (>=), w-end/b-0 are in neither ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL) \gset k120_
SELECT pg_temp.expect('K120', :'k120_summary', '{"maxFutureSkewSeconds":120, "recordsFutureSkew":4}');

\echo '--- K600 skew 600 s (contract maximum): b-120 no longer future; b-600 exactly on the boundary IS future; A6 (+3600 s) still stale ---'
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,600,NULL) \gset k600_
\echo :k600_summary
SELECT pg_temp.expect('K600', :'k600_summary', '{"maxFutureSkewSeconds":600, "realConnected":3, "realStale":2, "recordsFutureSkew":3}');

\echo '--- KB shadow boundary: a REAL observation exactly at sampledAt + 600 s is fresh with skew 600, stale with skew 599 ---'
UPDATE device_shadow SET payload_json = jsonb_set(payload_json::jsonb, '{presence,lastSeenAt}', '"2026-11-07T08:10:00Z"')::json WHERE device_id = 'EDGE-A6';
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,600,NULL) \gset kb1_
SELECT pg_temp.expect('KB600', :'kb1_summary', '{"realConnected":4, "realStale":1}');
EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,599,NULL) \gset kb2_
SELECT pg_temp.expect('KB599', :'kb2_summary', '{"realConnected":3, "realStale":2}');
UPDATE device_shadow SET payload_json = jsonb_set(payload_json::jsonb, '{presence,lastSeenAt}', '"2026-11-07T09:00:00Z"')::json WHERE device_id = 'EDGE-A6';

\echo '--- KX out-of-contract parameters are refused by the statement itself (defence in depth; the handler returns 400 first) ---'
SELECT pg_temp.must_fail($q$EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,-1,NULL)$q$, 'maxFutureSkewSeconds out of range');
SELECT pg_temp.must_fail($q$EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,601,NULL)$q$, 'maxFutureSkewSeconds out of range');
SELECT pg_temp.must_fail($q$EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,7200,NULL)$q$, 'maxFutureSkewSeconds out of range');
SELECT pg_temp.must_fail($q$EXECUTE s('u-viewer-p1','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',299,120,NULL)$q$, 'staleAfterSeconds out of range');
