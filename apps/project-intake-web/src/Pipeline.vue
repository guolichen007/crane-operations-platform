<script setup lang="ts">
// G3-3A real sales-stage pipeline. Column totals come from GET /stage-summary
// (server aggregation over the whole scope); each column pages independently
// through GET /projects?stage=…. Nothing here counts the loaded cards.
import { ref, reactive, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import { useRouter } from 'vue-router';
import { Search, RefreshCw, ArrowUpRight } from '@lucide/vue';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import { recovery } from './reasons';
import { stageCodes, nextStages, followupState, followupLabels, filterQuery, columnQuery, type PipelineFilters } from './pipeline';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';

const pageSize = 10;
const app = useApp(),
  router = useRouter();
const scopeNote = computed(
  () =>
    ({
      baobei_sales: '范围：你负责的项目',
      baobei_manager: '范围：本团队已分配项目',
      baobei_director: '范围：全部已分配项目',
      baobei_admin: '范围：全部项目，含待分配草稿',
    })[app.user?.role || ''] || ''
);

// Base filters (apply to the summary and to every column).
const q = ref(''),
  status = ref(''),
  province = ref(''),
  after = ref('');
const filters = computed<PipelineFilters>(() => ({
  q: q.value.trim(),
  status: status.value,
  province: province.value.trim(),
  updatedAfter: after.value ? new Date(after.value + 'T00:00:00').toISOString() : '',
}));

interface Column {
  items: Project[];
  page: number; // last loaded page
  total: number | null; // server total from the column's own list request
  state: 'idle' | 'loading' | 'ready' | 'error';
  failure: string;
  seq: number;
}
const blank = (): Column => ({ items: [], page: 0, total: null, state: 'idle', failure: '', seq: 0 });
const columns = reactive<Record<string, Column>>(Object.fromEntries(stageCodes.map((c) => [c, blank()])));
const summary = ref<{ asOf: string; total: number; counts: Record<string, number>; unclassified: number } | null>(null);
const summaryState = ref<'loading' | 'ready' | 'error'>('loading'),
  summaryFailure = ref(''),
  mobileStage = ref('L0_LEAD');
// Every filter change starts a new generation; responses of older generations
// are discarded so a slow request can never overwrite newer results.
let generation = 0,
  summarySeq = 0;

async function loadSummary() {
  const g = generation,
    seq = ++summarySeq;
  summaryState.value = 'loading';
  summaryFailure.value = '';
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
async function loadColumn(code: string, more = false) {
  const col = columns[code];
  if (col.state === 'loading') return;
  const g = generation,
    seq = ++col.seq,
    page = more ? col.page + 1 : 1;
  col.state = 'loading';
  col.failure = '';
  try {
    const b = await app.api<{ items: Project[]; total: number }>('/api/v1/projects?' + columnQuery(filters.value, code, page, pageSize));
    if (g !== generation || seq !== col.seq) return;
    // Projects can move between requests; never show one card twice.
    const known = new Set(more ? col.items.map((p) => p.id) : []);
    col.items = more ? [...col.items, ...b.items.filter((p) => !known.has(p.id))] : b.items;
    col.page = page;
    col.total = b.total;
    col.state = 'ready';
  } catch (e) {
    if (g !== generation || seq !== col.seq) return;
    col.failure = recovery(e).message;
    col.state = 'error';
  }
}
const visible = new Set<string>();
function reload() {
  generation++;
  // Counts of the previous filter must not stay on screen next to columns of
  // the new filter: show "…" until the matching summary arrives.
  summary.value = null;
  for (const code of stageCodes) Object.assign(columns[code], blank(), { seq: columns[code].seq + 1 });
  void loadSummary();
  // Only columns on screen (or the selected mobile stage) load right away.
  for (const code of visible) void loadColumn(code);
  if (isMobile.value) void loadColumn(mobileStage.value);
}
let timer: ReturnType<typeof setTimeout>;
watch(filters, () => {
  clearTimeout(timer);
  timer = setTimeout(reload, 250);
}, { deep: true });

// Lazy column loading on desktop/tablet: a column loads when it scrolls into view.
const board = ref<HTMLElement | null>(null);
const isMobile = ref(false);
let observer: IntersectionObserver | null = null,
  media: MediaQueryList | null = null;
const onMedia = () => {
  isMobile.value = !!media?.matches;
  if (isMobile.value && columns[mobileStage.value].state === 'idle') void loadColumn(mobileStage.value);
};
onMounted(async () => {
  media = window.matchMedia('(max-width: 700px)');
  media.addEventListener('change', onMedia);
  isMobile.value = media.matches;
  void loadSummary();
  if (isMobile.value) void loadColumn(mobileStage.value);
  await nextTick();
  observer = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const code = (e.target as HTMLElement).dataset.stage!;
        if (e.isIntersecting) {
          visible.add(code);
          if (columns[code].state === 'idle') void loadColumn(code);
        } else visible.delete(code);
      }
    },
    { root: board.value, rootMargin: '0px 200px 0px 200px' }
  );
  board.value?.querySelectorAll<HTMLElement>('[data-stage]').forEach((el) => observer!.observe(el));
});
onBeforeUnmount(() => {
  observer?.disconnect();
  media?.removeEventListener('change', onMedia);
  clearTimeout(timer);
});
// Desktop/tablet stage strip: real counts for all 12 stages; scrolls the
// board horizontally to the chosen column (which then loads lazily).
function jump(code: string) {
  const el = board.value?.querySelector<HTMLElement>(`[data-stage="${code}"]`);
  if (!el || !board.value) return;
  board.value.scrollTo({ left: el.offsetLeft - board.value.offsetLeft - 8, behavior: 'smooth' });
  if (columns[code].state === 'idle') void loadColumn(code);
}
function chooseMobile(code: string) {
  mobileStage.value = code;
  if (columns[code].state === 'idle') void loadColumn(code);
}

const countOf = (code: string) => (summary.value ? summary.value.counts[code] ?? 0 : null);
// Totals of the summary and of a column come from separate requests; if data
// changed in between they can differ. Say so instead of pretending.
const drifted = computed(() =>
  summaryState.value === 'ready' &&
  stageCodes.some((c) => columns[c].state === 'ready' && columns[c].total !== null && summary.value && columns[c].total !== summary.value.counts[c])
);
const owner = (p: Project) => (!p.ownerSubject ? '待分配' : p.ownerSubject === app.user?.subject ? '我' : p.ownerName || '姓名待核对');
const followup = (p: Project) => followupState(p.nextFollowupAt, summary.value?.asOf || '');
const asOfText = computed(() => (summary.value ? when(summary.value.asOf) : ''));
const hasFilters = computed(() => !!(q.value || status.value || province.value || after.value));

// Stage change through the existing stage-change API (reason required).
const moving = ref<Project | null>(null),
  target = ref(''),
  moveReason = ref(''),
  moveFailure = ref('');
async function openMove(p: Project) {
  moveFailure.value = '';
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
async function confirmMove() {
  const p = moving.value;
  if (!p) return;
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
      } catch {
        /* keep the dialog; the error above is shown */
      }
    }
    throw new Error(moveFailure.value);
  }
  moving.value = null;
  app.toast(`已从“${stages[from]}”推进到“${stages[target.value]}”，操作已记录`);
  // The summary and both affected columns reload from the server; other
  // columns keep their pages (an in-flight request of theirs stays valid).
  void loadSummary();
  for (const code of [from, target.value]) {
    Object.assign(columns[code], blank(), { seq: columns[code].seq + 1 });
    if (visible.has(code) || (isMobile.value && code === mobileStage.value)) void loadColumn(code);
  }
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>项目管道 <small v-if="summary">{{ summary.total }}</small></h1>
      <p class="muted">
        {{ scopeNote }} · 按销售阶段分栏，数量为服务端全量统计<template v-if="asOfText"> · 统计时间 {{ asOfText }}</template>
      </p>
    </div>
    <div class="actions">
      <ActionButton action-id="pipeline.refresh" :run="reload"><RefreshCw :size="15" aria-hidden="true" />刷新</ActionButton>
      <ActionButton action-id="nav.open" :run="() => router.push('/projects')">列表视图</ActionButton>
    </div>
  </div>
  <div class="filterbar pipeline-filters">
    <label class="search-field"><Search :size="16" aria-hidden="true" /><input v-model="q" aria-label="搜索报备" placeholder="客户、项目或报备编号" /></label>
    <select v-model="status" aria-label="报备状态筛选">
      <option value="">全部报备状态</option>
      <option v-for="(label, key) in statuses" :key="key" :value="key">{{ label }}</option>
    </select>
    <input v-model="province" aria-label="省份筛选" placeholder="省份" />
    <input v-model="after" type="date" aria-label="更新时间起" />
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

  <!-- Mobile: stage list + one stage at a time. -->
  <div v-if="isMobile" class="pipeline-mobile">
    <label class="stage-picker">销售阶段<select :value="mobileStage" aria-label="选择销售阶段" @change="chooseMobile(($event.target as HTMLSelectElement).value)">
      <option v-for="code in stageCodes" :key="code" :value="code">{{ stages[code] }}（{{ countOf(code) ?? '…' }}）</option>
    </select></label>
  </div>

  <nav v-if="!isMobile" class="stage-strip" aria-label="阶段分布">
    <ActionButton v-for="code in stageCodes" :key="code" action-id="pipeline.jump" :class="{ empty: countOf(code) === 0 }" :run="() => jump(code)">
      <span>{{ stages[code] }}</span><strong>{{ countOf(code) ?? '…' }}</strong>
    </ActionButton>
  </nav>
  <div ref="board" :class="['pipeline-board', { mobile: isMobile }]">
    <section
      v-for="code in stageCodes"
      v-show="!isMobile || code === mobileStage"
      :key="code"
      :data-stage="code"
      class="stage-column"
      :aria-label="stages[code] + ' 阶段'"
    >
      <header class="stage-head">
        <h2>{{ stages[code] }}</h2>
        <span class="stage-count" :aria-label="'共 ' + (countOf(code) ?? '加载中') + ' 项'">{{ countOf(code) ?? '…' }}</span>
      </header>
      <p class="stage-loaded muted">
        <template v-if="columns[code].total">已加载 {{ columns[code].items.length }} / 共 {{ columns[code].total }}</template>
        <template v-else-if="columns[code].state === 'idle'">滚动到此处时加载</template>
        <template v-else>&nbsp;</template>
      </p>
      <div class="stage-cards">
        <article v-for="p in columns[code].items" :key="p.id" class="stage-card">
          <ActionButton action-id="project.detail" class="stage-card-main" :run="() => router.push('/projects/' + p.id)">
            <strong class="clamp">{{ p.projectName || '未命名草稿' }}</strong>
            <span class="mono">{{ p.registrationNo }}</span>
            <span class="clamp muted">{{ p.customer || '待填写客户' }}</span>
            <span class="stage-card-meta"><span :class="['badge', p.intakeStatus.toLowerCase()]">报备：{{ statuses[p.intakeStatus] }}</span><span :class="{ unassigned: !p.ownerSubject }">{{ owner(p) }}</span></span>
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
      </div>
      <p v-if="columns[code].state === 'loading'" class="muted" role="status">正在加载…</p>
      <div v-else-if="columns[code].state === 'error'" class="form-alert" role="alert">
        {{ columns[code].failure }} <ActionButton action-id="pipeline.column-retry" :run="() => loadColumn(code, columns[code].page > 0)">重试本栏</ActionButton>
      </div>
      <p v-else-if="columns[code].state === 'ready' && !columns[code].items.length" class="muted stage-empty">本阶段暂无项目</p>
      <ActionButton
        v-if="columns[code].state === 'ready' && columns[code].total !== null && columns[code].items.length < columns[code].total"
        action-id="pipeline.load-more"
        class="stage-more"
        :run="() => loadColumn(code, true)"
      >
        加载更多（还有 {{ columns[code].total - columns[code].items.length }} 项）
      </ActionButton>
    </section>
  </div>

  <Modal :open="!!moving" title="推进销售阶段" description="阶段变更会记录原因、操作人和时间；服务端会再次校验是否允许。" @update:open="closeMove">
    <template v-if="moving">
      <p><strong>{{ moving.projectName || '未命名草稿' }}</strong> <span class="mono muted">{{ moving.registrationNo }}</span></p>
      <p class="muted">当前阶段：{{ stages[moving.salesStage] }} · 报备状态：{{ statuses[moving.intakeStatus] }}</p>
      <p v-if="moveFailure" class="form-alert" role="alert">{{ moveFailure }}</p>
      <p v-if="moveHint && !moveHint.allowed" class="form-alert" role="alert">当前不能修改阶段。</p>
      <label>目标阶段<select v-model="target" name="targetStage">
        <option value="">请选择</option>
        <option v-for="s in moveTargets" :key="s" :value="s">{{ stages[s] }}</option>
      </select></label>
      <p v-if="!moveTargets.length" class="muted">该项目当前没有可推进的阶段。</p>
      <label>变更原因 *<textarea v-model="moveReason" name="stageReason" maxlength="1000" rows="3"></textarea></label>
      <div class="actions modal-actions">
        <ActionButton action-id="dialog.cancel" :run="closeMove">取消</ActionButton><ActionButton
          action-id="pipeline.stage-confirm"
          primary
          :disabled="!target || !moveReason.trim() || !moveHint?.allowed"
          :reason="!target ? '请选择目标阶段' : '请填写变更原因'"
          :run="confirmMove"
        >
          确认推进
        </ActionButton>
      </div>
    </template>
  </Modal>
</template>
