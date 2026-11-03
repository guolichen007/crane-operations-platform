<script setup lang="ts">
import { computed } from 'vue';
import { X, ArrowUpRight, MapPin, ArrowLeft } from 'lucide-vue-next';
import { sceneProvider } from '../scene/provider';
import { statusText, statusColor, lifecycleText, type Device } from '../data/types';
import CraneTwin from './CraneTwin.vue';
const props = defineProps<{ device: Device }>();
const emit = defineEmits<{ close: []; select: [id: string]; project: [id: string] }>();
const siblings = computed(() => sceneProvider.devices.value.filter((d) => d.id === props.device.id));

const sourceLabel = computed(() => {
  switch (props.device.telemetrySource) {
    case 'cloud':
      return 'REAL ASSET · CLOUD TELEMETRY';
    case 'none':
      return 'REAL ASSET · NO TELEMETRY';
    default:
      return props.device.dataSource === 'real'
        ? 'REAL ASSET · DEMO TELEMETRY'
        : 'DEMO ASSET · DEMO TELEMETRY';
  }
});
const disclosureLabel = computed(() => {
  switch (props.device.telemetrySource) {
    case 'cloud':
      return '云端运行态';
    case 'none':
      return '未接入运行数据';
    default:
      return '模拟运行态';
  }
});
const disclosureCopy = computed(() => {
  switch (props.device.telemetrySource) {
    case 'cloud':
      return '运行参数来自 Cloud 实时遥测，非演示数据。';
    case 'none':
      return '该资产尚未接入运行数据，无实时遥测。';
    default:
      return '运行参数仅为演示，非现场实时采集。';
  }
});
const twinDemo = computed(() => props.device.telemetrySource === 'demo');
const fmt = (v: number | null | undefined, digits = 2) =>
  v === null || v === undefined || Number.isNaN(v) ? '—' : v.toFixed(digits);
const grabText = computed(() => props.device.grab ?? '—');
const modeText = computed(() => props.device.mode ?? '—');
const faultText = computed(() => props.device.fault || '暂无数据');
</script>
<template>
  <section
    class="device-panel telemetry-panel"
    data-testid="device-panel"
    :data-device-id="device.deviceId"
  >
    <div class="panel-title">
      <span>单台设备 / DEVICE CONTEXT</span
      ><button class="icon-button" aria-label="关闭详情" @click="emit('close')">
        <X :size="16" />
      </button>
    </div>
    <button class="back-project" @click="emit('project', device.id)">
      <ArrowLeft :size="11" />返回项目总览
    </button>
    <div class="detail-heading">
      <span :class="['source-badge', device.telemetrySource]" data-testid="device-source-badge">{{
        sourceLabel
      }}</span>
      <h2>{{ device.deviceName.replace(' · ', ' / ') }}</h2>
      <p><MapPin :size="12" />{{ device.locationLabel }}</p>
    </div>
    <div class="detail-model">
      {{ device.craneModel
      }}<span
        >{{ device.grabType }} · {{ device.ratedCapacityT }} t / {{ device.spanM }} m /
        {{ device.workClass }}</span
      >
    </div>
    <div class="telemetry-disclosure">
      <strong>{{ disclosureLabel }}</strong
      ><b :style="{ color: statusColor[device.status] }"
        ><i :style="{ background: statusColor[device.status] }"></i
        >{{ statusText[device.status] }}</b
      >
      <p>{{ disclosureCopy }}</p>
    </div>
    <div class="telemetry-grid">
      <CraneTwin
        :key="device.deviceId"
        :axes="{ x: device.x, y: device.y, z: device.z, grab: device.grab === '打开' ? 1 : 0 }"
        :demo="twinDemo"
        :alarm="device.status === 'ALARM'"
      />
      <div>
        <span>负载</span><b>{{ fmt(device.load) }}</b>
      </div>
      <div>
        <span>今日运行</span><b>{{ fmt(device.todayHours, 1) }}</b>
      </div>
      <div>
        <span>大车 X</span><b>{{ fmt(device.x) }}</b>
      </div>
      <div>
        <span>小车 Y</span><b>{{ fmt(device.y) }}</b>
      </div>
      <div>
        <span>起升 Z</span><b>{{ fmt(device.z) }}</b>
      </div>
      <div>
        <span>抓斗</span><b>{{ grabText }}</b>
      </div>
      <div>
        <span>控制模式</span><b>{{ modeText }}</b>
      </div>
      <div>
        <span>累计运行</span><b>{{ fmt(device.totalHours, 1) }}</b>
      </div>
    </div>
    <div class="device-heartbeat">
      <span>最后心跳 · {{ device.telemetrySource === 'demo' ? 'DEMO' : 'CLOUD' }}</span
      ><time>{{ device.heartbeat || '—' }}</time>
      <p>{{ faultText }}</p>
    </div>
    <div class="device-sibling-switch">
      <button
        v-for="(s, i) in siblings"
        :key="s.deviceId"
        :class="{ selected: s.deviceId === device.deviceId }"
        @click="emit('select', s.deviceId)"
      >
        {{ String.fromCharCode(65 + i) }}机 · {{ statusText[s.status] }}
      </button>
    </div>
    <section class="archive-plan">
      <h3>
        {{ device.dataSource === 'real' ? '匿名登记项目档案' : '演示项目档案' }} ·
        {{ lifecycleText[device.lifecycle] }}
      </h3>
      <span v-if="device.commissioningStatus !== 'OPERATIONAL'" class="archive-warning">
        {{ device.telemetrySource === 'cloud' ? '尚未投运 · 计划接入' : '尚未投运' }}
      </span>
      <p>{{ device.scheduleText }}</p>
      <div
        v-for="m in device.milestones ?? [{ date: device.plannedImplementation, name: '项目计划实施' }]"
        :key="m.name"
      >
        <time>{{ m.date }}</time
        ><strong>{{ m.name }}</strong>
      </div>
    </section>
    <code class="device-code">{{ device.deviceId }}</code>
    <p class="coordinate-note">
      {{
        device.telemetrySource === 'demo'
          ? (device.coordinateAccuracy === 'county_center_demo' ? '县中心' : '城市中心') +
            '演示坐标，非设备精确位置。'
          : '行政区中心示意，非设备精确坐标。'
      }}
    </p>
    <RouterLink
      :to="'/monitor?device=' + device.deviceId"
      class="primary-button detail-link"
    >
      {{ device.telemetrySource === 'demo' ? '查看模拟监控' : '查看运行监控' }}<ArrowUpRight
        :size="14"
      />
    </RouterLink>
  </section>
</template>
