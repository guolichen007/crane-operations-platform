<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue';
import { Box, RotateCcw } from 'lucide-vue-next';
import type { CraneTwinScene } from '../three/twin/CraneTwinScene';
import type { TwinAxes } from '../three/twin/CraneTwinFactory';
const props = defineProps<{
  axes?: Partial<TwinAxes>;
  demo?: boolean;
  label?: string;
  alarm?: boolean;
}>();
const host = ref<HTMLElement>(),
  error = ref(false),
  loading = ref(true);
let scene: CraneTwinScene | undefined,
  alive = true;
watch(
  () => props.axes,
  (axes) => scene?.setAxes(axes ?? {}),
  { deep: true }
);
watch(
  () => props.alarm,
  (alarm) => scene?.setHealth(alarm ? 'alarm' : 'normal')
);
onMounted(async () => {
  try {
    const { CraneTwinScene } = await import('../three/twin/CraneTwinScene');
    if (!alive) return;
    scene = new CraneTwinScene(host.value!, !!props.demo);
    scene.setAxes(props.axes ?? {});
    scene.setHealth(props.alarm ? 'alarm' : 'unknown');
    if (import.meta.env.DEV)
      (window as unknown as { __twinDiagnostics: () => unknown }).__twinDiagnostics = () =>
        scene?.diagnostics();
  } catch {
    error.value = true;
  } finally {
    loading.value = false;
  }
});
onUnmounted(() => {
  alive = false;
  scene?.dispose();
});
</script>
<template>
  <div class="twin-stage" data-testid="crane-twin">
    <div
      ref="host"
      class="twin-canvas"
      role="img"
      :aria-label="label ?? '行车四轴数字孪生结构示意'"
    ></div>
    <div class="twin-hud">
      <span><i></i> CRANE DIGITAL TWIN</span
      ><small>{{
        demo ? 'DEMO · 演示动画，非现场遥测' : '结构示意 · 最近状态，无遥测时保持静态'
      }}</small>
    </div>
    <div v-if="error" class="twin-fallback" data-testid="twin-fallback">
      <Box :size="42" /><b>行车结构示意</b>
      <p>当前环境使用二维结构视图</p>
      <div class="crane-blueprint"><i></i><i></i><span></span></div>
    </div>
    <p v-else-if="loading" class="twin-loading">正在构建数字孪生模型…</p>
    <button v-if="!error" class="twin-reset" @click="scene?.frame()">
      <RotateCcw :size="13" />复位视角
    </button>
    <div class="twin-coordinate">X — 大车　Y — 小车　Z — 起升　G — 抓斗</div>
  </div>
</template>
