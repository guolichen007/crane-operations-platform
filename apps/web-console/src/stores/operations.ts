import { defineStore } from 'pinia';
import { computed, ref, shallowRef } from 'vue';
import { filterProjects } from '../data';
import { sceneProvider } from '../scene/provider';
import type { Device, DeviceStatus, SourceFilter } from '../data/types';
import type { HomeDataState, HomeScope, HomeView } from '../scene/home-metrics';
import { hasLocation, scopeProjects } from '../scene/home-metrics';
import { countryLevel, type MapLevel } from '../three/map/DrilldownController';

export type StatusFilter = DeviceStatus | 'all' | 'planned' | 'REAL_RUNNING' | 'UNLOCATED' | 'PENDING_REVIEW';
export type FocusRequest = { kind: 'project' | 'device'; id: string; seq: number };

/**
 * Asset ids the server reports as "pending review" (H2-1D, summary.metrics.real.pendingReviewAssets, ≤100).
 * The PENDING_REVIEW filter shows exactly these on the map; it is never recomputed from Shadow.
 */
const reviewIds = shallowRef<ReadonlySet<string>>(new Set());
function matchesStatus(d: Device, status: StatusFilter): boolean {
  if (status === 'all') return true;
  if (status === 'PENDING_REVIEW') return reviewIds.value.has(d.deviceId);
  if (status === 'planned') return d.dataSource === 'real';
  if (status === 'REAL_RUNNING') return d.runtimeSource === 'REAL' && d.status === 'RUNNING';
  if (status === 'UNLOCATED') return !hasLocation(d);
  return d.status === status;
}

/**
 * The homepage workspace state — the single business-state store of the
 * Operations Center: source/status filters, selection, spatial scope (drill
 * path), view mode and the presentation state the spatial views report. The 2D
 * and 3D spatial views, KPI strip, Attention list and Inspector all read and
 * write this store; the URL is synchronized from it by views/overview/useHomeUrlSync.ts.
 */
export const useOperations = defineStore('operations', () => {
  const source = ref<SourceFilter>('all'),
    status = ref<StatusFilter>('all'),
    selectedId = ref<string | null>(null),
    selectedProjectId = ref<string | null>(null);
  /** Displayed drill path below the country (province first), reported by the active spatial view. */
  const regionPath = ref<number[]>([]);
  /** Displayed drill levels (country first), for breadcrumbs. */
  const regionStack = shallowRef<MapLevel[]>([countryLevel]);
  /** Next-level regions of the displayed level, for the region navigation list. */
  const regionChildren = shallowRef<MapLevel[]>([]);
  /** Requested drill path (URL / navigation); the active spatial view converges to it. */
  const regionTarget = ref<number[]>([]);
  const sceneState = ref('COUNTRY_IDLE');
  const cameraPreset = ref('COUNTRY_OVERVIEW');
  const tour = ref<'stopped' | 'playing' | 'paused'>('stopped');
  const view = ref<HomeView>('3d');
  /** Non-empty when 3D failed to initialize; the page then stays in 2D. */
  const webglUnavailable = ref('');
  /** A user-visible notice for a corrected deep link (unknown region, missing asset…). */
  const notice = ref('');
  /** Focus intent from lists/Inspector/URL; the active spatial view executes it. */
  const focusRequest = ref<FocusRequest | null>(null);
  let focusSeq = 0;

  const projects = sceneProvider.projects;
  const devices = sceneProvider.devices;
  const selected = computed(() => devices.value.find((d) => d.deviceId === selectedId.value));
  const selectedProject = computed(() => projects.value.find((p) => p.id === selectedProjectId.value));
  const visibleProjects = computed(() =>
    filterProjects(source.value, projects.value).filter(
      (p) =>
        status.value === 'all' ||
        (status.value === 'planned'
          ? p.dataSource === 'real'
          : devices.value.some((d) => d.id === p.id && matchesStatus(d, status.value)))
    )
  );
  const visibleDevices = computed(() =>
    devices.value.filter(
      (d) => (source.value === 'all' || d.dataSource === source.value) && matchesStatus(d, status.value)
    )
  );
  // Statistics scope (H1-R1-2 contract): authorized (the API only returns
  // authorized data) ∩ source ∩ displayed region. The status filter and the
  // focused project/device are deliberately NOT part of it, so a KPI never
  // filters itself and a selection never changes the totals.
  const scope = computed<HomeScope>(() => ({ source: source.value, regionPath: regionPath.value }));
  const scopeName = computed(() => regionStack.value[regionStack.value.length - 1]?.name ?? '全国');
  const scopedProjects = computed(() => scopeProjects(projects.value, scope.value));
  const scopedDevices = computed(() => scopeProjects(devices.value, scope.value));
  /** Devices the status filter shows inside the statistics scope (Inspector list, its total). */
  const scopedVisibleDevices = computed(() => scopedDevices.value.filter((d) => matchesStatus(d, status.value)));
  /** Width (px) of the overlay drawer covering the stage's right edge; the 3D camera keeps targets clear of it. */
  const inspectorInset = ref(0);
  /** Homepage data state (R1-1), derived by Overview from the provider. */
  const dataState = ref<HomeDataState>('loading');
  function select(id: string | null) {
    selectedId.value = id;
    selectedProjectId.value = null;
  }
  function selectProject(id: string | null) {
    selectedProjectId.value = id;
    selectedId.value = null;
  }
  function requestFocus(kind: FocusRequest['kind'], id: string) {
    focusRequest.value = { kind, id, seq: ++focusSeq };
  }
  /** Ask the active view to drill to `path` (adcodes below the country). */
  function navigateRegion(path: number[]) {
    regionTarget.value = [...path];
  }
  return {
    source,
    status,
    selectedId,
    selected,
    selectedProjectId,
    selectedProject,
    visibleProjects,
    visibleDevices,
    scope,
    scopeName,
    scopedProjects,
    scopedDevices,
    scopedVisibleDevices,
    inspectorInset,
    dataState,
    regionPath,
    regionStack,
    regionChildren,
    regionTarget,
    sceneState,
    cameraPreset,
    tour,
    view,
    webglUnavailable,
    notice,
    focusRequest,
    select,
    selectProject,
    requestFocus,
    navigateRegion,
    reviewIds,
  };
});
