<script setup lang="ts">
import MiniTrend from './MiniTrend.vue';
import { ref, watch, onUnmounted } from 'vue';
import { gsap } from 'gsap';
const props = defineProps<{
  label: string;
  value?: string | number;
  note?: string;
  trend?: number[];
}>();
const shown = ref(props.value),
  counter = { value: 0 };
let tween: gsap.core.Tween | undefined;
watch(
  () => props.value,
  (value) => {
    tween?.kill();
    if (typeof value !== 'number' || matchMedia('(prefers-reduced-motion: reduce)').matches) {
      shown.value = value;
      return;
    }
    counter.value = typeof shown.value === 'number' ? shown.value : 0;
    tween = gsap.to(counter, {
      value,
      duration: 0.35,
      ease: 'power2.out',
      onUpdate: () => {
        shown.value = Math.round(counter.value);
      },
    });
  }
);
onUnmounted(() => tween?.kill());
</script>
<template>
  <div class="metric-card">
    <span>{{ label }}</span
    ><strong>{{ shown ?? '—' }}</strong
    ><small>{{ note }}</small
    ><MiniTrend v-if="trend?.length" :values="trend" />
  </div>
</template>
