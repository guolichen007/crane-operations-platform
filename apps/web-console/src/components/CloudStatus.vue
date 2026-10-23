<script setup lang="ts">
import { ref, onMounted } from 'vue';
const token = ref(''),
  message = ref('云端模式 · 设备页读取 Last Known State'),
  authenticated = ref(false);
async function login() {
  const value = token.value;
  token.value = '';
  try {
    const res = await fetch('/api/v1/auth/dev-session', {
      method: 'POST',
      headers: { Authorization: `Bearer ${value}` },
    });
    if (!res.ok) throw Error('登录失败');
    authenticated.value = true;
    message.value = '已连接 Cloud API · 资产与运行来源分开显示';
    window.dispatchEvent(new Event('cloud-authenticated'));
  } catch {
    message.value = '登录失败，请核对本地开发凭据';
  }
}
onMounted(async () => {
  try {
    authenticated.value = (await fetch('/api/v1/devices')).ok;
  } catch {
    /* login remains available */
  }
});
</script>
<template>
  <section class="cloud-status" aria-label="云端连接">
    <span>{{ message }}</span>
    <form v-if="!authenticated" @submit.prevent="login">
      <label
        >本地开发凭据
        <input
          v-model="token"
          aria-label="本地开发凭据"
          type="password"
          autocomplete="off"
          required
      /></label>
      <button type="submit">连接云端</button>
    </form>
    <router-link to="/runtime">查看云端设备 →</router-link>
  </section>
</template>
<style scoped>
.cloud-status {
  position: fixed;
  bottom: 12px;
  left: 80px;
  z-index: 100;
  display: flex;
  gap: 20px;
  align-items: center;
  padding: 10px 16px;
  background: #122b35f5;
  border: 1px solid #39606b;
  color: #b9d4dd;
  font-size: 12px;
}
form {
  display: flex;
  gap: 8px;
}
input {
  width: 180px;
}
a {
  color: #72dcdd;
}
</style>
