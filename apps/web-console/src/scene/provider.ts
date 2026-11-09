import { computed, shallowRef, type ComputedRef, type Ref } from 'vue';
import type { Project, Device, Lifecycle } from '../data/types';
import { projects as demoAllProjects, devices as demoAllDevices, events as demoEvents, realProjects as demoRealProjects } from '../data';
import { listProjects, listAssets, listDevices, listAlarms } from '../cloud/api';
import { mapApiDevice } from './device-status';
import { formatCloudTime } from '../cloud/format';
import { loadGeoManifest } from './geo-manifest';
import type { InventoryProject } from '../../../../contracts/typescript/types.gen';

export type SceneEvent = { time: string; kind: string; text: string; color: string };

/**
 * SceneDataProvider is the single data source for the Twin Cockpit. The Three
 * scene, HUD, search and navigation consume this abstraction; Demo and API
 * modes differ only in the provider, never in the scene.
 */
export interface SceneDataProvider {
  readonly mode: 'demo' | 'api';
  readonly projects: Ref<Project[]>;
  readonly devices: Ref<Device[]>;
  readonly events: Ref<SceneEvent[]>;
  readonly realProjects: ComputedRef<Project[]>;
  readonly loading: Ref<boolean>;
  readonly error: Ref<string>;
  /**
   * ISO time at which this client last fetched successfully ('' for demo / never).
   * A client fetch time — NOT a device observation time (the Inventory API has no
   * observedAt contract yet, H2). Old projects/devices are kept when a later fetch
   * fails; `error` is then non-empty and the homepage marks the numbers stale.
   */
  readonly fetchedAt: Ref<string>;
  /**
   * Length of the /alarms detail page (the API serves the latest ≤100 records). A list length, NOT a
   * count: the homepage shows the windowed total from the operations summary instead (H2-1D). null in demo.
   */
  readonly alarmRecordCount: Ref<number | null>;
  /** Idempotent load: concurrent calls dedup; a failed load can be retried. */
  ensureLoaded(): Promise<void>;
  refresh(): Promise<void>;
}

function lifecycleMap(l: InventoryProject['lifecycle']): Lifecycle {
  // Contract lifecycle 'COMPLETED' maps to the demo 'OPERATIONAL' bucket.
  return l === 'COMPLETED' ? 'OPERATIONAL' : l;
}

// The scene's drill-down filters a project by whether its adminPath contains the
// current adcode. A city/county project must therefore carry its full ancestor
// path (province → prefecture → county), not just its own adcode, or province
// drill-down drops it. The parent chain comes from the same geo-manifest the
// MapSceneManager loads for drill-down, so the two stay consistent.
let adminParents: Map<number, number> | undefined;

async function loadAdminParents(): Promise<Map<number, number>> {
  if (adminParents) return adminParents;
  const map = new Map<number, number>();
  try {
    const manifest = await loadGeoManifest();
    for (const r of Object.values(manifest)) if (r.parent !== null) map.set(r.adcode, r.parent);
  } catch {
    // Fall through: an empty parent map yields [adminCode] as the path.
  }
  adminParents = map;
  return map;
}

function adminPathFor(code: number, parents: Map<number, number>): number[] {
  const path: number[] = [];
  let cur: number | undefined = code;
  while (cur !== undefined && cur !== 100000) {
    path.unshift(cur);
    cur = parents.get(cur);
  }
  return path;
}

function demoProvider(): SceneDataProvider {
  return {
    mode: 'demo',
    projects: shallowRef(demoAllProjects),
    devices: shallowRef(demoAllDevices),
    events: shallowRef(demoEvents as SceneEvent[]),
    realProjects: computed(() => demoRealProjects),
    loading: shallowRef(false),
    error: shallowRef(''),
    fetchedAt: shallowRef(''),
    alarmRecordCount: shallowRef<number | null>(null),
    async ensureLoaded() {},
    async refresh() {},
  };
}

function apiProvider(): SceneDataProvider {
  const projects = shallowRef<Project[]>([]);
  const devices = shallowRef<Device[]>([]);
  const events = shallowRef<SceneEvent[]>([]);
  const loading = shallowRef(true);
  const error = shallowRef('');
  const fetchedAt = shallowRef('');
  const alarmRecordCount = shallowRef<number | null>(null);
  let loaded = false;
  let inFlight: Promise<void> | null = null;

  async function load() {
    loading.value = true;
    try {
      // Data-source boundary: the flagship sandbox is driven by the INVENTORY
      // API (projects + assetDevices + runtime Shadow), not the formal Asset
      // center API (AssetMasterData). Only inventory projects carry adminCode,
      // so only they can be spatially located. Formal assets have no spatial
      // association yet and must not be painted into the sandbox as if they do.
      // Future association: formal Asset → Runtime Binding → spatial project.
      const [rawProjects, rawAssets, rawShadows, rawAlarms, adminParentsMap] = await Promise.all([
        listProjects(),
        listAssets(),
        listDevices(),
        listAlarms(),
        loadAdminParents(),
      ]);
      const mappedProjects: Project[] = rawProjects.map((p) => ({
        id: p.id,
        projectName: p.name,
        dataSource: 'real',
        province: p.province,
        city: p.city || null,
        county: p.county || null,
        locationLabel: p.locationLabel,
        approximateLng: 0,
        approximateLat: 0,
        craneModel: '',
        ratedCapacityT: 0,
        spanM: 0,
        workClass: '',
        grabType: '',
        quantity: p.assetDeviceCount,
        lifecycle: lifecycleMap(p.lifecycle),
        plannedImplementation: '',
        scheduleText: p.scheduleText,
        coordinateAccuracy: 'city_center_demo',
        adminPath: p.adminCode ? adminPathFor(p.adminCode, adminParentsMap) : [],
        adminCode: p.adminCode ?? undefined,
      }));
      const byProject = new Map(mappedProjects.map((p) => [p.id, p]));
      const shadowByDevice = new Map(rawShadows.map((s) => [s.deviceId, s]));
      const mappedDevices: Device[] = rawAssets.flatMap((a) => {
        const base = byProject.get(a.projectId);
        if (!base) return [];
        const shadow = a.runtimeDeviceId ? shadowByDevice.get(a.runtimeDeviceId) : undefined;
        return [mapApiDevice(a, base, shadow)];
      });
      projects.value = mappedProjects;
      devices.value = mappedDevices;
      events.value = rawAlarms.slice(0, 20).map((a) => ({
        // H0-D2 fix: show the Shanghai clock time of the instant, not the UTC substring of sentAt.
        time: formatCloudTime(a.sentAt).slice(11),
        kind: '告警',
        text: `${a.payload.text || a.payload.code} · ${a.deviceId}`,
        color: '#ff665f',
      }));
      error.value = '';
      fetchedAt.value = new Date().toISOString();
      alarmRecordCount.value = rawAlarms.length;
      loaded = true;
    } catch (e) {
      error.value = (e as Error).message;
      throw e;
    } finally {
      loading.value = false;
    }
  }

  return {
    mode: 'api',
    projects,
    devices,
    events,
    realProjects: computed(() => projects.value.filter((p) => p.dataSource === 'real')),
    loading,
    error,
    fetchedAt,
    alarmRecordCount,
    ensureLoaded() {
      if (loaded) return Promise.resolve();
      if (!inFlight) inFlight = load().finally(() => (inFlight = null));
      return inFlight;
    },
    refresh() {
      return load();
    },
  };
}

export const sceneProvider: SceneDataProvider =
  import.meta.env.VITE_DATA_MODE === 'api' ? apiProvider() : demoProvider();
