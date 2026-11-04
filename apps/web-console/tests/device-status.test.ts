import { describe, expect, it } from 'vitest';
import type { AssetDevice, Shadow } from '../../../contracts/typescript/types.gen';
import type { Project } from '../src/data/types';
import { apiAssetStatus, apiTelemetrySource, mapApiDevice } from '../src/scene/device-status';

function asset(overrides: Partial<AssetDevice> = {}): AssetDevice {
  return {
    assetDeviceId: 'A1',
    projectId: 'P1',
    displayName: '行车 A1',
    craneModel: null,
    ratedCapacityT: null,
    spanM: null,
    workClass: null,
    grabType: null,
    lifecycle: 'PLANNED',
    runtimeSource: null,
    runtimeDeviceId: 'R1',
    connectivity: null,
    ...overrides,
  };
}

function shadow(overrides: Partial<Shadow> = {}): Shadow {
  return {
    deviceId: 'R1',
    tenantId: 'demo',
    projectId: 'P1',
    presence: {
      connectivity: 'MQTT_CONNECTED',
      edgeHealth: 'UNKNOWN',
      controllerHealth: 'UNKNOWN',
      sensorHealth: 'UNKNOWN',
      bootId: 'b',
      bootSeq: 1,
      sequence: 1,
      lastSeenAt: '2026-11-03T12:00:00Z',
      disconnectedAt: '',
      disconnectReason: 'NONE',
    },
    reported: {
      operatingState: 'SILENT',
      mode: 'AUTO',
      alarmState: 'NONE',
      runtimeTotal: 123.5,
      position: { x: 1, y: 2, z: 3 },
      load: 42,
    },
    updatedAt: '2026-11-03T12:00:00Z',
    version: 1,
    upstreamStatus: 'SILENT',
    ...overrides,
  };
}

const base: Project = {
  id: 'P1',
  projectName: 'Project-A',
  dataSource: 'real',
  province: '浙江省',
  city: '嘉兴市',
  county: null,
  locationLabel: '浙江嘉兴',
  approximateLng: 0,
  approximateLat: 0,
  craneModel: 'QZ10t',
  ratedCapacityT: 10,
  spanM: 25,
  workClass: 'A8',
  grabType: '',
  quantity: 1,
  lifecycle: 'OPERATIONAL',
  plannedImplementation: '',
  scheduleText: '',
  coordinateAccuracy: 'city_center_demo',
  adminPath: [330000],
  adminCode: 330000,
};

describe('API 设备状态优先级（冻结）', () => {
  it('无 runtimeDeviceId → NOT_COMMISSIONED', () => {
    expect(apiAssetStatus(asset({ runtimeDeviceId: null }), undefined)).toBe('NOT_COMMISSIONED');
  });
  it('有 runtime 但 Shadow 不存在 → OFFLINE', () => {
    expect(apiAssetStatus(asset(), undefined)).toBe('OFFLINE');
  });
  it('MQTT_CONNECTED + SILENT + NONE → STANDBY', () => {
    expect(apiAssetStatus(asset(), shadow())).toBe('STANDBY');
  });
  it('MQTT_CONNECTED + LIVE + NONE → RUNNING', () => {
    const s = shadow();
    s.reported.operatingState = 'LIVE';
    expect(apiAssetStatus(asset(), s)).toBe('RUNNING');
  });
  it('MQTT_CONNECTED + EVENT + NONE → RUNNING', () => {
    const s = shadow();
    s.reported.operatingState = 'EVENT';
    expect(apiAssetStatus(asset(), s)).toBe('RUNNING');
  });
  it('MQTT_CONNECTED + LIVE + ACTIVE → ALARM', () => {
    const s = shadow();
    s.reported.operatingState = 'LIVE';
    s.reported.alarmState = 'ACTIVE';
    expect(apiAssetStatus(asset(), s)).toBe('ALARM');
  });
  it('OFFLINE 连接 + ACTIVE → OFFLINE（连接优先于过期告警）', () => {
    const s = shadow();
    s.presence.connectivity = 'OFFLINE';
    s.reported.alarmState = 'ACTIVE';
    expect(apiAssetStatus(asset(), s)).toBe('OFFLINE');
  });
  it('MAINTENANCE 不来自 connectivity 猜测', () => {
    const s = shadow();
    s.reported.operatingState = 'UNKNOWN';
    expect(apiAssetStatus(asset(), s)).not.toBe('MAINTENANCE');
  });
});

describe('遥测来源与 ViewModel 映射', () => {
  it('无 runtime / 无 shadow → telemetrySource none，未知字段不伪造', () => {
    const d = mapApiDevice(asset({ runtimeDeviceId: null }), base, undefined);
    expect(d.telemetrySource).toBe('none');
    expect(d.status).toBe('NOT_COMMISSIONED');
    expect(Number.isNaN(d.x)).toBe(true);
    expect(Number.isNaN(d.load)).toBe(true);
    expect(d.grab).toBeNull();
    expect(d.mode).toBeNull();
    expect(d.heartbeat).toBe('');
  });
  it('有 shadow → cloud，position/runtimeTotal/mode/lastSeenAt 真实映射', () => {
    const d = mapApiDevice(asset(), base, shadow());
    expect(d.telemetrySource).toBe('cloud');
    expect(d.status).toBe('STANDBY');
    expect(d.x).toBe(1);
    expect(d.y).toBe(2);
    expect(d.z).toBe(3);
    expect(d.load).toBe(42);
    expect(d.totalHours).toBe(123.5);
    expect(d.mode).toBe('自动');
    expect(d.heartbeat).toBe('2026-11-03T12:00:00Z');
  });
  it('commissioning 保守：COMPLETED + runtime 才 OPERATIONAL', () => {
    expect(mapApiDevice(asset(), { ...base, lifecycle: 'OPERATIONAL' }, shadow()).commissioningStatus).toBe(
      'OPERATIONAL'
    );
    expect(
      mapApiDevice(asset(), { ...base, lifecycle: 'PLANNED' }, shadow()).commissioningStatus
    ).toBe('NOT_COMMISSIONED');
  });
});

describe('现场来源 runtimeSource 语义', () => {
  it('SIMULATION + shadow → runtimeSource SIMULATION 保留，telemetrySource 仍 cloud', () => {
    const d = mapApiDevice(asset({ runtimeSource: 'SIMULATION' }), base, shadow());
    expect(d.telemetrySource).toBe('cloud');
    expect(d.runtimeSource).toBe('SIMULATION');
  });
  it('REAL + shadow → runtimeSource REAL', () => {
    expect(mapApiDevice(asset({ runtimeSource: 'REAL' }), base, shadow()).runtimeSource).toBe('REAL');
  });
  it('TEST + shadow → runtimeSource TEST', () => {
    expect(mapApiDevice(asset({ runtimeSource: 'TEST' }), base, shadow()).runtimeSource).toBe('TEST');
  });
  it('无 runtime → runtimeSource null', () => {
    expect(mapApiDevice(asset({ runtimeDeviceId: null }), base, undefined).runtimeSource).toBeNull();
  });
});

describe('技术元数据不继承项目默认值', () => {
  it('AssetDevice 元数据缺失 → NaN/空串，不用项目默认 0 t / 0 m / A8', () => {
    const d = mapApiDevice(asset(), base, shadow());
    expect(d.craneModel).toBe('');
    expect(Number.isNaN(d.ratedCapacityT)).toBe(true);
    expect(Number.isNaN(d.spanM)).toBe(true);
    expect(d.workClass).toBe('');
    expect(d.grabType).toBe('');
  });
  it('AssetDevice 元数据存在 → 真实映射', () => {
    const d = mapApiDevice(
      asset({ craneModel: 'QZ16t', ratedCapacityT: 16, spanM: 28, workClass: 'A7', grabType: '液压多瓣抓斗' }),
      base,
      shadow()
    );
    expect(d.craneModel).toBe('QZ16t');
    expect(d.ratedCapacityT).toBe(16);
    expect(d.spanM).toBe(28);
    expect(d.workClass).toBe('A7');
    expect(d.grabType).toBe('液压多瓣抓斗');
  });
});

describe('telemetrySource 判定', () => {
  it('demo→none 由 provider 层控制；此处仅 API 判定', () => {
    expect(apiTelemetrySource(asset(), shadow())).toBe('cloud');
    expect(apiTelemetrySource(asset({ runtimeDeviceId: null }), undefined)).toBe('none');
    expect(apiTelemetrySource(asset(), undefined)).toBe('none');
  });
});
