<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useApp } from './store';
import ActionButton from './ActionButton.vue';
import { ApiError } from './api-client';
import { loginErrorMessage } from './login-errors';
import { readStaffRoleDraft } from './staff-role-draft';
const app = useApp(),
  router = useRouter(),
  route = useRoute(),
  dev = ref(false),
  oidc = ref(false),
  loading = ref(true),
  configIssue = ref(''),
  subject = ref('synthetic-a');
const callbackIssue = computed(() => loginErrorMessage(route.query.error));
const sessionMessages = {
  expired: '您的登录会话已过期，请重新登录。',
  network: '网络连接失败，请检查网络后重试。',
  unavailable: '会话服务暂时不可用，请稍后重试。',
};
const notice = computed(() => configIssue.value || callbackIssue.value ||
  (app.sessionIssue ? sessionMessages[app.sessionIssue] : ''));
const canRetry = computed(() => !!configIssue.value || app.sessionIssue === 'network' || app.sessionIssue === 'unavailable');

async function loadConfig() {
  loading.value = true;
  configIssue.value = '';
  try {
    const c = await app.api<{ development: boolean; oidc: boolean }>('/auth/config');
    dev.value = c.development === true;
    oidc.value = c.oidc === true;
    if (!oidc.value && !dev.value) configIssue.value = '企业认证服务暂时不可用，请稍后重试。';
  } catch (e) {
    oidc.value = false;
    dev.value = false;
    configIssue.value = e instanceof ApiError && e.status === 0
      ? '网络连接失败，请检查网络后重试。'
      : '企业认证服务暂时不可用，请稍后重试。';
  } finally {
    loading.value = false;
  }
}
onMounted(loadConfig);
async function login() {
  try {
    await app.api('/auth/dev', 'POST', { subject: subject.value });
    await app.loadUser();
    if (app.user) {
      app.toast('登录成功');
      await router.push('/');
    }
  } catch {
    configIssue.value = '登录暂时未完成，请重试。';
  }
}
async function enterpriseLogin() {
  await loadConfig();
  if (oidc.value) window.location.assign(readStaffRoleDraft() ? '/auth/login?reauth=1' : '/auth/login');
}
async function retry() {
  await Promise.all([app.loadUser(), loadConfig()]);
  if (app.user && !callbackIssue.value) await router.replace('/');
}
</script>
<template>
  <div class="login">
    <div class="login-box">
      <div class="eyebrow">U-DAO / PROJECT INTAKE</div>
      <h1>每一个项目，<br />都有清晰的起点。</h1>
      <p class="muted">项目报备 · 归属确认 · 销售跟进</p>
      <p v-if="notice" class="login-notice" role="alert">{{ notice }}</p>
      <ActionButton
        action-id="auth.login"
        primary
        :disabled="loading || !oidc"
        :reason="loading ? '正在检查企业认证服务' : '请重试连接企业认证服务'"
        :run="enterpriseLogin"
      >
        {{ callbackIssue || app.sessionIssue === 'expired' ? '重新登录' : '企业账号登录' }} →
      </ActionButton>
      <ActionButton v-if="canRetry" action-id="auth.retry" :disabled="loading" :run="retry">重试连接</ActionButton>
      <p class="muted">如仍无法登录，请联系系统管理员。</p>
      <section v-if="dev" class="dev-login">
        <small>仅限本地开发 · 合成身份</small><label>测试身份<select v-model="subject">
          <option value="synthetic-a">示例销售 A</option>
          <option value="synthetic-b">示例销售 B</option>
          <option value="synthetic-manager">示例主管</option>
          <option value="synthetic-director">示例总监</option>
          <option value="synthetic-admin">示例管理员</option>
          <option value="synthetic-staff-desktop">交接销售桌面</option>
          <option value="synthetic-staff-mobile">交接销售手机</option>
          <option value="synthetic-c">示例销售 C</option>
          <option value="synthetic-manager-other">示例另一组主管</option>
        </select></label><ActionButton action-id="auth.dev-login" :run="login">使用开发身份进入</ActionButton>
      </section>
    </div>
  </div>
</template>
