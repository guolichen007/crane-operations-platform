-- H2-0 identity/statistics probe. Run on an EMPTY database migrated to 00012 (all goose Up sections).
-- Read-only findings Q1–Q11 are quoted in Homepage-H2-0-运营数据契约.md §1/§9. Synthetic data only.
\set ON_ERROR_STOP 1
INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING;
DELETE FROM project_memberships; 
INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) VALUES
 ('PA','demo','项目A','PLANNED',330481),('PB','demo','项目B','PLANNED',440300),('PN','demo','无行政区','PLANNED',NULL);
INSERT INTO projects(id,tenant_id,name) VALUES ('LEGACY','demo','遗留无生命周期');
-- Asset created by Inventory import AND visible in the asset center: one row.
INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES
 ('A1','PA','1号行车','PLANNED'),('A2','PA','2号行车','PLANNED'),('B1','PB','1号行车','PLANNED'),('N1','PN','无位置','PLANNED');
UPDATE asset_devices SET asset_status='RETIRED' WHERE id='A2';
INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES ('L1','LEGACY','遗留','PLANNED');
-- Runtime identities: A1 has SIM and REAL (allowed by unique(asset,source_mode)).
INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES
 ('SIM-A1','PA','A1','SIMULATION'),('EDGE-A1','PA','A1','REAL'),('SIM-B1','PB','B1','SIMULATION'),('ORPHAN','PB',NULL,'TEST');
INSERT INTO device_shadow VALUES
 ('SIM-A1','{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T01:00:00Z"},"reported":{"operatingState":"LIVE","alarmState":"ACTIVE"}}','2026-11-07T01:00:05Z',1),
 ('EDGE-A1','{"presence":{"connectivity":"OFFLINE","lastSeenAt":"2026-11-06T20:00:00Z"},"reported":{"operatingState":"SILENT","alarmState":"NONE"}}','2026-11-06T20:00:00Z',1),
 ('ORPHAN','{"presence":{"connectivity":"MQTT_CONNECTED"},"reported":{"operatingState":"LIVE","alarmState":"ACTIVE"}}',now(),1);
INSERT INTO alarms VALUES ('m1','SIM-A1','2026-11-06T16:30:00Z','{}'),('m2','SIM-A1','2026-11-06T16:31:00Z','{}'),('m3','ORPHAN',now(),'{}');
\echo '--- Q1 asset rows (asset center and Inventory read the same table) ---'
SELECT count(*) AS asset_rows, count(DISTINCT id) AS distinct_ids FROM asset_devices;
\echo '--- Q2 runtime identities per asset (SIM+REAL both possible) ---'
SELECT asset_device_id, count(*) runtimes, string_agg(source_mode,',' ORDER BY source_mode) FROM devices WHERE asset_device_id IS NOT NULL GROUP BY 1 ORDER BY 1;
\echo '--- Q3 naive join counts would double count A1 ---'
SELECT count(*) naive_join_rows, count(DISTINCT a.id) distinct_assets FROM asset_devices a JOIN devices d ON d.asset_device_id=a.id;
\echo '--- Q4 current Inventory.Assets picks ONE runtime per asset: REAL first even if offline ---'
SELECT a.id,d.source_mode,d.id,s.payload_json#>>'{presence,connectivity}' conn FROM asset_devices a LEFT JOIN LATERAL(SELECT id,source_mode FROM devices WHERE asset_device_id=a.id ORDER BY CASE source_mode WHEN 'REAL' THEN 0 WHEN 'SIMULATION' THEN 1 ELSE 2 END,id LIMIT 1) d ON true LEFT JOIN device_shadow s ON s.device_id=d.id ORDER BY a.id;
\echo '--- Q5 Inventory.Projects hides lifecycle NULL projects but Assets does not filter them ---'
SELECT (SELECT count(*) FROM projects WHERE lifecycle IS NOT NULL) inventory_projects, (SELECT count(*) FROM asset_devices a JOIN projects p ON p.id=a.project_id WHERE p.lifecycle IS NULL) assets_in_hidden_projects;
\echo '--- Q6 retired assets still listed by Inventory.Assets (no asset_status filter) ---'
SELECT id, asset_status FROM asset_devices WHERE asset_status<>'ACTIVE';
\echo '--- Q7 alarms on devices without an asset (unattributable to an asset) ---'
SELECT a.message_id, d.asset_device_id FROM alarms a JOIN devices d ON d.id=a.device_id WHERE d.asset_device_id IS NULL;
\echo '--- Q8 alarm devices vs alarm records (A1 has 2 records, 1 device) ---'
SELECT count(*) records, count(DISTINCT device_id) devices FROM alarms;
\echo '--- Q9 same display name in different projects ---'
SELECT display_name, count(*), string_agg(project_id||'/'||id, ',') FROM asset_devices GROUP BY 1 HAVING count(*)>1;
\echo '--- Q10 observed_at vs updated_at times available ---'
SELECT device_id, payload_json#>>'{presence,lastSeenAt}' last_seen, updated_at FROM device_shadow ORDER BY 1;
\echo '--- Q11 alarm observed_at at UTC date boundary (H0-D2) ---'
SELECT message_id, observed_at AT TIME ZONE 'UTC' utc, observed_at AT TIME ZONE 'Asia/Shanghai' cst FROM alarms WHERE message_id IN ('m1','m2');
