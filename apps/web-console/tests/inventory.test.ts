import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import type { AssetDevice, InventoryProject } from '../../../contracts/typescript/types.gen';
import { assetSource, projectConnection, inventorySummary } from '../src/cloud/inventory';
import { cloudDevice } from './cloud-fixtures';
it('separates real inventory, simulation sources and completed projects awaiting onboarding', () => {
  const document = JSON.parse(
    readFileSync('../../deploy/inventory/inventory.example.yaml', 'utf8')
  );
  const projects: InventoryProject[] = document.projects.map((p: InventoryProject) => ({
    ...p,
    assetDeviceCount: 0,
    runtimeDeviceCount: 0,
    realConnectedCount: 0,
    simulatedCount: 0,
  }));
  const assets: AssetDevice[] = document.assetDevices.map((a: AssetDevice, i: number) => ({
    ...a,
    runtimeSource: 'SIMULATION',
    runtimeDeviceId: document.runtimeDevices[i].runtimeDeviceId,
    connectivity: 'MQTT_CONNECTED',
  }));
  const devices = assets.map((a) => cloudDevice(a.runtimeDeviceId!));
  expect(inventorySummary(projects, assets, devices)).toEqual({
    projects: 9,
    planned: 6,
    awaiting: 3,
    assets: 10,
    simulationOnline: 10,
    realOnline: 0,
  });
  expect(projectConnection(projects[6]!)).toBe('待接入Cloud · 设备档案待补充');
  expect(projectConnection(projects[6]!)).not.toContain('离线');
  expect(assetSource(assets[0]!)).toBe('规划模拟 · 模拟在线');
  assets[0]!.runtimeSource = 'REAL';
  expect(inventorySummary(projects, assets, devices)).toMatchObject({
    simulationOnline: 9,
    realOnline: 1,
  });
  expect(assetSource(assets[0]!)).toBe('真实运行源 · 真实在线');
  assets[0]!.connectivity = 'OFFLINE';
  expect(assetSource(assets[0]!)).toBe('真实运行源 · 离线');
});
