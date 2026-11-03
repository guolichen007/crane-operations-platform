import { computed, shallowRef, type ComputedRef, type Ref } from 'vue';
import type { Project, Device, Lifecycle } from '../data/types';
import { projects as demoAllProjects, devices as demoAllDevices, events as demoEvents, realProjects as demoRealProjects } from '../data';
import { listProjects, listAssets, listDevices, listAlarms } from '../cloud/api';
import { mapApiDevice } from './device-status';
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
  /** Idempotent load: concurrent calls dedup; a failed load can be retried. */
  ensureLoaded(): Promise<void>;
  refresh(): Promise<void>;
}

function lifecycleMap(l: InventoryProject['lifecycle']): Lifecycle {
  // Contract lifecycle 'COMPLETED' maps to the demo 'OPERATIONAL' bucket.
  return l === 'COMPLETED' ? 'OPERATIONAL' : l;
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
  let loaded = false;
  let inFlight: Promise<void> | null = null;

  async function load() {
    loading.value = true;
    try {
      const [rawProjects, rawAssets, rawShadows, rawAlarms] = await Promise.all([
        listProjects(),
        listAssets(),
        listDevices(),
        listAlarms(),
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
        adminPath: p.adminCode ? [p.adminCode] : [],
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
        time: a.sentAt.slice(11, 19),
        kind: '告警',
        text: `${a.payload.text || a.payload.code} · ${a.deviceId}`,
        color: '#ff665f',
      }));
      error.value = '';
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
