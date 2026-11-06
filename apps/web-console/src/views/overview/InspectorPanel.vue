<script setup lang="ts">
// Right column. With a selection it shows the existing ProjectPanel/DevicePanel
// (unchanged components). Without one it lists what the current filter/region
// contains, so the column is always about the current context.
import { computed } from 'vue';
import { storeToRefs } from 'pinia';
import { ArrowUpRight, Info } from 'lucide-vue-next';
import ProjectPanel from '../../components/ProjectPanel.vue';
import DevicePanel from '../../components/DevicePanel.vue';
import { useOperations } from '../../stores/operations';
import { statusText, statusColor } from '../../data/types';
import { hasLocation } from '../../scene/home-metrics';

const props = defineProps<{ demo: boolean; drawer: boolean }>();
const store = useOperations();
const { selected, selectedProject, scopedProjects, scopedVisibleDevices, status, scope, scopeName } = storeToRefs(store);
const filtering = computed(() => status.value !== 'all');
// Same statistics scope as the KPI strip (source ∩ displayed region, H1-R1-2).
// The header shows the full count; only the rendered rows are capped, and the cap
// is stated, so the total can never be silently truncated.
const LIST_CAP = 60;
const devicesTotal = computed(() => scopedVisibleDevices.value.length);
const devicesShown = computed(() => scopedVisibleDevices.value.slice(0, LIST_CAP));
// Unfiltered list = registered (real) projects in scope, as the original
// "近期实施计划" panel; demo fixtures appear only through filters/drill-down.
const projectsHere = computed(() =>
  scopedProjects.value.filter((p) => p.dataSource === 'real' || scope.value.regionPath.length > 0)
);
const openDrawer = computed(() => !props.drawer || !!selected.value || !!selectedProject.value);
function focusProject(id: string) {
  store.requestFocus('project', id);
}
function focusDevice(id: string) {
  store.requestFocus('device', id);
}
</script>
<template>
  <aside
    v-if="openDrawer"
    class="right-context home-inspector"
    :class="{ 'overview-summary': !selectedProject && !selected, drawer }"
    :role="drawer ? 'dialog' : undefined"
    :aria-label="drawer ? '详情' : '上下文'"
    data-testid="home-inspector"
  >
    <ProjectPanel
      v-if="selectedProject"
      :project="selectedProject"
      @close="store.select(null)"
      @select="focusDevice"
      @focus="focusProject(selectedProject.id)"
    />
    <DevicePanel
      v-else-if="selected"
      :device="selected"
      @close="store.select(null)"
      @select="focusDevice"
      @project="focusProject"
    />
    <template v-else-if="filtering">
      <section class="inspector-list">
        <div class="panel-title">
          <h3>{{ statusText[status as keyof typeof statusText] ?? (status === 'UNLOCATED' ? '无空间位置' : status === 'REAL_RUNNING' ? '真实运行' : '登记项目') }}</h3>
          <span data-testid="inspector-total" :data-total="devicesTotal">{{ devicesTotal }} 台 · {{ scopeName }}</span>
        </div>
        <p v-if="!devicesTotal" class="muted small" data-testid="inspector-empty">当前范围内没有符合该过滤条件的设备。</p>
        <p v-else-if="devicesTotal > devicesShown.length" class="muted small" data-testid="inspector-cap">
          列表显示前 {{ devicesShown.length }} 台，共 {{ devicesTotal }} 台；完整清单见设备中心。
        </p>
        <button v-for="d in devicesShown" :key="d.deviceId" class="plan-item inspector-device" :data-device-id="d.deviceId" @click="focusDevice(d.deviceId)">
          <div class="plan-content">
            <div><strong>{{ d.deviceName }}</strong><i class="status-dot" :style="{ background: statusColor[d.status] }"></i></div>
            <p>{{ d.projectName }} · {{ hasLocation(d) ? d.locationLabel || '—' : '无空间位置' }}</p>
          </div>
        </button>
      </section>
    </template>
    <template v-else>
      <section class="project-plan">
        <div class="panel-title">
          <h3>{{ demo ? '近期实施计划' : '项目' }}</h3>
          <span class="gold">{{ projectsHere.length }} 项目</span>
        </div>
        <p class="muted small"><template v-if="demo">2026.11 — 2027.02 </template><span class="plan-caption">{{ scopeName }}</span></p>
        <p v-if="!projectsHere.length" class="muted small" data-testid="inspector-empty">当前范围内没有项目。</p>
        <button v-for="(p, i) in projectsHere" :key="p.id" class="plan-item" :data-project-id="p.id" @click="focusProject(p.id)">
          <div class="plan-track"><span>{{ String(i + 1).padStart(2, '0') }}</span><i></i></div>
          <div class="plan-content">
            <div><strong>{{ p.projectName }}</strong><ArrowUpRight :size="12" /></div>
            <p>{{ p.locationLabel || '无空间位置' }} · {{ p.quantity }} 台</p>
          </div>
        </button>
      </section>
      <div class="right-note">
        <Info :size="13" /><span>{{ demo ? '匿名登记项目尚未投运；金色连接表示计划接入' : 'Cloud 最近同步运行态 · 无遥测设备显示尚未投运' }}</span>
      </div>
    </template>
  </aside>
</template>
