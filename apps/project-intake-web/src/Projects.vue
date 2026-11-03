<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { Search, SlidersHorizontal, ArrowUpRight } from 'lucide-vue-next';
import { useApp } from './store';
import { type Project, statuses, stages, when } from './model';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';
const app = useApp(),
  router = useRouter(),
  route = useRoute(),
  items = ref<Project[]>([]),
  total = ref(0),
  q = ref(String(route.query.q || '')),
  status = ref(''),
  stage = ref(''),
  province = ref(''),
  after = ref(''),
  sort = ref('updated'),
  page = ref(1),
  preview = ref<Project | null>(null),
  columns = ref(false),
  showLocation = ref(true),
  showOwner = ref(true),
  loading = ref(false);
let generation = 0;
const pipeline = computed(() => route.path === '/pipeline');
async function load() {
  const n = ++generation;
  loading.value = true;
  try {
    const params = new URLSearchParams({
      q: q.value,
      status: status.value,
      stage: stage.value,
      province: province.value,
      sort: sort.value,
      page: String(page.value),
      pageSize: '20',
      due: String(route.path === '/followups'),
    });
    if (after.value) params.set('updatedAfter', new Date(after.value).toISOString());
    const b = await app.api<{ items: Project[]; total: number }>('/api/v1/projects?' + params);
    if (n === generation) {
      items.value = b.items;
      total.value = b.total;
    }
  } catch (e) {
    app.error = (e as Error).message;
  } finally {
    if (n === generation) loading.value = false;
  }
}
let timer: ReturnType<typeof setTimeout>;
watch([q, status, stage, province, after, sort], () => {
  page.value = 1;
  clearTimeout(timer);
  timer = setTimeout(load, 250);
});
watch(page, load);
watch(
  () => route.query.q,
  (v) => (q.value = String(v || ''))
);
void load();
async function open(p: Project) {
  preview.value = await app.api<Project>('/api/v1/projects/' + p.id);
}
</script>
<template>
  <div class="page-heading">
    <div>
      <div class="eyebrow">PROJECTS / 项目管理</div>
      <h1>
        {{ pipeline ? '项目管道' : route.path === '/followups' ? '待跟进' : '我的报备' }}
        <small>{{ total }}</small>
      </h1>
      <p class="muted">
        {{ pipeline ? '按销售阶段查看项目进展。' : '集中管理客户需求，让每个机会都有迹可循。' }}
      </p>
    </div>
    <ActionButton v-if="pipeline" action-id="nav.open" :run="() => router.push('/projects')">
      列表视图
    </ActionButton>
  </div>
  <div class="filterbar">
    <label class="search-field"><Search :size="16" /><input
      v-model="q"
      aria-label="搜索报备"
      placeholder="搜索客户或项目…"
    /></label><select v-model="status" aria-label="报备状态筛选">
      <option value="">全部状态</option>
      <option v-for="(label, key) in statuses" :key="key" :value="key">{{ label }}</option>
    </select><select v-model="stage" aria-label="阶段筛选">
      <option value="">全部阶段</option>
      <option v-for="(label, key) in stages" :key="key" :value="key">{{ label }}</option>
    </select><input v-model="province" aria-label="地区筛选" placeholder="省份" /><input
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
      {{ sort === 'name' ? '名称 ↑' : '最近更新 ↓' }}
    </ActionButton><ActionButton
      action-id="project.columns"
      :run="
        () => {
          columns = !columns;
        }
      "
    >
      <SlidersHorizontal :size="15" />列
    </ActionButton>
  </div>
  <div v-if="columns" class="column-settings">
    <label><input v-model="showLocation" type="checkbox" />地区</label><label><input v-model="showOwner" type="checkbox" />负责人</label>
  </div>
  <p v-if="loading" role="status" class="muted">正在加载报备…</p>
  <div v-if="pipeline" class="pipeline">
    <section v-for="(label, key) in stages" :key="key">
      <h3>
        {{ label }} <small>{{ items.filter((p) => p.salesStage === key).length }}</small>
      </h3>
      <ActionButton
        v-for="p in items.filter((p) => p.salesStage === key)"
        :key="p.id"
        action-id="project.preview"
        :run="() => open(p)"
      >
        <strong>{{ p.projectName || '未命名草稿' }}</strong><small>{{ p.customer }}</small><span class="badge">{{ statuses[p.intakeStatus] }}</span>
      </ActionButton>
    </section>
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
            <th>最近跟进 / 下一步</th>
            <th>更新时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in items"
            :key="p.id"
            tabindex="0"
            data-action-id="project.preview"
            :aria-label="'预览 ' + p.projectName"
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
              <span :class="['badge', p.intakeStatus.toLowerCase()]">{{
                statuses[p.intakeStatus]
              }}</span>
            </td>
            <td v-if="showOwner">
              {{ p.ownerSubject === app.user?.subject ? '我' : p.ownerSubject }}
            </td>
            <td>
              {{ when(p.lastActivityAt) }}<small>{{ p.nextAction || '尚未记录下一步' }}</small>
            </td>
            <td class="muted">{{ when(p.updatedAt) }}</td>
            <td @click.stop>
              <ActionButton action-id="project.preview" :run="() => open(p)" aria-label="预览项目">
                <ArrowUpRight :size="15" />
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
        <span class="mono">{{ p.registrationNo }}</span><strong>{{ p.projectName || '未命名草稿' }}</strong><span>{{ p.customer }}</span><span class="card-meta"><span class="badge">{{ statuses[p.intakeStatus] }}</span>{{ stages[p.salesStage] }} · {{ p.location.city }}</span><small>下一步：{{ p.nextAction || '尚未记录' }}</small>
      </ActionButton>
    </div>
  </template>
  <div v-if="!items.length && !loading" class="empty">
    <h2>还没有匹配的报备</h2>
    <p class="muted">调整筛选条件，或从右上角新建报备。</p>
  </div>
  <div class="pagination">
    <span class="muted">共 {{ total }} 项 · 第 {{ page }} 页{{ pipeline ? ' · 管道展示当前页' : '' }}</span><ActionButton
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
        <dt>位置</dt>
        <dd>
          {{ preview.location.province }} {{ preview.location.city }}
          {{ preview.location.factoryName }} {{ preview.location.workshop }}
        </dd>
        <dt>主要需求</dt>
        <dd>{{ preview.requirementNotes || '尚未填写' }}</dd>
        <dt>最近跟进</dt>
        <dd>{{ when(preview.lastActivityAt) }}</dd>
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
              return router.push('/projects/' + id);
            }
          "
        >
          打开完整详情 <ArrowUpRight :size="15" />
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
