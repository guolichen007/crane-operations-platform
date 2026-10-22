// Generated from contracts/model.json. DO NOT EDIT.
export interface Message {
  schemaVersion: number;
  messageId: string;
  tenantId: string;
  projectId: string;
  deviceId: string;
  bootId: string;
  sequence: number;
  timestamp: string;
  kind: string;
  connectivity: string;
  edgeHealth: string;
  controllerHealth: string;
  sensorHealth: string;
  operatingState: string;
  mode: string;
  alarmState: string;
  softwareVersion: string;
  runtimeTotal: number;
  x: number;
  y: number;
  z: number;
  load: number;
  sessionId: string;
  ttlSeconds: number;
  telemetryHz: number;
  code: string;
  text: string;
}
export interface Shadow {
  deviceId: string;
  tenantId: string;
  projectId: string;
  reported: Message;
  updatedAt: string;
  version: number;
}
export interface LiveSession {
  sessionId: string;
  deviceId: string;
  userId: string;
  expiresAt: string;
  ttlSeconds: number;
  telemetryHz: number;
}
