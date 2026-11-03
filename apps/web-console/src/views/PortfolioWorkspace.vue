<script setup lang="ts">
import { ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { FolderKanban, Check } from 'lucide-vue-next';
import {
  listIntakes, transitionIntake, approveIntake,
  listResources, listScheduleItems, listConflicts, overrideConflict,
} from '../cloud/portfolio';
import { ApiError } from '../cloud/client';

const queryClient = useQueryClient();
const tab = ref<'intake' | 'resource' | 'schedule' | 'conflict'>('intake');
const actionError = ref('');

const intakesQuery = useQuery({ queryKey: ['intakes'], queryFn: listIntakes });
const resourcesQuery = useQuery({ queryKey: ['resources'], queryFn: listResources });
const scheduleQuery = useQuery({ queryKey: ['schedule-items'], queryFn: listScheduleItems });
const conflictsQuery = useQuery({ queryKey: ['conflicts'], queryFn: listConflicts });

const intakeStatusText: Record<string, string> = {
  REGISTERED: '已登记', REVIEW: '评审中', PLANNING: '规划中', APPROVED: '已批准',
  REJECTED: '已驳回', CANCELLED: '已取消',
};
const nextAction: Record<string, string | null> = {
  REGISTERED: 'REVIEW', REVIEW: 'PLANNING', PLANNING: 'APPROVED',
  APPROVED: null, REJECTED: null, CANCELLED: null,
};

const transitionMutation = useMutation({
  mutationFn: (v: { id: string; status: string }) => transitionIntake(v.id, v.status, '运营推进'),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['intakes'] }),
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '流转失败'),
});
const approveMutation = useMutation({
  mutationFn: (id: string) => approveIntake(id),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['intakes'] }),
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '批准失败'),
});
const overrideMutation = useMutation({
  mutationFn: (v: { id: number; reason: string }) => overrideConflict(v.id, v.reason),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['conflicts'] }),
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '覆盖失败'),
});

function advance(id: string, status: string) {
  if (status === 'APPROVED') approveMutation.mutate(id);
  else transitionMutation.mutate({ id, status });
}
function override(id: number) {
  const reason = window.prompt('覆盖原因（至少 8 字）');
  if (reason) overrideMutation.mutate({ id, reason });
}
</script>
<template>
  <section class="workspace-page portfolio-workspace" data-testid="portfolio-workspace">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">PORTFOLIO / 项目组合</span>
        <h1>项目组合与资源排期</h1>
        <p>项目登记 · 资源 · 排期 · 冲突 · 状态机由后端控制</p>
      </div>
      <FolderKanban :size="20" />
    </header>

    <div class="tabs" role="tablist">
      <button :class="{ active: tab === 'intake' }" @click="tab = 'intake'">项目登记</button>
      <button :class="{ active: tab === 'resource' }" @click="tab = 'resource'">资源</button>
      <button :class="{ active: tab === 'schedule' }" @click="tab = 'schedule'">排期</button>
      <button :class="{ active: tab === 'conflict' }" @click="tab = 'conflict'">冲突</button>
    </div>

    <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>

    <template v-if="tab === 'intake'">
      <p v-if="intakesQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="!intakesQuery.data.value?.length" class="workspace-empty">暂无项目登记。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="intake-table">
          <thead><tr><th>编号</th><th>项目名</th><th>客户</th><th>状态</th><th>类型</th><th>优先级</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="i in intakesQuery.data.value" :key="i.id" class="data-row">
              <td>{{ i.intakeCode }}</td>
              <td>{{ i.projectName }}</td>
              <td>{{ i.customerName ?? '—' }}</td>
              <td>
                <span class="status-pill" :data-status="i.status === 'APPROVED' ? 'ACTIVE' : i.status === 'REJECTED' || i.status === 'CANCELLED' ? 'RETIRED' : 'DRAFT'">
                  {{ intakeStatusText[i.status] ?? i.status }}
                </span>
              </td>
              <td>{{ i.projectType }}</td>
              <td>{{ i.priority }}</td>
              <td>
                <button
                  v-if="nextAction[i.status]"
                  class="ghost-action"
                  @click="advance(i.id, nextAction[i.status]!)"
                >
                  <Check :size="13" />{{ nextAction[i.status] === 'APPROVED' ? '批准' : '推进' }}
                </button>
                <span v-else class="muted">终态</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else-if="tab === 'resource'">
      <p v-if="resourcesQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="!resourcesQuery.data.value?.length" class="workspace-empty">暂无资源。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table">
          <thead><tr><th>名称</th><th>类型</th><th>容量</th><th>独占</th><th>时区</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="r in resourcesQuery.data.value" :key="r.id" class="data-row">
              <td>{{ r.name }}</td>
              <td>{{ r.resourceType }}</td>
              <td>{{ r.capacity }}</td>
              <td>{{ r.exclusive ? '是' : '否' }}</td>
              <td>{{ r.homeTimezone }}</td>
              <td>{{ r.status }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else-if="tab === 'schedule'">
      <p v-if="scheduleQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="!scheduleQuery.data.value?.length" class="workspace-empty">暂无排期项。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table">
          <thead><tr><th>名称</th><th>类型</th><th>开始</th><th>结束</th><th>时区</th><th>状态</th><th>优先级</th></tr></thead>
          <tbody>
            <tr v-for="s in scheduleQuery.data.value" :key="s.id" class="data-row">
              <td>{{ s.name }}</td>
              <td>{{ s.sourceType }}</td>
              <td>{{ s.startAt }}</td>
              <td>{{ s.endAt }}</td>
              <td>{{ s.timezone }}</td>
              <td>{{ s.status }}</td>
              <td>{{ s.priority }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else>
      <p v-if="conflictsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="!conflictsQuery.data.value?.length" class="workspace-empty">无未解决冲突。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="conflict-table">
          <thead><tr><th>类型</th><th>严重度</th><th>左项</th><th>资源</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="c in conflictsQuery.data.value" :key="c.id" class="data-row">
              <td>{{ c.conflictType }}</td>
              <td>
                <span class="status-pill" :data-status="c.severity === 'BLOCKING' ? 'OUT_OF_SERVICE' : 'DRAFT'">{{ c.severity }}</span>
              </td>
              <td>{{ c.leftItemId }}</td>
              <td>{{ c.resourceId ?? '—' }}</td>
              <td>{{ c.status }}</td>
              <td>
                <button class="ghost-action" @click="override(c.id)">覆盖</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </section>
</template>
