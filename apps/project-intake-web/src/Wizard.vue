<script setup lang="ts">
import { reactive, ref, computed, watch, onMounted, onBeforeUnmount } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { Check, ShieldCheck } from 'lucide-vue-next';
import { useApp } from './store';
import {
  blank,
  stages,
  reqs,
  sources,
  confidences,
  type Input,
  type Project,
  type Duplicate,
} from './model';
import ActionButton from './ActionButton.vue';
const app = useApp(),
  route = useRoute(),
  router = useRouter(),
  form = reactive<Input>(blank()),
  step = ref(1),
  id = ref(String(route.params.id || route.query.draft || '')),
  saving = ref(false),
  saved = ref(''),
  dups = ref<Duplicate[]>([]),
  duplicateStatus = ref(''),
  ready = ref(false),
  leaving = ref(false),
  submitting = ref(false);
let timer: ReturnType<typeof setTimeout>,
  duplicateTimer: ReturnType<typeof setTimeout>,
  pending: Promise<void> | null = null,
  dupSeq = 0;
const snapshot = () => {
  const x = { ...form, version: 0 };
  return JSON.stringify(x);
};
const savedSignature = ref(snapshot());
const dirty = computed(() => snapshot() !== savedSignature.value);
const input = () => JSON.parse(JSON.stringify(form)) as Input;
onMounted(async () => {
  try {
    if (id.value) {
      const p = await app.api<Project>('/api/v1/projects/' + id.value);
      if (!['DRAFT', 'RETURNED'].includes(p.intakeStatus)) throw new Error('当前报备不可编辑');
      for (const key of Object.keys(blank()) as (keyof Input)[])
        Object.assign(form, { [key]: p[key] });
      savedSignature.value = snapshot();
    }
    ready.value = true;
  } catch (e) {
    app.error = (e as Error).message;
  }
});
async function save(silent = false) {
  clearTimeout(timer);
  if (pending) {
    await pending;
    if (dirty.value) return save(silent);
    return;
  }
  if (!dirty.value && id.value) {
    if (!silent) app.toast('草稿已保存');
    return;
  }
  const signature = snapshot(),
    body = input();
  saving.value = true;
  pending = (async () => {
    const p = await app.api<Project>(
      '/api/v1/projects' + (id.value ? '/' + id.value : ''),
      id.value ? 'PATCH' : 'POST',
      body
    );
    id.value = p.id;
    form.version = p.version;
    savedSignature.value = signature;
    saved.value = '草稿已保存';
    if (!route.params.id) {
      window.history.replaceState(window.history.state, '', '/new?draft=' + p.id);
    }
    if (!silent) app.toast('草稿已保存');
  })();
  try {
    await pending;
  } finally {
    pending = null;
    saving.value = false;
  }
}
watch(
  () => snapshot(),
  () => {
    if (!ready.value) return;
    saved.value = '有未保存改动';
    clearTimeout(timer);
    timer = setTimeout(() => {
      void save(true).catch((e) => {
        saved.value = '自动保存失败，输入已保留';
        app.error = e.message;
      });
    }, 1200);
  }
);
watch(
  () => [
    form.customer,
    form.projectName,
    form.location.province,
    form.location.city,
    form.location.factoryName,
    form.location.workshop,
  ],
  () => {
    clearTimeout(duplicateTimer);
    const seq = ++dupSeq;
    if (!form.customer || !form.projectName || !form.location.province || !form.location.city) {
      dups.value = [];
      duplicateStatus.value = '填写客户、位置和项目后自动查重';
      return;
    }
    duplicateStatus.value = '正在查重…';
    duplicateTimer = setTimeout(async () => {
      try {
        const b = await app.api<{ items: Duplicate[] }>(
          '/api/v1/duplicate-checks',
          'POST',
          input()
        );
        if (seq !== dupSeq) return;
        dups.value = b.items;
        duplicateStatus.value = b.items.length
          ? '发现疑似已有报备，提交后由主管确认'
          : '暂未发现疑似重复；提交时会再次核验';
      } catch (e) {
        if (seq === dupSeq) duplicateStatus.value = (e as Error).message;
      }
    }, 450);
  }
);
function next() {
  if (
    step.value === 1 &&
    (!form.customer.trim() ||
      !form.projectName.trim() ||
      !form.location.province.trim() ||
      !form.location.city.trim())
  )
    throw new Error('请填写终端客户、省、市和项目名称');
  if (
    step.value === 2 &&
    (!form.projectType || !form.requirementNotes.trim() || !form.requirements.length)
  )
    throw new Error('请选择项目类型、需求分类并填写主要需求');
  step.value++;
  window.scrollTo(0, 0);
}
async function submit() {
  submitting.value = true;
  try {
    await save(true);
    const p = await app.api<Project>('/api/v1/projects/' + id.value + '/submit', 'POST', {
      version: form.version,
    });
    leaving.value = true;
    app.toast(
      p.intakeStatus === 'UNDER_REVIEW' ? '报备已提交，疑似重复待主管确认' : '报备提交成功'
    );
    await router.push('/projects/' + p.id);
  } finally {
    submitting.value = false;
  }
}
onBeforeRouteLeave(async () => {
  if (leaving.value) return true;
  clearTimeout(timer);
  if (pending) {
    try {
      await pending;
    } catch {
      /* Guard retains unsaved input after failed save. */
    }
  }
  if (!dirty.value) return true;
  const result = await app.confirmDiscard();
  if (!result && dirty.value)
    timer = setTimeout(() => void save(true).catch((e) => (app.error = e.message)), 1200);
  return result;
});
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value || saving.value) {
    e.preventDefault();
    e.returnValue = '';
  }
}
window.addEventListener('beforeunload', beforeUnload);
onBeforeUnmount(() => {
  clearTimeout(timer);
  clearTimeout(duplicateTimer);
  window.removeEventListener('beforeunload', beforeUnload);
});
</script>
<template>
  <div class="wizard">
    <div class="page-heading">
      <div>
        <div class="eyebrow">NEW INTAKE / 新建报备</div>
        <h1>{{ route.params.id ? '继续编辑' : '报备一个新机会' }}</h1>
        <p class="muted">先记录已知信息，细节可以在后续跟进中补充。</p>
      </div>
      <span class="save-state" role="status">{{
        saving ? '正在保存…' : saved || '自动保存草稿'
      }}</span>
    </div>
    <ol class="steps">
      <li
        v-for="(label, i) in ['客户与项目', '设备与需求', '销售与来源']"
        :key="label"
        :class="{ current: step === i + 1, complete: step > i + 1 }"
      >
        <span><Check v-if="step > i + 1" :size="14" /><template v-else>{{ i + 1 }}</template></span>{{ label }}
      </li>
    </ol>
    <fieldset :disabled="!ready || submitting" class="form-body">
      <template v-if="step === 1">
        <div class="section-number">01 / 客户与项目</div>
        <h2>这个机会来自哪里？</h2>
        <p class="muted">准确的客户与位置可以帮助识别重复报备，无需 GPS。</p>
        <div class="form-grid">
          <label class="wide">终端客户 *<input
            v-model="form.customer"
            name="customer"
            autocomplete="organization"
            maxlength="200"
            placeholder="客户企业全称"
          /></label><label>省 *<input
            v-model="form.location.province"
            name="province"
            placeholder="例如：示例省"
            maxlength="100"
          /></label><label>市 *<input
            v-model="form.location.city"
            name="city"
            placeholder="例如：示例市"
            maxlength="100"
          /></label><label>区 / 县<input v-model="form.location.district" /></label><label>工业园区<input v-model="form.location.industrialPark" /></label><label>厂区<input
            v-model="form.location.factoryName"
            name="factoryName"
            placeholder="可选"
          /></label><label>车间<input v-model="form.location.workshop" /></label><label class="wide">详细地址<input v-model="form.location.addressDetail" /></label><label class="wide">项目名称 *<input
            v-model="form.projectName"
            name="projectName"
            maxlength="200"
            placeholder="简要描述项目或改造范围"
          /></label><label class="wide">招标编号<input v-model="form.tenderNo" placeholder="如有，可帮助准确查重" /></label>
        </div>
        <div :class="['duplicate-hint', { warning: dups.length }]" role="status">
          <ShieldCheck :size="18" />
          <div>
            {{ duplicateStatus || '填写客户、位置和项目后自动查重' }}
            <p v-for="(d, i) in dups" :key="i">
              {{ d.message }} · {{ d.region }} · {{ stages[d.salesStage] }} ·
              {{ new Date(d.createdAt).toLocaleDateString() }}
            </p>
          </div>
        </div>
      </template>
      <template v-if="step === 2">
        <div class="section-number">02 / 设备与需求</div>
        <h2>客户希望解决什么问题？</h2>
        <div class="form-grid">
          <label class="wide">项目类型 *<select v-model="form.projectType" name="projectType">
            <option value="">请选择</option>
            <option value="NEW_BUILD">新建项目</option>
            <option value="RETROFIT">存量改造</option>
            <option value="EXPANSION">扩建</option>
          </select></label>
          <div class="wide">
            <span class="field-label">需求分类 *</span>
            <div class="check-grid">
              <label v-for="(v, k) in reqs" :key="k"><input v-model="form.requirements" type="checkbox" :value="k" />{{ v }}</label>
            </div>
          </div>
          <label class="wide">主要需求 *<textarea
            v-model="form.requirementNotes"
            name="requirementNotes"
            rows="4"
            maxlength="4000"
            placeholder="记录当前痛点、目标或已知要求"
          ></textarea></label><label>行车类型<select
            :value="form.plannedEquipment[0]?.equipmentType || ''"
            @change="
              form.plannedEquipment = ($event.target as HTMLSelectElement).value
                ? [
                  {
                    equipmentType: ($event.target as HTMLSelectElement).value,
                    quantity: 1,
                    manufacturer: '',
                  },
                ]
                : []
            "
          >
            <option value="">暂不明确</option>
            <option value="BRIDGE">桥式行车</option>
            <option value="GANTRY">门式行车</option>
            <option value="OTHER">其他</option>
          </select></label><template v-if="form.plannedEquipment[0]">
            <label>预计数量<input
              v-model.number="form.plannedEquipment[0].quantity"
              type="number"
              min="1"
              max="10000"
            /></label><label>厂家<input v-model="form.plannedEquipment[0].manufacturer" /></label><label>基础设备情况<input v-model="form.plannedEquipment[0].notes" /></label>
          </template>
        </div>
        <p class="muted">以上为计划设备信息，后续转换时仍需正式设备建档。</p>
      </template>
      <template v-if="step === 3">
        <div class="section-number">03 / 销售与来源</div>
        <h2>明确当前进展与下一步。</h2>
        <div class="form-grid">
          <label>销售阶段 *<select v-model="form.salesStage">
            <option v-for="(v, k) in stages" :key="k" :value="k">{{ v }}</option>
          </select></label><label>信息来源 *<select v-model="form.informationSource" name="informationSource">
            <option value="">请选择</option>
            <option v-for="(v, k) in sources" :key="k" :value="k">{{ v }}</option>
          </select></label><label>信息可信度 *<select v-model="form.confidenceLevel" name="confidenceLevel">
            <option value="">请选择</option>
            <option v-for="(v, k) in confidences" :key="k" :value="k">{{ v }}</option>
          </select></label><label>预计金额（元）<input
            v-model.number="form.estimatedAmount"
            type="number"
            min="0"
            step="0.01"
          /></label><label>预计采购时间<input v-model="form.expectedPurchaseDate" type="date" /></label><label class="wide">补充说明<textarea v-model="form.notes" rows="3" maxlength="4000"></textarea>
          </label>
        </div>
        <div class="note">提交时将再次查重。发现疑似重复会进入待审核，主管确认后确定归属。</div>
      </template>
    </fieldset>
    <div class="wizard-actions">
      <ActionButton
        action-id="project.step-back"
        :disabled="step === 1 || submitting"
        reason="当前是第一步或正在提交"
        :run="
          () => {
            step--;
          }
        "
      >
        上一步
      </ActionButton><span class="spacer"></span><ActionButton
        action-id="project.save-draft"
        :disabled="!ready || submitting"
        reason="正在加载或提交"
        :run="() => save()"
      >
        保存草稿
      </ActionButton><ActionButton
        v-if="step < 3"
        action-id="project.step-next"
        primary
        :disabled="!ready"
        reason="正在加载"
        :run="next"
      >
        下一步 →
      </ActionButton><ActionButton
        v-else
        action-id="project.submit"
        primary
        :disabled="!form.informationSource || !form.confidenceLevel || !ready"
        reason="请选择信息来源和可信度"
        :run="submit"
      >
        提交报备
      </ActionButton>
    </div>
  </div>
</template>
