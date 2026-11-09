<script setup lang="ts">
import { reactive, ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { Check, ShieldCheck, Plus, Trash2, UserRound } from '@lucide/vue';
import { useApp } from './store';
import { ApiError } from './api-client';
import {
  blank,
  blankEquipment,
  stages,
  reqs,
  sources,
  confidences,
  equipmentTypes,
  controlModes,
  environments,
  projectTypes,
  statuses,
  type Input,
  type Project,
  type Duplicate,
  type DuplicateCheckResult,
  type RestrictedRelated,
  type KnowledgeState,
} from './model';
import { consistent } from './numeric';
import { readIntent, startIntent, markSent, clearIntent, type CreateIntent } from './create-intent';
import { recovery, hintReason } from './reasons';
import { merge, unresolved, type Side } from './merge';
import ActionButton from './ActionButton.vue';
import NumberField from './NumberField.vue';

const app = useApp(),
  route = useRoute(),
  router = useRouter(),
  form = reactive<Input>(blank()),
  step = ref(1),
  id = ref(String(route.params.id || route.query.draft || '')),
  project = ref<Project | null>(null),
  saving = ref(false),
  saved = ref(''),
  savedAt = ref(''),
  dups = ref<Duplicate[]>([]),
  dupRestricted = ref<RestrictedRelated | null>(null),
  dupState = ref<'insufficient' | 'checking' | 'none' | 'found' | 'error'>('insufficient'),
  ready = ref(false),
  loadError = ref(''),
  leaving = ref(false),
  submitting = ref(false),
  conflict = ref<Project | null>(null),
  choices = ref<Record<string, Side>>({}),
  candidates = ref<{ subject: string; name: string }[] | null>(null),
  assignTarget = ref(''),
  assignReason = ref('创建时指派销售负责人'),
  followLocal = ref(''),
  // G3-3B-R1: numeric fields whose current text cannot be saved exactly.
  invalidNumbers = ref<Set<string>>(new Set());
const stepNames = ['客户与项目', '设备与技术', '商务与跟进', '核对与提交'];
const isAdmin = computed(() => app.user?.role === 'baobei_admin');
let intent: CreateIntent | null = null;
let timer: ReturnType<typeof setTimeout>,
  duplicateTimer: ReturnType<typeof setTimeout>,
  pending: Promise<void> | null = null,
  dupSeq = 0;

const toLocal = (iso: string | null | undefined) => {
  if (!iso) return '';
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};
const minute = (iso: string | null | undefined) => (iso ? Math.floor(new Date(iso).getTime() / 60000) : null);
watch(followLocal, (v) => {
  const iso = v ? new Date(v).toISOString() : null;
  if (minute(iso) !== minute(form.nextFollowupAt)) form.nextFollowupAt = iso;
});

const snapshot = () => JSON.stringify({ ...form, version: 0 });
const savedSignature = ref(snapshot());
const dirty = computed(() => snapshot() !== savedSignature.value);

// Body sent to the server: knowledge flags always agree with their values.
function body(): Input {
  const b = JSON.parse(JSON.stringify(form)) as Input;
  b.knowledge = consistent(b.knowledge, { estimatedAmount: b.estimatedAmount });
  b.plannedEquipment = b.plannedEquipment.map((e) => ({
    ...e,
    knowledge: consistent(e.knowledge, { quantity: e.quantity, capacityTons: e.capacityTons, spanM: e.spanM, liftHeightM: e.liftHeightM }),
  }));
  return b;
}
// Server-side content this editor last loaded or saved: the merge base.
let base: Input = blank();
const pick = (p: Project): Input => {
  const out = blank();
  for (const key of Object.keys(out) as (keyof Input)[]) Object.assign(out, { [key]: JSON.parse(JSON.stringify(p[key] ?? blank()[key])) });
  out.version = p.version;
  return out;
};
function adopt(p: Project) {
  project.value = p;
  base = pick(p);
  for (const key of Object.keys(blank()) as (keyof Input)[]) Object.assign(form, { [key]: JSON.parse(JSON.stringify(p[key] ?? blank()[key])) });
  form.version = p.version;
  followLocal.value = toLocal(p.nextFollowupAt);
  savedSignature.value = snapshot();
}
async function loadProject() {
  const p = await app.api<Project>('/api/v1/projects/' + id.value);
  if (!p.actions?.edit?.allowed) throw new Error(hintReason(p.actions?.edit, '当前项目不可编辑') + '（' + statuses[p.intakeStatus] + '）');
  adopt(p);
}
// After a refresh or lost response, ask the server before creating again.
async function recoverIntent(): Promise<Project | null> {
  if (!intent?.sent || id.value) return null;
  try {
    const r = await app.api<{ project: Project }>('/api/v1/project-create-receipts/' + intent.key);
    id.value = r.project.id;
    clearIntent();
    intent = null;
    window.history.replaceState(window.history.state, '', '/new?draft=' + r.project.id);
    return r.project;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}
onMounted(async () => {
  try {
    if (!id.value && app.user) {
      intent = readIntent(app.user.subject);
      if (await recoverIntent()) app.toast('已找回上次已保存但未确认的草稿');
    }
    if (id.value) await loadProject();
    ready.value = true;
    if (project.value) void loadCandidates();
  } catch (e) {
    loadError.value = recovery(e).message;
    app.error = loadError.value;
  }
});
async function loadCandidates() {
  if (!isAdmin.value || !project.value || !project.value.actions['ownership-transfer']?.allowed) return;
  const b = await app.api<{ items: { subject: string; name: string }[] }>('/api/v1/ownership-candidates?projectId=' + project.value.id);
  candidates.value = b.items.filter((c) => c.subject !== project.value?.ownerSubject);
}

async function save(silent = false): Promise<void> {
  clearTimeout(timer);
  if (conflict.value) throw new Error('存在版本冲突，请先选择处理方式');
  // Never send the previous valid value while the box shows an invalid one.
  if (invalidBlock.value) {
    saved.value = '数值有误，未保存';
    throw new Error(invalidBlock.value);
  }
  if (pending) {
    await pending.catch(() => undefined);
    if (dirty.value) return save(silent);
    return;
  }
  if (!dirty.value && id.value) {
    if (!silent) app.toast('草稿已保存');
    return;
  }
  const signature = snapshot();
  saving.value = true;
  let recoveredNow = false;
  pending = (async () => {
    if (!id.value) {
      // The same intent may already have been committed: continue it with PATCH.
      const recovered = await recoverIntent();
      if (recovered) {
        project.value = recovered;
        base = pick(recovered);
        recoveredNow = true;
        form.version = recovered.version;
      } else {
        if (!intent && app.user) intent = startIntent(app.user.subject);
        markSent(intent!);
      }
    }
    const creating = !id.value;
    const p = await app.api<Project>('/api/v1/projects' + (creating ? '' : '/' + id.value), creating ? 'POST' : 'PATCH', body(), creating ? intent!.key : '');
    const created = creating || recoveredNow;
    id.value = p.id;
    project.value = p;
    base = pick(p);
    form.version = p.version;
    savedSignature.value = signature;
    saved.value = '草稿已保存';
    savedAt.value = new Date(p.updatedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
    if (created) {
      clearIntent();
      intent = null;
      if (!route.params.id) window.history.replaceState(window.history.state, '', '/new?draft=' + p.id);
      void loadCandidates();
    }
    if (!silent) app.toast('草稿已保存');
  })();
  try {
    await pending;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === 'VERSION_CONFLICT' && id.value) {
      conflict.value = await app.api<Project>('/api/v1/projects/' + id.value).catch(() => null);
      choices.value = {};
      saved.value = '版本冲突，自动保存已暂停';
      throw new Error('项目已被其他人更新，自动保存已暂停；你的输入仍保留');
    }
    if (e instanceof ApiError && e.code === 'IDEMPOTENCY_CONFLICT') {
      clearIntent();
      intent = null;
    }
    const r = recovery(e);
    saved.value = '保存失败，输入已保留';
    throw new Error(r.message);
  } finally {
    pending = null;
    saving.value = false;
  }
}
// Three-way merge against the version that caused the 409. Only fields
// changed on both sides need a decision; nothing is overwritten silently.
const mergeResult = computed(() => (conflict.value ? merge(base, body(), pick(conflict.value), choices.value) : null));
const openConflicts = computed(() => (mergeResult.value ? unresolved(mergeResult.value, choices.value) : []));
function takeServer() {
  if (!conflict.value) return;
  adopt(conflict.value);
  conflict.value = null;
  saved.value = '已载入最新版本';
}
async function applyMerge() {
  const c = conflict.value, r = mergeResult.value;
  if (!c || !r || openConflicts.value.length) return;
  const merged = r.merged;
  for (const key of Object.keys(blank()) as (keyof Input)[]) Object.assign(form, { [key]: JSON.parse(JSON.stringify(merged[key] ?? blank()[key])) });
  followLocal.value = toLocal(merged.nextFollowupAt);
  base = pick(c);
  project.value = c;
  form.version = c.version;
  conflict.value = null;
  choices.value = {};
  // A further 409 re-opens the panel against the newer server version.
  await save();
}

watch(invalidNumbers, (v) => {
  if (v.size) {
    clearTimeout(timer);
    saved.value = '数值有误，未保存';
  } else if (saved.value === '数值有误，未保存') {
    // G3-4-A1 UAT-F1: after "放弃修改" the form can be back at the saved
    // state (no snapshot change fires), so the status must not stay stale.
    saved.value = dirty.value ? '有未保存改动' : '草稿已保存';
  }
});
watch(
  () => snapshot(),
  () => {
    if (!ready.value || conflict.value) return;
    saved.value = invalidBlock.value ? '数值有误，未保存' : '有未保存改动';
    if (invalidBlock.value) return;
    clearTimeout(timer);
    timer = setTimeout(() => {
      void save(true).catch((e) => (app.error = e.message));
    }, 1200);
  }
);
const dupReady = computed(() => !!(form.customer.trim() && form.projectName.trim() && form.location.province.trim() && form.location.city.trim()));
function runDuplicateCheck(delay = 450) {
  clearTimeout(duplicateTimer);
  const seq = ++dupSeq;
  if (!dupReady.value) {
    dups.value = [];
    dupRestricted.value = null;
    dupState.value = 'insufficient';
    return;
  }
  dupState.value = 'checking';
  dups.value = [];
  dupRestricted.value = null;
  duplicateTimer = setTimeout(async () => {
    try {
      const b = await app.api<DuplicateCheckResult>('/api/v1/duplicate-checks', 'POST', body());
      if (seq !== dupSeq) return;
      dups.value = b.items;
      dupRestricted.value = b.restrictedRelated ?? null;
      // A hidden match is still a match: never report "no duplicates" when a notice is present.
      dupState.value = b.items.length || dupRestricted.value ? 'found' : 'none';
    } catch {
      if (seq === dupSeq) dupState.value = 'error';
    }
  }, delay);
}
watch(() => [form.customer, form.projectName, form.location.province, form.location.city, form.location.factoryName, form.location.workshop, form.tenderNo], () => runDuplicateCheck());
watch(step, (s) => s === 4 && runDuplicateCheck(0));
const dupMessage = computed(
  () =>
    ({
      insufficient: '信息不足，尚无法完成查重：需要客户、项目名称和省市。提交时服务端会再次核验。',
      checking: '正在查重…',
      none: '暂未发现疑似重复；提交时会再次核验',
      found: '发现疑似已有报备，提交后由主管确认',
      error: '查重暂时不可用，提交时服务端会再次核验',
    })[dupState.value]
);

function addEquipment() {
  if (form.plannedEquipment.length < 30) form.plannedEquipment.push(blankEquipment());
}
function markInvalid(key: string, bad: boolean) {
  const next = new Set(invalidNumbers.value);
  if (bad) next.add(key);
  else next.delete(key);
  invalidNumbers.value = next;
}
const invalidBlock = computed(() => (invalidNumbers.value.size ? '请先修正标红的数值（如预估金额最多两位小数）' : ''));
function setNumber(target: object, key: string, value: number | null, state: KnowledgeState) {
  const row = target as Record<string, unknown> & { knowledge?: Record<string, KnowledgeState> };
  row[key] = value;
  row.knowledge = { ...(row.knowledge || {}), [key]: state };
}
function toggle(list: string[] | null | undefined, value: string, on: boolean) {
  const out = (list || []).filter((x) => x !== value);
  if (on) out.push(value);
  return out;
}

const missing = computed(() => {
  const m: { label: string; step: number }[] = [];
  if (!form.customer.trim()) m.push({ label: '终端客户', step: 1 });
  if (!form.projectName.trim()) m.push({ label: '项目名称', step: 1 });
  if (!form.location.province.trim() || !form.location.city.trim()) m.push({ label: '项目所在省市', step: 1 });
  if (!form.informationSource) m.push({ label: '项目来源', step: 1 });
  if (!form.requirements.length) m.push({ label: '自动化需求分类', step: 2 });
  if (!form.requirementNotes.trim()) m.push({ label: '主要需求说明', step: 2 });
  form.plannedEquipment.forEach((e, i) => e.quantity == null && m.push({ label: `第 ${i + 1} 台设备的数量`, step: 2 }));
  if (!form.projectType) m.push({ label: '项目类型', step: 3 });
  if (!form.confidenceLevel) m.push({ label: '信息可信度', step: 3 });
  return m;
});
const ownerLine = computed(() => {
  const p = project.value;
  if (!p) return isAdmin.value ? '保存后为待分配草稿，由你保管' : '你（当前登录销售）';
  if (!p.ownerSubject) return '待分配 · 保管人 ' + (p.custodianName || '管理员');
  return p.ownerName || '姓名待核对';
});
const submitHint = computed(() => project.value?.actions?.submit);
const submitBlock = computed(() => {
  if (invalidBlock.value) return invalidBlock.value;
  if (!project.value) return '请先保存草稿';
  if (missing.value.length) return '还有 ' + missing.value.length + ' 项必填信息未完成';
  if (dirty.value) return '';
  return submitHint.value?.allowed ? '' : hintReason(submitHint.value, '当前不可提交');
});

async function assign() {
  if (!project.value) return;
  await save(true);
  const p = await app.api<Project>(`/api/v1/projects/${project.value.id}/ownership-transfer`, 'POST', {
    version: form.version,
    reason: assignReason.value.trim(),
    targetSubject: assignTarget.value,
  });
  adopt(p);
  assignTarget.value = '';
  app.toast('已指派给 ' + (p.ownerName || '销售负责人') + '，可以正式提交');
  void loadCandidates();
}
// G3-4-A0: one rule for every step change (next, back, header jump, "去填写"):
// while a number box holds text that cannot be saved, the user stays on that
// step to correct it or explicitly discard it. Leaving the step would unmount
// the field and silently fall back to the previously saved value.
const stepNotice = ref('');
function goStep(n: number) {
  if (invalidBlock.value) {
    stepNotice.value = invalidBlock.value + '，或点击该字段下的“放弃修改”后再切换步骤。';
    // G3-4-A1 visual review V1: on narrow screens the field can sit under the
    // fixed action bar; bring the first invalid field (and its hint) into view.
    void nextTick(() => {
      const el = document.querySelector<HTMLInputElement>('.wizard [aria-invalid="true"]');
      el?.scrollIntoView({ block: 'center' });
      el?.focus({ preventScroll: true });
    });
    return;
  }
  stepNotice.value = '';
  step.value = Math.min(4, Math.max(1, n));
  window.scrollTo(0, 0);
}
watch(invalidNumbers, (v) => !v.size && (stepNotice.value = ''));
function next() {
  goStep(step.value + 1);
}
async function submit() {
  submitting.value = true;
  try {
    await save(true);
    if (project.value && !project.value.actions.submit.allowed) throw new Error(hintReason(project.value.actions.submit));
    const p = await app.api<Project>('/api/v1/projects/' + id.value + '/submit', 'POST', { version: form.version });
    leaving.value = true;
    app.toast(p.intakeStatus === 'UNDER_REVIEW' ? '已正式报备，疑似重复待主管确认' : '已正式报备，等待审核');
    await router.push('/projects/' + p.id);
  } catch (e) {
    throw new Error(recovery(e).message);
  } finally {
    submitting.value = false;
  }
}
onBeforeRouteLeave(async () => {
  if (leaving.value) return true;
  clearTimeout(timer);
  if (pending) await pending.catch(() => undefined);
  // Invalid text in a number box is an unsaved change too.
  if (!dirty.value && !invalidNumbers.value.size) return true;
  const result = await app.confirmDiscard();
  if (!result && dirty.value && !conflict.value) timer = setTimeout(() => void save(true).catch((e) => (app.error = e.message)), 1200);
  return result;
});
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value || saving.value || invalidNumbers.value.size) {
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
        <h1>{{ route.params.id || id ? '继续完善报备' : isAdmin ? '新建项目' : '快速报备' }}</h1>
        <p class="muted">先记下已知信息即可保存草稿；正式报备前再补齐必填项。</p>
      </div>
      <span class="save-state" role="status" aria-live="polite">{{
        saving ? '正在保存…' : saved ? saved + (savedAt && saved === '草稿已保存' ? ' · ' + savedAt : '') : '有输入后自动保存草稿'
      }}</span>
    </div>
    <p v-if="loadError" class="form-alert" role="alert">{{ loadError }}</p>
    <div class="owner-strip">
      <UserRound :size="16" aria-hidden="true" /><span>负责人</span><strong>{{ ownerLine }}</strong>
      <span v-if="project" class="muted">{{ project.registrationNo }} · {{ statuses[project.intakeStatus] }}</span>
    </div>
    <ol class="steps" aria-label="报备步骤">
      <li v-for="(label, i) in stepNames" :key="label" :class="{ current: step === i + 1, complete: step > i + 1 }" :aria-current="step === i + 1 ? 'step' : undefined">
        <ActionButton action-id="project.step-jump" :run="() => goStep(i + 1)"><span class="step-dot"><Check v-if="step > i + 1" :size="14" /><template v-else>{{ i + 1 }}</template></span>{{ label }}</ActionButton>
      </li>
    </ol>
    <div v-if="conflict && mergeResult" class="conflict-panel" role="alert">
      <h2>项目已被其他人更新</h2>
      <p>自动保存已暂停，你的输入仍保留。系统已与服务器最新版本（修订 {{ conflict.version }}）逐字段比对：</p>
      <ul class="merge-summary">
        <li>采纳对方修改 {{ mergeResult.fromRemote.length }} 项<template v-if="mergeResult.remoteChanges.length">：{{ mergeResult.remoteChanges.join('；') }}</template></li>
        <li>保留你的修改 {{ mergeResult.fromLocal.length }} 项<template v-if="mergeResult.localChanges.length">：{{ mergeResult.localChanges.join('；') }}</template></li>
        <li v-if="mergeResult.conflicts.length">双方改了同一字段 {{ mergeResult.conflicts.length }} 项，需要逐项选择。</li>
        <li v-else>没有同一字段的冲突，可以直接合并保存。</li>
      </ul>
      <fieldset v-for="c in mergeResult.conflicts" :key="c.key" class="conflict-choice">
        <legend>{{ c.label }}</legend>
        <label><input v-model="choices[c.key]" type="radio" :name="'merge-' + c.key" value="local" />使用我的输入：{{ c.local }}</label>
        <label><input v-model="choices[c.key]" type="radio" :name="'merge-' + c.key" value="remote" />使用服务器版本：{{ c.remote }}</label>
      </fieldset>
      <div class="actions">
        <ActionButton action-id="draft.conflict-reload" :run="takeServer">放弃我的修改，载入服务器版本</ActionButton><ActionButton
          action-id="draft.conflict-merge"
          primary
          :disabled="openConflicts.length > 0"
          :reason="'还有 ' + openConflicts.length + ' 项需要选择'"
          :run="applyMerge"
        >
          合并并保存
        </ActionButton>
      </div>
    </div>
    <fieldset :disabled="!ready || submitting" class="form-body">
      <template v-if="step === 1">
        <h2>客户与项目</h2>
        <p class="muted">客户、项目名称和省市用于识别重复报备。暂不清楚可以留空。</p>
        <div class="form-grid">
          <label class="wide">终端客户<input v-model="form.customer" name="customer" autocomplete="organization" maxlength="200" placeholder="客户企业全称" /></label>
          <label class="wide">项目名称<input v-model="form.projectName" name="projectName" maxlength="200" placeholder="例如：二号车间桥式行车防摇改造" /></label>
          <label>省<input v-model="form.location.province" name="province" maxlength="100" /></label>
          <label>市<input v-model="form.location.city" name="city" maxlength="100" /></label>
          <label class="wide">项目来源<select v-model="form.informationSource" name="informationSource">
            <option value="">暂不明确</option>
            <option v-for="(v, k) in sources" :key="k" :value="k">{{ v }}</option>
          </select></label>
        </div>
        <h3 class="subhead">现场位置（可选）</h3>
        <div class="form-grid">
          <label>区 / 县<input v-model="form.location.district" /></label>
          <label>工业园区<input v-model="form.location.industrialPark" /></label>
          <label>厂区<input v-model="form.location.factoryName" name="factoryName" /></label>
          <label>车间<input v-model="form.location.workshop" name="workshop" /></label>
          <label class="wide">详细地址<input v-model="form.location.addressDetail" /></label>
          <label class="wide">招标编号<input v-model="form.tenderNo" name="tenderNo" placeholder="如有，可帮助准确查重" /></label>
        </div>
      </template>
      <template v-if="step === 2">
        <h2>设备与技术</h2>
        <p class="muted">逐台记录计划设备；吨位、跨度、起升高度不清楚时留空，不要填 0。</p>
        <div class="wide">
          <span class="field-label">自动化需求分类</span>
          <div class="check-grid">
            <label v-for="(v, k) in reqs" :key="k"><input v-model="form.requirements" type="checkbox" :value="k" />{{ v }}</label>
          </div>
        </div>
        <div class="form-grid">
          <label class="wide">主要需求说明<textarea v-model="form.requirementNotes" name="requirementNotes" rows="3" maxlength="4000" placeholder="当前痛点、目标、已知技术要求"></textarea></label>
        </div>
        <section v-for="(eq, i) in form.plannedEquipment" :key="i" class="equipment-row" :aria-label="'计划设备 ' + (i + 1)">
          <div class="equipment-head">
            <h3>计划设备 {{ i + 1 }}</h3>
            <ActionButton action-id="equipment.remove" :aria-label="'移除计划设备 ' + (i + 1)" :run="() => { form.plannedEquipment.splice(i, 1); }"><Trash2 :size="15" aria-hidden="true" />移除</ActionButton>
          </div>
          <div class="form-grid compact">
            <label>设备类型<select v-model="eq.equipmentType" :name="'equipmentType' + i">
              <option v-for="(v, k) in equipmentTypes" :key="k" :value="k">{{ v }}</option>
            </select></label>
            <NumberField label="数量" unit="台" integer :name="'quantity' + i" :model-value="eq.quantity" :state="eq.knowledge?.quantity" @update="(v, s) => setNumber(eq, 'quantity', v, s)" @validity="(bad) => markInvalid('quantity' + i, bad)" />
            <NumberField label="额定起重量" unit="t" :name="'capacityTons' + i" :model-value="eq.capacityTons" :state="eq.knowledge?.capacityTons" @update="(v, s) => setNumber(eq, 'capacityTons', v, s)" @validity="(bad) => markInvalid('capacityTons' + i, bad)" />
            <NumberField label="跨度" unit="m" :name="'spanM' + i" :model-value="eq.spanM" :state="eq.knowledge?.spanM" @update="(v, s) => setNumber(eq, 'spanM', v, s)" @validity="(bad) => markInvalid('spanM' + i, bad)" />
            <NumberField label="起升高度" unit="m" :name="'liftHeightM' + i" :model-value="eq.liftHeightM" :state="eq.knowledge?.liftHeightM" @update="(v, s) => setNumber(eq, 'liftHeightM', v, s)" @validity="(bad) => markInvalid('liftHeightM' + i, bad)" />
            <label>控制方式<select v-model="eq.controlMode">
              <option value="">暂不明确</option>
              <option v-for="(v, k) in controlModes" :key="k" :value="k">{{ v }}</option>
            </select></label>
            <label>厂家<input v-model="eq.manufacturer" maxlength="200" /></label>
            <label>型号<input v-model="eq.model" maxlength="200" /></label>
            <fieldset class="wide chip-set">
              <legend>工作环境</legend>
              <label v-for="(v, k) in environments" :key="k"><input type="checkbox" :checked="eq.environmentFlags?.includes(k)" @change="eq.environmentFlags = toggle(eq.environmentFlags, k, ($event.target as HTMLInputElement).checked)" />{{ v }}</label>
            </fieldset>
            <fieldset class="wide chip-set">
              <legend>该设备的自动化目标</legend>
              <label v-for="(v, k) in reqs" :key="k"><input type="checkbox" :checked="eq.automationGoals?.includes(k)" @change="eq.automationGoals = toggle(eq.automationGoals, k, ($event.target as HTMLInputElement).checked)" />{{ v }}</label>
            </fieldset>
            <label class="wide">现状说明<input v-model="eq.notes" maxlength="1000" /></label>
          </div>
        </section>
        <ActionButton action-id="equipment.add" :disabled="form.plannedEquipment.length >= 30" reason="最多记录 30 台" :run="addEquipment"><Plus :size="15" aria-hidden="true" />添加计划设备</ActionButton>
        <p class="muted small">计划设备仅用于报备，不会生成正式设备档案。</p>
      </template>
      <template v-if="step === 3">
        <h2>商务与跟进</h2>
        <div class="form-grid">
          <label>项目类型<select v-model="form.projectType" name="projectType">
            <option value="">暂不明确</option>
            <option v-for="(v, k) in projectTypes" :key="k" :value="k">{{ v }}</option>
          </select></label>
          <label>信息可信度<select v-model="form.confidenceLevel" name="confidenceLevel">
            <option value="">暂不明确</option>
            <option v-for="(v, k) in confidences" :key="k" :value="k">{{ v }}</option>
          </select></label>
          <label>销售阶段<select v-model="form.salesStage" name="salesStage">
            <option v-for="(v, k) in stages" :key="k" :value="k">{{ v }}</option>
          </select></label>
          <NumberField label="预估金额" unit="元" name="estimatedAmount" money :model-value="form.estimatedAmount" :state="form.knowledge?.estimatedAmount" @update="(v, s) => setNumber(form, 'estimatedAmount', v, s)" @validity="(bad) => markInvalid('estimatedAmount', bad)" />
          <label>预期采购日期<input v-model="form.expectedPurchaseDate" type="date" name="expectedPurchaseDate" /></label>
          <label>下次跟进时间<input v-model="followLocal" type="datetime-local" name="draftFollowupAt" /></label>
          <label class="wide">下一步事项<input v-model="form.nextAction" name="draftNextAction" maxlength="1000" placeholder="例如：约技术交流，确认跨度与控制方式" /></label>
          <label class="wide">补充说明<textarea v-model="form.notes" rows="3" maxlength="4000"></textarea></label>
        </div>
      </template>
      <template v-if="step === 4">
        <h2>核对与提交</h2>
        <section class="review-block">
          <h3>待补信息</h3>
          <p v-if="!missing.length" class="ok-line">必填信息已齐全。</p>
          <ul v-else class="missing-list">
            <li v-for="m in missing" :key="m.label">{{ m.label }}<ActionButton action-id="project.step-jump" :run="() => goStep(m.step)">去填写</ActionButton></li>
          </ul>
        </section>
        <section class="review-block">
          <h3>归属</h3>
          <p>{{ ownerLine }}</p>
          <template v-if="isAdmin && project && project.actions['ownership-transfer']?.allowed">
            <p v-if="candidates && !candidates.length" class="muted">
              目前没有可指派的有效销售。草稿会保持待分配，可在人员管理中邀请销售后再指派。
            </p>
            <div v-else-if="candidates" class="assign-row">
              <label>指派给有效销售<select v-model="assignTarget" name="assignTarget">
                <option value="">请选择</option>
                <option v-for="c in candidates" :key="c.subject" :value="c.subject">{{ c.name }}</option>
              </select></label>
              <label>指派原因<input v-model="assignReason" name="assignReason" maxlength="300" /></label>
              <ActionButton action-id="project.assign" :disabled="!assignTarget || !assignReason.trim()" reason="请选择销售并填写原因" :run="assign">确认指派</ActionButton>
            </div>
            <p class="muted small">指派后你将成为该项目的参与人，审核需由其他独立审核人完成。</p>
          </template>
        </section>
        <section class="review-block">
          <h3>重复风险</h3>
          <div :class="['duplicate-hint', { warning: dupState === 'found', pending: dupState === 'insufficient' || dupState === 'error' }]" role="status">
            <ShieldCheck :size="18" aria-hidden="true" />
            <div>
              {{ dupMessage }}
              <p v-for="(d, i) in dups" :key="i">{{ d.message }} · {{ d.region }} · {{ stages[d.salesStage] }} · {{ new Date(d.createdAt).toLocaleDateString('zh-CN') }}</p>
              <p v-if="dupRestricted" data-testid="restricted-related">{{ dupRestricted.message }}</p>
            </div>
          </div>
        </section>
        <section class="review-block">
          <h3>摘要</h3>
          <dl class="summary-grid">
            <dt>客户</dt><dd>{{ form.customer || '未填写' }}</dd>
            <dt>项目</dt><dd>{{ form.projectName || '未填写' }}</dd>
            <dt>地点</dt><dd>{{ [form.location.province, form.location.city, form.location.factoryName, form.location.workshop].filter(Boolean).join(' ') || '未填写' }}</dd>
            <dt>计划设备</dt><dd>{{ form.plannedEquipment.length ? form.plannedEquipment.map((e) => (equipmentTypes[e.equipmentType] || '设备') + ' × ' + (e.quantity ?? '数量未知')).join('，') : '未记录' }}</dd>
            <dt>预估金额</dt><dd>{{ form.estimatedAmount == null ? '未知' : form.estimatedAmount.toLocaleString('zh-CN') + ' 元' }}</dd>
            <dt>下一步</dt><dd>{{ form.nextAction || '未记录' }}</dd>
          </dl>
        </section>
        <p class="note">“保存草稿”不会建立销售保护；只有“提交正式报备”成功后才进入审核，并在服务端再次查重。</p>
      </template>
      <div v-if="step < 4 && step !== 1" class="dup-mini" role="status">{{ dupMessage }}</div>
      <div v-if="step === 1" :class="['duplicate-hint', { warning: dupState === 'found', pending: dupState === 'insufficient' || dupState === 'error' }]" role="status">
        <ShieldCheck :size="18" aria-hidden="true" />
        <div>
          {{ dupMessage }}
          <p v-for="(d, i) in dups" :key="i">{{ d.message }} · {{ d.region }} · {{ stages[d.salesStage] }} · {{ new Date(d.createdAt).toLocaleDateString('zh-CN') }}</p>
          <p v-if="dupRestricted" data-testid="restricted-related">{{ dupRestricted.message }}</p>
        </div>
      </div>
    </fieldset>
    <p v-if="stepNotice" class="form-alert wizard-step-notice" role="alert">{{ stepNotice }}</p>
    <div class="wizard-actions">
      <ActionButton action-id="project.step-back" :disabled="step === 1 || submitting" reason="当前是第一步或正在提交" :run="() => goStep(step - 1)">上一步</ActionButton><span class="spacer"></span><ActionButton
        action-id="project.save-draft"
        :disabled="!ready || submitting || saving || !!conflict || !!invalidBlock"
        :reason="conflict ? '请先处理版本冲突' : invalidBlock || '正在加载、保存或提交'"
        :run="() => save()"
      >
        保存草稿
      </ActionButton><ActionButton v-if="step < 4" action-id="project.step-next" primary :disabled="!ready" reason="正在加载" :run="next">下一步</ActionButton><ActionButton
        v-else
        action-id="project.submit"
        primary
        :disabled="!ready || !!submitBlock || !!conflict"
        :reason="submitBlock || '请先处理版本冲突'"
        :run="submit"
      >
        提交正式报备
      </ActionButton>
    </div>
  </div>
</template>
