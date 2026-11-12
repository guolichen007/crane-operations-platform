<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ArrowUpRight, Box, Search } from 'lucide-vue-next';
import type { AssetDevice } from '../../../../contracts/typescript/types.gen';
import { listAssets, listProjects } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { loadGeoManifest } from '../scene/geo-manifest';
import { parseRegionParam, regionPathOf } from '../scene/region-path';
import { assetSource, lifecycleName } from '../cloud/inventory';
import { RUNTIME_LINK_LABEL } from '../cloud/format';
import StatusPill from '../components/StatusPill.vue';
import MiniTrend from '../components/MiniTrend.vue';
import RightInspector from '../components/RightInspector.vue';
import CraneTwin from '../components/CraneTwin.vue';
const route = useRoute(),
  search = ref(''),
  selected = ref<AssetDevice>();
const { data, error, loading } = useCloudQuery(async () => {
  const [projects, assets, manifest] = await Promise.all([
    listProjects(),
    listAssets(),
    loadGeoManifest().catch(() => null),
  ]);
  return { projects, assets, manifest };
});
// ?region= comes from the homepage Inventory KPI (same scope as the card): a
// project is in the region when the region is on its administrative path.
// The region only NARROWS what the authorized API returned; it can never widen
// it. Failure modes are explicit (H1-R1 closure): a malformed or unknown region,
// or an unavailable region manifest, is reported — never shown as "0 devices".
const rawRegion = computed(() => (typeof route.query.region === 'string' ? route.query.region : ''));
const region = computed(() => parseRegionParam(rawRegion.value));
const regionState = computed<'none' | 'malformed' | 'unavailable' | 'unknown' | 'ok'>(() => {
  if (!rawRegion.value) return 'none';
  if (region.value === null) return 'malformed';
  if (!data.value) return 'ok'; // still loading
  if (!data.value.manifest) return 'unavailable';
  return data.value.manifest[region.value] ? 'ok' : 'unknown';
});
const regionName = computed(() =>
  region.value && regionState.value === 'ok' ? data.value?.manifest?.[region.value]?.name ?? '' : ''
);
const inRegion = computed(() => {
  const code = region.value;
  const m = data.value?.manifest;
  if (regionState.value === 'none' || regionState.value === 'malformed') return () => true;
  if (!code || !m || regionState.value !== 'ok') return () => false;
  const ok = new Set(
    (data.value?.projects ?? [])
      .filter((p) => p.adminCode && (regionPathOf(p.adminCode, m) ?? []).includes(code))
      .map((p) => p.id)
  );
  return (projectId: string) => ok.has(projectId);
});
const regionBlocked = computed(() => regionState.value === 'unavailable' || regionState.value === 'unknown');
const assets = computed(
  () =>
    data.value?.assets.filter(
      (a) =>
        (!route.query.project || a.projectId === route.query.project) &&
        inRegion.value(a.projectId) &&
        (a.displayName + a.assetDeviceId).toLowerCase().includes(search.value.toLowerCase())
    ) ?? []
);
const names = computed(() => new Map(data.value?.projects.map((p) => [p.id, p.name])));
// H2-1D-4: arriving from a homepage KPI whose server predicate this list cannot reproduce (REAL 在线 /
// 当前 REAL 告警 / 无运行身份). The list is the same authorization + region, NOT the KPI's exact set.
const kpiNotes: Record<string, string> = {
  'real-connected': 'REAL 在线',
  'alarm-real': '当前 REAL 告警',
  'no-runtime': '无运行身份',
};
const kpiNote = computed(() => (typeof route.query.kpi === 'string' ? kpiNotes[route.query.kpi] ?? '' : ''));
</script>
<template>
  <main class="premium-workspace">
    <div class="premium-page-heading">
      <div>
        <p class="premium-eyebrow">EQUIPMENT FLEET / 设备中心</p>
        <h1>行车资产 <span>业务档案与运行身份独立</span></h1>
      </div>
      <RouterLink class="premium-link" to="/runtime"
        >查看 Runtime Shadow <ArrowUpRight :size="14"
      /></RouterLink>
    </div>
    <div class="premium-filterbar">
      <label class="premium-search"
        ><Search :size="14" /><input
          v-model="search"
          aria-label="搜索行车资产"
          placeholder="搜索设备名称 / 资产编号" /></label
      ><span data-testid="inventory-scope"
        >{{ regionBlocked ? '—' : assets.length }} 台资产<template v-if="regionName"> · 行政区 {{ regionName }}</template> ·
        规划模拟不代表现场在线</span
      >
    </div>
    <p v-if="kpiNote" class="kpi-not-exact" role="note" data-testid="inventory-kpi-note">
      来自首页「{{ kpiNote }}」：此处是同授权、同行政区的全部 Inventory 资产，<b>不是</b>该指标的精确筛选结果——
      指标由服务端按运行身份、连接新鲜度与资产状态统计，目前尚无相同口径的明细接口。
    </p>
    <p v-if="error" role="status">{{ error }}</p>
    <p v-else-if="loading">正在读取资产…</p>
    <p v-else-if="regionState === 'unavailable'" role="alert" data-testid="inventory-region-error">
      行政区数据不可用，无法按行政区 {{ region }} 筛选；未显示任何设备，请刷新重试或<RouterLink to="/devices">查看全部授权设备</RouterLink>。
    </p>
    <p v-else-if="regionState === 'unknown'" role="alert" data-testid="inventory-region-error">
      行政区 {{ region }} 不存在；<RouterLink to="/devices">查看全部授权设备</RouterLink>。
    </p>
    <p v-else-if="!assets.length && regionName" class="no-data" data-testid="inventory-region-empty">
      {{ regionName }} 范围内没有您有权查看的 Inventory 设备。
    </p>
    <p v-else-if="!assets.length" class="no-data">
      设备档案待补充 · 尚未接入Cloud；不代表离线故障。
    </p>
    <p v-if="regionState === 'malformed'" role="status" data-testid="inventory-region-ignored">
      无效的行政区参数「{{ rawRegion }}」，已忽略并显示全部授权设备。
    </p>
    <div class="device-center-layout">
      <div class="premium-device-list">
        <div class="device-list-header">
          <span>设备 / 所属项目</span><span>运行来源</span><span>运行趋势</span><span>资产参数</span
          ><span>详情</span>
        </div>
        <section
          v-for="a in assets"
          :key="a.assetDeviceId"
          class="premium-device-row"
          data-testid="inventory-asset"
        >
          <div class="device-identity">
            <span class="device-symbol"><Box :size="21" /></span>
            <div>
              <small>{{ a.assetDeviceId }} · {{ lifecycleName[a.lifecycle] }}</small>
              <h2>{{ a.displayName }}</h2>
              <p>{{ names.get(a.projectId) }}</p>
            </div>
          </div>
          <div>
            <StatusPill :label="assetSource(a)" /><small class="device-runtime-id">{{
              a.runtimeDeviceId || '尚无运行身份'
            }}</small>
          </div>
          <MiniTrend />
          <div class="device-row-spec">
            <p>型号：{{ a.craneModel ?? '待补充' }}</p>
            <p>{{ a.ratedCapacityT ?? '—' }} t / {{ a.spanM ?? '—' }} m</p>
          </div>
          <div class="device-row-actions">
            <button @click="selected = a">档案</button
            ><RouterLink
              v-if="a.runtimeDeviceId"
              :to="'/devices/' + encodeURIComponent(a.runtimeDeviceId)"
              >{{ RUNTIME_LINK_LABEL }} <ArrowUpRight :size="12"
            /></RouterLink>
          </div>
        </section>
      </div>
      <RightInspector v-if="selected" title="资产上下文" @close="selected = undefined"
        ><p class="premium-eyebrow">{{ selected.assetDeviceId }}</p>
        <h2 class="inspector-name">{{ selected.displayName }}</h2>
        <CraneTwin :key="selected.assetDeviceId" />
        <p class="inspector-copy">
          型号：{{ selected.craneModel ?? '待补充' }}<br />额定载重
          {{ selected.ratedCapacityT ?? '待补充' }} t<br />跨度
          {{ selected.spanM ?? '待补充' }} m<br />工作级别 {{ selected.workClass ?? '待补充'
          }}<br />抓斗 {{ selected.grabType ?? '待补充' }}
        </p>
        <StatusPill :label="assetSource(selected)" />
        <p class="inspector-copy">
          结构模型为通用示意，不代表设备实际尺寸。尚无历史趋势数据。
        </p></RightInspector
      >
    </div>
  </main>
</template>
