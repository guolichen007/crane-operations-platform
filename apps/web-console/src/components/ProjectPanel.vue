<script setup lang="ts">
import { computed } from 'vue';
import { X, ChevronRight, LocateFixed, MapPin } from 'lucide-vue-next';
import { devices } from '../data';
import { statusText, statusColor, lifecycleText, type Project } from '../data/types';
const props = defineProps<{ project: Project }>();
const emit = defineEmits<{ close: []; select: [id: string]; focus: [] }>();
const instances = computed(() => devices.filter((d) => d.id === props.project.id));
</script>
<template>
  <section class="project-context" data-testid="project-panel" :data-project-id="project.id">
    <div class="panel-title">
      <span>项目总览 / PROJECT CONTEXT</span
      ><button class="icon-button" aria-label="关闭详情" @click="emit('close')">
        <X :size="16" />
      </button>
    </div>
    <span :class="['source-badge', project.dataSource]">{{
      project.dataSource === 'real' ? 'REAL PROJECT · 匿名登记项目' : 'DEMO PROJECT · 演示项目'
    }}</span>
    <h2>
      {{ project.projectName }}<small>{{ project.quantity }} 台行车</small>
    </h2>
    <p class="project-location"><MapPin :size="12" />{{ project.locationLabel }}</p>
    <p class="archive-model">
      {{ project.craneModel }}<span>{{ project.grabType }}</span>
    </p>
    <div class="telemetry-disclosure">
      <strong>DEMO TELEMETRY</strong>
      <p>以下运行态为固定模拟，匿名登记项目档案与实施计划保持不变。</p>
    </div>
    <div class="project-device-list">
      <h3>选择单台行车</h3>
      <button
        v-for="(d, i) in instances"
        :key="d.deviceId"
        :data-device-id="d.deviceId"
        @click="emit('select', d.deviceId)"
      >
        <span class="unit-letter">{{ String.fromCharCode(65 + i) }}</span>
        <div>
          <strong>{{ String.fromCharCode(65 + i) }}机</strong><small>{{ d.craneModel }}</small>
        </div>
        <span :style="{ color: statusColor[d.status] }"
          ><i :style="{ background: statusColor[d.status] }"></i>{{ statusText[d.status] }}</span
        ><ChevronRight :size="13" />
      </button>
    </div>
    <button class="primary-button project-focus-button" @click="emit('focus')">
      <LocateFixed :size="13" />查看项目沙盘
    </button>
    <section class="archive-plan">
      <h3>
        {{ project.dataSource === 'real' ? '匿名登记项目档案' : '演示项目档案' }} ·
        {{ lifecycleText[project.lifecycle] }}
      </h3>
      <span v-if="project.dataSource === 'real'" class="archive-warning"
        >尚未投运 · 不代表现场已接入</span
      >
      <p>{{ project.scheduleText }}</p>
      <div
        v-for="m in project.milestones ?? [
          { date: project.plannedImplementation, name: '项目计划实施' },
        ]"
        :key="m.name"
      >
        <time>{{ m.date }}</time
        ><strong>{{ m.name }}</strong>
      </div>
    </section>
    <p class="coordinate-note">近似城市 / 县中心演示坐标，非设备现场精确位置。</p>
  </section>
</template>
