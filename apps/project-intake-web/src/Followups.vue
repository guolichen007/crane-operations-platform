<script setup lang="ts">
// V1.1 R4 follow-up center. Four disjoint windows in business time
// (Asia/Shanghai): 已逾期 / 今天 / 未来 7 天 / 未安排. A read model of the
// projects' next follow-up; recording a follow-up happens on the project
// (owner only), which updates the next plan and keeps all history.
import { ref, computed, watch, onBeforeUnmount } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { Search, ArrowUpRight } from '@lucide/vue';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import { recovery } from './reasons';
import { readFilters, scopeParams } from './view-state';
import ActionButton from './ActionButton.vue';
import ScopePicker, { type ScopeValue } from './ScopePicker.vue';

type Window = 'overdue' | 'today' | 'week' | 'unscheduled';
interface Center {
  asOf: string;
  businessDay: { timezone: string; todayStart: string; tomorrowStart: string; weekEnd: string };
  window: Window;
  counts: Record<Window, number>;
  items: Project[];
  total: number;
}
const app = useApp(),
  router = useRouter(),
  route = useRoute();
// Old links (/followups?window=upcoming) keep working: upcoming = today + week.
const legacy: Record<string, Window> = { upcoming: 'week' };
const initialWindow = String(route.query.window || '');
const win = ref<Window>((['overdue', 'today', 'week', 'unscheduled'] as string[]).includes(initialWindow) ? (initialWindow as Window) : legacy[initialWindow] ?? (initialWindow === 'overdue' ? 'overdue' : 'today'));
const initial = readFilters(route.query);
const sel = ref<ScopeValue>({ scope: initial.scope, memberId: initial.memberId, teamId: initial.teamId }),
  q = ref(initial.q),
  page = ref(initial.page);
const data = ref<Center | null>(null),
  loading = ref(false),
  failure = ref('');
let seq = 0;
async function load() {
  const n = ++seq;
  loading.value = true;
  failure.value = '';
  if ((sel.value.scope === 'member' && !sel.value.memberId) || (sel.value.scope === 'team' && !sel.value.teamId && app.user?.role !== 'baobei_manager')) {
    data.value = null;
    failure.value = sel.value.scope === 'member' ? '请选择成员' : '请选择团队';
    loading.value = false;
    return;
  }
  const params = new URLSearchParams({ window: win.value, page: String(page.value), pageSize: '20', ...scopeParams(sel.value) });
  if (q.value.trim()) params.set('q', q.value.trim());
  try {
    const b = await app.api<Center>('/api/v1/followups?' + params);
    if (n !== seq) return;
    data.value = b;
  } catch (e) {
    if (n !== seq) return;
    failure.value = recovery(e).message;
  } finally {
    if (n === seq) loading.value = false;
  }
}
const ownPath = route.path;
function syncUrl() {
  if (route.path !== ownPath) return;
  const query: Record<string, string> = { window: win.value, ...scopeParams(sel.value) };
  if (q.value.trim()) query.q = q.value.trim();
  if (page.value > 1) query.page = String(page.value);
  void router.replace({ query });
}
let timer: ReturnType<typeof setTimeout>;
watch([win, sel, q], () => {
  page.value = 1;
  clearTimeout(timer);
  timer = setTimeout(() => {
    syncUrl();
    void load();
  }, 200);
}, { deep: true });
watch(page, () => {
  syncUrl();
  void load();
});
onBeforeUnmount(() => clearTimeout(timer));
void load();

const tabs: { key: Window; label: string; note: string }[] = [
  { key: 'overdue', label: '已逾期', note: '计划跟进时间已过，尚未登记新的跟进。' },
  { key: 'today', label: '今天', note: '今天（北京时间）还要跟进的项目。' },
  { key: 'week', label: '未来 7 天', note: '明天起 7 天内计划跟进，不含今天与已逾期。' },
  { key: 'unscheduled', label: '未安排', note: '进行中的项目还没有安排下一次跟进，不会产生提醒。' },
];
const note = computed(() => tabs.find((t) => t.key === win.value)?.note ?? '');
const daysLate = (p: Project) => {
  if (!p.nextFollowupAt || !data.value) return 0;
  const start = Date.parse(data.value.businessDay.todayStart);
  const at = Date.parse(p.nextFollowupAt);
  return at >= start ? 0 : Math.ceil((start - at) / 86400000);
};
const owner = (p: Project) => (p.ownerSubject === app.user?.subject ? '我' : p.ownerName || '姓名待核对');
const here = () => router.resolve({ path: route.path, query: route.query }).fullPath;
function open(p: Project, record = false) {
  return router.push({ path: '/projects/' + p.id, query: { from: here(), ...(record ? { followup: '1' } : {}) } });
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>跟进中心 <small v-if="data">{{ data.total }}</small></h1>
      <p class="muted">只包含进行中的项目（不含已成交、已失单、已拒绝和回收站）<template v-if="data"> · 统计时间 {{ when(data.asOf) }}（北京时间）</template></p>
    </div>
  </div>
  <ScopePicker v-model="sel" />
  <div class="segmented followup-tabs" role="tablist" aria-label="跟进时间窗口">
    <ActionButton
      v-for="t in tabs"
      :key="t.key"
      action-id="followup.window"
      role="tab"
      :aria-selected="win === t.key"
      :class="{ selected: win === t.key, late: t.key === 'overdue' && (data?.counts.overdue ?? 0) > 0 }"
      :run="() => { win = t.key; }"
    >
      {{ t.label }}<span class="count">{{ data ? data.counts[t.key] : '…' }}</span>
    </ActionButton>
  </div>
  <p class="muted window-note">{{ note }}</p>
  <div class="filterbar">
    <label class="search-field"><Search :size="16" aria-hidden="true" /><input v-model="q" aria-label="搜索报备" placeholder="客户、项目或报备编号" /></label>
  </div>
  <p v-if="loading" class="muted" role="status">正在加载跟进…</p>
  <div v-if="failure" class="form-alert" role="alert">{{ failure }} <ActionButton action-id="followup.reload" :run="load">重新加载</ActionButton></div>
  <template v-else-if="data">
    <ol class="followup-list" :aria-label="tabs.find((t) => t.key === win)?.label + '的跟进'">
      <li v-for="p in data.items" :key="p.id" :class="['followup-item', { late: daysLate(p) > 0 || win === 'overdue' }]">
        <div class="followup-when">
          <strong v-if="p.nextFollowupAt">{{ when(p.nextFollowupAt) }}</strong><strong v-else>未安排</strong>
          <small v-if="win === 'overdue'" class="late-text">逾期 {{ Math.max(daysLate(p), 1) }} 天</small>
        </div>
        <div class="followup-main">
          <span class="mono muted">{{ p.registrationNo }}</span>
          <strong class="clamp">{{ p.projectName || '未命名草稿' }}</strong>
          <span class="clamp muted">{{ p.customer || '待填写客户' }} · {{ stages[p.salesStage] }} · <span class="badge">{{ statuses[p.intakeStatus] }}</span></span>
          <span class="clamp">下一步：{{ p.nextAction || '尚未记录' }}</span>
          <small class="muted">负责人 {{ owner(p) }} · 最近跟进 {{ p.lastActivityAt ? when(p.lastActivityAt) : '暂无记录' }}</small>
        </div>
        <div class="followup-actions">
          <ActionButton v-if="p.actions?.activities?.allowed" action-id="followup.record" primary :run="() => open(p, true)">登记跟进</ActionButton>
          <ActionButton action-id="project.detail" :run="() => open(p)" :aria-label="'打开 ' + (p.projectName || '项目')">打开 <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
      </li>
    </ol>
    <div v-if="!data.items.length" class="empty">
      <h2>{{ ({ overdue: '没有逾期的跟进', today: '今天没有待跟进项目', week: '未来 7 天没有安排跟进', unscheduled: '进行中的项目都已安排下一次跟进' })[win] }}</h2>
    </div>
    <div v-if="data.total > 20" class="pagination">
      <span class="muted">共 {{ data.total }} 项 · 第 {{ page }} 页</span><ActionButton action-id="project.page-prev" :disabled="page === 1 || loading" reason="已经是第一页" :run="() => { page--; }">上一页</ActionButton><ActionButton action-id="project.page-next" :disabled="page * 20 >= data.total || loading" reason="没有下一页" :run="() => { page++; }">下一页</ActionButton>
    </div>
  </template>
</template>
