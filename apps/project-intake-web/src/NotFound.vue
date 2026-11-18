<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router';
import { SearchX } from '@lucide/vue';
import ActionButton from './ActionButton.vue';
// V1.1 final: unknown routes show a 404 inside the signed-in shell. The login
// and permission guards in main.ts run before this page as for any route.
const route = useRoute();
const router = useRouter();
</script>
<template>
  <section class="empty not-found" aria-labelledby="not-found-title">
    <SearchX class="empty-symbol" :size="28" aria-hidden="true" />
    <h1 id="not-found-title">页面不存在</h1>
    <p class="muted">地址 <span class="mono">{{ route.path }}</span> 没有对应的页面，可能已移动或输入有误。</p>
    <div class="actions not-found-actions">
      <ActionButton action-id="nav.open" primary :run="() => router.push('/')">返回工作台</ActionButton><ActionButton
        action-id="nav.open"
        :run="() => router.push('/projects')"
      >
        打开项目中心
      </ActionButton>
    </div>
  </section>
</template>
