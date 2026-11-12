<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { onBeforeRouteLeave } from 'vue-router';
import { FolderKanban, Check, Plus, RefreshCw } from 'lucide-vue-next';
import {
  listScopes, listIntakes, createIntake, transitionIntake, approveIntake,
  listResources, listScheduleItems, listConflicts, overrideConflict,
  listPortfolioProjects,
} from '../cloud/portfolio';
import FormalProjectPanel from '../components/FormalProjectPanel.vue';
import { ApiError } from '../cloud/client';
import { allowClose } from '../cloud/draft';
import { webSession, productionAuth } from '../cloud/session';
import {
  blankIntakeForm, formDirty, validateIntakeForm, toIntakeInput, intakeErrorMessage, listState, intakeStages,
  PRIORITIES, RISK_LEVELS, PRIORITY_TEXT, RISK_TEXT, PROJECT_TYPE_SUGGESTIONS, type IntakeForm,
} from '../cloud/intake';
import type { ProjectIntake, ScheduleConflict } from '../../../../contracts/typescript/types.gen';

const queryClient = useQueryClient();
const tab = ref<'intake' | 'formal' | 'resource' | 'schedule' | 'conflict'>('intake');
const selectedProject = ref('');
const actionError = ref('');
const notice = ref('');

const scopesQuery = useQuery({ queryKey: ['portfolio-scopes'], queryFn: listScopes });
const intakesQuery = useQuery({ queryKey: ['intakes'], queryFn: listIntakes });
const resourcesQuery = useQuery({ queryKey: ['resources'], queryFn: listResources });
const scheduleQuery = useQuery({ queryKey: ['schedule-items'], queryFn: listScheduleItems });
const conflictsQuery = useQuery({ queryKey: ['conflicts'], queryFn: listConflicts });
// Formal projects of the caller's portfolios (server facts: members, Inventory, map).
const formalQuery = useQuery({ queryKey: ['portfolio-projects'], queryFn: listPortfolioProjects });
const formalState = computed(() => listState({ isLoading: formalQuery.isLoading.value, isError: formalQuery.isError.value, data: formalQuery.data.value }));
const formalById = computed(() => new Map((formalQuery.data.value ?? []).map((p) => [p.id, p])));

const stateOf = (q: { isLoading: { value: boolean }; isError: { value: boolean }; data: { value: readonly unknown[] | undefined } }) =>
  computed(() => listState({ isLoading: q.isLoading.value, isError: q.isError.value, data: q.data.value }));
const intakeState = stateOf(intakesQuery);
const resourceState = stateOf(resourcesQuery);
const scheduleState = stateOf(scheduleQuery);
const conflictState = stateOf(conflictsQuery);
const errorText = (e: unknown) => intakeErrorMessage(e, 'load');

// The create entry is shown only when the caller has at least one portfolio scope (the scope
// list is already filtered by membership server-side). The server re-checks project_intake_write
// on submit; a 403 there is reported in the form.
const scopes = computed(() => (scopesQuery.data.value ?? []).filter((s) => s.status === 'ACTIVE'));
const canCreate = computed(() => scopes.value.length > 0);
const scopeName = (id: string) => scopesQuery.data.value?.find((s) => s.id === id)?.name ?? id;
// UX only (the server re-checks project_approve): planners do not see an approve button.
const canApprove = (scopeId: string) => scopesQuery.data.value?.find((s) => s.id === scopeId)?.myRole === 'portfolio_manager';
// DirtyGuard for the grant form inside the formal-project panel (Q3 fix B): switching tab,
// switching project or closing the panel asks the panel first; an in-flight change blocks.
const formalPanel = ref<{ canLeave: () => boolean } | null>(null);
const panelAllowsLeave = () => !formalPanel.value || formalPanel.value.canLeave();
type Tab = 'intake' | 'formal' | 'resource' | 'schedule' | 'conflict';
function switchTab(next: Tab) {
  if (next === tab.value) return;
  if (tab.value === 'formal' && !panelAllowsLeave()) return;
  tab.value = next;
  // Approvals by others since the page loaded must show up when the formal tab is opened.
  if (next === 'formal') void formalQuery.refetch();
}
function selectProject(id: string) {
  if (id === selectedProject.value) return;
  if (!panelAllowsLeave()) return;
  selectedProject.value = id;
}
function openFormal(id: string) {
  if (!panelAllowsLeave()) return;
  selectedProject.value = id;
  tab.value = 'formal';
}
const grants = computed(() => (productionAuth ? (webSession.value ? webSession.value.projects : null) : null));

const intakeStatusText: Record<string, string> = {
  REGISTERED: '已登记', REVIEW: '评审中', PLANNING: '规划中', APPROVED: '已批准',
  REJECTED: '已驳回', CANCELLED: '已取消',
};
const nextAction: Record<string, string | null> = {
  REGISTERED: 'REVIEW', REVIEW: 'PLANNING', PLANNING: 'APPROVED',
  APPROVED: null, REJECTED: null, CANCELLED: null,
};
const nextText: Record<string, string> = { REVIEW: '提交评审', PLANNING: '进入规划', APPROVED: '批准并生成正式项目' };

// ---- create form ----------------------------------------------------------------------
const creating = ref(false);
const initialScope = ref('');
const form = ref<IntakeForm>(blankIntakeForm());
const showMore = ref(false);
const formError = ref('');
const touched = ref(false);
const errors = computed(() => validateIntakeForm(form.value, scopes.value.map((s) => s.id)));
const shownErrors = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => creating.value && formDirty(form.value, initialScope.value));
const lastCreated = ref<ProjectIntake | null>(null);

const createMutation = useMutation({
  mutationFn: () => createIntake(toIntakeInput(form.value)),
  onSuccess: async (created) => {
    lastCreated.value = created;
    creating.value = false;
    notice.value = `已登记「${created.projectName}」（${created.intakeCode}），状态：已登记。`;
    await queryClient.invalidateQueries({ queryKey: ['intakes'] });
  },
  onError: (e) => {
    formError.value = intakeErrorMessage(e, 'create');
  },
});

function openCreate() {
  initialScope.value = scopes.value.length === 1 ? scopes.value[0].id : '';
  form.value = blankIntakeForm(initialScope.value);
  formError.value = '';
  touched.value = false;
  showMore.value = false;
  creating.value = true;
}
function submit() {
  touched.value = true;
  formError.value = '';
  if (createMutation.isPending.value || Object.keys(errors.value).length) return;
  createMutation.mutate();
}
function closeCreate() {
  if (!allowClose(dirty.value, createMutation.isPending.value)) return;
  creating.value = false;
  formError.value = '';
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && creating.value) closeCreate();
}
function onBeforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || createMutation.isPending.value) {
    event.preventDefault();
    event.returnValue = '';
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKeydown);
  window.addEventListener('beforeunload', onBeforeUnload);
});
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown);
  window.removeEventListener('beforeunload', onBeforeUnload);
});
onBeforeRouteLeave(() => allowClose(dirty.value, createMutation.isPending.value) && panelAllowsLeave());

// ---- transitions / approval -------------------------------------------------------------
const busyId = ref('');
const transitionMutation = useMutation({
  mutationFn: (v: { id: string; status: string }) => transitionIntake(v.id, v.status, '运营推进'),
  onSuccess: () => { notice.value = '状态已推进。'; },
  onError: (e) => (actionError.value = intakeErrorMessage(e, 'transition')),
  onSettled: () => { busyId.value = ''; return queryClient.invalidateQueries({ queryKey: ['intakes'] }); },
});
const approveMutation = useMutation({
  mutationFn: (id: string) => approveIntake(id),
  onSuccess: (r) => { notice.value = `已批准并生成正式项目 ${r.projectId}。该项目尚未授权给任何人，也未进入运营地图。`; },
  onError: (e) => (actionError.value = intakeErrorMessage(e, 'approve')),
  onSettled: async () => {
    busyId.value = '';
    await Promise.all([queryClient.invalidateQueries({ queryKey: ['intakes'] }), queryClient.invalidateQueries({ queryKey: ['portfolio-projects'] })]);
  },
});
const overrideMutation = useMutation({
  mutationFn: (v: { id: number; reason: string }) => overrideConflict(v.id, v.reason),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['conflicts'] }),
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '覆盖失败'),
});

function advance(intake: ProjectIntake, status: string) {
  if (busyId.value) return;
  if (status === 'APPROVED' && !window.confirm(`批准「${intake.projectName}」将生成正式项目。\n批准不会自动授予任何人项目访问权限，也不会进入运营地图。确定批准吗？`)) return;
  actionError.value = '';
  notice.value = '';
  busyId.value = intake.id;
  if (status === 'APPROVED') approveMutation.mutate(intake.id);
  else transitionMutation.mutate({ id: intake.id, status });
}
// Conflict override (Q4-QA-R1 §5): the server requires schedule_override on the conflict's owning
// portfolio scope (portfolio_manager only) and refuses a hard conflict on a resource without
// overrideAllowed. The conflict contract carries no scope id, so the button is offered only when the
// caller manages EVERY portfolio it belongs to (then each listed conflict is in a managed scope); a
// mixed planner/manager caller gets no executable button. The server still decides.
const managesAllScopes = computed(() => {
  const mine = (scopesQuery.data.value ?? []).filter((s) => s.myRole !== null);
  return mine.length > 0 && mine.every((s) => s.myRole === 'portfolio_manager');
});
const hardConflict = (c: ScheduleConflict) =>
  !!c.resourceId && resourcesQuery.data.value?.find((r) => r.id === c.resourceId)?.overrideAllowed === false;
function override(id: number) {
  const reason = window.prompt('覆盖原因（至少 8 字）');
  if (reason) overrideMutation.mutate({ id, reason });
}
const fmtDate = (v: string | null) => (v ? new Date(v).toLocaleDateString('zh-CN') : '—');
</script>
<template>
  <section class="workspace-page portfolio-workspace" data-testid="portfolio-workspace">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">PORTFOLIO / 项目组合</span>
        <h1>项目组合与资源排期</h1>
        <p>项目登记 → 评审 → 规划 → 批准生成正式项目；项目授权、资产登记与地图定位是之后的独立步骤</p>
      </div>
      <button v-if="tab === 'intake' && canCreate" class="primary-action" data-testid="intake-create" @click="openCreate">
        <Plus :size="15" />新建项目登记
      </button>
      <FolderKanban v-else :size="20" />
    </header>

    <div class="tabs" role="tablist">
      <button :class="{ active: tab === 'intake' }" @click="switchTab('intake')">项目登记</button>
      <button :class="{ active: tab === 'formal' }" data-testid="tab-formal" @click="switchTab('formal')">正式项目</button>
      <button :class="{ active: tab === 'resource' }" @click="switchTab('resource')">资源</button>
      <button :class="{ active: tab === 'schedule' }" @click="switchTab('schedule')">排期</button>
      <button :class="{ active: tab === 'conflict' }" @click="switchTab('conflict')">冲突</button>
    </div>

    <p v-if="actionError" class="form-error" role="alert" data-testid="portfolio-action-error">{{ actionError }}</p>
    <p v-if="notice" class="workspace-notice" role="status" data-testid="portfolio-notice">{{ notice }}</p>

    <template v-if="tab === 'intake'">
      <p v-if="scopesQuery.isError.value" class="workspace-error" role="alert" data-testid="scope-error">
        项目组合范围加载失败，暂时无法新建登记：{{ errorText(scopesQuery.error.value) }}
        <button class="ghost-action" @click="scopesQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </p>
      <p v-else-if="scopesQuery.isSuccess.value && !canCreate" class="workspace-hint" data-testid="intake-no-scope">
        您不属于任何项目组合，不能新建项目登记。如需登记项目，请联系管理员授予项目组合角色。
      </p>

      <div v-if="intakeState === 'loading'" class="skeleton table-skeleton" aria-busy="true" data-testid="intake-loading"></div>
      <div v-else-if="intakeState === 'error'" class="workspace-error" role="alert" data-testid="intake-error">
        项目登记加载失败：{{ errorText(intakesQuery.error.value) }}
        <button class="ghost-action" @click="intakesQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="intakeState === 'empty'" class="workspace-empty" data-testid="intake-empty">暂无项目登记。</p>
      <template v-else>
        <p v-if="intakeState === 'stale'" class="workspace-error" role="alert" data-testid="intake-stale">
          刷新失败，以下为上次成功加载的数据：{{ errorText(intakesQuery.error.value) }}
          <button class="ghost-action" @click="intakesQuery.refetch()"><RefreshCw :size="13" />重试</button>
        </p>
        <div class="data-table-wrap">
          <table class="data-table" data-testid="intake-table">
            <thead><tr><th>编号</th><th>项目名</th><th>项目组合</th><th>状态</th><th>后续环节</th><th>优先级</th><th>计划</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="i in intakesQuery.data.value" :key="i.id" class="data-row" :data-intake-id="i.id" :class="{ 'row-new': lastCreated?.id === i.id }">
                <td>{{ i.intakeCode }}</td>
                <td>{{ i.projectName }}<div class="cell-sub">{{ i.customerName ?? '' }}</div></td>
                <td>{{ scopeName(i.portfolioScopeId) }}</td>
                <td>
                  <span class="status-pill" :data-status="i.status === 'APPROVED' ? 'ACTIVE' : i.status === 'REJECTED' || i.status === 'CANCELLED' ? 'RETIRED' : 'DRAFT'">
                    {{ intakeStatusText[i.status] ?? i.status }}
                  </span>
                </td>
                <td>
                  <ul class="stage-list" data-testid="intake-stages">
                    <li v-for="s in intakeStages(i, grants, i.formalProjectId ? formalById.get(i.formalProjectId) : undefined)" :key="s.key" :data-tone="s.tone">{{ s.text }}</li>
                  </ul>
                  <button v-if="i.formalProjectId && formalById.get(i.formalProjectId)" class="link-action" data-testid="open-formal" @click="openFormal(i.formalProjectId)">查看正式项目与成员 →</button>
                </td>
                <td>{{ PRIORITY_TEXT[i.priority] ?? i.priority }}</td>
                <td>{{ fmtDate(i.plannedStart) }} – {{ fmtDate(i.plannedEnd) }}</td>
                <td>
                  <button
                    v-if="nextAction[i.status] && (nextAction[i.status] !== 'APPROVED' || canApprove(i.portfolioScopeId))"
                    class="ghost-action"
                    :disabled="!!busyId"
                    :data-testid="nextAction[i.status] === 'APPROVED' ? 'intake-approve' : 'intake-advance'"
                    @click="advance(i, nextAction[i.status]!)"
                  >
                    <Check :size="13" />{{ busyId === i.id ? '处理中…' : nextText[nextAction[i.status]!] }}
                  </button>
                  <span v-else-if="nextAction[i.status] === 'APPROVED'" class="muted" data-testid="await-approval">待项目组合经理批准</span>
                  <span v-else class="muted">终态</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>

    <template v-else-if="tab === 'formal'">
      <p class="workspace-hint">正式项目由批准登记生成。这里配置的是<strong>项目权限</strong>（谁能访问该项目的资产与设备），与上面的项目组合权限无关；审批不会自动授权。</p>
      <div v-if="formalState === 'loading'" class="skeleton table-skeleton" aria-busy="true" data-testid="formal-loading"></div>
      <div v-else-if="formalState === 'error'" class="workspace-error" role="alert" data-testid="formal-error">
        正式项目加载失败：{{ errorText(formalQuery.error.value) }}
        <button class="ghost-action" @click="formalQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="formalState === 'empty'" class="workspace-empty" data-testid="formal-empty">您所在的项目组合暂无已批准的正式项目。</p>
      <div v-else class="formal-layout">
        <p v-if="formalState === 'stale'" class="workspace-error" role="alert">刷新失败，以下为上次成功加载的数据。</p>
        <div class="data-table-wrap">
          <table class="data-table" data-testid="formal-table">
            <thead><tr><th>正式项目</th><th>项目组合</th><th>阶段</th><th>项目成员</th><th>运营地图</th><th></th></tr></thead>
            <tbody>
              <tr v-for="p in formalQuery.data.value" :key="p.id" class="data-row" :data-formal-id="p.id" :class="{ 'row-new': selectedProject === p.id }">
                <td>{{ p.name }}<div class="cell-sub">{{ p.id }} · 登记 {{ p.intakeCode }}</div></td>
                <td>{{ scopeName(p.portfolioScopeId) }}</td>
                <td>{{ p.projectPhase ?? '—' }}</td>
                <td>
                  <span class="status-pill" :data-status="p.grantStatus === 'NO_MEMBERS' ? 'OUT_OF_SERVICE' : 'ACTIVE'">
                    {{ p.grantStatus === 'NO_MEMBERS' ? '未配置成员' : `${p.memberCount} 名成员` }}
                  </span>
                </td>
                <td class="cell-sub">{{ p.inventoryRegistered ? (p.adminCode ? `行政区 ${p.adminCode}` : '未关联空间位置') : '未进入地图' }}</td>
                <td><button class="ghost-action" data-testid="formal-open" @click="selectProject(p.id)">{{ p.canManageGrants ? '管理成员' : '查看' }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <FormalProjectPanel
          ref="formalPanel"
          :key="selectedProject"
          v-if="selectedProject && formalById.get(selectedProject)"
          :project="formalById.get(selectedProject)!"
          :scope-name="scopeName(formalById.get(selectedProject)!.portfolioScopeId)"
          @close="selectedProject = ''"
        />
      </div>
    </template>

    <template v-else-if="tab === 'resource'">
      <div v-if="resourceState === 'loading'" class="skeleton table-skeleton" aria-busy="true"></div>
      <div v-else-if="resourceState === 'error'" class="workspace-error" role="alert" data-testid="resource-error">
        资源加载失败：{{ errorText(resourcesQuery.error.value) }}
        <button class="ghost-action" @click="resourcesQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="resourceState === 'empty'" class="workspace-empty">暂无资源。</p>
      <div v-else class="data-table-wrap">
        <p v-if="resourceState === 'stale'" class="workspace-error" role="alert">刷新失败，以下为上次成功加载的数据。</p>
        <table class="data-table">
          <thead><tr><th>名称</th><th>类型</th><th>容量</th><th>独占</th><th>时区</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="r in resourcesQuery.data.value" :key="r.id" class="data-row">
              <td>{{ r.name }}</td>
              <td>{{ r.resourceType }}</td>
              <td>{{ r.capacity }}</td>
              <td>{{ r.exclusive ? '是' : '否' }}</td>
              <td>{{ r.homeTimezone }}</td>
              <td>{{ r.status }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else-if="tab === 'schedule'">
      <div v-if="scheduleState === 'loading'" class="skeleton table-skeleton" aria-busy="true"></div>
      <div v-else-if="scheduleState === 'error'" class="workspace-error" role="alert" data-testid="schedule-error">
        排期加载失败：{{ errorText(scheduleQuery.error.value) }}
        <button class="ghost-action" @click="scheduleQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="scheduleState === 'empty'" class="workspace-empty">暂无排期项。</p>
      <div v-else class="data-table-wrap">
        <p v-if="scheduleState === 'stale'" class="workspace-error" role="alert">刷新失败，以下为上次成功加载的数据。</p>
        <table class="data-table">
          <thead><tr><th>名称</th><th>类型</th><th>开始</th><th>结束</th><th>时区</th><th>状态</th><th>优先级</th></tr></thead>
          <tbody>
            <tr v-for="s in scheduleQuery.data.value" :key="s.id" class="data-row">
              <td>{{ s.name }}</td>
              <td>{{ s.sourceType }}</td>
              <td>{{ s.startAt }}</td>
              <td>{{ s.endAt }}</td>
              <td>{{ s.timezone }}</td>
              <td>{{ s.status }}</td>
              <td>{{ s.priority }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else>
      <div v-if="conflictState === 'loading'" class="skeleton table-skeleton" aria-busy="true"></div>
      <div v-else-if="conflictState === 'error'" class="workspace-error" role="alert" data-testid="conflict-error">
        冲突加载失败：{{ errorText(conflictsQuery.error.value) }}
        <button class="ghost-action" @click="conflictsQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="conflictState === 'empty'" class="workspace-empty">无未解决冲突。</p>
      <div v-else class="data-table-wrap">
        <p v-if="conflictState === 'stale'" class="workspace-error" role="alert">刷新失败，以下为上次成功加载的数据。</p>
        <table class="data-table" data-testid="conflict-table">
          <thead><tr><th>类型</th><th>严重度</th><th>左项</th><th>资源</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="c in conflictsQuery.data.value" :key="c.id" class="data-row">
              <td>{{ c.conflictType }}</td>
              <td>
                <span class="status-pill" :data-status="c.severity === 'BLOCKING' ? 'OUT_OF_SERVICE' : 'DRAFT'">{{ c.severity }}</span>
              </td>
              <td>{{ c.leftItemId }}</td>
              <td>{{ c.resourceId ?? '—' }}</td>
              <td>{{ c.status }}</td>
              <td>
                <span v-if="hardConflict(c)" class="muted small" data-testid="override-hard">硬冲突，不可覆盖</span>
                <button v-else-if="managesAllScopes" class="ghost-action" data-testid="conflict-override" :disabled="overrideMutation.isPending.value" @click="override(c.id)">覆盖</button>
                <span v-else class="muted small" data-testid="override-not-allowed">需项目组合经理覆盖</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <div v-if="creating" class="modal-backdrop" @click.self="closeCreate">
      <form class="workspace-modal intake-form" data-testid="intake-create-form" novalidate @submit.prevent="submit">
        <h2>新建项目登记</h2>
        <p class="form-hint">登记只是立项申请。批准后才会生成正式项目；项目访问授权、资产登记和地图定位需要分别完成。</p>
        <label>项目组合 *
          <select v-model="form.portfolioScopeId" aria-label="项目组合" :aria-invalid="!!shownErrors.portfolioScopeId">
            <option value="" disabled>选择项目组合</option>
            <option v-for="s in scopes" :key="s.id" :value="s.id">{{ s.name }}（{{ s.code }}）</option>
          </select>
          <span v-if="shownErrors.portfolioScopeId" class="field-error">{{ shownErrors.portfolioScopeId }}</span>
        </label>
        <div class="form-row">
          <label>登记编号 *
            <input v-model="form.intakeCode" aria-label="登记编号" maxlength="80" placeholder="例如 INT-2026-001" :aria-invalid="!!shownErrors.intakeCode" />
            <span v-if="shownErrors.intakeCode" class="field-error">{{ shownErrors.intakeCode }}</span>
          </label>
          <label>项目类型 *
            <input v-model="form.projectType" aria-label="项目类型" maxlength="80" list="intake-project-types" placeholder="新装 / 改造 / 维保" :aria-invalid="!!shownErrors.projectType" />
            <datalist id="intake-project-types">
              <option v-for="t in PROJECT_TYPE_SUGGESTIONS" :key="t.value" :value="t.value">{{ t.label }}</option>
            </datalist>
            <span v-if="shownErrors.projectType" class="field-error">{{ shownErrors.projectType }}</span>
          </label>
        </div>
        <label>项目名称 *
          <input v-model="form.projectName" aria-label="项目名称" maxlength="160" :aria-invalid="!!shownErrors.projectName" />
          <span v-if="shownErrors.projectName" class="field-error">{{ shownErrors.projectName }}</span>
        </label>
        <div class="form-row">
          <label>优先级
            <select v-model="form.priority" aria-label="优先级">
              <option v-for="p in PRIORITIES" :key="p" :value="p">{{ PRIORITY_TEXT[p] }}</option>
            </select>
          </label>
          <label>风险等级
            <select v-model="form.riskLevel" aria-label="风险等级">
              <option v-for="r in RISK_LEVELS" :key="r" :value="r">{{ RISK_TEXT[r] }}</option>
            </select>
          </label>
        </div>
        <label>客户名称<input v-model="form.customerName" aria-label="客户名称" maxlength="160" /></label>
        <div class="form-row">
          <label>计划开始<input v-model="form.plannedStart" type="date" aria-label="计划开始" /></label>
          <label>计划结束
            <input v-model="form.plannedEnd" type="date" aria-label="计划结束" :aria-invalid="!!shownErrors.plannedEnd" />
            <span v-if="shownErrors.plannedEnd" class="field-error">{{ shownErrors.plannedEnd }}</span>
          </label>
        </div>
        <button type="button" class="link-action" :aria-expanded="showMore" @click="showMore = !showMore">
          {{ showMore ? '收起更多信息' : '更多信息（可选）' }}
        </button>
        <template v-if="showMore">
          <label>范围说明<textarea v-model="form.scopeSummary" aria-label="范围说明" maxlength="2000" rows="3"></textarea></label>
          <label>现场参考<input v-model="form.siteReference" aria-label="现场参考" maxlength="160" placeholder="现场地址或编号（不会自动定位到地图）" /></label>
          <div class="form-row">
            <label>期望开工<input v-model="form.requestedStart" type="date" aria-label="期望开工" /></label>
            <label>期望交付
              <input v-model="form.requestedHandover" type="date" aria-label="期望交付" :aria-invalid="!!shownErrors.requestedHandover" />
              <span v-if="shownErrors.requestedHandover" class="field-error">{{ shownErrors.requestedHandover }}</span>
            </label>
          </div>
        </template>
        <p v-if="formError" class="form-error" role="alert" data-testid="intake-form-error">{{ formError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="createMutation.isPending.value" @click="closeCreate">取消</button>
          <button type="submit" class="primary-action" :disabled="createMutation.isPending.value">
            {{ createMutation.isPending.value ? '保存中…' : '保存登记' }}
          </button>
        </div>
      </form>
    </div>
  </section>
</template>
<style scoped>
.workspace-notice { color: #6fd39b; font-size: 13px; margin: 0; }
.workspace-hint { color: var(--text-3); font-size: 13px; margin: 0; }
.workspace-error { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.stage-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; font-size: 12px; max-width: 320px; }
.stage-list li[data-tone='ok'] { color: #6fd39b; }
.stage-list li[data-tone='pending'] { color: #e8b75d; }
.stage-list li[data-tone='progress'] { color: #6ecdf2; }
.stage-list li[data-tone='stopped'] { color: #7c8ea2; }
.cell-sub { font-size: 11px; color: var(--text-3); }
.row-new td { background: rgba(110, 205, 242, 0.08); }
.intake-form { width: min(560px, calc(100vw - 48px)); }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.form-hint { margin: 0; font-size: 12px; color: var(--text-3); }
.field-error { color: #ff665f; font-size: 11px; }
.intake-form textarea {
  padding: 6px 10px; border: 1px solid var(--line-soft); border-radius: 7px;
  background: var(--surface-900); color: var(--text-1); font: inherit; font-size: 13px; resize: vertical;
}
.formal-layout { display: flex; flex-direction: column; gap: 16px; }
.link-action { align-self: flex-start; border: 0; background: transparent; color: var(--accent-blue); cursor: pointer; padding: 0; font-size: 12px; }
</style>
