-- H2-0-R3 DRAFT summary query (single statement = one MVCC read snapshot). RESEARCH ONLY — NOT production code.
-- Parameters (renumbered in R3; the unused source/region-echo parameters were removed, see contract §3.4):
--   $1 user id
--   $2 alarm window start      (timestamptz, inclusive)
--   $3 sampledAt               (timestamptz; window end, exclusive; freshness reference)
--   $4 staleAfterSeconds       (int, configurable)
--   $5 maxFutureSkewSeconds    (int, configurable — no hard-coded skew)
--   $6 region admin codes      (int[]; NULL = whole authorized scope). The admin hierarchy is not stored in
--                               PostgreSQL; the service expands a region to descendant admin codes from the
--                               geo manifest. It can only NARROW scoped_projects.
WITH
 grants AS (               -- 1. authorization first: enabled user × read-capable membership
   SELECT m.project_id FROM project_memberships m JOIN users u ON u.id = m.user_id
   WHERE m.user_id = $1 AND u.enabled AND m.role IN ('viewer','operator','maintainer','platform_admin')),
 scoped_projects AS (      -- 2. Inventory projects only; region narrows AFTER grants
   SELECT p.id, p.admin_code FROM projects p JOIN grants g ON g.project_id = p.id
   WHERE p.lifecycle IS NOT NULL
     AND ($6::int[] IS NULL OR p.admin_code = ANY($6::int[]))),
 assets AS (               -- 3. asset identity = asset_devices.id; RETIRED excluded (same predicate as inventory.Assets)
   SELECT a.id, a.project_id, a.asset_status FROM asset_devices a JOIN scoped_projects sp ON sp.id = a.project_id
   WHERE a.asset_status <> 'RETIRED'),
 bindings AS (             -- 4. all runtime identities of in-scope assets
   SELECT d.id AS runtime_id, d.asset_device_id AS asset_id, d.source_mode,
          s.payload_json#>>'{presence,connectivity}' AS conn,
          s.payload_json#>>'{reported,alarmState}' AS alarm_state,
          s.payload_json#>>'{reported,operatingState}' AS op_state,
          NULLIF(s.payload_json#>>'{presence,lastSeenAt}','')::timestamptz AS observed_at
   FROM devices d JOIN assets a ON a.id = d.asset_device_id
   LEFT JOIN device_shadow s ON s.device_id = d.id),
 fresh AS (                -- 5. freshness against ONE sampledAt, configurable stale / skew
   SELECT b.*,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NOT NULL
              AND b.observed_at >  $3::timestamptz - make_interval(secs => $4)
              AND b.observed_at <= $3::timestamptz + make_interval(secs => $5), false) AS connected_fresh,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NULL, false)            AS connected_no_time
   FROM bindings b),
 per_asset AS (            -- 6. exactly one row per asset (asset de-duplication happens here)
   SELECT a.id,
     bool_or(f.source_mode='REAL')                         AS has_real,
     bool_or(f.source_mode='SIMULATION')                   AS has_sim,
     bool_or(f.source_mode='TEST')                         AS has_test,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status='ACTIVE') AS real_connected,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status IN ('DRAFT','OUT_OF_SERVICE')) AS real_pending_review,
     bool_or(a.asset_status = 'OUT_OF_SERVICE')            AS out_of_service,
     bool_or(f.source_mode='REAL' AND f.conn='MQTT_CONNECTED' AND f.observed_at IS NOT NULL AND NOT f.connected_fresh) AS real_stale,
     bool_or(f.source_mode='REAL' AND f.connected_no_time) AS real_unknown_time,
     bool_or(f.source_mode='REAL' AND f.conn='OFFLINE')    AS real_offline,
     bool_or(f.source_mode='REAL'       AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS real_alarm,
     bool_or(f.source_mode='SIMULATION' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS sim_alarm,
     bool_or(f.source_mode='TEST'       AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS test_alarm,
     -- any source, per ASSET: an asset alarming on both REAL and SIM counts once here,
     -- so alarmDevicesAll <= REAL + SIMULATION + TEST (source columns may overlap; never add them).
     bool_or(f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS any_alarm,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.op_state='LIVE' AND a.asset_status='ACTIVE') AS real_live,
     count(f.runtime_id) = 0                               AS no_runtime,
     bool_or(a.asset_status = 'DRAFT')                     AS draft
   FROM assets a LEFT JOIN fresh f ON f.asset_id = a.id GROUP BY a.id),
 scoped_alarms AS (        -- 7. ONE alarm scope for window AND futureSkew:
                           --    project authorization via devices.project_id ∩ scoped_projects;
                           --    bound identities only if their asset is in `assets` (RETIRED / out-of-region excluded);
                           --    orphan identities (asset_device_id IS NULL) kept, attributed to their project.
   SELECT al.observed_at, d.source_mode, (d.asset_device_id IS NULL) AS unattributed
   FROM alarms al JOIN devices d ON d.id = al.device_id JOIN scoped_projects sp ON sp.id = d.project_id
   WHERE d.asset_device_id IS NULL OR EXISTS (SELECT 1 FROM assets x WHERE x.id = d.asset_device_id)),
 alarm_window AS (         -- 8. half-open window [start, sampledAt)
   SELECT * FROM scoped_alarms WHERE observed_at >= $2::timestamptz AND observed_at < $3::timestamptz)
SELECT json_build_object(
  'projects',            (SELECT count(*) FROM scoped_projects),
  'assetsRegistered',    (SELECT count(*) FROM per_asset),
  'assetsDraft',         (SELECT count(*) FILTER (WHERE draft) FROM per_asset),
  'assetsOutOfService',  (SELECT count(*) FILTER (WHERE out_of_service) FROM per_asset),
  'assetsNoRuntime',     (SELECT count(*) FILTER (WHERE no_runtime) FROM per_asset),
  'boundREAL',           (SELECT count(*) FILTER (WHERE has_real) FROM per_asset),
  'boundSIMULATION',     (SELECT count(*) FILTER (WHERE has_sim) FROM per_asset),
  'boundTEST',           (SELECT count(*) FILTER (WHERE has_test) FROM per_asset),
  'realConnected',       (SELECT count(*) FILTER (WHERE real_connected) FROM per_asset),
  'realPendingReview',   (SELECT count(*) FILTER (WHERE real_pending_review) FROM per_asset),
  'realStale',           (SELECT count(*) FILTER (WHERE real_stale) FROM per_asset),
  'realUnknownTime',     (SELECT count(*) FILTER (WHERE real_unknown_time) FROM per_asset),
  'realOffline',         (SELECT count(*) FILTER (WHERE real_offline) FROM per_asset),
  'realLive',            (SELECT count(*) FILTER (WHERE real_live) FROM per_asset),
  'alarmDevicesReal',    (SELECT count(*) FILTER (WHERE real_alarm) FROM per_asset),
  'alarmDevicesSim',     (SELECT count(*) FILTER (WHERE sim_alarm) FROM per_asset),
  'alarmDevicesTest',    (SELECT count(*) FILTER (WHERE test_alarm) FROM per_asset),
  'alarmDevicesAll',     (SELECT count(*) FILTER (WHERE any_alarm) FROM per_asset),
  -- mutually exclusive cells (source_mode × attribution); the 6 cells sum to recordsTotal.
  'recordsCells',        (SELECT json_build_object(
                            'REAL',       json_build_object('attributed', count(*) FILTER (WHERE source_mode='REAL' AND NOT unattributed),       'unattributed', count(*) FILTER (WHERE source_mode='REAL' AND unattributed)),
                            'SIMULATION', json_build_object('attributed', count(*) FILTER (WHERE source_mode='SIMULATION' AND NOT unattributed), 'unattributed', count(*) FILTER (WHERE source_mode='SIMULATION' AND unattributed)),
                            'TEST',       json_build_object('attributed', count(*) FILTER (WHERE source_mode='TEST' AND NOT unattributed),       'unattributed', count(*) FILTER (WHERE source_mode='TEST' AND unattributed)))
                          FROM alarm_window),
  'recordsTotal',        (SELECT count(*) FROM alarm_window),
  -- same scope as the window; records at/after sampledAt + maxSkew. Records in [sampledAt, sampledAt + maxSkew)
  -- arrived after the sample instant and belong to the next sample — counted nowhere in this one.
  'recordsFutureSkew',   (SELECT count(*) FROM scoped_alarms WHERE observed_at >= $3::timestamptz + make_interval(secs => $5)),
  'sampledAt',           $3::timestamptz
) AS summary;
