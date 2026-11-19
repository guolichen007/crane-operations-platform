<script setup lang="ts">
// V1.1 go-live: first-time account claim (首次认领). The member signed in with
// the temporary login name the administrator issued and has already set a new
// password (Keycloak). Here they enter their real name and their own mainland
// mobile; the login name becomes that phone, the temporary name stops working
// and they sign in again with the phone. Nothing else is available until then.
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { useApp } from './store';
import { recovery } from './reasons';
import ActionButton from './ActionButton.vue';
import { newIdempotencyKey } from './staff-account';

const app = useApp(),
  router = useRouter();
const name = ref(''),
  phone = ref(''),
  errors = ref<{ name?: string; phone?: string }>({}),
  failure = ref(''),
  done = ref('');
// One key per filled-in form: a retried click is the same request (no second claim).
let key = newIdempotencyKey(),
  keyFor = '';

interface ClaimCheck { name?: string; phone?: string }
function check(): ClaimCheck {
  const e: ClaimCheck = {};
  const n = name.value.trim();
  if (!n) e.name = '请填写你的真实姓名';
  else if (/^[0-9\s]+$/.test(n)) e.name = '姓名不能只包含数字';
  else if ([...n].length > 64) e.name = '姓名不能超过 64 个字';
  const p = phone.value.replace(/[\s-]/g, '').replace(/^(\+?86)/, '');
  if (!/^1[3-9]\d{9}$/.test(p)) e.phone = '请填写本人的 11 位中国大陆手机号';
  return e;
}
async function submit() {
  failure.value = '';
  errors.value = check();
  if (errors.value.name || errors.value.phone) {
    const field = errors.value.name ? 'claimName' : 'claimPhone';
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[name=${field}]`)?.focus());
    return;
  }
  const body = { name: name.value.trim(), phone: phone.value.trim() };
  const fingerprint = body.name + '\u0000' + body.phone;
  if (fingerprint !== keyFor) {
    key = newIdempotencyKey();
    keyFor = fingerprint;
  }
  try {
    const r = await app.api<{ claimed: boolean; login: string }>('/api/v1/me/claim', 'POST', body, key);
    // The server already ended every session; the page stays until the member
    // chooses to sign in again (the shell is left on the next navigation).
    done.value = r.login;
  } catch (e) {
    const r = recovery(e);
    const code = (e as { code?: string }).code ?? '';
    if (code === 'ACCOUNT_PHONE_TAKEN' || code === 'ACCOUNT_PHONE_BUSY' || code === 'ACCOUNT_PHONE_MAINLAND_ONLY') errors.value = { phone: r.message };
    else if (code === 'PROFILE_NAME_INVALID') errors.value = { name: r.message };
    else failure.value = r.message;
  }
}
async function logout() {
  await app.logout();
  await router.push('/login');
}
async function signInAgain() {
  // Idempotent: the session is already gone; this clears the local state too.
  await app.logout().catch(() => undefined);
  await router.push('/login');
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>{{ done ? '认领完成' : '完成首次登录' }}</h1>
      <p v-if="!done" class="muted">请填写你的真实姓名和本人手机号。提交后，登录账号会改为这个手机号，临时登录名随即失效，需要用手机号重新登录。</p>
    </div>
  </div>
  <section v-if="done" class="panel profile-form" role="status">
    <p>你的登录账号已改为手机号 <strong>{{ done }}</strong>。临时登录名已失效，请用手机号和刚设置的密码重新登录。</p>
    <div class="actions"><ActionButton action-id="claim.sign-in-again" primary :run="signInAgain">用手机号重新登录</ActionButton></div>
  </section>
  <form v-else class="profile-form panel" novalidate @submit.prevent="submit">
    <p v-if="failure" class="form-alert" role="alert">{{ failure }}</p>
    <label :class="{ invalid: errors.name }">真实姓名 <span class="req" aria-hidden="true">*</span>
      <input v-model="name" name="claimName" autocomplete="name" maxlength="64" aria-required="true" :aria-invalid="!!errors.name" :aria-describedby="errors.name ? 'claim-name-error' : undefined" />
      <small v-if="errors.name" id="claim-name-error" class="field-error" role="alert">{{ errors.name }}</small>
    </label>
    <label :class="{ invalid: errors.phone }">本人手机号 <span class="req" aria-hidden="true">*</span>
      <input v-model="phone" name="claimPhone" type="tel" inputmode="tel" autocomplete="tel" maxlength="20" placeholder="11 位手机号" aria-required="true" :aria-invalid="!!errors.phone" :aria-describedby="errors.phone ? 'claim-phone-error' : 'claim-phone-hint'" />
      <small v-if="errors.phone" id="claim-phone-error" class="field-error" role="alert">{{ errors.phone }}</small>
      <small v-else id="claim-phone-hint" class="muted">以后用这个手机号登录。一个手机号只能对应一名成员。</small>
    </label>
    <div class="actions">
      <ActionButton action-id="claim.submit" primary :run="submit">确认并改用手机号登录</ActionButton>
      <ActionButton action-id="auth.logout" :run="logout">退出登录</ActionButton>
    </div>
  </form>
</template>
