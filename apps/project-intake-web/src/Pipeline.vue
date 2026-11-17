<script setup lang="ts">
// Project center, board view. V1.1 R3: six display groups by default (each can
// be narrowed to one of its original stages), the original 12 stage columns on
// request (layout=stages). Counts come from GET /stage-summary (server
// aggregation over the whole selection); every column pages independently
// through GET /projects?group=… or ?stage=…. Groups are display only: stored
// stage codes, the stage machine and history never change.
import { ref, reactive, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { Search, RefreshCw, ArrowUpRight, List, Columns3, ChevronRight } from '@lucide/vue';
import { readFilters, writeFilters, toList, scopeOf } from './view-state';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import { recovery } from './reasons';
import { stageCodes, stageGroups, groupOf, nextStages, followupState, followupLabels, filterQuery, columnQuery, type PipelineFilters } from './pipeline';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import ScopePicker, { type ScopeValue } from './ScopePicker.vue';

const pageSize = 10;
const app = useApp(),
  router = useRouter(),
  route = useRoute();
const scopeNote = computed(
  () =>
    ({
      baobei_sales: '范围：你负责的项目',
      baobei_manager: '范围：本团队已分配项目（含你本人的项目）',
      baobei_director: '范围：全部已分配项目',
      baobei_admin: '范围：全部项目，含待分配草稿',
    })[app.user?.role || ''] || ''
);

// Base filters (apply to the summary and to every column), kept in the URL.
const initial = readFilters(route.query);
const q = ref(initial.q),
  status = ref(initial.status),
  province = ref(initial.province),
  after = ref(initial.after),
  sel = ref<ScopeValue>({ scope: initial.scope, memberId: initial.memberId, teamId: initial.teamId });
const layout = ref<'groups' | 'stages'>(route.query.layout === 'stages' ? 'stages' : 'groups');
const compact = ref(route.query.density !== 'standard');
// `col` remembers the column (group or stage) so returning from a project lands
// on it; an old `stage=` link opens that stage.
const rawCol = typeof route.query.col === 'string' ? route.query.col : typeof route.query.stage === 'string' ? route.query.stage : '';
const initialCol = rawCol in stages || /^G[1-6]$/.test(rawCol) ? rawCol : '';
const filters = computed<PipelineFilters>(() => ({
  q: q.value.trim(),
  status: status.value,
  province: province.value.trim(),
  updatedAfter: after.value ? new Date(after.value + 'T00:00:00').toISOString() : '',
  ...(sel.value.scope ? { scope: sel.value.scope } : {}),
  ...(sel.value.scope === 'member' && sel.value.memberId ? { memberId: sel.value.memberId } : {}),
  ...(sel.value.scope === 'team' && sel.value.teamId ? { teamId: sel.value.teamId } : {}),
}));

interface Column {
  items: Project[];
  page: number; // last loaded page
  total: number | null; // server total from the column's own list request
  state: 'idle' | 'loading' | 'ready' | 'error';
  failure: string;
  seq: number;
  exhausted: boolean;
}
const blank = (): Column => ({ items: [], page: 0, total: null, state: 'idle', failure: '', seq: 0, exhausted: false });
const groupCodes = stageGroups.map((g) => g.code);
const allKeys = [...groupCodes, ...stageCodes];
const columns = reactive<Record<string, Column>>(Object.fromEntries(allKeys.map((c) => [c, blank()])));
// Groups layout: the stage a group column is narrowed to ('' = whole group).
const sub = reactive<Record<string, string>>(Object.fromEntries(groupCodes.map((g) => [g, ''])));
// Empty groups start collapsed; the user can open one.
const opened = reactive<Record<string, boolean>>({});
if (initialCol && stageCodes.includes(initialCol) && layout.value === 'groups') {
  sub[groupOf(initialCol)] = initialCol;
  opened[groupOf(initialCol)] = true;
}
const keys = computed(() => (layout.value === 'groups' ? groupCodes : stageCodes));
const fetchKey = (key: string) => (layout.value === 'groups' ? sub[key] || key : key);
const label = (key: string) => (/^G/.test(key) ? stageGroups.find((g) => g.code === key)!.label : stages[key]);

const summary = ref<{ asOf: string; total: number; counts: Record<string, number>; unclassified: number } | null>(null);
const summaryState = ref<'loading' | 'ready' | 'error'>('loading'),
  summaryFailure = ref('');
const startKey = initialCol ? (layout.value === 'groups' && stageCodes.includes(initialCol) ? groupOf(initialCol) : initialCol) : '';
const mobileKey = ref(startKey && keys.value.includes(startKey) ? startKey : keys.value[0]),
  focusKey = ref(startKey);
// Every filter change starts a new generation; responses of older generations
// are discarded so a slow request can never overwrite newer results.
let generation = 0,
  summarySeq = 0;

const selectionIncomplete = computed(() => (sel.value.scope === 'member' && !sel.value.memberId) || (sel.value.scope === 'team' && !sel.value.teamId && app.user?.role !== 'baobei_manager'));
async function loadSummary() {
  const g = generation,
    seq = ++summarySeq;
  summaryState.value = 'loading';
  summaryFailure.value = '';
  if (selectionIncomplete.value) {
    summaryFailure.value = sel.value.scope === 'member' ? '请选择成员' : '请选择团队';
    summaryState.value = 'error';
    return;
  }
  try {
    const b = await app.api<{ asOf: string; total: number; stages: { code: string; count: number }[]; unclassified: number }>(
      '/api/v1/stage-summary?' + filterQuery(filters.value)
    );
    if (g !== generation || seq !== summarySeq) return;
    summary.value = { asOf: b.asOf, total: b.total, counts: Object.fromEntries(b.stages.map((s) => [s.code, s.count])), unclassified: b.unclassified };
    summaryState.value = 'ready';
  } catch (e) {
    if (g !== generation || seq !== summarySeq) return;
    summaryFailure.value = recovery(e).message;
    summaryState.value = 'error';
  }
}
async function loadColumn(key: string, more = false) {
  const col = columns[key];
  if (col.state === 'loading' || selectionIncomplete.value) return;
  const g = generation,
    seq = ++col.seq,
    page = more ? col.page + 1 : 1;
  col.state = 'loading';
  col.failure = '';
  try {
    const b = await app.api<{ items: Project[]; total: number }>('/api/v1/projects?' + columnQuery(filters.value, fetchKey(key), page, pageSize));
    if (g !== generation || seq !== col.seq) return;
    // Projects can move between requests; never show one card twice.
    const known = new Set(more ? col.items.map((p) => p.id) : []);
    const fresh = more ? b.items.filter((p) => !known.has(p.id)) : b.items;
    col.items = more ? [...col.items, ...fresh] : b.items;
    col.page = page;
    col.total = b.total;
    // Projects moved or were removed meanwhile: a page brought nothing new.
    col.exhausted = more && fresh.length === 0;
    col.state = 'ready';
  } catch (e) {
    if (g !== generation || seq !== col.seq) return;
    col.failure = recovery(e).message;
    col.state = 'error';
  }
}
function resetColumn(key: string) {
  Object.assign(columns[key], blank(), { seq: columns[key].seq + 1 });
}
function refreshColumn(key: string) {
  resetColumn(key);
  void loadSummary();
  void loadColumn(key);
}
const visible = new Set<string>();
function reload() {
  generation++;
  // Counts of the previous filter must not stay on screen next to columns of
  // the new filter: show "…" until the matching summary arrives.
  summary.value = null;
  for (const key of allKeys) resetColumn(key);
  void loadSummary();
  for (const key of visible) if (keys.value.includes(key) && !collapsed(key)) void loadColumn(key);
  if (isMobile.value) void loadColumn(mobileKey.value);
}
const shared = () => ({ q: q.value.trim(), status: status.value, province: province.value.trim(), after: after.value, ...sel.value });
const ownPath = route.path;
function viewQuery(col = focusKey.value) {
  return {
    ...writeFilters({ ...readFilters({}), ...shared() }),
    ...(layout.value === 'stages' ? { layout: 'stages' } : {}),
    ...(!compact.value ? { density: 'standard' } : {}),
    ...(col ? { col } : {}),
  };
}
function syncUrl() {
  if (route.path !== ownPath) return;
  const query = viewQuery();
  if (JSON.stringify(query) !== JSON.stringify(route.query)) void router.replace({ query });
}
let timer: ReturnType<typeof setTimeout>;
watch([filters], () => {
  clearTimeout(timer);
  timer = setTimeout(() => {
    syncUrl();
    reload();
  }, 250);
}, { deep: true });
const listLink = computed(() => ({ path: '/projects', query: toList({ ...readFilters({}), ...shared(), ...scopeOf(sel.value) }) }));
function openDetail(p: Project, key: string) {
  const col = layout.value === 'groups' && sub[key] ? sub[key] : key;
  focusKey.value = key;
  syncUrl();
  const from = router.resolve({ path: '/pipeline', query: viewQuery(col) }).fullPath;
  return router.push({ path: '/projects/' + p.id, query: { from } });
}

// Lazy column loading on desktop/tablet: a column loads when it scrolls into view.
const board = ref<HTMLElement | null>(null);
const isMobile = ref(false);
let observer: IntersectionObserver | null = null,
  media: MediaQueryList | null = null;
const onMedia = () => {
  isMobile.value = !!media?.matches;
  if (isMobile.value && columns[mobileKey.value].state === 'idle') void loadColumn(mobileKey.value);
};
function observe() {
  observer?.disconnect();
  visible.clear();
  board.value?.querySelectorAll<HTMLElement>('[data-key]').forEach((el) => observer!.observe(el));
}
onMounted(async () => {
  media = window.matchMedia('(max-width: 700px)');
  media.addEventListener('change', onMedia);
  isMobile.value = media.matches;
  void loadSummary();
  if (isMobile.value) void loadColumn(mobileKey.value);
  await nextTick();
  observer = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const key = (e.target as HTMLElement).dataset.key!;
        if (e.isIntersecting) {
          visible.add(key);
          if (columns[key].state === 'idle' && !collapsed(key)) void loadColumn(key);
        } else visible.delete(key);
      }
    },
    { root: board.value, rootMargin: '0px 200px 0px 200px' }
  );
  observe();
  // Returning from a project: bring its column back into view.
  if (focusKey.value && !isMobile.value) jump(focusKey.value, 'auto');
});
onBeforeUnmount(() => {
  observer?.disconnect();
  media?.removeEventListener('change', onMedia);
  clearTimeout(timer);
});
// Desktop/tablet strip: real counts; scrolls the board to the chosen column.
function jump(key: string, behavior: ScrollBehavior = 'smooth') {
  if (layout.value === 'groups' && stageCodes.includes(key)) {
    const g = groupOf(key);
    opened[g] = true;
    chooseSub(g, key);
    key = g;
  }
  if (collapsed(key)) opened[key] = true;
  focusKey.value = key;
  void nextTick(() => {
    const el = board.value?.querySelector<HTMLElement>(`[data-key="${key}"]`);
    if (!el || !board.value) return;
    board.value.scrollTo({ left: el.offsetLeft - board.value.offsetLeft - 8, behavior });
    if (columns[key].state === 'idle') void loadColumn(key);
  });
}
function chooseMobile(key: string) {
  mobileKey.value = key;
  focusKey.value = key;
  syncUrl();
  if (columns[key].state === 'idle') void loadColumn(key);
}
function chooseSub(g: string, stage: string) {
  if (sub[g] === stage) return;
  sub[g] = stage;
  resetColumn(g);
  void loadColumn(g);
}
async function setLayout(next: 'groups' | 'stages') {
  if (layout.value === next) return;
  layout.value = next;
  focusKey.value = '';
  mobileKey.value = keys.value[0];
  syncUrl();
  await nextTick();
  observe();
  if (isMobile.value) void loadColumn(mobileKey.value);
}
watch(compact, syncUrl);

const countOf = (key: string) => {
  if (!summary.value) return null;
  const s = /^G/.test(key) ? stageGroups.find((g) => g.code === key)!.stages : [key];
  return s.reduce((a, c) => a + (summary.value!.counts[c] ?? 0), 0);
};
// Count that matches the column's own list (a narrowed group shows its stage).
const listCount = (key: string) => countOf(layout.value === 'groups' && sub[key] ? sub[key] : key);
const collapsed = (key: string) => layout.value === 'groups' && countOf(key) === 0 && !opened[key];
// Totals of the summary and of a column come from separate requests; if data
// changed in between they can differ. Say so instead of pretending.
const drifted = computed(() =>
  summaryState.value === 'ready' &&
  keys.value.some((k) => columns[k].state === 'ready' && columns[k].total !== null && columns[k].total !== listCount(k))
);
const owner = (p: Project) => (!p.ownerSubject ? '待分配' : p.ownerSubject === app.user?.subject ? '我' : p.ownerName || '姓名待核对');
const followup = (p: Project) => followupState(p.nextFollowupAt, summary.value?.asOf || '');
const asOfText = computed(() => (summary.value ? when(summary.value.asOf) : ''));
const hasFilters = computed(() => !!(q.value || status.value || province.value || after.value));
const mobileOptions = computed(() => keys.value.map((k) => ({ key: k, label: label(k) })));

// Stage change through the existing stage-change API (reason required).
const moving = ref<Project | null>(null),
  target = ref(''),
  moveReason = ref(''),
  moveFailure = ref('');
async function openMove(p: Project) {
  moveFailure.value = '';
  moveErrors.value = {};
  target.value = '';
  moveReason.value = '';
  try {
    // Fresh copy: current version and server-computed actions.
    moving.value = await app.api<Project>('/api/v1/projects/' + p.id);
  } catch (e) {
    app.error = recovery(e).message;
  }
}
const moveTargets = computed(() => (moving.value ? nextStages(moving.value.salesStage, moving.value.intakeStatus) : []));
const moveHint = computed(() => moving.value?.actions?.['stage-change']);
async function closeMove() {
  if (moveReason.value.trim() && !(await app.confirmDiscard())) return;
  moving.value = null;
}
const moveErrors = ref<Record<string, string>>({});
async function confirmMove() {
  const p = moving.value;
  if (!p) return;
  const errors: Record<string, string> = {};
  if (!target.value) errors.targetStage = '请选择目标阶段';
  if (!moveReason.value.trim()) errors.stageReason = '请填写变更原因';
  moveErrors.value = errors;
  const first = Object.keys(errors)[0];
  if (first) {
    moveFailure.value = `还有 ${Object.keys(errors).length} 项需要填写：` + Object.values(errors).join('；');
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[name="${first}"]`)?.focus());
    return;
  }
  const from = p.salesStage;
  try {
    await app.api(`/api/v1/projects/${p.id}/stage-change`, 'POST', { version: p.version, salesStage: target.value, reason: moveReason.value });
  } catch (e) {
    const r = recovery(e);
    // The card stays in its column; input is kept; reload the copy on conflict.
    moveFailure.value = r.kind === 'conflict' ? r.message + '。已重新读取最新版本，请确认后再提交。' : r.message;
    if (r.kind === 'conflict') {
      try {
        moving.value = await app.api<Project>('/api/v1/projects/' + p.id);
        // The project may now be in another stage/status: a previously chosen
        // target must be confirmed again against the latest legal choices.
        if (!nextStages(moving.value.salesStage, moving.value.intakeStatus).includes(target.value)) {
          target.value = '';
          moveFailure.value += ' 项目当前阶段已变为“' + stages[moving.value.salesStage] + '”，请重新选择目标阶段。';
        }
      } catch {
        /* keep the dialog; the error above is shown */
      }
    }
    return;
  }
  moving.value = null;
  app.toast(`已从“${stages[from]}”推进到“${stages[target.value]}”，操作已记录`);
  // The summary and the affected columns reload from the server; other
  // columns keep their pages (an in-flight request of theirs stays valid).
  void loadSummary();
  const affected = layout.value === 'groups' ? [groupOf(from), groupOf(target.value)] : [from, target.value];
  for (const key of new Set(affected)) {
    resetColumn(key);
    if (visible.has(key) || (isMobile.value && key === mobileKey.value)) void loadColumn(key);
  }
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>项目中心 <small v-if="summary">{{ summary.total }}</small></h1>
      <p class="muted">
        {{ scopeNote }} · {{ layout === 'groups' ? '按六个阶段分组，可展开到原 12 个销售阶段' : '按 12 个销售阶段分栏' }}，数量为服务端全量统计<template v-if="asOfText"> · 统计时间 {{ asOfText }}</template>
      </p>
    </div>
    <div class="actions">
      <nav class="view-switch" aria-label="切换视图">
        <ActionButton action-id="view.list" :run="() => router.push(listLink)"><List :size="15" aria-hidden="true" />列表</ActionButton>
        <span class="view-current" aria-current="page"><Columns3 :size="15" aria-hidden="true" />管道</span>
      </nav>
      <ActionButton action-id="pipeline.refresh" :run="reload"><RefreshCw :size="15" aria-hidden="true" />刷新</ActionButton>
    </div>
  </div>
  <ScopePicker v-model="sel" />
  <div class="filterbar pipeline-filters">
    <label class="search-field"><Search :size="16" aria-hidden="true" /><input v-model="q" aria-label="搜索报备" placeholder="客户、项目或报备编号" /></label>
    <select v-model="status" aria-label="报备状态筛选">
      <option value="">全部报备状态</option>
      <option v-for="(text, key) in statuses" :key="key" :value="key">{{ text }}</option>
    </select>
    <input v-model="province" aria-label="省份筛选" placeholder="省份" />
    <input v-model="after" type="date" aria-label="更新时间起" />
  </div>
  <div class="pipeline-options">
    <div class="segmented" role="tablist" aria-label="管道分栏">
      <ActionButton action-id="pipeline.layout" role="tab" :aria-selected="layout === 'groups'" :class="{ selected: layout === 'groups' }" :run="() => setLayout('groups')">六组</ActionButton>
      <ActionButton action-id="pipeline.layout" role="tab" :aria-selected="layout === 'stages'" :class="{ selected: layout === 'stages' }" :run="() => setLayout('stages')">全部 12 个阶段</ActionButton>
    </div>
    <label class="density-toggle"><input v-model="compact" type="checkbox" />紧凑卡片</label>
  </div>
  <p class="muted pipeline-legend">
    阶段是销售进展；卡片上的标签是报备审核状态，两者相互独立。跟进标记：<span class="fu overdue">已逾期</span><span class="fu scheduled">已安排</span><span class="fu unscheduled">未安排</span>
  </p>
  <div v-if="summaryState === 'error'" class="form-alert" role="alert">
    无法加载阶段统计：{{ summaryFailure }} <ActionButton action-id="pipeline.refresh" :run="reload">重新加载</ActionButton>
  </div>
  <p v-if="summary && summary.unclassified" class="note" role="note">
    有 {{ summary.unclassified }} 个项目的销售阶段不在 12 个标准阶段内（历史数据），未显示在任何一栏，请联系管理员核对。
  </p>
  <p v-if="drifted" class="note pipeline-drift" role="status">
    数据在加载过程中有更新，部分栏的数量可能与统计不一致。<ActionButton action-id="pipeline.refresh" :run="reload">刷新管道</ActionButton>
  </p>
  <p v-if="summary && !summary.total && summaryState === 'ready'" class="empty muted">
    {{ hasFilters ? '没有符合筛选条件的项目，调整或清空筛选后再试。' : '范围内还没有项目。' }}
  </p>

  <!-- Mobile: one group (or stage) at a time. -->
  <div v-if="isMobile" class="pipeline-mobile">
    <label class="stage-picker">{{ layout === 'groups' ? '阶段分组' : '销售阶段' }}<select :value="mobileKey" :aria-label="layout === 'groups' ? '选择阶段分组' : '选择销售阶段'" @change="chooseMobile(($event.target as HTMLSelectElement).value)">
      <option v-for="o in mobileOptions" :key="o.key" :value="o.key">{{ o.label }}（{{ countOf(o.key) ?? '…' }}）</option>
    </select></label>
  </div>

  <nav v-if="!isMobile" class="stage-strip" aria-label="阶段分布">
    <ActionButton v-for="key in keys" :key="key" action-id="pipeline.jump" :class="{ empty: countOf(key) === 0 }" :run="() => jump(key)">
      <span>{{ label(key) }}</span><strong>{{ countOf(key) ?? '…' }}</strong>
    </ActionButton>
  </nav>
  <div ref="board" :class="['pipeline-board', { mobile: isMobile, grouped: layout === 'groups' }]">
    <section
      v-for="key in keys"
      v-show="!isMobile || key === mobileKey"
      :key="layout + key"
      :data-key="key"
      :data-stage="layout === 'stages' ? key : undefined"
      :data-group="layout === 'groups' ? key : undefined"
      :class="['stage-column', { collapsed: collapsed(key) && !isMobile }]"
      :aria-label="label(key) + (layout === 'groups' ? ' 分组' : ' 阶段')"
    >
      <header class="stage-head">
        <h2>{{ label(key) }}</h2>
        <span class="stage-count" :aria-label="'共 ' + (countOf(key) ?? '加载中') + ' 项'">{{ countOf(key) ?? '…' }}</span>
      </header>
      <template v-if="collapsed(key) && !isMobile">
        <ActionButton action-id="pipeline.group-open" class="stage-open" :aria-label="'展开 ' + label(key)" :run="() => { opened[key] = true; void loadColumn(key); }">
          暂无项目 <ChevronRight :size="14" aria-hidden="true" />
        </ActionButton>
      </template>
      <template v-else>
        <div v-if="layout === 'groups'" class="stage-subs" role="group" :aria-label="label(key) + ' 包含的阶段'">
          <ActionButton action-id="pipeline.sub-stage" :class="{ selected: !sub[key] }" :aria-pressed="!sub[key]" :run="() => chooseSub(key, '')">全部 {{ countOf(key) ?? '…' }}</ActionButton>
          <ActionButton
            v-for="s in stageGroups.find((g) => g.code === key)!.stages"
            :key="s"
            action-id="pipeline.sub-stage"
            :class="{ selected: sub[key] === s }"
            :aria-pressed="sub[key] === s"
            :run="() => chooseSub(key, s)"
          >
            {{ stages[s] }} {{ countOf(s) ?? '…' }}
          </ActionButton>
        </div>
        <p class="stage-loaded muted">
          <template v-if="columns[key].total">已加载 {{ columns[key].items.length }} / 共 {{ columns[key].total }}</template>
          <template v-else-if="columns[key].state === 'idle'">滚动到此处时加载</template>
          <template v-else>&nbsp;</template>
        </p>
        <div class="stage-cards" tabindex="-1">
          <article v-for="p in columns[key].items" :key="p.id" :class="['stage-card', { compact }]">
            <ActionButton action-id="project.detail" class="stage-card-main" :run="() => openDetail(p, key)">
              <strong class="clamp">{{ p.projectName || '未命名草稿' }}</strong>
              <span v-if="!compact" class="mono">{{ p.registrationNo }}</span>
              <span class="clamp muted">{{ p.customer || '待填写客户' }}<template v-if="layout === 'groups' && !sub[key]"> · {{ stages[p.salesStage] }}</template></span>
              <span class="stage-card-meta"><span v-if="!compact || p.intakeStatus !== 'APPROVED'" :class="['badge', p.intakeStatus.toLowerCase()]">报备：{{ statuses[p.intakeStatus] }}</span><span :class="{ unassigned: !p.ownerSubject }">{{ owner(p) }}</span></span>
              <span class="clamp">下一步：{{ p.nextAction || '尚未记录' }}</span>
              <span :class="['fu', followup(p)]">{{ followupLabels[followup(p)] }}<template v-if="p.nextFollowupAt"> · {{ when(p.nextFollowupAt) }}</template></span>
            </ActionButton>
            <ActionButton
              v-if="p.actions?.['stage-change']?.allowed"
              action-id="pipeline.stage-change"
              class="stage-card-move"
              :run="() => openMove(p)"
              :aria-label="'推进阶段：' + (p.projectName || '未命名草稿')"
            >
              推进阶段 <ArrowUpRight :size="14" aria-hidden="true" />
            </ActionButton>
          </article>
          <p v-if="columns[key].state === 'loading'" class="muted" role="status">正在加载…</p>
          <div v-else-if="columns[key].state === 'error'" class="form-alert" role="alert">
            {{ columns[key].failure }} <ActionButton action-id="pipeline.column-retry" :run="() => loadColumn(key, columns[key].page > 0)">重试本栏</ActionButton>
          </div>
          <p v-else-if="columns[key].state === 'ready' && !columns[key].items.length" class="muted stage-empty">本栏暂无项目</p>
          <p v-if="columns[key].state === 'ready' && columns[key].exhausted" class="note stage-exhausted" role="status">
            本栏数据已变化，继续加载没有新项目。<ActionButton action-id="pipeline.column-refresh" :run="() => refreshColumn(key)">刷新本栏</ActionButton>
          </p>
          <ActionButton
            v-if="columns[key].state === 'ready' && !columns[key].exhausted && columns[key].total !== null && columns[key].items.length < columns[key].total"
            action-id="pipeline.load-more"
            class="stage-more"
            :run="() => loadColumn(key, true)"
          >
            加载更多（还有 {{ columns[key].total - columns[key].items.length }} 项）
          </ActionButton>
        </div>
      </template>
    </section>
  </div>

  <Modal :open="!!moving" title="推进销售阶段" description="阶段变更会记录原因、操作人和时间；服务端会再次校验是否允许。" @update:open="closeMove">
    <template v-if="moving">
      <p><strong>{{ moving.projectName || '未命名草稿' }}</strong> <span class="mono muted">{{ moving.registrationNo }}</span></p>
      <p class="muted">当前阶段：{{ stages[moving.salesStage] }} · 报备状态：{{ statuses[moving.intakeStatus] }}</p>
      <p v-if="moveFailure" class="form-alert" role="alert">{{ moveFailure }}</p>
      <p v-if="moveHint && !moveHint.allowed" class="form-alert" role="alert">当前不能修改阶段。</p>
      <label :class="{ invalid: moveErrors.targetStage }">目标阶段 <span class="req" aria-hidden="true">*</span><select v-model="target" name="targetStage" aria-required="true" :aria-invalid="!!moveErrors.targetStage">
        <option value="">请选择</option>
        <option v-for="s in moveTargets" :key="s" :value="s">{{ stages[s] }}</option>
      </select><small v-if="moveErrors.targetStage" class="field-error">{{ moveErrors.targetStage }}</small></label>
      <p v-if="!moveTargets.length" class="muted">该项目当前没有可推进的阶段。</p>
      <label :class="{ invalid: moveErrors.stageReason }">变更原因 <span class="req" aria-hidden="true">*</span><textarea v-model="moveReason" name="stageReason" maxlength="1000" rows="3" aria-required="true" :aria-invalid="!!moveErrors.stageReason"></textarea><small v-if="moveErrors.stageReason" class="field-error">{{ moveErrors.stageReason }}</small></label>
      <div class="actions modal-actions">
        <ActionButton action-id="dialog.cancel" :run="closeMove">取消</ActionButton><ActionButton
          action-id="pipeline.stage-confirm"
          primary
          :disabled="!moveHint?.allowed || !moveTargets.length"
          reason="当前不能修改阶段"
          :run="confirmMove"
        >
          确认推进
        </ActionButton>
      </div>
    </template>
  </Modal>
</template>
