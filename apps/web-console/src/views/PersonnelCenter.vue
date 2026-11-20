<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { useRouter, onBeforeRouteLeave } from 'vue-router';
import { Search, UserPlus, RefreshCw, ChevronLeft, ChevronRight, Building2, AlertTriangle } from 'lucide-vue-next';
import {
  getSystemAccess, listPeople, listDepartments, listAdminPortfolioScopes, provisionPerson, listProvisions, resumeProvision, cancelProvision,
  createDepartment, validateBasic, systemRoleNames, portfolioRoleName, personnelErrorMessage, statusLabel, formatTime,
  SYSTEM_ROLES, SYSTEM_ROLE_TEXT, PORTFOLIO_ROLES, PORTFOLIO_ROLE_LABEL,
  type Profile, type SystemRole, type PortfolioRole, type ProvisionResult,
} from '../cloud/personnel';
import { allowClose } from '../cloud/draft';
import OneTimePassword from '../components/OneTimePassword.vue';

const router = useRouter();
const queryClient = useQueryClient();
const access = useQuery({ queryKey: ['system-access'], queryFn: getSystemAccess });
const caps = computed(() => access.data.value?.capabilities ?? {});
const canRead = computed(() => !!caps.value.personnel_read);
const canManage = computed(() => !!caps.value.account_manage);
const canGrantRoles = computed(() => !!caps.value.system_role_manage);
const canGrantPortfolio = computed(() => !!caps.value.portfolio_role_manage);
const canDepartments = computed(() => !!caps.value.department_manage);

const q = ref('');
const status = ref('');
const department = ref('');
const systemRole = ref('');
const applied = ref({ q: '', status: '', departmentId: '', systemRole: '' });
const page = ref(0);
const limit = 50;
function apply() {
  applied.value = { q: q.value.trim(), status: status.value, departmentId: department.value, systemRole: systemRole.value };
  page.value = 0;
}
const people = useQuery({
  queryKey: computed(() => ['people', applied.value, page.value]),
  queryFn: () => listPeople({ ...applied.value, limit, offset: page.value * limit }),
  enabled: canRead,
});
const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments, enabled: canRead });
const activeDepartments = computed(() => (departments.data.value ?? []).filter((d) => d.status === 'ACTIVE'));
const scopes = useQuery({ queryKey: ['admin-portfolio-scopes'], queryFn: listAdminPortfolioScopes, enabled: canRead });
const provisions = useQuery({ queryKey: ['provisions'], queryFn: listProvisions, enabled: canManage });

// ---------------------------------------------------------------- new person wizard (3 steps)
const wizardOpen = ref(false);
const step = ref<1 | 2 | 3>(1);
const blankProfile = (): Profile => ({ displayName: '', departmentId: '', jobTitle: '', phone: '', email: '' });
const username = ref('');
const profile = ref<Profile>(blankProfile());
const roles = ref<SystemRole[]>([]);
const portfolioGrants = ref<{ scopeId: string; role: PortfolioRole }[]>([]);
const touched = ref(false);
const wizardError = ref('');
let idempotencyKey = crypto.randomUUID();
const errors = computed(() => validateBasic(username.value, profile.value));
const shown = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => wizardOpen.value && (username.value !== '' || profile.value.displayName !== '' || roles.value.length > 0 || portfolioGrants.value.length > 0));
const result = ref<ProvisionResult | null>(null);
const deptName = computed(() => activeDepartments.value.find((d) => d.id === profile.value.departmentId)?.name ?? '未指定');

function openWizard() {
  username.value = '';
  profile.value = blankProfile();
  roles.value = [];
  portfolioGrants.value = [];
  touched.value = false;
  wizardError.value = '';
  step.value = 1;
  idempotencyKey = crypto.randomUUID();
  wizardOpen.value = true;
}
function closeWizard() {
  if (provision.isPending.value) return;
  if (!allowClose(dirty.value, false)) return;
  wizardOpen.value = false;
}
function next() {
  touched.value = true;
  wizardError.value = '';
  if (step.value === 1 && Object.keys(errors.value).length) return;
  if (step.value === 2 && portfolioGrants.value.some((g) => !g.scopeId)) {
    wizardError.value = '请为每一行选择项目组合，或删除空行';
    return;
  }
  if (step.value < 3) step.value = (step.value + 1) as 2 | 3;
}
function back() {
  wizardError.value = '';
  if (step.value > 1) step.value = (step.value - 1) as 1 | 2;
}
function toggleRole(r: SystemRole) {
  roles.value = roles.value.includes(r) ? roles.value.filter((x) => x !== r) : [...roles.value, r];
}
function addPortfolioRow() {
  portfolioGrants.value = [...portfolioGrants.value, { scopeId: '', role: 'portfolio_planner' }];
}
function removePortfolioRow(i: number) {
  portfolioGrants.value = portfolioGrants.value.filter((_, j) => j !== i);
}
const availableScopes = (i: number) =>
  (scopes.data.value ?? []).filter((s) => s.status === 'ACTIVE' && !portfolioGrants.value.some((g, j) => j !== i && g.scopeId === s.id));

const provision = useMutation({
  mutationFn: () => provisionPerson({ username: username.value.trim().toLowerCase(), profile: profile.value, systemRoles: roles.value, portfolioGrants: portfolioGrants.value }, idempotencyKey),
  onSuccess: async (r) => {
    wizardOpen.value = false;
    result.value = r;
    await Promise.all([queryClient.invalidateQueries({ queryKey: ['people'] }), queryClient.invalidateQueries({ queryKey: ['provisions'] })]);
  },
  onError: (e) => {
    // The same idempotency key is kept: a retry continues the same opening (never a second account).
    wizardError.value = personnelErrorMessage(e);
    void queryClient.invalidateQueries({ queryKey: ['provisions'] });
  },
});
function submit() {
  if (provision.isPending.value) return;
  wizardError.value = '';
  provision.mutate();
}
function closeResult() {
  const id = result.value?.person.id;
  result.value = null;
  if (id) void router.push(`/system/people/${encodeURIComponent(id)}`);
}

// ---------------------------------------------------------------- unfinished openings
const recoveryError = ref('');
const recovered = ref<ProvisionResult | null>(null);
const resume = useMutation({
  mutationFn: (id: string) => resumeProvision(id),
  onSuccess: async (r) => {
    recovered.value = r.temporaryPassword ? r : null;
    recoveryError.value = r.temporaryPassword ? '' : '开户已完成；初始密码未能再次显示，请在人员详情中「重置密码」。';
    await Promise.all([queryClient.invalidateQueries({ queryKey: ['people'] }), queryClient.invalidateQueries({ queryKey: ['provisions'] })]);
  },
  onError: (e) => (recoveryError.value = personnelErrorMessage(e)),
});
const cancel = useMutation({
  mutationFn: (id: string) => cancelProvision(id),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['provisions'] }),
  onError: (e) => (recoveryError.value = personnelErrorMessage(e)),
});
const PROVISION_STATE: Record<string, string> = { STARTED: '已提交，未创建登录账号', IDP_CREATED: '登录账号已创建（停用），未完成授权', BOUND: '授权已保存，未启用', FAILED: '失败' };

// ---------------------------------------------------------------- departments
const deptOpen = ref(false);
const newDept = ref({ id: '', name: '' });
const deptError = ref('');
const addDept = useMutation({
  mutationFn: () => createDepartment({ id: newDept.value.id.trim(), name: newDept.value.name.trim() }),
  onSuccess: async () => {
    newDept.value = { id: '', name: '' };
    deptError.value = '';
    await queryClient.invalidateQueries({ queryKey: ['departments'] });
  },
  onError: (e) => (deptError.value = personnelErrorMessage(e)),
});

function onBeforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || provision.isPending.value || result.value) {
    event.preventDefault();
    event.returnValue = '';
  }
}
onMounted(() => window.addEventListener('beforeunload', onBeforeUnload));
onBeforeUnmount(() => window.removeEventListener('beforeunload', onBeforeUnload));
onBeforeRouteLeave(() => (result.value ? false : allowClose(dirty.value, provision.isPending.value)));
watch(canRead, (v) => {
  if (v) void people.refetch();
});
</script>
<template>
  <section class="workspace-page personnel-center" data-testid="personnel-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">系统管理</span>
        <h1>人员与账号</h1>
        <p>开户、启停用、重置密码与权限配置 · 系统权限独立于项目与项目组合权限</p>
      </div>
      <div class="head-actions">
        <button v-if="canDepartments" class="ghost-action" data-testid="dept-open" @click="deptOpen = !deptOpen"><Building2 :size="15" />部门</button>
        <button v-if="canManage" class="primary-action" data-testid="person-create" :disabled="!access.data.value?.accountServiceConfigured" @click="openWizard">
          <UserPlus :size="15" />新增人员
        </button>
      </div>
    </header>

    <div v-if="access.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></div>
    <p v-else-if="access.isError.value" class="workspace-error" role="alert">系统权限读取失败：{{ personnelErrorMessage(access.error.value) }}</p>
    <p v-else-if="!canRead" class="workspace-empty" role="status" data-testid="personnel-forbidden">
      您没有系统管理权限。人员与账号由「系统管理员」或「账号管理员」维护；项目权限与项目组合权限不包含系统管理。
    </p>
    <template v-else>
      <p v-if="canManage && !access.data.value?.accountServiceConfigured" class="workspace-error" role="alert" data-testid="account-service-missing">
        本部署尚未配置账号服务，暂不能开户或重置密码（需在服务器开通账号服务）。人员资料与权限仍可查看。
      </p>

      <section v-if="deptOpen && canDepartments" class="dept-panel" data-testid="dept-panel">
        <h2>部门</h2>
        <ul class="dept-list">
          <li v-for="d in departments.data.value ?? []" :key="d.id" :data-status="d.status">{{ d.name }}<small>{{ d.id }} · {{ d.memberCount }} 人{{ d.status === 'DISABLED' ? ' · 已停用' : '' }}</small></li>
        </ul>
        <form class="dept-form" @submit.prevent="addDept.mutate()">
          <input v-model="newDept.id" aria-label="部门编号" placeholder="编号，如 ops" maxlength="80" />
          <input v-model="newDept.name" aria-label="部门名称" placeholder="名称，如 运维部" maxlength="80" />
          <button class="ghost-action" type="submit" :disabled="addDept.isPending.value">添加部门</button>
        </form>
        <p v-if="deptError" class="form-error" role="alert">{{ deptError }}</p>
      </section>

      <section v-if="canManage && (provisions.data.value?.length ?? 0) > 0" class="recovery-panel" data-testid="provision-recovery">
        <h2><AlertTriangle :size="15" />未完成的开户</h2>
        <p class="form-hint">以下开户因网络或身份服务中断未完成，登录账号尚未生效。可「继续开户」完成，或「放弃」释放用户名（仅限未授权的开户）。</p>
        <table class="data-table stack-table">
          <thead><tr><th>用户名</th><th>姓名</th><th>状态</th><th>更新时间</th><th></th></tr></thead>
          <tbody>
            <tr v-for="p in provisions.data.value" :key="p.id" :data-provision="p.username">
              <td data-label="用户名">{{ p.username }}</td>
              <td data-label="姓名">{{ p.request.profile.displayName }}</td>
              <td data-label="状态">{{ PROVISION_STATE[p.state] ?? p.state }}<div v-if="p.failure" class="cell-sub">{{ p.failure }}</div></td>
              <td data-label="更新时间">{{ formatTime(p.updatedAt) }}</td>
              <td class="row-actions">
                <button v-if="p.state !== 'FAILED'" class="ghost-action" :disabled="resume.isPending.value" data-testid="provision-resume" @click="resume.mutate(p.id)">继续开户</button>
                <button v-if="p.state !== 'BOUND'" class="danger-action" :disabled="cancel.isPending.value" data-testid="provision-cancel" @click="cancel.mutate(p.id)">放弃</button>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="recoveryError" class="form-error" role="alert">{{ recoveryError }}</p>
      </section>

      <div class="asset-filters">
        <div class="search-field">
          <Search :size="15" />
          <input v-model="q" placeholder="搜索姓名 / 用户名 / 岗位" aria-label="搜索人员" maxlength="80" @keyup.enter="apply" />
        </div>
        <select v-model="status" aria-label="状态筛选">
          <option value="">全部状态</option>
          <option value="ENABLED">启用</option>
          <option value="DISABLED">已停用</option>
          <option value="UNMANAGED">账号未纳管</option>
          <option value="ARCHIVED">已删除（归档）</option>
        </select>
        <select v-model="department" aria-label="部门筛选">
          <option value="">全部部门</option>
          <option v-for="d in departments.data.value ?? []" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
        <select v-model="systemRole" aria-label="系统角色筛选">
          <option value="">全部系统角色</option>
          <option v-for="r in SYSTEM_ROLES" :key="r" :value="r">{{ SYSTEM_ROLE_TEXT[r].label }}</option>
        </select>
        <button class="ghost-action" data-testid="people-filter" @click="apply">筛选</button>
        <button class="ghost-action" aria-label="刷新" @click="queryClient.invalidateQueries({ queryKey: ['people'] })"><RefreshCw :size="14" /></button>
      </div>

      <div v-if="people.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></div>
      <p v-else-if="people.isError.value" class="workspace-error" role="alert">人员加载失败：{{ personnelErrorMessage(people.error.value) }}</p>
      <p v-else-if="!people.data.value?.items.length" class="workspace-empty" data-testid="people-empty">没有符合条件的人员。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table stack-table" data-testid="people-table">
          <thead>
            <tr><th>姓名</th><th>用户名</th><th>部门 / 岗位</th><th>状态</th><th>系统角色</th><th>授权</th><th>最近登录</th><th>创建时间</th></tr>
          </thead>
          <tbody>
            <tr v-for="p in people.data.value.items" :key="p.id" class="data-row" :data-person="p.id" tabindex="0"
              @click="router.push(`/system/people/${encodeURIComponent(p.id)}`)" @keyup.enter="router.push(`/system/people/${encodeURIComponent(p.id)}`)">
              <td data-col="name" data-label="姓名">{{ p.displayName || '—' }}</td>
              <td data-col="username" data-label="用户名">{{ p.username || p.id }}</td>
              <td data-label="部门 / 岗位">{{ p.departmentName || '—' }}<div class="cell-sub">{{ p.jobTitle || '' }}</div></td>
              <td data-label="状态"><span class="status-pill" :data-tone="statusLabel(p).tone" data-col="status">{{ statusLabel(p).text }}</span></td>
              <td data-label="系统角色">{{ systemRoleNames(p.systemRoles) || '—' }}</td>
              <td data-label="授权">{{ p.portfolioCount }} 个组合 · {{ p.projectCount }} 个项目</td>
              <td data-label="最近登录">{{ formatTime(p.lastLoginAt) }}</td>
              <td data-label="创建时间">{{ formatTime(p.createdAt) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="(people.data.value?.total ?? 0) > limit" class="pager">
        <button :disabled="page === 0" aria-label="上一页" @click="page--"><ChevronLeft :size="15" /></button>
        <span>第 {{ page + 1 }} 页 · 共 {{ people.data.value?.total }} 人</span>
        <button :disabled="(page + 1) * limit >= (people.data.value?.total ?? 0)" aria-label="下一页" @click="page++"><ChevronRight :size="15" /></button>
      </div>
      <p v-else-if="people.data.value" class="cell-sub">共 {{ people.data.value.total }} 人</p>
    </template>

    <!-- New person: 基本信息 → 权限配置 → 账号开通 -->
    <div v-if="wizardOpen" class="modal-backdrop" @click.self="closeWizard">
      <form class="workspace-modal person-wizard" data-testid="person-wizard" novalidate @submit.prevent="step === 3 ? submit() : next()">
        <h2>新增人员</h2>
        <ol class="wizard-steps" aria-label="开户步骤">
          <li :class="{ current: step === 1, done: step > 1 }">1 基本信息</li>
          <li :class="{ current: step === 2, done: step > 2 }">2 权限配置</li>
          <li :class="{ current: step === 3 }">3 账号开通</li>
        </ol>

        <template v-if="step === 1">
          <div class="form-grid">
            <label>姓名 *<input v-model="profile.displayName" data-testid="wiz-name" maxlength="80" :aria-invalid="!!shown.displayName" />
              <span v-if="shown.displayName" class="field-error">{{ shown.displayName }}</span></label>
            <label>登录用户名 *<input v-model="username" data-testid="wiz-username" maxlength="63" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="如 zhangsan" :aria-invalid="!!shown.username" />
              <span v-if="shown.username" class="field-error">{{ shown.username }}</span></label>
            <label>部门
              <select v-model="profile.departmentId" data-testid="wiz-department">
                <option value="">未指定</option>
                <option v-for="d in activeDepartments" :key="d.id" :value="d.id">{{ d.name }}</option>
              </select></label>
            <label>岗位<input v-model="profile.jobTitle" data-testid="wiz-job" maxlength="80" :aria-invalid="!!shown.jobTitle" />
              <span v-if="shown.jobTitle" class="field-error">{{ shown.jobTitle }}</span></label>
            <label>联系电话（可选）<input v-model="profile.phone" inputmode="tel" maxlength="32" :aria-invalid="!!shown.phone" />
              <span v-if="shown.phone" class="field-error">{{ shown.phone }}</span></label>
            <label>邮箱（可选）<input v-model="profile.email" type="email" maxlength="254" :aria-invalid="!!shown.email" />
              <span v-if="shown.email" class="field-error">{{ shown.email }}</span></label>
          </div>
          <p class="form-hint">登录用户名开通后不可修改；只能由小写字母、数字、- 和 _ 组成。</p>
        </template>

        <template v-else-if="step === 2">
          <fieldset class="perm-block">
            <legend>系统权限</legend>
            <p v-if="!canGrantRoles" class="form-hint">账号管理员不能授予系统角色；如需授予，请由系统管理员在开户后调整。</p>
            <label v-for="r in SYSTEM_ROLES" :key="r" class="check-row">
              <input type="checkbox" :checked="roles.includes(r)" :disabled="!canGrantRoles" :data-testid="`wiz-role-${r}`" @change="toggleRole(r)" />
              <span><strong>{{ SYSTEM_ROLE_TEXT[r].label }}</strong><small>{{ SYSTEM_ROLE_TEXT[r].detail }}</small></span>
            </label>
          </fieldset>
          <fieldset class="perm-block">
            <legend>项目组合权限</legend>
            <p v-if="!canGrantPortfolio" class="form-hint">仅系统管理员可在开户时授予项目组合角色。</p>
            <template v-else>
              <div v-for="(g, i) in portfolioGrants" :key="i" class="grant-row">
                <select v-model="g.scopeId" :aria-label="`项目组合 ${i + 1}`" :data-testid="`wiz-scope-${i}`">
                  <option value="" disabled>选择项目组合</option>
                  <option v-for="s in availableScopes(i)" :key="s.id" :value="s.id">{{ s.name }}</option>
                </select>
                <select v-model="g.role" :aria-label="`项目组合角色 ${i + 1}`" :data-testid="`wiz-scope-role-${i}`">
                  <option v-for="r in PORTFOLIO_ROLES" :key="r" :value="r">{{ PORTFOLIO_ROLE_LABEL[r] }}</option>
                </select>
                <button type="button" class="ghost-action" @click="removePortfolioRow(i)">删除</button>
              </div>
              <button type="button" class="ghost-action" data-testid="wiz-add-scope" :disabled="!(scopes.data.value ?? []).some((s) => s.status === 'ACTIVE')" @click="addPortfolioRow">添加项目组合角色</button>
            </template>
          </fieldset>
          <p class="form-hint">项目权限（只读 / 操作员 / 维护员 / 项目管理员）由所在项目组合的「项目组合经理」在正式项目中授予，系统权限不包含任何项目数据访问。</p>
        </template>

        <template v-else>
          <dl class="wizard-summary" data-testid="wiz-summary">
            <div><dt>姓名</dt><dd>{{ profile.displayName }}</dd></div>
            <div><dt>登录用户名</dt><dd>{{ username.trim().toLowerCase() }}</dd></div>
            <div><dt>部门 / 岗位</dt><dd>{{ deptName }} · {{ profile.jobTitle || '—' }}</dd></div>
            <div><dt>系统角色</dt><dd>{{ systemRoleNames(roles) || '无' }}</dd></div>
            <div><dt>项目组合角色</dt><dd>{{ portfolioGrants.map((g) => `${(scopes.data.value ?? []).find((s) => s.id === g.scopeId)?.name ?? g.scopeId}：${portfolioRoleName(g.role)}`).join('；') || '无' }}</dd></div>
          </dl>
          <p class="form-hint">提交后系统将：创建停用的登录账号 → 保存人员与权限 → 生成一次性初始密码（首次登录必须修改）→ 启用账号。任一步骤中断都不会产生可登录但未授权的账号，可安全重试。</p>
        </template>

        <p v-if="wizardError" class="form-error" role="alert" data-testid="wiz-error">{{ wizardError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="provision.isPending.value" @click="closeWizard">取消</button>
          <button v-if="step > 1" type="button" class="ghost-action" :disabled="provision.isPending.value" @click="back">上一步</button>
          <button type="submit" class="primary-action" data-testid="wiz-next" :disabled="provision.isPending.value">
            {{ step < 3 ? '下一步' : provision.isPending.value ? '开通中…' : wizardError ? '重试开通' : '开通账号' }}
          </button>
        </div>
      </form>
    </div>

    <OneTimePassword v-if="result?.temporaryPassword" title="账号已开通" :username="result.person.username" :display-name="result.person.displayName"
      :password="result.temporaryPassword" @close="closeResult" />
    <OneTimePassword v-if="recovered?.temporaryPassword" title="开户已完成" :username="recovered.person.username" :display-name="recovered.person.displayName"
      :password="recovered.temporaryPassword" @close="recovered = null" />
  </section>
</template>
<style scoped>
.head-actions { display: flex; gap: 8px; align-items: center; }
.dept-panel, .recovery-panel { border: 1px solid var(--line-soft); border-radius: 12px; padding: 14px; display: flex; flex-direction: column; gap: 10px; }
.dept-panel h2, .recovery-panel h2 { margin: 0; font-size: 14px; display: flex; align-items: center; gap: 6px; color: var(--text-1); }
.dept-list { list-style: none; padding: 0; margin: 0; display: flex; flex-wrap: wrap; gap: 8px; }
.dept-list li { border: 1px solid var(--line-soft); border-radius: 8px; padding: 6px 10px; display: flex; flex-direction: column; font-size: 13px; color: var(--text-1); }
.dept-list li[data-status='DISABLED'] { opacity: 0.6; }
.dept-list small { color: var(--text-3); font-size: 11px; }
.dept-form { display: flex; gap: 8px; flex-wrap: wrap; }
.dept-form input, .person-wizard input, .person-wizard select { height: 32px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; }
.person-wizard { width: min(720px, 94vw); max-height: 92vh; overflow: auto; }
.wizard-steps { display: flex; gap: 6px; list-style: none; padding: 0; margin: 0 0 6px; font-size: 12px; color: var(--text-3); }
.wizard-steps li { flex: 1; padding: 6px 8px; border-radius: 7px; border: 1px solid var(--line-soft); text-align: center; }
.wizard-steps li.current { color: var(--text-1); border-color: var(--accent, #4aa3ff); }
.wizard-steps li.done { color: #6fd39b; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.form-grid label, .perm-block label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); }
.perm-block { border: 1px solid var(--line-soft); border-radius: 10px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; margin: 0; }
.perm-block legend { padding: 0 6px; font-size: 13px; color: var(--text-1); }
.check-row { flex-direction: row !important; align-items: flex-start; gap: 8px !important; }
.check-row span { display: flex; flex-direction: column; gap: 2px; }
.check-row strong { color: var(--text-1); font-size: 13px; }
.grant-row { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) auto; gap: 8px; }
.wizard-summary { display: grid; gap: 8px; margin: 0; }
.wizard-summary div { border: 1px solid var(--line-soft); border-radius: 8px; padding: 8px 10px; }
.wizard-summary dt { font-size: 11px; color: var(--text-3); }
.wizard-summary dd { margin: 4px 0 0; color: var(--text-1); }
.row-actions { display: flex; gap: 6px; }
.status-pill[data-tone='off'] { color: #f08a8a; }
.status-pill[data-tone='warn'] { color: #e8b75d; }
.status-pill[data-tone='ok'] { color: #6fd39b; }
@media (max-width: 700px) {
  .form-grid, .grant-row { grid-template-columns: 1fr; }
  .head-actions { flex-wrap: wrap; }
  .head-actions > button { flex: 1 1 auto; justify-content: center; }
  /* R9 A2: at phone widths each person is a card (label + value rows) instead of an 8-column
     table that had to scroll sideways inside its frame (measured 375 px: table 900+ px wide). */
  .stack-table, .stack-table tbody, .stack-table tr, .stack-table td { display: block; width: 100%; }
  .stack-table thead { display: none; }
  .stack-table tr { border-bottom: 1px solid var(--line-soft); padding: 10px 12px; }
  .stack-table td { position: relative; padding: 3px 0 3px 100px; border: 0; white-space: normal; overflow-wrap: anywhere; word-break: break-word; }
  .stack-table td::before { content: attr(data-label); position: absolute; left: 0; top: 3px; width: 92px; color: var(--text-3); font-size: 12px; }
  .stack-table td:not([data-label]) { padding-left: 0; }
  .stack-table td.row-actions { display: flex; flex-wrap: wrap; gap: 6px; padding-left: 0; }
  .stack-table .status-pill { display: inline-flex; width: auto; }
  .dept-form input { flex: 1 1 140px; min-width: 0; }
  .pager { justify-content: space-between; }
  .person-wizard { width: 100vw; max-height: 100dvh; border-radius: 0; }
  .wizard-steps { flex-wrap: wrap; }
}
</style>
