<script setup lang="ts">
// R8: minimum usable resource & schedule workflow on the existing scheduling kernel.
// Resources (create, weekly calendar) → schedule items (create, reservations) → approval (server
// re-detects conflicts; blocking ones are listed with an explanation) → conflicts (manager override
// with reason; hard conflicts cannot be overridden) → approved history (immutable baselines) and
// cancellation (If-Match + reason, history kept). The server decides every permission.
import { computed, ref } from 'vue';
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import { RefreshCw, Plus } from 'lucide-vue-next';
import {
  listResources, createResource, getResourceCalendar, putResourceCalendar,
  listScheduleItems, createScheduleItem, listReservations, createReservation, cancelReservation,
  approveSchedule, cancelScheduleItem, listConflicts, overrideConflict, listBaselines,
  type CalendarRule,
} from '../cloud/portfolio';
import { ApiError, NetworkWriteError } from '../cloud/client';
import { createScopeFence, ScopeChangedError } from '../cloud/scope-fence';
import { resetPortfolioScope } from '../cloud/queryClient';
import type { PortfolioScope, Resource, ScheduleItem, ScheduleConflict } from '../../../../contracts/typescript/types.gen';

const props = defineProps<{ view: 'resource' | 'schedule' | 'conflict' | 'history'; scopes: PortfolioScope[] }>();
const qc = useQueryClient();
const fence = createScopeFence();

// Each tab re-reads on entry (others may have approved / overridden meanwhile).
const resourcesQuery = useQuery({ queryKey: ['resources'], queryFn: listResources, refetchOnMount: 'always' });
const itemsQuery = useQuery({ queryKey: ['schedule-items'], queryFn: listScheduleItems, refetchOnMount: 'always' });
const conflictsQuery = useQuery({ queryKey: ['conflicts'], queryFn: listConflicts, refetchOnMount: 'always' });
const baselinesQuery = useQuery({ queryKey: ['baselines'], queryFn: listBaselines, refetchOnMount: 'always' });

const roleOf = (scopeId: string | null | undefined) => props.scopes.find((s) => s.id === scopeId)?.myRole ?? null;
const isManager = (scopeId: string | null | undefined) => roleOf(scopeId) === 'portfolio_manager';
const canWrite = (scopeId: string | null | undefined) => roleOf(scopeId) !== null;
const writableScopes = computed(() => props.scopes.filter((s) => s.status === 'ACTIVE' && s.myRole));
const scopeName = (id: string | null | undefined) => props.scopes.find((s) => s.id === id)?.name ?? id ?? '—';
const resourceName = (id: string | null | undefined) => resourcesQuery.data.value?.find((r) => r.id === id)?.name ?? id ?? '—';
const itemName = (id: string | null | undefined) => itemsQuery.data.value?.find((i) => i.id === id)?.name ?? id ?? '—';

const RESOURCE_TYPES: Record<string, string> = { PERSON: '人员', TEAM: '班组', SITE: '场地', WORKSHOP: '车间', BAY: '工位', RUNWAY: '轨道', ASSET: '设备', TEST_EQUIPMENT: '测试设备', TOOL: '工具', VEHICLE: '车辆', COMMISSIONING_SLOT: '调试窗口', NETWORK_CUTOVER_SLOT: '网络割接窗口', OTHER: '其他' };
const SOURCE_TYPES: Record<string, string> = { PROJECT_ACTIVITY: '项目活动', WORK_ORDER: '工单', INSPECTION: '检验', CALIBRATION: '标定', COMMISSIONING: '调试', SITE_WINDOW: '现场窗口', NETWORK_CUTOVER: '网络割接' };
const STATUS: Record<string, string> = { PLANNED: '计划中', APPROVED: '已批准', IN_PROGRESS: '进行中', COMPLETED: '已完成', CANCELLED: '已取消', PENDING: '待批准', ACTIVE: '已生效', OPEN: '未解决', RESOLVED: '已消除', OVERRIDDEN: '已覆盖' };
const CONFLICT: Record<string, string> = { RESOURCE_OVERLAP: '独占资源时段重叠', RESOURCE_OVER_CAPACITY: '资源超出容量', CALENDAR_UNAVAILABLE: '资源日历不可用', DEPENDENCY_VIOLATION: '前置依赖未满足' };
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
const fmt = (v: string) => new Date(v).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });

// Pure (used in templates for load errors); write failures additionally re-read the scope on 403.
function errText(e: unknown): string {
  if (e instanceof ScopeChangedError || e instanceof NetworkWriteError) return e.message;
  if (e instanceof ApiError) {
    if (e.status === 403) { return '没有该项目组合中的此项权限（排期写入需规划员或经理；日历、覆盖、批准后撤销需项目组合经理）。'; }
    if (e.status === 404) return '对象不存在或不在您的项目组合内。';
    if (e.status === 409 && e.code === 'version_conflict') return '数据已被他人修改，请刷新后重试。';
    if (e.status === 409 && e.code === 'schedule_conflict') return '存在未解决的阻断冲突，未批准。请到「冲突」页处理（覆盖或调整资源/时段）后再批准。';
    if (e.status === 409) return `状态不允许此操作：${e.message}`;
    if (e.status === 422) return `输入不符合要求：${e.message}`;
    if (e.status === 503) return '服务暂不可用，请稍后重试（输入已保留）。';
    return e.message;
  }
  return '操作失败';
}
const actionError = ref('');
function failed(e: unknown) {
  if (e instanceof ApiError && e.status === 403) void resetPortfolioScope();
  notice.value = '';
  actionError.value = errText(e);
}
const notice = ref('');
function ok(msg: string) {
  actionError.value = '';
  notice.value = msg;
}
async function refresh(...keys: string[]) {
  await Promise.all(keys.map((k) => qc.invalidateQueries({ queryKey: [k] })));
}

// ---- resources -------------------------------------------------------------------------------
const resourceForm = ref<{ portfolioScopeId: string; resourceType: string; name: string; capacity: number; exclusive: boolean; overrideAllowed: boolean } | null>(null);
function openResource() {
  fence.pin();
  actionError.value = '';
  resourceForm.value = { portfolioScopeId: writableScopes.value[0]?.id ?? '', resourceType: 'TEAM', name: '', capacity: 1, exclusive: true, overrideAllowed: true };
}
const resourceMutation = useMutation({
  mutationFn: () => {
    const f = resourceForm.value!;
    return fence.guard(() => createResource({ id: '', portfolioScopeId: f.portfolioScopeId, resourceType: f.resourceType as Resource['resourceType'], referenceId: null, name: f.name.trim(), capacity: f.exclusive ? 1 : Number(f.capacity), exclusive: f.exclusive, overrideAllowed: f.overrideAllowed, homeTimezone: 'Asia/Shanghai', status: 'ACTIVE', version: 1 }));
  },
  onSuccess: async (r) => { resourceForm.value = null; ok(`已创建资源「${r.name}」。`); await refresh('resources'); },
  onError: failed,
});

// ---- calendar --------------------------------------------------------------------------------
const calendarFor = ref<Resource | null>(null);
const calendarRules = ref<CalendarRule[]>([]);
const calendarVersion = ref(0);
const calendarLoading = ref(false);
async function openCalendar(r: Resource) {
  fence.pin();
  actionError.value = '';
  calendarFor.value = r;
  calendarLoading.value = true;
  try {
    const c = await getResourceCalendar(r.id);
    calendarVersion.value = c.version;
    calendarRules.value = c.rules.map((x) => ({ ...x }));
  } catch (e) {
    failed(e);
    calendarFor.value = null;
  } finally {
    calendarLoading.value = false;
  }
}
function addRule() {
  const used = new Set(calendarRules.value.map((r) => r.weekday));
  const day = [1, 2, 3, 4, 5, 6, 0].find((d) => !used.has(d));
  if (day !== undefined) calendarRules.value.push({ weekday: day, startLocal: '08:00', endLocal: '18:00', availability: 'AVAILABLE' });
}
const calendarMutation = useMutation({
  mutationFn: () => fence.guard(() => putResourceCalendar(calendarFor.value!.id, 'Asia/Shanghai', calendarVersion.value, calendarRules.value)),
  onSuccess: (c) => { calendarFor.value = null; ok(`日历已保存（v${c.version}）。未列出的星期全天可用。`); },
  onError: failed,
});

// ---- schedule items --------------------------------------------------------------------------
const toLocalInput = (d: Date) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
const itemForm = ref<{ portfolioScopeId: string; name: string; sourceType: string; priority: string; start: string; end: string } | null>(null);
function openItem() {
  fence.pin();
  actionError.value = '';
  const start = new Date(); start.setDate(start.getDate() + 1); start.setHours(9, 0, 0, 0);
  const end = new Date(start); end.setHours(17);
  itemForm.value = { portfolioScopeId: writableScopes.value[0]?.id ?? '', name: '', sourceType: 'COMMISSIONING', priority: 'MEDIUM', start: toLocalInput(start), end: toLocalInput(end) };
}
const itemMutation = useMutation({
  mutationFn: () => {
    const f = itemForm.value!;
    return fence.guard(() => createScheduleItem({ id: '', projectId: null, portfolioScopeId: f.portfolioScopeId, sourceType: f.sourceType as ScheduleItem['sourceType'], sourceId: '', name: f.name.trim(), startAt: new Date(f.start).toISOString(), endAt: new Date(f.end).toISOString(), timezone: 'Asia/Shanghai', status: 'PLANNED', priority: f.priority as ScheduleItem['priority'], exclusiveLocationId: null, version: 1 }));
  },
  onSuccess: async (it) => { itemForm.value = null; ok(`已创建排期「${it.name}」，状态：计划中。为其预约资源后提交批准。`); await refresh('schedule-items'); },
  onError: failed,
});

// ---- reservations of one item ----------------------------------------------------------------
const selected = ref<ScheduleItem | null>(null);
const reservationsQuery = useQuery({
  queryKey: computed(() => ['reservations', selected.value?.id]),
  queryFn: () => listReservations(selected.value!.id),
  enabled: computed(() => !!selected.value),
});
const reserveForm = ref<{ resourceId: string; start: string; end: string; quantity: number } | null>(null);
function selectItem(it: ScheduleItem) {
  selected.value = it;
  reserveForm.value = null;
  actionError.value = '';
}
function openReserve() {
  fence.pin();
  const it = selected.value!;
  reserveForm.value = { resourceId: '', start: toLocalInput(new Date(it.startAt)), end: toLocalInput(new Date(it.endAt)), quantity: 1 };
}
const reserveMutation = useMutation({
  mutationFn: () => {
    const f = reserveForm.value!;
    return fence.guard(() => createReservation(selected.value!.id, { id: '', scheduleItemId: selected.value!.id, resourceId: f.resourceId, startAt: new Date(f.start).toISOString(), endAt: new Date(f.end).toISOString(), quantity: Number(f.quantity), status: 'PENDING', version: 1 }));
  },
  onSuccess: async () => { reserveForm.value = null; ok('已预约（待批准）。'); await refresh('reservations'); },
  onError: failed,
});
const cancelResMutation = useMutation({
  mutationFn: (r: { id: string; version: number }) => cancelReservation(r.id, r.version),
  onSuccess: async () => { ok('已撤回预约。'); await refresh('reservations'); },
  onError: failed,
});
const scopeResources = computed(() => (resourcesQuery.data.value ?? []).filter((r) => r.portfolioScopeId === selected.value?.portfolioScopeId && r.status === 'ACTIVE'));

const modalOpen = computed(() => !!(resourceForm.value || calendarFor.value || itemForm.value || reserveForm.value));

// ---- approval / cancel -----------------------------------------------------------------------
const approveMutation = useMutation({
  mutationFn: (id: string) => approveSchedule([id]),
  onSuccess: async () => { ok('排期已批准：预约生效，并生成不可修改的基线。'); await refresh('schedule-items', 'reservations', 'conflicts', 'baselines'); selected.value = null; },
  onError: async (e) => { failed(e); await refresh('conflicts'); },
});
const cancelItemMutation = useMutation({
  mutationFn: (v: { id: string; version: number; reason: string }) => cancelScheduleItem(v.id, v.version, v.reason),
  onSuccess: async () => { ok('排期已取消；预约已释放，审批基线保留。'); selected.value = null; await refresh('schedule-items', 'reservations', 'conflicts'); },
  onError: failed,
});
function cancelItem(it: ScheduleItem) {
  const reason = window.prompt(it.status === 'APPROVED' ? '取消已批准排期（释放资源，基线保留）。请输入原因：' : '撤销计划中的排期，请输入原因：');
  if (reason && reason.trim()) cancelItemMutation.mutate({ id: it.id, version: it.version, reason: reason.trim() });
}

// ---- conflicts -------------------------------------------------------------------------------
// The conflict contract has no scope id: derive it from the conflicting item (the server checks the
// item's scope); if the item is not listed, offer the button only when the caller manages every
// portfolio it belongs to.
const managesAll = computed(() => { const mine = props.scopes.filter((s) => s.myRole); return mine.length > 0 && mine.every((s) => s.myRole === 'portfolio_manager'); });
const canOverride = (c: ScheduleConflict) => {
  const scope = itemsQuery.data.value?.find((i) => i.id === c.leftItemId)?.portfolioScopeId;
  return scope ? isManager(scope) : managesAll.value;
};
// Mirrors the server rule: dependency violations and conflicts on a resource without overrideAllowed
// (except calendar unavailability) cannot be overridden.
const hard = (c: ScheduleConflict) => c.conflictType === 'DEPENDENCY_VIOLATION' || (!!c.resourceId && c.conflictType !== 'CALENDAR_UNAVAILABLE' && resourcesQuery.data.value?.find((r) => r.id === c.resourceId)?.overrideAllowed === false);
const overrideMutation = useMutation({
  mutationFn: (v: { id: number; reason: string }) => overrideConflict(v.id, v.reason),
  onSuccess: async () => { ok('冲突已覆盖（原因已记录）。如无其他阻断冲突，可回到「排期」提交批准。'); await refresh('conflicts'); },
  onError: failed,
});
function override(c: ScheduleConflict) {
  const reason = window.prompt('覆盖原因（至少 8 个字，将记录审计）：');
  if (reason) overrideMutation.mutate({ id: c.id, reason });
}
</script>

<template>
  <div class="schedule-board" :data-view="view">
    <p v-if="actionError && !modalOpen" class="form-error" role="alert" data-testid="schedule-action-error">{{ actionError }}</p>
    <p v-if="notice" class="workspace-notice" role="status" data-testid="schedule-notice">{{ notice }}</p>

    <!-- 资源 -->
    <template v-if="view === 'resource'">
      <div class="board-toolbar">
        <button v-if="writableScopes.length" class="primary-action" data-testid="resource-create" @click="openResource"><Plus :size="14" />新建资源</button>
        <button class="ghost-action" @click="resourcesQuery.refetch()"><RefreshCw :size="13" />刷新</button>
      </div>
      <p v-if="resourcesQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="resourcesQuery.isError.value" class="workspace-error" role="alert" data-testid="resource-error">资源加载失败：{{ errText(resourcesQuery.error.value) }}</p>
      <p v-else-if="!resourcesQuery.data.value?.length" class="workspace-empty" data-testid="resource-empty">暂无资源。{{ writableScopes.length ? '点击「新建资源」添加班组、轨道窗口、调试设备等。' : '' }}</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="resource-table">
          <thead><tr><th>名称</th><th>类型</th><th>项目组合</th><th>容量</th><th>独占</th><th>冲突可覆盖</th><th>状态</th><th>日历</th></tr></thead>
          <tbody>
            <tr v-for="r in resourcesQuery.data.value" :key="r.id" class="data-row" :data-resource-id="r.id">
              <td>{{ r.name }}</td><td>{{ RESOURCE_TYPES[r.resourceType] ?? r.resourceType }}</td><td>{{ scopeName(r.portfolioScopeId) }}</td>
              <td>{{ r.capacity }}</td><td>{{ r.exclusive ? '是' : '否' }}</td><td>{{ r.overrideAllowed ? '可覆盖' : '不可覆盖（硬约束）' }}</td>
              <td>{{ r.status === 'ACTIVE' ? '在用' : '停用' }}</td>
              <td><button class="ghost-action" data-testid="resource-calendar" @click="openCalendar(r)">{{ isManager(r.portfolioScopeId) ? '配置日历' : '查看日历' }}</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <!-- 排期 -->
    <template v-else-if="view === 'schedule'">
      <div class="board-toolbar">
        <button v-if="writableScopes.length" class="primary-action" data-testid="schedule-create" @click="openItem"><Plus :size="14" />新建排期</button>
        <button class="ghost-action" @click="itemsQuery.refetch()"><RefreshCw :size="13" />刷新</button>
      </div>
      <p v-if="itemsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="itemsQuery.isError.value" class="workspace-error" role="alert" data-testid="schedule-error">排期加载失败：{{ errText(itemsQuery.error.value) }}</p>
      <p v-else-if="!itemsQuery.data.value?.length" class="workspace-empty" data-testid="schedule-empty">暂无排期项。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="schedule-table">
          <thead><tr><th>名称</th><th>类型</th><th>项目组合</th><th>开始</th><th>结束</th><th>状态</th><th>版本</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="it in itemsQuery.data.value" :key="it.id" class="data-row" :data-item-id="it.id" :data-status="it.status" :class="{ 'row-focus': selected?.id === it.id }">
              <td>{{ it.name }}</td><td>{{ SOURCE_TYPES[it.sourceType] ?? it.sourceType }}</td><td>{{ scopeName(it.portfolioScopeId) }}</td>
              <td>{{ fmt(it.startAt) }}</td><td>{{ fmt(it.endAt) }}</td><td>{{ STATUS[it.status] ?? it.status }}</td><td>v{{ it.version }}</td>
              <td class="row-actions">
                <button class="ghost-action" data-testid="schedule-open" @click="selectItem(it)">资源预约</button>
                <button v-if="it.status === 'PLANNED' && isManager(it.portfolioScopeId)" class="primary-action" data-testid="schedule-approve" :disabled="approveMutation.isPending.value" @click="approveMutation.mutate(it.id)">批准</button>
                <button v-if="(it.status === 'PLANNED' && canWrite(it.portfolioScopeId)) || (it.status === 'APPROVED' && isManager(it.portfolioScopeId))" class="danger-action" data-testid="schedule-cancel" :disabled="cancelItemMutation.isPending.value" @click="cancelItem(it)">取消</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <section v-if="selected" class="reservation-panel" data-testid="reservation-panel">
        <header class="asset-location-head">
          <h2>「{{ selected.name }}」的资源预约</h2>
          <button v-if="selected.status === 'PLANNED' && canWrite(selected.portfolioScopeId)" class="primary-action" data-testid="reservation-create" @click="openReserve"><Plus :size="14" />预约资源</button>
        </header>
        <p v-if="reservationsQuery.isLoading.value" class="skeleton" aria-busy="true" style="height: 32px"></p>
        <p v-else-if="reservationsQuery.isError.value" class="workspace-error" role="alert">{{ errText(reservationsQuery.error.value) }}</p>
        <p v-else-if="!reservationsQuery.data.value?.length" class="workspace-empty" data-testid="reservation-empty">尚未预约资源。</p>
        <table v-else class="data-table" data-testid="reservation-table">
          <thead><tr><th>资源</th><th>开始</th><th>结束</th><th>数量</th><th>状态</th><th></th></tr></thead>
          <tbody>
            <tr v-for="r in reservationsQuery.data.value" :key="r.id" class="data-row" :data-status="r.status">
              <td>{{ resourceName(r.resourceId) }}</td><td>{{ fmt(r.startAt) }}</td><td>{{ fmt(r.endAt) }}</td><td>{{ r.quantity }}</td><td>{{ STATUS[r.status] ?? r.status }}</td>
              <td><button v-if="r.status === 'PENDING' && selected.status === 'PLANNED' && canWrite(selected.portfolioScopeId)" class="ghost-action" data-testid="reservation-cancel" @click="cancelResMutation.mutate({ id: r.id, version: r.version })">撤回</button></td>
            </tr>
          </tbody>
        </table>
        <p class="muted small">批准时服务器在资源锁内重新检测冲突：独占资源重叠、超容量、资源日历不可用、前置依赖。</p>
      </section>
    </template>

    <!-- 冲突 -->
    <template v-else-if="view === 'conflict'">
      <p v-if="conflictsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="conflictsQuery.isError.value" class="workspace-error" role="alert" data-testid="conflict-error">冲突加载失败：{{ errText(conflictsQuery.error.value) }}</p>
      <p v-else-if="!conflictsQuery.data.value?.length" class="workspace-empty" data-testid="conflict-empty">无未解决冲突。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="conflict-table">
          <thead><tr><th>冲突</th><th>严重度</th><th>排期</th><th>资源</th><th>状态</th><th>处理</th></tr></thead>
          <tbody>
            <tr v-for="c in conflictsQuery.data.value" :key="c.id" class="data-row" :data-conflict-id="c.id">
              <td>{{ CONFLICT[c.conflictType] ?? c.conflictType }}</td>
              <td><span class="status-pill" :data-status="c.severity === 'BLOCKING' ? 'OUT_OF_SERVICE' : 'DRAFT'">{{ c.severity === 'BLOCKING' ? '阻断' : '提示' }}</span></td>
              <td>{{ itemName(c.leftItemId) }}</td><td>{{ resourceName(c.resourceId) }}</td><td>{{ STATUS[c.status] ?? c.status }}</td>
              <td>
                <span v-if="hard(c)" class="muted small" data-testid="override-hard">硬约束不可覆盖：请撤回该资源预约或调整时段</span>
                <button v-else-if="canOverride(c)" class="ghost-action" data-testid="conflict-override" :disabled="overrideMutation.isPending.value" @click="override(c)">覆盖</button>
                <span v-else class="muted small" data-testid="override-not-allowed">需项目组合经理覆盖</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <!-- 已批准历史 -->
    <template v-else>
      <p v-if="baselinesQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="baselinesQuery.isError.value" class="workspace-error" role="alert">基线加载失败：{{ errText(baselinesQuery.error.value) }}</p>
      <p v-else-if="!baselinesQuery.data.value?.length" class="workspace-empty" data-testid="baseline-empty">尚无已批准排期。</p>
      <table v-else class="data-table" data-testid="baseline-table">
        <thead><tr><th>基线</th><th>项目组合</th><th>版本</th><th>批准人</th><th>批准时间</th></tr></thead>
        <tbody>
          <tr v-for="b in baselinesQuery.data.value" :key="b.id" class="data-row">
            <td>{{ b.name }}</td><td>{{ scopeName(b.portfolioScopeId) }}</td><td>v{{ b.baselineVersion }}</td><td>{{ b.approvedBy }}</td><td>{{ fmt(b.approvedAt) }}</td>
          </tr>
        </tbody>
      </table>
      <p class="muted small">每次批准生成一份不可修改的基线（排期与生效预约的快照）；之后的取消不会改写历史。</p>
    </template>

    <!-- dialogs -->
    <div v-if="resourceForm" class="modal-backdrop" @click.self="resourceForm = null">
      <form class="workspace-modal" data-testid="resource-form" @submit.prevent="resourceMutation.mutate()">
        <h2>新建资源</h2>
        <p v-if="actionError" class="form-error" role="alert" data-testid="schedule-action-error">{{ actionError }}</p>
        <label>项目组合<select v-model="resourceForm.portfolioScopeId" aria-label="资源项目组合" required><option v-for="s in writableScopes" :key="s.id" :value="s.id">{{ s.name }}</option></select></label>
        <label>名称<input v-model="resourceForm.name" aria-label="资源名称" required maxlength="200" placeholder="例如：调试一班 / 3 号轨道窗口" /></label>
        <label>类型<select v-model="resourceForm.resourceType" aria-label="资源类型"><option v-for="(t, k) in RESOURCE_TYPES" :key="k" :value="k">{{ t }}</option></select></label>
        <label class="inline"><input v-model="resourceForm.exclusive" type="checkbox" aria-label="独占" />独占（同一时段只能被一个排期使用，容量为 1）</label>
        <label v-if="!resourceForm.exclusive">容量<input v-model.number="resourceForm.capacity" type="number" min="1" max="10000" aria-label="资源容量" /></label>
        <label class="inline"><input v-model="resourceForm.overrideAllowed" type="checkbox" aria-label="冲突可覆盖" />冲突允许经理覆盖（取消勾选为硬约束）</label>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="resourceMutation.isPending.value" @click="resourceForm = null">取消</button>
          <button type="submit" class="primary-action" data-testid="resource-submit" :disabled="resourceMutation.isPending.value || !resourceForm.name.trim()">{{ resourceMutation.isPending.value ? '创建中…' : '创建' }}</button>
        </div>
      </form>
    </div>

    <div v-if="calendarFor" class="modal-backdrop" @click.self="calendarFor = null">
      <form class="workspace-modal" data-testid="calendar-form" @submit.prevent="calendarMutation.mutate()">
        <h2>「{{ calendarFor.name }}」每周可用时间（Asia/Shanghai，v{{ calendarVersion }}）</h2>
        <p v-if="actionError" class="form-error" role="alert" data-testid="schedule-action-error">{{ actionError }}</p>
        <p v-if="calendarLoading" class="skeleton" aria-busy="true" style="height: 32px"></p>
        <p v-else-if="!calendarRules.length" class="muted small">未设置规则：全周全天可用。</p>
        <div v-for="(r, i) in calendarRules" :key="i" class="calendar-rule" data-testid="calendar-rule">
          <select v-model.number="r.weekday" :aria-label="`规则${i + 1}星期`" :disabled="!isManager(calendarFor.portfolioScopeId)"><option v-for="(d, k) in WEEKDAYS" :key="k" :value="k">{{ d }}</option></select>
          <input v-model="r.startLocal" type="time" :aria-label="`规则${i + 1}开始`" :disabled="!isManager(calendarFor.portfolioScopeId)" />
          <input v-model="r.endLocal" type="time" :aria-label="`规则${i + 1}结束`" :disabled="!isManager(calendarFor.portfolioScopeId)" />
          <select v-model="r.availability" :aria-label="`规则${i + 1}可用性`" :disabled="!isManager(calendarFor.portfolioScopeId)"><option value="AVAILABLE">可用</option><option value="UNAVAILABLE">全天不可用</option></select>
          <button v-if="isManager(calendarFor.portfolioScopeId)" type="button" class="ghost-action" @click="calendarRules.splice(i, 1)">删除</button>
        </div>
        <div class="modal-actions">
          <button v-if="isManager(calendarFor.portfolioScopeId) && calendarRules.length < 7" type="button" class="ghost-action" data-testid="calendar-add" @click="addRule">添加一天</button>
          <button type="button" class="ghost-action" :disabled="calendarMutation.isPending.value" @click="calendarFor = null">关闭</button>
          <button v-if="isManager(calendarFor.portfolioScopeId)" type="submit" class="primary-action" data-testid="calendar-submit" :disabled="calendarMutation.isPending.value || calendarLoading">{{ calendarMutation.isPending.value ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </div>

    <div v-if="itemForm" class="modal-backdrop" @click.self="itemForm = null">
      <form class="workspace-modal" data-testid="schedule-form" @submit.prevent="itemMutation.mutate()">
        <h2>新建排期</h2>
        <p v-if="actionError" class="form-error" role="alert" data-testid="schedule-action-error">{{ actionError }}</p>
        <label>项目组合<select v-model="itemForm.portfolioScopeId" aria-label="排期项目组合" required><option v-for="s in writableScopes" :key="s.id" :value="s.id">{{ s.name }}</option></select></label>
        <label>名称<input v-model="itemForm.name" aria-label="排期名称" required maxlength="200" placeholder="例如：1 号行车调试" /></label>
        <label>类型<select v-model="itemForm.sourceType" aria-label="排期类型"><option v-for="(t, k) in SOURCE_TYPES" :key="k" :value="k">{{ t }}</option></select></label>
        <label>开始<input v-model="itemForm.start" type="datetime-local" aria-label="排期开始" required /></label>
        <label>结束<input v-model="itemForm.end" type="datetime-local" aria-label="排期结束" required /></label>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="itemMutation.isPending.value" @click="itemForm = null">取消</button>
          <button type="submit" class="primary-action" data-testid="schedule-submit" :disabled="itemMutation.isPending.value || !itemForm.name.trim()">{{ itemMutation.isPending.value ? '创建中…' : '创建' }}</button>
        </div>
      </form>
    </div>

    <div v-if="reserveForm && selected" class="modal-backdrop" @click.self="reserveForm = null">
      <form class="workspace-modal" data-testid="reservation-form" @submit.prevent="reserveMutation.mutate()">
        <h2>为「{{ selected.name }}」预约资源</h2>
        <p v-if="actionError" class="form-error" role="alert" data-testid="schedule-action-error">{{ actionError }}</p>
        <p v-if="!scopeResources.length" class="workspace-empty">该项目组合暂无在用资源，请先到「资源」页创建。</p>
        <label v-else>资源<select v-model="reserveForm.resourceId" aria-label="预约资源" required><option value="" disabled>选择资源</option><option v-for="r in scopeResources" :key="r.id" :value="r.id">{{ r.name }}（{{ RESOURCE_TYPES[r.resourceType] ?? r.resourceType }}{{ r.exclusive ? '，独占' : `，容量 ${r.capacity}` }}）</option></select></label>
        <label>开始<input v-model="reserveForm.start" type="datetime-local" aria-label="预约开始" required /></label>
        <label>结束<input v-model="reserveForm.end" type="datetime-local" aria-label="预约结束" required /></label>
        <label>数量<input v-model.number="reserveForm.quantity" type="number" min="1" aria-label="预约数量" /></label>
        <p class="muted small">预约时段须在排期时段之内。</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="reserveMutation.isPending.value" @click="reserveForm = null">取消</button>
          <button type="submit" class="primary-action" data-testid="reservation-submit" :disabled="reserveMutation.isPending.value || !reserveForm.resourceId">{{ reserveMutation.isPending.value ? '预约中…' : '预约' }}</button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.board-toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
.row-actions { display: flex; gap: 6px; flex-wrap: wrap; }
.row-focus { outline: 2px solid var(--accent, #2f6fed); outline-offset: -2px; }
.reservation-panel { margin-top: 14px; padding: 12px; border: 1px solid var(--line, #d9dee7); border-radius: 8px; background: var(--panel, #fff); }
.reservation-panel .data-table { width: 100%; }
.calendar-rule { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin: 6px 0; }
label.inline { display: flex; gap: 6px; align-items: center; flex-direction: row; }
.muted.small { font-size: 12px; }
@media (max-width: 640px) {
  .reservation-panel { overflow-x: auto; }
  .calendar-rule > * { flex: 1 1 40%; }
}
</style>
