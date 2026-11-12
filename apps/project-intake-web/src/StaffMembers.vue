<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ShieldCheck } from '@lucide/vue';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
import { ApiError } from './api-client';
import { clearStaffDraft, readStaffDraft, saveStaffDraft, type StaffDraftAction } from './staff-role-draft';
import { fieldLabels, fieldOrder, serverFieldErrors, validateStaff, type StaffErrors, type StaffField } from './staff-form';
import { when } from './model';
import { can } from './navigation';
import { newIdempotencyKey, accountActions, accountCause, accountDisplayLabel, accountDisplayState, accountOperationLabel, type AccountAction } from './staff-account';
import {
  capabilities, nextStep, revocable, roles, staffEvents, statusLabel, syncStatuses,
  type StaffEvent, type StaffMember, type StaffProject, type StaffTeam,
} from './staff-model';

type Dialog = 'invite' | 'edit' | 'verify' | 'permissions' | 'suspend' | 'reactivate' | 'retry' | 'archive' | 'transfer' |
  'promote' | 'demote' | 'review' | 'revoke' | AccountAction;
const app = useApp();
const route = useRoute();
const router = useRouter();
const memberId = computed(() => typeof route.params.id === 'string' ? route.params.id : '');
const administrator = computed(() => app.user?.role === 'baobei_admin');
const supervisor = computed(() => app.user?.role === 'baobei_manager');
const members = ref<StaffMember[]>([]);
const teams = ref<StaffTeam[]>([]);
const member = ref<StaffMember | null>(null);
const projects = ref<StaffProject[]>([]);
const events = ref<StaffEvent[]>([]);
const loading = ref(false);
const q = ref('');
const status = ref('');
const sort = ref('name');
const page = ref(1);
const total = ref(0);
const pageSize = ref(20);
const selected = ref<string[]>([]);
const dialog = ref<Dialog | null>(null);
const emptyForm = () => ({ name: '', phone: '', email: '', teamId: '', role: 'baobei_sales', note: '', reason: '', permissions: [] as string[] });
const form = ref(emptyForm());
const review = ref({ subject: '', username: '' });
const receipt = ref(''); // review draft (unverified), shown for identity operations
const errors = ref<StaffErrors>({});
const formIssue = ref('');
const target = ref('');
const targetSearch = ref('');
const targets = ref<StaffMember[]>([]);
const results = ref<{id: string; status: string}[]>([]);
const working = ref(false);
const reauthUntil = ref<number>(0);
const now = ref(Date.now());
setInterval(() => (now.value = Date.now()), 15_000);
const formVersion = ref(0);
// Account service (dedicated realm): one-time credential shown after create/reset.
const accountService = computed(() => can(app.user, 'staff.account'));
const credential = ref<{ login: string; temporaryPassword: string; name: string; action: string } | null>(null);
const accountPhone = ref('');
// One idempotency key per opened dialog: a retry after a timeout replays the same request.
const accountKey = ref('');

const titles: Record<Dialog, string> = {
  invite: '添加成员', edit: '编辑成员资料', permissions: '设置业务权限',
  verify: '重新核验企业身份', promote: '设为销售主管', demote: '恢复为普通销售',
  suspend: '停用账号', reactivate: '恢复账号', retry: '重试开通',
  archive: '归档账号', transfer: '批量移交项目', review: '审核开通',
  'account-create': '开通登录账号', 'account-reset': '重置登录密码', 'account-phone': '更换登录手机号',
  'account-disable': '停用登录', 'account-enable': '恢复登录',
  revoke: '撤销开户', 'account-revoke': '撤销开户',
};
const descriptions: Partial<Record<Dialog, string>> = {
  invite: '保存后成员进入“待开通”，由管理员审核并开通企业账号。',
  edit: '修改姓名、电话、邮箱或备注无需再次输入密码。',
  verify: '成员原登录会撤销，核验并重新登录后恢复访问。',
  promote: '提交后立即撤销该成员的旧会话；开通主管角色并重新登录后生效。',
  demote: '提交后立即撤销主管会话；移除主管角色并重新登录后恢复销售权限。',
  suspend: '立即撤销该成员的登录，未结束项目进入待移交清单。',
  reactivate: '恢复后成员进入“待开通”，管理员审核开通、成员重新登录后可用。',
  transfer: '仅移交选中项目，保留创建人、审批和重复裁决记录。',
  archive: '归档保留人员历史记录，不能再登录。',
  retry: '把失败的开通任务重新排队。',
  review: '确认此人可以加入报备中心及其角色。审核只记录业务批准，不代表企业身份已核验；身份运维核验后才会开通。',
  'account-create': '以工作手机号为登录账号，生成一次性初始密码。成员首次登录必须修改密码。',
  'account-reset': '生成新的一次性初始密码，成员当前登录全部失效，下次登录必须修改密码。',
  'account-phone': '登录手机号随工作手机一起更换。旧手机号立即不能登录，成员当前登录全部失效。',
  'account-disable': '立即停止该成员使用报备中心，未结束项目进入待移交清单。',
  'account-enable': '恢复登录账号和角色，成员重新登录后可用。',
  revoke: '撤销这次未完成的开户：成员记录标记为“已撤销开户”并保留历史，工作手机和邮箱释放，可用正确信息重新添加。',
  'account-revoke': '撤销这次未完成的开户：删除尚未使用的登录账号，成员记录标记为“已撤销开户”并保留历史，工作手机和邮箱释放，可重新添加。',
};
const canManage = computed(() => member.value?.canManage ?? false);
const showRoleAction = computed(() => administrator.value && canManage.value && member.value?.status === 'ACTIVE' &&
  ['baobei_sales', 'baobei_manager'].includes(member.value.role));
const failureReason = computed(() => member.value?.status === 'PROVISION_FAILED' || member.value?.syncStatus === 'FAILED'
  ? events.value.find(event => event.action === 'staff.provision.fail')?.reason : '');
const needsTeam = computed(() => ['baobei_sales', 'baobei_manager'].includes(form.value.role));
const profileDialog = computed(() => dialog.value === 'invite' || dialog.value === 'edit');
const reauthActive = computed(() => reauthUntil.value > now.value);
const reauthMinutes = computed(() => Math.max(1, Math.ceil((reauthUntil.value - now.value) / 60_000)));

// Mirrors server needsApproval(...sensitive): only these ask for enterprise reauthentication.
const sensitive = computed(() => {
  const kind = dialog.value, m = member.value;
  if (!administrator.value || !kind) return false;
  if (kind === 'promote' || kind === 'demote' || kind === 'verify') return true;
  // Server rule: account actions on advanced roles (not disabling) need a fresh password check.
  if (kind.startsWith('account-')) return !!m && m.role !== 'baobei_sales' && kind !== 'account-disable' && kind !== 'account-revoke';
  if (kind === 'invite') return form.value.role !== 'baobei_sales';
  if (kind === 'reactivate') return m?.role !== 'baobei_sales';
  if (kind === 'edit' && m) return form.value.role !== m.role ||
    form.value.email.trim().toLowerCase() !== m.email && m.role !== 'baobei_sales' ||
    form.value.teamId !== m.teamId && m.role === 'baobei_manager';
  return false;
});
const needsReauth = computed(() => sensitive.value && !reauthActive.value);
const reasonRequired = computed(() => {
  const kind = dialog.value;
  if (!kind || kind === 'invite' || kind === 'account-create' || kind === 'account-enable') return false;
  if (kind === 'edit') return !!member.value && (form.value.role !== member.value.role || form.value.teamId !== member.value.teamId);
  return true;
});
const errorList = computed(() => fieldOrder.filter(f => errors.value[f]).map(f => ({ field: f, label: fieldLabels[f], message: errors.value[f]! })));

async function load() {
  loading.value = true;
  try {
    if (memberId.value) {
      const path = '/api/v1/staff/' + memberId.value;
      const [detail, history] = await Promise.all([
        app.api<{member: StaffMember; projects: StaffProject[]; reauthExpiresAt: string | null}>(path),
        app.api<{items: StaffEvent[]}>(path + '/audit'),
      ]);
      member.value = detail.member;
      reauthUntil.value = detail.reauthExpiresAt ? Date.parse(detail.reauthExpiresAt) : 0;
      projects.value = detail.projects;
      events.value = history.items;
      selected.value = selected.value.filter(id => projects.value.some(project => project.id === id));
    } else {
      const params = new URLSearchParams({q: q.value, status: status.value, sort: sort.value, page: String(page.value)});
      const result = await app.api<{items: StaffMember[]; total: number; pageSize: number; reauthExpiresAt: string | null}>('/api/v1/staff?' + params);
      members.value = result.items;
      total.value = result.total;
      pageSize.value = result.pageSize;
      reauthUntil.value = result.reauthExpiresAt ? Date.parse(result.reauthExpiresAt) : 0;
    }
  } finally {
    loading.value = false;
  }
}

// Keep the form, go to the enterprise login page (fresh password), come back here.
function reauthenticateAndResume() {
  const kind = dialog.value;
  if (!app.user || !kind || !['invite', 'edit', 'promote', 'demote', 'verify', 'reactivate'].includes(kind)) return;
  if (!checkForm()) return;
  saveStaffDraft({
    actor: app.user.subject, staffId: kind === 'invite' ? '' : member.value?.id ?? '', action: kind as StaffDraftAction,
    version: kind === 'invite' ? 0 : formVersion.value, form: { ...form.value, permissions: [...form.value.permissions] },
    expiresAt: Date.now() + 300_000,
  });
  window.location.assign('/auth/login?reauth=1');
}

async function changePage(next: number) {
  page.value = next;
  await load();
}

async function searchFromInput() {
  try { await changePage(1); } catch (error) { app.error = (error as Error).message; }
}

async function findTargets() {
  const params = new URLSearchParams({status: 'ACTIVE', q: targetSearch.value});
  const result = await app.api<{items: StaffMember[]}>('/api/v1/staff?' + params);
  targets.value = result.items.filter(item => item.role === 'baobei_sales' && item.id !== memberId.value);
  if (!targets.value.some(item => item.id === target.value)) target.value = '';
}

async function open(kind: Dialog) {
  accountKey.value = newIdempotencyKey();
  accountPhone.value = '';
  const existing = kind === 'invite' ? null : member.value;
  formVersion.value = existing?.version ?? 0;
  errors.value = {};
  formIssue.value = '';
  receipt.value = '';
  review.value = { subject: '', username: '' };
  form.value = existing ? {
    name: existing.name, phone: existing.phone, email: existing.email, teamId: existing.teamId, role: existing.role,
    note: existing.note, reason: '', permissions: [...existing.permissions],
  } : { ...emptyForm(), teamId: supervisor.value ? app.user?.teamId ?? '' : '', permissions: Object.keys(capabilities) };
  if (kind === 'invite' || kind === 'edit') {
    teams.value = (await app.api<{items: StaffTeam[]}>('/api/v1/teams')).items;
  }
  if (kind === 'transfer') {
    targetSearch.value = '';
    target.value = '';
    await findTargets();
  }
  dialog.value = kind;
}

function checkForm(): boolean {
  const kind = dialog.value;
  if (kind === 'invite' || kind === 'edit') {
    errors.value = validateStaff(form.value, {
      phoneRequired: kind === 'invite' || !!member.value?.phone, reasonRequired: reasonRequired.value,
    });
  } else if (kind === 'verify') {
    errors.value = validateStaff({ ...form.value, name: member.value?.name ?? 'x', phone: '' }, { phoneRequired: false, reasonRequired: true });
  } else if (kind === 'transfer' && (!target.value || !selected.value.length)) {
    errors.value = form.value.reason.trim() ? {} : { reason: '请填写操作原因' };
    formIssue.value = '请选择接收销售';
  } else {
    errors.value = reasonRequired.value && !form.value.reason.trim() ? { reason: '请填写操作原因' } : {};
  }
  if (errorList.value.length || formIssue.value) {
    void focusFirstError();
    return false;
  }
  return true;
}

async function focusFirstError() {
  await nextTick();
  const first = errorList.value[0]?.field;
  (document.querySelector<HTMLElement>(`[data-field="${first}"]`) ?? document.querySelector<HTMLElement>('.form-summary'))?.focus();
}

function focusField(field: StaffField) {
  document.querySelector<HTMLElement>(`[data-field="${field}"]`)?.focus();
}

// Account actions: Keycloak is called by the server; the one-time password, if any,
// is shown once in its own dialog and kept only in memory until that dialog closes.
async function saveAccount(kind: AccountAction) {
  if (!member.value) return;
  const body: Record<string, string> = { reason: form.value.reason.trim() };
  if (kind === 'account-phone') {
    errors.value = validateStaff({ ...form.value, name: member.value.name, phone: accountPhone.value, role: 'baobei_director' },
      { phoneRequired: true, reasonRequired: true });
    if (errors.value.phone || errors.value.reason) return void focusFirstError();
    body.phone = accountPhone.value.trim();
  } else if (reasonRequired.value && !body.reason) {
    errors.value = { reason: '请填写操作原因' };
    return void focusFirstError();
  }
  const r = await fetch('/api/v1/staff/' + member.value.id + '/account/' + accountActions[kind], {
    method: 'POST', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': app.user?.csrfToken || '', 'Idempotency-Key': accountKey.value },
    body: JSON.stringify(body),
  }).catch(() => null);
  if (!r) throw new ApiError('网络连接失败，请检查网络后重试；再次提交不会重复操作', 0, 'NETWORK');
  const data = await r.json().catch(() => ({}));
  if (!r.ok) {
    // Show the server's current state (处理中 / 待核对 / 开通失败) behind the dialog;
    // the dialog and everything typed stay as they are.
    await load().catch(() => undefined);
    throw new ApiError(data.message || '操作未完成，请重试', r.status, data.code || '');
  }
  member.value = data.member;
  dialog.value = null;
  if (data.credential) credential.value = { ...data.credential, name: data.member.name, action: kind === 'account-create' ? '登录账号已开通' : '密码已重置' };
  else app.toast(data.replayed ? '该操作已完成（重复提交未再次执行）' : { 'account-phone': '登录手机号已更换', 'account-disable': '登录已停用', 'account-enable': '登录已恢复', 'account-revoke': '已撤销开户，登录账号已删除，工作手机已释放' }[kind as string] ?? '已保存');
  await load();
}

function closeCredential() {
  const wasShown = !!credential.value;
  credential.value = null;
  if (wasShown) app.toast('初始密码已关闭，无法再次查看；如成员未收到，可在成员详情中“重置密码”生成新的初始密码');
}

// "核对账号状态": the server reads the real login state and completes, undoes or
// keeps the operation for review. No request body, nothing is repeated.
async function reconcileAccount() {
  if (!member.value) return;
  const r = await fetch('/api/v1/staff/' + member.value.id + '/account/reconcile', {
    method: 'POST', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': app.user?.csrfToken || '' },
    body: '{}',
  }).catch(() => null);
  if (!r) throw new ApiError('网络连接失败，请检查网络后重试', 0, 'NETWORK');
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new ApiError(data.message || '核对未完成，请稍后重试', r.status, data.code || '');
  await load();
  app.toast(accountDisplayState(member.value!) === 'REVIEW' ? '仍无法确认账号状态，登录保持暂停；请稍后再次核对' : '账号状态已核对');
}

async function copyCredential() {
  if (!credential.value) return;
  await navigator.clipboard.writeText(`登录手机号：${credential.value.login}\n初始密码：${credential.value.temporaryPassword}\n首次登录后请立即修改密码。`);
  app.toast('账号和初始密码已复制');
}

async function save() {
  const kind = dialog.value;
  if (!kind || working.value) return;
  formIssue.value = '';
  if (kind.startsWith('account-')) {
    if (needsReauth.value) {
      formIssue.value = '此操作需要先再次输入你的登录密码。';
      return;
    }
    working.value = true;
    try {
      await saveAccount(kind as AccountAction);
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.code === 'STAFF_REAUTH_REQUIRED') reauthUntil.value = 0;
        const field = serverFieldErrors[error.code] ?? (error.code.startsWith('ACCOUNT_PHONE') ? 'phone' : undefined);
        if (field) {
          errors.value = { [field]: error.message };
          void focusFirstError();
        } else formIssue.value = error.message;
        return;
      }
      throw error;
    } finally {
      working.value = false;
    }
    return;
  }
  if (!checkForm()) return;
  if (needsReauth.value) {
    formIssue.value = '此操作需要先再次输入你的登录密码。';
    return;
  }
  working.value = true;
  const path = '/api/v1/staff/' + memberId.value;
  const reason = form.value.reason.trim();
  const version = formVersion.value;
  const f = form.value;
  try {
    if (kind === 'promote' || kind === 'demote' || kind === 'verify') {
      const person = member.value;
      if (!person) return;
      const role = kind === 'promote' ? 'baobei_manager' : kind === 'demote' ? 'baobei_sales' : person.role;
      await app.api(path, 'PATCH', {
        name: person.name, phone: person.phone, email: kind === 'verify' ? f.email : person.email,
        teamId: person.teamId, note: person.note, role, reason, version,
        ...(kind === 'verify' ? {verifyIdentity: true} : {}),
      });
    } else if (kind === 'invite' || kind === 'edit') {
      const payload = {
        name: f.name.trim(), phone: f.phone.trim(), email: f.email.trim(), teamId: needsTeam.value ? f.teamId : '',
        role: f.role, note: f.note, reason,
      };
      if (kind === 'invite') {
        const created = await app.api<StaffMember>('/api/v1/staff/invitations', 'POST', {
          ...payload, permissions: payload.role === 'baobei_sales' ? f.permissions : Object.keys(capabilities),
        });
        clearStaffDraft();
        dialog.value = null;
        app.toast('已添加成员，状态：待开通');
        await router.push('/team/members/' + created.id);
        return;
      }
      await app.api(path, 'PATCH', {...payload, version});
    } else if (kind === 'review') {
      const result = await app.api<{draft: unknown}>(path + '/provision-review', 'POST', {
        version, reason, subject: review.value.subject.trim(), username: review.value.username.trim(),
      });
      receipt.value = JSON.stringify(result.draft, null, 2);
      app.toast('审核已通过，等待企业身份核验');
      await load();
      return;
    } else if (kind === 'permissions') {
      await app.api(path + '/permissions', 'PATCH', {version, reason, permissions: f.permissions});
    } else if (kind === 'transfer') {
      const result = await app.api<{items: {id: string; status: string}[]}>(path + '/ownership-transfer-batch', 'POST', {
        targetId: target.value, version, reason,
        projects: projects.value.filter(item => selected.value.includes(item.id)).map(item => ({id: item.id, version: item.version})),
      });
      results.value = result.items.map(item => ({id: projects.value.find(project => project.id === item.id)?.number ?? item.id, status: item.status}));
      selected.value = [];
    } else {
      await app.api(path + '/' + kind, 'POST', {version, reason});
    }
    clearStaffDraft();
    dialog.value = null;
    app.toast({
      promote: '已提交主管授权，等待开通', demote: '已提交恢复为销售，等待开通', verify: '已提交身份核验，等待开通',
      reactivate: '已申请恢复，状态：待开通', suspend: '账号已停用', archive: '账号已归档', retry: '已重新排队', revoke: '已撤销开户，工作手机已释放，可重新添加',
      permissions: '业务权限已保存', edit: '成员资料已保存', transfer: '移交已完成',
    }[kind as string] ?? '已保存');
    await load();
  } catch (error) {
    // Keep every input; show the problem next to the field or at the top of the form.
    if (error instanceof ApiError) {
      if (error.code === 'STAFF_REAUTH_REQUIRED') {
        reauthUntil.value = 0;
        formIssue.value = '密码验证已超时，请重新验证后继续。表单内容会保留。';
        return;
      }
      const field = serverFieldErrors[error.code] as StaffField | undefined;
      if (field) {
        errors.value = { [field]: error.message };
        void focusFirstError();
        return;
      }
      formIssue.value = error.message;
      return;
    }
    throw error;
  } finally {
    working.value = false;
  }
}

async function copyReceipt() {
  await navigator.clipboard.writeText(receipt.value);
  app.toast('待核验草稿已复制');
}

onMounted(async () => {
  try {
    await load();
    // Entry from the workspace "添加成员": open the add form right away, then
    // drop the flag so refresh/back does not reopen it.
    if (route.query.add === '1' && !memberId.value) {
      void router.replace({ query: { ...route.query, add: undefined } });
      await open('invite');
      return;
    }
    const draft = readStaffDraft();
    if (!draft || route.query.resume !== draft.action) return;
    clearStaffDraft();
    if (!administrator.value || draft.actor !== app.user?.subject) return;
    if (draft.action === 'invite' ? !!memberId.value : draft.staffId !== member.value?.id || draft.version !== member.value?.version) {
      app.error = '成员记录已被他人修改，请重新核对后再操作。';
      return;
    }
    await open(draft.action);
    form.value = { ...draft.form, permissions: [...draft.form.permissions] };
    if (reauthActive.value) app.toast(`密码验证成功，${reauthMinutes.value} 分钟内可完成此操作`);
    else formIssue.value = '密码验证未完成或已超时，请重新验证。';
  } catch (error) { app.error = (error as Error).message; }
});
</script>

<template>
  <section class="staff-page">
    <div class="page-heading">
      <div>
        <h1>{{ memberId ? '成员详情' : administrator ? '人员管理' : '团队成员' }}</h1>
        <p class="muted">{{ memberId ? '资料、账号状态、业务权限与项目交接' : '添加成员、开通企业账号、调整权限与停用' }}</p>
      </div>
      <div class="actions">
        <span v-if="administrator && reauthActive" class="reauth-chip" role="status">
          <ShieldCheck :size="15" aria-hidden="true" />已验证密码 · {{ reauthMinutes }} 分钟内有效
        </span>
        <ActionButton v-if="!memberId" action-id="staff.invite" primary :run="() => open('invite')">添加成员</ActionButton>
        <ActionButton v-else action-id="staff.back" :run="() => router.push(administrator ? '/settings/users' : '/team/members')">返回成员列表</ActionButton>
      </div>
    </div>

    <div v-if="!memberId">
      <div class="staff-filters panel">
        <label>姓名、邮箱或手机<input v-model="q" aria-label="姓名、邮箱或手机" maxlength="254" @keyup.enter="searchFromInput" /></label>
        <label>账号状态<select v-model="status" @change="searchFromInput"><option value="">全部状态</option>
          <option value="PENDING_PROVISION">待开通 / 待身份核验</option>
          <option value="PENDING_ACTIVATION">待首次登录</option>
          <option value="ACTIVE">已启用</option>
          <option value="PROVISION_FAILED">开通失败</option>
          <option value="SUSPENDED">已停用</option>
          <option value="ARCHIVED">已归档</option>
          <option value="REVOKED">已撤销开户</option>
        </select></label>
        <label>排序<select v-model="sort" @change="searchFromInput">
          <option value="name">姓名</option><option value="email">邮箱</option>
          <option value="lastLogin">最近登录</option><option value="projects">项目数</option>
        </select></label>
        <ActionButton action-id="staff.search" :run="() => changePage(1)" :disabled="loading">搜索</ActionButton>
      </div>
      <p v-if="loading" role="status">正在加载成员…</p>
      <div v-else-if="!members.length" class="staff-empty panel">
        <p>没有符合条件的成员。</p>
        <ActionButton v-if="!q && !status" action-id="staff.invite-empty" primary :run="() => open('invite')">添加第一位成员</ActionButton>
      </div>
      <div v-else class="staff-table" role="table" aria-label="成员列表">
        <div class="staff-table-head" role="row">
          <span role="columnheader">成员</span><span role="columnheader">团队与角色</span>
          <span role="columnheader">账号状态</span><span role="columnheader">下一步</span><span role="columnheader">项目 · 最近登录</span>
          <span role="columnheader"><span class="sr-only">操作</span></span>
        </div>
        <article v-for="item in members" :key="item.id" class="staff-row" role="row">
          <div class="staff-name" role="cell">
            <strong>{{ item.name }}</strong>
            <span class="muted">{{ item.phone || '手机待补全' }}<template v-if="item.email"> · {{ item.email }}</template></span>
          </div>
          <span role="cell">{{ item.team || '全局' }} · {{ roles[item.role] }}</span>
          <span role="cell"><span class="staff-status" :data-state="item.status">{{ statusLabel(item) }}</span></span>
          <span role="cell" class="staff-next">{{ nextStep(item, administrator, accountService) || '—' }}</span>
          <span role="cell" class="muted">{{ item.projectCount }} 项 · {{ when(item.lastLogin) }}</span>
          <span role="cell"><ActionButton action-id="staff.detail" :run="() => router.push('/team/members/' + item.id)" :aria-label="'查看' + item.name">
            {{ administrator && item.status === 'PENDING_PROVISION' && !item.provisionReviewed ? '审核开通' :
              administrator && item.status === 'PROVISION_FAILED' ? '查看原因' : '查看详情' }}
          </ActionButton></span>
        </article>
      </div>
      <div class="actions staff-pagination">
        <span class="muted">共 {{ total }} 人 · 第 {{ page }} 页</span>
        <ActionButton action-id="staff.previous" :disabled="page <= 1 || loading" :run="() => changePage(page - 1)">上一页</ActionButton>
        <ActionButton action-id="staff.next" :disabled="page * pageSize >= total || loading" :run="() => changePage(page + 1)">下一页</ActionButton>
      </div>
    </div>
    <p v-else-if="loading && !member" role="status">正在加载成员…</p>
    <template v-else-if="member">
      <div class="staff-card panel">
        <div class="staff-title"><h2>{{ member.name }}</h2><span class="staff-status" :data-state="member.status">{{ statusLabel(member) }}</span></div>
        <dl class="staff-facts">
          <div><dt>工作手机</dt><dd>{{ member.phone || '待补全' }}</dd></div>
          <div><dt>工作邮箱</dt><dd>{{ member.email || '未填写（选填）' }}</dd></div>
          <div><dt>{{ member.roleChanged && member.status !== 'ACTIVE' ? '团队与目标角色' : '团队与角色' }}</dt><dd>{{ member.team || '全局' }} · {{ roles[member.role] }}</dd></div>
          <div><dt>最近登录</dt><dd>{{ when(member.lastLogin) }}</dd></div>
          <div v-if="administrator && accountService"><dt>登录账号</dt><dd data-testid="account-state">{{ member.loginName || '未开通' }} · {{ accountDisplayLabel[accountDisplayState(member)] ?? member.accountState }}<template v-if="member.mustChangePassword && accountDisplayState(member) === 'READY'">（待首次修改密码）</template></dd></div>
          <div v-else-if="administrator"><dt>企业账号</dt><dd>{{ member.identityVerified ? '已绑定并核验' : member.identityLinked ? '已绑定，待核验' : '尚未绑定' }}</dd></div>
          <div><dt>备注</dt><dd>{{ member.note || '—' }}</dd></div>
        </dl>
        <ol v-if="accountService && !['ACTIVE','ARCHIVED','SUSPENDED','REVOKED'].includes(member.status)" class="staff-progress" aria-label="开通进度">
          <li :data-done="true">已添加成员</li>
          <li :data-done="['READY','DISABLED'].includes(accountDisplayState(member))" :data-current="['NONE','CREATING','PROCESSING'].includes(accountDisplayState(member))" :data-failed="['FAILED','REVIEW'].includes(accountDisplayState(member))">开通登录账号</li>
          <li :data-current="member.status === 'PENDING_ACTIVATION'">成员首次登录并修改密码</li>
        </ol>
        <ol v-else-if="!['ACTIVE','ARCHIVED','REVOKED'].includes(member.status)" class="staff-progress" aria-label="开通进度">
          <li :data-done="true">已添加成员</li>
          <li :data-done="member.provisionReviewed || ['PENDING_ACTIVATION','ACTIVE'].includes(member.status)" :data-current="member.status === 'PENDING_PROVISION' && !member.provisionReviewed">管理员审核开通</li>
          <li :data-done="['PENDING_ACTIVATION','ACTIVE'].includes(member.status)" :data-current="member.status === 'PENDING_PROVISION' && member.provisionReviewed" :data-failed="member.status === 'PROVISION_FAILED'">企业身份核验并绑定</li>
          <li :data-current="member.status === 'PENDING_ACTIVATION'">成员首次登录激活</li>
        </ol>
        <p v-if="nextStep(member, administrator, accountService)" class="staff-hint" role="status">{{ nextStep(member, administrator, accountService) }}</p>
        <p v-if="failureReason" class="staff-hint" data-tone="risk" role="status">失败原因：{{ failureReason }}</p>
        <p v-if="member.status === 'REVOKED'" class="staff-hint" role="status" data-testid="revoked-hint">该开户已撤销，记录仅供查阅；工作手机已释放，可用正确信息重新添加成员。</p>
        <p
          v-if="administrator && accountService && member.accountOperation" class="staff-hint" data-testid="account-operation"
          :data-tone="member.accountOperation.state === 'STARTED' ? undefined : 'risk'" role="status"
        >
          {{ accountOperationLabel[member.accountOperation.operation] ?? '账号操作' }}{{
            member.accountOperation.state === 'STARTED' ? '处理中，系统会自动核对结果，请稍后刷新。' :
            member.accountOperation.state === 'REVIEW' ? '待核对：' + accountCause(member.accountOperation.detail) + '。核对完成前该成员不能登录。' :
            '未完成：' + accountCause(member.accountOperation.detail) + '。可重新发起。' }}
        </p>
        <p v-if="showRoleAction && !member.identityVerified" class="muted">
          {{ member.identityLinked ? '变更角色前需先重新核验企业身份。' : '请联系身份运维核对企业账号标识。' }}
        </p>
        <div v-if="canManage" class="actions">
          <template v-if="administrator && accountService && ['REVIEW','PROCESSING'].includes(accountDisplayState(member))">
            <ActionButton action-id="staff.account-reconcile" primary :run="reconcileAccount">核对账号状态</ActionButton>
          </template>
          <template v-else-if="administrator && accountService">
            <ActionButton
              v-if="['PENDING_PROVISION','PROVISION_FAILED'].includes(member.status) && member.accountState !== 'READY' || member.accountState === 'NONE' && member.status !== 'SUSPENDED'"
              action-id="staff.account-create" primary :disabled="!member.phone" reason="请先编辑资料填写工作手机" :run="() => open('account-create')"
            >
              开通登录账号
            </ActionButton>
            <ActionButton v-if="['READY','FAILED'].includes(member.accountState) && member.identityLinked && member.status !== 'SUSPENDED'" action-id="staff.account-reset" :run="() => open('account-reset')">重置密码</ActionButton>
            <ActionButton v-if="member.accountState !== 'NONE' && member.identityLinked && member.status !== 'SUSPENDED'" action-id="staff.account-phone" :run="() => open('account-phone')">更换登录手机号</ActionButton>
            <ActionButton v-if="member.status === 'SUSPENDED' && member.identityLinked" action-id="staff.account-enable" primary :run="() => open('account-enable')">恢复登录</ActionButton>
            <ActionButton v-if="member.status !== 'SUSPENDED' && member.identityLinked && member.accountState !== 'NONE'" action-id="staff.account-disable" :run="() => open('account-disable')">停用登录</ActionButton>
          </template>
          <ActionButton
            v-if="administrator && !accountService && member.status === 'PENDING_PROVISION' && member.syncStatus === 'PENDING'"
            action-id="staff.provision-review" primary :run="() => open('review')"
          >
            {{ member.provisionReviewed ? '重新生成待核验草稿' : '审核开通' }}
          </ActionButton>
          <ActionButton
            v-if="showRoleAction && member.role === 'baobei_sales'" action-id="staff.promote"
            :disabled="!member.identityVerified" reason="请先核验企业身份" :run="() => open('promote')"
          >
            设为销售主管
          </ActionButton>
          <ActionButton
            v-if="showRoleAction && member.role === 'baobei_manager'" action-id="staff.demote"
            :disabled="!member.identityVerified" reason="请先核验企业身份" :run="() => open('demote')"
          >
            恢复为普通销售
          </ActionButton>
          <ActionButton
            v-if="showRoleAction && member.identityLinked && !member.identityVerified"
            action-id="staff.verify-identity" :run="() => open('verify')"
          >
            重新核验企业身份
          </ActionButton>
          <ActionButton
            v-if="revocable(member) && (administrator || !(member.identityLinked && member.accountState !== 'NONE'))" action-id="staff.revoke"
            :run="() => open(member!.identityLinked && member!.accountState !== 'NONE' ? 'account-revoke' : 'revoke')"
          >
            撤销开户
          </ActionButton>
          <ActionButton action-id="staff.edit" :run="() => open('edit')">编辑资料</ActionButton>
          <ActionButton v-if="!accountService && (member.status === 'PROVISION_FAILED' || member.status === 'SUSPENDED' && member.syncStatus === 'FAILED')" action-id="staff.retry" :run="() => open('retry')">重试开通</ActionButton>
          <ActionButton v-if="member.status === 'SUSPENDED' && !(administrator && accountService && member.identityLinked)" action-id="staff.reactivate" :run="() => open('reactivate')">恢复账号</ActionButton>
          <ActionButton v-if="administrator && member.status === 'SUSPENDED'" action-id="staff.archive" :run="() => open('archive')">归档账号</ActionButton>
          <ActionButton v-if="member.status !== 'SUSPENDED' && !(administrator && accountService && member.identityLinked && member.accountState !== 'NONE')" action-id="staff.suspend" :run="() => open('suspend')">停用账号</ActionButton>
        </div>
      </div>
      <div v-if="member.role === 'baobei_sales'" class="staff-card panel">
        <h2>业务权限</h2>
        <ul class="staff-permissions"><li v-for="(label, key) in capabilities" :key="key" :data-on="member.permissions.includes(key)">{{ label }}<span>{{ member.permissions.includes(key) ? '允许' : '关闭' }}</span></li></ul>
        <ActionButton v-if="canManage" action-id="staff.permissions" :run="() => open('permissions')">设置业务权限</ActionButton>
      </div>
      <div class="staff-card panel">
        <h2>名下项目 · {{ member.projectCount }}</h2>
        <p v-if="member.role !== 'baobei_sales'" class="muted">人员管理仅展示项目计数。</p>
        <template v-else>
          <p v-if="!projects.length" class="muted">没有待移交的未结束项目</p>
          <label v-for="item in projects" :key="item.id" class="staff-check">
            <input v-model="selected" type="checkbox" :value="item.id" :disabled="!canManage" />
            <span>{{ item.number }} · {{ item.name }}{{ item.queued ? ' · 待移交' : '' }}</span>
          </label>
          <ActionButton v-if="canManage" action-id="staff.transfer" :disabled="!selected.length" reason="请先勾选要移交的项目" :run="() => open('transfer')">移交所选项目</ActionButton>
          <ul v-if="results.length" aria-label="移交结果">
            <li v-for="item in results" :key="item.id">
              {{ item.id }} · {{ ({
                TRANSFERRED: '已移交', ALREADY_TRANSFERRED: '此前已移交', VERSION_CONFLICT: '版本冲突，请刷新后重试', NOT_AVAILABLE: '当前不可移交',
              } as Record<string, string>)[item.status] }}
            </li>
          </ul>
        </template>
      </div>
      <div class="staff-card panel">
        <h2>操作历史</h2>
        <p v-if="!events.length" class="muted">暂无人员操作记录</p>
        <ol class="staff-audit">
          <li v-for="(event,index) in events" :key="index">
            <div><strong>{{ staffEvents[event.action] || event.action }}</strong><span class="muted">{{ event.actor }} · {{ when(event.at) }}</span></div>
            <p>{{ event.reason }}</p>
          </li>
        </ol>
        <p class="muted staff-meta">人员编号 {{ member.id }} · 版本 {{ member.version }} · 身份任务 {{ syncStatuses[member.syncStatus] }}</p>
      </div>
    </template>

    <Modal
      :open="dialog !== null" :title="dialog ? titles[dialog] : ''" :description="dialog ? descriptions[dialog] : undefined" sheet
      @update:open="value => { if (!value && !working) dialog = null; }"
    >
      <form class="staff-form" novalidate @submit.prevent="save">
        <div v-if="errorList.length || formIssue" class="form-summary" tabindex="-1" role="alert">
          <strong>{{ formIssue || `还有 ${errorList.length} 项需要处理` }}</strong>
          <ul v-if="errorList.length"><li v-for="e in errorList" :key="e.field"><a :href="'#staff-' + e.field" @click.prevent="focusField(e.field)">{{ e.label }}：{{ e.message }}</a></li></ul>
        </div>
        <p v-if="dialog && !profileDialog && member" class="staff-form-who">{{ member.name }} · {{ member.team || '全局' }} · {{ roles[member.role] }}</p>
        <template v-if="profileDialog">
          <label :class="{ invalid: errors.name }">姓名 <span class="req" aria-hidden="true">*</span>
            <input id="staff-name" v-model="form.name" data-field="name" maxlength="128" autocomplete="off" :aria-invalid="!!errors.name" aria-required="true" aria-describedby="staff-name-error" />
            <small v-if="errors.name" id="staff-name-error" class="field-error">{{ errors.name }}</small>
          </label>
          <label :class="{ invalid: errors.phone }">工作手机 <span v-if="dialog === 'invite' || member?.phone" class="req" aria-hidden="true">*</span>
            <input id="staff-phone" v-model="form.phone" data-field="phone" type="tel" inputmode="tel" maxlength="32" placeholder="如 138 0000 0000" autocomplete="off" :aria-invalid="!!errors.phone" :aria-required="dialog === 'invite' || !!member?.phone" :readonly="dialog === 'edit' && accountService && member?.accountState !== 'NONE'" aria-describedby="staff-phone-error" />
            <small v-if="errors.phone" id="staff-phone-error" class="field-error">{{ errors.phone }}</small>
            <small v-else-if="dialog === 'edit' && accountService && member?.accountState !== 'NONE'" class="muted">手机号即登录账号，请使用“更换登录手机号”修改。</small>
            <small v-else-if="dialog === 'edit' && !member?.phone" class="muted">历史成员可暂不填写，建议补全。</small>
          </label>
          <label v-if="administrator">角色 <span class="req" aria-hidden="true">*</span><select v-model="form.role" data-field="role">
            <option v-for="(label,key) in roles" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <label v-else>角色<input :value="roles[form.role]" disabled /></label>
          <label v-if="needsTeam" :class="{ invalid: errors.teamId }">团队 <span class="req" aria-hidden="true">*</span>
            <select id="staff-teamId" v-model="form.teamId" data-field="teamId" :disabled="supervisor" :aria-invalid="!!errors.teamId" aria-required="true" aria-describedby="staff-team-error">
              <option value="">选择团队</option><option v-for="team in teams.filter(t => t.status !== 'ARCHIVED' || t.id === member?.teamId && dialog === 'edit')" :key="team.id" :value="team.id">{{ team.name }}{{ team.status === 'ARCHIVED' ? '（已归档）' : '' }}</option>
            </select>
            <small v-if="errors.teamId" id="staff-team-error" class="field-error">{{ errors.teamId }}</small>
          </label>
          <label :class="{ invalid: errors.email }">工作邮箱（选填）
            <input id="staff-email" v-model="form.email" data-field="email" type="email" maxlength="254" autocomplete="off" :aria-invalid="!!errors.email" aria-describedby="staff-email-error" />
            <small v-if="errors.email" id="staff-email-error" class="field-error">{{ errors.email }}</small>
          </label>
          <label>备注（选填）<textarea v-model="form.note" maxlength="1000" rows="2" /></label>
        </template>
        <label v-if="dialog === 'verify'" :class="{ invalid: errors.email }">工作邮箱（选填）
          <input id="staff-email" v-model="form.email" data-field="email" type="email" maxlength="254" :aria-invalid="!!errors.email" />
          <small v-if="errors.email" class="field-error">{{ errors.email }}</small>
        </label>
        <template v-if="dialog?.startsWith('account-') && member">
          <dl class="staff-facts compact">
            <div><dt>成员</dt><dd>{{ member.name }}</dd></div>
            <div><dt>角色</dt><dd>{{ member.team || '全局' }} · {{ roles[member.role] }}</dd></div>
            <div><dt>登录手机号</dt><dd>{{ member.loginName || (member.phone ? member.phone.replace(/^\+86/, '') : '未填写') }}</dd></div>
            <div><dt>账号状态</dt><dd>{{ accountDisplayLabel[accountDisplayState(member)] ?? member.accountState }}</dd></div>
          </dl>
          <label v-if="dialog === 'account-phone'" :class="{ invalid: errors.phone }">新登录手机号 <span class="req" aria-hidden="true">*</span>
            <input id="staff-phone" v-model="accountPhone" data-field="phone" type="tel" inputmode="tel" maxlength="32" placeholder="11 位手机号" autocomplete="off" aria-required="true" :aria-invalid="!!errors.phone" />
            <small v-if="errors.phone" class="field-error">{{ errors.phone }}</small>
          </label>
        </template>
        <template v-if="dialog === 'review' && member">
          <dl class="staff-facts compact">
            <div><dt>姓名</dt><dd>{{ member.name }}</dd></div>
            <div><dt>工作手机</dt><dd>{{ member.phone || '待补全' }}</dd></div>
            <div><dt>邮箱</dt><dd>{{ member.email || '无' }}</dd></div>
            <div><dt>团队与角色</dt><dd>{{ member.team || '全局' }} · {{ roles[member.role] }}</dd></div>
          </dl>
          <label>企业账号用户名（选填）<input v-model="review.username" maxlength="64" autocomplete="off" placeholder="身份运维创建账号时使用" /></label>
          <label>企业账号 ID（选填）<input v-model="review.subject" maxlength="36" autocomplete="off" placeholder="已有账号时填写 Keycloak 用户 ID" /></label>
          <div v-if="receipt" class="receipt">
            <p><strong>审核已通过，等待企业身份核验。</strong>账号尚未开通。下面是待核验草稿，不能直接用于开通：请交给身份运维在企业认证系统中核对账号 ID、{{ member.email ? '邮箱、' : '' }}唯一的“{{ roles[member.role] }}”角色和归属，由运维写出最终开通回执。成员首次登录后才会启用。</p>
            <pre>{{ receipt }}</pre>
            <ActionButton action-id="staff.receipt-copy" :run="copyReceipt">复制待核验草稿</ActionButton>
          </div>
        </template>
        <fieldset v-if="dialog === 'permissions' || dialog === 'invite' && form.role === 'baobei_sales'">
          <legend>业务权限</legend>
          <label v-for="(label, key) in capabilities" :key="key" class="staff-check">
            <input v-model="form.permissions" type="checkbox" :value="key" />{{ label }}
          </label>
        </fieldset>
        <template v-if="dialog === 'transfer'">
          <label>查找接收销售<input v-model="targetSearch" maxlength="254" /></label>
          <ActionButton action-id="staff.target-search" :run="findTargets">查找</ActionButton>
          <label>接收销售 <span class="req" aria-hidden="true">*</span><select v-model="target" required>
            <option value="">请选择</option>
            <option v-for="item in targets" :key="item.id" :value="item.id">{{ item.name }} · {{ item.team }}</option>
          </select></label>
        </template>
        <label v-if="!receipt" :class="{ invalid: errors.reason }">操作原因 <span v-if="reasonRequired" class="req" aria-hidden="true">*</span><span v-else class="muted">（选填，将记录默认原因）</span>
          <textarea id="staff-reason" v-model="form.reason" data-field="reason" maxlength="600" rows="2" :aria-invalid="!!errors.reason" :aria-required="reasonRequired" aria-describedby="staff-reason-error" />
          <small v-if="errors.reason" id="staff-reason-error" class="field-error">{{ errors.reason }}</small>
        </label>
        <div v-if="sensitive && !receipt" class="reauth-note" :data-ok="reauthActive">
          <ShieldCheck :size="16" aria-hidden="true" />
          <span v-if="reauthActive">已验证密码，{{ reauthMinutes }} 分钟内可提交。</span>
          <span v-else>授予或变更高级角色前，需要再次输入你的登录密码。验证后会回到这里，已填内容会保留。</span>
        </div>
        <div class="actions">
          <ActionButton action-id="staff.cancel" :disabled="working" :run="() => dialog = null">{{ receipt ? '完成' : '取消' }}</ActionButton>
          <template v-if="!receipt">
            <ActionButton v-if="needsReauth" action-id="staff.role-reauth" primary :run="reauthenticateAndResume">验证密码并继续</ActionButton>
            <ActionButton v-else action-id="staff.confirm" primary :disabled="working" :run="save">
              {{ ({ invite: '添加成员', review: '批准开通', suspend: '确认停用', archive: '确认归档', revoke: '确认撤销开户', 'account-revoke': '确认撤销开户', 'account-create': '开通账号', 'account-reset': '重置密码',
                    'account-phone': '更换手机号', 'account-disable': '停用登录', 'account-enable': '恢复登录' } as Record<string, string>)[dialog ?? ''] ?? '保存' }}
            </ActionButton>
          </template>
        </div>
      </form>
    </Modal>

    <Modal :open="!!credential" :title="credential?.action ?? ''" description="初始密码只显示这一次，关闭后无法再次查看；如遗失请重置密码。" @update:open="(v) => { if (!v) closeCredential(); }">
      <div v-if="credential" class="credential-card" role="group" aria-label="登录信息">
        <p><strong>{{ credential.name }}</strong> 现在可以用下面的手机号和初始密码登录，首次登录必须设置新密码。</p>
        <p class="muted" data-testid="credential-once">关闭后无法再次查看此密码。如成员未收到或遗失，请在成员详情中点击“重置密码”生成新的初始密码。</p>
        <dl class="credential">
          <div><dt>登录手机号</dt><dd data-testid="credential-login">{{ credential.login }}</dd></div>
          <div><dt>初始密码</dt><dd data-testid="credential-password" class="mono">{{ credential.temporaryPassword }}</dd></div>
        </dl>
        <div class="actions">
          <ActionButton action-id="staff.credential-copy" :run="copyCredential">复制账号和密码</ActionButton>
          <ActionButton action-id="staff.credential-done" primary :run="closeCredential">已交给成员，关闭</ActionButton>
          <ActionButton v-if="!memberId" action-id="staff.invite-next" :run="() => { closeCredential(); return open('invite'); }">继续添加下一位</ActionButton>
          <ActionButton v-else action-id="staff.back-list" :run="() => { closeCredential(); return router.push(administrator ? '/settings/users' : '/team/members'); }">返回人员列表</ActionButton>
        </div>
      </div>
    </Modal>
  </section>
</template>
