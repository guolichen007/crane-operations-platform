import { computed, shallowRef, type ComputedRef, type Ref } from 'vue';
import type { Project, Device, DeviceStatus, Lifecycle } from '../data/types';
import { projects as demoAllProjects, devices as demoAllDevices, events as demoEvents, realProjects as demoRealProjects } from '../data';
import { listProjects, listAssets, listDevices, listAlarms } from '../cloud/api';
import type {
  InventoryProject,
  AssetDevice,
  Shadow,
} from '../../../../contracts/typescript/types.gen';

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
  refresh(): Promise<void>;
}

function lifecycleMap(l: InventoryProject['lifecycle']): Lifecycle {
  // Contract lifecycle 'COMPLETED' maps to the demo 'OPERATIONAL' bucket.
  return l === 'COMPLETED' ? 'OPERATIONAL' : l;
}

function apiAssetStatus(a: AssetDevice, shadows: Shadow[]): DeviceStatus {
  // No runtime binding / no telemetry -> PLANNED / NO_RUNTIME, never RUNNING green.
  if (!a.runtimeDeviceId) return 'NOT_COMMISSIONED';
  const shadow = shadows.find((s) => s.deviceId === a.runtimeDeviceId);
  if (!shadow) return 'OFFLINE';
  return shadow.presence.connectivity === 'MQTT_CONNECTED' ? 'RUNNING' : 'OFFLINE';
}

function createDemoProvider(): SceneDataProvider {
  return {
    mode: 'demo',
    projects: shallowRef(demoAllProjects),
    devices: shallowRef(demoAllDevices),
    events: shallowRef(demoEvents as SceneEvent[]),
    realProjects: computed(() => demoRealProjects),
    loading: shallowRef(false),
    error: shallowRef(''),
    async refresh() {},
  };
}

function createApiProvider(): SceneDataProvider {
  const projects = shallowRef<Project[]>([]);
  const devices = shallowRef<Device[]>([]);
  const events = shallowRef<SceneEvent[]>([]);
  const loading = shallowRef(true);
  const error = shallowRef('');

  async function refresh() {
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
      const mappedDevices: Device[] = rawAssets.flatMap((a) => {
        const base = byProject.get(a.projectId);
        if (!base) return [];
        const status = apiAssetStatus(a, rawShadows);
        return [
          {
            ...base,
            telemetrySource: 'demo' as const,
            commissioningStatus: 'NOT_COMMISSIONED' as const,
            grab: '闭合' as const,
            mode: '自动' as const,
            fault: '无活动故障',
            deviceId: a.assetDeviceId,
            deviceName: a.displayName,
            status,
            todayHours: 0,
            totalHours: 0,
            load: 0,
            x: 0,
            y: 0,
            z: 0,
            heartbeat: '',
            craneModel: a.craneModel ?? base.craneModel,
          },
        ];
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
    } catch (e) {
      error.value = (e as Error).message;
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
    refresh,
  };
}

export const sceneProvider: SceneDataProvider =
  import.meta.env.VITE_DATA_MODE === 'api' ? createApiProvider() : createDemoProvider();
