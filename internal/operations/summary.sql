-- Operations summary (Homepage H2-1C). ONE statement = one MVCC read snapshot; every number in the
-- response comes from this statement. Shape "E" from H2-1B (docs/04-前端与可视化/Homepage-H2-1B-性能验证与告警查询决策.md):
-- alarms are time-bounded at the scan; per_asset / scoped_alarms are each aggregated once.
--
-- $1  user id                         $2  roles that grant read (from identity.HasCapability, Go side)
-- $3  all projects (development only) $4  alarm window start (inclusive)
-- $5  sampledAt (window end, exclusive; freshness reference)
-- $6  staleAfterSeconds 300..7200     $7  maxFutureSkewSeconds 0..600
-- $8  country scope?                  $9/$10 parallel arrays: admin code → direct child of the scope
--                                     (0 = the scope region itself). Built in Go from the embedded geo index.
--                                     At country scope they cover every indexed code; at a region scope only
--                                     the region's descendants, so they can only NARROW the authorized set.
WITH
 params AS (
   -- defence in depth: the handler/config already enforce the ranges; an out-of-range value aborts the
   -- statement instead of producing numbers (sub-select so it is evaluated only when reached).
   SELECT
     CASE WHEN $6::int BETWEEN 300 AND 7200 THEN $6::int ELSE (SELECT ('staleAfterSeconds out of range: ' || $6)::int) END AS stale_s,
     CASE WHEN $7::int BETWEEN 0 AND 600   THEN $7::int ELSE (SELECT ('maxFutureSkewSeconds out of range: ' || $7)::int) END AS skew_s),
 grants AS (               -- 1. authorization first: enabled user × read-capable membership
   SELECT m.project_id FROM project_memberships m JOIN users u ON u.id = m.user_id
   WHERE m.user_id = $1 AND u.enabled AND m.role = ANY($2::text[])),
 authorized AS (           -- authorized Inventory projects (whole scope; region not applied)
   SELECT p.id, p.admin_code FROM projects p
   WHERE p.lifecycle IS NOT NULL AND ($3::bool OR p.id IN (SELECT project_id FROM grants))),
 region_map AS (
   SELECT code, child FROM unnest($9::int[], $10::int[]) AS t(code, child)),
 scoped_projects AS (      -- 2. region narrows AFTER authorization. A project without an indexed code
                           --    (NULL or unknown adminCode) is "unmapped": counted only at country scope.
   SELECT ap.id, rm.child, (rm.code IS NULL) AS unmapped
   FROM authorized ap LEFT JOIN region_map rm ON rm.code = ap.admin_code
   WHERE $8::bool OR rm.code IS NOT NULL),
 assets AS (               -- 3. asset identity = asset_devices.id; RETIRED excluded (same predicate as inventory.Assets)
   SELECT a.id, a.asset_status, sp.child, sp.unmapped FROM asset_devices a JOIN scoped_projects sp ON sp.id = a.project_id
   WHERE a.asset_status <> 'RETIRED'),
 bindings AS (             -- 4. all runtime identities; observation time parsed with NESTED CASE so a failing cast is
                           --    never evaluated (no reliance on AND evaluation order). Accepted only as RFC 3339 with an
                           --    explicit zone, parseable, and >= 2000-01-01; anything else → NULL → per-device UNKNOWN.
   SELECT d.id AS runtime_id, d.asset_device_id AS asset_id, d.source_mode,
          s.payload_json#>>'{presence,connectivity}' AS conn,
          s.payload_json#>>'{reported,alarmState}' AS alarm_state,
          s.payload_json#>>'{reported,operatingState}' AS op_state,
          CASE WHEN t.parsed >= timestamptz '2000-01-01T00:00:00Z' THEN t.parsed END AS observed_at
   FROM devices d JOIN assets a ON a.id = d.asset_device_id
   LEFT JOIN device_shadow s ON s.device_id = d.id
   LEFT JOIN LATERAL (
     SELECT CASE WHEN r.raw ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$'
                 THEN CASE WHEN pg_input_is_valid(r.raw, 'timestamptz') THEN r.raw::timestamptz END
            END AS parsed
     FROM (SELECT s.payload_json#>>'{presence,lastSeenAt}' AS raw) r) t ON true),
 fresh AS (                -- 5. freshness against ONE sampledAt: (sampledAt − stale, sampledAt + skew]
   SELECT b.*,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NOT NULL
              AND b.observed_at >  $5::timestamptz - make_interval(secs => p.stale_s)
              AND b.observed_at <= $5::timestamptz + make_interval(secs => p.skew_s), false) AS connected_fresh,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NULL, false)                  AS connected_no_time
   FROM bindings b CROSS JOIN params p),
 per_asset AS (            -- 6. exactly one row per asset (asset de-duplication happens here)
   SELECT a.id, a.asset_status, a.child, a.unmapped,
     bool_or(f.source_mode='REAL')                         AS has_real,
     bool_or(f.source_mode='SIMULATION')                   AS has_sim,
     bool_or(f.source_mode='TEST')                         AS has_test,
     coalesce(bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status='ACTIVE'), false) AS real_connected,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status IN ('DRAFT','OUT_OF_SERVICE')) AS real_pending_review,
     coalesce(bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status IN ('DRAFT','OUT_OF_SERVICE')), false) AS pending_review_alarm,
     bool_or(f.source_mode='REAL' AND f.conn='MQTT_CONNECTED' AND f.observed_at IS NOT NULL AND NOT f.connected_fresh) AS real_stale,
     bool_or(f.source_mode='REAL' AND f.connected_no_time) AS real_unknown_time,
     bool_or(f.source_mode='REAL' AND f.conn='OFFLINE')    AS real_offline,
     coalesce(bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE'), false) AS real_alarm,
     bool_or(f.source_mode='SIMULATION' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS sim_alarm,
     bool_or(f.source_mode='TEST'       AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS test_alarm,
     -- any source, per ASSET: devicesAll <= REAL + SIMULATION + TEST (source columns may overlap; never add them).
     bool_or(f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS any_alarm,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.op_state='LIVE' AND a.asset_status='ACTIVE') AS real_live,
     count(f.runtime_id) = 0                               AS no_runtime
   FROM assets a LEFT JOIN fresh f ON f.asset_id = a.id GROUP BY a.id, a.asset_status, a.child, a.unmapped),
 scoped_alarms AS (        -- 7. ONE alarm scope for window AND futureSkew: authorization via devices.project_id;
                           --    bound identities only if their asset is in `assets`; orphan identities kept.
   SELECT al.observed_at, d.source_mode, d.asset_device_id AS asset_id, (d.asset_device_id IS NULL) AS unattributed
   FROM alarms al JOIN devices d ON d.id = al.device_id JOIN scoped_projects sp ON sp.id = d.project_id
   WHERE al.observed_at >= $4::timestamptz
     AND (d.asset_device_id IS NULL OR EXISTS (SELECT 1 FROM assets x WHERE x.id = d.asset_device_id))),
 agg_asset AS (
   SELECT count(*) AS registered,
     count(*) FILTER (WHERE asset_status='DRAFT') AS draft, count(*) FILTER (WHERE asset_status='OUT_OF_SERVICE') AS oos,
     count(*) FILTER (WHERE no_runtime) AS no_runtime,
     count(*) FILTER (WHERE unmapped) AS unlocated, count(*) FILTER (WHERE NOT unmapped AND child = 0) AS at_scope,
     count(*) FILTER (WHERE has_real) AS b_real, count(*) FILTER (WHERE has_sim) AS b_sim, count(*) FILTER (WHERE has_test) AS b_test,
     count(*) FILTER (WHERE real_connected) AS r_conn, count(*) FILTER (WHERE real_pending_review) AS r_review,
     count(*) FILTER (WHERE pending_review_alarm) AS r_review_alarm, count(*) FILTER (WHERE real_stale) AS r_stale,
     count(*) FILTER (WHERE real_unknown_time) AS r_unknown, count(*) FILTER (WHERE real_offline) AS r_offline, count(*) FILTER (WHERE real_live) AS r_live,
     count(*) FILTER (WHERE real_alarm) AS a_real, count(*) FILTER (WHERE sim_alarm) AS a_sim, count(*) FILTER (WHERE test_alarm) AS a_test, count(*) FILTER (WHERE any_alarm) AS a_all
   FROM per_asset),
 agg_region AS (           -- per direct child of the scope; only mapped assets below the scope level
   SELECT child, count(*) AS registered, count(*) FILTER (WHERE real_connected) AS r_conn, count(*) FILTER (WHERE real_alarm) AS a_real
   FROM per_asset WHERE NOT unmapped AND child <> 0 GROUP BY child),
 review AS (               -- bounded detail list: at most 100 rows reach json_agg, deterministic byte order
   SELECT id, asset_status, pending_review_alarm FROM per_asset WHERE real_pending_review ORDER BY id COLLATE "C" LIMIT 100),
 agg_alarm AS (
   SELECT
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='REAL' AND NOT unattributed) AS real_att,
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='REAL' AND unattributed) AS real_un,
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='SIMULATION' AND NOT unattributed) AS sim_att,
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='SIMULATION' AND unattributed) AS sim_un,
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='TEST' AND NOT unattributed) AS test_att,
     count(*) FILTER (WHERE observed_at < $5::timestamptz AND source_mode='TEST' AND unattributed) AS test_un,
     count(*) FILTER (WHERE observed_at < $5::timestamptz) AS total,
     count(DISTINCT asset_id) FILTER (WHERE observed_at < $5::timestamptz AND NOT unattributed) AS assets,
     -- [sampledAt, sampledAt + skew) belongs to the next sample and is counted nowhere here.
     count(*) FILTER (WHERE observed_at >= f.future_from) AS future
   FROM scoped_alarms, (SELECT $5::timestamptz + make_interval(secs => skew_s) AS future_from FROM params) f)
SELECT json_build_object(
  'staleAfterSeconds', p.stale_s, 'maxFutureSkewSeconds', p.skew_s,
  'authorizedProjects', (SELECT count(*) FROM authorized),
  'projects', (SELECT count(*) FROM scoped_projects),
  'registered', g.registered, 'draft', g.draft, 'outOfService', g.oos, 'noRuntime', g.no_runtime,
  'unlocated', g.unlocated, 'atScopeLevel', g.at_scope,
  'boundREAL', g.b_real, 'boundSIMULATION', g.b_sim, 'boundTEST', g.b_test,
  'realConnected', g.r_conn, 'realPendingReview', g.r_review, 'pendingReviewAlarm', g.r_review_alarm,
  'pendingReviewAssets', (SELECT coalesce(json_agg(json_build_object('id', id, 'assetStatus', asset_status, 'alarm', pending_review_alarm) ORDER BY id COLLATE "C"), '[]') FROM review),
  'realStale', g.r_stale, 'realUnknownTime', g.r_unknown, 'realOffline', g.r_offline, 'realLive', g.r_live,
  'alarmDevicesReal', g.a_real, 'alarmDevicesSim', g.a_sim, 'alarmDevicesTest', g.a_test, 'alarmDevicesAll', g.a_all,
  'records', json_build_object('REAL', json_build_object('attributed', w.real_att, 'unattributed', w.real_un),
                               'SIMULATION', json_build_object('attributed', w.sim_att, 'unattributed', w.sim_un),
                               'TEST', json_build_object('attributed', w.test_att, 'unattributed', w.test_un)),
  'recordsTotal', w.total, 'recordsAssets', w.assets, 'recordsFutureSkew', w.future,
  'regions', (SELECT coalesce(json_agg(json_build_array(child, registered, r_conn, a_real) ORDER BY child), '[]') FROM agg_region)
) AS summary
FROM params p, agg_asset g, agg_alarm w
