<script setup lang="ts">
// V1.1 R2: one scope selector for list, pipeline and statistics. The choices
// come from the server (availableScopes per role; members and teams from
// /api/v1/analytics/members). It only shapes the request; the server checks it.
import { ref, computed, onMounted } from 'vue';
import { useApp } from './store';
import { roleLabel } from './navigation';
import ActionButton from './ActionButton.vue';

export interface ScopeValue {
  scope: string;
  memberId: string;
  teamId: string;
}
const props = defineProps<{ modelValue: ScopeValue }>();
const emit = defineEmits<{ 'update:modelValue': [ScopeValue] }>();
const app = useApp();
const role = computed(() => app.user?.role ?? '');
const scopes = computed<string[]>(() =>
  ({ baobei_sales: [], baobei_manager: ['team', 'mine', 'member'], baobei_director: ['all', 'team', 'member'], baobei_admin: ['all', 'team', 'member'] } as Record<string, string[]>)[role.value] ?? []
);
const labels: Record<string, string> = { mine: '我的项目', team: '团队项目', member: '按成员', all: '全部项目' };
interface Option { id: string; name: string; role?: string; status?: string }
const members = ref<Option[]>([]),
  teams = ref<Option[]>([]),
  failure = ref('');
async function loadMembers(teamId = '') {
  try {
    const b = await app.api<{ members?: { memberId: string; name: string; role: string; status: string; others?: boolean }[]; teams?: { teamId: string; name: string; status: string }[] }>(
      '/api/v1/analytics/members' + (teamId ? '?teamId=' + teamId : '')
    );
    if (b.teams) teams.value = b.teams.map((t) => ({ id: t.teamId, name: t.name, status: t.status }));
    if (b.members) members.value = b.members.filter((m) => !m.others).map((m) => ({ id: m.memberId, name: m.name, role: m.role, status: m.status }));
  } catch (e) {
    failure.value = (e as Error).message;
  }
}
const current = computed(() => props.modelValue.scope || scopes.value[0] || '');
function choose(scope: string) {
  emit('update:modelValue', { scope: scope === scopes.value[0] ? '' : scope, memberId: '', teamId: '' });
}
const isDirector = computed(() => role.value === 'baobei_director' || role.value === 'baobei_admin');
// Director picks a team first, then its members.
const memberTeam = ref('');
async function pickTeamForMembers(teamId: string) {
  memberTeam.value = teamId;
  members.value = [];
  if (teamId) await loadMembers(teamId);
}
onMounted(async () => {
  if (scopes.value.length) await loadMembers();
});
</script>
<template>
  <div v-if="scopes.length" class="scope-picker" role="group" aria-label="统计范围">
    <div class="segmented" role="tablist" aria-label="项目范围">
      <ActionButton
        v-for="s in scopes"
        :key="s"
        action-id="scope.select"
        role="tab"
        :aria-selected="current === s"
        :class="{ selected: current === s }"
        :run="() => choose(s)"
      >
        {{ labels[s] }}
      </ActionButton>
    </div>
    <select
      v-if="current === 'team' && isDirector"
      :value="modelValue.teamId"
      aria-label="选择团队"
      @change="emit('update:modelValue', { scope: 'team', memberId: '', teamId: ($event.target as HTMLSelectElement).value })"
    >
      <option value="">请选择团队</option>
      <option v-for="t in teams" :key="t.id" :value="t.id">{{ t.name }}{{ t.status === 'ARCHIVED' ? '（已归档）' : '' }}</option>
    </select>
    <template v-if="current === 'member'">
      <select v-if="isDirector" :value="memberTeam" aria-label="成员所在团队" @change="pickTeamForMembers(($event.target as HTMLSelectElement).value)">
        <option value="">先选择团队</option>
        <option v-for="t in teams" :key="t.id" :value="t.id">{{ t.name }}</option>
      </select>
      <select
        :value="modelValue.memberId"
        aria-label="选择成员"
        @change="emit('update:modelValue', { scope: 'member', memberId: ($event.target as HTMLSelectElement).value, teamId: '' })"
      >
        <option value="">请选择成员</option>
        <option v-for="m in members" :key="m.id" :value="m.id">{{ m.name }} · {{ roleLabel(m.role || '') }}{{ m.status && m.status !== 'ACTIVE' ? '（已停用）' : '' }}</option>
      </select>
    </template>
    <small v-if="failure" class="field-error" role="alert">{{ failure }}</small>
  </div>
</template>
