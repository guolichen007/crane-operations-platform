<script setup lang="ts">
import { inject, ref } from 'vue';
import { modalErrorKey } from './modal-error';
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
// Inside a dialog, failures are reported to that dialog (see Modal.vue).
const reportInDialog = inject(modalErrorKey, null);
async function activate(event: MouseEvent) {
  rememberAction(event.currentTarget as HTMLElement);
  if (busy.value || props.disabled) return;
  busy.value = true;
  app.error = '';
  reportInDialog?.('');
  try {
    await props.run?.();
  } catch (e) {
    const message = e instanceof Error ? e.message : '操作失败，请重试';
    if (reportInDialog) reportInDialog(message);
    else app.error = message;
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
