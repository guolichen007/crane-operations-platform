<script setup lang="ts">
import { ref, reactive, onMounted, computed, onBeforeUnmount } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { Plus, Pencil, ArrowLeft, Send } from '@lucide/vue';
import { useApp } from './store';
import {
  type Project,
  type Activity,
  type ActionHint,
  statuses,
  stages,
  reqs,
  activityTypes,
  sources,
  confidences,
  equipmentTypes,
  controlModes,
  environments,
  projectTypes,
  when,
} from './model';
import { projectScopeLabel } from './navigation';
import { hintReason, recovery } from './reasons';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
const protectionLabels: Record<string, string> = { NOT_STARTED: '未开始', ACTIVE: '有效', WARNING: '需要跟进', EXPIRING: '即将到期', EXPIRED: '需主管复核' };
const auditLabels: Record<string, string> = {
  'project.create': '创建报备',
  'project.create_unassigned': '创建待分配草稿',
  'project.edit': '更新草稿',
  'project.submit': '正式提交',
  'activity.create': '记录跟进',
  'project.stage-change': '修改销售阶段',
  'ownership.transfer': '转移归属',
  'protection.sweep': '保护资格复核',
  'review.approve': '批准报备',
  'review.return': '退回补充',
  'review.reject': '拒绝报备',
  'duplicate.confirm': '确认重复',
  'duplicate.dismiss': '排除重复并批准',
  APPROVE: '批准',
  RETURN: '退回补充',
  REJECT: '拒绝',
  CONFIRMED_DUPLICATE: '确认重复',
  DISTINCT_APPROVED: '排除重复并批准',
};
const zones = ['项目概要', '设备与技术需求', '跟进时间线', '审批和重复风险', '归属历史', '操作审计'] as const;
const app = useApp(),
  route = useRoute(),
  router = useRouter(),
  p = ref<Project | null>(null),
  failure = ref(''),
  tab = ref<(typeof zones)[number]>('项目概要'),
  activityOpen = ref(false),
  activity = reactive<Activity>({
    type: 'PHONE',
    occurredAt: new Date(Date.now() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16),
    summary: '',
    result: '',
    nextAction: '',
    nextFollowupAt: '',
  });
const governanceOpen = ref(''),
  governanceReason = ref(''),
  target = ref(''),
  targetStage = ref(''),
  candidates = ref<{ subject: string; name: string }[]>([]);
const can = (action: string) => !!p.value?.actions?.[action]?.allowed;
const shown = (action: string) => !!p.value?.actions?.[action] && (can(action) || p.value.actions[action].reasonCode !== 'BUSINESS_PERMISSION_DENIED');
const reason = (action: string) => hintReason(p.value?.actions?.[action] as ActionHint | undefined);
const governanceTitles: Record<string, string> = {
  'ownership-transfer': '指派或转移负责人',
  'stage-change': '修改销售阶段',
  approve: '批准报备',
  return: '退回补充',
  reject: '拒绝报备',
};
async function openGovernance(action: string) {
  governanceReason.value = '';
  target.value = '';
  targetStage.value = '';
  if (action === 'ownership-transfer' && p.value) {
    const b = await app.api<{ items: { subject: string; name: string }[] }>(`/api/v1/ownership-candidates?projectId=${p.value.id}`);
    candidates.value = b.items.filter((c) => c.subject !== p.value?.ownerSubject);
  }
  governanceOpen.value = action;
}
async function closeGovernance() {
  if (governanceReason.value.trim() && !(await app.confirmDiscard())) return;
  governanceOpen.value = '';
}
const governanceReady = computed(() => {
  if (!governanceReason.value.trim()) return false;
  if (governanceOpen.value === 'ownership-transfer') return !!target.value;
  if (governanceOpen.value === 'stage-change') return !!targetStage.value;
  return true;
});
async function saveGovernance() {
  if (!p.value) return;
  const action = governanceOpen.value;
  const body: Record<string, unknown> = { version: p.value.version, reason: governanceReason.value };
  if (action === 'ownership-transfer') body.targetSubject = target.value;
  if (action === 'stage-change') body.salesStage = targetStage.value;
  try {
    await app.api(`/api/v1/projects/${p.value.id}/${action}`, 'POST', body);
  } catch (e) {
    throw new Error(recovery(e).message);
  }
  governanceOpen.value = '';
  await load();
  app.toast(action === 'ownership-transfer' ? '负责人已更新，操作已记录' : ['approve', 'return', 'reject'].includes(action) ? '审核结论已保存' : '项目已更新，操作已记录');
}
const ownerText = computed(() => {
  if (!p.value) return '';
  if (!p.value.ownerSubject) return '待分配';
  if (p.value.ownerSubject === app.user?.subject) return '我';
  return p.value.ownerName || '姓名待核对';
});
async function load() {
  try {
    p.value = await app.api<Project>('/api/v1/projects/' + route.params.id);
    failure.value = '';
  } catch (e) {
    failure.value = recovery(e).message;
  }
}
onMounted(load);
async function submit() {
  if (!p.value) return;
  try {
    const b = await app.api<Project>('/api/v1/projects/' + p.value.id + '/submit', 'POST', { version: p.value.version });
    app.toast(b.intakeStatus === 'UNDER_REVIEW' ? '已正式报备，疑似重复待主管确认' : '已正式报备，等待审核');
  } catch (e) {
    throw new Error(recovery(e).message);
  }
  await load();
}
async function record() {
  if (!p.value) return;
  try {
    await app.api('/api/v1/projects/' + p.value.id + '/activities', 'POST', {
      ...activity,
      version: p.value.version,
      occurredAt: new Date(activity.occurredAt).toISOString(),
      nextFollowupAt: activity.nextFollowupAt ? new Date(activity.nextFollowupAt).toISOString() : '',
    });
  } catch (e) {
    throw new Error(recovery(e).message);
  }
  activityOpen.value = false;
  activity.summary = '';
  activity.result = '';
  activity.nextAction = '';
  activity.nextFollowupAt = '';
  await load();
  tab.value = '跟进时间线';
  app.toast('跟进已记录');
}
async function closeActivity() {
  if (activity.summary || activity.result || activity.nextAction) {
    if (!(await app.confirmDiscard())) return;
  }
  activityOpen.value = false;
}
const activityDirty = computed(() => activityOpen.value && !!(activity.summary || activity.result || activity.nextAction));
onBeforeRouteLeave(async () => !(activityDirty.value || (governanceOpen.value && governanceReason.value.trim())) || (await app.confirmDiscard()));
function preventUnload(e: BeforeUnloadEvent) {
  if (activityDirty.value || (governanceOpen.value && governanceReason.value.trim())) {
    e.preventDefault();
    e.returnValue = '';
  }
}
window.addEventListener('beforeunload', preventUnload);
onBeforeUnmount(() => window.removeEventListener('beforeunload', preventUnload));
const num = (v: number | null | undefined, unit: string) => (v == null ? '未知' : v.toLocaleString('zh-CN') + ' ' + unit);
const reviewSummary = computed(() => {
  const r = p.value?.review;
  if (!r) return '';
  if (r.eligible) return r.pendingDuplicates ? '你可以独立处理本项目的重复裁决。' : '你可以独立审核本项目。';
  return hintReason({ allowed: false, reasonCode: r.reasonCode });
});
</script>
<template>
  <p v-if="failure" class="form-alert" role="alert">
    {{ failure }} <ActionButton action-id="project.reload" :run="load">重新加载</ActionButton>
  </p>
  <template v-if="p">
    <ActionButton action-id="nav.open" class="back-link" :run="() => router.push('/projects')">
      <ArrowLeft :size="14" aria-hidden="true" />返回{{ projectScopeLabel(app.user) }}
    </ActionButton>
    <div class="detail-heading">
      <div>
        <div class="eyebrow">{{ p.registrationNo }}</div>
        <h1>{{ p.projectName || '未命名草稿' }}</h1>
        <div class="actions">
          <span :class="['badge', p.intakeStatus.toLowerCase()]">{{ statuses[p.intakeStatus] }}</span><span class="badge">{{ stages[p.salesStage] }}</span><span :class="['owner-tag', { unassigned: !p.ownerSubject }]">负责人 · {{ ownerText }}</span>
        </div>
      </div>
      <div class="actions">
        <ActionButton v-if="can('edit')" action-id="project.edit" :run="() => router.push('/projects/' + p?.id + '/edit')">
          <Pencil :size="14" aria-hidden="true" />编辑
        </ActionButton><ActionButton
          v-if="shown('submit')"
          action-id="project.submit"
          primary
          :disabled="!can('submit')"
          :reason="reason('submit')"
          :run="submit"
        >
          <Send :size="14" aria-hidden="true" />提交正式报备
        </ActionButton><ActionButton
          v-if="shown('activities')"
          action-id="activity.open"
          :primary="!shown('submit')"
          :disabled="!can('activities')"
          :reason="reason('activities')"
          :run="() => { activityOpen = true; }"
        >
          <Plus :size="14" aria-hidden="true" />记录跟进
        </ActionButton>
      </div>
    </div>
    <div v-if="p.intakeStatus === 'UNDER_REVIEW'" class="note">发现疑似重复，报备已进入待审核。归属需要独立审核人确认。</div>
    <div v-if="!p.ownerSubject" class="note">这是待分配草稿，只有管理员可见。指派有效销售后才能正式提交。</div>
    <div class="actions governance-bar">
      <ActionButton v-if="shown('stage-change')" action-id="project.stage-change" :disabled="!can('stage-change')" :reason="reason('stage-change')" :run="() => openGovernance('stage-change')">修改销售阶段</ActionButton><ActionButton
        v-if="shown('ownership-transfer')"
        action-id="ownership.transfer"
        :disabled="!can('ownership-transfer')"
        :reason="reason('ownership-transfer')"
        :run="() => openGovernance('ownership-transfer')"
      >
        {{ p.ownerSubject ? '转移归属' : '指派销售' }}
      </ActionButton><span class="badge">保护期 {{ protectionLabels[p.protectionStatus] || p.protectionStatus }}</span><span class="muted">下次复核 {{ when(p.nextReviewAt) }}</span>
    </div>
    <div class="tabs" role="tablist" aria-label="项目信息分区">
      <ActionButton
        v-for="t in zones"
        :key="t"
        action-id="project.tab"
        role="tab"
        :aria-selected="tab === t"
        :class="{ selected: tab === t }"
        :run="() => { tab = t; }"
      >
        {{ t }}
      </ActionButton>
    </div>
    <div class="detail-body" role="tabpanel" :aria-label="tab">
      <template v-if="tab === '项目概要'">
        <h2>项目概要</h2>
        <dl class="detail-grid">
          <dt>终端客户</dt><dd>{{ p.customer || '待填写' }}</dd>
          <dt>项目位置</dt><dd>{{ Object.values(p.location).filter(Boolean).join(' · ') || '待填写' }}</dd>
          <dt>项目来源</dt><dd>{{ sources[p.informationSource] || '暂不明确' }}</dd>
          <dt>信息可信度</dt><dd>{{ confidences[p.confidenceLevel] || '暂不明确' }}</dd>
          <dt>项目类型</dt><dd>{{ projectTypes[p.projectType] || '暂不明确' }}</dd>
          <dt>预估金额</dt><dd>{{ num(p.estimatedAmount, '元') }}<span v-if="p.knowledge?.estimatedAmount === 'LEGACY_UNCONFIRMED'" class="muted">（历史值待确认）</span></dd>
          <dt>预期采购</dt><dd>{{ p.expectedPurchaseDate || '暂不明确' }}</dd>
          <dt>下一步</dt><dd>{{ p.nextAction || '尚未记录' }}</dd>
          <dt>下次跟进</dt><dd>{{ when(p.nextFollowupAt) }}</dd>
          <dt>关系方</dt><dd>{{ p.parties.map((x) => x.name).join('、') || '暂无' }}</dd>
          <dt>补充说明</dt><dd class="prewrap">{{ p.notes || '暂无' }}</dd>
        </dl>
      </template>
      <template v-if="tab === '设备与技术需求'">
        <h2>设备与技术需求</h2>
        <div class="actions">
          <span v-for="r in p.requirements" :key="r" class="badge">{{ reqs[r] }}</span>
        </div>
        <p class="prewrap">{{ p.requirementNotes || '主要需求尚未填写' }}</p>
        <p v-if="!p.plannedEquipment.length" class="muted">暂无计划设备信息。</p>
        <table v-else class="equipment-table">
          <thead><tr><th>类型</th><th>数量</th><th>起重量</th><th>跨度</th><th>起升高度</th><th>控制方式</th><th>环境</th><th>厂家 / 型号</th></tr></thead>
          <tbody>
            <tr v-for="(eq, i) in p.plannedEquipment" :key="i">
              <td>{{ equipmentTypes[eq.equipmentType] || '其他设备' }}</td>
              <td>{{ eq.quantity == null ? '未知' : eq.quantity + ' 台' }}</td>
              <td>{{ num(eq.capacityTons, 't') }}</td>
              <td>{{ num(eq.spanM, 'm') }}</td>
              <td>{{ num(eq.liftHeightM, 'm') }}</td>
              <td>{{ controlModes[eq.controlMode || ''] || '暂不明确' }}</td>
              <td>{{ (eq.environmentFlags || []).map((k) => environments[k]).join('、') || '—' }}</td>
              <td>{{ [eq.manufacturer, eq.model].filter(Boolean).join(' ') || '—' }}</td>
            </tr>
          </tbody>
        </table>
        <p class="muted small">计划设备仅用于报备，不生成正式设备档案。</p>
      </template>
      <template v-if="tab === '跟进时间线'">
        <h2>跟进时间线</h2>
        <p v-if="!p.activities?.length" class="muted">还没有跟进。正式报备后可以记录每次沟通与下一步。</p>
        <ol class="timeline">
          <li v-for="(a, i) in p.activities" :key="i">
            <span class="muted">{{ when(a.occurredAt) }} · {{ activityTypes[a.type] }} · {{ a.actorName || '姓名待核对' }}</span>
            <h3>{{ a.summary }}</h3>
            <p v-if="a.result">{{ a.result }}</p>
            <p class="muted">下一步：{{ a.nextAction || '尚未记录' }} · {{ when(a.nextFollowupAt) }}</p>
          </li>
        </ol>
      </template>
      <template v-if="tab === '审批和重复风险'">
        <h2>审批和重复风险</h2>
        <p>当前状态：{{ statuses[p.intakeStatus] }}<template v-if="p.review?.pendingDuplicates"> · 待裁决重复候选 {{ p.review.pendingDuplicates }} 个</template></p>
        <p v-if="p.evidenceState === 'INCOMPLETE'" class="muted">此项目来自历史数据，提交证据不完整，审核需走独立核验流程。</p>
        <div v-if="p.review" :class="['review-eligibility', { ok: p.review.eligible }]" role="status">
          <strong>{{ reviewSummary }}</strong>
          <ul>
            <li>项目可读：是</li>
            <li>本轮独立性：{{ p.review.projectIndependence.allowed ? '满足' : hintReason(p.review.projectIndependence) }}</li>
            <li>相关候选：{{ p.review.relatedCandidates.allowed ? '均可见且未参与' : hintReason(p.review.relatedCandidates) }}</li>
          </ul>
        </div>
        <div class="actions">
          <ActionButton v-if="shown('approve')" action-id="review.approve" primary :disabled="!can('approve')" :reason="reason('approve')" :run="() => openGovernance('approve')">批准</ActionButton><ActionButton
            v-if="shown('return')"
            action-id="review.return"
            :disabled="!can('return')"
            :reason="reason('return')"
            :run="() => openGovernance('return')"
          >
            退回补充
          </ActionButton><ActionButton v-if="shown('reject')" action-id="review.reject" :disabled="!can('reject')" :reason="reason('reject')" :run="() => openGovernance('reject')">拒绝</ActionButton><ActionButton
            v-if="p.review?.pendingDuplicates"
            action-id="nav.open"
            :run="() => router.push('/duplicates')"
          >
            去重复处理
          </ActionButton>
        </div>
        <h3>审核记录</h3>
        <p v-if="!p.governanceHistory?.some((g) => g.action !== 'ownership.transfer')" class="muted">尚无审核结论。</p>
        <ol class="timeline">
          <li v-for="(g, i) in p.governanceHistory?.filter((g) => g.action !== 'ownership.transfer')" :key="i">
            <span class="muted">{{ when(g.at) }} · {{ g.actorName || '姓名待核对' }}</span>
            <p>{{ auditLabels[g.action] || '审核' }} · {{ g.reason }}</p>
          </li>
        </ol>
      </template>
      <template v-if="tab === '归属历史'">
        <h2>归属历史</h2>
        <dl class="detail-grid">
          <dt>当前负责人</dt><dd>{{ ownerText }}</dd>
          <dt>创建人</dt><dd>{{ p.createdByName || '姓名待核对' }}</dd>
          <dt v-if="!p.ownerSubject">草稿保管人</dt><dd v-if="!p.ownerSubject">{{ p.custodianName || '姓名待核对' }}</dd>
          <dt>本轮提交人</dt><dd>{{ p.submittedByName || (p.submissionRevision ? '姓名待核对' : '尚未正式提交') }}</dd>
        </dl>
        <ol class="timeline">
          <li v-for="(g, i) in p.governanceHistory?.filter((g) => g.action === 'ownership.transfer')" :key="i">
            <span class="muted">{{ when(g.at) }} · {{ g.actorName || '姓名待核对' }}</span>
            <p>{{ g.fromOwnerName || (g.fromOwner ? '姓名待核对' : '待分配') }} → {{ g.toOwnerName || '姓名待核对' }}</p>
            <p class="muted">{{ g.reason }}</p>
          </li>
        </ol>
      </template>
      <template v-if="tab === '操作审计'">
        <h2>操作审计</h2>
        <ol class="timeline">
          <li v-for="(a, i) in p.audit" :key="i">
            <span class="muted">{{ when(a.createdAt) }} · {{ a.actorName || (a.subject === 'maintenance' ? '系统维护' : '姓名待核对') }} · 修订 {{ a.revision }}</span>
            <p>
              {{ auditLabels[a.action] || a.action }}<span v-if="a.details.reason"> · {{ a.details.reason }}</span>
            </p>
          </li>
        </ol>
      </template>
    </div>
    <Modal
      :open="!!governanceOpen"
      :title="governanceTitles[governanceOpen] || '确认操作'"
      description="请填写依据。操作会校验当前版本并记录操作人与时间。"
      @update:open="closeGovernance"
    >
      <label v-if="governanceOpen === 'ownership-transfer'">新负责人<select v-model="target" name="targetOwner">
        <option value="">请选择</option>
        <option v-for="c in candidates" :key="c.subject" :value="c.subject">{{ c.name }}</option>
      </select></label>
      <p v-if="governanceOpen === 'ownership-transfer' && !candidates.length" class="muted">当前没有可指派的有效销售。</p>
      <label v-if="governanceOpen === 'stage-change'">目标阶段<select v-model="targetStage" name="targetStage">
        <option value="">请选择</option>
        <option v-for="(label, key) in stages" :key="key" :value="key">{{ label }}</option>
      </select></label>
      <label>操作原因<textarea v-model="governanceReason" name="governanceReason" maxlength="1000"></textarea></label>
      <div class="actions modal-actions">
        <ActionButton action-id="dialog.cancel" :run="closeGovernance">取消</ActionButton><ActionButton
          action-id="governance.confirm"
          primary
          :disabled="!governanceReady"
          reason="请选择目标并填写原因"
          :run="saveGovernance"
        >
          确认
        </ActionButton>
      </div>
    </Modal><Modal :open="activityOpen" title="记录一次跟进" description="记录沟通结果，并明确下一步。" @update:open="closeActivity">
      <div class="form-grid">
        <label>跟进类型<select v-model="activity.type">
          <option v-for="(v, k) in activityTypes" :key="k" :value="k">{{ v }}</option>
        </select></label><label>时间 *<input v-model="activity.occurredAt" type="datetime-local" /></label><label class="wide">摘要 *<textarea
          v-model="activity.summary"
          name="summary"
          rows="3"
          maxlength="2000"
        ></textarea></label><label class="wide">结果<textarea v-model="activity.result" rows="2"></textarea></label><label class="wide">下一步动作<input v-model="activity.nextAction" name="nextAction" /></label><label class="wide">下一次跟进时间<input
          v-model="activity.nextFollowupAt"
          type="datetime-local"
          name="nextFollowupAt"
        /></label>
      </div>
      <div class="actions modal-actions">
        <ActionButton action-id="dialog.cancel" :run="closeActivity">取消</ActionButton><ActionButton
          action-id="activity.create"
          primary
          :disabled="!can('activities') || !activity.summary.trim() || !activity.occurredAt"
          :reason="can('activities') ? '请填写跟进时间和摘要' : reason('activities')"
          :run="record"
        >
          保存跟进
        </ActionButton>
      </div>
    </Modal>
  </template>
  <p v-else-if="!failure" class="muted" role="status">正在加载项目…</p>
</template>
