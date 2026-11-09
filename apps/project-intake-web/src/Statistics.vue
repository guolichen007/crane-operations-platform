<script setup lang="ts">
// G3-3B statistics: real server aggregation (GET /stage-summary) over the
// caller's scope. Shows counts per sales stage and the KNOWN part of the
// estimated project amount. Amounts stay decimal strings end to end.
import { ref, computed, watch, onMounted } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { Search, RefreshCw } from '@lucide/vue';
import { useApp } from './store';
import { statuses, stages, when } from './model';
import { recovery } from './reasons';
import { filterQuery, type PipelineFilters } from './pipeline';
import { yuan, sharePercent, type StageSummaryResponse, type AmountSummary } from './amount';
import ActionButton from './ActionButton.vue';

const app = useApp(),
  router = useRouter(),
  route = useRoute();
const scopeNote = computed(
  () =>
    ({
      baobei_sales: '你负责的项目',
      baobei_manager: '本团队已分配项目',
      baobei_director: '全部已分配项目',
      baobei_admin: '全部项目，含待分配草稿',
    })[app.user?.role || ''] || ''
);
const q = ref(String(route.query.q || '')),
  status = ref(String(route.query.status || '')),
  province = ref(String(route.query.province || '')),
  after = ref(String(route.query.after || ''));
const filters = computed<PipelineFilters>(() => ({
  q: q.value.trim(),
  status: status.value,
  province: province.value.trim(),
  updatedAfter: after.value ? new Date(after.value + 'T00:00:00').toISOString() : '',
}));
const data = ref<StageSummaryResponse | null>(null),
  state = ref<'loading' | 'ready' | 'error'>('loading'),
  failure = ref('');
let seq = 0;
async function load() {
  const n = ++seq;
  state.value = 'loading';
  failure.value = '';
  try {
    const b = await app.api<StageSummaryResponse>('/api/v1/stage-summary?' + filterQuery(filters.value));
    if (n !== seq) return;
    data.value = b;
    state.value = 'ready';
  } catch (e) {
    if (n !== seq) return;
    failure.value = recovery(e).message;
    state.value = 'error';
  }
}
let timer: ReturnType<typeof setTimeout>;
watch(filters, () => {
  data.value = null;
  clearTimeout(timer);
  timer = setTimeout(load, 250);
}, { deep: true });
onMounted(load);

const statusText = computed(() => (status.value ? '报备状态：' + statuses[status.value] : '报备状态：全部（含草稿、退回）'));
const notKnown = (m: AmountSummary) => m.unknownCount + m.legacyUnconfirmedCount + m.invalidCount;
// Click-through keeps the same base filters (stage + q/status/province/after).
function openStage(code: string) {
  const query: Record<string, string> = { stage: code };
  if (q.value.trim()) query.q = q.value.trim();
  if (status.value) query.status = status.value;
  if (province.value.trim()) query.province = province.value.trim();
  if (after.value) query.after = after.value;
  return router.push({ path: '/projects', query });
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>统计</h1>
      <p class="muted">
        范围：{{ scopeNote }} · {{ statusText }}<template v-if="data"> · 统计时间 {{ when(data.asOf) }}</template>
      </p>
    </div>
    <div class="actions">
      <ActionButton action-id="statistics.refresh" :run="load"><RefreshCw :size="15" aria-hidden="true" />刷新</ActionButton>
    </div>
  </div>
  <div class="filterbar pipeline-filters">
    <label class="search-field"><Search :size="16" aria-hidden="true" /><input v-model="q" aria-label="搜索报备" placeholder="客户、项目或报备编号" /></label>
    <select v-model="status" aria-label="报备状态筛选">
      <option value="">全部报备状态</option>
      <option v-for="(label, key) in statuses" :key="key" :value="key">{{ label }}</option>
    </select>
    <input v-model="province" aria-label="省份筛选" placeholder="省份" />
    <input v-model="after" type="date" aria-label="更新时间起" />
  </div>
  <p class="note stats-scope" role="note">
    金额为<strong>项目预估金额</strong>中已确认的部分（人民币元），不是合同金额、销售收入或回款。未知、历史待确认与数据异常的金额不计入合计，只显示数量。
  </p>
  <p v-if="state === 'loading'" class="muted" role="status">正在计算统计…</p>
  <div v-else-if="state === 'error'" class="form-alert" role="alert">
    无法加载统计：{{ failure }} <ActionButton action-id="statistics.refresh" :run="load">重新加载</ActionButton>
  </div>
  <template v-if="data && state === 'ready'">
    <section class="stats-kpis" aria-label="金额与数量概览">
      <div class="stats-kpi">
        <span class="stats-label">项目总数</span>
        <strong data-testid="stat-total">{{ data.total }}</strong>
        <small class="muted">当前范围与筛选下全部项目</small>
      </div>
      <div class="stats-kpi primary">
        <span class="stats-label">已确认预估金额</span>
        <strong data-testid="stat-known-total">{{ yuan(data.amount.knownTotal) }}</strong>
        <small class="muted" data-testid="stat-known-count">来自 {{ data.amount.knownCount }} 个项目<template v-if="data.amount.knownZeroCount">（其中 {{ data.amount.knownZeroCount }} 个确认为 0）</template></small>
      </div>
      <div class="stats-kpi">
        <span class="stats-label">金额未知</span>
        <strong data-testid="stat-unknown">{{ data.amount.unknownCount }}</strong>
        <small class="muted">未填写预估金额的项目</small>
      </div>
      <div class="stats-kpi">
        <span class="stats-label">历史金额待确认</span>
        <strong data-testid="stat-legacy">{{ data.amount.legacyUnconfirmedCount }}</strong>
        <small class="muted">历史记录为 0，尚未确认是否真实</small>
      </div>
      <div :class="['stats-kpi', { warn: data.amount.invalidCount }]">
        <span class="stats-label">金额数据异常</span>
        <strong data-testid="stat-invalid">{{ data.amount.invalidCount }}</strong>
        <small class="muted">已知性与数值矛盾或两处金额不一致，未计入</small>
      </div>
    </section>
    <p v-if="!data.amount.knownCount && data.total" class="note" role="note">当前范围内还没有已确认的预估金额，合计 ¥0.00 不代表项目价值为零。</p>
    <p v-if="data.amount.knownImplicitCount" class="muted stats-implicit">已确认金额中有 {{ data.amount.knownImplicitCount }} 个来自历史记录（未单独标记已知性，按既有规则视为已知）。</p>
    <p v-if="data.unclassified" class="note" role="note">
      有 {{ data.unclassified }} 个项目的销售阶段不在 12 个标准阶段内（已确认金额 {{ yuan(data.unclassifiedAmount.knownTotal) }}），已计入总数与合计，但不属于任何阶段行，请联系管理员核对。
    </p>

    <div class="stats-table-wrap">
      <table class="stats-table">
        <caption class="sr-only">销售阶段的项目数量与预估金额</caption>
        <thead>
          <tr>
            <th scope="col">销售阶段</th>
            <th scope="col" class="num">项目数</th>
            <th scope="col" class="num">已确认预估金额</th>
            <th scope="col" class="num">有已知金额</th>
            <th scope="col" class="num">未知 / 待确认 / 异常</th>
            <th scope="col"><span class="sr-only">打开</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in data.stages" :key="s.code" :data-stage="s.code">
            <th scope="row">
              {{ stages[s.code] }}
              <span class="stats-bar" aria-hidden="true"><span :style="{ width: sharePercent(s.amount.knownTotal, data.amount.knownTotal) + '%' }"></span></span>
            </th>
            <td class="num">{{ s.count }}</td>
            <td class="num mono">{{ yuan(s.amount.knownTotal) }}</td>
            <td class="num">{{ s.amount.knownCount }}</td>
            <td class="num">{{ s.amount.unknownCount }} / {{ s.amount.legacyUnconfirmedCount }} / {{ s.amount.invalidCount }}</td>
            <td>
              <ActionButton action-id="statistics.open-stage" class="stats-open" :disabled="!s.count" reason="本阶段没有项目" :run="() => openStage(s.code)" :aria-label="'查看' + stages[s.code] + '阶段项目'">查看</ActionButton>
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <th scope="row">合计</th>
            <td class="num">{{ data.total }}</td>
            <td class="num mono">{{ yuan(data.amount.knownTotal) }}</td>
            <td class="num">{{ data.amount.knownCount }}</td>
            <td class="num">{{ data.amount.unknownCount }} / {{ data.amount.legacyUnconfirmedCount }} / {{ data.amount.invalidCount }}</td>
            <td></td>
          </tr>
        </tfoot>
      </table>
    </div>
    <div class="stats-cards">
      <ActionButton
        v-for="s in data.stages"
        :key="s.code"
        action-id="statistics.open-stage"
        class="stats-card"
        :disabled="!s.count"
        reason="本阶段没有项目"
        :run="() => openStage(s.code)"
      >
        <span class="stats-card-head"><strong>{{ stages[s.code] }}</strong><span>{{ s.count }} 个项目</span></span>
        <span class="mono">{{ yuan(s.amount.knownTotal) }}</span>
        <small class="muted">已知金额 {{ s.amount.knownCount }} 个 · 未计入 {{ notKnown(s.amount) }} 个</small>
      </ActionButton>
    </div>
    <p class="muted stats-foot">未提供赢单率、同比环比、排名或收入预测：当前数据没有可信依据。</p>
  </template>
</template>
