<script setup lang="ts">
import { ref, watch } from 'vue';
import {
  Map,
  FolderKanban,
  BriefcaseBusiness,
  Container,
  Gauge,
  TriangleAlert,
  ChartColumn,
  Star,
  Clock,
  Settings,
  CircleHelp,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-vue-next';

type NavItem = { path: string; name: string; icon: typeof Map };
const primary: NavItem[] = [
  { path: '/overview', name: '数字沙盘', icon: Map },
  { path: '/portfolio', name: '项目组合', icon: FolderKanban },
  { path: '/projects', name: '项目', icon: BriefcaseBusiness },
  { path: '/assets', name: '资产', icon: Container },
  { path: '/runtime', name: '运营', icon: Gauge },
  { path: '/alarms', name: '告警', icon: TriangleAlert },
  { path: '/analytics', name: '分析', icon: ChartColumn },
];
const recent: NavItem[] = [
  { path: '/overview', name: '收藏', icon: Star },
  { path: '/overview', name: '最近', icon: Clock },
];
const meta: NavItem[] = [
  { path: '/overview', name: '设置', icon: Settings },
  { path: '/overview', name: '帮助', icon: CircleHelp },
];

const STORAGE_KEY = 'crane.rail.collapsed';
const collapsed = ref(localStorage.getItem(STORAGE_KEY) === '1');
watch(collapsed, (v) => localStorage.setItem(STORAGE_KEY, v ? '1' : '0'));
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
        ><component :is="n.icon" :size="19" :stroke-width="1.7" /><span>{{
          n.name
        }}</span></RouterLink
      >
      <div class="rail-sep"></div>
      <RouterLink
        v-for="n in recent"
        :key="n.name"
        :to="n.path"
        active-class="active"
        :title="n.name"
        ><component :is="n.icon" :size="18" :stroke-width="1.6" /><span>{{
          n.name
        }}</span></RouterLink
      >
      <div class="rail-sep"></div>
      <RouterLink
        v-for="n in meta"
        :key="n.name"
        :to="n.path"
        active-class="active"
        :title="n.name"
        ><component :is="n.icon" :size="18" :stroke-width="1.6" /><span>{{
          n.name
        }}</span></RouterLink
      >
    </nav>
    <div class="rail-foot"><CircleHelp :size="18" /><small v-if="!collapsed">A1.1</small></div>
  </aside>
</template>
