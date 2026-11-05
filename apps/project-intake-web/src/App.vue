<script setup lang="ts">
import { ref, computed, watch } from 'vue';
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
  Users,
  Settings,
  Inbox,
  ClipboardCheck,
  Copy,
} from '@lucide/vue';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
import { clearStaffRoleDraft } from './staff-role-draft';
import { navigation, navGroups, can, workspaceLabel, roleLabel, type NavItem } from './navigation';
const app = useApp(),
  router = useRouter(),
  route = useRoute(),
  search = ref('');
const icons = {
  dashboard: LayoutDashboard,
  clock: Clock3,
  folder: FolderKanban,
  inbox: Inbox,
  columns: Columns3,
  check: ClipboardCheck,
  copy: Copy,
  chart: ChartNoAxesCombined,
  users: Users,
  settings: Settings,
};
const nav = computed(() => navigation(app.user));
const groups = computed(() => {
  const out: { key: NavItem['group']; label: string; items: NavItem[] }[] = [];
  for (const item of nav.value) {
    let g = out.find((x) => x.key === item.group);
    if (!g) out.push((g = { key: item.group, label: navGroups[item.group], items: [] }));
    g.items.push(item);
  }
  return out;
});
const active = (path: string) =>
  path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(path + '/');
const createLabel = computed(() => (app.user?.role === 'baobei_admin' ? '新建项目' : '新建报备'));
async function logout() {
  await app.api('/auth/logout', 'POST', {});
  clearStaffRoleDraft();
  app.user = null;
  app.toast('已退出登录');
  await router.push('/login');
}
watch(() => app.user, (user, previous) => {
  if (previous && !user && app.sessionIssue === 'expired' && route.path !== '/login')
    void router.replace('/login');
});
function showError(event: ErrorEvent) {
  app.error = event.message;
}
window.addEventListener('error', showError);
</script>
<template>
  <div v-if="app.user && route.path !== '/login'" class="shell">
    <aside class="sidebar" aria-label="主导航">
      <a href="/" class="brand" data-action-id="nav.open" @click.prevent="router.push('/')"><span class="brand-mark" aria-hidden="true"></span><span>项目报备中心<small>U-DAO 行车项目</small></span></a>
      <div class="workspace-label">{{ workspaceLabel(app.user) }}</div>
      <nav>
        <template v-for="g in groups" :key="g.key">
          <span class="nav-group">{{ g.label }}</span>
          <ActionButton
            v-for="item in g.items"
            :key="item.path"
            action-id="nav.open"
            :class="{ active: active(item.path) }"
            :aria-current="active(item.path) ? 'page' : undefined"
            :title="item.label"
            :run="() => router.push(item.path)"
          >
            <component :is="icons[item.icon as keyof typeof icons]" :size="17" aria-hidden="true" />{{ item.label }}
          </ActionButton>
        </template>
      </nav>
      <div class="sidebar-footer">
        <span class="account-role">{{ roleLabel(app.user.role) }}</span>
        <span>{{ app.user.name }}</span>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <form
          class="global-search"
          role="search"
          data-action-id="global.search"
          @submit.prevent="router.push({ path: '/projects', query: { q: search } })"
        >
          <Search :size="16" aria-hidden="true" /><input
            v-model="search"
            aria-label="全局搜索"
            placeholder="搜索项目、客户或报备编号"
          />
        </form>
        <div class="top-actions">
          <ActionButton
            v-if="app.user.role === 'baobei_sales' || can(app.user, 'project.create')"
            :disabled="!can(app.user, 'project.create')"
            reason="当前账号未启用新建报备"
            action-id="project.create"
            primary
            :run="() => router.push('/new')"
          >
            <Plus :size="16" aria-hidden="true" /><span>{{ createLabel }}</span>
          </ActionButton><span class="avatar" aria-hidden="true">{{ app.user.name.slice(-1) }}</span><span class="user-name">{{ app.user.name }}<small>{{ roleLabel(app.user.role) }}</small></span><ActionButton action-id="auth.logout" :run="logout" aria-label="退出登录">
            <LogOut :size="16" aria-hidden="true" />
          </ActionButton>
        </div>
      </header>
      <main class="content"><RouterView :key="route.path" /></main>
    </div>
  </div>
  <RouterView v-else-if="route.path === '/login'" />
  <div v-if="app.message" class="toast" role="status">{{ app.message }}</div>
  <div v-if="app.error && route.path !== '/login'" class="error-banner" role="alert">
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
