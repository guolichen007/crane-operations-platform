<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ArrowUpRight, Box, Search } from 'lucide-vue-next';
import type { AssetDevice } from '../../../../contracts/typescript/types.gen';
import { listAssets, listProjects } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { assetSource, lifecycleName } from '../cloud/inventory';
import StatusPill from '../components/StatusPill.vue';
import MiniTrend from '../components/MiniTrend.vue';
import RightInspector from '../components/RightInspector.vue';
import CraneTwin from '../components/CraneTwin.vue';
const route = useRoute(), search = ref(''), selected = ref<AssetDevice>();
const { data, error, loading } = useCloudQuery(async () => { const [projects, assets] = await Promise.all([listProjects(), listAssets()]); return { projects, assets }; });
const assets = computed(() => data.value?.assets.filter(a => (!route.query.project || a.projectId === route.query.project) && (a.displayName + a.assetDeviceId).toLowerCase().includes(search.value.toLowerCase())) ?? []);
const names = computed(() => new Map(data.value?.projects.map(p => [p.id, p.name])));
</script>
<template><main class="premium-workspace"><div class="premium-page-heading"><div><p class="premium-eyebrow">EQUIPMENT FLEET / 设备中心</p><h1>行车资产 <span>业务档案与运行身份独立</span></h1></div><RouterLink class="premium-link" to="/runtime">查看 Runtime Shadow <ArrowUpRight :size="14" /></RouterLink></div><div class="premium-filterbar"><label class="premium-search"><Search :size="14" /><input v-model="search" aria-label="搜索行车资产" placeholder="搜索设备名称 / 资产编号" /></label><span>{{ assets.length }} 台资产 · 规划模拟不代表现场在线</span></div><p v-if="error" role="status">{{ error }}</p><p v-else-if="loading">正在读取资产…</p><p v-else-if="!assets.length" class="no-data">设备档案待补充 · 尚未接入Cloud；不代表离线故障。</p><div class="device-center-layout"><div class="premium-device-list"><div class="device-list-header"><span>设备 / 所属项目</span><span>运行来源</span><span>运行趋势</span><span>资产参数</span><span>详情</span></div><section v-for="a in assets" :key="a.assetDeviceId" class="premium-device-row" data-testid="inventory-asset"><div class="device-identity"><span class="device-symbol"><Box :size="21" /></span><div><small>{{ a.assetDeviceId }} · {{ lifecycleName[a.lifecycle] }}</small><h2>{{ a.displayName }}</h2><p>{{ names.get(a.projectId) }}</p></div></div><div><StatusPill :label="assetSource(a)" /><small class="device-runtime-id">{{ a.runtimeDeviceId || '尚无运行身份' }}</small></div><MiniTrend /><div class="device-row-spec"><p>型号：{{ a.craneModel ?? '待补充' }}</p><p>{{ a.ratedCapacityT ?? '—' }} t / {{ a.spanM ?? '—' }} m</p></div><div class="device-row-actions"><button @click="selected = a">档案</button><RouterLink v-if="a.runtimeDeviceId" :to="'/devices/' + encodeURIComponent(a.runtimeDeviceId)">最近状态 / LIVE <ArrowUpRight :size="12" /></RouterLink></div></section></div><RightInspector v-if="selected" title="资产上下文" @close="selected = undefined"><p class="premium-eyebrow">{{ selected.assetDeviceId }}</p><h2 class="inspector-name">{{ selected.displayName }}</h2><CraneTwin :key="selected.assetDeviceId" /><p class="inspector-copy">型号：{{ selected.craneModel ?? '待补充' }}<br />额定载重 {{ selected.ratedCapacityT ?? '待补充' }} t<br />跨度 {{ selected.spanM ?? '待补充' }} m<br />工作级别 {{ selected.workClass ?? '待补充' }}<br />抓斗 {{ selected.grabType ?? '待补充' }}</p><StatusPill :label="assetSource(selected)" /><p class="inspector-copy">结构模型为通用示意，不代表设备实际尺寸。尚无历史趋势数据。</p></RightInspector></div></main></template>
