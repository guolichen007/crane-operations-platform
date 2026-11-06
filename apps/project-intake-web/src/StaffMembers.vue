<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
import { ApiError } from './api-client';
import { clearStaffRoleDraft, readStaffRoleDraft, saveStaffRoleDraft, type StaffRoleAction } from './staff-role-draft';
import { when } from './model';
import {
  capabilities, roles, staffEvents, statuses, syncStatuses,
  type StaffEvent, type StaffMember, type StaffProject, type StaffTeam,
} from './staff-model';

type Dialog = 'invite' | 'edit' | 'verify' | 'permissions' | 'suspend' | 'reactivate' | 'retry' | 'archive' | 'transfer' | StaffRoleAction;
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
const form = ref({ name: '', email: '', teamId: '', role: 'baobei_sales', note: '', reason: '', permissions: [] as string[] });
const target = ref('');
const targetSearch = ref('');
const targets = ref<StaffMember[]>([]);
const results = ref<{id: string; status: string}[]>([]);
const working = ref(false);
const reauthReady = ref(false);
const roleVersion = ref(0);
const titles: Record<Dialog, string> = {
  invite: '添加团队成员', edit: '编辑成员资料', permissions: '设置业务权限',
  verify: '完善企业身份', promote: '设为销售主管', demote: '恢复为普通销售',
  suspend: '确认停用账号', reactivate: '申请恢复账号', retry: '重试身份任务',
  archive: '确认归档账号', transfer: '批量移交项目',
};
const descriptions: Partial<Record<Dialog, string>> = {
  invite: '提交后等待管理员核对企业账号及 Baobei 角色。',
  verify: '提交真实工作邮箱以重新核对企业身份。原会话会撤销，核对和员工登录激活后恢复访问。',
  promote: '变更提交后会立即撤销旧会话。身份运维同步角色，员工重新登录激活后，主管权限才生效。',
  demote: '提交后立即撤销主管会话。身份运维移除主管角色，员工重新登录激活后恢复销售权限。',
  suspend: '此操作立即撤销 Baobei 会话，未结束项目进入待移交清单。',
  reactivate: '完成管理员核对和企业账号激活后，成员才能再次登录。',
  transfer: '仅移交选中项目，保留创建人、审批和重复裁决。',
  archive: '归档保留人员历史记录。',
};
const canManage = computed(() => member.value?.canManage ?? false);
const roleAction = computed(() => dialog.value === 'promote' || dialog.value === 'demote' ? dialog.value : null);
const showRoleAction = computed(() => administrator.value && canManage.value && member.value?.status === 'ACTIVE' &&
  ['baobei_sales', 'baobei_manager'].includes(member.value.role));
const failureReason = computed(() => member.value?.syncStatus === 'FAILED'
  ? events.value.find(event => event.action === 'staff.provision.fail')?.reason : '');
const roleStatus: Record<string, string> = {
  PENDING_PROVISION: '待身份运维同步', PENDING_ACTIVATION: '待员工登录激活',
  ACTIVE: '已生效', PROVISION_FAILED: '身份同步失败',
};
const needsTeam = computed(() => ['baobei_sales','baobei_manager'].includes(form.value.role));
const profileDialog = computed(() => dialog.value === 'invite' || dialog.value === 'edit');
const formReady = computed(() => form.value.reason.trim().length > 0 &&
  (!profileDialog.value || form.value.name.trim().length > 0 && form.value.email.trim().length > 0 &&
    (!needsTeam.value || !!form.value.teamId)) &&
  (dialog.value !== 'transfer' || !!target.value && selected.value.length > 0));

async function load() {
  loading.value = true;
  try {
    if (memberId.value) {
      const path = '/api/v1/staff/' + memberId.value;
      const [detail, history] = await Promise.all([
        app.api<{member: StaffMember; projects: StaffProject[]; recentReauth: boolean}>(path),
        app.api<{items: StaffEvent[]}>(path + '/audit'),
      ]);
      member.value = detail.member;
      reauthReady.value = detail.recentReauth;
      projects.value = detail.projects;
      events.value = history.items;
      selected.value = selected.value.filter(id => projects.value.some(project => project.id === id));
    } else {
      const params = new URLSearchParams({q: q.value, status: status.value, sort: sort.value, page: String(page.value)});
      const result = await app.api<{items: StaffMember[]; total: number; pageSize: number}>('/api/v1/staff?' + params);
      members.value = result.items;
      total.value = result.total;
      pageSize.value = result.pageSize;
    }
  } finally {
    loading.value = false;
  }
}

function reauthenticate() { window.location.assign('/auth/login?reauth=1'); }

function reauthenticateForRole() {
  if (!app.user || !member.value || !roleAction.value || !formReady.value) return;
  saveStaffRoleDraft({
    actor: app.user.subject, staffId: member.value.id, action: roleAction.value,
    version: roleVersion.value, reason: form.value.reason.trim(), expiresAt: Date.now() + 300_000,
  });
  reauthenticate();
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
  const existing = member.value;
  roleVersion.value = existing?.version ?? 0;
  form.value = {
    name: existing?.name ?? '',
    email: existing?.email ?? '',
    teamId: existing?.teamId ?? app.user?.teamId ?? '',
    role: existing?.role ?? 'baobei_sales',
    note: existing?.note ?? '',
    reason: '',
    permissions: [...(existing?.permissions ?? Object.keys(capabilities))],
  };
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

async function save() {
  const kind = dialog.value;
  if (!kind || !formReady.value || working.value || roleAction.value && !reauthReady.value) return;
  working.value = true;
  const path = '/api/v1/staff/' + memberId.value;
  const reason = form.value.reason.trim();
  const version = member.value?.version ?? 0;
  try {
    if (kind === 'promote' || kind === 'demote' || kind === 'verify') {
      const person = member.value;
      if (!person) return;
      const role = kind === 'promote' ? 'baobei_manager' : kind === 'demote' ? 'baobei_sales' : person.role;
      await app.api(path, 'PATCH', {
        name: person.name, email: kind === 'verify' ? form.value.email : person.email,
        teamId: person.teamId, note: person.note, role, reason, version: roleVersion.value,
        ...(kind === 'verify' ? {verifyIdentity: true} : {}),
      });
    } else if (kind === 'invite' || kind === 'edit') {
      const payload = {
        name: form.value.name, email: form.value.email, teamId: needsTeam.value ? form.value.teamId : '',
        role: form.value.role, note: form.value.note, reason,
      };
      if (kind === 'invite') {
        await app.api('/api/v1/staff/invitations', 'POST', {
          ...payload, permissions: payload.role === 'baobei_sales' ? form.value.permissions : Object.keys(capabilities),
        });
        q.value = payload.email;
        status.value = '';
        page.value = 1;
      } else {
        await app.api(path, 'PATCH', {...payload, version});
      }
    } else if (kind === 'permissions') {
      await app.api(path + '/permissions', 'PATCH', {version, reason, permissions: form.value.permissions});
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
    dialog.value = null;
    app.toast(['invite', 'reactivate', 'promote', 'demote', 'verify'].includes(kind)
      ? '已提交身份核对，等待处理' : '操作已保存');
    await load();
  } catch (error) {
    if (roleAction.value && error instanceof ApiError && error.code === 'STAFF_REAUTH_REQUIRED') reauthReady.value = false;
    throw error;
  } finally {
    working.value = false;
  }
}

onMounted(async () => {
  try {
    await load();
    const draft = readStaffRoleDraft();
    if (!draft || !member.value || draft.staffId !== member.value.id) return;
    clearStaffRoleDraft();
    if (!administrator.value || draft.actor !== app.user?.subject) return;
    if (draft.version !== member.value.version || !showRoleAction.value || !member.value.identityVerified ||
        (draft.action === 'promote' ? 'baobei_sales' : 'baobei_manager') !== member.value.role) {
      app.error = '人员记录已变化，请重新发起授权。';
      return;
    }
    await open(draft.action);
    form.value.reason = draft.reason;
  } catch (error) { app.error = (error as Error).message; }
});
</script>

<template>
  <section class="staff-page">
    <div class="page-heading">
      <div>
        <div class="eyebrow">销售运营 / 人员治理</div>
        <h1>{{ memberId ? '成员详情' : administrator ? '人员管理' : '团队成员' }}</h1>
        <p class="muted">账号状态、业务权限与项目交接</p>
      </div>
      <div class="actions">
        <ActionButton v-if="administrator" action-id="staff.reauth" :run="reauthenticate">企业重新认证</ActionButton>
        <ActionButton v-if="!memberId" action-id="staff.invite" primary :run="() => open('invite')">添加成员</ActionButton>
        <ActionButton v-else action-id="staff.back" :run="() => router.push(administrator ? '/settings/users' : '/team/members')">返回成员列表</ActionButton>
      </div>
    </div>

    <div v-if="!memberId">
      <div class="staff-filters panel">
        <label>姓名或邮箱<input v-model="q" aria-label="姓名或邮箱" maxlength="254" @keyup.enter="searchFromInput" /></label>
        <label>账号状态<select v-model="status" @change="searchFromInput"><option value="">全部状态</option>
          <option v-for="(label, value) in statuses" :key="value" :value="value">{{ label }}</option>
        </select></label>
        <label>排序<select v-model="sort" @change="searchFromInput">
          <option value="name">姓名</option><option value="email">邮箱</option>
          <option value="lastLogin">最近登录</option><option value="projects">项目数</option>
        </select></label>
        <ActionButton action-id="staff.search" :run="() => changePage(1)" :disabled="loading">搜索</ActionButton>
      </div>
      <p v-if="loading" role="status">正在加载成员…</p>
      <p v-else-if="!members.length" class="muted">没有符合条件的成员</p>
      <div v-else class="staff-list" aria-label="成员列表">
        <article v-for="item in members" :key="item.id" class="staff-row panel">
          <div class="staff-name"><strong>{{ item.name }}</strong><span class="muted">{{ item.email || '邮箱待补充' }}</span></div>
          <span>{{ item.team || '全局角色' }} · {{ roles[item.role] }}</span>
          <span class="staff-status" :data-state="item.status">{{ statuses[item.status] }}</span>
          <span>{{ item.projectCount }} 项 · 登录 {{ when(item.lastLogin) }}</span>
          <ActionButton action-id="staff.detail" :run="() => router.push('/team/members/' + item.id)" :aria-label="'查看' + item.name">查看详情</ActionButton>
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
        <div class="staff-title"><h2>{{ member.name }}</h2><span class="staff-status" :data-state="member.status">{{ statuses[member.status] }}</span></div>
        <dl class="staff-facts">
          <div><dt>工作邮箱</dt><dd>{{ member.email || '待补充' }}</dd></div>
          <div><dt>{{ member.roleChanged && member.status !== 'ACTIVE' ? '团队与目标角色' : '团队与角色' }}</dt><dd>{{ member.team || '全局角色' }} · {{ roles[member.role] }}</dd></div>
          <div><dt>最近登录</dt><dd>{{ when(member.lastLogin) }}</dd></div>
          <div><dt>身份任务</dt><dd>{{ syncStatuses[member.syncStatus] }}</dd></div>
          <div v-if="administrator"><dt>企业身份</dt><dd>{{ member.identityVerified ? '已核验' : '待核验' }}</dd></div>
          <div v-if="member.roleChanged"><dt>授权状态</dt><dd>{{ roleStatus[member.status] || statuses[member.status] }}</dd></div>
          <div><dt>人员编号 / 版本</dt><dd>{{ member.id }} / {{ member.version }}</dd></div>
          <div><dt>备注</dt><dd>{{ member.note || '—' }}</dd></div>
        </dl>
        <p v-if="member.status === 'PENDING_PROVISION'" class="muted">等待管理员处理受控开通回执，成员暂不可登录。</p>
        <p v-if="member.status === 'PENDING_ACTIVATION'" class="muted">等待成员使用已核对的企业账号完成登录激活。</p>
        <p v-if="failureReason" role="status">最近一次同步失败：{{ failureReason }}</p>
        <p v-if="showRoleAction && !member.identityVerified" class="muted">
          {{ member.identityLinked ? '请先完善工作邮箱并完成企业身份核验。' : '请联系身份运维核对企业账号标识。' }}
        </p>
        <div v-if="canManage" class="actions">
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
            完善企业身份
          </ActionButton>
          <ActionButton action-id="staff.edit" :run="() => open('edit')">编辑资料</ActionButton>
          <ActionButton v-if="member.status !== 'SUSPENDED'" action-id="staff.suspend" :run="() => open('suspend')">停用账号</ActionButton>
          <ActionButton v-if="member.status === 'SUSPENDED'" action-id="staff.reactivate" :disabled="!member.email" reason="请先补充工作邮箱" :run="() => open('reactivate')">申请恢复</ActionButton>
          <ActionButton v-if="member.status === 'PROVISION_FAILED' || member.status === 'SUSPENDED' && member.syncStatus === 'FAILED'" action-id="staff.retry" :run="() => open('retry')">重试身份任务</ActionButton>
          <ActionButton v-if="administrator && member.status === 'SUSPENDED'" action-id="staff.archive" :run="() => open('archive')">归档账号</ActionButton>
        </div>
      </div>
      <div v-if="member.role === 'baobei_sales'" class="staff-card panel">
        <h2>业务权限</h2>
        <ul><li v-for="(label, key) in capabilities" :key="key">{{ label }} · {{ member.permissions.includes(key) ? '允许' : '关闭' }}</li></ul>
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
          <ActionButton v-if="canManage" action-id="staff.transfer" :disabled="!selected.length" :run="() => open('transfer')">移交所选项目</ActionButton>
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
        <h2>最近操作</h2>
        <p v-if="!events.length" class="muted">暂无人员操作记录</p>
        <ol class="staff-audit">
          <li v-for="(event,index) in events" :key="index">
            <strong>{{ staffEvents[event.action] || event.action }}</strong>
            <span class="muted"> {{ event.actor }} · {{ when(event.at) }} · v{{ event.version }}</span><p>{{ event.reason }}</p>
          </li>
        </ol>
      </div>
    </template>

    <Modal
      :open="dialog !== null" :title="dialog ? titles[dialog] : ''" :description="dialog ? descriptions[dialog] : undefined" sheet
      @update:open="value => { if (!value && !working) dialog = null; }"
    >
      <form class="staff-form" @submit.prevent="save">
        <p v-if="roleAction || dialog === 'verify'">
          {{ member?.name }} · {{ member?.team || '全局角色' }} · {{ roles[member?.role ?? ''] }}
        </p>
        <label v-if="dialog === 'verify'">工作邮箱<input v-model="form.email" required type="email" maxlength="254" /></label>
        <template v-if="profileDialog">
          <label>姓名<input v-model="form.name" required maxlength="128" /></label>
          <label>工作邮箱<input v-model="form.email" required type="email" maxlength="254" /></label>
          <label v-if="administrator">角色<select v-model="form.role">
            <option v-for="(label,key) in roles" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <label v-else>角色<input :value="roles[form.role]" disabled /></label>
          <label v-if="needsTeam">团队<select v-model="form.teamId" :disabled="supervisor" required>
            <option value="">选择团队</option><option v-for="team in teams" :key="team.id" :value="team.id">{{ team.name }}</option>
          </select></label>
          <label>备注<textarea v-model="form.note" maxlength="1000" rows="3" /></label>
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
          <label>接收销售<select v-model="target" required>
            <option value="">请选择</option>
            <option v-for="item in targets" :key="item.id" :value="item.id">{{ item.name }} · {{ item.team }}</option>
          </select></label>
        </template>
        <label>操作原因<textarea v-model="form.reason" required maxlength="300" rows="3" /></label>
        <div class="actions">
          <ActionButton action-id="staff.cancel" :disabled="working" :run="() => dialog = null">取消</ActionButton>
          <ActionButton
            v-if="roleAction && !reauthReady" action-id="staff.role-reauth"
            primary :disabled="!formReady" :run="reauthenticateForRole"
          >
            重新认证并返回
          </ActionButton>
          <ActionButton
            v-else action-id="staff.confirm" primary
            :disabled="!formReady || working || dialog === 'verify' && !form.email.trim()" :run="save"
          >
            确认提交
          </ActionButton>
        </div>
      </form>
    </Modal>
  </section>
</template>
