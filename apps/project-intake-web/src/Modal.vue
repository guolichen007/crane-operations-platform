<script setup lang="ts">
import { watch } from 'vue';
import { actionInvoker } from './focus';
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from 'reka-ui';
const props = defineProps<{
  open: boolean;
  title: string;
  description?: string;
  sheet?: boolean;
}>();
let returnFocus: HTMLElement | null = null;
watch(
  () => props.open,
  (open) => {
    if (open)
      returnFocus =
        document.activeElement !== document.body
          ? (document.activeElement as HTMLElement)
          : actionInvoker();
  },
  { flush: 'sync' }
);
function restoreFocus(event: Event) {
  event.preventDefault();
  requestAnimationFrame(() => returnFocus?.focus());
}
const emit = defineEmits<{ 'update:open': [v: boolean] }>();
</script>
<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogPortal>
      <DialogOverlay class="overlay" /><DialogContent
        :class="['modal', { sheet }]"
        @interact-outside.prevent
        @close-auto-focus="restoreFocus"
      >
        <DialogTitle class="modal-title">{{ title }}</DialogTitle><DialogDescription class="muted">
          {{ description || '请查看并完成以下操作。' }}
        </DialogDescription><slot />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
