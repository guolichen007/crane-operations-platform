<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useRouter } from 'vue-router';
import { ArrowUpRight, Plus } from '@lucide/vue';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import ActionButton from './ActionButton.vue';
const app = useApp(),
  router = useRouter(),
  items = ref<Project[]>([]),
  total = ref(0);
onMounted(async () => {
  try {
    const b = await app.api<{ items: Project[]; total: number }>('/api/v1/projects?pageSize=100');
    items.value = b.items;
    total.value = b.total;
  } catch (e) {
    app.error = (e as Error).message;
  }
});
const due = computed(() =>
  items.value.filter(
    (p) => p.nextFollowupAt && new Date(p.nextFollowupAt).getTime() <= Date.now() + 7 * 86400000
  )
);
</script>
<template>
  <div class="page-heading">
    <div>
      <div class="eyebrow">OVERVIEW / 工作空间</div>
      <h1>{{ $route.path === '/statistics' ? '基础统计' : '工作台' }}</h1>
      <p class="muted">{{ app.user?.name }}，让每次跟进都有下一步。</p>
    </div>
    <span class="muted">{{
      new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' })
    }}</span>
  </div>
  <div class="metric-strip">
    <div>
      <span>我的报备</span><strong>{{ total }}</strong>
    </div>
    <div>
      <span>有效报备 · 当前页</span><strong>{{
        items.filter((p) => ['SUBMITTED', 'APPROVED'].includes(p.intakeStatus)).length
      }}</strong>
    </div>
    <div>
      <span>7 日内待跟进 · 当前页</span><strong>{{ due.length }}</strong>
    </div>
    <div>
      <span>待审核 / 重复 · 当前页</span><strong>{{ items.filter((p) => p.intakeStatus === 'UNDER_REVIEW').length }}</strong>
    </div>
  </div>
  <div class="section-title">
    <h2>
      最近项目 <small>{{ total }} 项</small>
    </h2>
    <ActionButton action-id="nav.open" :run="() => router.push('/projects')">
      全部报备 <ArrowUpRight :size="14" />
    </ActionButton>
  </div>
  <div v-if="!items.length" class="empty">
    <span class="empty-symbol">↗</span>
    <h2>从第一个项目开始</h2>
    <p class="muted">记录客户与需求，报备中心会协助你识别重复。</p>
    <ActionButton
      v-if="app.user?.role === 'baobei_sales'"
      action-id="project.create"
      primary
      :disabled="!app.user.permissions.includes('project.create')"
      reason="团队未授予新建报备权限"
      :run="() => router.push('/new')"
    >
      <Plus :size="16" />快速报备
    </ActionButton>
  </div>
  <div v-else class="recent-list">
    <ActionButton
      v-for="p in items.slice(0, 6)"
      :key="p.id"
      action-id="project.detail"
      :run="() => router.push('/projects/' + p.id)"
    >
      <span class="record-icon">P</span><span class="record-title">{{ p.projectName || '未命名草稿'
      }}<small>{{ p.customer || '待填写客户' }} · {{ p.registrationNo }}</small></span><span class="badge">{{ statuses[p.intakeStatus] }}</span><span class="muted desktop">{{ stages[p.salesStage] }}</span><span class="muted">{{ when(p.updatedAt) }}</span><ArrowUpRight :size="16" />
    </ActionButton>
  </div>
  <div class="dashboard-bottom">
    <section>
      <h2>跟进日程</h2>
      <p v-if="!due.length" class="muted">未来 7 日内暂无待跟进项目。</p>
      <p v-for="p in due.slice(0, 5)" :key="p.id">
        {{ when(p.nextFollowupAt) }} · {{ p.projectName }}<br /><small class="muted">{{
          p.nextAction
        }}</small>
      </p>
    </section>
    <section>
      <h2>最近动态</h2>
      <p v-if="!items.length" class="muted">提交项目或记录跟进后，动态会出现在这里。</p>
      <p v-for="p in items.slice(0, 4)" :key="p.id">
        <span class="live-dot"></span>{{ p.projectName || '草稿' }}<br /><small class="muted">{{ statuses[p.intakeStatus] }} · {{ when(p.updatedAt) }}</small>
      </p>
    </section>
  </div>
  <p v-if="total > 100" class="muted">
    指标按最近 100 条计算，总报备数显示完整数量；完整聚合统计留后续批次。
  </p>
</template>
