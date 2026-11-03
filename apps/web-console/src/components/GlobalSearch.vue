<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { Search, CornerDownLeft } from 'lucide-vue-next';
import { devices, projects } from '../data';
import { statusText, type Device } from '../data/types';

type Result = {
  key: string;
  label: string;
  hint: string;
  kind: 'device' | 'project';
  device?: Device;
  projectId?: string;
};

const router = useRouter();
const open = ref(false);
const query = ref('');
const selectedIndex = ref(0);

const results = computed<Result[]>(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return [];
  const match = (...values: (string | undefined)[]) =>
    values.some((v) => v && v.toLowerCase().includes(q));
  const out: Result[] = [];
  for (const d of devices) {
    if (
      match(d.deviceId, d.deviceName, d.projectName, d.province, d.city ?? undefined, d.craneModel, statusText[d.status])
    ) {
      out.push({
        key: 'd:' + d.deviceId,
        label: `${d.deviceName}`,
        hint: `${d.province}${d.city ? ' · ' + d.city : ''} · ${d.craneModel} · ${statusText[d.status]}`,
        kind: 'device',
        device: d,
      });
    }
  }
  for (const p of projects) {
    if (match(p.id, p.projectName, p.province, p.city ?? undefined, p.craneModel)) {
      out.push({
        key: 'p:' + p.id,
        label: `${p.projectName}`,
        hint: `${p.province}${p.city ? ' · ' + p.city : ''} · ${p.quantity} 台`,
        kind: 'project',
        projectId: p.id,
      });
    }
  }
  return out.slice(0, 40);
});

function openSearch() {
  open.value = true;
  query.value = '';
  selectedIndex.value = 0;
}

function close() {
  open.value = false;
  query.value = '';
}

function choose(result: Result) {
  if (result.kind === 'device' && result.device) {
    window.dispatchEvent(
      new CustomEvent('locate-asset', { detail: { deviceId: result.device.deviceId } })
    );
  } else if (result.kind === 'project' && result.projectId) {
    window.dispatchEvent(
      new CustomEvent('locate-project', { detail: { projectId: result.projectId } })
    );
  }
  void router.push('/overview');
  close();
}

function onKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    openSearch();
    return;
  }
  if (!open.value) return;
  if (e.key === 'Escape') {
    close();
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    selectedIndex.value = Math.min(selectedIndex.value + 1, results.value.length - 1);
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    selectedIndex.value = Math.max(selectedIndex.value - 1, 0);
  } else if (e.key === 'Enter') {
    const hit = results.value[selectedIndex.value];
    if (hit) choose(hit);
  }
}

onMounted(() => window.addEventListener('keydown', onKey));
onUnmounted(() => window.removeEventListener('keydown', onKey));
</script>
<template>
  <div v-if="open" class="global-search-backdrop" @click="close">
    <div class="global-search" role="dialog" aria-label="全局搜索" @click.stop>
      <div class="global-search-input">
        <Search :size="16" />
        <input
          ref="input"
          v-model="query"
          placeholder="搜索资产编号 / 名称 / 项目 / 省市 / 型号 / 状态"
          aria-label="搜索资产、项目或省市"
        />
        <kbd>ESC</kbd>
      </div>
      <div class="global-search-results">
        <p v-if="!query.trim()" class="global-search-empty">输入关键词搜索行车资产、项目或省市…</p>
        <p v-else-if="!results.length" class="global-search-empty">无匹配结果</p>
        <button
          v-for="(r, i) in results"
          :key="r.key"
          class="global-search-item"
          :class="{ selected: i === selectedIndex }"
          :data-result-kind="r.kind"
          @mouseenter="selectedIndex = i"
          @click="choose(r)"
        >
          <span class="global-search-label">{{ r.label }}</span>
          <span class="global-search-hint">{{ r.hint }}</span>
          <CornerDownLeft v-if="i === selectedIndex" :size="13" />
        </button>
      </div>
    </div>
  </div>
</template>
