<script setup lang="ts">
// G3-4-A0 four-role home. Two server aggregates only:
//   GET /api/v1/workspace-summary  — task counts, recent items, 7-day follow-ups
//   GET /api/v1/stage-summary      — 12 sales stages and the known estimated amount
// Both use the caller's projectScope on the server. Every number links to a
// list with the same server filter, so the list total equals the number.
import { ref, onMounted, computed } from 'vue';
import { useRouter } from 'vue-router';
import { ArrowUpRight, Plus, UserPlus, Users, Building2 } from '@lucide/vue';
import { useApp } from './store';
import { statuses, stages, when } from './model';
import { can, projectScopeLabel } from './navigation';
import { recovery } from './reasons';
import { yuan, sharePercent, type StageSummaryResponse } from './amount';
import ActionButton from './ActionButton.vue';

interface Item {
  id: string;
  registrationNo: string;
  projectName: string;
  customer: string;
  intakeStatus: string;
  salesStage: string;
  ownerName: string;
  updatedAt: string;
  nextFollowupAt: string | null;
  nextAction: string;
}
interface Summary {
  asOf: string;
  scope: string;
  counts: Record<string, number>;
  recent: Item[];
  followups: Item[];
}
const app = useApp(),
  router = useRouter(),
  data = ref<Summary | null>(null),
  state = ref<'loading' | 'ready' | 'error'>('loading'),
  failure = ref(''),
  forbidden = ref(false),
  stagesData = ref<StageSummaryResponse | null>(null),
  stageState = ref<'loading' | 'ready' | 'error'>('loading'),
  stageFailure = ref('');
let seq = 0,
  stageSeq = 0;
// The task summary and the stage summary load independently: a failing stage
// panel never hides the tasks, and a reload discards older responses.
async function load() {
  const n = ++seq;
  state.value = 'loading';
  try {
    const b = await app.api<Summary>('/api/v1/workspace-summary');
    if (n !== seq) return;
    data.value = b;
    state.value = 'ready';
  } catch (e) {
    if (n !== seq) return;
    const r = recovery(e);
    failure.value = r.message;
    forbidden.value = r.kind === 'forbidden';
    state.value = 'error';
  }
}
async function loadStages() {
  const n = ++stageSeq;
  stageState.value = 'loading';
  try {
    const b = await app.api<StageSummaryResponse>('/api/v1/stage-summary');
    if (n !== stageSeq) return;
    stagesData.value = b;
    stageState.value = 'ready';
  } catch (e) {
    if (n !== stageSeq) return;
    stageFailure.value = recovery(e).message;
    stageState.value = 'error';
  }
}
function reloadAll() {
  void load();
  void loadStages();
}
onMounted(reloadAll);

const role = computed(() => app.user?.role || '');
const c = (k: string) => data.value?.counts[k] ?? 0;
interface Tile {
  key: string;
  label: string;
  note: string;
  to: string;
  tone?: 'risk' | 'attention';
  count?: () => number;
}
const blocked = () => Math.max(0, c('reviewQueue') - c('reviewable'));
// Role order answers each role's first question. Every tile links to a list
// with exactly the same server filter and scope as its number.
const tiles = computed<Tile[]>(() => {
  const t: Tile[] = [];
  const overdue: Tile = { key: 'overdue', label: '已逾期跟进', note: '下次跟进时间早于统计时间', to: '/followups?window=overdue', tone: 'risk' };
  const upcoming: Tile = { key: 'upcoming7d', label: '未来 7 天跟进', note: '统计时间起 7 天内，不含已逾期', to: '/followups?window=upcoming' };
  const returned: Tile = { key: 'returned', label: '退回补充', note: '审核退回，需补充后重新提交', to: '/projects?status=RETURNED', tone: 'attention' };
  const drafts: Tile = { key: 'drafts', label: '草稿', note: '尚未正式报备，可继续填写', to: '/projects?status=DRAFT' };
  const reviewable: Tile = { key: 'reviewable', label: '你可独立审核', note: '你未参与、证据完整，可直接决定', to: '/reviews?eligibility=reviewable' };
  const others: Tile = { key: 'blocked', label: '需要其他人处理', note: '你参与过、证据待核验或需跨组处理', to: '/reviews?eligibility=blocked', count: blocked };
  switch (role.value) {
    case 'baobei_sales':
      t.push(overdue, upcoming, returned, drafts);
      break;
    case 'baobei_manager':
      t.push(reviewable, others, overdue, upcoming);
      break;
    case 'baobei_director':
      t.push(reviewable, others, overdue, upcoming);
      break;
    case 'baobei_admin':
      t.push({ key: 'unassignedDrafts', label: '待分配草稿', note: '指派有效销售后才能正式报备', to: '/unassigned', tone: 'attention' }, reviewable, others, overdue);
      break;
  }
  return t.filter((x) => !['reviewable', 'blocked'].includes(x.key) || can(app.user, 'project.review'));
});
const tileCount = (t: Tile) => (t.count ? t.count() : c(t.key));
const effective = computed(() => c('submitted') + c('underReview') + c('approved'));
const asOfText = computed(() => (data.value ? when(data.value.asOf) : ''));
const question = computed(
  () =>
    ({
      baobei_sales: '今天需要跟进哪些客户和项目？',
      baobei_manager: '本组有哪些审核与跟进需要处理？',
      baobei_director: '跨团队的项目进展与风险如何？',
      baobei_admin: '有哪些待分配与组织治理事项？',
    })[role.value] || ''
);
const scopeText = computed(
  () =>
    ({
      baobei_sales: '你负责的项目',
      baobei_manager: '本团队已分配项目',
      baobei_director: '全部已分配项目（不含待分配草稿）',
      baobei_admin: '全部项目，含待分配草稿',
    })[role.value] || ''
);
const go = (to: string) => {
  const [path, query] = to.split('?');
  return router.push({ path, query: Object.fromEntries(new URLSearchParams(query || '')) });
};
const maxStage = computed(() => Math.max(1, ...(stagesData.value?.stages.map((s) => s.count) ?? [1])));
const amount = computed(() => stagesData.value?.amount ?? null);
</script>
<template>
  <div class="page-heading home-heading">
    <div>
      <p class="home-eyebrow">{{ app.user?.name }} · {{ scopeText }}</p>
      <h1>工作台</h1>
      <p class="home-question">{{ question }}</p>
    </div>
    <div class="home-actions">
      <ActionButton v-if="role === 'baobei_admin' && can(app.user, 'staff.manage')" action-id="staff.invite-start" :run="() => router.push({ path: '/settings/users', query: { add: '1' } })"><UserPlus :size="16" aria-hidden="true" />添加成员</ActionButton>
    </div>
  </div>

  <p v-if="state === 'loading' && !data" class="muted" role="status" aria-busy="true">正在汇总你的任务…</p>
  <div v-else-if="state === 'error'" class="form-alert" role="alert">
    无法获取任务汇总：{{ failure }}<template v-if="forbidden">。请联系管理员核对账号权限。</template>
    <ActionButton v-else action-id="dashboard.reload" :run="reloadAll">重新加载</ActionButton>
  </div>
  <template v-if="data && state !== 'error'">
    <p class="scope-line">
      <span>{{ projectScopeLabel(app.user) }} · 共 {{ c('total') }} 项 · 有效报备 {{ effective }} 项（已提交、重复待审、已批准）</span><span class="muted scope-asof">统计时间 {{ asOfText }}（服务端实时汇总）<ActionButton action-id="dashboard.refresh" class="link-button" :run="reloadAll">{{ state === 'loading' ? '刷新中…' : '刷新' }}</ActionButton></span>
    </p>
    <section class="task-board" aria-label="首要任务">
      <ActionButton
        v-for="tile in tiles"
        :key="tile.key"
        action-id="dashboard.task"
        :class="['task-tile', tile.tone, { zero: tileCount(tile) === 0 }]"
        :aria-label="tile.label + ' ' + tileCount(tile) + ' 项'"
        :run="() => go(tile.to)"
      >
        <span class="task-count">{{ tileCount(tile) }}</span><span class="task-label">{{ tile.label }}</span><span class="task-note">{{ tile.note }}</span>
      </ActionButton>
    </section>
    <p v-if="can(app.user, 'project.review') && blocked() > 0" class="muted small">
      有 {{ blocked() }} 项你参与过或证据待核验，需要其他独立审核人处理；工作台不提供绕过入口。
    </p>

    <div v-if="!c('total')" class="empty">
      <h2>{{ role === 'baobei_admin' ? '还没有业务项目' : role === 'baobei_sales' ? '还没有报备' : '范围内暂无项目' }}</h2>
      <p class="muted">
        {{ role === 'baobei_admin' ? '没有销售人员时也可以先建立待分配草稿，之后再指派。' : role === 'baobei_sales' ? '记下客户和项目名称就能保存草稿，其他信息可以稍后补充。' : '销售提交报备后会出现在这里。' }}
      </p>
      <ActionButton v-if="can(app.user, 'project.create')" action-id="project.create" primary :run="() => router.push('/new')">
        <Plus :size="16" aria-hidden="true" />{{ role === 'baobei_admin' ? '先建立草稿' : '快速报备' }}
      </ActionButton>
    </div>

    <div class="home-grid">
      <section class="home-panel home-stages" aria-labelledby="home-stages-title">
        <div class="section-title">
          <h2 id="home-stages-title">销售阶段分布</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/pipeline')">项目管道 <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <p class="panel-def muted">按销售阶段统计{{ scopeText }}的全部项目（含草稿与退回），与报备状态无关。</p>
        <!-- G3-4-A1: a separate snapshot from the task summary; its own time is shown. -->
        <p v-if="stagesData && stageState === 'ready'" class="muted panel-asof" data-testid="home-stage-asof">阶段与金额统计时间 {{ when(stagesData.asOf) }}</p>
        <p v-if="stageState === 'loading'" class="muted" role="status">正在加载阶段分布…</p>
        <div v-else-if="stageState === 'error'" class="form-alert" role="alert">
          无法加载阶段分布：{{ stageFailure }} <ActionButton action-id="dashboard.stage-reload" :run="loadStages">重新加载</ActionButton>
        </div>
        <ol v-else-if="stagesData" class="stage-bars">
          <li v-for="s in stagesData.stages" :key="s.code">
            <ActionButton
              action-id="dashboard.stage"
              :class="['stage-bar', { zero: !s.count }]"
              :disabled="!s.count"
              reason="本阶段没有项目"
              :aria-label="stages[s.code] + ' ' + s.count + ' 项'"
              :run="() => router.push({ path: '/projects', query: { stage: s.code } })"
            >
              <span class="stage-bar-label">{{ stages[s.code] }}</span>
              <span class="stage-bar-track" aria-hidden="true"><span :style="{ width: (s.count / maxStage) * 100 + '%' }"></span></span>
              <span class="stage-bar-count">{{ s.count }}</span>
            </ActionButton>
          </li>
        </ol>
        <p v-if="stagesData?.unclassified" class="note" role="note">另有 {{ stagesData.unclassified }} 个项目的销售阶段不在 12 个标准阶段内，请联系管理员核对。</p>
      </section>

      <section class="home-panel home-amount" aria-labelledby="home-amount-title">
        <div class="section-title">
          <h2 id="home-amount-title">已确认预估金额</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/statistics')">统计 <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <p class="panel-def muted">项目预估金额中已知的部分（人民币元），不是合同金额、收入或回款。</p>
        <!-- G4-S1A: amounts are shown only for the current successful snapshot.
             After a failed refresh the previous figures are withheld, so an old
             amount is never presented as this refresh's live result. -->
        <template v-if="amount && stagesData && stageState === 'ready'">
          <strong :class="['amount-figure', { long: amount.knownTotal.length > 15, huge: amount.knownTotal.length > 19 }]" data-testid="home-known-total">{{ yuan(amount.knownTotal) }}</strong>
          <p class="muted amount-basis" data-testid="home-known-count">来自 {{ amount.knownCount }} 个项目<template v-if="amount.knownZeroCount">（其中 {{ amount.knownZeroCount }} 个确认为 0）</template><template v-if="!amount.knownCount && stagesData.total">；合计 ¥0.00 不代表项目价值为零</template></p>
          <dl class="amount-split">
            <div><dt>金额未知</dt><dd data-testid="home-unknown">{{ amount.unknownCount }}</dd></div>
            <div><dt>历史待确认</dt><dd data-testid="home-legacy">{{ amount.legacyUnconfirmedCount }}</dd></div>
            <div :class="{ warn: amount.invalidCount }"><dt>数据异常</dt><dd data-testid="home-invalid">{{ amount.invalidCount }}</dd></div>
          </dl>
          <p v-if="amount.knownImplicitCount" class="muted small">其中 {{ amount.knownImplicitCount }} 个来自历史记录：未单独标记已知性，按既有规则视为已知，未经逐条人工确认。</p>
          <ol class="amount-bars" aria-label="各阶段已确认预估金额">
            <li v-for="s in stagesData.stages.filter((x) => x.amount.knownCount)" :key="s.code">
              <span>{{ stages[s.code] }}</span><span class="amount-bar-track" aria-hidden="true"><span :style="{ width: sharePercent(s.amount.knownTotal, amount.knownTotal) + '%' }"></span></span><span class="amount-bar-value">{{ yuan(s.amount.knownTotal) }}</span>
            </li>
          </ol>
        </template>
        <p v-else-if="stageState === 'loading'" class="muted" role="status">正在加载金额…</p>
        <div v-else-if="stageState === 'error'" class="form-alert" role="alert" data-testid="home-amount-error">
          金额与阶段分布来自同一汇总，本次刷新失败，暂不可用：{{ stageFailure }}。<template v-if="stagesData">上次成功统计（{{ when(stagesData.asOf) }}）的金额已隐藏，避免被误认为当前结果。</template>可在左侧“销售阶段分布”重新加载。
        </div>
      </section>

      <section v-if="can(app.user, 'staff.manage') || can(app.user, 'team.manage')" class="home-panel home-governance" aria-labelledby="home-gov-title">
        <div class="section-title"><h2 id="home-gov-title">组织治理</h2></div>
        <p class="panel-def muted">人员与团队的变更由服务端重新鉴权；此处只提供入口，不显示未统计的数字。</p>
        <div class="gov-links">
          <ActionButton v-if="can(app.user, 'staff.manage')" action-id="nav.open" :run="() => router.push(role === 'baobei_admin' ? '/settings/users' : '/team/members')">
            <Users :size="16" aria-hidden="true" />{{ role === 'baobei_admin' ? '维护人员与邀请' : '查看团队成员' }}
          </ActionButton>
          <ActionButton v-if="can(app.user, 'team.manage')" action-id="nav.open" :run="() => router.push('/settings/teams')"><Building2 :size="16" aria-hidden="true" />维护团队</ActionButton>
        </div>
      </section>
    </div>

    <div v-if="c('total')" class="home-columns">
      <section>
        <div class="section-title">
          <h2>最近更新</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/projects')">查看{{ projectScopeLabel(app.user) }} <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <div class="recent-list">
          <ActionButton v-for="p in data.recent" :key="p.id" action-id="project.detail" :run="() => router.push({ path: '/projects/' + p.id, query: { from: '/' } })">
            <span class="record-title">{{ p.projectName || '未命名草稿' }}<small>{{ p.customer || '待填写客户' }} · {{ p.registrationNo }} · {{ p.ownerName || '待分配' }}</small></span><span :class="['badge', p.intakeStatus.toLowerCase()]">{{ statuses[p.intakeStatus] }}</span><span class="muted desktop">{{ stages[p.salesStage] }}</span><span class="muted">{{ when(p.updatedAt) }}</span>
          </ActionButton>
        </div>
      </section>
      <section>
        <div class="section-title">
          <h2>待跟进（已逾期与 7 天内）</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/followups')">全部 <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <p class="panel-def muted">按下次跟进时间排序，最多 5 项；已逾期的排在前面。</p>
        <p v-if="!data.followups.length" class="muted">没有逾期或 7 天内需要跟进的项目。</p>
        <ol class="followup-list">
          <li v-for="p in data.followups" :key="p.id" :class="{ late: p.nextFollowupAt && new Date(p.nextFollowupAt) < new Date(data.asOf) }">
            <time>{{ when(p.nextFollowupAt) }}<span v-if="p.nextFollowupAt && new Date(p.nextFollowupAt) < new Date(data.asOf)" class="late-tag">已逾期</span></time>
            <ActionButton action-id="project.detail" class="clamp-2" :title="p.projectName" :run="() => router.push({ path: '/projects/' + p.id, query: { from: '/' } })">{{ p.projectName || '未命名草稿' }}</ActionButton>
            <small class="clamp-2">{{ p.nextAction || '尚未记录下一步' }}</small>
          </li>
        </ol>
      </section>
    </div>
  </template>
</template>
