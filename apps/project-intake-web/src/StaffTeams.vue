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
  open.value = true;
}
async function save() {
  if (!name.value.trim() || !reason.value.trim() || saving.value) return;
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
      <div><div class="eyebrow">权限设置 / 组织设置</div><h1>团队管理</h1><p class="muted">维护团队名称和人员归属入口</p></div>
      <ActionButton action-id="team.create" primary :run="() => edit(null)">新建团队</ActionButton>
    </div>
    <div class="staff-list" aria-label="团队列表">
      <article v-for="item in teams" :key="item.id" class="staff-card panel">
        <div class="staff-title"><h2>{{ item.name }}</h2><span class="muted">v{{ item.version }}</span></div>
        <ActionButton action-id="team.edit" :aria-label="'编辑' + item.name" :run="() => edit(item)">编辑团队</ActionButton>
      </article>
    </div>
    <Modal :open="open" :title="team ? '编辑团队' : '新建团队'" sheet @update:open="value => { if (!saving) open = value; }">
      <form class="staff-form" @submit.prevent="save">
        <label>团队名称<input v-model="name" maxlength="128" required /></label>
        <label>操作原因<textarea v-model="reason" maxlength="300" rows="3" required /></label>
        <div class="actions">
          <ActionButton action-id="team.cancel" :disabled="saving" :run="() => open = false">取消</ActionButton>
          <ActionButton action-id="team.confirm" primary :disabled="saving || !name.trim() || !reason.trim()" :run="save">确认提交</ActionButton>
        </div>
      </form>
    </Modal>
  </section>
</template>
