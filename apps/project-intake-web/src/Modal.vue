<script setup lang="ts">
import { provide, ref, watch } from 'vue';
import { X } from '@lucide/vue';
import { actionInvoker } from './focus';
import ActionButton from './ActionButton.vue';
import { modalErrorKey } from './modal-error';
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
// Errors thrown by actions inside the dialog are shown here, above the dialog's
// own content, never behind the overlay in the page banner.
const failure = ref('');
provide(modalErrorKey, (message: string) => (failure.value = message));
let returnFocus: HTMLElement | null = null;
watch(
  () => props.open,
  (open) => {
    failure.value = '';
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
        <!-- Same path as Esc: the owner decides (e.g. confirm unsaved input). -->
        <ActionButton action-id="dialog.close" class="modal-close" aria-label="关闭" :run="() => emit('update:open', false)"><X :size="18" aria-hidden="true" /></ActionButton>
        <DialogTitle class="modal-title">{{ title }}</DialogTitle><DialogDescription class="muted">
          {{ description || '请查看并完成以下操作。' }}
        </DialogDescription>
        <p v-if="failure" class="form-alert modal-failure" role="alert">{{ failure }}</p>
        <slot />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
