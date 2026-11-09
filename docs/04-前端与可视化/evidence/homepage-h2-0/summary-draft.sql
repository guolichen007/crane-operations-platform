-- H2-1B DRAFT summary query (single statement = one MVCC read snapshot). RESEARCH ONLY — NOT production code.
-- Shape chosen by H2-1B EXPLAIN study (evidence/homepage-h2-1b): variant "E" —
--   * alarms are time-bounded at the scan (observed_at >= windowStart) inside scoped_alarms,
--   * per_asset and scoped_alarms are each aggregated ONCE (agg_asset / agg_alarm) instead of ~20 scalar sub-selects,
--   * CTEs left to PostgreSQL's default (referenced-more-than-once CTEs materialize); forcing NOT MATERIALIZED
--     inflated the plan to 608 nodes, scanned alarms 4 times and ran ~9x slower than this shape.
-- Target: PostgreSQL 17 (production/CI image postgres:17.8). Uses pg_input_is_valid (PG ≥ 16).
-- Parameters:
--   $1 user id
--   $2 alarm window start      (timestamptz, inclusive)
--   $3 sampledAt               (timestamptz; window end, exclusive; freshness reference)
--   $4 staleAfterSeconds       (int, contract range 300–7200)
--   $5 maxFutureSkewSeconds    (int, contract range 0–600)
--   $6 region admin codes      (int[]; NULL = whole authorized scope; only NARROWS scoped_projects)
-- The handler validates $4/$5 and returns HTTP 400 before running this query; the `params` guard below
-- is defence in depth (an out-of-range value aborts the statement instead of producing numbers).
WITH
 params AS (
   SELECT
     -- the ELSE branch is a sub-select so it is evaluated only when reached (a constant cast would be folded at plan time)
     CASE WHEN $4::int BETWEEN 300 AND 7200 THEN $4::int ELSE (SELECT ('staleAfterSeconds out of range 300..7200: ' || $4)::int) END AS stale_s,
     CASE WHEN $5::int BETWEEN 0 AND 600   THEN $5::int ELSE (SELECT ('maxFutureSkewSeconds out of range 0..600: ' || $5)::int) END AS skew_s),
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
 bindings AS (             -- 4. all runtime identities of in-scope assets; observation time parsed DEFENSIVELY:
                           --    usable only if RFC 3339 with an explicit zone (Z or ±hh:mm), parseable, and ≥ 2000-01-01
                           --    (Go's zero time 0001-01-01T00:00:00Z is "no observation"). Anything else → NULL → per-device UNKNOWN.
                           --    A bad value never aborts the summary and is never counted as online.
   SELECT d.id AS runtime_id, d.asset_device_id AS asset_id, d.source_mode,
          s.payload_json#>>'{presence,connectivity}' AS conn,
          s.payload_json#>>'{reported,alarmState}' AS alarm_state,
          s.payload_json#>>'{reported,operatingState}' AS op_state,
          CASE WHEN t.raw ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$'
                AND pg_input_is_valid(t.raw, 'timestamptz')
                AND t.raw::timestamptz >= timestamptz '2000-01-01T00:00:00Z'
               THEN t.raw::timestamptz END AS observed_at
   FROM devices d JOIN assets a ON a.id = d.asset_device_id
   LEFT JOIN device_shadow s ON s.device_id = d.id
   LEFT JOIN LATERAL (SELECT s.payload_json#>>'{presence,lastSeenAt}' AS raw) t ON true),
 fresh AS (                -- 5. freshness against ONE sampledAt: (sampledAt − stale, sampledAt + skew]
   SELECT b.*,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NOT NULL
              AND b.observed_at >  $3::timestamptz - make_interval(secs => p.stale_s)
              AND b.observed_at <= $3::timestamptz + make_interval(secs => p.skew_s), false) AS connected_fresh,
     coalesce(b.conn = 'MQTT_CONNECTED' AND b.observed_at IS NULL, false)                  AS connected_no_time
   FROM bindings b CROSS JOIN params p),
 per_asset AS (            -- 6. exactly one row per asset (asset de-duplication happens here)
   SELECT a.id, a.asset_status,
     bool_or(f.source_mode='REAL')                         AS has_real,
     bool_or(f.source_mode='SIMULATION')                   AS has_sim,
     bool_or(f.source_mode='TEST')                         AS has_test,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status='ACTIVE') AS real_connected,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND a.asset_status IN ('DRAFT','OUT_OF_SERVICE')) AS real_pending_review,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status IN ('DRAFT','OUT_OF_SERVICE')) AS pending_review_alarm,
     bool_or(a.asset_status = 'OUT_OF_SERVICE')            AS out_of_service,
     bool_or(f.source_mode='REAL' AND f.conn='MQTT_CONNECTED' AND f.observed_at IS NOT NULL AND NOT f.connected_fresh) AS real_stale,
     bool_or(f.source_mode='REAL' AND f.connected_no_time) AS real_unknown_time,
     bool_or(f.source_mode='REAL' AND f.conn='OFFLINE')    AS real_offline,
     bool_or(f.source_mode='REAL'       AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS real_alarm,
     bool_or(f.source_mode='SIMULATION' AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS sim_alarm,
     bool_or(f.source_mode='TEST'       AND f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS test_alarm,
     -- any source, per ASSET: alarmDevicesAll <= REAL + SIMULATION + TEST (source columns may overlap; never add them).
     bool_or(f.connected_fresh AND f.alarm_state='ACTIVE' AND a.asset_status='ACTIVE') AS any_alarm,
     bool_or(f.source_mode='REAL' AND f.connected_fresh AND f.op_state='LIVE' AND a.asset_status='ACTIVE') AS real_live,
     count(f.runtime_id) = 0                               AS no_runtime,
     bool_or(a.asset_status = 'DRAFT')                     AS draft
   FROM assets a LEFT JOIN fresh f ON f.asset_id = a.id GROUP BY a.id, a.asset_status),
 scoped_alarms AS (        -- 7. ONE alarm scope for window AND futureSkew (authorization via devices.project_id;
                           --    bound identities only if their asset is in `assets`; orphan identities kept).
                           --    Time-bounded at the source: only [windowStart, …) can contribute to any metric.
   SELECT al.observed_at, d.source_mode, d.asset_device_id AS asset_id, (d.asset_device_id IS NULL) AS unattributed
   FROM alarms al JOIN devices d ON d.id = al.device_id JOIN scoped_projects sp ON sp.id = d.project_id
   WHERE al.observed_at >= $2::timestamptz
     AND (d.asset_device_id IS NULL OR EXISTS (SELECT 1 FROM assets x WHERE x.id = d.asset_device_id))),
 agg_asset AS (
   SELECT count(*) AS registered,
     count(*) FILTER (WHERE draft) AS draft, count(*) FILTER (WHERE out_of_service) AS oos, count(*) FILTER (WHERE no_runtime) AS no_runtime,
     count(*) FILTER (WHERE has_real) AS b_real, count(*) FILTER (WHERE has_sim) AS b_sim, count(*) FILTER (WHERE has_test) AS b_test,
     count(*) FILTER (WHERE real_connected) AS r_conn, count(*) FILTER (WHERE real_pending_review) AS r_review,
     count(*) FILTER (WHERE pending_review_alarm) AS r_review_alarm, count(*) FILTER (WHERE real_stale) AS r_stale,
     count(*) FILTER (WHERE real_unknown_time) AS r_unknown, count(*) FILTER (WHERE real_offline) AS r_offline, count(*) FILTER (WHERE real_live) AS r_live,
     count(*) FILTER (WHERE real_alarm) AS a_real, count(*) FILTER (WHERE sim_alarm) AS a_sim, count(*) FILTER (WHERE test_alarm) AS a_test, count(*) FILTER (WHERE any_alarm) AS a_all,
     coalesce(json_agg(json_build_object('id', id, 'assetStatus', asset_status, 'alarm', pending_review_alarm) ORDER BY id) FILTER (WHERE real_pending_review), '[]') AS review_list
   FROM per_asset),
 agg_alarm AS (
   SELECT
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='REAL' AND NOT unattributed) AS real_att,
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='REAL' AND unattributed) AS real_un,
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='SIMULATION' AND NOT unattributed) AS sim_att,
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='SIMULATION' AND unattributed) AS sim_un,
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='TEST' AND NOT unattributed) AS test_att,
     count(*) FILTER (WHERE observed_at < $3::timestamptz AND source_mode='TEST' AND unattributed) AS test_un,
     count(*) FILTER (WHERE observed_at < $3::timestamptz) AS total,
     count(DISTINCT asset_id) FILTER (WHERE observed_at < $3::timestamptz AND NOT unattributed) AS assets,
     count(*) FILTER (WHERE observed_at >= p.future_from) AS future
   FROM scoped_alarms, (SELECT $3::timestamptz + make_interval(secs => skew_s) AS future_from FROM params) p)
SELECT json_build_object(
  'staleAfterSeconds', p.stale_s, 'maxFutureSkewSeconds', p.skew_s,
  'projects', (SELECT count(*) FROM scoped_projects),
  'assetsRegistered', g.registered, 'assetsDraft', g.draft, 'assetsOutOfService', g.oos, 'assetsNoRuntime', g.no_runtime,
  'boundREAL', g.b_real, 'boundSIMULATION', g.b_sim, 'boundTEST', g.b_test,
  'realConnected', g.r_conn, 'realPendingReview', g.r_review, 'pendingReviewAlarm', g.r_review_alarm,
  'pendingReviewAssets', (SELECT coalesce(json_agg(e), '[]') FROM (SELECT e FROM json_array_elements(g.review_list) e LIMIT 100) x),
  'realStale', g.r_stale, 'realUnknownTime', g.r_unknown, 'realOffline', g.r_offline, 'realLive', g.r_live,
  'alarmDevicesReal', g.a_real, 'alarmDevicesSim', g.a_sim, 'alarmDevicesTest', g.a_test, 'alarmDevicesAll', g.a_all,
  'recordsCells', json_build_object('REAL', json_build_object('attributed', w.real_att, 'unattributed', w.real_un),
                                    'SIMULATION', json_build_object('attributed', w.sim_att, 'unattributed', w.sim_un),
                                    'TEST', json_build_object('attributed', w.test_att, 'unattributed', w.test_un)),
  'recordsTotal', w.total, 'recordsAssets', w.assets, 'recordsFutureSkew', w.future,
  'sampledAt', $3::timestamptz
) AS summary
FROM params p, agg_asset g, agg_alarm w;
