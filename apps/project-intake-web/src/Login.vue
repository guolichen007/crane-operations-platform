<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import { useApp } from './store';
import ActionButton from './ActionButton.vue';
const app = useApp(),
  router = useRouter(),
  dev = ref(false),
  oidc = ref(false),
  subject = ref('synthetic-a');
onMounted(async () => {
  try {
    const c = await app.api<{ development: boolean; oidc: boolean }>('/auth/config');
    dev.value = c.development;
    oidc.value = c.oidc;
  } catch (e) {
    app.error = (e as Error).message;
  }
});
async function login() {
  await app.api('/auth/dev', 'POST', { subject: subject.value });
  await app.loadUser();
  app.toast('登录成功');
  await router.push('/');
}
function enterpriseLogin() {
  window.location.assign('/auth/login');
}
</script>
<template>
  <div class="login">
    <div class="login-box">
      <div class="eyebrow">U-DAO / PROJECT INTAKE</div>
      <h1>每一个项目，<br />都有清晰的起点。</h1>
      <p class="muted">项目报备 · 归属确认 · 销售跟进</p>
      <ActionButton
        action-id="auth.login"
        primary
        :disabled="!oidc"
        reason="企业 OIDC 尚未配置，本地可使用下方 synthetic 入口"
        :run="enterpriseLogin"
      >
        企业账号登录 →
      </ActionButton>
      <section v-if="dev" class="dev-login">
        <small>仅限本地开发 · 合成身份</small><label>测试身份<select v-model="subject">
          <option value="synthetic-a">示例销售 A</option>
          <option value="synthetic-b">示例销售 B</option>
          <option value="synthetic-manager">示例主管</option>
        </select></label><ActionButton action-id="auth.dev-login" :run="login">使用开发身份进入</ActionButton>
      </section>
    </div>
  </div>
</template>
