<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useApp } from './store';
import ActionButton from './ActionButton.vue';
import BrandMark from './BrandMark.vue';
import { ApiError } from './api-client';
import { loginErrorMessage } from './login-errors';
import { readStaffDraft } from './staff-role-draft';
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
  expired: '登录已过期，请重新登录。',
  network: '网络连接失败，请检查网络后重试。',
  unavailable: '会话服务暂时不可用，请稍后重试。',
  'signed-out': '已退出登录。',
  switched: '此浏览器已在其他标签页登录了另一个账号，请重新登录。',
};
const notice = computed(() => configIssue.value || callbackIssue.value ||
  (app.sessionIssue ? sessionMessages[app.sessionIssue] : ''));
const noticeTone = computed(() => !configIssue.value && !callbackIssue.value &&
  app.sessionIssue === 'signed-out' ? 'info' : 'warn');
const canRetry = computed(() => !!configIssue.value || app.sessionIssue === 'network' || app.sessionIssue === 'unavailable');

async function loadConfig() {
  loading.value = true;
  configIssue.value = '';
  try {
    const c = await app.api<{ development: boolean; oidc: boolean }>('/auth/config');
    dev.value = c.development === true;
    oidc.value = c.oidc === true;
    if (!oidc.value && !dev.value) configIssue.value = '登录服务暂时不可用，请稍后重试。';
  } catch (e) {
    oidc.value = false;
    dev.value = false;
    configIssue.value = e instanceof ApiError && e.status === 0
      ? '网络连接失败，请检查网络后重试。'
      : '登录服务暂时不可用，请稍后重试。';
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
// The password is entered only on the enterprise (Keycloak) page, never here.
async function enterpriseLogin() {
  await loadConfig();
  if (!oidc.value) return;
  window.location.assign(readStaffDraft() ? '/auth/login?reauth=1' : '/auth/login');
}
async function retry() {
  await Promise.all([app.loadUser(), loadConfig()]);
  if (app.user && !callbackIssue.value) await router.replace('/');
}
</script>
<template>
  <div class="login">
    <section class="login-cover" aria-label="U·DAO 友道智造 项目报备中心">
      <BrandMark variant="full" tagline inverse class="login-brand" />
      <svg class="login-crane" viewBox="0 0 640 300" role="img" aria-label="桥式行车示意图">
        <!-- runway rails and columns -->
        <g class="crane-structure">
          <path d="M24 70 H616" />
          <path d="M40 70 V300 M600 70 V300" />
          <path d="M40 120 L84 70 M600 120 L556 70" />
        </g>
        <!-- bridge girder spanning the bay -->
        <g class="crane-girder">
          <rect x="52" y="78" width="536" height="26" rx="2" />
          <path d="M60 104 L84 78 L108 104 L132 78 L156 104 L180 78 L204 104 L228 78 L252 104 L276 78 L300 104 L324 78 L348 104 L372 78 L396 104 L420 78 L444 104 L468 78 L492 104 L516 78 L540 104 L564 78 L580 96" />
        </g>
        <!-- trolley, rope and hook -->
        <g class="crane-trolley">
          <rect x="300" y="60" width="72" height="20" rx="3" />
          <circle cx="314" cy="82" r="5" /><circle cx="358" cy="82" r="5" />
        </g>
        <path class="crane-rope" d="M330 104 V196 M342 104 V196" />
        <g class="crane-hook">
          <rect x="322" y="196" width="28" height="16" rx="3" />
          <path d="M336 212 V226 a10 10 0 1 1 -10 10" />
        </g>
        <!-- the load: a registered project card -->
        <g class="crane-card">
          <path d="M326 246 L292 262 M346 246 L380 262" />
          <rect x="262" y="262" width="148" height="34" rx="4" />
          <path d="M276 274 H352 M276 284 H330" />
          <circle cx="392" cy="279" r="7" />
        </g>
      </svg>
      <div class="login-cover-copy">
        <h2>行车项目，从第一条线索就有归属。</h2>
        <ul>
          <li>登记客户、厂区与设备需求，系统提示重复项目</li>
          <li>主管审核确认归属，保护期内由负责人持续跟进</li>
          <li>十二阶段销售管道，总监随时查看各团队进展</li>
        </ul>
      </div>
    </section>
    <section class="login-panel">
      <div class="login-box">
        <BrandMark variant="full" class="login-brand compact" />
        <h1>登录项目报备中心</h1>
        <p class="muted">使用管理员为你开通的账号和密码登录。</p>
        <p v-if="notice" class="login-notice" :data-tone="noticeTone" role="alert">{{ notice }}</p>
        <ActionButton
          action-id="auth.login"
          primary
          :disabled="loading || !oidc"
          :reason="loading ? '正在连接登录服务' : '请重试连接登录服务'"
          :run="() => enterpriseLogin()"
        >
          {{ callbackIssue || app.sessionIssue === 'expired' ? '重新登录' : '登录' }}
        </ActionButton>
        <ActionButton v-if="canRetry" action-id="auth.retry" :disabled="loading" :run="retry">重试连接</ActionButton>
        <p class="muted login-help">还没有账号或无法登录，请联系系统管理员。</p>
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
    </section>
  </div>
</template>
