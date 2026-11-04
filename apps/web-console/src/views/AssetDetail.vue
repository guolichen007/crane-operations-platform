<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { useRoute, useRouter } from 'vue-router';
import { Pencil, Move, Archive, ArrowLeft, Plus } from 'lucide-vue-next';
import {
  getAsset,
  updateAsset,
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
const equipmentEditError = ref('');
function openEquipmentEdit(e: EquipmentInstallation) {
  equipmentEditing.value = { ...e };
  equipmentEditError.value = '';
}
const updateEquipmentMutation = useMutation({
  mutationFn: (draft: EquipmentInstallation) => updateEquipment(draft.id, draft, equipmentEditing.value!.version),
  onSuccess: async () => {
    equipmentEditing.value = null;
    await queryClient.invalidateQueries({ queryKey: ['equipment', id] });
  },
  onError: (e) => {
    if (e instanceof ApiError && e.status === 409) {
      equipmentEditError.value = '版本冲突：该 Equipment 已被他人修改，请刷新后重试。';
    } else {
      equipmentEditError.value = e instanceof ApiError ? e.message : '更新失败';
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
        <h1>{{ asset.assetCode ?? asset.id }}</h1>
        <p>{{ asset.assetType }} · {{ asset.manufacturerSnapshot || '未关联制造商' }} · v{{ asset.version }}</p>
      </div>
      <div v-if="canWrite" class="detail-actions">
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

    <div v-if="equipmentEditing" class="modal-backdrop" @click.self="equipmentEditing = null">
      <form
        class="workspace-modal"
        data-testid="equipment-edit-form"
        @submit.prevent="updateEquipmentMutation.mutate(equipmentEditing)"
      >
        <h2>编辑 Equipment（If-Match v{{ equipmentEditing.version }}）</h2>
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
          <button type="button" class="ghost-action" @click="equipmentEditing = null">取消</button>
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
