<script setup lang="ts">
import { ref } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import {
  LayoutDashboard,
  FolderKanban,
  Clock3,
  Columns3,
  ChartNoAxesCombined,
  Plus,
  Search,
  LogOut,
} from 'lucide-vue-next';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
const app = useApp(),
  router = useRouter(),
  route = useRoute(),
  search = ref('');
const nav = [
  { path: '/', label: '工作台', icon: LayoutDashboard },
  { path: '/projects', label: '我的报备', icon: FolderKanban },
  { path: '/followups', label: '待跟进', icon: Clock3 },
  { path: '/pipeline', label: '项目管道', icon: Columns3 },
  { path: '/statistics', label: '统计', icon: ChartNoAxesCombined },
];
async function logout() {
  await app.api('/auth/logout', 'POST', {});
  app.user = null;
  app.toast('已退出登录');
  await router.push('/login');
}
function showError(event: ErrorEvent) {
  app.error = event.message;
}
window.addEventListener('error', showError);
</script>
<template>
  <div v-if="app.user" class="shell">
    <aside class="sidebar">
      <a href="/" class="brand" data-action-id="nav.open" @click.prevent="router.push('/')"><span class="brand-mark">U</span><span>项目报备中心<small>PROJECT INTAKE</small></span></a>
      <div class="workspace-label">销售工作空间</div>
      <nav>
        <ActionButton
          v-for="item in nav"
          :key="item.path"
          action-id="nav.open"
          :class="{ active: route.path === item.path }"
          :run="() => router.push(item.path)"
        >
          <component :is="item.icon" :size="17" />{{ item.label }}
        </ActionButton>
      </nav>
      <div class="sidebar-footer">
        <span class="live-dot"></span>独立报备工作空间<br /><small>U-DAO · 让项目有序推进</small>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <form
          class="global-search"
          data-action-id="global.search"
          @submit.prevent="router.push({ path: '/projects', query: { q: search } })"
        >
          <Search :size="16" /><input
            v-model="search"
            aria-label="全局搜索"
            placeholder="搜索项目、客户或报备编号…"
          /><kbd>↵</kbd>
        </form>
        <div class="top-actions">
          <ActionButton
            v-if="app.user.role === 'baobei_sales'"
            action-id="project.create"
            primary
            :run="() => router.push('/new')"
          >
            <Plus :size="16" /><span>新建报备</span>
          </ActionButton><span class="avatar">{{ app.user.name.slice(-1) }}</span><span class="user-name">{{ app.user.name }}</span><ActionButton action-id="auth.logout" :run="logout" aria-label="退出登录">
            <LogOut :size="16" />
          </ActionButton>
        </div>
      </header>
      <main class="content"><RouterView :key="route.path" /></main>
    </div>
  </div>
  <RouterView v-else />
  <div v-if="app.message" class="toast" role="status">{{ app.message }}</div>
  <div v-if="app.error" class="error-banner" role="alert">
    {{ app.error }}<span> · 请重试或刷新页面；表单输入会保留。</span>
  </div>
  <Modal
    :open="app.confirmOpen"
    title="离开当前表单？"
    description="仍有未保存内容。离开将放弃这些改动。"
    @update:open="app.answer(false)"
  >
    <div class="actions">
      <ActionButton action-id="guard.stay" :run="() => app.answer(false)">继续编辑</ActionButton><ActionButton action-id="guard.leave" :run="() => app.answer(true)">
        放弃改动并离开
      </ActionButton>
    </div>
  </Modal>
</template>
