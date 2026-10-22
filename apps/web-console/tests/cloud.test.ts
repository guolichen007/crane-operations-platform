import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  listAlarms,
  listDevices,
  getDevice,
  openLive,
  renewLive,
  closeLive,
} from '../src/cloud/api';
import {
  formatCloudTime,
  deviceState,
  silentExplanation,
  summarizeDevices,
} from '../src/cloud/format';
import { cloudDevice, cloudFleet, cloudAlarm, cloudTime, viewerLease } from './cloud-fixtures';

afterEach(() => vi.unstubAllGlobals());
describe('Cloud API client', () => {
  it('uses existing contract endpoints, same-origin cookie and bounded requests', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(cloudFleet())));
    vi.stubGlobal('fetch', fetcher);
    expect(await listDevices()).toHaveLength(20);
    expect(fetcher).toHaveBeenLastCalledWith(
      '/api/v1/devices',
      expect.objectContaining({
        credentials: 'same-origin',
        method: 'GET',
        signal: expect.any(AbortSignal),
      })
    );
    fetcher.mockImplementation(async () => new Response(JSON.stringify(cloudAlarm())));
    await listAlarms();
    expect(fetcher.mock.lastCall?.[0]).toBe('/api/v1/alarms');
    await getDevice('device/a');
    expect(fetcher.mock.lastCall?.[0]).toBe('/api/v1/devices/device%2Fa/shadow');
    fetcher.mockImplementation(async () => new Response(JSON.stringify(viewerLease())));
    await openLive('SIM-001');
    expect(fetcher.mock.lastCall).toEqual([
      '/api/v1/devices/SIM-001/live-sessions',
      expect.objectContaining({ method: 'POST' }),
    ]);
    await renewLive('viewer-1');
    expect(fetcher.mock.lastCall?.[0]).toBe('/api/v1/live-sessions/viewer-1/renew');
    fetcher.mockResolvedValue(new Response(null, { status: 204 }));
    await closeLive('viewer-1', true);
    expect(fetcher.mock.lastCall).toEqual([
      '/api/v1/live-sessions/viewer-1',
      expect.objectContaining({ method: 'DELETE', keepalive: true }),
    ]);
    expect(fetcher.mock.lastCall?.[1]).not.toHaveProperty('signal');
  });
  it('treats already deleted leases as cleaned, but surfaces authorization/server errors', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 404 }));
    vi.stubGlobal('fetch', fetcher);
    await expect(closeLive('gone')).resolves.toBeUndefined();
    fetcher.mockResolvedValue(new Response(null, { status: 401 }));
    await expect(listAlarms()).rejects.toThrow('请先');
    fetcher.mockResolvedValue(new Response(null, { status: 503 }));
    await expect(listDevices()).rejects.toThrow('503');
  });
});
it('formats Cloud timestamps in Shanghai, including midnight and invalid/Go zero values', () => {
  expect(formatCloudTime(cloudTime)).toBe('2026-10-22 16:49:15');
  expect(formatCloudTime('2026-10-21T16:00:00Z')).toBe('2026-10-22 00:00:00');
  for (const value of [undefined, '', 'invalid', '0001-01-01T00:00:00Z'])
    expect(formatCloudTime(value)).toBe('—');
});
it('gives readable SILENT semantics without changing protocol values', () => {
  const device = cloudDevice();
  expect(deviceState(device)).toBe('在线 · 静默监测');
  expect(silentExplanation).toContain('仅上传心跳和状态快照');
  device.presence.connectivity = 'OFFLINE';
  expect(deviceState(device)).toBe('离线 · 静默监测');
});
it('derives runtime counts solely from existing Shadow fields without producing geography', () => {
  const devices = cloudFleet();
  devices[0]!.upstreamStatus = 'LIVE';
  devices[1]!.upstreamStatus = 'STARTING';
  devices[2]!.presence.connectivity = 'OFFLINE';
  const before = JSON.stringify(devices);
  expect(summarizeDevices(devices)).toEqual({ total: 20, online: 19, live: 1 });
  expect(summarizeDevices([])).toEqual({ total: 0, online: 0, live: 0 });
  expect(JSON.stringify(devices)).toBe(before);
});
