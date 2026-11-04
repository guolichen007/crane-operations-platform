<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { Search, Plus, RefreshCw, ChevronLeft, ChevronRight } from 'lucide-vue-next';
import { listAssets, createAsset } from '../cloud/asset';
import { ApiError } from '../cloud/client';
import { webSession } from '../cloud/session';
import { isViewerOnly } from '../cloud/auth-state';
import type { AssetMasterData } from '../../../../contracts/typescript/types.gen';

const router = useRouter();
const queryClient = useQueryClient();
const canCreate = computed(() => !isViewerOnly(webSession.value));

const q = ref('');
const status = ref('');
const type = ref('');
const sort = ref('');
const page = ref(0);
const limit = 50;
const applied = ref({ q: '', status: '', type: '', sort: '' });
const creating = ref(false);
const formError = ref('');
const newAsset = ref<Partial<AssetMasterData>>({ projectId: '', assetType: 'CRANE' });
let idempotencyKey = crypto.randomUUID();

function apply() {
  applied.value = { q: q.value, status: status.value, type: type.value, sort: sort.value };
  page.value = 0;
}

const params = computed(() => ({
  limit,
  offset: page.value * limit,
  sort: applied.value.sort || undefined,
  q: applied.value.q || undefined,
  status: applied.value.status || undefined,
  type: applied.value.type || undefined,
}));

const assetsQuery = useQuery({
  queryKey: ['assets', params],
  queryFn: () => listAssets(params.value),
});

const createMutation = useMutation({
  mutationFn: (input: AssetMasterData) => createAsset(input, idempotencyKey),
  onSuccess: async (created) => {
    creating.value = false;
    idempotencyKey = crypto.randomUUID();
    await queryClient.invalidateQueries({ queryKey: ['assets'] });
    await router.push(`/assets/${created.id}`);
  },
  onError: (e) => {
    formError.value = e instanceof ApiError ? e.message : '创建失败';
  },
});

function openCreate() {
  idempotencyKey = crypto.randomUUID();
  formError.value = '';
  newAsset.value = { projectId: '', assetType: 'CRANE' };
  creating.value = true;
}

const statusOptions = ['DRAFT', 'ACTIVE', 'OUT_OF_SERVICE', 'RETIRED'];
const typeOptions = ['CRANE', 'GANTRY', 'BRIDGE', 'PORTAL', 'JIB'];

const statusText: Record<string, string> = {
  DRAFT: '草稿',
  ACTIVE: '在用',
  OUT_OF_SERVICE: '停用',
  RETIRED: '退役',
};
</script>
<template>
  <section class="workspace-page asset-center" data-testid="asset-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">ASSET CENTER / 资产中心</span>
        <h1>资产台账</h1>
        <p>服务端分页、搜索、状态与类型筛选 · If-Match 乐观并发 · 幂等创建</p>
      </div>
      <button v-if="canCreate" class="primary-action" data-testid="asset-create" @click="openCreate">
        <Plus :size="15" />新建资产
      </button>
    </header>

    <div class="asset-filters">
      <div class="search-field">
        <Search :size="15" />
        <input v-model="q" placeholder="搜索编号 / 名称" @keyup.enter="apply" />
      </div>
      <select v-model="status" aria-label="状态筛选">
        <option value="">全部状态</option>
        <option v-for="s in statusOptions" :key="s" :value="s">{{ statusText[s] ?? s }}</option>
      </select>
      <select v-model="type" aria-label="类型筛选">
        <option value="">全部类型</option>
        <option v-for="t in typeOptions" :key="t" :value="t">{{ t }}</option>
      </select>
      <button class="ghost-action" @click="apply">筛选</button>
      <button class="ghost-action" :aria-label="'刷新'" @click="queryClient.invalidateQueries({ queryKey: ['assets'] })">
        <RefreshCw :size="14" />
      </button>
    </div>

    <div v-if="assetsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></div>
    <p v-else-if="assetsQuery.isError.value" class="workspace-error" role="alert">
      资产加载失败：{{ (assetsQuery.error.value as Error).message }}
    </p>
    <p v-else-if="!assetsQuery.data.value?.length" class="workspace-empty">
      暂无资产，点击「新建资产」开始录入。
    </p>
    <div v-else class="data-table-wrap">
      <table class="data-table" data-testid="asset-table">
        <thead>
          <tr>
            <th>资产编号</th>
            <th>名称</th>
            <th>类型</th>
            <th>状态</th>
            <th>制造商</th>
            <th>型号</th>
            <th>版本</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in assetsQuery.data.value" :key="a.id" class="data-row" @click="router.push(`/assets/${a.id}`)">
            <td>{{ a.assetCode ?? '—' }}</td>
            <td>{{ a.id }}</td>
            <td>{{ a.assetType }}</td>
            <td>
              <span class="status-pill" :data-status="a.assetStatus">{{ statusText[a.assetStatus] ?? a.assetStatus }}</span>
            </td>
            <td>{{ a.manufacturerSnapshot || '—' }}</td>
            <td>{{ a.modelSnapshot || '—' }}</td>
            <td>{{ a.version }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="pager" v-if="assetsQuery.data.value?.length">
      <button :disabled="page === 0" aria-label="上一页" @click="page--">
        <ChevronLeft :size="15" />
      </button>
      <span>第 {{ page + 1 }} 页</span>
      <button
        :disabled="(assetsQuery.data.value?.length ?? 0) < limit"
        aria-label="下一页"
        @click="page++"
      >
        <ChevronRight :size="15" />
      </button>
    </div>

    <div v-if="creating" class="modal-backdrop" @click.self="creating = false">
      <form
        class="workspace-modal"
        data-testid="asset-create-form"
        @submit.prevent="createMutation.mutate(newAsset as AssetMasterData)"
      >
        <h2>新建资产</h2>
        <label>项目 ID<input v-model="newAsset.projectId" required placeholder="Project-A" /></label>
        <label>资产编号<input v-model="newAsset.assetCode" placeholder="CRN-001" /></label>
        <label>类型
          <select v-model="newAsset.assetType">
            <option v-for="t in typeOptions" :key="t" :value="t">{{ t }}</option>
          </select>
        </label>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="creating = false">取消</button>
          <button type="submit" class="primary-action" :disabled="createMutation.isPending.value">
            {{ createMutation.isPending.value ? '创建中…' : '创建' }}
          </button>
        </div>
      </form>
    </div>
  </section>
</template>
