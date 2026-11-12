// R5-UX-R1 shared helpers: mocked Inventory API (no server writes) and scene probes.
// The scene graph is observed through three.js' own `__THREE_DEVTOOLS__` hook
// (every Scene/Object3D announces itself), so the probes work identically on the
// pre-fix and post-fix code without adding test-only hooks to product code.
import { expect, type Page, type Route } from '@playwright/test';
import { cloudDevice, cloudAlarm } from '../../../apps/web-console/tests/cloud-fixtures';
import { inventorySummary, routeSummary } from '../summary-mock';

export type Proj = { id: string; name: string; adminCode: number | null; devices: number };
export type Asset = { id: string; projectId: string; alarm?: boolean };

export const located: Proj[] = [
  { id: 'P001', name: 'Project-A', adminCode: 330481, devices: 2 },
  { id: 'P002', name: 'Project-B', adminCode: null, devices: 1 },
];
export const locatedAssets: Asset[] = [
  { id: 'A-1', projectId: 'P001', alarm: true },
  { id: 'A-2', projectId: 'P001' },
  { id: 'B-1', projectId: 'P002' },
];
/** Every project lacks an administrative location: the map must not invent any marker. */
export const unlocated: Proj[] = [
  { id: 'P101', name: 'Project-U1', adminCode: null, devices: 2 },
  { id: 'P102', name: 'Project-U2', adminCode: null, devices: 1 },
];
export const unlocatedAssets: Asset[] = [
  { id: 'U-1', projectId: 'P101' },
  { id: 'U-2', projectId: 'P101' },
  { id: 'U-3', projectId: 'P102' },
];

export async function api(page: Page, projects: Proj[], assets: Asset[], userId = 'r5') {
  await page.route('**/auth/session', (r) =>
    r.fulfill({
      json: {
        userId,
        displayName: 'R5 验收',
        projects: projects.map((p) => ({ projectId: p.id, projectName: p.name, role: 'viewer' })),
        csrfToken: 'synthetic-csrf-test-only',
        expiresAt: '2099-01-01T00:00:00Z',
        idleExpiresAt: '2099-01-01T00:00:00Z',
      },
    })
  );
  const rawProjects = projects.map((p) => ({
    id: p.id,
    name: p.name,
    lifecycle: 'PLANNED',
    province: '',
    city: '',
    county: '',
    locationLabel: '',
    adminCode: p.adminCode,
    scheduleText: '',
    notes: '',
    simulatedCount: 0,
    runtimeDeviceCount: 0,
    realConnectedCount: 0,
    assetDeviceCount: p.devices,
  }));
  const rawAssets = assets.map((a) => ({
    assetDeviceId: a.id,
    projectId: a.projectId,
    displayName: a.id,
    craneModel: null,
    ratedCapacityT: null,
    spanM: null,
    workClass: null,
    grabType: null,
    lifecycle: 'PLANNED',
    runtimeSource: a.alarm ? 'SIMULATION' : null,
    runtimeDeviceId: a.alarm ? `RT-${a.id}` : null,
    connectivity: a.alarm ? 'MQTT_CONNECTED' : null,
  }));
  const shadows = assets
    .filter((a) => a.alarm)
    .map((a) => {
      const s = cloudDevice(`RT-${a.id}`);
      s.reported.alarmState = 'ACTIVE';
      return s;
    });
  await page.route('**/api/v1/**', async (r: Route) => {
    const path = new URL(r.request().url()).pathname;
    return r.fulfill({
      json:
        path === '/api/v1/projects'
          ? rawProjects
          : path === '/api/v1/assets/devices'
            ? rawAssets
            : path === '/api/v1/devices'
              ? shadows
              : path === '/api/v1/alarms'
                ? shadows.length
                  ? [cloudAlarm()]
                  : []
                : {},
    });
  });
  await routeSummary(page, async (c) => inventorySummary(projects, assets, c.region));
}

/** Install before navigation: records every three.js Scene so probes can read the real graph. */
export async function captureScenes(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { __r5Scenes: unknown[]; __THREE_DEVTOOLS__: EventTarget };
    w.__r5Scenes = [];
    const t = new EventTarget();
    t.addEventListener('observe', (e) => {
      const d = (e as CustomEvent).detail as { isScene?: boolean };
      if (d?.isScene) w.__r5Scenes.push(d);
    });
    w.__THREE_DEVTOOLS__ = t;
  });
}

export type SceneProbe = {
  /** Every mesh named merged-side-flow in the live map scene. */
  walls: { count: number; effectivelyVisible: number; inScene: number };
  /** Country region groups still rendered (effectively visible). */
  countryRegionsVisible: number;
  geometries: number;
  textures: number;
  drawCalls: number;
};

/** Reads the map scene graph: wall count/visibility (including ancestors) and GPU counters. */
export async function probe(page: Page): Promise<SceneProbe> {
  return page.evaluate(() => {
    type O = {
      name: string;
      visible: boolean;
      parent: O | null;
      children: O[];
      isScene?: boolean;
      userData: Record<string, unknown>;
      traverse: (f: (o: O) => void) => void;
    };
    const scenes = (window as unknown as { __r5Scenes: O[] }).__r5Scenes;
    // The map scene is the one that contains country regions (groups carrying an adcode).
    const map = [...scenes].reverse().find((s) => {
      let hit = false;
      s.traverse((o) => {
        if (o.userData?.adcode === 110000) hit = true;
      });
      return hit;
    })!;
    const shown = (o: O | null): boolean => {
      for (let n = o; n; n = n.parent) if (!n.visible) return false;
      return true;
    };
    const walls: O[] = [];
    let countryRegionsVisible = 0;
    map.traverse((o) => {
      if (o.name === 'merged-side-flow') walls.push(o);
    });
    // Country groups: direct children of a group whose children carry province adcodes.
    map.traverse((o) => {
      if (typeof o.userData?.adcode === 'number' && (o.userData.adcode as number) % 10000 === 0 && o.userData.depth === 0.72 && shown(o)) countryRegionsVisible++;
    });
    const d = (window as unknown as { __spatialDiagnostics: () => { memory: { geometries: number; textures: number }; drawCalls: number } }).__spatialDiagnostics();
    return {
      walls: {
        count: walls.length,
        effectivelyVisible: walls.filter(shown).length,
        inScene: walls.filter((w) => {
          let n: O | null = w;
          while (n?.parent) n = n.parent;
          return n === map;
        }).length,
      },
      countryRegionsVisible,
      geometries: d.memory.geometries,
      textures: d.memory.textures,
      drawCalls: d.drawCalls,
    };
  });
}

export type Framing = {
  canvas: { left: number; top: number; width: number; height: number };
  inset: number;
  usable: { width: number; height: number };
  region: { minX: number; maxX: number; minY: number; maxY: number };
  fillW: number;
  fillH: number;
  fill: number;
  margins: { left: number; right: number; top: number; bottom: number };
  preset: string;
  adcode: number;
};

/** Projected bounds of the CURRENT region's real geometry versus the uncovered canvas. */
export async function framing(page: Page): Promise<Framing> {
  return page.evaluate(() => {
    const canvas = document.querySelector('.map-canvas canvas')!.getBoundingClientRect();
    const d = (window as unknown as { __spatialDiagnostics: () => { region: { minX: number; maxX: number; minY: number; maxY: number }; preset: string; adcode: number; viewOffset: number } }).__spatialDiagnostics();
    // The covered strip is what the camera itself compensates for: an open drawer
    // sets a projection view offset of half its width (CameraRig.setViewPadding).
    const inset = (d.viewOffset ?? 0) * 2;
    const usableW = canvas.width - inset,
      usableH = canvas.height;
    const r = d.region;
    const fillW = (r.maxX - r.minX) / usableW,
      fillH = (r.maxY - r.minY) / usableH;
    return {
      canvas: { left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height },
      inset,
      usable: { width: usableW, height: usableH },
      region: r,
      fillW: +fillW.toFixed(3),
      fillH: +fillH.toFixed(3),
      fill: +Math.max(fillW, fillH).toFixed(3),
      margins: {
        left: Math.round(r.minX),
        right: Math.round(usableW - r.maxX),
        top: Math.round(r.minY),
        bottom: Math.round(usableH - r.maxY),
      },
      preset: d.preset,
      adcode: d.adcode,
    };
  });
}

export const main = (page: Page) => page.locator('main.overview');

/** Wait until the scene reports an idle state at `adcode` and the camera has settled. */
export async function settled(page: Page, adcode: number) {
  await expect
    .poll(async () => page.evaluate(() => (window as unknown as { __spatialDiagnostics?: () => { adcode: number } }).__spatialDiagnostics?.()?.adcode), { timeout: 30000 })
    .toBe(adcode);
  await expect(main(page)).toHaveAttribute('data-scene-state', /IDLE/, { timeout: 30000 });
  // camera-controls smoothTime 0.18 s; wait until the projected region stops moving.
  let last = '';
  for (let i = 0; i < 40; i++) {
    const f = await framing(page);
    const key = [f.region.minX, f.region.maxX, f.region.minY, f.region.maxY].map((v) => Math.round(v)).join(',');
    if (key === last) return;
    last = key;
    await page.waitForTimeout(250);
  }
}
