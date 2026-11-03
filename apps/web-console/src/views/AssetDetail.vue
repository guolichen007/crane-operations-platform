<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { useRoute, useRouter } from 'vue-router';
import { Pencil, Move, Archive, ArrowLeft } from 'lucide-vue-next';
import { getAsset, updateAsset, retireAsset, moveAsset, listEquipment, listLocations } from '../cloud/asset';
import { ApiError } from '../cloud/client';
import type { AssetMasterData, AssetLocation } from '../../../../contracts/typescript/types.gen';

const route = useRoute();
const router = useRouter();
const queryClient = useQueryClient();
const id = computed(() => route.params.id as string);

const assetQuery = useQuery({
  queryKey: ['asset', id],
  queryFn: () => getAsset(id.value),
});
const equipmentQuery = useQuery({
  queryKey: ['equipment', id],
  queryFn: () => listEquipment(id.value),
});
const locationsQuery = useQuery({
  queryKey: ['locations', assetQuery.data.value?.projectId],
  queryFn: () => listLocations(assetQuery.data.value?.projectId ?? ''),
  enabled: computed(() => !!assetQuery.data.value?.projectId),
});

const tab = ref<'overview' | 'equipment' | 'location'>('overview');
const editing = ref(false);
const moving = ref(false);
const retiring = ref(false);
const actionError = ref('');
const editDraft = ref<AssetMasterData | null>(null);
const moveLocation = ref('');

function openEdit() {
  editDraft.value = { ...(assetQuery.data.value as AssetMasterData) };
  editing.value = true;
}
const updateMutation = useMutation({
  mutationFn: (input: AssetMasterData) => updateAsset(id.value, input, assetQuery.data.value!.version),
  onSuccess: async () => {
    editing.value = false;
    await queryClient.invalidateQueries({ queryKey: ['asset', id] });
  },
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '更新失败'),
});
const retireMutation = useMutation({
  mutationFn: () => retireAsset(id.value, assetQuery.data.value!.version),
  onSuccess: async () => {
    retiring.value = false;
    await queryClient.invalidateQueries({ queryKey: ['asset', id] });
  },
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '退役失败'),
});
const moveMutation = useMutation({
  mutationFn: () => moveAsset(id.value, moveLocation.value, assetQuery.data.value!.version),
  onSuccess: async () => {
    moving.value = false;
    await queryClient.invalidateQueries({ queryKey: ['asset', id] });
  },
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '移动失败'),
});

const asset = computed(() => assetQuery.data.value);
const statusText: Record<string, string> = {
  DRAFT: '草稿',
  ACTIVE: '在用',
  OUT_OF_SERVICE: '停用',
  RETIRED: '退役',
};
</script>
<template>
  <section class="workspace-page asset-detail" data-testid="asset-detail">
    <header class="detail-head">
      <button class="ghost-action" aria-label="返回" @click="router.push('/assets')">
        <ArrowLeft :size="15" />返回
      </button>
      <div v-if="asset" class="detail-title">
        <span class="status-pill" :data-status="asset.assetStatus">{{
          statusText[asset.assetStatus] ?? asset.assetStatus
        }}</span>
        <h1>{{ asset.assetCode ?? asset.id }}</h1>
        <p>{{ asset.assetType }} · {{ asset.manufacturerSnapshot || '未关联制造商' }} · v{{ asset.version }}</p>
      </div>
      <div class="detail-actions">
        <button class="ghost-action" @click="openEdit"><Pencil :size="14" />编辑</button>
        <button class="ghost-action" @click="moving = true"><Move :size="14" />移动</button>
        <button class="danger-action" @click="retiring = true"><Archive :size="14" />退役</button>
      </div>
    </header>

    <p v-if="assetQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
    <p v-else-if="assetQuery.isError.value" class="workspace-error" role="alert">
      资产加载失败：{{ (assetQuery.error.value as Error).message }}
    </p>
    <template v-else-if="asset">
      <div class="tabs" role="tablist">
        <button :class="{ active: tab === 'overview' }" @click="tab = 'overview'">概览</button>
        <button :class="{ active: tab === 'equipment' }" @click="tab = 'equipment'">设备</button>
        <button :class="{ active: tab === 'location' }" @click="tab = 'location'">位置</button>
      </div>

      <div v-if="tab === 'overview'" class="detail-grid">
        <div class="detail-cell"><span>资产编号</span><b>{{ asset.assetCode ?? '—' }}</b></div>
        <div class="detail-cell"><span>类型</span><b>{{ asset.assetType }}</b></div>
        <div class="detail-cell"><span>制造商</span><b>{{ asset.manufacturerSnapshot || '—' }}</b></div>
        <div class="detail-cell"><span>品牌</span><b>{{ asset.brandSnapshot || '—' }}</b></div>
        <div class="detail-cell"><span>型号</span><b>{{ asset.modelSnapshot || '—' }}</b></div>
        <div class="detail-cell"><span>序列号</span><b>{{ asset.serialNumber ?? '—' }}</b></div>
        <div class="detail-cell"><span>特种设备代码</span><b>{{ asset.specialEquipmentCode ?? '—' }}</b></div>
        <div class="detail-cell"><span>使用登记证号</span><b>{{ asset.useRegistrationNo ?? '—' }}</b></div>
      </div>

      <div v-else-if="tab === 'equipment'">
        <p v-if="equipmentQuery.isLoading.value">加载设备…</p>
        <p v-else-if="!equipmentQuery.data.value?.length" class="workspace-empty">该资产暂无 Equipment。</p>
        <table v-else class="data-table">
          <thead>
            <tr><th>Equipment 编号</th><th>类型</th><th>关键度</th><th>行政状态</th></tr>
          </thead>
          <tbody>
            <tr v-for="e in equipmentQuery.data.value" :key="e.id" class="data-row">
              <td>{{ e.equipmentCode }}</td>
              <td>{{ e.typeCode }}</td>
              <td>{{ e.criticality }}</td>
              <td>{{ e.administrativeState }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="workspace-empty">
        当前位置与安装历史将在资产位置中心展示；移动操作用顶部「移动」。
      </div>
    </template>

    <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>

    <div v-if="editing && editDraft" class="modal-backdrop" @click.self="editing = false">
      <form class="workspace-modal" @submit.prevent="updateMutation.mutate(editDraft)">
        <h2>编辑资产（If-Match v{{ asset?.version }}）</h2>
        <label>资产编号<input v-model="editDraft.assetCode" /></label>
        <label>类型<input v-model="editDraft.assetType" /></label>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="editing = false">取消</button>
          <button type="submit" class="primary-action">保存</button>
        </div>
      </form>
    </div>

    <div v-if="moving" class="modal-backdrop" @click.self="moving = false">
      <form class="workspace-modal" @submit.prevent="moveMutation.mutate()">
        <h2>移动资产（迁移安装位置）</h2>
        <label>目标位置
          <select v-model="moveLocation" required>
            <option value="" disabled>选择位置</option>
            <option v-for="l in (locationsQuery.data.value as AssetLocation[])" :key="l.id" :value="l.id">
              {{ l.name }}（{{ l.kind }}）
            </option>
          </select>
        </label>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="moving = false">取消</button>
          <button type="submit" class="primary-action">移动</button>
        </div>
      </form>
    </div>

    <div v-if="retiring" class="modal-backdrop" @click.self="retiring = false">
      <div class="workspace-modal">
        <h2>确认退役</h2>
        <p>退役是终态，不可恢复。存在活动 Runtime Binding 时将拒绝。</p>
        <div class="modal-actions">
          <button class="ghost-action" @click="retiring = false">取消</button>
          <button class="danger-action" @click="retireMutation.mutate()">确认退役</button>
        </div>
      </div>
    </div>
  </section>
</template>
