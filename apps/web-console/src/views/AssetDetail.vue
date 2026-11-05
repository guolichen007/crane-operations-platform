<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import { Pencil, Move, Archive, ArrowLeft, Plus } from 'lucide-vue-next';
import {
  getAsset,
  updateAsset,
  assetChanges,
  retireAsset,
  moveAsset,
  listEquipment,
  createEquipment,
  updateEquipment,
  removeEquipment,
  listAllLocations,
} from '../cloud/asset';
import { ApiError } from '../cloud/client';
import { webSession } from '../cloud/session';
import { canWriteProject } from '../cloud/auth-state';
import { useDraftSession, allowDiscard, fieldDiff } from '../cloud/draft';
import type { AssetMasterData, AssetLocation, EquipmentInstallation } from '../../../../contracts/typescript/types.gen';

const route = useRoute();
const router = useRouter();
const queryClient = useQueryClient();
const id = computed(() => route.params.id as string);
// Authorization and business lifecycle are combined: a RETIRED asset is terminal,
// so even a platform_admin must not be offered edit / move / retire / install
// entry points (the backend refuses them anyway).
const canWrite = computed(
  () => canWriteProject(webSession.value, asset.value?.projectId ?? '') && asset.value?.assetStatus !== 'RETIRED',
);

const assetQuery = useQuery({
  queryKey: ['asset', id],
  queryFn: () => getAsset(id.value),
});
const equipmentQuery = useQuery({
  queryKey: ['equipment', id],
  queryFn: () => listEquipment(id.value),
});
// Move-target candidates use a distinct cache entry from the LocationCenter tree
// (which also keys on ['locations', projectId]) and fetch every ACTIVE
// same-project location, so the picker never omits a legal target past page one.
// The project id is a computed so the query key reacts to project/asset changes:
// reading assetQuery.data.value?.projectId once at setup would pin the key and
// leak one project's locations into another project's asset detail.
const assetProjectId = computed(() => assetQuery.data.value?.projectId);
const locationsQuery = useQuery({
  queryKey: computed(() => ['asset-move-locations', assetProjectId.value]),
  queryFn: () => listAllLocations(assetProjectId.value ?? '', { status: 'ACTIVE' }),
  enabled: computed(() => !!assetProjectId.value),
});

const tab = ref<'overview' | 'equipment' | 'location'>('overview');
const actionError = ref('');

// Each write action pins the version (and a snapshot) at the moment its dialog
// opens. Submits always send that pinned version as If-Match — never the latest
// cached version, which a background refetch may have advanced under an old draft.
const edit = useDraftSession<AssetMasterData>();
const editError = ref('');
const editConflict = ref(false);
const move = useDraftSession<{ locationId: string }>();
const moveError = ref('');
const retire = useDraftSession<{ confirm: true }>();
const retireError = ref('');

const editLabels: Partial<Record<keyof AssetMasterData, string>> = {
  displayName: '资产名称', assetCode: '资产编号', assetType: '类型', assetStatus: '状态',
  manufacturerSnapshot: '制造商', brandSnapshot: '品牌', modelSnapshot: '型号', serialNumber: '序列号',
};
const conflictDiff = computed(() =>
  edit.draft.value && edit.latest.value ? fieldDiff(edit.draft.value, edit.latest.value, editLabels) : [],
);

function errorText(e: unknown, fallback: string): string {
  if (!(e instanceof ApiError)) return fallback;
  if (e.status === 403) return '没有执行此操作的权限。';
  if (e.status === 404) return '资产不存在或已不在您的授权范围内。';
  if (e.status === 422) return `输入不符合要求：${e.message}`;
  if (e.status === 503) return '服务暂不可用，请稍后重试（草稿已保留）。';
  return e.message || fallback;
}

function openEdit() {
  const current = assetQuery.data.value;
  if (!current) return;
  edit.start(current, current.version);
  editError.value = '';
  editConflict.value = false;
}
async function refreshForConflict() {
  // Fetch the latest server copy for side-by-side comparison; the draft is kept.
  const fresh = await queryClient.fetchQuery({ queryKey: ['asset', id], queryFn: () => getAsset(id.value), staleTime: 0 });
  edit.latest.value = fresh;
}
function rebaseOnLatest() {
  if (edit.latest.value) edit.rebase(edit.latest.value, edit.latest.value.version);
  editConflict.value = false;
  editError.value = '';
}
async function refreshAfterWrite() {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: ['asset', id] }),
    queryClient.invalidateQueries({ queryKey: ['assets'] }),
  ]);
}

const updateMutation = useMutation({
  // PATCH carries only the fields the user changed, against the pinned version.
  mutationFn: () => updateAsset(id.value, assetChanges(edit.base.value!, edit.draft.value!), edit.baseVersion.value!),
  onSuccess: async () => {
    edit.close();
    await refreshAfterWrite();
  },
  onError: async (e) => {
    // Never close the dialog or drop the draft on failure.
    if (e instanceof ApiError && e.status === 409) {
      editConflict.value = true;
      editError.value = '数据已被其他用户修改。您的草稿已保留，可刷新查看最新内容后决定。';
      await refreshForConflict().catch(() => undefined);
    } else {
      editError.value = errorText(e, '更新失败');
    }
  },
});

function openMove() {
  const current = assetQuery.data.value;
  if (!current) return;
  move.start({ locationId: '' }, current.version);
  moveError.value = '';
}
const moveMutation = useMutation({
  mutationFn: () => moveAsset(id.value, move.draft.value!.locationId, move.baseVersion.value!),
  onSuccess: async () => {
    move.close();
    await refreshAfterWrite();
  },
  onError: (e) => {
    moveError.value = e instanceof ApiError && e.status === 409
      ? '资产已被其他用户修改，请关闭后重新打开再移动。'
      : errorText(e, '移动失败');
  },
});

function openRetire() {
  const current = assetQuery.data.value;
  if (!current) return;
  retire.start({ confirm: true }, current.version);
  retireError.value = '';
}
const retireMutation = useMutation({
  mutationFn: () => retireAsset(id.value, retire.baseVersion.value!),
  onSuccess: async () => {
    retire.close();
    await refreshAfterWrite();
  },
  onError: (e) => {
    retireError.value = e instanceof ApiError && e.status === 409
      ? '资产已被其他用户修改或存在活动绑定，未退役。请关闭后核对再操作。'
      : errorText(e, '退役失败');
  },
});

// ---- DirtyGuard: cancel button, backdrop, Esc, route change and page unload all
// go through the same check; clean drafts close without a prompt. ----
const anyDirty = computed(() => edit.dirty.value || move.dirty.value || equipmentEditDirty.value);
function closeEdit() {
  if (allowDiscard(edit.dirty.value)) edit.close();
}
function closeMove() {
  if (allowDiscard(move.dirty.value)) move.close();
}
function closeRetire() {
  retire.close();
}
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape') return;
  if (edit.open.value) closeEdit();
  else if (move.open.value) closeMove();
  else if (retire.open.value) closeRetire();
  else if (equipmentEditing.value) closeEquipmentEdit();
}
function onBeforeUnload(event: BeforeUnloadEvent) {
  if (anyDirty.value) {
    event.preventDefault();
    event.returnValue = '';
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKeydown);
  window.addEventListener('beforeunload', onBeforeUnload);
});
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown);
  window.removeEventListener('beforeunload', onBeforeUnload);
});
onBeforeRouteLeave(() => allowDiscard(anyDirty.value));

// ---- Equipment install / remove (asset-scoped, never a second master data
// set). Removal closes the validity interval; an active runtime binding is
// refused by the backend. ----
// Registered equipment type codes, mirroring internal/equipment specFileFor. The
// install form never offers an unregistered code (e.g. CRANE_BRIDGE).
const equipmentTypeCodes = [
  'MOTOR', 'PLC', 'SAFETY_PLC', 'VFD', 'SERVO_DRIVE',
  'LIDAR_3D', 'LIDAR_2D', 'CAMERA_RGB', 'CAMERA_DEPTH', 'CAMERA_STEREO', 'CAMERA_THERMAL',
  'INDUSTRIAL_PC', 'EDGE_GATEWAY', 'INDUSTRIAL_SWITCH', 'POE_SWITCH',
  'LOAD_CELL', 'IMU', 'REDUCER', 'BRAKE', 'DRUM', 'HOOK', 'GRAB', 'WHEEL', 'COUPLING',
  'PLC_IO', 'RELAY', 'CONTACTOR', 'REMOTE_CONTROLLER', 'ENCODER', 'ROPE_ENCODER',
  'LIMIT_SWITCH', 'PROXIMITY_SENSOR', 'ANTI_COLLISION_SENSOR', 'WIND_SENSOR',
  'TEMPERATURE_SENSOR', 'VIBRATION_SENSOR', 'GPU', 'ROUTER', 'MEDIA_CONVERTER',
  'POWER_SUPPLY', 'UPS', 'TRANSFORMER', 'CONDUCTOR_RAIL',
];
const equipmentAdding = ref(false);
const equipmentDraft = ref<EquipmentInstallation>({
  id: '',
  assetId: '',
  equipmentCode: '',
  typeCode: '',
  catalogItemId: null,
  serialNumber: null,
  semanticSlot: null,
  positionLabel: null,
  criticality: 'MEDIUM',
  administrativeState: 'IN_SERVICE',
  version: 0,
  removedAt: null,
});
// Optional spec fields are left empty rather than inventing device parameters.
const equipmentSpec = ref<Record<string, unknown>>({});
// The idempotency key stays stable across retries of one submit; a fresh key is
// only generated when (re)opening the form or after a successful install.
let equipmentIdemKey = '';
function openEquipmentAdd() {
  equipmentDraft.value = {
    id: '',
    assetId: '',
    equipmentCode: '',
    typeCode: '',
    catalogItemId: null,
    serialNumber: null,
    semanticSlot: null,
    positionLabel: null,
    criticality: 'MEDIUM',
    administrativeState: 'IN_SERVICE',
    version: 0,
    removedAt: null,
  };
  equipmentSpec.value = {};
  equipmentIdemKey = crypto.randomUUID();
  equipmentAdding.value = true;
}
const createEquipmentMutation = useMutation({
  mutationFn: () =>
    createEquipment(
      { ...equipmentDraft.value, assetId: id.value, spec: equipmentSpec.value },
      equipmentIdemKey
    ),
  onSuccess: async () => {
    equipmentAdding.value = false;
    equipmentIdemKey = '';
    await queryClient.invalidateQueries({ queryKey: ['equipment', id] });
  },
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '安装失败'),
});
const removingId = ref('');
const removeConfirm = ref(false);
const removeEquipmentMutation = useMutation({
  mutationFn: (equipmentId: string) =>
    removeEquipment(equipmentId, equipmentQuery.data.value!.find((x) => x.id === equipmentId)!.version),
  onSuccess: async () => {
    removeConfirm.value = false;
    removingId.value = '';
    await queryClient.invalidateQueries({ queryKey: ['equipment', id] });
  },
  onError: (e) => (actionError.value = e instanceof ApiError ? e.message : '拆卸失败'),
});

// ---- Equipment update (If-Match) — only editable business fields; a removed
// equipment cannot be edited; TELEMETRY_OWNED data is never sent. ----
const equipmentEditing = ref<EquipmentInstallation | null>(null);
const equipmentEditBase = ref<EquipmentInstallation | null>(null);
const equipmentEditError = ref('');
const equipmentEditDirty = computed(
  () => !!equipmentEditing.value && JSON.stringify(equipmentEditing.value) !== JSON.stringify(equipmentEditBase.value),
);
function openEquipmentEdit(e: EquipmentInstallation) {
  equipmentEditBase.value = { ...e };
  equipmentEditing.value = { ...e };
  equipmentEditError.value = '';
}
function closeEquipmentEdit() {
  if (!allowDiscard(equipmentEditDirty.value)) return;
  equipmentEditing.value = null;
  equipmentEditBase.value = null;
}
const updateEquipmentMutation = useMutation({
  // The version is the one pinned when the editor opened.
  mutationFn: (draft: EquipmentInstallation) => updateEquipment(draft.id, draft, equipmentEditBase.value!.version),
  onSuccess: async () => {
    equipmentEditing.value = null;
    equipmentEditBase.value = null;
    await queryClient.invalidateQueries({ queryKey: ['equipment', id] });
  },
  onError: (e) => {
    if (e instanceof ApiError && e.status === 409) {
      equipmentEditError.value = '版本冲突：该 Equipment 已被他人修改。您的草稿已保留，请取消后重新打开以获取最新版本。';
    } else {
      equipmentEditError.value = errorText(e, '更新失败');
    }
  },
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
        <h1>{{ asset.displayName || asset.assetCode || asset.id }}</h1>
        <p>{{ asset.assetType }} · {{ asset.manufacturerSnapshot || '未关联制造商' }} · v{{ asset.version }}</p>
      </div>
      <div v-if="canWrite" class="detail-actions">
        <button class="ghost-action" @click="openEdit"><Pencil :size="14" />编辑</button>
        <button class="ghost-action" @click="openMove"><Move :size="14" />移动</button>
        <button class="danger-action" @click="openRetire"><Archive :size="14" />退役</button>
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
        <div class="detail-cell"><span>资产名称</span><b>{{ asset.displayName || '—' }}</b></div>
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
        <div class="equipment-toolbar">
          <button v-if="canWrite" class="primary-action" @click="openEquipmentAdd">
            <Plus :size="14" />安装 Equipment
          </button>
        </div>
        <p v-if="equipmentQuery.isLoading.value">加载设备…</p>
        <p v-else-if="!equipmentQuery.data.value?.length" class="workspace-empty">该资产暂无 Equipment。</p>
        <table v-else class="data-table">
          <thead>
            <tr>
              <th>Equipment 编号</th><th>类型</th><th>关键度</th><th>行政状态</th><th>版本</th><th v-if="canWrite">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="e in equipmentQuery.data.value" :key="e.id" class="data-row">
              <td>{{ e.equipmentCode }}</td>
              <td>{{ e.typeCode }}</td>
              <td>{{ e.criticality }}</td>
              <td>
                {{ e.administrativeState }}
                <span v-if="e.removedAt" class="tiny-badge">已拆卸</span>
              </td>
              <td>v{{ e.version }}</td>
              <td v-if="canWrite">
                <!-- 已拆卸设备（removedAt 非空）不可再编辑或拆卸。 -->
                <template v-if="!e.removedAt">
                  <button class="ghost-action" @click="openEquipmentEdit(e)">编辑</button>
                  <button
                    class="danger-action"
                    :disabled="removeEquipmentMutation.isPending.value"
                    @click="removingId = e.id; removeConfirm = true"
                  >
                    拆卸
                  </button>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="workspace-empty">
        当前位置查询待接入。
      </div>
    </template>

    <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>

    <div v-if="edit.open.value && edit.draft.value" class="modal-backdrop" @click.self="closeEdit">
      <form class="workspace-modal" data-testid="asset-edit-form" @submit.prevent="updateMutation.mutate()">
        <h2>编辑资产（基于 v{{ edit.baseVersion.value }}）</h2>
        <label>资产名称<input v-model="edit.draft.value.displayName" required maxlength="160" /></label>
        <label>资产编号<input v-model="edit.draft.value.assetCode" maxlength="160" /></label>
        <label>类型<input v-model="edit.draft.value.assetType" required maxlength="80" /></label>
        <div v-if="editError" class="form-error" role="alert" data-testid="asset-edit-error">
          <p>{{ editError }}</p>
          <template v-if="editConflict">
            <table v-if="conflictDiff.length" class="conflict-diff" data-testid="asset-conflict-diff">
              <thead><tr><th>字段</th><th>您的草稿</th><th>服务器最新（v{{ edit.latest.value?.version }}）</th></tr></thead>
              <tbody>
                <tr v-for="d in conflictDiff" :key="d.field"><td>{{ d.field }}</td><td>{{ d.mine ?? '—' }}</td><td>{{ d.theirs ?? '—' }}</td></tr>
              </tbody>
            </table>
            <div class="modal-actions">
              <button type="button" class="ghost-action" @click="refreshForConflict">重新获取最新内容</button>
              <button type="button" class="ghost-action" :disabled="!edit.latest.value" @click="rebaseOnLatest">放弃我的修改，基于最新版本重新编辑</button>
            </div>
          </template>
        </div>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="closeEdit">取消</button>
          <button type="submit" class="primary-action" :disabled="!edit.dirty.value || updateMutation.isPending.value || editConflict">
            {{ updateMutation.isPending.value ? '保存中…' : '保存' }}
          </button>
        </div>
      </form>
    </div>

    <div v-if="move.open.value && move.draft.value" class="modal-backdrop" @click.self="closeMove">
      <form class="workspace-modal" data-testid="asset-move-form" @submit.prevent="moveMutation.mutate()">
        <h2>移动资产（基于 v{{ move.baseVersion.value }}）</h2>
        <label>目标位置
          <select v-model="move.draft.value.locationId" required>
            <option value="" disabled>选择位置</option>
            <option v-for="l in (locationsQuery.data.value as AssetLocation[])" :key="l.id" :value="l.id">
              {{ l.name }}（{{ l.kind }}）
            </option>
          </select>
        </label>
        <p v-if="moveError" class="form-error" role="alert">{{ moveError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="closeMove">取消</button>
          <button type="submit" class="primary-action" :disabled="moveMutation.isPending.value">移动</button>
        </div>
      </form>
    </div>

    <div v-if="retire.open.value" class="modal-backdrop" @click.self="closeRetire">
      <div class="workspace-modal" data-testid="asset-retire-dialog">
        <h2>确认退役（基于 v{{ retire.baseVersion.value }}）</h2>
        <p>退役是终态，不可恢复。存在活动 Runtime Binding 时将拒绝。</p>
        <p v-if="retireError" class="form-error" role="alert">{{ retireError }}</p>
        <div class="modal-actions">
          <button class="ghost-action" @click="closeRetire">取消</button>
          <button class="danger-action" :disabled="retireMutation.isPending.value" @click="retireMutation.mutate()">确认退役</button>
        </div>
      </div>
    </div>

    <div v-if="equipmentAdding" class="modal-backdrop" @click.self="equipmentAdding = false">
      <form class="workspace-modal" data-testid="equipment-create-form" @submit.prevent="createEquipmentMutation.mutate()">
        <h2>安装 Equipment</h2>
        <label>设备类型
          <select v-model="equipmentDraft.typeCode" required aria-label="设备类型选择">
            <option value="" disabled>选择已注册类型</option>
            <option v-for="t in equipmentTypeCodes" :key="t" :value="t">{{ t }}</option>
          </select>
        </label>
        <label>Equipment 编号<input v-model="equipmentDraft.equipmentCode" required /></label>
        <label>序列号<input v-model="equipmentDraft.serialNumber" /></label>
        <label>关键度
          <select v-model="equipmentDraft.criticality">
            <option value="LOW">低</option>
            <option value="MEDIUM">中</option>
            <option value="HIGH">高</option>
            <option value="SAFETY_CRITICAL">安全关键</option>
          </select>
        </label>
        <p class="muted small">规格字段按后端版本化 Schema 校验，未知参数留空，不凭空填写设备参数。</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="equipmentAdding = false">取消</button>
          <button type="submit" class="primary-action" :disabled="createEquipmentMutation.isPending.value">
            安装
          </button>
        </div>
      </form>
    </div>

    <div v-if="equipmentEditing" class="modal-backdrop" @click.self="closeEquipmentEdit">
      <form
        class="workspace-modal"
        data-testid="equipment-edit-form"
        @submit.prevent="updateEquipmentMutation.mutate(equipmentEditing)"
      >
        <h2>编辑 Equipment（基于 v{{ equipmentEditBase?.version }}）</h2>
        <label>Equipment 编号<input v-model="equipmentEditing.equipmentCode" required /></label>
        <label>序列号<input v-model="equipmentEditing.serialNumber" /></label>
        <label>关键度
          <select v-model="equipmentEditing.criticality">
            <option value="LOW">低</option>
            <option value="MEDIUM">中</option>
            <option value="HIGH">高</option>
            <option value="SAFETY_CRITICAL">安全关键</option>
          </select>
        </label>
        <p class="muted small">设备类型、Catalog 与 Spec 在此不修改（后端边界一致）。</p>
        <p v-if="equipmentEditError" class="form-error" role="alert">{{ equipmentEditError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" @click="closeEquipmentEdit">取消</button>
          <button type="submit" class="primary-action" :disabled="updateEquipmentMutation.isPending.value">
            保存
          </button>
        </div>
      </form>
    </div>

    <div v-if="removeConfirm" class="modal-backdrop" @click.self="removeConfirm = false">
      <div class="workspace-modal">
        <h2>确认拆卸 Equipment</h2>
        <p>拆卸将关闭安装有效期并保留历史记录，不会硬删除。存在活动 Runtime Binding 时后端将拒绝。</p>
        <div class="modal-actions">
          <button class="ghost-action" @click="removeConfirm = false">取消</button>
          <button
            class="danger-action"
            :disabled="removeEquipmentMutation.isPending.value"
            @click="removeEquipmentMutation.mutate(removingId)"
          >
            确认拆卸
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
