<script setup lang="ts">
// Lightweight 2D spatial view: SVG of the same authorized DataV.GeoAtlas boundary
// files the 3D scene uses, projected with the same Mercator (and the country
// South China Sea inset rule). It shares the workspace store with the 3D view:
// clicking a region drills (store.navigateRegion), project/device lists open the
// Inspector — no separate business state. It never loads Three.js, and it is the
// fallback when WebGL is unavailable.
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { geoMercator } from 'd3-geo';
import type { Feature, FeatureCollection } from 'geojson';
import { ChevronRight, ArrowLeft } from 'lucide-vue-next';
import { useOperations } from '../../stores/operations';
import { sceneProvider } from '../../scene/provider';
import { loadGeoManifest, childEntries, type GeoManifest } from '../../scene/geo-manifest';
import { countryLevel, type MapLevel } from '../../three/map/DrilldownController';
import { statusText, statusColor } from '../../data/types';

const store = useOperations();
const { visibleDevices, regionTarget, regionStack, selectedId, selectedProjectId } = storeToRefs(store);
const W = 1000;
const H = 640;
const manifest = shallowRef<GeoManifest | null>(null);
const features = shallowRef<Feature[]>([]);
const loading = ref(true);
const error = ref('');
let seq = 0;
let alive = true;
const files = new Map<string, Promise<FeatureCollection>>();
function file(path: string) {
  let p = files.get(path);
  if (!p) {
    p = fetch(`${import.meta.env.BASE_URL}geo/${path}`).then((r) => {
      if (!r.ok) throw new Error('本地地图资源缺失：' + path);
      return r.json() as Promise<FeatureCollection>;
    });
    p.catch(() => files.delete(path));
    files.set(path, p);
  }
  return p;
}
const leaf = computed(() => regionTarget.value[regionTarget.value.length - 1] ?? 100000);

async function load() {
  const my = ++seq;
  loading.value = true;
  error.value = '';
  try {
    const m = manifest.value ?? (manifest.value = await loadGeoManifest());
    const target = [...regionTarget.value];
    const code = target[target.length - 1] ?? 100000;
    const entry = m[code];
    if (!entry) throw new Error('行政区数据不存在');
    let fc: FeatureCollection;
    if (entry.childrenFile) fc = await file(entry.childrenFile);
    else {
      const all = await file(entry.boundaryFile!);
      fc = { type: 'FeatureCollection', features: all.features.filter((f) => Number(f.properties?.adcode) === code) };
    }
    if (my !== seq || !alive) return; // superseded by a newer region
    features.value = fc.features.filter((f) => m[Number(f.properties?.adcode)]);
    const stack: MapLevel[] = [countryLevel, ...target.map((c) => ({ adcode: c, name: m[c]!.name, level: m[c]!.level as MapLevel['level'] }))];
    store.regionStack = stack;
    store.regionChildren = childEntries(m, code).map((r) => ({ adcode: r.adcode, name: r.name, level: r.level as MapLevel['level'] }));
    store.regionPath = target;
    store.sceneState = target.length ? 'REGION_IDLE' : 'COUNTRY_IDLE';
  } catch (e) {
    if (my === seq) error.value = String((e as Error).message ?? e);
  } finally {
    if (my === seq) loading.value = false;
  }
}
watch(() => regionTarget.value.join(','), () => void load(), { immediate: true });
// Focus requests (Inspector lists, ?project= / ?asset= deep links). 2D has no
// camera, so a request only selects — the same store action the 3D stage uses
// after flying, so both views end in the same selection state.
watch(
  () => store.focusRequest?.seq,
  () => {
    const req = store.focusRequest;
    if (!req) return;
    store.focusRequest = null;
    if (req.kind === 'device') {
      if (sceneProvider.devices.value.some((d) => d.deviceId === req.id)) store.select(req.id);
    } else if (sceneProvider.projects.value.some((p) => p.id === req.id)) store.selectProject(req.id);
  },
  { immediate: true }
);
// Esc mirrors the 3D stage: close the selection (and the narrow-width drawer)
// first, otherwise go up one region level.
function handleKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return;
  if (selectedId.value || selectedProjectId.value) store.select(null);
  else if (regionTarget.value.length) store.navigateRegion(regionTarget.value.slice(0, -1));
}
onMounted(() => window.addEventListener('keydown', handleKey));
onUnmounted(() => {
  alive = false;
  seq++;
  window.removeEventListener('keydown', handleKey);
});

const country = computed(() => leaf.value === 100000);
/**
 * [lng,lat] → SVG point. Planar fit: bounds are computed from the projected ring
 * coordinates themselves, because d3's spherical fitExtent treats rings of the
 * opposite winding as "the whole globe minus the polygon" and would shrink the
 * map to a dot. The country view applies the same South China Sea inset rule as
 * GeoProjection.projectCountry.
 */
const project = computed<(lng: number, lat: number) => [number, number]>(() => {
  const unit = geoMercator().scale(1).translate([0, 0]);
  const inset = country.value;
  const raw = (lng: number, lat: number) =>
    (unit(inset && lat < 17.6 && lng > 105 ? [124 + (lng - 115) * 0.16, 20 + (lat - 10) * 0.3] : [lng, lat]) ?? [0, 0]) as [number, number];
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const f of features.value) {
    const g = f.geometry;
    if (!g || (g.type !== 'Polygon' && g.type !== 'MultiPolygon')) continue;
    for (const poly of g.type === 'Polygon' ? [g.coordinates] : g.coordinates)
      for (const [x, y] of poly[0] ?? []) {
        const [px, py] = raw(x!, y!);
        if (px < minX) minX = px;
        if (px > maxX) maxX = px;
        if (py < minY) minY = py;
        if (py > maxY) maxY = py;
      }
  }
  const pad = 20;
  const w = maxX - minX, h = maxY - minY;
  const k = w > 0 && h > 0 ? Math.min((W - 2 * pad) / w, (H - 2 * pad) / h) : 1;
  const ox = (W - w * k) / 2 - minX * k, oy = (H - h * k) / 2 - minY * k;
  return (lng, lat) => {
    const [px, py] = raw(lng, lat);
    return [px * k + ox, py * k + oy];
  };
});
function pathOf(f: Feature): string {
  const g = f.geometry;
  if (!g || (g.type !== 'Polygon' && g.type !== 'MultiPolygon')) return '';
  const pt = project.value;
  const polys = g.type === 'Polygon' ? [g.coordinates] : g.coordinates;
  let d = '';
  for (const poly of polys)
    for (const ring of poly) d += 'M' + ring.map(([x, y]) => pt(x!, y!).map((v) => v.toFixed(1)).join(',')).join('L') + 'Z';
  return d;
}

const devicesByRegion = computed(() => {
  const depth = regionTarget.value.length;
  const counts = new Map<number, { total: number; alarm: number }>();
  for (const d of visibleDevices.value) {
    const code = d.adminPath[depth];
    if (code === undefined || (depth && d.adminPath[depth - 1] !== leaf.value)) continue;
    const c = counts.get(code) ?? { total: 0, alarm: 0 };
    c.total++;
    if (d.status === 'ALARM') c.alarm++;
    counts.set(code, c);
  }
  return counts;
});
const regions = computed(() =>
  features.value.map((f) => {
    const code = Number(f.properties?.adcode);
    const entry = manifest.value?.[code];
    const c = devicesByRegion.value.get(code);
    const center = entry?.center ? project.value(entry.center[0], entry.center[1]) : null;
    return { code, name: entry?.name ?? '', d: pathOf(f), count: c?.total ?? 0, alarm: c?.alarm ?? 0, center };
  })
);
function drill(code: number) {
  if (!manifest.value?.[code] || code === leaf.value) return;
  if (manifest.value[code]!.parent !== leaf.value) return;
  store.navigateRegion([...regionTarget.value, code]);
}
function crumb(i: number) {
  store.navigateRegion(regionStack.value.slice(1, i + 1).map((l) => l.adcode));
}
</script>
<template>
  <div class="stage2d" data-testid="spatial-2d" :data-region="leaf">
    <div class="map-topline stage2d-topline">
      <nav class="breadcrumb" aria-label="行政区路径">
        <button v-for="(s, i) in regionStack" :key="s.adcode" @click="crumb(i)">
          {{ s.name }}<ChevronRight v-if="i < regionStack.length - 1" :size="12" />
        </button>
      </nav>
      <button v-if="regionStack.length > 1" class="back-button" @click="store.navigateRegion([])"><ArrowLeft :size="12" />返回全国</button>
    </div>
    <p v-if="loading" class="stage2d-state" aria-busy="true">正在加载行政区边界…</p>
    <p v-else-if="error" class="stage2d-state" role="alert">
      {{ error }} <button class="ghost-action" @click="load()">重试</button>
    </p>
    <svg v-else :viewBox="`0 0 ${W} ${H}`" class="stage2d-map" role="group" aria-label="二维行政区地图">
      <g>
        <path
          v-for="r in regions"
          :key="r.code"
          :d="r.d"
          :class="{ has: r.count > 0, alarm: r.alarm > 0, drillable: r.code !== leaf }"
          :data-region-code="r.code"
          @click="drill(r.code)"
        >
          <title>{{ r.name }} · {{ r.count }} 台</title>
        </path>
      </g>
      <g class="stage2d-labels" aria-hidden="true">
        <template v-for="r in regions" :key="'l' + r.code">
          <text v-if="r.center && (r.count > 0 || !country)" :x="r.center[0]" :y="r.center[1]">{{ r.name.replace(/(省|市|自治区|壮族|回族|维吾尔|特别行政区)$/g, '') }}{{ r.count ? ' · ' + r.count : '' }}</text>
        </template>
      </g>
    </svg>
    <p class="stage2d-legend" aria-hidden="true"><i :style="{ background: statusColor.ALARM }"></i>{{ statusText.ALARM }} · 色块深浅 = 设备数 · 点击区域下钻</p>
  </div>
</template>
