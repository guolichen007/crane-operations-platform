<script setup lang="ts">
// Q3: formal projects that the caller holds a ProjectGrant on but that are not yet in Inventory.
// "登记运营信息" turns the SAME project id into an Inventory project (lifecycle + administrative
// region). Only callers whose project role has location_write may submit; the server decides.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query';
import { onBeforeRouteLeave } from 'vue-router';
import { RefreshCw, MapPinned } from 'lucide-vue-next';
import { listPendingInventoryProjects, registerProjectInventory } from '../cloud/portfolio';
import { loadGeoManifest } from '../scene/geo-manifest';
import { allowClose } from '../cloud/draft';
import { sceneProvider } from '../scene/provider';
import {
  LIFECYCLES, blankRegistration, registrationDirty, provinces, childrenOf, precisionText, validateRegistration, toRegistration,
  registrationErrorMessage, type RegistrationForm,
} from '../cloud/inventory-registration';
import { intakeErrorMessage } from '../cloud/intake';
import type { InventoryProject, PendingInventoryProject } from '../../../../contracts/typescript/types.gen';

const emit = defineEmits<{ registered: [project: InventoryProject] }>();
const queryClient = useQueryClient();
const pendingQuery = useQuery({ queryKey: ['inventory-pending'], queryFn: listPendingInventoryProjects });
const geoQuery = useQuery({ queryKey: ['geo-manifest'], queryFn: loadGeoManifest, staleTime: Infinity, retry: 1 });
const manifest = computed(() => geoQuery.data.value);

const target = ref<PendingInventoryProject | null>(null);
const verifying = ref(false);
const confirmed = ref('');
const form = ref<RegistrationForm>(blankRegistration());
const touched = ref(false);
const formError = ref('');
const errors = computed(() => validateRegistration(form.value, manifest.value));
const shown = computed(() => (touched.value ? errors.value : {}));
const dirty = computed(() => target.value !== null && registrationDirty(form.value));
const precision = computed(() => precisionText(manifest.value, form.value));
const provinceList = computed(() => (manifest.value ? provinces(manifest.value) : []));
const prefectureList = computed(() => (manifest.value ? childrenOf(manifest.value, form.value.province) : []));
const countyList = computed(() => (manifest.value ? childrenOf(manifest.value, form.value.prefecture) : []));

function onProvince() { form.value.prefecture = ''; form.value.county = ''; }
function onPrefecture() { form.value.county = ''; }

const mutation = useMutation({
  mutationFn: () => registerProjectInventory(target.value!.id, toRegistration(form.value)),
  onSuccess: async (project) => {
    target.value = null;
    form.value = blankRegistration();
    emit('registered', project);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['inventory-pending'] }),
      queryClient.invalidateQueries({ queryKey: ['portfolio-projects'] }),
      queryClient.invalidateQueries({ queryKey: ['projects'] }),
      // Homepage / map / search read the Inventory through the scene provider: reload it.
      sceneProvider.refresh().catch(() => undefined),
    ]);
  },
  onError: async (e) => {
    formError.value = registrationErrorMessage(e);
    // Server errors are rolled back (Q3-R1); a network error or a 409 means the outcome must be
    // confirmed from the server before anything is resubmitted: reload the pending list, and if
    // the project is no longer pending it IS registered — close the form instead of retrying.
    const id = target.value?.id;
    verifying.value = true;
    try {
      const pending = await queryClient.fetchQuery({ queryKey: ['inventory-pending'], queryFn: listPendingInventoryProjects, staleTime: 0 });
      if (id && !pending.some((p) => p.id === id)) {
        target.value = null;
        form.value = blankRegistration();
        confirmed.value = `${id} 已登记（服务端确认）。为避免重复提交，表单已关闭。`;
        await Promise.all([queryClient.invalidateQueries({ queryKey: ['portfolio-projects'] }), sceneProvider.refresh().catch(() => undefined)]);
        emit('registered', { id } as InventoryProject);
      }
    } catch {
      formError.value += ' 无法确认当前状态，请稍后刷新页面查看，不要重复提交。';
    } finally {
      verifying.value = false;
    }
  },
});

function open(p: PendingInventoryProject) {
  if (!allowClose(dirty.value, mutation.isPending.value)) return;
  target.value = p;
  form.value = blankRegistration();
  touched.value = false;
  formError.value = '';
}
function close() {
  if (!allowClose(dirty.value, mutation.isPending.value)) return;
  target.value = null;
}
function submit() {
  touched.value = true;
  formError.value = '';
  if (mutation.isPending.value || verifying.value || Object.keys(errors.value).length) return;
  mutation.mutate();
}
function onKeydown(e: KeyboardEvent) { if (e.key === 'Escape' && target.value) close(); }
function onBeforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value || mutation.isPending.value) { e.preventDefault(); e.returnValue = ''; }
}
onMounted(() => { window.addEventListener('keydown', onKeydown); window.addEventListener('beforeunload', onBeforeUnload); });
onBeforeUnmount(() => { window.removeEventListener('keydown', onKeydown); window.removeEventListener('beforeunload', onBeforeUnload); });
onBeforeRouteLeave(() => allowClose(dirty.value, mutation.isPending.value));
const lifecycleText = (v: string) => LIFECYCLES.find((l) => l.value === v)?.label ?? v;
</script>
<template>
  <section class="pending-inventory" data-testid="pending-inventory">
    <p v-if="confirmed" class="confirmed" role="status" data-testid="registration-confirmed">{{ confirmed }}</p>
    <div v-if="pendingQuery.isLoading.value" class="skeleton" style="height: 48px" aria-busy="true"></div>
    <p v-else-if="pendingQuery.isError.value" class="workspace-error" role="alert" data-testid="pending-error">
      待登记项目加载失败：{{ intakeErrorMessage(pendingQuery.error.value, 'load') }}
      <button class="ghost-action" @click="pendingQuery.refetch()"><RefreshCw :size="13" />重试</button>
    </p>
    <template v-else-if="pendingQuery.data.value?.length">
      <h2><MapPinned :size="15" />待登记运营信息的正式项目</h2>
      <p class="hint">这些项目已批准并已授权给您，但尚未登记 Inventory（生命周期与行政区），因此还不在项目列表和全国地图中。</p>
      <table class="data-table" data-testid="pending-table">
        <thead><tr><th>正式项目</th><th>您的项目角色</th><th>已登记资产</th><th></th></tr></thead>
        <tbody>
          <tr v-for="p in pendingQuery.data.value" :key="p.id" :data-pending-id="p.id">
            <td>{{ p.name }}<div class="sub">{{ p.id }}</div></td>
            <td>{{ p.role }}</td>
            <td>{{ p.assetCount }}</td>
            <td>
              <button v-if="p.canRegister" class="primary-action" data-testid="register-open" @click="open(p)">登记运营信息</button>
              <span v-else class="sub">需项目管理员登记</span>
            </td>
          </tr>
        </tbody>
      </table>
    </template>

    <div v-if="target" class="modal-backdrop" @click.self="close">
      <form class="workspace-modal registration-form" data-testid="registration-form" novalidate @submit.prevent="submit">
        <h2>登记运营信息 · {{ target.name }}</h2>
        <p class="hint">登记后该项目（同一 ID {{ target.id }}）进入项目列表、运营统计和全国地图。位置只到行政区级，不是设备精确坐标；登记不会授予任何人权限。</p>
        <label>项目生命周期 *
          <select v-model="form.lifecycle" aria-label="项目生命周期" :aria-invalid="!!shown.lifecycle">
            <option value="" disabled>选择生命周期</option>
            <option v-for="l in LIFECYCLES" :key="l.value" :value="l.value">{{ l.label }}</option>
          </select>
          <span v-if="shown.lifecycle" class="field-error">{{ shown.lifecycle }}</span>
        </label>
        <p v-if="geoQuery.isError.value" class="workspace-error" role="alert" data-testid="geo-error">
          行政区数据加载失败，暂不能选择位置。<button type="button" class="ghost-action" @click="geoQuery.refetch()"><RefreshCw :size="13" />重试</button>
        </p>
        <div class="form-row three">
          <label>省 *
            <select v-model="form.province" aria-label="省" :disabled="!manifest" :aria-invalid="!!shown.province" @change="onProvince">
              <option value="">选择省份</option>
              <option v-for="r in provinceList" :key="r.adcode" :value="String(r.adcode)">{{ r.name }}</option>
            </select>
            <span v-if="shown.province" class="field-error">{{ shown.province }}</span>
          </label>
          <label>市
            <select v-model="form.prefecture" aria-label="市" :disabled="!prefectureList.length" @change="onPrefecture">
              <option value="">（省级 / 不细分）</option>
              <option v-for="r in prefectureList" :key="r.adcode" :value="String(r.adcode)">{{ r.name }}</option>
            </select>
          </label>
          <label>区县
            <select v-model="form.county" aria-label="区县" :disabled="!countyList.length">
              <option value="">（市级 / 不细分）</option>
              <option v-for="r in countyList" :key="r.adcode" :value="String(r.adcode)">{{ r.name }}</option>
            </select>
          </label>
        </div>
        <p class="hint" data-testid="precision">定位精度：{{ precision }}</p>
        <label>位置说明<input v-model="form.locationLabel" aria-label="位置说明" maxlength="240" placeholder="留空则使用行政区名称" /></label>
        <label>实施计划备注<input v-model="form.scheduleText" aria-label="实施计划备注" maxlength="400" /></label>
        <p v-if="formError" class="form-error" role="alert" data-testid="registration-error">{{ formError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="mutation.isPending.value" @click="close">取消</button>
          <button type="submit" class="primary-action" :disabled="mutation.isPending.value || verifying" data-testid="registration-submit">
            {{ mutation.isPending.value ? '保存中…' : verifying ? '确认状态中…' : `登记为「${lifecycleText(form.lifecycle) || '…'}」` }}
          </button>
        </div>
      </form>
    </div>
  </section>
</template>
<style scoped>
.pending-inventory { display: flex; flex-direction: column; gap: 10px; margin-bottom: 18px; }
.pending-inventory h2 { margin: 0; font-size: 15px; color: var(--text-1); display: flex; align-items: center; gap: 6px; }
.confirmed { color: #6fd39b; font-size: 13px; margin: 0; }
.hint { margin: 0; font-size: 12px; color: var(--text-3); }
.sub { font-size: 11px; color: var(--text-3); }
.registration-form { width: min(600px, calc(100vw - 48px)); }
.form-row.three { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.field-error { color: #ff665f; font-size: 11px; }
</style>
