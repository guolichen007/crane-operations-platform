<script setup lang="ts">
import { ref } from 'vue';
import { rememberAction } from './focus';
import { useApp } from './store';
const props = withDefaults(
  defineProps<{
    actionId: string;
    run: () => unknown;
    disabled?: boolean;
    reason?: string;
    primary?: boolean;
  }>(),
  { disabled: false, primary: false, reason: undefined }
);
const busy = ref(false);
const app = useApp();
async function activate(event: MouseEvent) {
  rememberAction(event.currentTarget as HTMLElement);
  if (busy.value || props.disabled) return;
  busy.value = true;
  app.error = '';
  try {
    await props.run?.();
  } catch (e) {
    app.error = e instanceof Error ? e.message : '操作失败，请重试';
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <button
    type="button"
    :data-action-id="actionId"
    :disabled="disabled || busy"
    :aria-busy="busy"
    :title="disabled ? reason : undefined"
    :class="{ primary }"
    @click="activate"
  >
    <span v-if="busy" class="spinner" aria-hidden="true"></span><slot />
  </button>
</template>
