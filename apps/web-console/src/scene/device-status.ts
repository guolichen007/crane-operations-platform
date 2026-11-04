import type { Device, DeviceStatus, Project, TelemetrySource } from '../data/types';
import type { AssetDevice, Shadow } from '../../../../contracts/typescript/types.gen';

/**
 * Frozen API device status priority. Shadow is the authoritative source:
 * no runtime binding -> NOT_COMMISSIONED; binding without Shadow or offline
 * connectivity -> OFFLINE (connectivity wins over a stale alarm); connected +
 * active alarm -> ALARM; connected + LIVE/EVENT -> RUNNING; else STANDBY.
 * MAINTENANCE is never guessed from connectivity.
 */
export function apiAssetStatus(a: AssetDevice, shadow: Shadow | undefined): DeviceStatus {
  if (!a.runtimeDeviceId) return 'NOT_COMMISSIONED';
  if (!shadow) return 'OFFLINE';
  if (shadow.presence.connectivity !== 'MQTT_CONNECTED') return 'OFFLINE';
  if (shadow.reported.alarmState === 'ACTIVE') return 'ALARM';
  const op = shadow.reported.operatingState;
  if (op === 'LIVE' || op === 'EVENT') return 'RUNNING';
  return 'STANDBY'; // SILENT / UNKNOWN / DISCONNECTED — never green by default.
}

export function apiTelemetrySource(a: AssetDevice, shadow: Shadow | undefined): TelemetrySource {
  return !a.runtimeDeviceId || !shadow ? 'none' : 'cloud';
}

export function apiDeviceMode(shadow: Shadow | undefined): Device['mode'] {
  const mode = shadow?.reported.mode;
  if (mode === 'AUTO') return '自动';
  if (mode === 'MANUAL') return '手动';
  return null; // UNKNOWN / absent — not fabricated.
}

/**
 * Maps an API AssetDevice + optional Shadow into the unified scene Device
 * view-model. Unknown values stay NaN / null / '' — never fabricated as 0 /
 * 自动 / 闭合 / 无故障.
 */
export function mapApiDevice(a: AssetDevice, base: Project, shadow: Shadow | undefined): Device {
  const reported = shadow?.reported;
  const presence = shadow?.presence;
  const pos = reported?.position;
  return {
    ...base,
    telemetrySource: apiTelemetrySource(a, shadow),
    runtimeSource: a.runtimeSource,
    commissioningStatus:
      base.lifecycle === 'OPERATIONAL' && a.runtimeDeviceId ? 'OPERATIONAL' : 'NOT_COMMISSIONED',
    grab: null, // no grab contract from Shadow — unknown.
    mode: apiDeviceMode(shadow),
    fault: '',
    deviceId: a.assetDeviceId,
    deviceName: a.displayName,
    status: apiAssetStatus(a, shadow),
    todayHours: Number.NaN, // no per-day aggregation contract.
    totalHours: reported?.runtimeTotal ?? Number.NaN,
    load: reported?.load ?? Number.NaN,
    x: pos?.x ?? Number.NaN,
    y: pos?.y ?? Number.NaN,
    z: pos?.z ?? Number.NaN,
    heartbeat: presence?.lastSeenAt ?? '',
    // Technical metadata is authored on the AssetDevice, never inherited from a
    // project default; missing values stay NaN/'' so the UI renders '—', not
    // a fabricated "0 t / 0 m / A8".
    craneModel: a.craneModel ?? '',
    ratedCapacityT: a.ratedCapacityT ?? Number.NaN,
    spanM: a.spanM ?? Number.NaN,
    workClass: a.workClass ?? '',
    grabType: a.grabType ?? '',
  };
}
