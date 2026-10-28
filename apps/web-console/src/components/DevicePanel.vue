<script setup lang="ts">
import { computed } from 'vue';
import { X, ArrowUpRight, MapPin, ArrowLeft } from 'lucide-vue-next';
import { devices } from '../data';
import { statusText, statusColor, lifecycleText, type Device } from '../data/types';
import CraneTwin from './CraneTwin.vue';
const props = defineProps<{ device: Device }>();
const emit = defineEmits<{ close: []; select: [id: string]; project: [id: string] }>();
const siblings = computed(() => devices.filter((d) => d.id === props.device.id));
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
      <span :class="['source-badge', device.dataSource]">{{
        device.dataSource === 'real' ? 'REAL ASSET · DEMO TELEMETRY' : 'DEMO ASSET · DEMO TELEMETRY'
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
      <strong>模拟运行态</strong
      ><b :style="{ color: statusColor[device.status] }"
        ><i :style="{ background: statusColor[device.status] }"></i
        >{{ statusText[device.status] }}</b
      >
      <p>运行参数仅为演示，非现场实时采集。</p>
    </div>
    <div class="telemetry-grid">
      <CraneTwin :key="device.deviceId" :axes="{ x: device.x, y: device.y, z: device.z, grab: device.grab === '打开' ? 1 : 0 }" :demo="true" :alarm="device.status === 'ALARM'" />
      <div>
        <span>负载率</span><b>{{ device.load }}<small> %</small></b>
      </div>
      <div>
        <span>今日运行</span><b>{{ device.todayHours.toFixed(1) }}<small> h</small></b>
      </div>
      <div>
        <span>大车 X</span><b>{{ device.x.toFixed(2) }}<small> m</small></b>
      </div>
      <div>
        <span>小车 Y</span><b>{{ device.y.toFixed(2) }}<small> m</small></b>
      </div>
      <div>
        <span>起升 Z</span><b>{{ device.z.toFixed(2) }}<small> m</small></b>
      </div>
      <div>
        <span>抓斗</span><b>{{ device.grab }}</b>
      </div>
      <div>
        <span>控制模式</span><b>{{ device.mode }}</b>
      </div>
      <div>
        <span>累计模拟运行</span><b>{{ device.totalHours }}<small> h</small></b>
      </div>
    </div>
    <div class="device-heartbeat">
      <span>最后心跳 · DEMO</span><time>{{ device.heartbeat }}</time>
      <p>{{ device.fault }}</p>
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
      <span v-if="device.dataSource === 'real'" class="archive-warning">尚未投运 · 计划接入</span>
      <p>{{ device.scheduleText }}</p>
      <div
        v-for="m in device.milestones ?? [
          { date: device.plannedImplementation, name: '项目计划实施' },
        ]"
        :key="m.name"
      >
        <time>{{ m.date }}</time
        ><strong>{{ m.name }}</strong>
      </div>
    </section>
    <code class="device-code">{{ device.deviceId }}</code>
    <p class="coordinate-note">
      {{
        device.coordinateAccuracy === 'county_center_demo' ? '县中心' : '城市中心'
      }}演示坐标，非设备精确位置。
    </p>
    <RouterLink :to="'/monitor?device=' + device.deviceId" class="primary-button detail-link">
      查看模拟监控<ArrowUpRight :size="14" />
    </RouterLink>
  </section>
</template>
