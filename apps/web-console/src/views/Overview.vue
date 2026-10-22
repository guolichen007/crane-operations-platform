<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { storeToRefs } from 'pinia';
import {
  ChevronRight,
  ArrowUpRight,
  ArrowLeft,
  Play,
  Pause,
  Square,
  RotateCcw,
  LocateFixed,
  SlidersHorizontal,
  Info,
} from 'lucide-vue-next';
import { useOperations } from '../stores/operations';
import { realProjects, devices, aggregate, events } from '../data';
import { statusText, statusColor, type DeviceStatus, type Project } from '../data/types';
import { MapSceneManager } from '../three/map/MapSceneManager';
import { countryLevel, type MapLevel } from '../three/map/DrilldownController';
import type { RegionEntry } from '../three/map/GeoJsonLoader';
import { DemoTourController } from '../demo/DemoTourController';
import DevicePanel from '../components/DevicePanel.vue';
import ProjectPanel from '../components/ProjectPanel.vue';
import CloudRuntime from '../components/CloudRuntime.vue';
import { cloudMode } from '../cloud/api';
const isDev = import.meta.env.DEV;
const observation = ref<'operations' | 'cinematic'>('operations');
async function switchObservation(mode: 'operations' | 'cinematic') {
  stop();
  await scene?.setObservation(mode);
  observation.value = scene?.rig.observation ?? mode;
}
const countryView = ref<'overview' | 'assets'>('overview');
const cameraPreset = ref('COUNTRY_OVERVIEW');
async function switchCountryView(mode: 'overview' | 'assets') {
  stop();
  countryView.value = mode;
  await scene?.setCountryView(mode);
}
const store = useOperations();
const { source, status, selected, selectedProject, visibleProjects, visibleDevices } =
  storeToRefs(store);
const host = ref<HTMLElement>();
const stack = ref<MapLevel[]>([countryLevel]),
  children = ref<RegionEntry[]>([]),
  sceneState = ref('COUNTRY_IDLE'),
  error = ref(''),
  loading = ref(true),
  debug = ref(false),
  stats = ref<Record<string, string | number>>({});
const touring = ref(false),
  paused = ref(false),
  tourTitle = ref(''),
  progress = ref(0),
  regionSearch = ref('');
let scene: MapSceneManager | undefined, tour: DemoTourController | undefined;
const totals = computed(() => aggregate(visibleProjects.value));
const online = computed(
  () => visibleDevices.value.filter((d) => d.dataSource === 'demo' && d.status !== 'OFFLINE').length
);
const demoCount = devices.filter((d) => d.dataSource === 'demo').length;
const alarmCount = devices.filter((d) => d.status === 'ALARM').length;
const statuses: DeviceStatus[] = ['RUNNING', 'STANDBY', 'ALARM', 'OFFLINE', 'MAINTENANCE'];
const counts = computed(() =>
  statuses.map((s) => ({
    status: s,
    count: visibleDevices.value.filter((d) => d.status === s).length,
  }))
);
const navigation = computed(() =>
  children.value
    .filter((c) => c.name.includes(regionSearch.value))
    .sort((a, b) => {
      const realCode = (n: number) => realProjects.some((p) => p.adminPath.includes(n));
      return Number(realCode(b.adcode)) - Number(realCode(a.adcode));
    })
);
function stop() {
  tour?.exit();
  scene?.setTour(false);
}
async function focus(p: Project) {
  stop();
  await scene?.focusProject(p);
}
function select(id: string) {
  stop();
  store.select(id);
  const d = devices.find((d) => d.deviceId === id);
  if (d) void scene?.focusDevice(d);
}
function openProject(id: string) {
  stop();
  store.selectProject(id);
  const p = realProjects.find((p) => p.id === id) ?? visibleProjects.value.find((p) => p.id === id);
  if (p) void scene?.focusProject(p);
}

async function enter(code: number) {
  stop();
  regionSearch.value = '';
  await scene?.enter(code);
}
async function back(index = 0) {
  stop();
  store.select(null);
  regionSearch.value = '';
  await scene?.back(index);
}
function play() {
  if (!tour) return;
  tour.play();
  if (tour?.running) scene?.setTour(true);
}
function pause() {
  tour?.pause();
  scene?.setTour(false);
}
function handleKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    stop();
    if (/^(PROJECT|DEVICE)_/.test(cameraPreset.value)) void back(0);
    else if (stack.value.length > 1) void back(stack.value.length - 2);
    else store.select(null);
  }
  if (isDev && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'd') {
    e.preventDefault();
    debug.value = !debug.value;
  }
}
function userStop(e: PointerEvent) {
  if (touring.value && !(e.target as HTMLElement).closest('.tour-controls')) stop();
}
watch([selected, selectedProject], () =>
  scene?.setSelection(selected.value?.deviceId ?? selectedProject.value?.id ?? null)
);
watch([visibleProjects, visibleDevices], () =>
  scene?.setFilter(visibleProjects.value, visibleDevices.value)
);
onMounted(async () => {
  window.addEventListener('keydown', handleKey);
  window.addEventListener('start-tour', play);
  document.addEventListener('pointerdown', userStop, true);
  try {
    scene = new MapSceneManager(host.value!, {
      change: (s, c, state) => {
        stack.value = s;
        children.value = c;
        sceneState.value = state;
        cameraPreset.value = scene?.rig.preset ?? 'COUNTRY_OVERVIEW';
        observation.value = scene?.rig.observation ?? 'operations';
        countryView.value = scene?.rig.countryMode ?? 'overview';
      },
      select: (id) => store.select(id),
      project: (id) => store.selectProject(id),
      error: (m) => (error.value = m),
      stats: (s) => (stats.value = s),
    });
    if (import.meta.env.DEV)
      (window as unknown as { __spatialDiagnostics: () => unknown }).__spatialDiagnostics = () =>
        scene?.diagnostics();
    await scene.init();
    if (!document.contains(host.value!)) return;
    scene.setFilter(visibleProjects.value, visibleDevices.value);
  } catch (e) {
    error.value = `WebGL 初始化失败：${String(e)}`;
  } finally {
    loading.value = false;
  }
  tour = new DemoTourController(
    [
      {
        at: 0,
        title: '全国设备网络 · 资产全景',
        action: async () => {
          source.value = 'all';
          status.value = 'all';
          store.select(null);
          await scene?.back(0);
          if (tour?.running) scene?.setTour(true);
        },
      },
      {
        at: 4,
        title: '6 个匿名登记项目 · 10 台计划设备',
        action: () => {
          source.value = 'real';
        },
      },
      {
        at: 8,
        title: '浙江省 · 近期实施双项目',
        action: async () => {
          await scene?.enter(330000);
          if (tour?.running) scene?.setTour(true);
        },
      },
      {
        at: 14,
        title: '嘉兴 → 海宁 · 逐级查看',
        action: async () => {
          await scene?.enter(330400);
          if (tour?.running) await scene?.enter(330481);
          if (tour?.running) scene?.setTour(true);
        },
      },
      {
        at: 20,
        title: 'Project-A · 2026 年 11 月实施',
        action: () => {
          store.selectProject(realProjects[0]!.id);
        },
      },
      {
        at: 27,
        title: '回到全国 · 项目实施态势',
        action: async () => {
          store.select(null);
          await scene?.back(0);
          if (tour?.running) scene?.setTour(true);
        },
      },
      {
        at: 32,
        title: 'Project-F · 3 台行车与两阶段里程碑',
        action: async () => {
          await scene?.focusProject(realProjects[5]!, () => !!tour?.running);
          if (tour?.running) scene?.setTour(true);
        },
      },
      {
        at: 38,
        title: '全国设备网络 · 持续连接',
        action: async () => {
          source.value = 'all';
          store.select(null);
          await scene?.back(0);
          if (tour?.running) scene?.setTour(true);
        },
      },
    ],
    (r, p, title, value) => {
      touring.value = r;
      paused.value = p;
      tourTitle.value = title;
      progress.value = value;
    }
  );
});
onUnmounted(() => {
  window.removeEventListener('keydown', handleKey);
  window.removeEventListener('start-tour', play);
  document.removeEventListener('pointerdown', userStop, true);
  tour?.dispose();
  scene?.dispose();
});
</script>
<template>
  <main
    class="overview"
    :data-scene-level="stack[stack.length - 1]?.level"
    :data-adcode="stack[stack.length - 1]?.adcode"
    :data-scene-state="sceneState"
    :data-camera-preset="cameraPreset"
    :data-source-filter="source"
    :data-tour="touring ? (paused ? 'paused' : 'playing') : 'stopped'"
  >
    <div class="overview-intro">
      <div>
        <div class="eyebrow"><span class="live-dot"></span>NATIONAL OPERATIONS / 全国</div>
        <h1>全国设备运营态势<span>空间资产 · 协同运营</span></h1>
      </div>
      <div class="view-description">本地模拟数据 <span>/</span> 匿名登记项目按实施计划展示</div>
    </div>
    <CloudRuntime v-if="cloudMode" />
    <div class="kpi-strip" aria-label="匿名登记与演示空间资产">
      <div>
        <span>匿名登记项目</span><strong>6<small>项</small></strong
        ><em>覆盖 5 个省级区域</em>
      </div>
      <div>
        <span>登记行车</span><strong>10<small>台</small></strong
        ><em class="gold">全部处于实施计划阶段</em>
      </div>
      <div>
        <span>演示设备</span><strong>{{ demoCount }}<small>台</small></strong
        ><em>22 个演示项目</em>
      </div>
      <div>
        <span>演示资产在线 <b class="tiny-badge">DEMO</b></span
        ><strong
          >{{ devices.filter((d) => d.dataSource === 'demo' && d.status !== 'OFFLINE').length
          }}<small>台</small></strong
        ><em class="green">连接率 95.5%</em>
      </div>
      <div>
        <span>模拟告警 <b class="tiny-badge">DEMO</b></span
        ><strong class="coral">{{ alarmCount }}<small>项</small></strong
        ><em>固定模拟告警</em>
      </div>
      <div>
        <span>未来 120 天实施</span><strong>5<small>项</small></strong
        ><em>另 1 项于 2027 年 2 月</em>
      </div>
    </div>
    <div class="sandbox-stage">
      <div
        class="map-canvas"
        ref="host"
        role="img"
        aria-label="全国行车三维地图，支持鼠标拖动、缩放和点击行政区"
      ></div>
      <div v-if="loading" class="scene-loading"><span></span>正在构建空间沙盘</div>
      <div v-if="error" class="scene-error">
        {{ error }}<button @click="error = ''">关闭</button>
      </div>
      <aside class="left-context">
        <section>
          <div class="panel-title">
            <h3>全国资产概况</h3>
            <span>ASSETS</span>
          </div>
          <div class="asset-grid">
            <div>
              <b>{{ totals.provinces }}</b
              ><span>覆盖省份</span>
            </div>
            <div>
              <b>{{ totals.projects }}</b
              ><span>项目数量</span>
            </div>
            <div>
              <b>{{ totals.cranes }}</b
              ><span>行车数量</span>
            </div>
            <div>
              <b
                >{{
                  visibleDevices.filter((d) => d.dataSource === 'demo').length
                    ? (
                        (online / visibleDevices.filter((d) => d.dataSource === 'demo').length) *
                        100
                      ).toFixed(1)
                    : '—'
                }}<small>%</small></b
              ><span>演示设备在线率</span>
            </div>
          </div>
        </section>
        <section>
          <div class="panel-title">
            <h3>模拟运行态</h3>
            <span>{{ totals.cranes }} UNITS</span>
          </div>
          <div class="stacked-bar">
            <i
              v-for="c in counts"
              :key="c.status"
              :style="{
                width: (c.count / totals.cranes) * 100 + '%',
                background: statusColor[c.status],
              }"
              :title="statusText[c.status] + ' ' + c.count"
            ></i>
          </div>
          <div class="status-legend">
            <div v-for="c in counts" :key="c.status">
              <i :style="{ background: statusColor[c.status] }"></i
              ><span>{{ statusText[c.status] }}</span
              ><b>{{ c.count }}</b>
            </div>
          </div>
          <p class="simulation-note">DEMO TELEMETRY · 真实档案 10 台尚未投运</p>
        </section>
        <section>
          <div class="panel-title">
            <h3>数据筛选</h3>
            <SlidersHorizontal :size="13" />
          </div>
          <div class="segmented source-filter">
            <button
              v-for="s in [
                { id: 'all', name: '全部展示' },
                { id: 'real', name: '匿名登记项目' },
                { id: 'demo', name: '演示设备' },
              ] as const"
              :key="s.id"
              :class="{ selected: source === s.id }"
              @click="
                stop();
                source = s.id;
              "
            >
              {{ s.name }}
            </button>
          </div>
          <div class="status-filters">
            <button
              v-for="s in [
                { id: 'all', name: '全部状态' },
                { id: 'RUNNING', name: '运行' },
                { id: 'planned', name: '计划' },
                { id: 'ALARM', name: '告警' },
                { id: 'OFFLINE', name: '离线' },
              ] as const"
              :key="s.id"
              :class="{ selected: status === s.id }"
              @click="
                stop();
                status = s.id;
              "
            >
              {{ s.name }}
            </button>
          </div>
        </section>
        <section class="region-navigation">
          <div class="panel-title">
            <h3>{{ stack.length === 1 ? '行政区导航' : stack[stack.length - 1]?.name }}</h3>
            <span>点击钻取</span>
          </div>
          <input
            v-if="children.length > 8"
            v-model="regionSearch"
            class="region-search"
            placeholder="搜索行政区域"
            aria-label="搜索行政区域"
          />
          <div class="region-chips">
            <button
              v-for="r in navigation"
              :key="r.adcode"
              :data-region-code="r.adcode"
              @click="enter(r.adcode)"
            >
              {{
                r.name
                  .replace('壮族自治区', '')
                  .replace('维吾尔自治区', '')
                  .replace('回族自治区', '')
                  .replace('藏族羌族自治州', '州')
                  .replace('哈尼族彝族自治州', '州')
              }}<ChevronRight :size="11" />
</button
            ><span v-if="!children.length" class="muted small"
              >已到达项目所在区域，点击行车查看详情。</span
            >
          </div>
        </section>
      </aside>
      <div class="map-topline">
        <div class="breadcrumb">
          <button v-for="(s, i) in stack" :key="s.adcode" @click="back(i)">
            {{ s.name }}<ChevronRight v-if="i < stack.length - 1" :size="12" />
          </button>
          <span v-if="selectedProject || selected" class="context-crumb"
            >› {{ selectedProject?.projectName ?? selected?.projectName }}</span
          >
        </div>
        <button
          v-if="stack.length > 1 || sceneState === 'PROJECT_FOCUS'"
          class="back-button"
          @click="back(0)"
        >
          <ArrowLeft :size="12" />返回全国
        </button>
      </div>
      <div v-if="cameraPreset.startsWith('COUNTRY_')" class="map-view-switch">
        <button
          :class="{ selected: countryView === 'overview' }"
          @click="switchCountryView('overview')"
        >
          全国态势
</button
        ><button
          :class="{ selected: countryView === 'assets' }"
          @click="switchCountryView('assets')"
        >
          设备聚焦
        </button>
      </div>
      <div class="observation-switch" aria-label="观察角度">
        <button
          :class="{ selected: observation === 'operations' }"
          @click="switchObservation('operations')"
        >
          运营视角
        </button>
        <button
          :class="{ selected: observation === 'cinematic' }"
          @click="switchObservation('cinematic')"
        >
          立体视角
        </button>
      </div>
      <div v-if="cameraPreset.startsWith('COUNTRY_')" class="south-sea-inset-note">
        南海诸岛 · 附图
      </div>
      <div class="map-caption">
        <span>CN / {{ stack[stack.length - 1]?.adcode }}</span>
        <p>
          {{
            stack.length === 1 ? '全国设备空间分布' : stack[stack.length - 1]?.name + ' · 项目分布'
          }}
        </p>
        <small>NATIONAL ASSET MAP</small
        ><button
          class="map-help"
          aria-label="地图操作帮助"
          title="拖动旋转 · 滚轮缩放 · 点击区域钻取"
        >
          ?
        </button>
      </div>
      <div class="map-tools">
        <button title="重置全国视角" aria-label="重置全国视角" @click="back(0)">
          <RotateCcw :size="15" />
</button
        ><button title="聚焦匿名登记项目" aria-label="聚焦Project-A" @click="focus(realProjects[0]!)">
          <LocateFixed :size="16" />
        </button>
      </div>
      <div class="map-legend">
        <span><i class="green-dot"></i>省级设备数量</span
        ><span><i class="green-dot"></i>演示设备网络</span
        >
      </div>
      <aside class="right-context" :class="{ 'overview-summary': !selectedProject && !selected }">
        <ProjectPanel
          v-if="selectedProject"
          :project="selectedProject"
          @close="store.select(null)"
          @select="select"
          @focus="focus(selectedProject)"
        />

        <DevicePanel
          v-else-if="selected"
          :device="selected"
          @close="store.select(null)"
          @select="select"
          @project="openProject"
        /><template v-else>
          <section class="project-plan">
            <div class="panel-title">
              <h3>近期实施计划</h3>
              <span class="gold">6 项目</span>
            </div>
            <p class="muted small">2026.11 — 2027.02 <span class="plan-caption">匿名登记项目</span></p>
            <button
              v-for="(p, i) in realProjects"
              :key="p.id"
              class="plan-item"
              :data-project-id="p.id"
              @click="focus(p)"
            >
              <div class="plan-track">
                <span>{{ String(i + 1).padStart(2, '0') }}</span
                ><i></i>
              </div>
              <div class="plan-content">
                <div>
                  <strong>{{ p.projectName }}</strong
                  ><ArrowUpRight :size="12" />
                </div>
                <p>{{ p.locationLabel }} · {{ p.quantity }} 台</p>
              </div>
            </button>
          </section>
          <div class="right-note">
            <Info :size="13" /><span>匿名登记项目尚未投运<br />金色连接表示计划接入</span>
          </div>
        </template>
      </aside>
      <div v-if="touring" class="tour-controls">
        <div>
          <Play :size="14" /><span>{{ tourTitle }}</span
          ><b>{{ Math.floor(progress * 45) }} / 45s</b>
        </div>
        <div class="tour-progress"><i :style="{ width: progress * 100 + '%' }"></i></div>
        <button v-if="!paused" aria-label="暂停巡演" @click="pause"><Pause :size="14" />暂停</button
        ><button v-else @click="play"><Play :size="14" />继续</button
        ><button aria-label="退出巡演" @click="stop"><Square :size="12" />退出巡演</button>
      </div>
      <div class="map-attribution">
        开发边界：DataV.GeoAtlas · 仅限内部原型，未经审图<br />公开展示前须替换合规地图并标注来源 /
        审图号
      </div>
      <div v-if="isDev && debug" class="debug-panel" data-testid="debug-panel">
        <b>RENDER DIAGNOSTICS</b>
        <div v-for="(value, key) in stats" :key="key">
          <span>{{ key }}</span
          ><strong>{{ value }}</strong>
        </div>
      </div>
    </div>
    <div class="event-strip">
      <div class="event-label">
        <span class="live-dot"></span><strong>全国事件流</strong><small>DEMO / 模拟事件</small>
      </div>
      <div v-for="event in events" :key="event.time" class="event-item">
        <time>{{ event.time }}</time
        ><span :style="{ color: event.color }">{{ event.kind }}</span>
        <p>{{ event.text }}</p>
      </div>
    </div>
  </main>
</template>
