<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue';
import { useRoute } from 'vue-router';
import {
  Search,
  LayoutGrid,
  List,
  ArrowUpRight,
  Check,
  TriangleAlert,
  Radio,
  Video,
  ShieldCheck,
  ChevronRight,
  X,
  CalendarDays,
} from 'lucide-vue-next';
import { projects, devices, alarms, events, realProjects } from '../data';
import { lifecycleText, statusText, type Device, type SourceFilter } from '../data/types';
import DevicePanel from '../components/DevicePanel.vue';
import ProjectPanel from '../components/ProjectPanel.vue';
import type { Project } from '../data/types';
const Chart = defineAsyncComponent(() => import('../components/Chart.vue'));
const route = useRoute();
const page = computed(() => route.path.slice(1));
const titles: Record<string, [string, string]> = {
  projects: ['项目中心', '统一管理全国项目与实施计划'],
  devices: ['设备中心', '查看设备档案、运行状态与所属项目'],
  monitor: ['实时监控', '演示设备运行快照与模拟趋势'],
  alarms: ['告警中心', '固定演示告警 · 本地确认流程'],
  analytics: ['运行分析', '演示统计 · 运行效率与设备健康'],
  maintenance: ['运维计划', '保养计划、点检任务与处理进度'],
  remote: ['远程运维', '演示环境 · 不下发真实设备指令'],
};
const source = ref<SourceFilter>('all'),
  search = ref(''),
  province = ref('all'),
  lifecycle = ref('all'),
  layout = ref('cards'),
  selected = ref<Device>(),
  selectedProject = ref<Project>(),
  monitorId = ref(devices.find((d) => d.status === 'RUNNING')!.deviceId),
  alarmStatus = ref('all'),
  alarmLevel = ref('all');
const acknowledged = ref<string[]>(JSON.parse(localStorage.getItem('v031-acknowledged') ?? '[]'));
const modal = ref(''),
  notice = ref(''),
  remoteLog = ref<string[]>([]),
  tick = ref(0);
let timer: ReturnType<typeof setInterval>;
const maintenanceState = ref<Record<string, string>>(
  JSON.parse(localStorage.getItem('v031-maintenance') ?? '{}')
);
const filteredProjects = computed(() =>
  projects.filter(
    (p) =>
      (source.value === 'all' || p.dataSource === source.value) &&
      (province.value === 'all' || p.province === province.value) &&
      (lifecycle.value === 'all' || p.lifecycle === lifecycle.value) &&
      `${p.projectName}${p.locationLabel}${p.craneModel}`
        .toLowerCase()
        .includes(search.value.toLowerCase())
  )
);
const filteredDevices = computed(() =>
  devices.filter(
    (d) =>
      (source.value === 'all' || d.dataSource === source.value) &&
      (province.value === 'all' || d.province === province.value) &&
      (lifecycle.value === 'all' || d.lifecycle === lifecycle.value) &&
      `${d.deviceId}${d.deviceName}${d.locationLabel}${d.craneModel}`
        .toLowerCase()
        .includes(search.value.toLowerCase())
  )
);
const activeDevice = computed(
  () => devices.find((d) => d.deviceId === monitorId.value) ?? devices[10]!
);
const filteredAlarms = computed(() =>
  alarms.filter(
    (a) =>
      (alarmStatus.value === 'all' ||
        acknowledged.value.includes(a.id) === (alarmStatus.value === 'ack')) &&
      (alarmLevel.value === 'all' || a.level === alarmLevel.value) &&
      `${a.device.deviceName}${a.code}${a.description}`.includes(search.value)
  )
);
const tasks = devices
  .filter((d) => d.status === 'MAINTENANCE' || d.status === 'ALARM')
  .map((d, i) => ({
    id: `WO-202610-${i + 1}`,
    device: d,
    type: i % 2 ? '月度保养' : '专项点检',
    name: ['起升机构润滑与制动检查', '行走变频器散热检查', '抓斗钢丝绳磨损检测'][i % 3],
    date: `2026-10-${20 + i}`,
    owner: ['李工', '张工', '陈工'][i % 3],
    initial: ['待维护', '维护中', '已完成'][i % 3]!,
  }));
function taskStatus(id: string, initial: string) {
  return maintenanceState.value[id] ?? initial;
}
function moveTask(id: string, initial: string) {
  const stages = ['待维护', '维护中', '已完成'];
  maintenanceState.value[id] = stages[(stages.indexOf(taskStatus(id, initial)) + 1) % 3]!;
  localStorage.setItem('v031-maintenance', JSON.stringify(maintenanceState.value));
}
function ack(id: string) {
  if (!acknowledged.value.includes(id)) acknowledged.value.push(id);
  localStorage.setItem('v031-acknowledged', JSON.stringify(acknowledged.value));
}
function confirmSimulation() {
  remoteLog.value.unshift(
    `${new Date().toLocaleTimeString('zh-CN')} · 模拟${modal.value} · 仅本地记录`
  );
  notice.value = `模拟${modal.value}已记录`;
  modal.value = '';
}
function inspect(d: Device) {
  selectedProject.value = undefined;
  selected.value = d;
}
function inspectProject(id: string) {
  selected.value = undefined;
  selectedProject.value = projects.find((p) => p.id === id);
}
function selectSibling(id: string) {
  selectedProject.value = undefined;
  selected.value = devices.find((d) => d.deviceId === id);
}
function fromQuery() {
  const id = String(route.query.device ?? '');
  if (devices.some((d) => d.deviceId === id)) monitorId.value = id;
}
watch(page, () => {
  search.value = '';
  source.value = 'all';
  province.value = 'all';
  lifecycle.value = 'all';
  selected.value = undefined;
  selectedProject.value = undefined;
  notice.value = '';
});
watch(() => route.query.device, fromQuery);
onMounted(() => {
  fromQuery();
  timer = setInterval(() => tick.value++, 1000);
});
onUnmounted(() => clearInterval(timer));
const chartStyle = {
  textStyle: { fontFamily: 'Microsoft YaHei, sans-serif', color: '#82959f' },
  grid: { left: 45, right: 25, top: 28, bottom: 35 },
  tooltip: { trigger: 'axis' as const },
  xAxis: {
    type: 'category' as const,
    data: ['周一', '周二', '周三', '周四', '周五', '周六', '周日'],
    axisLine: { lineStyle: { color: '#25333e' } },
    axisTick: { show: false },
  },
  yAxis: { type: 'value' as const, splitLine: { lineStyle: { color: '#1c2832' } } },
};
const hoursOption = {
  ...chartStyle,
  series: [
    {
      type: 'bar' as const,
      data: [8.4, 10.2, 9.7, 12.8, 11.3, 7.6, 9.2],
      barWidth: 24,
      itemStyle: { color: '#51b9c5', borderRadius: [3, 3, 0, 0] },
    },
  ],
};
const utilizationOption = {
  ...chartStyle,
  xAxis: { ...chartStyle.xAxis, data: ['5月', '6月', '7月', '8月', '9月', '10月'] },
  series: [
    {
      type: 'line' as const,
      data: [62, 68, 65, 74, 78, 81],
      smooth: true,
      symbolSize: 6,
      lineStyle: { color: '#e8b75d', width: 2 },
      itemStyle: { color: '#e8b75d' },
      areaStyle: { color: '#e8b75d', opacity: 0.08 },
    },
  ],
};
const rankOption = {
  ...chartStyle,
  grid: { left: 65, right: 35, top: 20, bottom: 25 },
  xAxis: { type: 'value' as const, splitLine: { show: false } },
  yAxis: {
    type: 'category' as const,
    data: ['福建', '江苏', '河南', '山东', '湖北'],
    axisLine: { show: false },
    axisTick: { show: false },
  },
  series: [
    {
      type: 'bar' as const,
      data: [6.8, 7.3, 8.2, 9.4, 10.8],
      barWidth: 13,
      itemStyle: { color: '#55bca2', borderRadius: [0, 3, 3, 0] },
    },
  ],
};
const ratioOption = {
  textStyle: chartStyle.textStyle,
  tooltip: { trigger: 'item' as const },
  legend: { bottom: 12, textStyle: { color: '#82959f' } },
  series: [
    {
      type: 'pie' as const,
      radius: ['48%', '66%'],
      center: ['50%', '43%'],
      label: { show: false },
      data: [
        { value: 58, name: '运行', itemStyle: { color: '#55d6a4' } },
        { value: 22, name: '待机', itemStyle: { color: '#6ecdf2' } },
        { value: 8, name: '告警', itemStyle: { color: '#ff665f' } },
        { value: 5, name: '离线', itemStyle: { color: '#66727e' } },
        { value: 7, name: '维护', itemStyle: { color: '#e8b75d' } },
      ],
    },
  ],
};
const trend = computed(() => ({
  ...chartStyle,
  xAxis: {
    ...chartStyle.xAxis,
    data: ['14:26', '14:27', '14:28', '14:29', '14:30', '14:31', '14:32'],
  },
  series: [
    {
      type: 'line' as const,
      data: [32, 48, 44, 63, 58, 71, activeDevice.value.load],
      smooth: true,
      showSymbol: false,
      lineStyle: { color: '#51d6e8' },
      areaStyle: { color: '#51d6e8', opacity: 0.08 },
    },
  ],
}));
</script>
<template>
  <main class="workspace-page">
    <div class="page-heading">
      <div>
        <div class="eyebrow">OPERATIONS / {{ page.toUpperCase() }}</div>
        <h1>{{ titles[page]?.[0] }}</h1>
        <p>{{ titles[page]?.[1] }}</p>
      </div>
      <span class="environment-pill"><i class="gold-dot"></i>本地演示工作区</span>
    </div>
    <template v-if="page === 'projects' || page === 'devices'">
      <div class="workspace-summary">
        <div>
          <span>匿名登记项目</span><b>6 <small>项</small></b>
        </div>
        <div>
          <span>登记行车</span><b>10 <small>台</small></b>
        </div>
        <div>
          <span>演示项目</span><b>22 <small>项</small></b>
        </div>
        <div>
          <span>演示行车</span><b>44 <small>台</small></b>
        </div>
        <p><ShieldCheck :size="17" />匿名登记项目按计划展示<br />运行数据均为 DEMO 模拟</p>
      </div>
      <div class="filter-toolbar">
        <div class="segmented">
          <button
            v-for="s in [
              { id: 'all', name: '全部展示' },
              { id: 'real', name: '匿名登记项目' },
              { id: 'demo', name: '演示设备' },
            ] as const"
            :key="s.id"
            :class="{ selected: source === s.id }"
            @click="source = s.id"
          >
            {{ s.name }}
          </button>
        </div>
        <label class="search-box"
          ><Search :size="15" /><input
            v-model="search"
            aria-label="搜索项目设备"
            placeholder="搜索项目、型号或地区" /></label
        ><select v-model="province" aria-label="地区筛选">
          <option value="all">全部地区</option>
          <option v-for="p in [...new Set(projects.map((p) => p.province))]" :key="p">
            {{ p }}
          </option>
</select
        ><select v-model="lifecycle" aria-label="生命周期筛选">
          <option value="all">全部生命周期</option>
          <option v-for="(name, key) in lifecycleText" :key="key" :value="key">{{ name }}</option>
        </select>
        <div v-if="page === 'projects'" class="layout-toggle">
          <button
            aria-label="卡片视图"
            :class="{ selected: layout === 'cards' }"
            @click="layout = 'cards'"
          >
            <LayoutGrid :size="16" />
</button
          ><button
            aria-label="表格视图"
            :class="{ selected: layout === 'table' }"
            @click="layout = 'table'"
          >
            <List :size="16" />
          </button>
        </div>
      </div>
      <template v-if="page === 'projects'">
        <div v-if="layout === 'cards'" class="project-card-grid">
          <button
            v-for="p in filteredProjects"
            :key="p.id"
            class="project-card"
            @click="inspectProject(p.id)"
          >
            <div>
              <span :class="['source-badge', p.dataSource]">{{
                p.dataSource === 'real' ? 'REAL · 匿名登记项目' : 'DEMO · 演示数据'
              }}</span
              ><ArrowUpRight :size="16" />
            </div>
            <h2>{{ p.projectName }}</h2>
            <p>{{ p.locationLabel }}</p>
            <strong class="card-model">{{ p.craneModel }}</strong>
            <div class="card-spec">
              <span>{{ p.grabType }}</span
              ><b>{{ p.quantity }} 台</b>
            </div>
            <footer>
              <span>{{ lifecycleText[p.lifecycle] }}</span
              ><time>{{ p.plannedImplementation }}</time>
            </footer>
          </button>
        </div>
        <div v-else class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>项目</th>
                <th>地区</th>
                <th>型号</th>
                <th>数量</th>
                <th>来源</th>
                <th>生命周期</th>
                <th>计划</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in filteredProjects" :key="p.id">
                <td>{{ p.projectName }}</td>
                <td>{{ p.locationLabel }}</td>
                <td>{{ p.craneModel }}</td>
                <td>{{ p.quantity }} 台</td>
                <td>
                  <span :class="['source-badge', p.dataSource]">{{
                    p.dataSource === 'real' ? '真实' : '演示'
                  }}</span>
                </td>
                <td>{{ lifecycleText[p.lifecycle] }}</td>
                <td>{{ p.plannedImplementation }}</td>
                <td>
                  <button class="text-button" @click="inspectProject(p.id)">查看详情</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <section class="delivery-timeline">
          <h3>匿名登记项目实施时间轴</h3>
          <div>
            <button v-for="p in realProjects" :key="p.id" @click="inspectProject(p.id)">
              <time>{{ p.plannedImplementation }}</time
              ><i></i><strong>{{ p.projectName }}</strong
              ><span>{{ p.quantity }} 台 · {{ lifecycleText[p.lifecycle] }}</span>
            </button>
          </div>
        </section>
      </template>
      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>设备编号 / 所属项目</th>
              <th>地区</th>
              <th>型号</th>
              <th>起重量 / 跨度</th>
              <th>抓斗</th>
              <th>来源</th>
              <th>生命周期</th>
              <th>模拟状态</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="d in filteredDevices" :key="d.deviceId">
              <td>
                <strong>{{ d.deviceName }}</strong
                ><code>{{ d.deviceId }}</code>
              </td>
              <td>{{ d.locationLabel }}</td>
              <td>{{ d.craneModel }}</td>
              <td>{{ d.ratedCapacityT }}t / {{ d.spanM }}m</td>
              <td>{{ d.grabType }}</td>
              <td>
                <span :class="['source-badge', d.dataSource]">{{
                  d.dataSource === 'real' ? 'REAL ASSET / DEMO' : 'DEMO ASSET'
                }}</span>
              </td>
              <td>{{ lifecycleText[d.lifecycle] }}</td>
              <td>
                <span :class="['state-label', d.status]">{{ statusText[d.status] }}</span>
              </td>
              <td><button class="text-button" @click="inspect(d)">详情</button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="result-count">
        共 {{ page === 'projects' ? filteredProjects.length : filteredDevices.length }} 条记录
      </p>
    </template>
    <template v-else-if="page === 'monitor' || page === 'remote'">
      <div class="monitor-toolbar">
        <label
          >当前设备
          <select v-model="monitorId" aria-label="选择监控设备">
            <option v-for="d in devices" :key="d.deviceId" :value="d.deviceId">
              {{ d.deviceName }} · {{ d.dataSource === 'real' ? '真实计划项目' : 'DEMO' }}
            </option>
          </select></label
        ><span :class="['source-badge', activeDevice.dataSource]">{{
          activeDevice.dataSource === 'real' ? 'REAL ASSET · DEMO TELEMETRY' : 'DEMO TELEMETRY'
        }}</span>
      </div>
      <div v-if="page === 'remote'" class="remote-banner">
        <ShieldCheck :size="20" /><strong>演示环境 · 不下发真实设备指令</strong
        ><span>控制操作只记录在当前页面</span>
      </div>
      <div v-if="activeDevice.dataSource === 'real'" class="real-archive-notice">
        <strong>REAL ASSET · DEMO TELEMETRY</strong> · 真实档案尚未投运，以下为运行模拟。<br />{{
          activeDevice.scheduleText
        }}
      </div>
      <div class="monitor-layout">
        <section class="crane-monitor">
          <div class="panel-title">
            <h3>{{ activeDevice.projectName }}</h3>
            <span class="demo-note">模拟运行画面 / 非实时视频</span>
          </div>
          <div class="industrial-scene">
            <div class="warehouse-grid"></div>
            <svg viewBox="0 0 800 330" role="img" aria-label="抓斗行车示意图">
              <defs>
                <linearGradient id="steel" x1="0" y1="0" x2="1" y2="1">
                  <stop stop-color="#54757f" />
                  <stop offset="1" stop-color="#142d3c" />
                </linearGradient>
              </defs>
              <path d="M80 280 L270 190 L715 190 L530 280 Z" fill="#101e29" stroke="#304651" />
              <path
                d="M110 275 L110 95 L195 55 L195 235 M560 275 L560 95 L645 55 L645 235"
                stroke="url(#steel)"
                stroke-width="15"
                fill="none"
              />
              <path d="M95 88 L570 88 L657 48 L183 48 Z" fill="url(#steel)" stroke="#7a9da4" />
              <path
                d="M95 88 L95 108 L570 108 L570 88 M570 108 L657 68 L657 48"
                fill="#1e3b4b"
                stroke="#73929b"
              />
              <path
                d="M190 57 L320 88 M320 57 L450 88 M450 57 L570 86"
                stroke="#6b8c96"
                stroke-width="2"
              />
              <g
                :transform="`translate(${activeDevice.status === 'RUNNING' ? Math.sin(tick * 0.12) * 45 : 0},0)`"
              >
                <path
                  d="M355 70 L397 51 L433 51 L391 70 Z M355 70 L355 87 L391 87 L391 70"
                  fill="#d3ae65"
                />
                <path d="M374 86 L374 211 M387 86 L387 211" stroke="#c4d8da" stroke-width="1.5" />
                <path
                  d="M352 210 L407 210 L420 238 L382 256 L340 238 Z"
                  fill="#997b43"
                  stroke="#dbc284"
                />
                <path
                  d="M352 210 L382 233 L407 210 M382 233 L382 256"
                  stroke="#e5d3ac"
                  fill="none"
                />
              </g>
              <path
                d="M80 293 L540 293 M195 249 L700 249"
                stroke="#416570"
                stroke-dasharray="4 7"
              />
              <text x="85" y="320" fill="#64818b" font-size="11" letter-spacing="3">
                QZ GRAB CRANE / DEMO VISUALIZATION
              </text>
            </svg>
            <div class="monitor-status">
              <i class="green-dot"></i>{{ statusText[activeDevice.status]
              }}<span>工作模式 {{ activeDevice.mode === '自动' ? 'AUTO' : 'MANUAL' }}</span>
            </div>
          </div>
          <div class="axis-metrics">
            <div>
              <span>大车 X</span
              ><b
                >{{
                  (
                    activeDevice.x +
                    (activeDevice.status === 'RUNNING' ? Math.sin(tick * 0.12) * 2 : 0)
                  ).toFixed(2)
                }}
                <small>m</small></b
              >
            </div>
            <div>
              <span>小车 Y</span><b>{{ activeDevice.y.toFixed(2) }} <small>m</small></b>
            </div>
            <div>
              <span>起升 Z</span><b>{{ activeDevice.z.toFixed(2) }} <small>m</small></b>
            </div>
            <div>
              <span>当前负载</span><b>{{ activeDevice.load }} <small>%</small></b>
            </div>
          </div>
        </section>
        <section v-if="page === 'monitor'" class="monitor-side">
          <div class="panel-title">
            <h3>运行参数</h3>
            <Radio :size="14" />
          </div>
          <dl>
            <dt>设备编号</dt>
            <dd>{{ activeDevice.deviceId }}</dd>
            <dt>行车型号</dt>
            <dd>{{ activeDevice.craneModel }}</dd>
            <dt>抓斗开闭</dt>
            <dd>{{ activeDevice.grab }} · DEMO</dd>
            <dt>今日运行</dt>
            <dd>{{ activeDevice.todayHours.toFixed(1) }} h</dd>
            <dt>累计运行</dt>
            <dd>{{ activeDevice.totalHours }} h</dd>
            <dt>当前故障</dt>
            <dd>{{ activeDevice.fault }}</dd>
            <dt>最后心跳 · 模拟快照</dt>
            <dd>{{ activeDevice.heartbeat }}</dd>
          </dl>
        </section>
        <section v-else class="remote-control">
          <div class="video-placeholder">
            <Video :size="30" /><span>视频监控占位</span><small>演示环境 · 未连接视频源</small>
          </div>
          <h3>模拟操作区</h3>
          <p class="muted small">连接状态：演示会话 · 模式：本地模拟</p>
          <div class="control-grid">
            <button
              v-for="action in [
                '启动',
                '停止',
                '大车前进',
                '大车后退',
                '小车左移',
                '小车右移',
                '起升',
                '下降',
                '抓斗打开',
                '抓斗闭合',
              ]"
              :key="action"
              @click="modal = action"
            >
              {{ action }}
            </button>
          </div>
          <button class="simulated-stop" @click="modal = '急停'">模拟急停</button>
        </section>
      </div>
      <div v-if="page === 'monitor'" class="monitor-bottom">
        <section class="chart-card">
          <h3>负载变化趋势 <span>DEMO / %</span></h3>
          <Chart :option="trend" />
        </section>
        <section class="event-card">
          <h3>模拟事件</h3>
          <div v-for="e in events" :key="e.time">
            <time>{{ e.time }}</time
            ><span>{{ e.text }}</span>
          </div>
        </section>
      </div>
      <section v-if="page === 'remote'" class="local-log">
        <h3>本地模拟操作记录</h3>
        <p v-if="!remoteLog.length" class="muted">暂无模拟操作</p>
        <p v-for="log in remoteLog" :key="log">{{ log }}</p>
        <p v-if="notice" class="green" role="status">{{ notice }}</p>
      </section>
    </template>
    <template v-else-if="page === 'alarms'">
      <div class="alarm-summary">
        <div>
          <TriangleAlert :size="21" /><span>活动演示告警</span><b>{{ alarms.length }}</b>
        </div>
        <div>
          <span>未确认</span
          ><b class="coral">{{ alarms.filter((a) => !acknowledged.includes(a.id)).length }}</b>
        </div>
        <div>
          <span>已确认</span><b>{{ acknowledged.length }}</b>
        </div>
        <div><span>数据来源</span><strong>DEMO MOCK</strong></div>
      </div>
      <div class="filter-toolbar">
        <label class="search-box"
          ><Search :size="14" /><input
            v-model="search"
            placeholder="设备、告警码或描述"
            aria-label="搜索告警" /></label
        ><select v-model="alarmStatus" aria-label="告警确认状态">
          <option value="all">全部状态</option>
          <option value="unack">未确认</option>
          <option value="ack">已确认</option>
</select
        ><select v-model="alarmLevel" aria-label="告警级别">
          <option value="all">全部级别</option>
          <option>一级</option>
          <option>二级</option>
          <option>提示</option>
</select
        ><span class="demo-note">固定告警快照 · 确认保存在本机</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>级别</th>
              <th>时间</th>
              <th>设备 / 项目</th>
              <th>告警码</th>
              <th>描述</th>
              <th>来源</th>
              <th>模拟状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in filteredAlarms" :key="a.id">
              <td>
                <span :class="['alarm-level', a.level === '一级' ? 'critical' : '']">{{
                  a.level
                }}</span>
              </td>
              <td>{{ a.time }}</td>
              <td>
                <button class="text-button" @click="inspect(a.device)">
                  {{ a.device.deviceName }}
                </button>
              </td>
              <td>
                <code>{{ a.code }}</code>
              </td>
              <td>{{ a.description }}</td>
              <td><span class="source-badge demo">DEMO TELEMETRY</span></td>
              <td>{{ acknowledged.includes(a.id) ? '已确认' : '未确认' }}</td>
              <td>
                <button
                  :disabled="acknowledged.includes(a.id)"
                  class="text-button"
                  @click="ack(a.id)"
                >
                  {{ acknowledged.includes(a.id) ? '已确认' : '确认告警' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!filteredAlarms.length" class="empty-state">无匹配告警</div>
      </div>
    </template>
    <template v-else-if="page === 'analytics'">
      <div class="analytics-kpis">
        <div>
          <span>平均日运行 · 演示</span><b>9.9<small> h</small></b
          ><em>较上周 +8.2%</em>
        </div>
        <div>
          <span>设备利用率 · 演示</span><b>81<small>%</small></b
          ><em>较上月 +3.0%</em>
        </div>
        <div>
          <span>MTBF · 演示</span><b>728<small> h</small></b
          ><em>平均故障间隔</em>
        </div>
        <div>
          <span>MTTR · 演示</span><b>2.4<small> h</small></b
          ><em>平均修复时间</em>
        </div>
      </div>
      <div class="analytics-grid">
        <section class="chart-card">
          <h3>日运行时长<span>DEMO / h</span></h3>
          <Chart :option="hoursOption" />
        </section>
        <section class="chart-card">
          <h3>月设备利用率<span>DEMO / %</span></h3>
          <Chart :option="utilizationOption" />
        </section>
        <section class="chart-card">
          <h3>地区设备运行排行<span>DEMO / h</span></h3>
          <Chart :option="rankOption" />
        </section>
        <section class="chart-card">
          <h3>运行状态占比<span>演示分布示意</span></h3>
          <Chart :option="ratioOption" />
        </section>
      </div>
      <p class="muted small">
        本页所有分析指标为固定演示样本；真实计划项目不参与运行统计。占比图为方案目标分布示意，非实时设备计数。
      </p>
    </template>
    <template v-else-if="page === 'maintenance'">
      <div class="maintenance-caption">
        <CalendarDays :size="18" /><span>2026 年 10 月 · 演示保养与点检任务</span
        ><small>流程变更仅保存在本机</small>
      </div>
      <div class="kanban">
        <section v-for="stage in ['待维护', '维护中', '已完成']" :key="stage">
          <h3>
            <i :class="stage === '已完成' ? 'green-dot' : 'gold-dot'"></i>{{ stage
            }}<span>{{ tasks.filter((t) => taskStatus(t.id, t.initial) === stage).length }}</span>
          </h3>
          <article
            v-for="t in tasks.filter((t) => taskStatus(t.id, t.initial) === stage)"
            :key="t.id"
          >
            <div>
              <code>{{ t.id }}</code
              ><span class="source-badge demo">演示任务</span>
            </div>
            <h4>{{ t.name }}</h4>
            <button class="text-button" @click="inspect(t.device)">
              {{ t.device.deviceName }}
            </button>
            <p>{{ t.type }} · {{ t.date }}</p>
            <footer>
              <span>{{ t.owner }}</span
              ><button @click="moveTask(t.id, t.initial)">
                {{ stage === '待维护' ? '开始维护' : stage === '维护中' ? '标记完成' : '重新排期'
                }}<ChevronRight :size="12" />
              </button>
            </footer>
          </article>
        </section>
      </div>
    </template>
    <div
      v-if="selected || selectedProject"
      class="drawer-backdrop"
      @click.self="
        selected = undefined;
        selectedProject = undefined;
      "
    >
      <aside class="detail-drawer">
        <ProjectPanel
          v-if="selectedProject"
          :project="selectedProject"
          @close="selectedProject = undefined"
          @select="selectSibling"
          @focus="selectSibling(devices.find((d) => d.id === selectedProject!.id)!.deviceId)"
        />
        <DevicePanel
          v-else-if="selected"
          :device="selected"
          @close="selected = undefined"
          @select="selectSibling"
          @project="inspectProject"
        />
      </aside>
    </div>
    <div v-if="modal" class="modal-backdrop" @click.self="modal = ''">
      <section
        class="confirmation-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="simulation-title"
      >
        <button class="modal-close icon-button" aria-label="关闭确认" @click="modal = ''">
          <X :size="18" />
</button
        ><ShieldCheck :size="32" />
        <h2 id="simulation-title">模拟操作确认</h2>
        <p>设备：{{ activeDevice.deviceName }}</p>
        <strong>模拟{{ modal }}</strong>
        <p>仅在本地记录演示操作，不下发真实设备指令。</p>
        <div>
          <button @click="modal = ''">取消</button
          ><button class="primary-button" @click="confirmSimulation">
            <Check :size="14" />确认模拟
          </button>
        </div>
      </section>
    </div>
  </main>
</template>
