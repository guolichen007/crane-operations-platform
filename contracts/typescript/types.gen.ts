// Generated from contracts/model.json. DO NOT EDIT.
export interface Position {
  x: number;
  y: number;
  z: number;
}
export interface ReportedState {
  operatingState: "DISCONNECTED" | "SILENT" | "LIVE" | "EVENT" | "UNKNOWN";
  mode: "AUTO" | "MANUAL" | "UNKNOWN";
  alarmState: "NONE" | "ACTIVE" | "UNKNOWN";
  runtimeTotal: number;
  position: Position;
  load: number;
}
export interface BirthPayload {
  edgeHealth: "EDGE_HEALTHY" | "DEGRADED" | "UNKNOWN";
  controllerHealth: "CONTROLLER_HEALTHY" | "DEGRADED" | "UNKNOWN";
  sensorHealth: "HEALTHY" | "DEGRADED" | "UNKNOWN";
  softwareVersion: string;
  reported: ReportedState;
}
export interface HeartbeatPayload {
  edgeHealth: "EDGE_HEALTHY" | "DEGRADED" | "UNKNOWN";
  controllerHealth: "CONTROLLER_HEALTHY" | "DEGRADED" | "UNKNOWN";
  sensorHealth: "HEALTHY" | "DEGRADED" | "UNKNOWN";
}
export interface SnapshotPayload {
  edgeHealth: "EDGE_HEALTHY" | "DEGRADED" | "UNKNOWN";
  controllerHealth: "CONTROLLER_HEALTHY" | "DEGRADED" | "UNKNOWN";
  sensorHealth: "HEALTHY" | "DEGRADED" | "UNKNOWN";
  reported: ReportedState;
}
export interface LiveTelemetryPayload {
  leaseId: string;
  generation: number;
  reported: ReportedState;
}
export interface DeathPayload {
  reason: "GRACEFUL" | "UNEXPECTED";
}
export interface AlarmPayload {
  code: string;
  text: string;
}
export interface EventPayload {
  code: string;
  text: string;
}
export interface WatchCommandPayload {
  leaseId: string;
  generation: number;
  ttlSeconds: number;
  telemetryHz: 1;
  expiresAt: string;
}
export interface WatchAckPayload {
  commandId: string;
  leaseId: string;
  generation: number;
  action: "WATCH_START" | "WATCH_RENEW" | "WATCH_STOP";
  result: "APPLIED" | "DUPLICATE" | "STALE" | "EXPIRED" | "REJECTED";
  reason: string;
  edgeState: "DISCONNECTED" | "SILENT" | "LIVE" | "EVENT";
  appliedAt: string;
}
export interface EventStoredAckPayload {
  sourceMessageId: string;
  result: "STORED";
}
export interface Presence {
  connectivity: "MQTT_CONNECTED" | "OFFLINE" | "UNKNOWN";
  edgeHealth: "EDGE_HEALTHY" | "DEGRADED" | "UNKNOWN";
  controllerHealth: "CONTROLLER_HEALTHY" | "DEGRADED" | "UNKNOWN";
  sensorHealth: "HEALTHY" | "DEGRADED" | "UNKNOWN";
  bootId: string;
  bootSeq: number;
  sequence: number;
  lastSeenAt: string;
  disconnectedAt: string;
  disconnectReason: "NONE" | "GRACEFUL" | "UNEXPECTED" | "MIGRATED";
}
export interface Shadow {
  deviceId: string;
  tenantId: string;
  projectId: string;
  presence: Presence;
  reported: ReportedState;
  updatedAt: string;
  version: number;
  upstreamStatus: "SILENT" | "STARTING" | "LIVE" | "STOPPING" | "FAILED" | "EXPIRED";
}
export interface LiveSession {
  sessionId: string;
  deviceId: string;
  userId: string;
  expiresAt: string;
  ttlSeconds: number;
  telemetryHz: 1;
  upstreamStatus: "SILENT" | "STARTING" | "LIVE" | "STOPPING" | "FAILED" | "EXPIRED";
}
export interface InventoryProjectInput {
  id: string;
  name: string;
  lifecycle: "PLANNED" | "PRE_DEVELOPMENT" | "COMPLETED";
  province: string;
  city: string;
  county: string;
  locationLabel: string;
  adminCode: number | null;
  scheduleText: string;
  notes: string;
}
export interface InventoryAssetInput {
  assetDeviceId: string;
  projectId: string;
  displayName: string;
  craneModel: string | null;
  ratedCapacityT: number | null;
  spanM: number | null;
  workClass: string | null;
  grabType: string | null;
  lifecycle: "PLANNED" | "PRE_DEVELOPMENT" | "COMPLETED";
}
export interface RuntimeDescriptor {
  runtimeDeviceId: string;
  projectId: string;
  assetDeviceId: string | null;
  sourceMode: "SIMULATION" | "REAL" | "TEST";
}
export interface InventoryDocument {
  tenantId: string;
  projects: (InventoryProjectInput)[];
  assetDevices: (InventoryAssetInput)[];
  runtimeDevices: (RuntimeDescriptor)[];
}
export interface FleetManifest {
  tenantId: string;
  devices: (RuntimeDescriptor)[];
}
export interface InventoryProject {
  id: string;
  name: string;
  lifecycle: "PLANNED" | "PRE_DEVELOPMENT" | "COMPLETED";
  province: string;
  city: string;
  county: string;
  locationLabel: string;
  adminCode: number | null;
  scheduleText: string;
  notes: string;
  assetDeviceCount: number;
  runtimeDeviceCount: number;
  realConnectedCount: number;
  simulatedCount: number;
}
export interface AssetDevice {
  assetDeviceId: string;
  projectId: string;
  displayName: string;
  craneModel: string | null;
  ratedCapacityT: number | null;
  spanM: number | null;
  workClass: string | null;
  grabType: string | null;
  lifecycle: "PLANNED" | "PRE_DEVELOPMENT" | "COMPLETED";
  runtimeSource: "SIMULATION" | "REAL" | "TEST" | null;
  runtimeDeviceId: string | null;
  connectivity: "MQTT_CONNECTED" | "OFFLINE" | "UNKNOWN" | null;
}
export type Envelope = {
  schemaVersion: 1;
  messageId: string;
  tenantId: string;
  projectId: string;
  deviceId: string;
  bootId: string;
  bootSeq: number;
  sequence: number;
  sentAt: string;
  correlationId: string;
} & (
  | { kind: "BIRTH"; payload: BirthPayload }
  | { kind: "HEARTBEAT"; payload: HeartbeatPayload }
  | { kind: "SNAPSHOT"; payload: SnapshotPayload }
  | { kind: "LIVE"; payload: LiveTelemetryPayload }
  | { kind: "LWT"; payload: DeathPayload }
  | { kind: "DEATH"; payload: DeathPayload }
  | { kind: "ALARM"; payload: AlarmPayload }
  | { kind: "EVENT"; payload: EventPayload }
  | { kind: "WATCH_START"; payload: WatchCommandPayload }
  | { kind: "WATCH_RENEW"; payload: WatchCommandPayload }
  | { kind: "WATCH_STOP"; payload: WatchCommandPayload }
  | { kind: "WATCH_ACK"; payload: WatchAckPayload }
  | { kind: "EVENT_STORED_ACK"; payload: EventStoredAckPayload }
);
