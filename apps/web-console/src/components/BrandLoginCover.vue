<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { ArrowUpRight, ShieldCheck, Layers3, Activity, RadioTower } from 'lucide-vue-next';
import { gsap } from 'gsap';
import BrandMark from './BrandMark.vue';
import type { AuthState } from '../cloud/auth-state';
defineProps<{ state: AuthState; error: string }>();
defineEmits<{ retry: [] }>();
const host = ref<HTMLElement>(),
  root = ref<HTMLElement>();
let dispose: (() => void) | undefined,
  alive = true,
  context: gsap.Context;
onMounted(async () => {
  if (!matchMedia('(prefers-reduced-motion: reduce)').matches)
    context = gsap.context(() => {
      gsap.from('.cover-enter', {
        y: 14,
        opacity: 0,
        stagger: 0.11,
        duration: 0.7,
        ease: 'power2.out',
      });
    }, root.value);
  try {
    const { createCoverScene } = await import('../three/cover/CoverScene');
    if (alive) dispose = createCoverScene(host.value!);
  } catch {
    /* CSS blueprint remains available without WebGL. */
  }
});
onUnmounted(() => {
  alive = false;
  dispose?.();
  context?.revert();
});
</script>
<template>
  <main ref="root" class="brand-cover" data-testid="brand-login-cover" :data-auth-state="state">
    <div ref="host" class="cover-scene" aria-hidden="true"></div>
    <header class="cover-header">
      <BrandMark /><span>友道智造<span class="brand-divider">/</span>工业智能化</span
      ><small><i></i> U-DAO INDUSTRIAL INTELLIGENCE</small>
    </header>
    <section class="cover-copy">
      <p class="cover-enter cover-kicker">CONNECTED ASSETS. INTELLIGENT OPERATIONS.</p>
      <h1 class="cover-enter">让每一次起落，<br />尽在<span>掌握。</span></h1>
      <div class="cover-enter cover-product">
        <b>全国行车设备运营平台</b>
        <p>U-Dao Crane Operations Platform</p>
      </div>
      <p class="cover-enter cover-description">
        连接设备与决策，洞察每一处现场。<br />统一设备运营 · 项目态势 · 安全运维
      </p>
      <div class="cover-enter cover-access">
        <div v-if="state === 'AUTH_LOADING'" class="cover-loading" role="status">
          正在验证安全会话…
        </div>
        <template v-else-if="state === 'AUTH_ERROR'"
          ><p role="alert">{{ error }}</p>
          <button class="cover-cta" @click="$emit('retry')">
            重试连接 <ArrowUpRight :size="18" /></button
        ></template>
        <a v-else class="cover-cta" href="/auth/login">账号登录 <ArrowUpRight :size="20" /></a>
        <small><ShieldCheck :size="14" /> 企业统一身份认证 · 安全访问</small>
      </div>
    </section>
    <div class="cover-scene-caption">
      <span class="tiny-cross">+</span><b>CRANE DIGITAL TWIN</b
      ><span>程序化工业场景 / 视觉演示</span><i></i><small>01 — CONNECT</small>
    </div>
    <footer class="cover-footer">
      <div
        v-for="[icon, title, english] in [
          [Layers3, '设备数字化', 'Equipment Digitalization'],
          [Activity, '运行可视化', 'Visual Operations'],
          [ShieldCheck, '安全智能化', 'Intelligent Safety'],
          [RadioTower, '管理精细化', 'Refined Management'],
        ]"
        :key="String(title)"
      >
        <component :is="icon" :size="23" /><span
          >{{ title }}<small>{{ english }}</small></span
        >
      </div>
      <p>友道智造 · 专注工业智能化</p>
    </footer>
  </main>
</template>
