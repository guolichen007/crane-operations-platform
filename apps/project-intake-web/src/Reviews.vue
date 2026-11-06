<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { useApp } from './store';
import { hintReason, recovery, RESTRICTED_RELATED_MESSAGE } from './reasons';
import { type Project, type RestrictedRelated, statuses, stages, sources, confidences, when } from './model';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
type Eligibility = 'reviewable' | 'blocked' | 'all';
const pageSize = 20;
function eligibilityFrom(v: unknown): Eligibility {
  return v === 'blocked' || v === 'all' ? v : 'reviewable';
}
interface Case {
  id: string;
  score: number;
  risk: string;
  signals: string[];
  decision: string;
  candidate?: Project;
  evidenceGrade?: 'COMPLETE_VERIFIED' | 'LEGACY_PARTIAL_EVIDENCE';
}
const app = useApp(),
  route = useRoute(),
  router = useRouter(),
  items = ref<Project[]>([]),
  total = ref(0),
  page = ref(1),
  kind = ref(route.path === '/duplicates' ? 'duplicates' : 'pending'),
  eligibility = ref<Eligibility>(eligibilityFrom(route.query.eligibility)),
  counts = ref<Record<Eligibility, number> | null>(null),
  asOf = ref(''),
  loadState = ref<'loading' | 'ready' | 'error'>('loading'),
  loadFailure = ref(''),
  openFailure = ref(''),
  decideFailure = ref(''),
  selected = ref<Project | null>(null),
  cases = ref<Case[]>([]),
  restricted = ref<RestrictedRelated | null>(null),
  operation = ref(''),
  caseId = ref(''),
  reason = ref(''),
  acknowledge = ref(false);
const legacyPending = computed(() => cases.value.some((c) => c.decision === 'PENDING' && c.evidenceGrade === 'LEGACY_PARTIAL_EVIDENCE'));
const comparison = computed(() => route.path === '/duplicates');
// The pending queue is filtered on the server by review eligibility (G3-2B);
// duplicates/protection queues keep their original semantics.
const usesEligibility = computed(() => !comparison.value && kind.value === 'pending');
let loadSeq = 0,
  openSeq = 0;
async function load() {
  const seq = ++loadSeq;
  loadState.value = 'loading';
  loadFailure.value = '';
  const query = usesEligibility.value
    ? `kind=pending&eligibility=${eligibility.value}&page=${page.value}&pageSize=${pageSize}`
    : `kind=${kind.value}&page=${page.value}`;
  try {
    const b = await app.api<{ items: Project[]; total: number; counts?: Record<Eligibility, number>; asOf?: string }>(`/api/v1/reviews?${query}`);
    // A slower response for an earlier view must not overwrite the current one.
    if (seq !== loadSeq) return;
    items.value = b.items;
    total.value = b.total;
    counts.value = b.counts ?? null;
    asOf.value = b.asOf ?? '';
    loadState.value = 'ready';
  } catch (e) {
    if (seq !== loadSeq) return;
    items.value = [];
    loadFailure.value = recovery(e).message;
    loadState.value = 'error';
  }
}
onMounted(load);
function chooseEligibility(next: Eligibility) {
  if (next === eligibility.value) return;
  void router.replace({ query: { ...route.query, eligibility: next } });
}
watch(
  () => route.query.eligibility,
  (v) => {
    const next = eligibilityFrom(v);
    if (next === eligibility.value) return;
    eligibility.value = next;
    page.value = 1;
    void load();
  }
);
async function open(p: Project) {
  const seq = ++openSeq;
  // Clear first so nothing from a previous project is shown while loading or after an error.
  selected.value = null;
  cases.value = [];
  restricted.value = null;
  openFailure.value = '';
  try {
    const b = await app.api<{ project: Project; cases: Case[]; restrictedRelated: RestrictedRelated | null }>(
      `/api/v1/projects/${p.id}/duplicate-comparison`
    );
    // Rapidly opening another project must not show this (older) result.
    if (seq !== openSeq) return;
    cases.value = b.cases;
    restricted.value = b.restrictedRelated;
    selected.value = b.project;
  } catch (e) {
    if (seq !== openSeq) return;
    const r = recovery(e);
    openFailure.value = r.message;
    if (r.kind === 'missing' || r.kind === 'conflict') void load();
  }
}
const viewLabels: Record<Eligibility, string> = { reviewable: '我可审核', blocked: '需要其他人处理', all: '全部待审' };
const emptyText: Record<Eligibility, string> = {
  reviewable: '当前没有你可以立即审核的报备。',
  blocked: '没有需要其他人处理的报备。',
  all: '范围内没有待审核的报备。',
};
function nextStep(p: Project) {
  if (!p.review) return '待审核';
  return p.review.nextAction === 'duplicate-decisions' ? '完成重复裁决' : '审核：批准、退回或拒绝';
}
// Waiting time only from the frozen submission time; never from updatedAt.
function waiting(p: Project) {
  if (!p.submittedAt) return '提交时间未记录';
  const ms = (asOf.value ? Date.parse(asOf.value) : Date.now()) - Date.parse(p.submittedAt);
  const hours = Math.max(0, Math.floor(ms / 3_600_000));
  if (hours < 1) return '等待不足 1 小时';
  if (hours < 48) return `已等待 ${hours} 小时`;
  return `已等待 ${Math.floor(hours / 24)} 天`;
}
function prompt(action: string, id = '') {
  operation.value = action;
  decideFailure.value = '';
  caseId.value = id;
  reason.value = '';
  acknowledge.value = false;
}
async function decide() {
  if (!selected.value) return;
  let endpoint = operation.value;
  const body: Record<string, unknown> = { version: selected.value.version, reason: reason.value };
  if (operation.value.startsWith('duplicate.')) {
    endpoint = 'duplicate-decisions';
    body.decision =
      operation.value === 'duplicate.confirm' ? 'CONFIRMED_DUPLICATE' : 'DISTINCT_APPROVED';
    body.caseId = caseId.value;
    if (operation.value === 'duplicate.dismiss' && legacyPending.value) body.acknowledgeLegacyEvidence = acknowledge.value;
    body.relatedVersions = Object.fromEntries(cases.value.filter((c) => c.candidate).map((c) => [c.candidate!.id, c.candidate!.version]));
  }
  try {
    await app.api(`/api/v1/projects/${selected.value.id}/${endpoint}`, 'POST', body);
  } catch (e) {
    const r = recovery(e);
    // Stale list or button state: refresh the queue; the server decision stands.
    if (r.kind === 'conflict' || r.kind === 'missing' || r.kind === 'forbidden') void load();
    // Shown inside the dialog: the page banner is behind the modal. Input is kept.
    decideFailure.value = r.message;
    throw new Error(r.message);
  }
  operation.value = '';
  selected.value = null;
  restricted.value = null;
  app.toast('审核结论已保存，列表已更新');
  await load();
}
const decisionLabels:Record<string,string>={PENDING:'待处理',CONFIRMED_DUPLICATE:'已确认重复',DISTINCT_APPROVED:'已排除重复'};
const protectionLabels:Record<string,string>={ACTIVE:'有效',WARNING:'需要跟进',EXPIRING:'即将到期',EXPIRED:'需主管复核'};
async function closeOperation(){if(reason.value.trim()&&!(await app.confirmDiscard()))return;operation.value=''}
onBeforeRouteLeave(async()=>!operation.value||!reason.value.trim()||await app.confirmDiscard());
const projectTypes:Record<string,string>={RETROFIT:'存量改造',NEW_BUILD:'新建',EXPANSION:'扩建'};const equipmentTypes:Record<string,string>={BRIDGE:'桥式行车',GANTRY:'门式行车',OTHER:'其他'};
const fields = [
  ['客户', (p: Project) => p.customer],
  ['项目名称', (p: Project) => p.projectName],
  ['省市', (p: Project) => p.location.province + ' ' + p.location.city],
  ['厂区', (p: Project) => p.location.factoryName],
  ['车间', (p: Project) => p.location.workshop],
  ['项目类型', (p: Project) => projectTypes[p.projectType]||'待确认'],
  [
    '设备类型/数量',
    (p: Project) => p.plannedEquipment.map((e) => (equipmentTypes[e.equipmentType]||'其他设备') + ' × ' + e.quantity).join('、'),
  ],
  ['需求', (p: Project) => p.requirementNotes],
  ['销售阶段', (p: Project) => stages[p.salesStage]],
  ['信息来源', (p: Project) => sources[p.informationSource]],
  ['可信度', (p: Project) => confidences[p.confidenceLevel]],
  ['报备时间', (p: Project) => when(p.createdAt)],
] as const;
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>
        {{ comparison ? '重复处理' : '报备审核' }} <small>{{ total }}</small>
      </h1>
      <p class="muted">核验信息与归属，保留每一次决策的依据。<template v-if="usesEligibility && asOf">统计时间 {{ when(asOf) }}</template></p>
    </div>
    <label v-if="!comparison">审核队列<select
      v-model="kind"
      aria-label="审核队列"
      @change="
        page = 1;
        load();
      "
    >
      <option value="pending">待审核</option>
      <option value="duplicates">疑似重复</option>
      <option value="protection">保护资格待复核</option>
    </select></label>
  </div>
  <div v-if="usesEligibility" class="segmented queue-views" role="tablist" aria-label="审核资格">
    <ActionButton
      v-for="v in (['reviewable', 'blocked', 'all'] as const)"
      :key="v"
      action-id="review.view"
      role="tab"
      :aria-selected="eligibility === v"
      :class="{ selected: eligibility === v }"
      :run="() => chooseEligibility(v)"
    >
      {{ viewLabels[v] }}<span class="count">{{ counts ? counts[v] : '…' }}</span>
    </ActionButton>
  </div>
  <p v-if="openFailure" class="form-alert" role="alert">{{ openFailure }}</p>
  <p v-if="loadState === 'loading'" class="muted" role="status" aria-busy="true">正在加载审核队列…</p>
  <div v-else-if="loadState === 'error'" class="form-alert" role="alert">
    无法加载审核队列：{{ loadFailure }}
    <ActionButton action-id="project.reload" :run="load">重新加载</ActionButton>
  </div>
  <template v-else>
    <div v-if="usesEligibility" class="review-cards">
      <ActionButton
        v-for="p in items"
        :key="p.id"
        action-id="review.open"
        :class="['review-card', { blocked: !p.review?.eligible }]"
        :run="() => open(p)"
      >
        <span class="review-card-head"><strong>{{ p.projectName }}</strong><span class="mono">{{ p.registrationNo }}</span></span>
        <span class="review-card-meta"><span class="badge">{{ statuses[p.intakeStatus] }}</span><span>{{ waiting(p) }}</span></span>
        <span class="review-card-next">下一步：{{ nextStep(p) }}</span>
        <span :class="['review-card-who', { ok: p.review?.eligible }]">
          {{ p.review?.eligible ? '你可以审核' : '需要其他人处理：' + hintReason({ allowed: false, reasonCode: p.review?.reasonCode ?? null }) }}
        </span>
      </ActionButton>
    </div>
    <div v-else class="review-list">
      <ActionButton v-for="p in items" :key="p.id" action-id="review.open" :run="() => open(p)">
        <span class="mono">{{ p.registrationNo }}</span><strong>{{ p.projectName }}</strong><span class="muted">{{ p.customer }} · {{ p.location.city }}</span><span class="badge">{{ statuses[p.intakeStatus] }}</span><span class="badge">保护期 {{ protectionLabels[p.protectionStatus] }}</span>
      </ActionButton>
    </div>
    <p v-if="!items.length" class="empty muted">{{ usesEligibility ? emptyText[eligibility] : '当前没有需要处理的报备。' }}</p>
  </template>
  <div class="pagination">
    <span>{{ total }} 项 · 第 {{ page }} 页</span><ActionButton
      action-id="project.page-prev"
      :disabled="page === 1"
      reason="已是第一页"
      :run="
        async () => {
          page--;
          await load();
        }
      "
    >
      上一页
    </ActionButton><ActionButton
      action-id="project.page-next"
      :disabled="page * pageSize >= total"
      reason="没有下一页"
      :run="
        async () => {
          page++;
          await load();
        }
      "
    >
      下一页
    </ActionButton>
  </div>
  <Modal
    :open="!!selected"
    title="报备审核与重复对比"
    description="逐项核验候选，跨团队信息由销售总监处理。"
    sheet
    @update:open="selected = null; restricted = null"
  >
    <template v-if="selected">
      <h2>{{ selected.projectName }}</h2>
      <p class="muted">{{ selected.registrationNo }} · 负责人 {{ selected.ownerName || '姓名待核对' }}</p>
      <p v-if="selected.review && !selected.review.eligible" class="review-eligibility" role="status">{{ hintReason({ allowed: false, reasonCode: selected.review.reasonCode }) }}</p>
      <p v-if="restricted" class="restricted-related note" role="note" data-testid="restricted-related">{{ RESTRICTED_RELATED_MESSAGE }}</p>
      <template v-for="c in cases" :key="c.id">
        <div class="note">
          {{ c.risk==='HIGH'?'高风险':'疑似重复' }} · {{ c.score }} 分 · {{ c.signals.join(' / ') || '历史候选' }} ·
          {{ decisionLabels[c.decision]||'已处理' }}
        </div>
        <p v-if="c.evidenceGrade === 'LEGACY_PARTIAL_EVIDENCE'" class="legacy-evidence" role="note">
          该候选来自历史数据，提交与参与记录不完整。系统只能排除已知参与人；“未发现参与记录”不等于已证明你未参与过该项目。
        </p>
        <div v-if="c.candidate" class="comparison-grid">
          <div class="comparison-heading">新报备</div>
          <div class="comparison-heading">已有报备</div>
          <template v-for="[label, value] in fields" :key="label">
            <div>
              <small class="muted">{{ label }}</small>
              <p>{{ value(selected) || '—' }}</p>
            </div>
            <div>
              <small class="muted">{{ label }}</small>
              <p>{{ value(c.candidate) || '—' }}</p>
            </div>
          </template>
        </div>
        <div v-if="c.decision === 'PENDING'" class="actions">
          <ActionButton
            action-id="duplicate.confirm"
            :disabled="!!restricted || !selected.actions?.['duplicate-decisions']?.allowed"
            :reason="hintReason(selected.actions?.['duplicate-decisions'])"
            :run="() => prompt('duplicate.confirm', c.id)"
          >
            确认重复
          </ActionButton><ActionButton
            action-id="duplicate.dismiss"
            primary
            :disabled="!!restricted || !selected.actions?.['duplicate-decisions']?.allowed"
            :reason="hintReason(selected.actions?.['duplicate-decisions'])"
            :run="() => prompt('duplicate.dismiss', c.id)"
          >
            不是重复并批准
          </ActionButton>
        </div>
      </template>
      <dl v-if="!cases.length">
        <dt>客户</dt>
        <dd>{{ selected.customer }}</dd>
        <dt>主要需求</dt>
        <dd>{{ selected.requirementNotes }}</dd>
        <dt>下一步复核</dt>
        <dd>{{ when(selected.nextReviewAt) }}</dd>
      </dl>
      <div class="actions modal-actions">
        <ActionButton
          action-id="review.approve"
          primary
          :disabled="!selected.actions?.approve?.allowed"
          :reason="hintReason(selected.actions?.approve)"
          :run="() => prompt('approve')"
        >
          批准
        </ActionButton><ActionButton
          action-id="review.return"
          :disabled="!selected.actions?.return?.allowed"
          :reason="hintReason(selected.actions?.return)"
          :run="() => prompt('return')"
        >
          退回补充
        </ActionButton><ActionButton
          action-id="review.reject"
          :disabled="!selected.actions?.reject?.allowed"
          :reason="hintReason(selected.actions?.reject)"
          :run="() => prompt('reject')"
        >
          拒绝
        </ActionButton><ActionButton
          action-id="project.detail"
          :run="() => router.push('/projects/' + selected?.id)"
        >
          完整详情
        </ActionButton><ActionButton
          action-id="project.preview-close"
          :run="
            () => {
              selected = null;
            }
          "
        >
          关闭
        </ActionButton>
      </div>
    </template>
  </Modal>
  <Modal
    :open="!!operation"
    title="确认审核结论"
    :description="
      operation === 'duplicate.dismiss'
        ? '此操作会将全部待处理候选标记为非重复并批准；请确认已逐一核验。'
        : '请填写原因。结论将记录操作者、时间和审计历史。'
    "
    @update:open="closeOperation"
  >
    <p v-if="decideFailure" class="form-alert" role="alert">{{ decideFailure }}<template v-if="decideFailure.includes('其他人更新')">。审核队列已刷新，请关闭后重新打开该项目。</template></p>
    <label v-if="operation === 'duplicate.dismiss' && legacyPending" class="legacy-ack"><input v-model="acknowledge" type="checkbox" name="acknowledgeLegacyEvidence" />我已核实：历史候选证据不完整，我确认与其无利益关联并对“不是重复并批准”的结论负责。确认内容会写入审计。</label>
    <label>审核原因 *<textarea
      v-model="reason"
      name="reviewReason"
      maxlength="1000"
      rows="3"
    ></textarea>
    </label>
    <div class="actions modal-actions">
      <ActionButton
        action-id="dialog.cancel"
        :run="closeOperation"
      >
        取消
      </ActionButton><ActionButton
        action-id="review.confirm"
        primary
        :disabled="!reason.trim() || (operation === 'duplicate.dismiss' && legacyPending && !acknowledge)"
        :reason="!reason.trim() ? '请填写审核原因' : '请确认历史证据不完整的责任声明'"
        :run="decide"
      >
        确认并保存结论
      </ActionButton>
    </div>
  </Modal>
</template>
