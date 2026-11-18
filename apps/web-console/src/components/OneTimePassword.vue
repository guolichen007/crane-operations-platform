<script setup lang="ts">
// Shows a one-time temporary password exactly once. It lives only in this component's memory:
// never in the query cache, Web Storage, the URL or the console. Closing requires confirming that
// the password was handed over; afterwards it is gone (reset the password again if it was lost).
import { ref, onBeforeUnmount } from 'vue';
import { Copy, Check, KeyRound } from 'lucide-vue-next';

const props = defineProps<{ title: string; username: string; displayName: string; password: string; note?: string }>();
const emit = defineEmits<{ close: [] }>();
const copied = ref(false);
const confirmed = ref(false);
const shown = ref(false);

async function copy() {
  try {
    await navigator.clipboard.writeText(props.password);
    copied.value = true;
  } catch {
    shown.value = true; // clipboard blocked: let the administrator read it
  }
}
function close() {
  if (!confirmed.value) return;
  emit('close');
}
onBeforeUnmount(() => {
  copied.value = false;
});
</script>
<template>
  <div class="modal-backdrop" data-testid="one-time-password">
    <section class="workspace-modal otp-modal" role="dialog" aria-modal="true" :aria-label="title">
      <h2><KeyRound :size="16" />{{ title }}</h2>
      <p class="form-hint">请将以下初始密码通过当面或企业内部通讯工具交给 <strong>{{ displayName }}</strong>。员工首次登录时必须修改密码。</p>
      <dl class="otp-facts">
        <div><dt>登录用户名</dt><dd data-testid="otp-username">{{ username }}</dd></div>
        <div>
          <dt>初始密码（只显示这一次）</dt>
          <dd class="otp-secret">
            <code data-testid="otp-password" :class="{ masked: !shown }">{{ shown ? password : '••••••••••••••••' }}</code>
            <button type="button" class="ghost-action" data-testid="otp-reveal" @click="shown = !shown">{{ shown ? '隐藏' : '显示' }}</button>
            <button type="button" class="ghost-action" data-testid="otp-copy" @click="copy">
              <Check v-if="copied" :size="14" /><Copy v-else :size="14" />{{ copied ? '已复制' : '复制' }}
            </button>
          </dd>
        </div>
      </dl>
      <p v-if="note" class="workspace-notice" role="status">{{ note }}</p>
      <p class="form-hint">系统不会保存此密码，也无法再次查看。如未记录，请关闭后对该人员再次「重置密码」。</p>
      <label class="otp-confirm"><input v-model="confirmed" type="checkbox" data-testid="otp-confirm" />我已记录并将安全交付该密码</label>
      <div class="modal-actions">
        <button type="button" class="primary-action" :disabled="!confirmed" data-testid="otp-close" @click="close">完成</button>
      </div>
    </section>
  </div>
</template>
<style scoped>
.otp-modal h2 { display: flex; align-items: center; gap: 8px; }
.otp-facts { display: grid; gap: 10px; margin: 0; }
.otp-facts div { border: 1px solid var(--line-soft); border-radius: 8px; padding: 8px 10px; }
.otp-facts dt { font-size: 11px; color: var(--text-3); }
.otp-facts dd { margin: 4px 0 0; color: var(--text-1); }
.otp-secret { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.otp-secret code { font-size: 16px; letter-spacing: 1px; padding: 4px 8px; border-radius: 6px; background: var(--surface-900); user-select: all; }
.otp-confirm { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-2); }
</style>
