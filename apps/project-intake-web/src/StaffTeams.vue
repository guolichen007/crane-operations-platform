<script setup lang="ts">
import { onMounted, ref } from 'vue';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
import { useApp } from './store';
import type { StaffTeam } from './staff-model';

const app = useApp();
const teams = ref<StaffTeam[]>([]);
const team = ref<StaffTeam | null>(null);
const open = ref(false);
const name = ref('');
const reason = ref('');
const saving = ref(false);
async function load() {
  teams.value = (await app.api<{items: StaffTeam[]}>('/api/v1/teams')).items;
}
function edit(value: StaffTeam | null) {
  team.value = value;
  name.value = value?.name ?? '';
  reason.value = '';
  errors.value = {};
  open.value = true;
}
const errors = ref<Record<string, string>>({});
async function save() {
  if (saving.value) return;
  const e: Record<string, string> = {};
  if (!name.value.trim()) e.teamName = '请填写团队名称';
  if (!reason.value.trim()) e.teamReason = '请填写操作原因';
  errors.value = e;
  const first = Object.keys(e)[0];
  if (first) {
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[name="${first}"]`)?.focus());
    throw new Error(`还有 ${Object.keys(e).length} 项需要填写：` + Object.values(e).join('；'));
  }
  saving.value = true;
  try {
    const existing = team.value;
    await app.api('/api/v1/teams' + (existing ? '/' + existing.id : ''), existing ? 'PATCH' : 'POST', {
      name: name.value, reason: reason.value, version: existing?.version ?? 0,
    });
    open.value = false;
    app.toast('团队已保存');
    await load();
  } finally {
    saving.value = false;
  }
}
onMounted(async () => {
  try { await load(); } catch (error) { app.error = (error as Error).message; }
});
</script>

<template>
  <section class="staff-page">
    <div class="page-heading">
      <div><h1>团队管理</h1><p class="muted">维护团队名称和人员归属入口</p></div>
      <ActionButton action-id="team.create" primary :run="() => edit(null)">新建团队</ActionButton>
    </div>
    <div class="staff-list" aria-label="团队列表">
      <article v-for="item in teams" :key="item.id" class="staff-card panel">
        <div class="staff-title"><h2>{{ item.name }}</h2><span class="muted">v{{ item.version }}</span></div>
        <ActionButton action-id="team.edit" :aria-label="'编辑' + item.name" :run="() => edit(item)">编辑团队</ActionButton>
      </article>
    </div>
    <Modal :open="open" :title="team ? '编辑团队' : '新建团队'" :description="team ? '修改团队名称，变更会记录原因。' : '团队名称不能与现有团队重复。'" sheet @update:open="value => { if (!saving) open = value; }">
      <form class="staff-form" @submit.prevent="save">
        <label :class="{ invalid: errors.teamName }">团队名称 <span class="req" aria-hidden="true">*</span><input v-model="name" name="teamName" maxlength="128" aria-required="true" :aria-invalid="!!errors.teamName" /><small v-if="errors.teamName" class="field-error">{{ errors.teamName }}</small></label>
        <label :class="{ invalid: errors.teamReason }">操作原因 <span class="req" aria-hidden="true">*</span><textarea v-model="reason" name="teamReason" maxlength="300" rows="3" aria-required="true" :aria-invalid="!!errors.teamReason" /><small v-if="errors.teamReason" class="field-error">{{ errors.teamReason }}</small></label>
        <div class="actions">
          <ActionButton action-id="team.cancel" :disabled="saving" :run="() => open = false">取消</ActionButton>
          <ActionButton action-id="team.confirm" primary :disabled="saving" :run="save">保存团队</ActionButton>
        </div>
      </form>
    </Modal>
  </section>
</template>
