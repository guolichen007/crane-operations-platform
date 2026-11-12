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
// One navigation hierarchy: every business page appears exactly once. Asset
// management is a group (资产与设备) inside the same <nav>, between 项目 and 运营;
// there is no separate top-level 资产 link duplicating 资产台账.
const before: NavItem[] = [
  { path: '/overview', name: '数字沙盘', icon: Map },
  { path: '/portfolio', name: '项目组合', icon: FolderKanban },
  { path: '/projects', name: '项目', icon: BriefcaseBusiness },
];
const assets: NavItem[] = [
  { path: '/assets', name: '资产台账', icon: Container },
  { path: '/locations', name: '位置中心', icon: MapPin },
  { path: '/catalog', name: '设备目录', icon: BookOpen },
];
const after: NavItem[] = [
  { path: '/runtime', name: '运营', icon: Gauge },
  { path: '/alarms', name: '告警', icon: TriangleAlert },
  ...(productionAuth ? [] : [{ path: '/analytics', name: '分析', icon: ChartColumn }] as NavItem[]),
];

const STORAGE_KEY = 'crane.rail.collapsed';
const collapsedPref = ref(localStorage.getItem(STORAGE_KEY) === '1');
watch(collapsedPref, (v) => localStorage.setItem(STORAGE_KEY, v ? '1' : '0'));
// R5-UX-PERF B: on phones the rail is a fixed 56 px icon strip (CSS). The
// expanded group button and its text were drawn over the page content at
// 375 px, so phone widths always render the collapsed (icon) structure; the
// saved desktop preference is untouched.
const phoneQuery = typeof matchMedia === 'function' ? matchMedia('(max-width: 700px)') : null;
const phone = ref(!!phoneQuery?.matches);
phoneQuery?.addEventListener('change', (e) => (phone.value = e.matches));
const collapsed = computed({
  get: () => phone.value || collapsedPref.value,
  set: (v: boolean) => (collapsedPref.value = v),
});

// The group is active whenever one of its pages (or an asset detail) is open,
// and is expanded by default.
const inAssets = computed(() => assets.some((a) => route.path === a.path || route.path.startsWith(a.path + '/')));
const assetsOpen = ref(inAssets.value || localStorage.getItem('crane.rail.assetsOpen') !== '0');
watch(assetsOpen, (v) => localStorage.setItem('crane.rail.assetsOpen', v ? '1' : '0'));
watch(inAssets, (v) => {
  if (v) assetsOpen.value = true;
});
// In the collapsed rail there is no room for a disclosure; the three pages are
// shown as icon links (title + aria-label) so they stay reachable.
const showChildren = computed(() => collapsed.value || assetsOpen.value);
</script>
<template>
  <aside class="side-rail" :class="{ collapsed }" data-testid="side-rail">
    <div class="rail-head">
      <span v-if="!collapsed" class="rail-brand">OPS 工作台</span>
      <span v-else class="rail-index">OPS</span>
      <button
        v-if="!phone"
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
      <RouterLink v-for="n in before" :key="n.path" :to="n.path" active-class="active" :title="n.name" :aria-label="n.name"
        ><component :is="n.icon" :size="19" :stroke-width="1.7" /><span>{{ n.name }}</span></RouterLink
      >
      <div class="rail-assets" :class="{ collapsed, active: inAssets }" data-testid="rail-assets">
        <button
          v-if="!collapsed"
          type="button"
          class="rail-group-toggle"
          :aria-expanded="assetsOpen"
          aria-controls="rail-assets-menu"
          @click="assetsOpen = !assetsOpen"
        >
          <Container :size="19" :stroke-width="1.7" /><span>资产与设备</span
          ><ChevronDown :size="14" :class="{ flipped: !assetsOpen }" />
        </button>
        <div
          v-if="showChildren"
          id="rail-assets-menu"
          class="rail-sub"
          role="group"
          aria-label="资产与设备"
        >
          <RouterLink
            v-for="a in assets"
            :key="a.path"
            :to="a.path"
            active-class="active"
            :title="a.name"
            :aria-label="a.name"
            ><component :is="a.icon" :size="collapsed ? 19 : 16" :stroke-width="1.7" /><span>{{ a.name }}</span></RouterLink
          >
        </div>
      </div>
      <RouterLink v-for="n in after" :key="n.path" :to="n.path" active-class="active" :title="n.name" :aria-label="n.name"
        ><component :is="n.icon" :size="19" :stroke-width="1.7" /><span>{{ n.name }}</span></RouterLink
      >
    </nav>
    <div class="rail-foot"><span class="rail-version-dot"></span><small v-if="!collapsed">A1.1</small></div>
  </aside>
</template>
