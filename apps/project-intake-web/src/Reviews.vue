<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { useApp } from './store';
import { hintReason, recovery, RESTRICTED_RELATED_MESSAGE } from './reasons';
import { type Project, type RestrictedRelated, statuses, stages, sources, confidences, when } from './model';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
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
  selected = ref<Project | null>(null),
  cases = ref<Case[]>([]),
  restricted = ref<RestrictedRelated | null>(null),
  operation = ref(''),
  caseId = ref(''),
  reason = ref(''),
  acknowledge = ref(false);
const legacyPending = computed(() => cases.value.some((c) => c.decision === 'PENDING' && c.evidenceGrade === 'LEGACY_PARTIAL_EVIDENCE'));
const comparison = computed(() => route.path === '/duplicates');
async function load() {
  const b = await app.api<{ items: Project[]; total: number }>(
    `/api/v1/reviews?kind=${kind.value}&page=${page.value}`
  );
  items.value = b.items;
  total.value = b.total;
}
onMounted(() => load().catch((e) => (app.error = e.message)));
async function open(p: Project) {
  // Clear first so nothing from a previous project is shown while loading or after an error.
  selected.value = null;
  cases.value = [];
  restricted.value = null;
  const b = await app.api<{ project: Project; cases: Case[]; restrictedRelated: RestrictedRelated | null }>(
    `/api/v1/projects/${p.id}/duplicate-comparison`
  );
  cases.value = b.cases;
  restricted.value = b.restrictedRelated;
  selected.value = b.project;
}
function prompt(action: string, id = '') {
  operation.value = action;
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
    throw new Error(recovery(e).message);
  }
  operation.value = '';
  selected.value = null;
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
      <p class="muted">核验信息与归属，保留每一次决策的依据。</p>
    </div>
    <label v-if="!comparison">审核队列<select
      v-model="kind"
      aria-label="审核队列"
      @change="
        page = 1;
        load().catch((e) => (app.error = e.message));
      "
    >
      <option value="pending">待审核</option>
      <option value="duplicates">疑似重复</option>
      <option value="protection">保护资格待复核</option>
    </select></label>
  </div>
  <div class="review-list">
    <ActionButton v-for="p in items" :key="p.id" action-id="review.open" :run="() => open(p)">
      <span class="mono">{{ p.registrationNo }}</span><strong>{{ p.projectName }}</strong><span class="muted">{{ p.customer }} · {{ p.location.city }}</span><span class="badge">{{ statuses[p.intakeStatus] }}</span><span class="badge">保护期 {{ protectionLabels[p.protectionStatus] }}</span>
    </ActionButton>
  </div>
  <p v-if="!items.length" class="empty muted">当前没有需要处理的报备。</p>
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
      :disabled="page * 20 >= total"
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
