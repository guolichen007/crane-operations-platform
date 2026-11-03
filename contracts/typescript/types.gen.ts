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
export interface ProjectGrant {
  projectId: string;
  projectName: string;
  role: "viewer" | "operator" | "maintainer" | "platform_admin";
}
export interface WebSession {
  userId: string;
  displayName: string;
  projects: (ProjectGrant)[];
  csrfToken: string;
  expiresAt: string;
  idleExpiresAt: string;
}
export interface AssetMasterData {
  id: string;
  projectId: string;
  assetCode: string | null;
  assetType: string;
  assetStatus: "DRAFT" | "ACTIVE" | "OUT_OF_SERVICE" | "RETIRED";
  manufacturerId: string | null;
  manufacturerSnapshot: string;
  brandSnapshot: string;
  modelSnapshot: string;
  serialNumber: string | null;
  manufactureDate: string | null;
  installationDate: string | null;
  commissioningDate: string | null;
  liftingHeightM: number | null;
  applicationType: string | null;
  controlMode: string | null;
  version: number;
  specialEquipmentCode: string | null;
  useRegistrationNo: string | null;
}
export interface AssetLocation {
  id: string;
  projectId: string;
  parentId: string | null;
  kind: "SITE" | "PLANT" | "WORKSHOP" | "BAY" | "RUNWAY" | "OTHER";
  code: string | null;
  name: string;
  province: string;
  city: string;
  county: string;
  longitude: number | null;
  latitude: number | null;
  coordinateSystem: "WGS84" | "GCJ02" | "LOCAL_SITE" | "UNKNOWN";
  coordinateSource: "MANUAL" | "SURVEY" | "IMPORTED" | "SITE_CENTER";
  status: "ACTIVE" | "DISABLED";
  version: number;
  address: string;
}
export interface EquipmentInstallation {
  id: string;
  assetId: string;
  equipmentCode: string;
  typeCode: string;
  catalogItemId: string | null;
  serialNumber: string | null;
  semanticSlot: string | null;
  positionLabel: string | null;
  criticality: "LOW" | "MEDIUM" | "HIGH" | "SAFETY_CRITICAL";
  administrativeState: "IN_SERVICE" | "MAINTENANCE" | "OUT_OF_SERVICE" | "FAULT_REPORTED";
  version: number;
}
export interface EquipmentCatalogItem {
  id: string;
  projectId: string | null;
  name: string;
  typeCode: string;
  manufacturerId: string | null;
  brand: string;
  model: string;
  productCode: string | null;
  specSchemaVersion: string;
  status: "ACTIVE" | "DISABLED";
  version: number;
}
export interface EquipmentType {
  typeCode: string;
  displayName: string;
  domain: "MECHANICAL" | "CONTROL" | "PERCEPTION" | "COMPUTE" | "NETWORK" | "POWER" | "SENSOR" | "OTHER";
  specSchemaVersion: string;
  status: "ACTIVE" | "DISABLED";
}
export interface PortfolioScope {
  id: string;
  tenantId: string;
  code: string;
  name: string;
  status: "ACTIVE" | "DISABLED";
  version: number;
}
export interface ProjectIntake {
  id: string;
  portfolioScopeId: string;
  tenantId: string;
  intakeCode: string;
  projectName: string;
  customerName: string | null;
  projectType: string;
  priority: "LOW" | "MEDIUM" | "HIGH" | "CRITICAL";
  riskLevel: "LOW" | "MEDIUM" | "HIGH";
  scopeSummary: string;
  siteReference: string | null;
  registeredAt: string;
  registeredBy: string;
  requestedStart: string | null;
  requestedHandover: string | null;
  plannedStart: string | null;
  plannedEnd: string | null;
  status: "REGISTERED" | "REVIEW" | "PLANNING" | "APPROVED" | "REJECTED" | "CANCELLED";
  projectManagerUserId: string | null;
  technicalLeadUserId: string | null;
  siteLeadUserId: string | null;
  formalProjectId: string | null;
  version: number;
}
export interface ProjectIntakeInput {
  portfolioScopeId: string;
  intakeCode: string;
  projectName: string;
  customerName: string | null;
  projectType: string;
  priority: "LOW" | "MEDIUM" | "HIGH" | "CRITICAL";
  riskLevel: "LOW" | "MEDIUM" | "HIGH";
  scopeSummary: string;
  siteReference: string | null;
  requestedStart: string | null;
  requestedHandover: string | null;
  plannedStart: string | null;
  plannedEnd: string | null;
  projectManagerUserId: string | null;
  technicalLeadUserId: string | null;
  siteLeadUserId: string | null;
}
export interface ScheduleItem {
  id: string;
  projectId: string | null;
  sourceType: "PROJECT_ACTIVITY" | "WORK_ORDER" | "INSPECTION" | "CALIBRATION" | "COMMISSIONING" | "SITE_WINDOW" | "NETWORK_CUTOVER";
  sourceId: string;
  name: string;
  startAt: string;
  endAt: string;
  timezone: string;
  status: "PLANNED" | "APPROVED" | "IN_PROGRESS" | "COMPLETED" | "CANCELLED";
  priority: "LOW" | "MEDIUM" | "HIGH" | "CRITICAL";
  exclusiveLocationId: string | null;
  version: number;
  portfolioScopeId: string | null;
}
export interface Resource {
  id: string;
  portfolioScopeId: string;
  resourceType: "PERSON" | "TEAM" | "SITE" | "WORKSHOP" | "BAY" | "RUNWAY" | "ASSET" | "TEST_EQUIPMENT" | "TOOL" | "VEHICLE" | "COMMISSIONING_SLOT" | "NETWORK_CUTOVER_SLOT" | "OTHER";
  referenceId: string | null;
  name: string;
  capacity: number;
  exclusive: boolean;
  overrideAllowed: boolean;
  homeTimezone: string;
  status: "ACTIVE" | "DISABLED";
  version: number;
}
export interface ResourceReservation {
  id: string;
  scheduleItemId: string;
  resourceId: string;
  startAt: string;
  endAt: string;
  quantity: number;
  status: "PENDING" | "ACTIVE" | "CANCELLED";
  version: number;
}
export interface ScheduleConflict {
  id: number;
  conflictType: "RESOURCE_OVERLAP" | "RESOURCE_OVER_CAPACITY" | "SITE_OVERLAP" | "RUNWAY_OVERLAP" | "ASSET_DOWNTIME_OVERLAP" | "DEPENDENCY_VIOLATION" | "CALENDAR_UNAVAILABLE" | "SITE_ACCESS_WINDOW" | "COMMISSIONING_CONFLICT" | "NETWORK_CUTOVER_CONFLICT";
  severity: "WARNING" | "BLOCKING";
  leftItemId: string;
  rightItemId: string | null;
  resourceId: string | null;
  status: "OPEN" | "RESOLVED" | "OVERRIDDEN";
}
export interface ScheduleDependency {
  id: string;
  predecessorItemId: string;
  successorItemId: string;
  dependencyType: "FS" | "SS" | "FF" | "SF";
  lagMinutes: number;
}
export interface ScheduleBaseline {
  id: string;
  portfolioScopeId: string;
  projectId: string | null;
  name: string;
  baselineVersion: number;
  approvedBy: string;
  approvedAt: string;
}
export interface Manufacturer {
  id: string;
  projectId: string | null;
  name: string;
  shortName: string;
  countryRegion: string | null;
  website: string | null;
  status: "ACTIVE" | "DISABLED";
  version: number;
}
export interface ManufacturerInput {
  projectId: string | null;
  name: string;
  shortName: string;
  countryRegion: string | null;
  website: string | null;
}
export interface EquipmentRelation {
  id: string;
  assetId: string;
  fromEquipmentInstallationId: string;
  toEquipmentInstallationId: string;
  relationType: "CONTAINS" | "MOUNTED_ON" | "DRIVES" | "CONTROLS" | "SENSES" | "MONITORS" | "POWERED_BY" | "NETWORKED_TO" | "PROTECTS" | "BACKS_UP";
  source: string;
  notes: string;
  version: number;
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
