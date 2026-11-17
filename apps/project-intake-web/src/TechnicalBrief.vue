<script setup lang="ts">
// V1.1 R6 (B3) technical brief: completed during technical exchange / site
// survey, never required at filing. The filed equipment and requirements stay
// as filed (shown read-only); every save is a new version with history; open
// questions are tracked with asker, answer and status.
import { ref, computed, onMounted } from 'vue';
import { Plus, Trash2 } from '@lucide/vue';
import { useApp } from './store';
import { recovery } from './reasons';
import { when, type ActionHint } from './model';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';

interface Param { key: string; value: string; unit: string }
interface Equip { equipmentType: string; quantity: number | null; capacityTons: number | null; spanM: number | null; liftHeightM: number | null; notes: string }
interface Question { id: string; question: string; status: string; answer: string; askedByName: string; answeredByName: string; briefVersion: number; version: number; createdAt: string; canAnswer: boolean; canClose: boolean }
interface Brief {
  version: number; status: string; projectNature: string; scenario: string; workshop: string; productionLine: string; siteConstraints: string;
  parameters: Param[]; equipment: Equip[]; updatedByName: string; updatedAt: string | null;
  filed: { plannedEquipment: { equipmentType: string; quantity: number | null }[]; requirements: string[]; requirementNotes: string };
  questions: Question[]; revisions: { version: number; actorName: string; reason: string; at: string }[];
  actions: Record<string, ActionHint>;
}
const props = defineProps<{ projectId: string }>();
const app = useApp();
const brief = ref<Brief | null>(null),
  failure = ref(''),
  editing = ref(false),
  form = ref<Omit<Brief, 'filed' | 'questions' | 'revisions' | 'actions' | 'updatedByName' | 'updatedAt'> & { reason: string }>(blankForm()),
  formError = ref('');
const statuses: Record<string, string> = { NOT_STARTED: '未开始', COLLECTING: '收集中', CLARIFIED: '已澄清' };
const natures: Record<string, string> = { '': '暂不明确', NEW_BUILD: '新建', RETROFIT: '技改', EXPANSION: '扩产', REPLACEMENT: '替换' };
const qStatus: Record<string, string> = { OPEN: '待答复', ANSWERED: '已答复', CLOSED: '已关闭' };
function blankForm() {
  return { version: 0, status: 'COLLECTING', projectNature: '', scenario: '', workshop: '', productionLine: '', siteConstraints: '', parameters: [] as Param[], equipment: [] as Equip[], reason: '' };
}
async function load() {
  failure.value = '';
  try {
    brief.value = await app.api<Brief>(`/api/v1/projects/${props.projectId}/technical-brief`);
  } catch (e) {
    failure.value = recovery(e).message;
  }
}
onMounted(load);
function startEdit() {
  const b = brief.value!;
  form.value = { version: b.version, status: b.status === 'NOT_STARTED' ? 'COLLECTING' : b.status, projectNature: b.projectNature, scenario: b.scenario, workshop: b.workshop, productionLine: b.productionLine, siteConstraints: b.siteConstraints,
    parameters: b.parameters.map((x) => ({ ...x })), equipment: b.equipment.map((x) => ({ ...x })), reason: '' };
  formError.value = '';
  editing.value = true;
}
const num = (v: unknown) => (v === '' || v == null || Number.isNaN(Number(v)) ? null : Number(v));
async function save() {
  const f = form.value;
  if (f.parameters.some((p) => !p.key.trim())) return (formError.value = '每个技术参数都需要填写名称');
  if (f.equipment.some((e) => !e.equipmentType.trim())) return (formError.value = '每台设备都需要填写类型');
  formError.value = '';
  try {
    brief.value = await app.api<Brief>(`/api/v1/projects/${props.projectId}/technical-brief`, 'PUT', {
      ...f,
      equipment: f.equipment.map((e) => ({ ...e, quantity: num(e.quantity), capacityTons: num(e.capacityTons), spanM: num(e.spanM), liftHeightM: num(e.liftHeightM) })),
    });
  } catch (e) {
    const r = recovery(e);
    if (r.kind === 'conflict') {
      await load();
      throw new Error('技术需求已被其他人更新（已重新读取最新版本 ' + brief.value?.version + '），请核对后再保存，你的输入仍保留。');
    }
    throw new Error(r.message);
  }
  editing.value = false;
  app.toast('技术需求已保存为版本 ' + brief.value?.version);
}
// Questions
const asking = ref(false),
  question = ref(''),
  answering = ref<Question | null>(null),
  answer = ref('');
async function ask() {
  if (!question.value.trim()) throw new Error('请填写待澄清问题');
  brief.value = await app.api<Brief>(`/api/v1/projects/${props.projectId}/technical-brief/questions`, 'POST', { question: question.value });
  asking.value = false;
  question.value = '';
  app.toast('澄清问题已记录');
}
async function act(q: Question, action: 'answer' | 'close' | 'reopen') {
  try {
    brief.value = await app.api<Brief>(`/api/v1/projects/${props.projectId}/technical-brief/questions/${q.id}/${action}`, 'POST', { answer: answer.value, version: q.version });
  } catch (e) {
    const r = recovery(e);
    if (r.kind === 'conflict') await load();
    throw new Error(r.message);
  }
  answering.value = null;
  answer.value = '';
}
const openCount = computed(() => brief.value?.questions.filter((q) => q.status === 'OPEN').length ?? 0);
</script>
<template>
  <section class="technical" aria-label="技术需求与澄清">
    <p v-if="failure" class="form-alert" role="alert">{{ failure }} <ActionButton action-id="technical.reload" :run="load">重新加载</ActionButton></p>
    <template v-if="brief">
      <div class="technical-head">
        <h2>技术需求 <span class="badge">{{ statuses[brief.status] }}</span><small v-if="brief.version" class="muted"> 版本 {{ brief.version }} · {{ brief.updatedByName }} · {{ when(brief.updatedAt) }}</small></h2>
        <ActionButton v-if="brief.actions['technical.edit']?.allowed" action-id="technical.edit" :run="startEdit">{{ brief.version ? '更新技术需求' : '补录技术需求' }}</ActionButton>
      </div>
      <p v-if="!brief.version" class="muted">技术交流或现场勘察后补录场景、参数与现场约束即可；正式报备时不要求全部填写。</p>
      <dl v-else class="detail-grid">
        <dt>项目性质</dt><dd>{{ natures[brief.projectNature] }}</dd>
        <dt>使用场景</dt><dd class="prewrap">{{ brief.scenario || '待补充' }}</dd>
        <dt>车间 / 产线</dt><dd>{{ [brief.workshop, brief.productionLine].filter(Boolean).join(' / ') || '待补充' }}</dd>
        <dt>现场约束</dt><dd class="prewrap">{{ brief.siteConstraints || '待补充' }}</dd>
        <dt>关键参数</dt><dd>{{ brief.parameters.map((p) => p.key + '：' + p.value + (p.unit ? ' ' + p.unit : '')).join('；') || '待补充' }}</dd>
        <dt>设备清单（补录）</dt><dd>{{ brief.equipment.map((e) => e.equipmentType + (e.quantity != null ? ' ×' + e.quantity : '') + (e.capacityTons != null ? ' ' + e.capacityTons + 't' : '') + (e.spanM != null ? ' 跨度' + e.spanM + 'm' : '')).join('；') || '待补充' }}</dd>
      </dl>
      <p class="muted small">报备时填写的设备与需求保持原样（{{ brief.filed.plannedEquipment.map((e) => e.equipmentType + (e.quantity != null ? '×' + e.quantity : '')).join('、') || '无' }}），补录内容单独留版本记录，不改写历史提交。</p>

      <h3>待澄清问题 <small class="muted">待答复 {{ openCount }}</small></h3>
      <ActionButton v-if="brief.actions['technical.ask']?.allowed" action-id="technical.ask" :run="() => { asking = true; }"><Plus :size="14" aria-hidden="true" />新增问题</ActionButton>
      <p v-if="!brief.questions.length" class="muted">暂无待澄清问题。</p>
      <ol class="question-list">
        <li v-for="q in brief.questions" :key="q.id" :class="'q-' + q.status.toLowerCase()">
          <div><span class="badge">{{ qStatus[q.status] }}</span> <strong>{{ q.question }}</strong></div>
          <small class="muted">{{ q.askedByName }} 提出 · {{ when(q.createdAt) }} · 对应技术需求版本 {{ q.briefVersion }}</small>
          <p v-if="q.answer" class="prewrap">答复（{{ q.answeredByName }}）：{{ q.answer }}</p>
          <div class="actions">
            <ActionButton v-if="q.canAnswer" action-id="technical.answer" :run="() => { answering = q; answer = q.answer; }">答复</ActionButton>
            <ActionButton v-if="q.canClose" action-id="technical.close" :run="() => act(q, q.status === 'CLOSED' ? 'reopen' : 'close')">{{ q.status === 'CLOSED' ? '重新打开' : '关闭' }}</ActionButton>
          </div>
        </li>
      </ol>
      <h3>需求版本记录</h3>
      <p v-if="!brief.revisions.length" class="muted">尚无版本。</p>
      <ol class="plain-list"><li v-for="r in brief.revisions" :key="r.version">版本 {{ r.version }} · {{ r.actorName }} · {{ when(r.at) }}<template v-if="r.reason"> · {{ r.reason }}</template></li></ol>
    </template>

    <Modal :open="editing" title="技术需求" description="保存后形成新版本；如他人已更新会提示冲突，你的输入保留。" sheet @update:open="(v) => (editing = v)">
      <form class="staff-form technical-form" novalidate @submit.prevent="save">
        <p v-if="formError" class="form-alert" role="alert">{{ formError }}</p>
        <label>技术状态<select v-model="form.status" name="techStatus"><option value="COLLECTING">收集中</option><option value="CLARIFIED">已澄清</option></select></label>
        <label>项目性质<select v-model="form.projectNature" name="projectNature"><option v-for="(l, k) in natures" :key="k" :value="k">{{ l }}</option></select></label>
        <label class="wide">使用场景<textarea v-model="form.scenario" name="scenario" maxlength="2000" rows="2"></textarea></label>
        <label>车间<input v-model="form.workshop" name="techWorkshop" maxlength="200" /></label>
        <label>产线<input v-model="form.productionLine" name="productionLine" maxlength="200" /></label>
        <label class="wide">现场约束<textarea v-model="form.siteConstraints" name="siteConstraints" maxlength="4000" rows="2"></textarea></label>
        <fieldset class="wide">
          <legend>关键参数（选填）</legend>
          <div v-for="(p, i) in form.parameters" :key="'p' + i" class="tech-row">
            <input v-model="p.key" :aria-label="'参数名称 ' + (i + 1)" placeholder="名称，如定位精度" maxlength="60" />
            <input v-model="p.value" :aria-label="'参数数值 ' + (i + 1)" placeholder="数值" maxlength="200" />
            <input v-model="p.unit" :aria-label="'参数单位 ' + (i + 1)" placeholder="单位" maxlength="20" />
            <ActionButton action-id="technical.row-remove" :aria-label="'删除参数 ' + (i + 1)" :run="() => { form.parameters.splice(i, 1); }"><Trash2 :size="14" aria-hidden="true" /></ActionButton>
          </div>
          <ActionButton action-id="technical.row-add" :run="() => { form.parameters.push({ key: '', value: '', unit: '' }); }"><Plus :size="14" aria-hidden="true" />添加参数</ActionButton>
        </fieldset>
        <fieldset class="wide">
          <legend>设备补录（选填）</legend>
          <div v-for="(e, i) in form.equipment" :key="'e' + i" class="tech-row">
            <input v-model="e.equipmentType" :aria-label="'设备类型 ' + (i + 1)" placeholder="设备类型" maxlength="60" />
            <input v-model="e.quantity" :aria-label="'数量 ' + (i + 1)" placeholder="数量" inputmode="numeric" />
            <input v-model="e.capacityTons" :aria-label="'起重量 ' + (i + 1)" placeholder="起重量 t" inputmode="decimal" />
            <input v-model="e.spanM" :aria-label="'跨度 ' + (i + 1)" placeholder="跨度 m" inputmode="decimal" />
            <ActionButton action-id="technical.row-remove" :aria-label="'删除设备 ' + (i + 1)" :run="() => { form.equipment.splice(i, 1); }"><Trash2 :size="14" aria-hidden="true" /></ActionButton>
          </div>
          <ActionButton action-id="technical.row-add" :run="() => { form.equipment.push({ equipmentType: '', quantity: null, capacityTons: null, spanM: null, liftHeightM: null, notes: '' }); }"><Plus :size="14" aria-hidden="true" />添加设备</ActionButton>
        </fieldset>
        <label class="wide">本次修改说明（选填）<input v-model="form.reason" name="techReason" maxlength="600" /></label>
        <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { editing = false; }">取消</ActionButton><ActionButton action-id="technical.save" primary :run="save">保存新版本</ActionButton></div>
      </form>
    </Modal>
    <Modal :open="asking" title="新增待澄清问题" description="问题会记录提出人和对应的技术需求版本。" @update:open="(v) => (asking = v)">
      <form class="staff-form" novalidate @submit.prevent="ask">
        <label>问题 <span class="req" aria-hidden="true">*</span><textarea v-model="question" name="techQuestion" maxlength="2000" rows="3" aria-required="true"></textarea></label>
        <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { asking = false; }">取消</ActionButton><ActionButton action-id="technical.ask-confirm" primary :run="ask">记录问题</ActionButton></div>
      </form>
    </Modal>
    <Modal :open="!!answering" title="答复澄清问题" @update:open="(v) => { if (!v) answering = null; }">
      <form v-if="answering" class="staff-form" novalidate @submit.prevent="() => act(answering!, 'answer')">
        <p>{{ answering.question }}</p>
        <label>答复 <span class="req" aria-hidden="true">*</span><textarea v-model="answer" name="techAnswer" maxlength="4000" rows="3" aria-required="true"></textarea></label>
        <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { answering = null; }">取消</ActionButton><ActionButton action-id="technical.answer-confirm" primary :run="() => act(answering!, 'answer')">保存答复</ActionButton></div>
      </form>
    </Modal>
  </section>
</template>
