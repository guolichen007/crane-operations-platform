<script setup lang="ts">
// Recent events. Demo: local fixtures. API: the latest alarm records from /alarms (a detail list of at
// most 100 — its length is never shown as a count). The record COUNT comes from the operations summary:
// metrics.alarm.records.total over the stated window (H2-1D-2). Times are Shanghai local (H0-D2).
import { computed } from 'vue';
import { sceneProvider } from '../../scene/provider';
import type { HomeDataState } from '../../scene/home-metrics';
import type { OperationsSummary } from '../../../../../contracts/typescript/types.gen';
import { WINDOW_LABEL } from '../../scene/home-summary';
const props = defineProps<{ demo: boolean; state: HomeDataState; summary: OperationsSummary | null }>();
const events = computed(() => sceneProvider.events.value);
const recordText = computed(() => {
  const s = props.summary;
  if (!s) return '告警记录总数不可用';
  return `${WINDOW_LABEL[s.freshness.alarmWindow]}告警记录 ${s.metrics.alarm.records.total} 条（服务端计数 · 非未处理数）`;
});
</script>
<template>
  <section class="event-strip" aria-label="最近事件" data-testid="home-events">
    <div class="event-label">
      <span class="live-dot"></span><strong>最近事件</strong>
      <small data-testid="event-records">{{ demo ? 'DEMO / 模拟事件' : `CLOUD / ${recordText} · 下列为最近记录明细${state === 'stale' ? ' · 已过期' : ''}` }}</small>
    </div>
    <p v-if="state === 'loading'" class="event-empty">正在读取告警记录…</p>
    <p v-else-if="state === 'unavailable'" class="event-empty">告警记录明细不可用</p>
    <p v-else-if="!events.length" class="event-empty">{{ demo ? '暂无模拟事件' : '暂无告警记录' }}</p>
    <div v-for="(event, i) in events.slice(0, 4)" :key="i + event.time" class="event-item">
      <time>{{ event.time }}</time><span :style="{ color: event.color }">{{ event.kind }}</span>
      <p>{{ event.text }}</p>
    </div>
  </section>
</template>
