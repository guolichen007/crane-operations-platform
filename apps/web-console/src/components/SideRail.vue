<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import {
  Map,
  FolderKanban,
  BriefcaseBusiness,
  Container,
  MapPin,
  BookOpen,
  Gauge,
  TriangleAlert,
  ChartColumn,
  ChevronDown,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-vue-next';
import { productionAuth } from '../cloud/session';

type NavItem = { path: string; name: string; icon: typeof Map };
const route = useRoute();
// Top-level business pages (one <a> each in the nav). The asset-ledger pages are a
// separate group below, not three more top-level items.
const primary: NavItem[] = [
  { path: '/overview', name: '数字沙盘', icon: Map },
  { path: '/portfolio', name: '项目组合', icon: FolderKanban },
  { path: '/projects', name: '项目', icon: BriefcaseBusiness },
  { path: '/assets', name: '资产', icon: Container },
  { path: '/runtime', name: '运营', icon: Gauge },
  { path: '/alarms', name: '告警', icon: TriangleAlert },
  ...(productionAuth ? [] : [{ path: '/analytics', name: '分析', icon: ChartColumn }] as NavItem[]),
];
// Asset-management sub-pages; every child is a registered route.
const assets: NavItem[] = [
  { path: '/assets', name: '资产台账', icon: Container },
  { path: '/locations', name: '位置中心', icon: MapPin },
  { path: '/catalog', name: '设备目录', icon: BookOpen },
];

const STORAGE_KEY = 'crane.rail.collapsed';
const collapsed = ref(localStorage.getItem(STORAGE_KEY) === '1');
watch(collapsed, (v) => localStorage.setItem(STORAGE_KEY, v ? '1' : '0'));

// The group is active whenever one of its pages is open, and expands by default.
const inAssets = computed(() => assets.some((a) => route.path === a.path || route.path.startsWith(a.path + '/')));
const assetsOpen = ref(inAssets.value || localStorage.getItem('crane.rail.assetsOpen') !== '0');
watch(assetsOpen, (v) => localStorage.setItem('crane.rail.assetsOpen', v ? '1' : '0'));
watch(inAssets, (v) => { if (v) assetsOpen.value = true; });
</script>
<template>
  <aside class="side-rail" :class="{ collapsed }" data-testid="side-rail">
    <div class="rail-head">
      <span v-if="!collapsed" class="rail-brand">OPS 工作台</span>
      <span v-else class="rail-index">OPS</span>
      <button
        class="rail-toggle"
        :aria-label="collapsed ? '展开侧栏' : '折叠侧栏'"
        :title="collapsed ? '展开侧栏' : '折叠侧栏'"
        @click="collapsed = !collapsed"
      >
        <PanelLeftOpen v-if="collapsed" :size="16" />
        <PanelLeftClose v-else :size="16" />
      </button>
    </div>
    <nav aria-label="业务导航" class="rail-nav">
      <RouterLink
        v-for="n in primary"
        :key="n.name"
        :to="n.path"
        active-class="active"
        :title="n.name"
        ><component :is="n.icon" :size="19" :stroke-width="1.7" /><span>{{ n.name }}</span></RouterLink
      >
    </nav>
    <!-- Asset-management group: separate from the top-level nav so the rail
         link count stays stable; children are grouped (ledger / locations / catalog). -->
    <div class="rail-assets" :class="{ collapsed }" data-testid="rail-assets">
      <button
        class="rail-group-toggle"
        :aria-expanded="assetsOpen && !collapsed"
        aria-label="资产与设备"
        :title="'资产与设备'"
        @click="assetsOpen = !assetsOpen"
      >
        <Container :size="19" :stroke-width="1.7" /><span v-if="!collapsed">资产与设备</span
        ><ChevronDown v-if="!collapsed" :size="14" :class="{ flipped: !assetsOpen }" />
      </button>
      <div v-if="assetsOpen && !collapsed" class="rail-sub" role="group" aria-label="资产与设备子菜单">
        <RouterLink v-for="a in assets" :key="a.path" :to="a.path" active-class="active" :title="a.name"
          ><component :is="a.icon" :size="16" :stroke-width="1.7" /><span>{{ a.name }}</span></RouterLink
        >
      </div>
    </div>
    <div class="rail-foot"><span class="rail-version-dot"></span><small v-if="!collapsed">A1.1</small></div>
  </aside>
</template>
