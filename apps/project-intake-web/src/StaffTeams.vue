<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
import { ApiError } from './api-client';
import { when } from './model';
import { staffEvents, type StaffEvent, type StaffTeam } from './staff-model';

type Dialog = 'edit' | 'archive' | 'restore' | 'history';
const app = useApp();
const teams = ref<StaffTeam[]>([]);
const team = ref<StaffTeam | null>(null);
const dialog = ref<Dialog | null>(null);
const name = ref('');
const reason = ref('');
const saving = ref(false);
const loading = ref(true);
const history = ref<StaffEvent[]>([]);
const formIssue = ref('');
const errors = ref<Record<string, string>>({});
const active = computed(() => teams.value.filter(t => t.status !== 'ARCHIVED'));
const archived = computed(() => teams.value.filter(t => t.status === 'ARCHIVED'));
// The server refuses the archive while members or unfinished projects remain;
// the page says why before the administrator tries.
const blocker = (t: StaffTeam) => t.memberCount > 0
  ? `还有 ${t.memberCount} 名成员，请先调整他们的团队或停用并归档`
  : t.openProjectCount > 0 ? `还有 ${t.openProjectCount} 个未结束项目，请先移交或结束` : '';

async function load() {
  loading.value = true;
  try {
    teams.value = (await app.api<{items: StaffTeam[]}>('/api/v1/teams')).items;
  } finally {
    loading.value = false;
  }
}
function openDialog(kind: Dialog, value: StaffTeam | null) {
  team.value = value;
  name.value = value?.name ?? '';
  reason.value = '';
  errors.value = {};
  formIssue.value = '';
  dialog.value = kind;
}
async function openHistory(value: StaffTeam) {
  openDialog('history', value);
  history.value = [];
  try {
    history.value = (await app.api<{history: StaffEvent[]}>('/api/v1/teams/' + value.id)).history;
  } catch (error) {
    formIssue.value = (error as Error).message;
  }
}
function close(value: boolean) {
  if (!value && !saving.value) dialog.value = null;
}
async function save() {
  if (saving.value || !dialog.value || dialog.value === 'history') return;
  const kind = dialog.value;
  const e: Record<string, string> = {};
  if (kind === 'edit' && !name.value.trim()) e.teamName = '请填写团队名称';
  if (!reason.value.trim()) e.teamReason = '请填写操作原因';
  errors.value = e;
  formIssue.value = '';
  const first = Object.keys(e)[0];
  if (first) {
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[name="${first}"]`)?.focus());
    formIssue.value = `还有 ${Object.keys(e).length} 项需要填写：` + Object.values(e).join('；');
    return;
  }
  saving.value = true;
  const existing = team.value;
  try {
    if (kind === 'edit') {
      await app.api('/api/v1/teams' + (existing ? '/' + existing.id : ''), existing ? 'PATCH' : 'POST', {
        name: name.value, reason: reason.value, version: existing?.version ?? 0,
      });
    } else if (existing) {
      await app.api(`/api/v1/teams/${existing.id}/${kind}`, 'POST', { version: existing.version, reason: reason.value });
    }
    dialog.value = null;
    app.toast({ edit: '团队已保存', archive: '团队已归档，可在“已归档团队”中查看和恢复', restore: '团队已恢复，可重新选择' }[kind]);
    await load();
  } catch (error) {
    // Keep the dialog and what was typed; show the server's reason inside it.
    if (error instanceof ApiError) {
      formIssue.value = error.message;
      // Server field errors (details.field) go next to the field and get focus.
      const field = ({ name: 'teamName', reason: 'teamReason' } as Record<string, string>)[error.field];
      if (field) {
        errors.value = { [field]: error.message };
        requestAnimationFrame(() => document.querySelector<HTMLElement>(`[name="${field}"]`)?.focus());
      }
      if (error.code === 'STAFF_CONFLICT') formIssue.value = '团队已被他人修改，已刷新最新信息，请核对后重试。';
      await load().catch(() => undefined);
      const fresh = teams.value.find(t => t.id === existing?.id);
      if (fresh) team.value = fresh;
      return;
    }
    throw error;
  } finally {
    saving.value = false;
  }
}
onMounted(async () => {
  try { await load(); } catch (error) { app.error = (error as Error).message; }
});
const titles: Record<Dialog, string> = { edit: '编辑团队', archive: '归档团队', restore: '恢复团队', history: '团队记录' };
</script>

<template>
  <section class="staff-page">
    <div class="page-heading">
      <div><h1>团队管理</h1><p class="muted">维护团队名称、成员归属；不再使用的团队可归档并随时恢复</p></div>
      <ActionButton action-id="team.create" primary :run="() => openDialog('edit', null)">新建团队</ActionButton>
    </div>
    <p v-if="loading && !teams.length" role="status">正在加载团队…</p>
    <div v-else class="staff-list" aria-label="团队列表">
      <p v-if="!active.length" class="muted">还没有使用中的团队。</p>
      <article v-for="item in active" :key="item.id" class="staff-card panel" :data-team="item.name">
        <div class="staff-title"><h2>{{ item.name }}</h2><span class="staff-status" data-state="ACTIVE">使用中</span></div>
        <p class="muted">成员 {{ item.memberCount }} 人 · 未结束项目 {{ item.openProjectCount }} 个</p>
        <div class="actions">
          <ActionButton action-id="team.edit" :aria-label="'编辑' + item.name" :run="() => openDialog('edit', item)">编辑团队</ActionButton>
          <ActionButton action-id="team.history" :aria-label="'查看' + item.name + '记录'" :run="() => openHistory(item)">查看记录</ActionButton>
          <ActionButton
            action-id="team.archive" :aria-label="'归档' + item.name" :disabled="!!blocker(item)" :reason="blocker(item)"
            :run="() => openDialog('archive', item)"
          >
            归档团队
          </ActionButton>
        </div>
      </article>
    </div>
    <div v-if="archived.length" class="staff-list" aria-label="已归档团队">
      <h2 class="section-title">已归档团队 · {{ archived.length }}</h2>
      <article v-for="item in archived" :key="item.id" class="staff-card panel" :data-team="item.name">
        <div class="staff-title"><h2>{{ item.name }}</h2><span class="staff-status" data-state="ARCHIVED">已归档</span></div>
        <p class="muted">归档于 {{ when(item.archivedAt) }}。历史项目和审批记录保留不变；归档期间不能被新成员选择。</p>
        <div class="actions">
          <ActionButton action-id="team.history" :aria-label="'查看' + item.name + '记录'" :run="() => openHistory(item)">查看记录</ActionButton>
          <ActionButton action-id="team.restore" primary :aria-label="'恢复' + item.name" :run="() => openDialog('restore', item)">恢复团队</ActionButton>
        </div>
      </article>
    </div>
    <Modal
      :open="dialog !== null" :title="dialog === 'edit' && !team ? '新建团队' : dialog ? titles[dialog] : ''"
      :description="dialog === 'archive' ? '归档后该团队不能再被新成员选择，历史项目、审批和记录保持不变，可随时恢复。'
        : dialog === 'restore' ? '恢复后该团队可重新被成员选择。' : dialog === 'history' ? undefined
          : team ? '修改团队名称，变更会记录原因。' : '团队名称不能与现有团队（含已归档）重复。'"
      sheet @update:open="close"
    >
      <div v-if="dialog === 'history'">
        <p v-if="formIssue" class="form-summary" role="alert">{{ formIssue }}</p>
        <p v-else-if="!history.length" class="muted">正在加载记录…</p>
        <ol class="staff-audit" aria-label="团队记录">
          <li v-for="(event, index) in history" :key="index">
            <div><strong>{{ staffEvents[event.action] || event.action }}</strong><span class="muted">{{ event.actor }} · {{ when(event.at) }}</span></div>
            <p>{{ event.reason }}</p>
          </li>
        </ol>
        <div class="actions"><ActionButton action-id="team.cancel" :run="() => dialog = null">关闭</ActionButton></div>
      </div>
      <form v-else class="staff-form" novalidate @submit.prevent="save">
        <div v-if="formIssue" class="form-summary" tabindex="-1" role="alert"><strong>{{ formIssue }}</strong></div>
        <p v-if="team && dialog !== 'edit'" class="staff-form-who">{{ team.name }} · 成员 {{ team.memberCount }} 人 · 未结束项目 {{ team.openProjectCount }} 个</p>
        <label v-if="dialog === 'edit'" :class="{ invalid: errors.teamName }">团队名称 <span class="req" aria-hidden="true">*</span><input v-model="name" name="teamName" maxlength="128" aria-required="true" :aria-invalid="!!errors.teamName" /><small v-if="errors.teamName" class="field-error">{{ errors.teamName }}</small></label>
        <label :class="{ invalid: errors.teamReason }">操作原因 <span class="req" aria-hidden="true">*</span><textarea v-model="reason" name="teamReason" maxlength="300" rows="3" aria-required="true" :aria-invalid="!!errors.teamReason" /><small v-if="errors.teamReason" class="field-error">{{ errors.teamReason }}</small></label>
        <div class="actions">
          <ActionButton action-id="team.cancel" :disabled="saving" :run="() => dialog = null">取消</ActionButton>
          <ActionButton action-id="team.confirm" primary :disabled="saving" :run="save">
            {{ dialog === 'archive' ? '确认归档' : dialog === 'restore' ? '确认恢复' : '保存团队' }}
          </ActionButton>
        </div>
      </form>
    </Modal>
  </section>
</template>
