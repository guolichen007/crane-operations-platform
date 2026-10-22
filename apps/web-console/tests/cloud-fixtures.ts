import type { LiveSession, Shadow } from '../../../contracts/typescript/types.gen';
import type { CloudAlarm } from '../src/cloud/api';
export const cloudTime = '2026-10-22T08:49:15.368508068Z';
export function cloudDevice(id = 'SIM-001'): Shadow {
  return {
    deviceId: id,
    tenantId: 'demo',
    projectId: 'simulator',
    presence: {
      connectivity: 'MQTT_CONNECTED',
      edgeHealth: 'EDGE_HEALTHY',
      controllerHealth: 'CONTROLLER_HEALTHY',
      sensorHealth: 'HEALTHY',
      bootId: 'test-boot',
      bootSeq: 1,
      sequence: 1,
      lastSeenAt: cloudTime,
      disconnectedAt: '0001-01-01T00:00:00Z',
      disconnectReason: 'NONE',
    },
    reported: {
      operatingState: 'SILENT',
      mode: 'AUTO',
      alarmState: 'NONE',
      runtimeTotal: 10,
      position: { x: 1, y: 2, z: 3 },
      load: 4,
    },
    updatedAt: cloudTime,
    version: 1,
    upstreamStatus: 'SILENT',
  };
}
export const cloudFleet = () =>
  Array.from({ length: 20 }, (_, i) => cloudDevice(`SIM-${String(i + 1).padStart(3, '0')}`));
export function cloudAlarm(): CloudAlarm {
  return {
    schemaVersion: 1,
    messageId: 'cloud-alarm-1',
    tenantId: 'demo',
    projectId: 'simulator',
    deviceId: 'SIM-020',
    bootId: 'test-boot',
    bootSeq: 1,
    sequence: 2,
    sentAt: cloudTime,
    kind: 'ALARM',
    correlationId: '',
    payload: { code: 'SIM_OVERLOAD', text: 'Cloud 测试负载告警' },
  };
}
export function viewerLease(): LiveSession {
  return {
    sessionId: 'viewer-1',
    deviceId: 'SIM-001',
    userId: 'dev-admin',
    expiresAt: new Date(Date.now() + 300000).toISOString(),
    ttlSeconds: 300,
    telemetryHz: 1,
    upstreamStatus: 'STARTING',
  };
}
