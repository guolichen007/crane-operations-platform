import type { Shadow } from '../../../../contracts/typescript/types.gen';

const formatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
});
export function formatCloudTime(value?: string): string {
  if (!value || value.startsWith('0001-')) return '—';
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '—';
  const parts = Object.fromEntries(formatter.formatToParts(date).map((p) => [p.type, p.value]));
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second}`;
}
export const silentExplanation =
  '静默监测：设备保持云端连接，仅上传心跳和状态快照；进入 LIVE 后才开启实时数据流。';
export function deviceState(device: Shadow): string {
  const connectivity = { MQTT_CONNECTED: '在线', OFFLINE: '离线', UNKNOWN: '连接未知' }[
    device.presence.connectivity
  ];
  const state = {
    SILENT: '静默监测',
    LIVE: '实时采集',
    EVENT: '事件上报',
    DISCONNECTED: '采集断开',
    UNKNOWN: '采集状态未知',
  }[device.reported.operatingState];
  return `${connectivity} · ${state}`;
}
// Runtime summary deliberately has no dependency on spatial/demo data.
export function summarizeDevices(devices: Shadow[]) {
  return {
    total: devices.length,
    online: devices.filter((d) => d.presence.connectivity === 'MQTT_CONNECTED').length,
    live: devices.filter((d) => d.upstreamStatus === 'LIVE').length,
  };
}
