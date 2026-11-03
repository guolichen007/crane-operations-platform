<script setup lang="ts">
import { ref, reactive, onMounted, computed, onBeforeUnmount } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { Plus, Pencil, ArrowLeft } from '@lucide/vue';
import { useApp } from './store';
import { type Project, type Activity, statuses, stages, reqs, activityTypes, when } from './model';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
const protectionLabels:Record<string,string>={ACTIVE:'有效',WARNING:'需要跟进',EXPIRING:'即将到期',EXPIRED:'需主管复核'};
const auditLabels: Record<string, string> = {
  'project.create': '创建报备',
  'project.edit': '更新草稿',
  'project.submit': '提交报备',
  'activity.create': '记录跟进',
'project.stage-change':'修改销售阶段','ownership.transfer':'转移归属','protection.sweep':'保护资格复核','review.approve':'批准报备','review.return':'退回补充','review.reject':'拒绝报备','duplicate.confirm':'确认重复','duplicate.dismiss':'排除重复并批准',APPROVE:'批准',RETURN:'退回补充',REJECT:'拒绝',CONFIRMED_DUPLICATE:'确认重复',DISTINCT_APPROVED:'排除重复并批准',
};
const app = useApp(),
  route = useRoute(),
  router = useRouter(),
  p = ref<Project | null>(null),
  tab = ref('概况'),
  activityOpen = ref(false),
  activity = reactive<Activity>({
    type: 'PHONE',
    occurredAt: new Date(Date.now() - new Date().getTimezoneOffset() * 60000)
      .toISOString()
      .slice(0, 16),
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
const isManager = computed(() =>
  ['baobei_manager', 'baobei_director'].includes(app.user?.role || '')
);
async function openGovernance(action: string) {
  governanceReason.value = '';
  target.value = '';
  targetStage.value = '';
  if (action === 'ownership-transfer' && p.value) {
    const b = await app.api<{ items: { subject: string; name: string }[] }>(
      `/api/v1/ownership-candidates?projectId=${p.value.id}`
    );
    candidates.value = b.items.filter((c) => c.subject !== p.value?.ownerSubject);
  }
  governanceOpen.value = action;
}
async function closeGovernance(){if(governanceReason.value.trim()&&!(await app.confirmDiscard()))return;governanceOpen.value=''}
async function saveGovernance() {
  if (!p.value) return;
  const body = {
    version: p.value.version,
    reason: governanceReason.value,
    ...(governanceOpen.value === 'ownership-transfer'
      ? { targetSubject: target.value }
      : { salesStage: targetStage.value }),
  };
  await app.api(`/api/v1/projects/${p.value.id}/${governanceOpen.value}`, 'POST', body);
  governanceOpen.value = '';
  await load();
  app.toast('项目已更新，操作已记录审计');
}
const owner = computed(
  () => p.value?.ownerSubject === app.user?.subject && app.user?.role === 'baobei_sales'
);
async function load() {
  p.value = await app.api<Project>('/api/v1/projects/' + route.params.id);
}
onMounted(() => load().catch((e) => (app.error = e.message)));
async function submit() {
  if (!p.value) return;
  const b = await app.api<Project>('/api/v1/projects/' + p.value.id + '/submit', 'POST', {
    version: p.value.version,
  });
  app.toast(b.intakeStatus === 'UNDER_REVIEW' ? '已提交，等待重复审核' : '报备提交成功');
  await load();
}
async function record() {
  if (!p.value) return;
  await app.api('/api/v1/projects/' + p.value.id + '/activities', 'POST', {
    ...activity,
    version: p.value.version,
    occurredAt: new Date(activity.occurredAt).toISOString(),
    nextFollowupAt: activity.nextFollowupAt ? new Date(activity.nextFollowupAt).toISOString() : '',
  });
  activityOpen.value = false;
  activity.summary = '';
  activity.result = '';
  activity.nextAction = '';
  activity.nextFollowupAt = '';
  await load();
  tab.value = '跟进记录';
  app.toast('跟进已记录，项目状态已更新');
}
async function closeActivity() {
  if (activity.summary || activity.result || activity.nextAction) {
    if (!(await app.confirmDiscard())) return;
  }
  activityOpen.value = false;
}
const activityDirty = computed(
  () => activityOpen.value && !!(activity.summary || activity.result || activity.nextAction)
);
onBeforeRouteLeave(async () => !(activityDirty.value || governanceOpen.value && governanceReason.value.trim()) || (await app.confirmDiscard()));
function preventUnload(e: BeforeUnloadEvent) {
  if (activityDirty.value || governanceOpen.value && governanceReason.value.trim()) {
    e.preventDefault();
    e.returnValue = '';
  }
}
window.addEventListener('beforeunload', preventUnload);
onBeforeUnmount(() => window.removeEventListener('beforeunload', preventUnload));
</script>
<template>
  <template v-if="p">
    <ActionButton action-id="nav.open" class="back-link" :run="() => router.push('/projects')">
      <ArrowLeft :size="14" />返回我的报备
    </ActionButton>
    <div class="detail-heading">
      <div>
        <div class="eyebrow">{{ p.registrationNo }} / PROJECT RECORD</div>
        <h1>{{ p.projectName || '未命名草稿' }}</h1>
        <div class="actions">
          <span :class="['badge', p.intakeStatus.toLowerCase()]">{{
            statuses[p.intakeStatus]
          }}</span><span class="badge">{{ stages[p.salesStage] }}</span><span class="muted">负责人 · {{ owner ? '我' : p.ownerSubject }}</span>
        </div>
      </div>
      <div v-if="owner" class="actions">
        <ActionButton
          v-if="['DRAFT', 'RETURNED'].includes(p.intakeStatus)"
          action-id="project.edit"
          :run="() => router.push('/projects/' + p?.id + '/edit')"
        >
          <Pencil :size="14" />编辑
        </ActionButton><ActionButton
          v-if="['DRAFT', 'RETURNED'].includes(p.intakeStatus)"
          action-id="project.submit"
          primary
          :run="submit"
        >
          提交报备
        </ActionButton><ActionButton
          v-else
          action-id="activity.open"
          primary
          :disabled="!['SUBMITTED', 'UNDER_REVIEW', 'APPROVED'].includes(p.intakeStatus)"
          reason="当前状态不允许跟进"
          :run="
            () => {
              activityOpen = true;
            }
          "
        >
          <Plus :size="14" />记录跟进
        </ActionButton>
      </div>
    </div>
    <div v-if="p.intakeStatus === 'UNDER_REVIEW'" class="note">
      发现疑似重复，报备已进入待审核。归属需要主管进一步确认。
    </div>
    <div class="actions" style="margin-bottom: 20px">
      <ActionButton
        v-if="owner && ['SUBMITTED', 'UNDER_REVIEW', 'APPROVED'].includes(p.intakeStatus)"
        action-id="project.stage-change"
        :run="() => openGovernance('stage-change')"
      >
        修改销售阶段
      </ActionButton><ActionButton
        v-if="isManager"
        action-id="ownership.transfer"
        :run="() => openGovernance('ownership-transfer')"
      >
        转移归属
      </ActionButton><span class="badge">保护期 {{ protectionLabels[p.protectionStatus] }}</span><span class="muted">下次复核 {{ when(p.nextReviewAt) }}</span>
    </div>
    <div class="tabs" role="tablist">
      <ActionButton
        v-for="t in ['概况', '关系方', '设备与需求', '跟进记录', '重复与审批', '审计历史']"
        :key="t"
        action-id="project.tab"
        role="tab"
        :aria-selected="tab === t"
        :class="{ selected: tab === t }"
        :run="
          () => {
            tab = t;
          }
        "
      >
        {{ t }}
      </ActionButton>
    </div>
    <div class="detail-body">
      <template v-if="tab === '概况'">
        <h2>项目概况</h2>
        <dl class="detail-grid">
          <dt>终端客户</dt>
          <dd>{{ p.customer || '待填写' }}</dd>
          <dt>项目位置</dt>
          <dd>{{ Object.values(p.location).filter(Boolean).join(' · ') || '待填写' }}</dd>
          <dt>主要需求</dt>
          <dd>{{ p.requirementNotes || '待填写' }}</dd>
          <dt>预计金额</dt>
          <dd>
            {{ p.estimatedAmount ? p.estimatedAmount.toLocaleString('zh-CN') + ' 元' : '尚不明确' }}
          </dd>
          <dt>预计采购</dt>
          <dd>{{ p.expectedPurchaseDate || '尚不明确' }}</dd>
          <dt>下一步</dt>
          <dd>{{ p.nextAction || '尚未记录' }}</dd>
          <dt>下次跟进</dt>
          <dd>{{ when(p.nextFollowupAt) }}</dd>
          <dt>补充说明</dt>
          <dd class="prewrap">{{ p.notes || '暂无' }}</dd>
        </dl>
      </template><template v-if="tab === '关系方'">
        <h2>项目关系方</h2>
        <p><span class="badge">终端客户</span> {{ p.customer || '待填写' }}</p>
        <p v-for="(party, i) in p.parties" :key="i">
          <span class="badge">{{ party.role }}</span> {{ party.name }}
        </p>
        <p class="muted">联系人维护将在后续批次开放。</p>
      </template><template v-if="tab === '设备与需求'">
        <h2>设备与需求</h2>
        <div class="actions">
          <span v-for="r in p.requirements" :key="r" class="badge">{{ reqs[r] }}</span>
        </div>
        <p class="prewrap">{{ p.requirementNotes }}</p>
        <h3>计划设备</h3>
        <p v-for="(eq, i) in p.plannedEquipment" :key="i">
          {{ eq.equipmentType }} × {{ eq.quantity }} · {{ eq.manufacturer || '厂家待确认'
          }}<br /><small>{{ eq.notes }}</small>
        </p>
        <p v-if="!p.plannedEquipment.length" class="muted">暂无计划设备信息。</p>
      </template><template v-if="tab === '跟进记录'">
        <h2>跟进时间线</h2>
        <p v-if="!p.activities?.length" class="muted">还没有跟进，记录一次交流以明确下一步。</p>
        <ol class="timeline">
          <li v-for="(a, i) in p.activities" :key="i">
            <span class="muted">{{ when(a.occurredAt) }} · {{ activityTypes[a.type] }}</span>
            <h3>{{ a.summary }}</h3>
            <p v-if="a.result">{{ a.result }}</p>
            <p class="muted">
              下一步：{{ a.nextAction || '尚未记录' }} · {{ when(a.nextFollowupAt) }}
            </p>
          </li>
        </ol>
      </template><template v-if="tab === '重复与审批'">
        <h2>重复与审批</h2>
        <p>
          {{
            p.intakeStatus === 'UNDER_REVIEW'
              ? '当前存在疑似重复，等待主管核验。'
              : '当前状态：' + statuses[p.intakeStatus]
          }}
        </p>
        <ActionButton v-if="isManager" action-id="nav.open" :run="() => router.push('/reviews')">
          进入审核队列
        </ActionButton>
      </template><template v-if="tab === '审计历史'">
        <h2>审计时间线</h2>
        <ol class="timeline">
          <li v-for="(g, i) in p.governanceHistory" :key="i">
            <span class="muted">{{ when(g.at) }} · {{ g.actor }}</span>
            <p>{{ auditLabels[g.action]||'项目治理' }} · {{ g.reason }}</p>
            <p v-if="g.toOwner">{{ g.fromOwner || '首次归属' }} → {{ g.toOwner }}</p>
          </li>
        </ol>
        <ol class="timeline">
          <li v-for="(a, i) in p.audit" :key="i">
            <span class="muted">{{ when(a.createdAt) }} · {{ a.subject }} · 修订 {{ a.revision }}</span>
            <p>
              {{ auditLabels[a.action] || a.action
              }}<span v-if="a.details.reason"> · {{ a.details.reason }}</span>
            </p>
          </li>
        </ol>
      </template>
    </div>
    <Modal
      :open="!!governanceOpen"
      title="确认项目治理操作"
      description="操作需填写依据，更新版本并保留审计历史。"
      @update:open="closeGovernance"
    >
      <label v-if="governanceOpen === 'ownership-transfer'">新负责人<select v-model="target" name="targetOwner">
        <option value="">请选择</option>
        <option v-for="c in candidates" :key="c.subject" :value="c.subject">{{ c.name }}</option>
      </select></label><label v-else>目标阶段<select v-model="targetStage" name="targetStage">
        <option value="">请选择</option>
        <option v-for="(label, key) in stages" :key="key" :value="key">{{ label }}</option>
      </select></label><label>操作原因<textarea
        v-model="governanceReason"
        name="governanceReason"
        maxlength="1000"
      ></textarea>
      </label>
      <div class="actions modal-actions">
        <ActionButton
          action-id="dialog.cancel"
          :run="closeGovernance"
        >
          取消
        </ActionButton><ActionButton
          action-id="governance.confirm"
          primary
          :disabled="!governanceReason.trim() || !(target || targetStage)"
          reason="请选择目标并填写原因"
          :run="saveGovernance"
        >
          确认更新
        </ActionButton>
      </div>
    </Modal><Modal
      :open="activityOpen"
      title="记录一次跟进"
      description="记录沟通结果，并明确下一步。"
      @update:open="closeActivity"
    >
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
          :disabled="!activity.summary.trim() || !activity.occurredAt"
          reason="请填写跟进时间和摘要"
          :run="record"
        >
          保存跟进
        </ActionButton>
      </div>
    </Modal>
  </template>
  <p v-else class="muted">正在加载项目…</p>
</template>
