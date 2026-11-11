<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { onBeforeRouteLeave } from 'vue-router';
import { RefreshCw, UserPlus, X } from 'lucide-vue-next';
import { listProjectMembers, changeProjectMember } from '../cloud/portfolio';
import { allowClose } from '../cloud/draft';
import { webSession, queryWebSession, productionAuth } from '../cloud/session';
import {
  PROJECT_ROLES, PROJECT_ROLE_TEXT, validateGrantForm, grantChange, grantErrorMessage, mapStatus, type GrantForm, type ProjectRole,
} from '../cloud/project-grants';
import type { PortfolioProject, ProjectMember, ProjectGrantResult } from '../../../../contracts/typescript/types.gen';

const props = defineProps<{ project: PortfolioProject; scopeName: string }>();
const emit = defineEmits<{ close: [] }>();
const queryClient = useQueryClient();
const selfId = computed(() => (productionAuth ? webSession.value?.userId ?? null : null));
const map = computed(() => mapStatus(props.project));

// Members are only readable with project_grant_manage; others see the count from the list.
const membersQuery = useQuery({
  queryKey: computed(() => ['project-members', props.project.id]),
  queryFn: () => listProjectMembers(props.project.id),
  enabled: computed(() => props.project.canManageGrants),
});
const members = computed<ProjectMember[]>(() => membersQuery.data.value ?? []);

const form = ref<GrantForm>({ userId: '', role: '' });
const touched = ref(false);
const formError = ref('');
const notice = ref('');
const errors = computed(() => validateGrantForm(form.value, selfId.value));
const shown = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => form.value.userId.trim() !== '' || form.value.role !== '');
// The parent must ask canLeave() before switching tab / project or closing the panel, so a
// typed draft or an in-flight change is never silently discarded (DirtyGuard, Q3 fix B).
defineExpose({ canLeave: () => allowClose(dirty.value, grantMutation.isPending.value) });

async function refreshAfterChange(result: ProjectGrantResult) {
  // The change is committed whatever the re-read did (Q3 fix A). A missing list is reloaded
  // separately instead of being treated as a failed grant.
  if (result.membersRefreshed) queryClient.setQueryData(['project-members', props.project.id], result.members);
  else void membersQuery.refetch();
  // Grant state, the caller's own session (if they changed themselves — refused server-side)
  // and every project-scoped view that depends on grants.
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: ['portfolio-projects'] }),
    queryClient.invalidateQueries({ queryKey: ['assets'] }),
    queryClient.invalidateQueries({ queryKey: ['projects'] }),
    productionAuth ? queryWebSession().catch(() => null) : Promise.resolve(null),
  ]);
}

const grantMutation = useMutation({
  mutationFn: (v: { userId: string; role: ProjectRole | null }) => changeProjectMember(props.project.id, grantChange(v.userId, v.role, members.value)),
  onSuccess: async (result, v) => {
    notice.value = v.role ? `已将 ${v.userId} 设为「${PROJECT_ROLE_TEXT[v.role].label}」，立即生效。` : `已撤销 ${v.userId} 的项目权限，其现有会话的下一次请求即失效。`;
    if (!result.membersRefreshed) notice.value += '（成员列表刷新失败，正在重新加载；授权本身已生效）';
    form.value = { userId: '', role: '' };
    touched.value = false;
    await refreshAfterChange(result);
  },
  onError: async (e) => {
    formError.value = grantErrorMessage(e);
    await membersQuery.refetch();
  },
});

function submit() {
  touched.value = true;
  formError.value = '';
  notice.value = '';
  if (grantMutation.isPending.value || Object.keys(errors.value).length) return;
  const role = form.value.role as ProjectRole;
  const existing = members.value.find((m) => m.userId === form.value.userId.trim());
  if (existing && !window.confirm(`${existing.userId} 当前为「${PROJECT_ROLE_TEXT[existing.role as ProjectRole]?.label ?? existing.role}」，确定改为「${PROJECT_ROLE_TEXT[role].label}」吗？`)) return;
  grantMutation.mutate({ userId: form.value.userId.trim(), role });
}
function changeRole(m: ProjectMember, role: ProjectRole) {
  if (grantMutation.isPending.value || role === m.role) return;
  if (!window.confirm(`将 ${m.userId} 的项目角色由「${PROJECT_ROLE_TEXT[m.role as ProjectRole]?.label ?? m.role}」改为「${PROJECT_ROLE_TEXT[role].label}」？`)) return;
  formError.value = '';
  grantMutation.mutate({ userId: m.userId, role });
}
function revoke(m: ProjectMember) {
  if (grantMutation.isPending.value) return;
  if (!window.confirm(`撤销 ${m.userId} 对「${props.project.name}」的全部项目权限？其正在使用的会话将在下一次请求时失去访问。`)) return;
  formError.value = '';
  grantMutation.mutate({ userId: m.userId, role: null });
}
function close() {
  if (!allowClose(dirty.value, grantMutation.isPending.value)) return;
  emit('close');
}
function onBeforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || grantMutation.isPending.value) {
    event.preventDefault();
    event.returnValue = '';
  }
}
onMounted(() => window.addEventListener('beforeunload', onBeforeUnload));
onBeforeUnmount(() => window.removeEventListener('beforeunload', onBeforeUnload));
onBeforeRouteLeave(() => allowClose(dirty.value, grantMutation.isPending.value));
</script>
<template>
  <section class="formal-panel" data-testid="formal-panel" :aria-label="`正式项目 ${project.name}`">
    <header class="formal-panel-head">
      <div>
        <span class="eyebrow">正式项目</span>
        <h2>{{ project.name }}</h2>
        <p class="cell-sub">{{ project.id }} · 项目组合：{{ scopeName }} · 来自登记 {{ project.intakeCode }}</p>
      </div>
      <button class="ghost-action" aria-label="关闭正式项目" @click="close"><X :size="14" /></button>
    </header>

    <dl class="formal-facts">
      <div><dt>项目阶段</dt><dd>{{ project.projectPhase ?? '未设置' }}</dd></div>
      <div><dt>项目成员</dt><dd data-testid="formal-grant-status">{{ project.grantStatus === 'NO_MEMBERS' ? '尚未配置（审批不会自动授权）' : `${project.memberCount} 名` }}</dd></div>
      <div><dt>运营地图</dt><dd :data-tone="map.tone">{{ map.text }}</dd></div>
      <div><dt>您的管理权限</dt><dd>{{ project.canManageGrants ? '项目组合经理：可配置项目成员' : '仅查看（配置成员需要项目组合经理角色）' }}</dd></div>
    </dl>

    <template v-if="project.canManageGrants">
      <h3>项目成员（项目权限）</h3>
      <div v-if="membersQuery.isLoading.value" class="skeleton" style="height: 60px" aria-busy="true"></div>
      <div v-else-if="membersQuery.isError.value" class="workspace-error" role="alert" data-testid="members-error">
        {{ grantErrorMessage(membersQuery.error.value) }}
        <button class="ghost-action" @click="membersQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </div>
      <p v-else-if="!members.length" class="workspace-hint" data-testid="members-empty">尚无项目成员。被授权前，任何人（包括项目组合经理和平台管理员）都无法访问该项目的资产。</p>
      <table v-else class="data-table" data-testid="members-table">
        <thead><tr><th>账号</th><th>项目角色</th><th>状态</th><th></th></tr></thead>
        <tbody>
          <tr v-for="m in members" :key="m.userId" :data-member="m.userId">
            <td>{{ m.displayName || m.userId }}<div class="cell-sub">{{ m.userId }}</div></td>
            <td>
              <select :value="m.role" :aria-label="`${m.userId} 的项目角色`" :disabled="grantMutation.isPending.value" @change="changeRole(m, ($event.target as HTMLSelectElement).value as ProjectRole)">
                <option v-for="r in PROJECT_ROLES" :key="r" :value="r">{{ PROJECT_ROLE_TEXT[r].label }}</option>
              </select>
            </td>
            <td>{{ m.enabled ? '启用' : '已停用（不生效）' }}</td>
            <td><button class="danger-action" :disabled="grantMutation.isPending.value" data-testid="member-revoke" @click="revoke(m)">撤销</button></td>
          </tr>
        </tbody>
      </table>

      <form class="grant-form" data-testid="grant-form" novalidate @submit.prevent="submit">
        <h3><UserPlus :size="14" />授予项目权限</h3>
        <div class="form-row">
          <label>平台账号 ID *
            <input v-model="form.userId" aria-label="平台账号 ID" maxlength="80" autocomplete="off" placeholder="被授权人的精确账号 ID" :aria-invalid="!!shown.userId" />
            <span v-if="shown.userId" class="field-error">{{ shown.userId }}</span>
          </label>
          <label>项目角色 *
            <select v-model="form.role" aria-label="项目角色" :aria-invalid="!!shown.role">
              <option value="" disabled>选择项目角色</option>
              <option v-for="r in PROJECT_ROLES" :key="r" :value="r">{{ PROJECT_ROLE_TEXT[r].label }}</option>
            </select>
            <span v-if="shown.role" class="field-error">{{ shown.role }}</span>
          </label>
        </div>
        <p v-if="form.role" class="form-hint" data-testid="role-detail">{{ PROJECT_ROLE_TEXT[form.role].label }}：{{ PROJECT_ROLE_TEXT[form.role].detail }}</p>
        <p class="form-hint">只能授权已在本平台登录过的启用账号；不提供账号搜索。服务端会核对账号、角色与项目归属。</p>
        <p v-if="formError" class="form-error" role="alert" data-testid="grant-error">{{ formError }}</p>
        <p v-if="notice" class="workspace-notice" role="status" data-testid="grant-notice">{{ notice }}</p>
        <div class="modal-actions">
          <button type="submit" class="primary-action" :disabled="grantMutation.isPending.value" data-testid="grant-submit">
            {{ grantMutation.isPending.value ? '保存中…' : '授予权限' }}
          </button>
        </div>
      </form>
    </template>
    <p v-else class="workspace-hint" data-testid="grant-readonly">您可以查看该正式项目，但配置项目成员需要所在项目组合的「项目组合经理」角色。</p>
  </section>
</template>
<style scoped>
.formal-panel { border: 1px solid var(--line-soft); border-radius: 12px; padding: 18px; display: flex; flex-direction: column; gap: 14px; background: rgba(13, 31, 54, 0.55); }
.formal-panel-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.formal-panel h2 { margin: 4px 0; font-size: 18px; color: var(--text-1); }
.formal-panel h3 { margin: 0; font-size: 14px; color: var(--text-1); display: flex; align-items: center; gap: 6px; }
.formal-facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 10px; margin: 0; }
.formal-facts div { border: 1px solid var(--line-soft); border-radius: 8px; padding: 8px 10px; }
.formal-facts dt { font-size: 11px; color: var(--text-3); }
.formal-facts dd { margin: 4px 0 0; font-size: 13px; color: var(--text-2); }
.formal-facts dd[data-tone='pending'] { color: #e8b75d; }
.formal-facts dd[data-tone='ok'] { color: #6fd39b; }
.grant-form { display: flex; flex-direction: column; gap: 10px; border-top: 1px solid var(--line-soft); padding-top: 12px; }
.grant-form label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); }
.grant-form input, .grant-form select, .data-table select {
  height: 32px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px;
}
.form-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; }
.form-hint { margin: 0; font-size: 12px; color: var(--text-3); }
.field-error { color: #ff665f; font-size: 11px; }
.workspace-notice { color: #6fd39b; font-size: 13px; margin: 0; }
.workspace-hint { color: var(--text-3); font-size: 13px; margin: 0; }
.cell-sub { font-size: 11px; color: var(--text-3); margin: 0; }
</style>
