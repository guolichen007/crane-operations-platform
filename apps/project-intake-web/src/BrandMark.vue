<script setup lang="ts">
// U·DAO automation / 友道智造 — generated mark (no official vector supplied yet).
// Emblem: a "U" that is also a crane hook, with the brand's middle dot as the load.
// variant="emblem" for compact places; "full" adds the wordmark and Chinese name.
withDefaults(defineProps<{ variant?: 'emblem' | 'full'; tagline?: boolean; inverse?: boolean }>(), {
  variant: 'emblem',
  tagline: false,
  inverse: false,
});
</script>
<template>
  <span :class="['brand-mark-svg', variant, { inverse }]" :role="variant === 'full' ? 'img' : undefined" :aria-label="variant === 'full' ? 'U·DAO automation 友道智造' : undefined" :aria-hidden="variant === 'emblem' ? 'true' : undefined">
    <svg class="emblem" viewBox="0 0 48 48" aria-hidden="true">
      <rect x="1" y="1" width="46" height="46" rx="10" class="tile" />
      <path d="M14 11v15a10 10 0 0 0 20 0V11" class="hook" />
      <path d="M10 11h8M30 11h8" class="bar" />
      <circle cx="24" cy="26" r="4" class="dot" />
    </svg>
    <span v-if="variant === 'full'" class="wordmark">
      <span class="latin">U<span class="mid">·</span>DAO <span class="auto">automation</span></span>
      <span class="cn">友道智造</span>
      <span v-if="tagline" class="tag">专注工业智能化</span>
    </span>
  </span>
</template>
<style scoped>
.brand-mark-svg { display: inline-flex; align-items: center; gap: 12px; line-height: 1; }
.emblem { width: 32px; height: 32px; flex: none; }
.full .emblem { width: 44px; height: 44px; }
.tile { fill: #173f74; }
.hook, .bar { fill: none; stroke: #ffffff; stroke-width: 4; stroke-linecap: round; stroke-linejoin: round; }
.dot { fill: #e2a20e; }
.inverse .tile { fill: #ffffff; }
.inverse .hook, .inverse .bar { stroke: #173f74; }
.wordmark { display: grid; gap: 6px; }
.latin { font-size: 21px; font-weight: 800; letter-spacing: 0.04em; color: #173f74; }
.latin .mid { color: #e2a20e; padding: 0 2px; }
.latin .auto { font-size: 13px; font-weight: 600; letter-spacing: 0.08em; color: #5f6b7c; }
.cn { font-size: 15px; font-weight: 700; letter-spacing: 0.18em; color: #1b2430; }
.tag { font-size: 12px; letter-spacing: 0.06em; color: #5f6b7c; font-weight: 400; }
.inverse .latin, .inverse .cn { color: #ffffff; }
.inverse .latin .auto, .inverse .tag { color: #c9d5e6; }
</style>
