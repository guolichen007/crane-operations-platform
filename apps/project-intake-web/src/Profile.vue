<script setup lang="ts">
// V1.1 R3: confirm (first login) or correct one's own display name. Only the
// name can change; role, team and login phone are shown read-only. Signing out
// and changing the password stay available while the name is unconfirmed.
import { ref, computed, onMounted } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useApp } from './store';
import { roleLabel } from './navigation';
import { recovery } from './reasons';
import { when } from './model';
import ActionButton from './ActionButton.vue';

interface Profile {
  name: string;
  role: string;
  team: string;
  completed: boolean;
  source: string;
  version: number;
  history: { action: string; oldName: string; newName: string; reason: string; at: string }[];
}
const app = useApp(),
  router = useRouter(),
  route = useRoute();
const profile = ref<Profile | null>(null),
  name = ref(''),
  reason = ref(''),
  error = ref(''),
  failure = ref('');
const firstTime = computed(() => !!profile.value && !profile.value.completed);
async function load() {
  failure.value = '';
  try {
    profile.value = await app.api<Profile>('/api/v1/me/profile');
    name.value = profile.value.name;
  } catch (e) {
    failure.value = recovery(e).message;
  }
}
onMounted(load);
function check() {
  const v = name.value.trim();
  if (!v) return '请填写你的姓名';
  if (/^[0-9\s]+$/.test(v)) return '姓名不能只包含数字';
  if ([...v].length > 64) return '姓名不能超过 64 个字';
  return '';
}
async function save() {
  error.value = check();
  if (error.value || !profile.value) {
    requestAnimationFrame(() => document.querySelector<HTMLElement>('[name=displayName]')?.focus());
    return;
  }
  const wasFirst = firstTime.value;
  try {
    profile.value = await app.api<Profile>('/api/v1/me/profile', 'PATCH', { name: name.value.trim(), reason: reason.value.trim(), version: profile.value.version });
  } catch (e) {
    const r = recovery(e);
    // A stale page: someone (e.g. an administrator) changed the name meanwhile.
    error.value = r.kind === 'conflict' ? '姓名已在其他页面更新，已重新读取最新姓名，请核对后再保存' : r.message;
    if (r.kind === 'conflict') {
      const typed = name.value;
      await load();
      name.value = typed;
    }
    return;
  }
  await app.loadUser();
  app.toast(wasFirst ? '姓名已确认' : '姓名已更新，操作已记录');
  const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//') ? route.query.next : '/';
  if (wasFirst) await router.replace(next);
  reason.value = '';
}
async function logout() {
  await app.logout();
  await router.push('/login');
}
const actions: Record<string, string> = { 'profile.confirm': '首次确认', 'profile.rename': '修改姓名', 'profile.backfill': '历史核实' };
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>{{ firstTime ? '请确认你的姓名' : '个人资料' }}</h1>
      <p class="muted">
        {{ firstTime ? '姓名会显示在你负责的项目、审核记录和团队统计中。确认后即可开始使用。' : '只能修改你本人的显示姓名；角色、团队和登录手机号由管理员维护。' }}
      </p>
    </div>
  </div>
  <p v-if="failure" class="form-alert" role="alert">{{ failure }} <ActionButton action-id="profile.reload" :run="load">重新加载</ActionButton></p>
  <form v-if="profile" class="profile-form panel" novalidate @submit.prevent="save">
    <label :class="{ invalid: error }">姓名 <span class="req" aria-hidden="true">*</span>
      <input v-model="name" name="displayName" autocomplete="name" maxlength="64" aria-required="true" :aria-invalid="!!error" :aria-describedby="error ? 'name-error' : undefined" />
      <small v-if="error" id="name-error" class="field-error" role="alert">{{ error }}</small>
    </label>
    <label v-if="!firstTime">修改说明（选填）<input v-model="reason" name="profileReason" maxlength="200" /></label>
    <dl class="account-facts">
      <div><dt>角色</dt><dd>{{ roleLabel(profile.role) }}</dd></div>
      <div v-if="profile.team"><dt>团队</dt><dd>{{ profile.team }}</dd></div>
    </dl>
    <div class="actions">
      <ActionButton action-id="profile.save" primary :run="save">{{ firstTime ? '确认姓名并继续' : '保存姓名' }}</ActionButton>
      <ActionButton v-if="firstTime" action-id="auth.logout" :run="logout">退出登录</ActionButton>
    </div>
  </form>
  <section v-if="profile && profile.history.length" class="panel profile-history" aria-label="姓名记录">
    <h2>姓名记录</h2>
    <ol>
      <li v-for="(h, i) in profile.history" :key="i"><strong>{{ actions[h.action] || h.action }}</strong> · {{ h.oldName === h.newName ? h.newName : h.oldName + ' → ' + h.newName }} <span class="muted">{{ when(h.at) }}<template v-if="h.reason"> · {{ h.reason }}</template></span></li>
    </ol>
  </section>
</template>
