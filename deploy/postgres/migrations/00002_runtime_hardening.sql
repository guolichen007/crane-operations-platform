-- +goose Up
-- One-time pre-release conversion. Runtime accepts only the typed V1 contract.
UPDATE device_shadow SET payload_json = jsonb_build_object(
 'deviceId',device_id,'tenantId',payload_json->>'tenantId','projectId',payload_json->>'projectId',
 'presence',jsonb_build_object('connectivity',COALESCE(payload_json#>>'{reported,connectivity}','UNKNOWN'),
   'edgeHealth',COALESCE(payload_json#>>'{reported,edgeHealth}','UNKNOWN'),
   'controllerHealth',COALESCE(payload_json#>>'{reported,controllerHealth}','UNKNOWN'),
   'sensorHealth',COALESCE(NULLIF(payload_json#>>'{reported,sensorHealth}',''),'UNKNOWN'),
   'bootId',COALESCE(NULLIF(payload_json#>>'{reported,bootId}',''),'migrated'), 'bootSeq',0,'sequence',0,
   'lastSeenAt',updated_at,'disconnectedAt',updated_at,'disconnectReason','MIGRATED'),
 'reported',jsonb_build_object('operatingState',COALESCE(NULLIF(payload_json#>>'{reported,operatingState}',''),'UNKNOWN'),
   'mode',COALESCE(NULLIF(payload_json#>>'{reported,mode}',''),'UNKNOWN'),
   'alarmState',COALESCE(NULLIF(payload_json#>>'{reported,alarmState}',''),'UNKNOWN'),
   'runtimeTotal',COALESCE((payload_json#>'{reported,runtimeTotal}')::numeric,0),
   'position',jsonb_build_object('x',COALESCE((payload_json#>'{reported,x}')::numeric,0),'y',COALESCE((payload_json#>'{reported,y}')::numeric,0),'z',COALESCE((payload_json#>'{reported,z}')::numeric,0)),
   'load',COALESCE((payload_json#>'{reported,load}')::numeric,0)),
 'version',version,'updatedAt',updated_at,'upstreamStatus','SILENT'
) WHERE NOT payload_json ? 'presence';

-- Preserve old anonymous alarm/event records in the typed API model; historical
-- bootSeq=1 is a migration marker and these records are never republished.
UPDATE alarms SET payload_json = jsonb_build_object(
 'schemaVersion',1,'messageId',message_id,'tenantId',payload_json->>'tenantId','projectId',payload_json->>'projectId',
 'deviceId',device_id,'bootId',payload_json->>'bootId','bootSeq',1,'sequence',GREATEST(COALESCE((payload_json->>'sequence')::bigint,1),1),
 'sentAt',observed_at,'kind','ALARM','correlationId','',
 'payload',jsonb_build_object('code',payload_json->>'code','text',payload_json->>'text)
) WHERE NOT payload_json ? 'payload';
UPDATE events SET payload_json = jsonb_build_object(
 'schemaVersion',1,'messageId',message_id,'tenantId',payload_json->>'tenantId','projectId',payload_json->>'projectId',
 'deviceId',device_id,'bootId',payload_json->>'bootId','bootSeq',1,'sequence',GREATEST(COALESCE((payload_json->>'sequence')::bigint,1),1),
 'sentAt',observed_at,'kind','EVENT','correlationId','',
 'payload',jsonb_build_object('code',payload_json->>'code','text',payload_json->>'text)
) WHERE NOT payload_json ? 'payload';

CREATE INDEX live_sessions_device_expiry ON live_sessions(device_id,expires_at);
CREATE TABLE device_live_state (
 device_id text PRIMARY KEY REFERENCES devices(id), lease_id text NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 status text NOT NULL CHECK(status IN ('SILENT','STARTING','LIVE','STOPPING')),
 desired_until timestamptz NOT NULL, last_command_id text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL
);
CREATE TABLE device_commands (
 command_id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id),
 kind text NOT NULL CHECK(kind IN ('WATCH_START','WATCH_RENEW','WATCH_STOP')),
 generation bigint NOT NULL CHECK(generation>0),
 status text NOT NULL CHECK(status IN ('PENDING','APPLIED','FAILED','EXPIRED')),
 created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 acked_at timestamptz, result text NOT NULL DEFAULT '',
 payload_json jsonb NOT NULL, last_sent_at timestamptz
);
CREATE INDEX device_commands_pending ON device_commands(status,expires_at);
-- +goose Down
-- Typed payload migration is intentionally forward-only: do not revive the POC wire protocol.
DROP TABLE device_commands,device_live_state;
DROP INDEX live_sessions_device_expiry;
