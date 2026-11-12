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
  Settings, Trash2,
  Inbox,
  ClipboardCheck,
  Copy,
  UserRoundCog,
} from '@lucide/vue';
import ActionButton from './ActionButton.vue';
import BrandMark from './BrandMark.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
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
  trash: Trash2,
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
const accountOpen = ref(false);
// One sign-out: ends this Baobei session on the server. The next sign-in always
// asks for the password again (prompt=login), so another person can sign in.
async function logout() {
  accountOpen.value = false;
  await app.logout();
  app.toast('已退出登录');
  await router.push('/login');
}
// Keycloak's own "update password" page (current password, then new); returns here.
function changePassword() {
  accountOpen.value = false;
  window.location.assign('/auth/login?action=password');
}
watch(() => app.user, (user, previous) => {
  if (previous && !user && app.sessionIssue && route.path !== '/login')
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
      <a href="/" class="brand" data-action-id="nav.open" @click.prevent="router.push('/')"><BrandMark class="brand-logo" /><span>项目报备中心<small>U·DAO 友道智造</small></span></a>
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
          </ActionButton><ActionButton action-id="auth.account" class="account-trigger" :aria-label="'个人账号：' + app.user.name" :run="() => { accountOpen = true; }">
            <span class="avatar" aria-hidden="true">{{ app.user.name.slice(-1) }}</span><span class="user-name">{{ app.user.name }}<small>{{ roleLabel(app.user.role) }}</small></span><UserRoundCog :size="16" aria-hidden="true" />
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
    :open="accountOpen"
    title="个人账号"
    description="查看个人资料或退出登录。"
    @update:open="(v) => (accountOpen = v)"
  >
    <div v-if="app.user" class="account-options">
      <section aria-label="个人资料">
        <h3>个人资料</h3>
        <dl class="account-facts">
          <div><dt>姓名</dt><dd>{{ app.user.name }}</dd></div>
          <div><dt>角色</dt><dd>{{ roleLabel(app.user.role) }}</dd></div>
          <div><dt>工作空间</dt><dd>{{ workspaceLabel(app.user) }}</dd></div>
        </dl>
      </section>
      <section aria-label="修改密码">
        <h3>修改密码</h3>
        <template v-if="can(app.user, 'account.password')">
          <p class="muted">需要输入当前密码，再设置新密码。登录手机号只能由管理员更换。</p>
          <ActionButton action-id="auth.change-password" :run="changePassword">修改密码</ActionButton>
        </template>
        <p v-else class="muted">当前登录方式下请联系管理员修改密码。</p>
      </section>
      <section>
        <ActionButton action-id="auth.logout" primary :run="logout"><LogOut :size="16" aria-hidden="true" />退出登录</ActionButton>
      </section>
    </div>
  </Modal>
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
