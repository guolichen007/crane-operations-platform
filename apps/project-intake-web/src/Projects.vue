<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { Search, SlidersHorizontal, ArrowUpRight, List, Columns3 } from '@lucide/vue';
import { readFilters, writeFilters, toPipeline } from './view-state';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import { projectScopeLabel, can } from './navigation';
import { recovery } from './reasons';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
const app = useApp(),
  router = useRouter(),
  route = useRoute(),
  items = ref<Project[]>([]),
  total = ref(0),
  asOf = ref(''),
  // G4-S5-R0: filters, sort and page live in the URL (back/forward/refresh/share).
  initial = readFilters(route.query),
  q = ref(initial.q),
  status = ref(initial.status),
  // G3-3B: the statistics page links here with the same base filters.
  stage = ref(initial.stage),
  province = ref(initial.province),
  after = ref(initial.after),
  sort = ref<'updated' | 'name'>(initial.sort),
  page = ref(initial.page),
  preview = ref<Project | null>(null),
  columns = ref(false),
  showLocation = ref(true),
  showOwner = ref(true),
  loading = ref(false),
  failure = ref(''),
  followup = ref<'overdue' | 'upcoming'>(route.query.window === 'upcoming' ? 'upcoming' : 'overdue');
let generation = 0;
const view = computed(() =>
  route.path === '/followups' ? 'followups' : route.path === '/unassigned' ? 'unassigned' : 'projects'
);
const scopeName = computed(() => projectScopeLabel(app.user));
const scopeNote = computed(
  () =>
    ({
      baobei_sales: '范围：你负责的项目',
      baobei_manager: '范围：本团队已分配项目',
      baobei_director: '范围：全部已分配项目',
      baobei_admin: '范围：全部项目，含待分配草稿',
    })[app.user?.role || ''] || ''
);
const title = computed(
  () =>
    ({ followups: '待跟进', unassigned: '待分配草稿', projects: scopeName.value })[view.value]
);
async function load() {
  const n = ++generation;
  loading.value = true;
  failure.value = '';
  try {
    const params = new URLSearchParams({ page: String(page.value), pageSize: '20' });
    const set = (k: string, v: string) => v && params.set(k, v);
    set('q', q.value);
    set('status', status.value);
    set('stage', stage.value);
    set('province', province.value);
    if (sort.value !== 'updated') params.set('sort', sort.value);
    if (after.value) params.set('updatedAfter', new Date(after.value).toISOString());
    if (view.value === 'followups') {
      params.set('followup', followup.value);
      params.set('sort', 'followup');
    }
    if (view.value === 'unassigned') params.set('assignment', 'unassigned');
    const b = await app.api<{ items: Project[]; total: number; asOf: string }>('/api/v1/projects?' + params);
    if (n === generation) {
      items.value = b.items;
      total.value = b.total;
      asOf.value = b.asOf;
    }
  } catch (e) {
    if (n === generation) failure.value = recovery(e).message;
  } finally {
    if (n === generation) loading.value = false;
  }
}
const current = () => ({ q: q.value.trim(), status: status.value, stage: stage.value, province: province.value.trim(), after: after.value, sort: sort.value, page: page.value });
// Keep the URL in step with the screen (replace: typing does not flood history).
// Bound to this page's route: a pending sync must never rewrite another page's URL.
const ownPath = route.path;
function syncUrl() {
  if (route.path !== ownPath) return;
  const query = { ...writeFilters(current()), ...(view.value === 'followups' && followup.value === 'upcoming' ? { window: 'upcoming' } : {}) };
  if (JSON.stringify(query) !== JSON.stringify(route.query)) void router.replace({ query });
}
let timer: ReturnType<typeof setTimeout>;
let applying = false;
watch([q, status, stage, province, after, sort, followup], () => {
  if (applying) return;
  page.value = 1;
  clearTimeout(timer);
  timer = setTimeout(() => {
    syncUrl();
    void load();
  }, 250);
});
watch(page, () => {
  if (applying) return;
  syncUrl();
  void load();
});
// Back/forward or a link (global search, statistics) changed the query: apply it.
watch(
  () => route.query,
  (query) => {
    const f = readFilters(query);
    const same = JSON.stringify(f) === JSON.stringify(current()) &&
      (query.window === 'upcoming') === (followup.value === 'upcoming');
    if (same || route.path !== ownPath) return;
    applying = true;
    q.value = f.q; status.value = f.status; stage.value = f.stage; province.value = f.province;
    after.value = f.after; sort.value = f.sort; page.value = f.page;
    followup.value = query.window === 'upcoming' ? 'upcoming' : 'overdue';
    void Promise.resolve().then(() => {
      applying = false;
      void load();
    });
  }
);
onBeforeUnmount(() => clearTimeout(timer));
void load();
const here = () => router.resolve({ path: route.path, query: route.query }).fullPath;
function openDetail(id: string) {
  return router.push({ path: '/projects/' + id, query: { from: here() } });
}
const pipelineLink = computed(() => ({ path: '/pipeline', query: toPipeline(current()) }));
async function open(p: Project) {
  preview.value = await app.api<Project>('/api/v1/projects/' + p.id);
}
const owner = (p: Project) => (!p.ownerSubject ? '待分配' : p.ownerSubject === app.user?.subject ? '我' : p.ownerName || '姓名待核对');
const asOfText = computed(() => (asOf.value ? new Date(asOf.value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : ''));
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>
        {{ title }} <small>{{ total }}</small>
      </h1>
      <p class="muted">
        {{ scopeNote }}<template v-if="asOfText"> · 统计时间 {{ asOfText }}</template>
      </p>
    </div>
    <div class="actions">
      <nav v-if="view === 'projects'" class="view-switch" aria-label="切换视图">
        <span class="view-current" aria-current="page"><List :size="15" aria-hidden="true" />列表视图</span>
        <ActionButton action-id="view.pipeline" :run="() => router.push(pipelineLink)"><Columns3 :size="15" aria-hidden="true" />管道视图</ActionButton>
      </nav>
      <ActionButton v-if="view === 'unassigned' && can(app.user, 'project.create')" action-id="project.create" primary :run="() => router.push('/new')">新建项目</ActionButton>
    </div>
  </div>
  <div v-if="view === 'followups'" class="segmented" role="tablist" aria-label="跟进时间窗口">
    <ActionButton action-id="followup.window" role="tab" :aria-selected="followup === 'overdue'" :class="{ selected: followup === 'overdue' }" :run="() => { followup = 'overdue'; }">已逾期</ActionButton>
    <ActionButton action-id="followup.window" role="tab" :aria-selected="followup === 'upcoming'" :class="{ selected: followup === 'upcoming' }" :run="() => { followup = 'upcoming'; }">未来 7 天</ActionButton>
  </div>
  <p v-if="view === 'followups'" class="muted window-note">
    {{ followup === 'overdue' ? '下次跟进时间早于统计时间的项目。' : '下次跟进时间在统计时间起 7 天内的项目，与“已逾期”不重叠。' }}
  </p>
  <div class="filterbar">
    <label class="search-field"><Search :size="16" aria-hidden="true" /><input
      v-model="q"
      aria-label="搜索报备"
      placeholder="客户、项目或报备编号"
    /></label><select v-model="status" aria-label="报备状态筛选">
      <option value="">全部状态</option>
      <option v-for="(label, key) in statuses" :key="key" :value="key">{{ label }}</option>
    </select><select v-model="stage" aria-label="阶段筛选">
      <option value="">全部阶段</option>
      <option v-for="(label, key) in stages" :key="key" :value="key">{{ label }}</option>
    </select><input v-model="province" aria-label="省份筛选" placeholder="省份" /><input
      v-model="after"
      type="date"
      aria-label="更新时间起"
    /><ActionButton
      action-id="project.sort"
      :run="
        () => {
          sort = sort === 'name' ? 'updated' : 'name';
        }
      "
    >
      {{ sort === 'name' ? '按名称' : '按最近更新' }}
    </ActionButton><ActionButton
      action-id="project.columns"
      :run="
        () => {
          columns = !columns;
        }
      "
    >
      <SlidersHorizontal :size="15" aria-hidden="true" />列
    </ActionButton>
  </div>
  <div v-if="columns" class="column-settings">
    <label><input v-model="showLocation" type="checkbox" />地区</label><label><input v-model="showOwner" type="checkbox" />负责人</label>
  </div>
  <p v-if="loading" role="status" class="muted">正在加载报备…</p>
  <div v-if="failure" class="form-alert" role="alert">
    {{ failure }} <ActionButton action-id="project.reload" :run="load">重新加载</ActionButton>
  </div>
  <template v-else>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>报备编号 / 客户</th>
            <th>项目名称</th>
            <th v-if="showLocation">地区</th>
            <th>销售阶段</th>
            <th>报备状态</th>
            <th v-if="showOwner">负责人</th>
            <th>下次跟进 / 下一步</th>
            <th>更新时间</th>
            <th><span class="sr-only">操作</span></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in items"
            :key="p.id"
            tabindex="0"
            data-action-id="project.preview"
            :aria-label="'预览 ' + (p.projectName || '未命名草稿')"
            @click="open(p)"
            @keydown.enter="open(p)"
            @keydown.space.prevent="open(p)"
          >
            <td>
              <span class="mono">{{ p.registrationNo }}</span><small>{{ p.customer || '待填写客户' }}</small>
            </td>
            <td class="project-cell">{{ p.projectName || '未命名草稿' }}</td>
            <td v-if="showLocation">{{ p.location.province }} {{ p.location.city }}</td>
            <td>{{ stages[p.salesStage] }}</td>
            <td>
              <span :class="['badge', p.intakeStatus.toLowerCase()]">{{ statuses[p.intakeStatus] }}</span>
            </td>
            <td v-if="showOwner" :class="{ unassigned: !p.ownerSubject }">{{ owner(p) }}</td>
            <td>
              {{ when(p.nextFollowupAt) }}<small>{{ p.nextAction || '尚未记录下一步' }}</small>
            </td>
            <td class="muted">{{ when(p.updatedAt) }}</td>
            <td @click.stop>
              <ActionButton action-id="project.preview" :run="() => open(p)" aria-label="预览项目">
                <ArrowUpRight :size="15" aria-hidden="true" />
              </ActionButton>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="mobile-projects">
      <ActionButton
        v-for="p in items"
        :key="p.id"
        action-id="project.preview"
        class="project-card"
        :run="() => open(p)"
      >
        <span class="mono">{{ p.registrationNo }}</span><strong>{{ p.projectName || '未命名草稿' }}</strong><span>{{ p.customer }}</span><span class="card-meta"><span class="badge">{{ statuses[p.intakeStatus] }}</span>{{ stages[p.salesStage] }} · {{ owner(p) }}</span><small>下一步：{{ p.nextAction || '尚未记录' }}</small>
      </ActionButton>
    </div>
    <div v-if="!items.length && !loading" class="empty">
      <h2>{{ q || status || stage || province || after ? '没有符合筛选条件的项目' : view === 'unassigned' ? '没有待分配草稿' : view === 'followups' ? (followup === 'overdue' ? '没有逾期的跟进' : '未来 7 天没有安排跟进') : '还没有项目' }}</h2>
      <p class="muted">{{ q || status || stage || province || after ? '调整或清空筛选条件后再试。' : '新建报备后会出现在这里。' }}</p>
    </div>
  </template>
  <div class="pagination">
    <span class="muted">共 {{ total }} 项 · 第 {{ page }} 页 · 本页 {{ items.length }} 项</span><ActionButton
      action-id="project.page-prev"
      :disabled="page === 1 || loading"
      reason="已经是第一页或正在加载"
      :run="
        () => {
          page--;
        }
      "
    >
      上一页
    </ActionButton><ActionButton
      action-id="project.page-next"
      :disabled="page * 20 >= total || loading"
      reason="没有下一页或正在加载"
      :run="
        () => {
          page++;
        }
      "
    >
      下一页
    </ActionButton>
  </div>
  <Modal
    :open="!!preview"
    title="报备快速预览"
    description="查看关键信息，或进入完整详情。"
    sheet
    @update:open="preview = null"
  >
    <template v-if="preview">
      <span class="mono muted">{{ preview.registrationNo }}</span>
      <h2>{{ preview.projectName || '未命名草稿' }}</h2>
      <p>{{ preview.customer }}</p>
      <div class="actions">
        <span class="badge">{{ statuses[preview.intakeStatus] }}</span><span class="badge">{{ stages[preview.salesStage] }}</span>
      </div>
      <dl>
        <dt>负责人</dt>
        <dd>{{ owner(preview) }}</dd>
        <dt>位置</dt>
        <dd>
          {{ preview.location.province }} {{ preview.location.city }}
          {{ preview.location.factoryName }} {{ preview.location.workshop }}
        </dd>
        <dt>主要需求</dt>
        <dd>{{ preview.requirementNotes || '尚未填写' }}</dd>
        <dt>下次跟进</dt>
        <dd>{{ when(preview.nextFollowupAt) }}</dd>
        <dt>下一步</dt>
        <dd>{{ preview.nextAction || '尚未记录' }}</dd>
      </dl>
      <div class="actions">
        <ActionButton
          action-id="project.detail"
          primary
          :run="
            () => {
              const id = preview?.id;
              preview = null;
              return id && openDetail(id);
            }
          "
        >
          打开完整详情 <ArrowUpRight :size="15" aria-hidden="true" />
        </ActionButton><ActionButton
          action-id="project.preview-close"
          :run="
            () => {
              preview = null;
            }
          "
        >
          关闭
        </ActionButton>
      </div>
    </template>
  </Modal>
</template>
