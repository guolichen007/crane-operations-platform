<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { ArrowLeft, KeyRound, LogOut, Power, PowerOff, Save, ShieldCheck, Trash2 } from 'lucide-vue-next';
import {
  getSystemAccess, getPerson, updatePerson, setEnabled, resetPassword, revokeSessions, setSystemRoles, setPortfolioRole,
  archivePerson, purgeCheck, purgePerson, REFERENCE_TEXT,
  listDepartments, listAdminPortfolioScopes, validateProfile, systemRoleNames, portfolioRoleName, personnelErrorMessage, statusLabel, formatTime,
  SYSTEM_ROLES, SYSTEM_ROLE_TEXT, PORTFOLIO_ROLES, PORTFOLIO_ROLE_LABEL, PROJECT_ROLE_LABEL, actionText, resultText,
  type Profile, type SystemRole, type PortfolioRole, type AccountResult,
} from '../cloud/personnel';
import { allowClose } from '../cloud/draft';
import { webSession } from '../cloud/session';
import OneTimePassword from '../components/OneTimePassword.vue';

const route = useRoute();
const router = useRouter();
const queryClient = useQueryClient();
const id = computed(() => String(route.params.id ?? ''));
const access = useQuery({ queryKey: ['system-access'], queryFn: getSystemAccess });
const caps = computed(() => access.data.value?.capabilities ?? {});
const person = useQuery({ queryKey: computed(() => ['person', id.value]), queryFn: () => getPerson(id.value), enabled: computed(() => !!caps.value.personnel_read) });
const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments, enabled: computed(() => !!caps.value.personnel_read) });
const scopes = useQuery({ queryKey: ['admin-portfolio-scopes'], queryFn: listAdminPortfolioScopes, enabled: computed(() => !!caps.value.portfolio_role_manage) });
const p = computed(() => person.data.value);
const self = computed(() => !!p.value && p.value.id === webSession.value?.userId);
// Account managers may not act on system role holders (server enforces; UI hides). An archived
// (deleted) person is frozen: no account action at all.
const archived = computed(() => !!p.value?.archivedAt);
const canAct = computed(() => !archived.value && !!caps.value.account_manage && !!p.value && (p.value.systemRoles.length === 0 || !!caps.value.system_role_manage));
const canArchive = computed(() => !!caps.value.personnel_archive && !!p.value && !archived.value && !self.value && !p.value.protected);
const notice = ref('');
const actionError = ref('');
const otp = ref<AccountResult | null>(null);

async function refresh() {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: ['person', id.value] }),
    queryClient.invalidateQueries({ queryKey: ['people'] }),
  ]);
}
function syncNote(r: AccountResult): string {
  if (r.identitySync === 'PENDING') return '（平台侧已生效；身份服务同步失败，可点击「重试身份同步」）';
  return '';
}

// ---------------------------------------------------------------- profile edit
const editing = ref(false);
const form = ref<Profile>({ displayName: '', departmentId: '', jobTitle: '', phone: '', email: '' });
const touched = ref(false);
const errors = computed(() => validateProfile(form.value));
const shown = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => {
  if (!editing.value || !p.value) return false;
  const v = p.value;
  return form.value.displayName !== v.displayName || form.value.departmentId !== v.departmentId || form.value.jobTitle !== v.jobTitle || form.value.phone !== v.phone || form.value.email !== v.email;
});
let baseVersion = 0;
function startEdit() {
  if (!p.value) return;
  const v = p.value;
  form.value = { displayName: v.displayName, departmentId: v.departmentId, jobTitle: v.jobTitle, phone: v.phone, email: v.email };
  baseVersion = v.version;
  touched.value = false;
  actionError.value = '';
  editing.value = true;
}
const save = useMutation({
  mutationFn: () => updatePerson(id.value, baseVersion, form.value),
  onSuccess: async (r) => {
    editing.value = false;
    notice.value = '资料已保存' + syncNote(r);
    await refresh();
  },
  onError: async (e) => {
    actionError.value = personnelErrorMessage(e);
    await refresh();
  },
});
function submitEdit() {
  touched.value = true;
  if (Object.keys(errors.value).length || save.isPending.value) return;
  save.mutate();
}

// ---------------------------------------------------------------- account actions
const act = useMutation({
  mutationFn: async (kind: 'disable' | 'enable' | 'reset' | 'revoke' | 'sync') => {
    if (!p.value) throw new Error('missing');
    // Repeating the current state's change re-runs only the identity-provider synchronization.
    if (kind === 'sync') return setEnabled(id.value, p.value.version, p.value.enabled);
    if (kind === 'disable' || kind === 'enable') return setEnabled(id.value, p.value.version, kind === 'enable');
    if (kind === 'reset') return resetPassword(id.value);
    return revokeSessions(id.value);
  },
  onSuccess: async (r, kind) => {
    actionError.value = '';
    const text = { disable: `已停用，已结束 ${r.sessionsRevoked} 个在线会话与实时观看`, enable: '已启用（此前撤销的权限不会恢复）', reset: `已重置密码，已结束 ${r.sessionsRevoked} 个在线会话`, revoke: `已撤销 ${r.sessionsRevoked} 个在线会话与实时观看`, sync: r.identitySync === 'SYNCED' ? '身份服务已同步' : '身份服务仍不可用' }[kind];
    notice.value = text + syncNote(r);
    if (kind === 'reset' && r.temporaryPassword) otp.value = r;
    await refresh();
  },
  onError: async (e) => {
    actionError.value = personnelErrorMessage(e);
    await refresh();
  },
});
function run(kind: 'disable' | 'enable' | 'reset' | 'revoke' | 'sync') {
  if (!p.value || act.isPending.value) return;
  if (kind === 'sync') {
    notice.value = '';
    act.mutate(kind);
    return;
  }
  const name = p.value.displayName || p.value.username;
  const ask = {
    disable: `停用「${name}」？其所有登录会话、实时观看将立即失效，历史业务记录与审计保留。`,
    enable: `启用「${name}」？已撤销的权限不会自动恢复。`,
    reset: `重置「${name}」的密码？旧密码与所有在线会话立即失效，新的初始密码只显示一次。`,
    revoke: `结束「${name}」的所有在线会话与实时观看？其下次访问需要重新登录。`,
  }[kind];
  if (!window.confirm(ask)) return;
  notice.value = '';
  act.mutate(kind);
}

// ---------------------------------------------------------------- system roles
const roleDraft = ref<SystemRole[]>([]);
watch(p, (v) => (roleDraft.value = v ? [...v.systemRoles] : []), { immediate: true });
const rolesDirty = computed(() => !!p.value && [...roleDraft.value].sort().join() !== [...p.value.systemRoles].sort().join());
const saveRoles = useMutation({
  mutationFn: () => setSystemRoles(id.value, roleDraft.value, p.value?.systemRoles ?? []),
  onSuccess: async () => {
    notice.value = '系统角色已更新，立即生效';
    actionError.value = '';
    await refresh();
  },
  onError: async (e) => {
    actionError.value = personnelErrorMessage(e);
    await refresh();
  },
});
function toggleRole(r: SystemRole) {
  roleDraft.value = roleDraft.value.includes(r) ? roleDraft.value.filter((x) => x !== r) : [...roleDraft.value, r];
}

// ---------------------------------------------------------------- portfolio roles
const newScope = ref('');
const newScopeRole = ref<PortfolioRole>('portfolio_planner');
const portfolioMut = useMutation({
  mutationFn: (v: { scope: string; role: PortfolioRole | null; expected: string | null }) => setPortfolioRole(id.value, v.scope, v.role, v.expected),
  onSuccess: async (_r, v) => {
    notice.value = v.role ? '项目组合角色已更新，立即生效' : '已撤销该项目组合角色，立即生效';
    actionError.value = '';
    newScope.value = '';
    await refresh();
  },
  onError: async (e) => {
    actionError.value = personnelErrorMessage(e);
    await refresh();
  },
});
const grantableScopes = computed(() => (scopes.data.value ?? []).filter((s) => s.status === 'ACTIVE' && !(p.value?.portfolioGrants ?? []).some((g) => g.scopeId === s.id)));
function changePortfolio(scope: string, current: string, role: PortfolioRole | null) {
  if (portfolioMut.isPending.value) return;
  if (role === null && !window.confirm('撤销该项目组合角色？其在该组合的待办与审批权限立即失效。')) return;
  portfolioMut.mutate({ scope, role, expected: current });
}

// ---------------------------------------------------------------- delete (archive) / purge
// Deleting a person archives it: grants, sessions, live views and the login are revoked at once;
// the person and every historical record stay. Only an archived test account that no business
// record references can be removed permanently (server re-checks everything).
const deleteOpen = ref(false);
const deleteReason = ref('');
const deleteConfirm = ref('');
const deleteError = ref('');
const loginName = computed(() => p.value?.username || p.value?.id || '');
function openDelete() {
  deleteReason.value = '';
  deleteConfirm.value = '';
  deleteError.value = '';
  deleteOpen.value = true;
}
const archive = useMutation({
  mutationFn: () => archivePerson(id.value, p.value?.version ?? 0, deleteReason.value.trim(), deleteConfirm.value.trim()),
  onSuccess: async (r) => {
    deleteOpen.value = false;
    const n = r.revoked.systemRoles.length + r.revoked.portfolio.length + r.revoked.projects.length;
    notice.value = `已删除（归档）：撤销 ${n} 项授权、结束 ${r.sessionsRevoked} 个在线会话与实时观看，登录已禁用；历史记录保留。` + syncNote(r);
    actionError.value = '';
    await refresh();
  },
  onError: (e) => (deleteError.value = personnelErrorMessage(e)),
});
const purgeInfo = useQuery({
  queryKey: computed(() => ['purge-check', id.value]),
  queryFn: () => purgeCheck(id.value),
  enabled: computed(() => archived.value && !!caps.value.personnel_archive),
});
const purgeOpen = ref(false);
const purgeConfirm = ref('');
const purge = useMutation({
  mutationFn: () => purgePerson(id.value, purgeConfirm.value.trim()),
  onSuccess: async () => {
    purgeOpen.value = false;
    await queryClient.invalidateQueries({ queryKey: ['people'] });
    void router.push('/system/people');
  },
  onError: (e) => (deleteError.value = personnelErrorMessage(e)),
});
const referenceText = computed(() =>
  Object.entries(purgeInfo.data.value?.references ?? {}).map(([k, n]) => `${REFERENCE_TEXT[k] ?? k} ${n}`).join('、'));

onBeforeRouteLeave(() => (otp.value ? false : allowClose(dirty.value, save.isPending.value)));
const changeText = (c: unknown) => {
  if (!c || typeof c !== 'object') return '';
  return Object.entries(c as Record<string, unknown>)
    .filter(([k]) => k !== 'provisionId')
    .map(([k, v]) => `${k}: ${typeof v === 'object' ? JSON.stringify(v) : String(v)}`)
    .join('；');
};
</script>
<template>
  <section class="workspace-page person-detail" data-testid="person-detail">
    <header class="workspace-page-head">
      <div>
        <button class="ghost-action" @click="router.push('/system/people')"><ArrowLeft :size="14" />人员列表</button>
        <h1 data-testid="person-name">{{ p?.displayName || id }}</h1>
        <p v-if="p">{{ p.username || p.id }} · <span class="status-pill" :data-tone="statusLabel(p).tone" data-testid="person-status">{{ statusLabel(p).text }}</span></p>
      </div>
    </header>

    <div v-if="access.isLoading.value || person.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></div>
    <p v-else-if="!caps.personnel_read" class="workspace-empty" role="status">您没有系统管理权限。</p>
    <p v-else-if="person.isError.value" class="workspace-error" role="alert">{{ personnelErrorMessage(person.error.value) }}</p>
    <template v-else-if="p">
      <p v-if="notice" class="workspace-notice" role="status" data-testid="person-notice">{{ notice }}</p>
      <section v-if="archived" class="archived-banner" data-testid="person-archived" role="status">
        <strong>该人员已删除（归档）</strong>
        <span>{{ formatTime(p.archivedAt) }} · {{ p.archivedBy }} · 原因：{{ p.archiveReason }}</span>
        <span>登录、授权与会话均已撤销；其历史操作、审批与责任记录保留，不可恢复启用。</span>
        <template v-if="caps.personnel_archive">
          <span v-if="purgeInfo.data.value?.purgeable" class="form-hint">该账号没有任何业务历史引用，可作为测试账号永久删除。</span>
          <span v-else-if="purgeInfo.data.value && !purgeInfo.data.value.testAccount" class="form-hint" data-testid="purge-blocked">非测试账号（正式员工或未标记），只能保留为归档，不能永久删除。</span>
          <span v-else-if="purgeInfo.data.value" class="form-hint" data-testid="purge-blocked">存在业务历史（{{ referenceText || '受保护' }}），只能保留为归档，不能永久删除。</span>
          <button v-if="purgeInfo.data.value?.purgeable" class="danger-action" data-testid="person-purge-open" @click="purgeConfirm = ''; deleteError = ''; purgeOpen = true"><Trash2 :size="14" />永久删除测试账号</button>
        </template>
      </section>
      <p v-if="actionError" class="form-error" role="alert" data-testid="person-error">{{ actionError }}</p>

      <div class="detail-grid">
        <section class="detail-card">
          <h2>基本资料</h2>
          <form v-if="editing" class="profile-form" novalidate @submit.prevent="submitEdit">
            <label>姓名 *<input v-model="form.displayName" maxlength="80" data-testid="edit-name" :aria-invalid="!!shown.displayName" /><span v-if="shown.displayName" class="field-error">{{ shown.displayName }}</span></label>
            <label>部门<select v-model="form.departmentId"><option value="">未指定</option><option v-for="d in (departments.data.value ?? []).filter((d) => d.status === 'ACTIVE' || d.id === form.departmentId)" :key="d.id" :value="d.id">{{ d.name }}</option></select></label>
            <label>岗位<input v-model="form.jobTitle" maxlength="80" data-testid="edit-job" /></label>
            <label>联系电话<input v-model="form.phone" maxlength="32" inputmode="tel" :aria-invalid="!!shown.phone" /><span v-if="shown.phone" class="field-error">{{ shown.phone }}</span></label>
            <label>邮箱<input v-model="form.email" type="email" maxlength="254" :aria-invalid="!!shown.email" /><span v-if="shown.email" class="field-error">{{ shown.email }}</span></label>
            <div class="modal-actions">
              <button type="button" class="ghost-action" :disabled="save.isPending.value" @click="allowClose(dirty, false) && (editing = false)">取消</button>
              <button type="submit" class="primary-action" :disabled="save.isPending.value" data-testid="edit-save"><Save :size="14" />{{ save.isPending.value ? '保存中…' : '保存' }}</button>
            </div>
          </form>
          <dl v-else class="facts">
            <div><dt>姓名</dt><dd>{{ p.displayName || '—' }}</dd></div>
            <div><dt>部门</dt><dd>{{ p.departmentName || '—' }}</dd></div>
            <div><dt>岗位</dt><dd>{{ p.jobTitle || '—' }}</dd></div>
            <div><dt>联系电话</dt><dd>{{ p.phone || '—' }}</dd></div>
            <div><dt>邮箱</dt><dd>{{ p.email || '—' }}</dd></div>
            <div><dt>创建</dt><dd>{{ formatTime(p.createdAt) }}<small v-if="p.createdBy"> · {{ p.createdBy }}</small></dd></div>
          </dl>
          <button v-if="!editing && canAct" class="ghost-action" data-testid="edit-open" @click="startEdit">编辑资料</button>
        </section>

        <section class="detail-card">
          <h2>登录账号</h2>
          <dl class="facts">
            <div><dt>登录用户名</dt><dd data-testid="person-username">{{ p.username || '（未纳管）' }}</dd></div>
            <div><dt>平台状态</dt><dd>{{ p.enabled ? '启用' : '已停用' }}</dd></div>
            <div><dt>身份服务</dt><dd data-testid="person-identity">{{ !p.identity.available ? '暂不可读' : !p.identity.exists ? '无托管登录账号' : (p.identity.enabled ? '启用' : '停用') + (p.identity.passwordChangeRequired ? ' · 待首次改密' : '') }}</dd></div>
            <div><dt>最近登录</dt><dd data-testid="person-last-login">{{ formatTime(p.lastLoginAt) }}</dd></div>
          </dl>
          <div v-if="canAct" class="account-actions">
            <button v-if="p.enabled" class="danger-action" :disabled="act.isPending.value || self" data-testid="person-disable" @click="run('disable')"><PowerOff :size="14" />停用</button>
            <button v-else class="ghost-action" :disabled="act.isPending.value" data-testid="person-enable" @click="run('enable')"><Power :size="14" />启用</button>
            <button class="ghost-action" :disabled="act.isPending.value || !p.managed" data-testid="person-reset" @click="run('reset')"><KeyRound :size="14" />重置密码</button>
            <button class="ghost-action" :disabled="act.isPending.value" data-testid="person-revoke" @click="run('revoke')"><LogOut :size="14" />撤销会话</button>
            <button v-if="p.identitySync === 'PENDING' && p.managed" class="ghost-action" :disabled="act.isPending.value" data-testid="person-sync" @click="run('sync')">重试身份同步</button>
          </div>
          <div v-if="canArchive" class="account-actions danger-zone">
            <button class="danger-action" :disabled="act.isPending.value || archive.isPending.value" data-testid="person-delete" @click="openDelete"><Trash2 :size="14" />删除人员</button>
          </div>
          <p v-if="p.protected" class="form-hint" data-testid="person-protected">受保护身份（{{ p.protected }}），不能删除。</p>
          <p v-if="self" class="form-hint">不能停用或删除自己的账号。</p>
          <p v-else-if="caps.account_manage && !canAct" class="form-hint">该人员持有系统角色，只有系统管理员可以管理其账号。</p>
        </section>

        <section class="detail-card">
          <h2><ShieldCheck :size="15" />系统权限</h2>
          <template v-if="caps.system_role_manage && !self && !archived">
            <label v-for="r in SYSTEM_ROLES" :key="r" class="check-row">
              <input type="checkbox" :checked="roleDraft.includes(r)" :data-testid="`detail-role-${r}`" :disabled="!p.enabled && !roleDraft.includes(r)" @change="toggleRole(r)" />
              <span><strong>{{ SYSTEM_ROLE_TEXT[r].label }}</strong><small>{{ SYSTEM_ROLE_TEXT[r].detail }}</small></span>
            </label>
            <button class="primary-action" :disabled="!rolesDirty || saveRoles.isPending.value" data-testid="detail-roles-save" @click="saveRoles.mutate()">保存系统角色</button>
          </template>
          <p v-else>{{ systemRoleNames(p.systemRoles) || '无系统角色' }}</p>
          <p v-if="self && caps.system_role_manage" class="form-hint">不能调整自己的系统角色，请由另一位系统管理员处理。</p>
        </section>

        <section class="detail-card">
          <h2>项目组合授权</h2>
          <table v-if="p.portfolioGrants?.length" class="data-table stack-table" data-testid="detail-portfolio">
            <thead><tr><th>项目组合</th><th>角色</th><th v-if="caps.portfolio_role_manage && !self && !archived"></th></tr></thead>
            <tbody>
              <tr v-for="g in p.portfolioGrants" :key="g.scopeId" :data-scope="g.scopeId">
                <td data-label="项目组合">{{ g.scopeName }}</td>
                <td data-label="角色">
                  <select v-if="caps.portfolio_role_manage && !self && !archived" :value="g.role" :aria-label="`${g.scopeName} 角色`" :disabled="portfolioMut.isPending.value"
                    @change="changePortfolio(g.scopeId, g.role, ($event.target as HTMLSelectElement).value as PortfolioRole)">
                    <option v-for="r in PORTFOLIO_ROLES" :key="r" :value="r">{{ PORTFOLIO_ROLE_LABEL[r] }}</option>
                  </select>
                  <template v-else>{{ portfolioRoleName(g.role) }}</template>
                </td>
                <td v-if="caps.portfolio_role_manage && !self && !archived"><button class="danger-action" :disabled="portfolioMut.isPending.value" @click="changePortfolio(g.scopeId, g.role, null)">撤销</button></td>
              </tr>
            </tbody>
          </table>
          <p v-else class="workspace-hint">无项目组合角色。</p>
          <form v-if="caps.portfolio_role_manage && !self && p.enabled && grantableScopes.length" class="grant-row" @submit.prevent="newScope && portfolioMut.mutate({ scope: newScope, role: newScopeRole, expected: null })">
            <select v-model="newScope" aria-label="新增项目组合" data-testid="detail-scope"><option value="" disabled>选择项目组合</option><option v-for="s in grantableScopes" :key="s.id" :value="s.id">{{ s.name }}</option></select>
            <select v-model="newScopeRole" aria-label="新增项目组合角色" data-testid="detail-scope-role"><option v-for="r in PORTFOLIO_ROLES" :key="r" :value="r">{{ PORTFOLIO_ROLE_LABEL[r] }}</option></select>
            <button class="ghost-action" type="submit" :disabled="!newScope || portfolioMut.isPending.value" data-testid="detail-scope-add">授予</button>
          </form>
        </section>

        <section class="detail-card">
          <h2>项目授权</h2>
          <table v-if="p.projectGrants?.length" class="data-table stack-table" data-testid="detail-projects">
            <thead><tr><th>项目</th><th>项目角色</th></tr></thead>
            <tbody><tr v-for="g in p.projectGrants" :key="g.scopeId"><td data-label="项目">{{ g.scopeName }}<div class="cell-sub">{{ g.scopeId }}</div></td><td data-label="项目角色">{{ PROJECT_ROLE_LABEL[g.role] ?? g.role }}</td></tr></tbody>
          </table>
          <p v-else class="workspace-hint">无项目权限。</p>
          <p class="form-hint">项目权限由项目所在组合的「项目组合经理」在「项目组合 → 正式项目 → 项目成员」中授予或撤销。</p>
        </section>

        <section class="detail-card history-card">
          <h2>安全操作与历史</h2>
          <ol v-if="p.history.length" class="history" data-testid="person-history">
            <li v-for="(h, i) in p.history" :key="i" :data-action="h.action">
              <span class="h-time">{{ formatTime(h.at) }}</span>
              <span class="h-action">{{ actionText(h.action) }}<small v-if="h.result !== 'success'"> · {{ resultText(h.result) }}</small></span>
              <span class="h-actor">{{ h.actor }}<small v-if="h.scope"> · {{ h.scope }}</small></span>
              <span v-if="changeText(h.change)" class="h-change">{{ changeText(h.change) }}</span>
            </li>
          </ol>
          <p v-else class="workspace-hint">暂无记录。</p>
        </section>
      </div>
    </template>

    <div v-if="deleteOpen && p" class="modal-backdrop" @click.self="!archive.isPending.value && (deleteOpen = false)">
      <form class="workspace-modal delete-modal" data-testid="person-delete-dialog" novalidate @submit.prevent="archive.mutate()">
        <h2>删除人员「{{ p.displayName || loginName }}」</h2>
        <ul class="delete-effects">
          <li>立即撤销全部系统、项目组合与项目授权（{{ p.systemRoles.length + p.portfolioCount + p.projectCount }} 项）；</li>
          <li>立即结束所有在线会话与实时观看，并禁用登录账号；</li>
          <li>人员从默认列表隐藏；历史操作、审批、责任人记录与审计全部保留；</li>
          <li>删除后不能恢复启用。如只是暂时不用，请改用「停用」。</li>
        </ul>
        <label>删除原因 *<input v-model="deleteReason" maxlength="200" data-testid="delete-reason" placeholder="如：离职 / 测试账号清理" /></label>
        <label><span>请输入登录用户名 <code>{{ loginName }}</code> 以确认 *</span><input v-model="deleteConfirm" data-testid="delete-confirm" autocomplete="off" autocapitalize="off" spellcheck="false" /></label>
        <p v-if="deleteError" class="form-error" role="alert" data-testid="delete-error">{{ deleteError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="archive.isPending.value" @click="deleteOpen = false">取消</button>
          <button type="submit" class="danger-action" data-testid="delete-submit" :disabled="archive.isPending.value || !deleteReason.trim() || deleteConfirm.trim() !== loginName">
            {{ archive.isPending.value ? '删除中…' : '确认删除' }}
          </button>
        </div>
      </form>
    </div>
    <div v-if="purgeOpen && p" class="modal-backdrop" @click.self="!purge.isPending.value && (purgeOpen = false)">
      <form class="workspace-modal delete-modal" data-testid="person-purge-dialog" novalidate @submit.prevent="purge.mutate()">
        <h2>永久删除测试账号「{{ loginName }}」</h2>
        <p class="form-hint">仅用于没有任何业务历史的测试账号：登录账号与人员记录将被永久移除，安全审计保留；该用户名不会再分配给新人员。</p>
        <label><span>请输入登录用户名 <code>{{ loginName }}</code> 以确认 *</span><input v-model="purgeConfirm" data-testid="purge-confirm" autocomplete="off" autocapitalize="off" spellcheck="false" /></label>
        <p v-if="deleteError" class="form-error" role="alert">{{ deleteError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="purge.isPending.value" @click="purgeOpen = false">取消</button>
          <button type="submit" class="danger-action" data-testid="purge-submit" :disabled="purge.isPending.value || purgeConfirm.trim() !== loginName">永久删除</button>
        </div>
      </form>
    </div>

    <OneTimePassword v-if="otp?.temporaryPassword && p" title="密码已重置" :username="p.username" :display-name="p.displayName" :password="otp.temporaryPassword"
      note="旧密码与所有在线会话已失效。" @close="otp = null" />
  </section>
</template>
<style scoped>
.detail-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 14px; }
.detail-card { border: 1px solid var(--line-soft); border-radius: 12px; padding: 14px; display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.detail-card h2 { margin: 0; font-size: 14px; color: var(--text-1); display: flex; align-items: center; gap: 6px; }
.history-card { grid-column: 1 / -1; }
.facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 8px; margin: 0; }
.facts div { border: 1px solid var(--line-soft); border-radius: 8px; padding: 6px 10px; }
.facts dt { font-size: 11px; color: var(--text-3); }
.facts dd { margin: 4px 0 0; font-size: 13px; color: var(--text-1); overflow-wrap: anywhere; }
.profile-form { display: grid; gap: 10px; }
.profile-form label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); }
.profile-form input, .profile-form select, .grant-row select, .data-table select { height: 32px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; }
.account-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.check-row { display: flex; align-items: flex-start; gap: 8px; font-size: 12px; color: var(--text-3); }
.check-row span { display: flex; flex-direction: column; gap: 2px; }
.check-row strong { color: var(--text-1); font-size: 13px; }
.grant-row { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) auto; gap: 8px; }
.history { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; max-height: 360px; overflow: auto; }
.history li { display: grid; grid-template-columns: 150px 120px minmax(0, 1fr); gap: 4px 10px; font-size: 12px; color: var(--text-2); border-bottom: 1px solid var(--line-soft); padding-bottom: 6px; }
.h-change { grid-column: 2 / -1; color: var(--text-3); overflow-wrap: anywhere; }
.status-pill[data-tone='off'] { color: #f08a8a; }
.status-pill[data-tone='warn'] { color: #e8b75d; }
.status-pill[data-tone='ok'] { color: #6fd39b; }
.danger-zone { border-top: 1px dashed var(--line-soft); padding-top: 10px; }
.archived-banner { border: 1px solid rgba(240, 138, 138, 0.45); border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px; color: var(--text-2); font-size: 13px; }
.archived-banner strong { color: #f08a8a; }
.archived-banner button { align-self: flex-start; }
.delete-modal { width: min(560px, 94vw); display: flex; flex-direction: column; gap: 10px; }
.delete-modal label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); }
.delete-modal input { height: 34px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; }
.delete-effects { margin: 0; padding-left: 18px; font-size: 13px; color: var(--text-2); display: flex; flex-direction: column; gap: 4px; }
.data-table { min-width: 0; }
.detail-card .data-table td, .detail-card .data-table th { overflow-wrap: anywhere; }
@media (max-width: 700px) {
  .detail-grid { grid-template-columns: 1fr; }
  .detail-card { padding: 12px; }
  .facts { grid-template-columns: 1fr 1fr; }
  .account-actions > button { flex: 1 1 calc(50% - 8px); justify-content: center; }
  .stack-table, .stack-table tbody, .stack-table tr, .stack-table td { display: block; width: 100%; }
  .stack-table thead { display: none; }
  .stack-table tr { border-bottom: 1px solid var(--line-soft); padding: 8px 0; }
  .stack-table td { position: relative; padding: 3px 0 3px 88px; border: 0; white-space: normal; overflow-wrap: anywhere; word-break: break-word; }
  .stack-table td::before { content: attr(data-label); position: absolute; left: 0; top: 3px; width: 80px; color: var(--text-3); font-size: 12px; }
  .stack-table td:not([data-label]) { padding-left: 0; }
  .stack-table select { width: 100%; }
  .delete-modal { width: 100vw; border-radius: 0; }
  .history li { grid-template-columns: 1fr; }
  .h-change { grid-column: 1; }
  .grant-row { grid-template-columns: 1fr; }
}
</style>
