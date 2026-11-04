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
      return props.device.runtimeSource === 'REAL'
        ? 'REAL ASSET · CLOUD TELEMETRY'
        : props.device.runtimeSource === 'SIMULATION'
          ? 'REAL ASSET · SIMULATION TELEMETRY'
          : props.device.runtimeSource === 'TEST'
            ? 'REAL ASSET · TEST TELEMETRY'
            : 'REAL ASSET · CLOUD TELEMETRY';
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
      return props.device.runtimeSource === 'REAL' ? '云端运行态' : '模拟运行态';
    case 'none':
      return '未接入运行数据';
    default:
      return '模拟运行态';
  }
});
const disclosureCopy = computed(() => {
  switch (props.device.telemetrySource) {
    case 'cloud':
      return props.device.runtimeSource === 'REAL'
        ? '运行参数来自 Cloud 最近同步遥测，非演示数据。'
        : '运行参数来自 Cloud 的模拟/测试运行源，非现场真实采集。';
    case 'none':
      return '该资产尚未接入运行数据，无遥测。';
    default:
      return '运行参数仅为演示，非现场真实采集。';
  }
});
// Only the pure demo fixture runs the programmatic sine animation. Cloud
// SIMULATION/TEST keep their source label but render the Cloud Shadow axes
// (never overridden by a local demo animation); none → static structure pose.
const twinDemo = computed(() => props.device.telemetrySource === 'demo');
// Unknown axes (NaN) and unknown grab (null) are passed as undefined so the
// twin renders a neutral schematic pose instead of a fabricated field position.
const twinAxes = computed(() => ({
  x: Number.isFinite(props.device.x) ? props.device.x : undefined,
  y: Number.isFinite(props.device.y) ? props.device.y : undefined,
  z: Number.isFinite(props.device.z) ? props.device.z : undefined,
  grab:
    props.device.grab === '打开' ? 1 : props.device.grab === '闭合' ? 0 : undefined,
}));
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
      <p><MapPin :size="12" />{{ device.locationLabel || '无空间定位信息' }}</p>
    </div>
    <div class="detail-model">
      {{ device.craneModel || '—'
      }}<span
        >{{ device.grabType || '—' }} · {{ fmt(device.ratedCapacityT, 1) }} t /
        {{ fmt(device.spanM, 1) }} m / {{ device.workClass || '—' }}</span
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
        :axes="twinAxes"
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
      <span>最后心跳 ·
        {{
          device.telemetrySource === 'cloud'
            ? 'CLOUD'
            : device.telemetrySource === 'demo'
              ? 'DEMO'
              : '未接入'
        }}</span
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
