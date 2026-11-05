<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { ArrowUpRight, Plus, UserPlus } from '@lucide/vue';
import { useApp } from './store';
import { statuses, stages, when } from './model';
import { can, projectScopeLabel } from './navigation';
import { recovery } from './reasons';
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
  route = useRoute(),
  data = ref<Summary | null>(null),
  state = ref<'loading' | 'ready' | 'error'>('loading'),
  failure = ref('');
async function load() {
  state.value = 'loading';
  try {
    data.value = await app.api<Summary>('/api/v1/workspace-summary');
    state.value = 'ready';
  } catch (e) {
    failure.value = recovery(e).message;
    state.value = 'error';
  }
}
onMounted(load);
const role = computed(() => app.user?.role || '');
const isStats = computed(() => route.path === '/statistics');
const c = (k: string) => data.value?.counts[k] ?? 0;
interface Tile {
  key: string;
  label: string;
  note: string;
  to: string;
  tone?: 'risk' | 'attention';
}
// Each tile links to a list with exactly the same server filter and scope.
const tiles = computed<Tile[]>(() => {
  const t: Tile[] = [];
  if (role.value === 'baobei_admin') t.push({ key: 'unassignedDrafts', label: '待分配草稿', note: '指派有效销售后才能正式报备', to: '/unassigned', tone: 'attention' });
  if (can(app.user, 'project.review'))
    t.push({ key: 'reviewable', label: '你可独立审核', note: `范围内待审 ${c('reviewQueue')} 项，其余需他人处理`, to: '/reviews' });
  t.push({ key: 'overdue', label: '已逾期跟进', note: '下次跟进时间已过', to: '/followups?window=overdue', tone: 'risk' });
  t.push({ key: 'upcoming7d', label: '未来 7 天跟进', note: '不含已逾期', to: '/followups?window=upcoming' });
  t.push({ key: 'drafts', label: '草稿', note: `另有退回补充 ${c('returned')} 项`, to: '/projects?status=DRAFT' });
  return t;
});
const effective = computed(() => c('submitted') + c('underReview') + c('approved'));
const asOfText = computed(() =>
  data.value ? new Date(data.value.asOf).toLocaleString('zh-CN', { month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
);
const greeting = computed(
  () =>
    ({
      baobei_sales: '先记下新线索，再处理到期的跟进。',
      baobei_admin: '先处理待分配草稿与你可独立审核的项目。',
      baobei_manager: '先处理本组待审核，再看逾期跟进。',
      baobei_director: '关注跨组重复与逾期风险。',
    })[role.value] || ''
);
const go = (to: string) => {
  const [path, query] = to.split('?');
  return router.push({ path, query: Object.fromEntries(new URLSearchParams(query || '')) });
};
</script>
<template>
  <div class="page-heading home-heading">
    <div>
      <h1>{{ isStats ? '基础统计' : '工作台' }}</h1>
      <p class="muted">{{ app.user?.name }}，{{ greeting }}</p>
    </div>
    <div class="home-actions">
      <ActionButton v-if="role === 'baobei_admin' && can(app.user, 'staff.manage')" action-id="nav.open" :run="() => router.push('/settings/users')"><UserPlus :size="16" aria-hidden="true" />邀请销售</ActionButton>
    </div>
  </div>

  <p v-if="state === 'loading'" class="muted" role="status" aria-busy="true">正在汇总你的任务…</p>
  <div v-else-if="state === 'error'" class="form-alert" role="alert">
    无法获取任务汇总：{{ failure }}
    <ActionButton action-id="dashboard.reload" :run="load">重新加载</ActionButton>
  </div>
  <template v-else-if="data">
    <p class="scope-line">
      <span>{{ projectScopeLabel(app.user) }} · 共 {{ c('total') }} 项 · 有效报备 {{ effective }} 项</span><span class="muted">统计时间 {{ asOfText }}（服务端实时汇总）</span>
    </p>
    <section class="task-board" aria-label="待办任务">
      <ActionButton
        v-for="tile in tiles"
        :key="tile.key"
        action-id="dashboard.task"
        :class="['task-tile', tile.tone, { zero: c(tile.key) === 0 }]"
        :aria-label="tile.label + ' ' + c(tile.key) + ' 项'"
        :run="() => go(tile.to)"
      >
        <span class="task-count">{{ c(tile.key) }}</span><span class="task-label">{{ tile.label }}</span><span class="task-note">{{ tile.note }}</span>
      </ActionButton>
    </section>
    <p v-if="role === 'baobei_admin' && c('reviewQueue') > c('reviewable')" class="muted small">
      有 {{ c('reviewQueue') - c('reviewable') }} 项你参与过或证据待核验，需要其他独立审核人处理。
    </p>

    <div v-if="!c('total')" class="empty">
      <h2>{{ role === 'baobei_admin' ? '还没有业务项目' : role === 'baobei_sales' ? '还没有报备' : '范围内暂无项目' }}</h2>
      <p class="muted">
        {{ role === 'baobei_admin' ? '没有销售人员时也可以先建立待分配草稿，之后再指派。' : role === 'baobei_sales' ? '记下客户和项目名称就能保存草稿，其他信息可以稍后补充。' : '销售提交报备后会出现在这里。' }}
      </p>
      <ActionButton
        v-if="can(app.user, 'project.create')"
        action-id="project.create"
        primary
        :run="() => router.push('/new')"
      >
        <Plus :size="16" aria-hidden="true" />{{ role === 'baobei_admin' ? '先建立草稿' : '快速报备' }}
      </ActionButton>
    </div>
    <div v-else class="home-columns">
      <section>
        <div class="section-title">
          <h2>最近更新</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/projects')">查看{{ projectScopeLabel(app.user) }} <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <div class="recent-list">
          <ActionButton v-for="p in data.recent" :key="p.id" action-id="project.detail" :run="() => router.push('/projects/' + p.id)">
            <span class="record-title">{{ p.projectName || '未命名草稿' }}<small>{{ p.customer || '待填写客户' }} · {{ p.registrationNo }} · {{ p.ownerName || '待分配' }}</small></span><span :class="['badge', p.intakeStatus.toLowerCase()]">{{ statuses[p.intakeStatus] }}</span><span class="muted desktop">{{ stages[p.salesStage] }}</span><span class="muted">{{ when(p.updatedAt) }}</span>
          </ActionButton>
        </div>
      </section>
      <section>
        <div class="section-title">
          <h2>7 天内需跟进</h2>
          <ActionButton action-id="nav.open" :run="() => router.push('/followups')">全部 <ArrowUpRight :size="14" aria-hidden="true" /></ActionButton>
        </div>
        <p v-if="!data.followups.length" class="muted">7 天内没有需要跟进的项目。</p>
        <ol class="followup-list">
          <li v-for="p in data.followups" :key="p.id" :class="{ late: p.nextFollowupAt && new Date(p.nextFollowupAt) < new Date(data.asOf) }">
            <time>{{ when(p.nextFollowupAt) }}</time>
            <ActionButton action-id="project.detail" :run="() => router.push('/projects/' + p.id)">{{ p.projectName || '未命名草稿' }}</ActionButton>
            <small>{{ p.nextAction || '尚未记录下一步' }}</small>
          </li>
        </ol>
      </section>
    </div>
    <p v-if="isStats" class="note">此页仅提供与工作台相同口径的范围计数。阶段管道、金额、团队对比与趋势分析在后续批次（G3）提供。</p>
  </template>
</template>
