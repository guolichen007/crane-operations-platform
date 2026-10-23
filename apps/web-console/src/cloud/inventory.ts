import type {
  InventoryProject,
  AssetDevice,
  Shadow,
} from '../../../../contracts/typescript/types.gen';
export const lifecycleName = {
  PLANNED: '计划实施',
  PRE_DEVELOPMENT: '软件预开发',
  COMPLETED: '已完成',
};
export function projectConnection(p: InventoryProject) {
  if (p.realConnectedCount > 0) return '真实在线';
  if (p.lifecycle === 'COMPLETED') return '待接入Cloud · 设备档案待补充';
  return '规划/预开发 · 尚未真实接入';
}
export function assetSource(a: AssetDevice) {
  if (a.runtimeSource === 'SIMULATION')
    return a.connectivity === 'MQTT_CONNECTED' ? '规划模拟 · 模拟在线' : '规划模拟 · 模拟未连接';
  if (a.runtimeSource === 'REAL')
    return a.connectivity === 'MQTT_CONNECTED' ? '真实运行源 · 真实在线' : '真实运行源 · 离线';
  if (a.runtimeSource === 'TEST') return '测试运行源';
  return '待接入Cloud';
}
export function inventorySummary(
  projects: InventoryProject[],
  assets: AssetDevice[],
  shadows: Shadow[]
) {
  const online = new Set(
    shadows.filter((d) => d.presence.connectivity === 'MQTT_CONNECTED').map((d) => d.deviceId)
  );
  return {
    projects: projects.length,
    planned: projects.filter((p) => p.lifecycle !== 'COMPLETED').length,
    awaiting: projects.filter((p) => p.lifecycle === 'COMPLETED' && p.runtimeDeviceCount === 0)
      .length,
    assets: assets.length,
    simulationOnline: assets.filter(
      (a) => a.runtimeSource === 'SIMULATION' && a.runtimeDeviceId && online.has(a.runtimeDeviceId)
    ).length,
    realOnline: assets.filter(
      (a) => a.runtimeSource === 'REAL' && a.runtimeDeviceId && online.has(a.runtimeDeviceId)
    ).length,
  };
}
