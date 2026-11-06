<script setup lang="ts">
import { computed } from 'vue';
import { sceneProvider } from '../../scene/provider';
import type { HomeDataState } from '../../scene/home-metrics';
defineProps<{ demo: boolean; state: HomeDataState }>();
const events = computed(() => sceneProvider.events.value);
// API alarm records: the API serves the latest ≤100 records, not open alarms.
const records = computed(() => sceneProvider.alarmRecordCount.value);
</script>
<template>
  <section class="event-strip" aria-label="最近事件" data-testid="home-events">
    <div class="event-label">
      <span class="live-dot"></span><strong>最近事件</strong>
      <small>{{ demo ? 'DEMO / 模拟事件' : `CLOUD / 最近 ${state === 'loading' || state === 'unavailable' ? '—' : records} 条告警记录 · 非未处理数${state === 'stale' ? ' · 已过期' : ''}` }}</small>
    </div>
    <p v-if="state === 'loading'" class="event-empty">正在读取告警记录…</p>
    <p v-else-if="state === 'unavailable'" class="event-empty">告警记录不可用</p>
    <p v-else-if="!events.length" class="event-empty">{{ demo ? '暂无模拟事件' : '暂无告警记录' }}</p>
    <div v-for="(event, i) in events.slice(0, 4)" :key="i + event.time" class="event-item">
      <time>{{ event.time }}</time><span :style="{ color: event.color }">{{ event.kind }}</span>
      <p>{{ event.text }}</p>
    </div>
  </section>
</template>
