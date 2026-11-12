<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import { Search, MapPin, Plus, RefreshCw } from 'lucide-vue-next';
import { listAllLocations, createLocation } from '../cloud/asset';
import { ApiError } from '../cloud/client';
import { webSession, productionAuth } from '../cloud/session';
import { canWriteProject } from '../cloud/auth-state';
import { allowClose } from '../cloud/draft';
import { isAuthorizedScope } from '../cloud/project-scope';
import { KIND_TEXT, LOCATION_KINDS, blankLocation, locationDirty, locationErrorText, toNewLocation, validateLocation, type LocationForm } from '../cloud/location-form';
import type { AssetLocation } from '../../../../contracts/typescript/types.gen';

const route = useRoute();
const router = useRouter();
const queryClient = useQueryClient();
const q = ref('');
const kind = ref('');
const status = ref('');
// The project scope is explicit: only `?project=` selects a scope. There is no
// silent fallback to the first authorized project, which would mislead the
// "全国运营视图" scope. Without a scope, the page offers the caller's projects.
const projectId = computed(() => (typeof route.query.project === 'string' ? route.query.project : ''));
const grants = computed(() => webSession.value?.projects ?? []);
const authorized = computed(() => !productionAuth || isAuthorizedScope(projectId.value, grants.value));
// The create entry follows CapLocationWrite (platform_admin on THIS project); the server decides.
const canCreate = computed(() => !!projectId.value && (!productionAuth || canWriteProject(webSession.value, projectId.value)));

const locationsQuery = useQuery({
  queryKey: ['locations', projectId],
  queryFn: () => listAllLocations(projectId.value, { sort: 'name' }),
  enabled: computed(() => !!projectId.value && authorized.value),
});

type LocationNode = AssetLocation & { children: LocationNode[] };
// Full hierarchy (SITE → PLANT → WORKSHOP → BAY → RUNWAY): build the nested tree
// from the flat parentId list, so every level is expressed instead of a flat
// two-level "lazy tree".
const fullTree = computed<LocationNode[]>(() => {
  const items = (locationsQuery.data.value ?? []) as LocationNode[];
  const byId = new Map<string, LocationNode>();
  for (const l of items) byId.set(l.id, { ...l, children: [] });
  const roots: LocationNode[] = [];
  for (const node of byId.values()) {
    if (node.parentId && byId.has(node.parentId)) byId.get(node.parentId)!.children.push(node);
    else roots.push(node);
  }
  return roots;
});
// Client-side filter with ancestor preservation: a node is kept when it matches
// (name/code + kind + status) or when any descendant matches, so a matching BAY
// never masquerades as a top-level SITE after filtering.
const tree = computed<LocationNode[]>(() => {
  const ql = q.value.trim().toLowerCase();
  const match = (n: AssetLocation) =>
    (kind.value === '' || n.kind === kind.value) &&
    (status.value === '' || n.status === status.value) &&
    (ql === '' || n.name.toLowerCase().includes(ql) || (n.code ?? '').toLowerCase().includes(ql));
  const prune = (nodes: LocationNode[]): LocationNode[] => {
    const out: LocationNode[] = [];
    for (const n of nodes) {
      const children = prune(n.children);
      if (match(n) || children.length > 0) out.push({ ...n, children });
    }
    return out;
  };
  return prune(fullTree.value);
});
const flattened = computed(() => {
  const out: { node: LocationNode; depth: number }[] = [];
  const walk = (nodes: LocationNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ node: n, depth });
      walk(n.children, depth + 1);
    }
  };
  walk(tree.value, 0);
  return out;
});
const filtering = computed(() => !!(q.value.trim() || kind.value || status.value));
const activeParents = computed(() => ((locationsQuery.data.value ?? []) as AssetLocation[]).filter((l) => l.status === 'ACTIVE'));

// ---- Minimal create (Q4-QA-R1 §4): kind, name, optional code / parent / address. No coordinates are
// entered here: the server stores coordinateSystem UNKNOWN, never an invented longitude/latitude.
const creating = ref(false);
const form = ref<LocationForm>(blankLocation());
const touched = ref(false);
const formError = ref('');
const errors = computed(() => validateLocation(form.value));
const shown = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => creating.value && locationDirty(form.value));
const created = ref('');
// Q4-QA-R1a: the target project is pinned when the form opens; a later ?project= change (top bar)
// never redirects this draft to another project. The server stays the authority (URL path project,
// location_write, parent in the same project).
const formProject = ref('');
const formParents = computed(() => (formProject.value === projectId.value ? activeParents.value : []));

function openCreate() {
  if (!allowClose(dirty.value, mutation.isPending.value)) return;
  form.value = blankLocation();
  touched.value = false;
  formError.value = '';
  created.value = '';
  formProject.value = projectId.value;
  creating.value = true;
}
function closeCreate() {
  if (!allowClose(dirty.value, mutation.isPending.value)) return;
  creating.value = false;
}
const mutation = useMutation({
  mutationFn: () => createLocation(toNewLocation(formProject.value, form.value)),
  onSuccess: async (loc) => {
    creating.value = false;
    created.value = `已创建位置「${loc.name}」（${KIND_TEXT[loc.kind] ?? loc.kind}，坐标未知）。可在资产详情中把资产移动到这里。`;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['locations'] }),
      queryClient.invalidateQueries({ queryKey: ['asset-move-locations'] }),
    ]);
  },
  // The dialog stays open with every input kept; the error is shown inside it.
  onError: (e) => (formError.value = locationErrorText(e)),
});
function submit() {
  touched.value = true;
  formError.value = '';
  if (mutation.isPending.value || Object.keys(errors.value).length) return;
  mutation.mutate();
}
function chooseProject(e: Event) {
  const value = (e.target as HTMLSelectElement).value;
  void router.replace({ path: '/locations', query: value ? { project: value } : {} });
}
const loadErrorText = computed(() => {
  const e = locationsQuery.error.value;
  if (e instanceof ApiError && e.status === 403) return '没有该项目的位置查看权限。';
  if (e instanceof ApiError && e.status === 404) return '项目不存在或已不在您的授权范围内。';
  if (e instanceof ApiError && e.status === 503) return '服务暂不可用，位置未能加载。';
  return `位置加载失败：${(e as Error | null)?.message ?? ''}`;
});
function onKeydown(e: KeyboardEvent) { if (e.key === 'Escape' && creating.value) closeCreate(); }
function onBeforeUnload(e: BeforeUnloadEvent) { if (dirty.value || mutation.isPending.value) { e.preventDefault(); e.returnValue = ''; } }
onMounted(() => { window.addEventListener('keydown', onKeydown); window.addEventListener('beforeunload', onBeforeUnload); });
onBeforeUnmount(() => { window.removeEventListener('keydown', onKeydown); window.removeEventListener('beforeunload', onBeforeUnload); });
onBeforeRouteLeave(() => allowClose(dirty.value, mutation.isPending.value));
// Same page, another project: refuse while submitting, ask before discarding a draft, then close it.
onBeforeRouteUpdate((to, from) => {
  if (to.query.project === from.query.project) return true;
  if (!allowClose(dirty.value, mutation.isPending.value)) return false;
  creating.value = false;
  formError.value = '';
  created.value = '';
  return true;
});
</script>
<template>
  <section class="workspace-page location-center" data-testid="location-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">LOCATION CENTER / 位置中心</span>
        <h1>位置层级</h1>
        <p>SITE → PLANT → WORKSHOP → BAY → RUNWAY · 完整层级</p>
      </div>
      <button v-if="canCreate && authorized" class="primary-action" data-testid="location-create" @click="openCreate">
        <Plus :size="14" />新建位置
      </button>
      <MapPin v-else :size="20" />
    </header>

    <p v-if="created" class="registered-notice" role="status" data-testid="location-created">{{ created }}</p>

    <div v-if="!projectId" class="workspace-empty" data-testid="location-no-scope">
      <p>位置按项目管理。请选择项目：</p>
      <select aria-label="选择项目" data-testid="location-project-select" @change="chooseProject">
        <option value="">选择项目</option>
        <option v-for="g in grants" :key="g.projectId" :value="g.projectId">{{ g.projectName }}</option>
      </select>
      <p v-if="productionAuth && !grants.length" class="muted small">当前账号没有任何项目授权。</p>
    </div>
    <p v-else-if="!authorized" class="workspace-empty" role="status" data-testid="location-unauthorized">
      项目 {{ projectId }} 不在您的授权范围内。
    </p>
    <template v-else>
      <div class="asset-filters">
        <div class="search-field">
          <Search :size="15" />
          <input v-model="q" placeholder="搜索名称 / 编号" />
        </div>
        <select v-model="kind" aria-label="类型筛选">
          <option value="">全部类型</option>
          <option v-for="k in LOCATION_KINDS" :key="k" :value="k">{{ KIND_TEXT[k] ?? k }}</option>
        </select>
        <select v-model="status" aria-label="状态筛选">
          <option value="">全部状态</option>
          <option value="ACTIVE">在用</option>
          <option value="DISABLED">停用</option>
        </select>
      </div>

      <p v-if="locationsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true" data-testid="location-loading"></p>
      <p v-else-if="locationsQuery.isError.value" class="workspace-error" role="alert" data-testid="location-error">
        {{ loadErrorText }}
        <button type="button" class="ghost-action" @click="locationsQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </p>
      <div v-else-if="!flattened.length" class="workspace-empty" data-testid="location-empty">
        <template v-if="filtering">没有符合筛选条件的位置。</template>
        <template v-else-if="canCreate">该项目还没有位置。点击右上角「新建位置」创建第一个位置（如场地 / 车间），之后即可把资产移动到这里。</template>
        <template v-else>该项目还没有位置，需由项目管理员创建。</template>
      </div>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="location-table">
          <thead><tr><th>名称</th><th>编号</th><th>类型</th><th>状态</th><th>坐标</th><th>地址</th></tr></thead>
          <tbody>
            <tr v-for="row in flattened" :key="row.node.id" class="data-row" :class="row.depth ? 'tree-child' : 'tree-root'" :data-location-id="row.node.id">
              <td :style="{ paddingLeft: 12 + row.depth * 16 + 'px' }">{{ row.depth ? '└ ' : '' }}{{ row.node.name }}</td>
              <td>{{ row.node.code ?? '—' }}</td>
              <td>{{ KIND_TEXT[row.node.kind] ?? row.node.kind }}</td>
              <td>{{ row.node.status === 'ACTIVE' ? '在用' : '停用' }}</td>
              <td>{{ row.node.coordinateSystem === 'UNKNOWN' ? '未知' : row.node.coordinateSystem }}</td>
              <td>{{ row.node.address || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <div v-if="creating" class="modal-backdrop" @click.self="closeCreate">
      <form class="workspace-modal" data-testid="location-create-form" novalidate @submit.prevent="submit">
        <h2>新建位置 · {{ formProject }}</h2>
        <label>类型 *
          <select v-model="form.kind" aria-label="位置类型" :aria-invalid="!!shown.kind">
            <option value="" disabled>选择类型</option>
            <option v-for="k in LOCATION_KINDS" :key="k" :value="k">{{ KIND_TEXT[k] }}（{{ k }}）</option>
          </select>
          <span v-if="shown.kind" class="field-error">{{ shown.kind }}</span>
        </label>
        <label>名称 *<input v-model="form.name" aria-label="位置名称" maxlength="256" :aria-invalid="!!shown.name" />
          <span v-if="shown.name" class="field-error">{{ shown.name }}</span>
        </label>
        <label>编号（项目内唯一，可留空）<input v-model="form.code" aria-label="位置编号" maxlength="160" /></label>
        <label>上级位置
          <select v-model="form.parentId" aria-label="上级位置">
            <option value="">（无，顶层位置）</option>
            <option v-for="l in formParents" :key="l.id" :value="l.id">{{ l.name }}（{{ KIND_TEXT[l.kind] ?? l.kind }}）</option>
          </select>
        </label>
        <label>地址<input v-model="form.address" aria-label="地址" maxlength="240" /></label>
        <p class="hint">不录入经纬度：坐标保持「未知」，不会生成或推算坐标。</p>
        <p v-if="formError" class="form-error" role="alert" data-testid="location-create-error">{{ formError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="mutation.isPending.value" @click="closeCreate">取消</button>
          <button type="submit" class="primary-action" :disabled="mutation.isPending.value" data-testid="location-create-submit">
            {{ mutation.isPending.value ? '创建中…' : '创建' }}
          </button>
        </div>
      </form>
    </div>
  </section>
</template>
<style scoped>
.workspace-page-head { display: flex; align-items: flex-start; justify-content: space-between; }
.hint { margin: 0; font-size: 12px; color: var(--text-3); }
.field-error { color: #ff665f; font-size: 11px; }
.registered-notice { color: #6fd39b; font-size: 13px; margin: 0; }
</style>
