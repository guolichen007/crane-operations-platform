<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, shallowRef } from 'vue';
import { useRoute } from 'vue-router';
import type { Shadow } from '../../../../contracts/typescript/types.gen';
import { listDevices, getDevice, listAssets, listProjects } from '../cloud/api';
import { formatCloudTime, deviceState, silentExplanation } from '../cloud/format';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { assetSource } from '../cloud/inventory';
import { LiveViewer, type ViewerSnapshot } from '../cloud/LiveViewer';
import { canWatchProject, productionAuth } from '../cloud/session';
import CraneTwin from '../components/CraneTwin.vue';
import AxisGauge from '../components/AxisGauge.vue';
const route = useRoute();
const { data: metadata } = useCloudQuery(async () => {
  const [assets, projects] = await Promise.all([listAssets(), listProjects()]);
  return { assets, projects };
});
const devices = ref<Shadow[]>([]),
  current = ref<Shadow>(),
  error = ref('');
const viewer = shallowRef<ViewerSnapshot>({ state: 'STOPPED', error: '' });
const detail = computed(() => (typeof route.params.id === 'string' ? route.params.id : ''));
let disposed = false,
  revision = 0,
  poll: ReturnType<typeof setInterval>;
// Storage may be disabled by browser policy. TTL still limits the lease in that case.
const storage = {
  getItem: (key: string) => window.sessionStorage.getItem(key),
  setItem: (key: string, value: string) => window.sessionStorage.setItem(key, value),
  removeItem: (key: string) => window.sessionStorage.removeItem(key),
};
const live = new LiveViewer({
  storage,
  target: window,
  onChange: (value) => {
    viewer.value = value;
  },
  onShadow: (shadow) => {
    current.value = shadow;
  },
});
async function refresh() {
  const request = ++revision,
    id = detail.value;
  try {
    if (id) {
      const shadow = await getDevice(id);
      if (disposed || request !== revision || id !== detail.value) return;
      // A slower poll must not replace a more recent stream frame.
      if (!current.value || shadow.version >= current.value.version) current.value = shadow;
      live.pollingAvailable();
    } else {
      const result = await listDevices();
      if (disposed || request !== revision) return;
      devices.value = result.sort((a, b) => a.deviceId.localeCompare(b.deviceId));
    }
    error.value = '';
  } catch (e) {
    if (!disposed && request === revision) error.value = (e as Error).message;
  }
}
function sessionExpired() {
  ++revision;
  current.value = undefined;
  devices.value = [];
}
watch(detail, () => {
  ++revision;
  current.value = undefined;
  void live.stop();
  void refresh();
});
onMounted(async () => {
  try {
    await live.initialize();
  } catch {
    /* The next LIVE attempt retries stale cleanup. */
  }
  if (disposed) return;
  void refresh();
  poll = setInterval(() => void refresh(), 15000);
  window.addEventListener('cloud-authenticated', refresh);
  window.addEventListener('cloud-session-expired', sessionExpired);
});
onUnmounted(() => {
  disposed = true;
  ++revision;
  clearInterval(poll);
  window.removeEventListener('cloud-authenticated', refresh);
  window.removeEventListener('cloud-session-expired', sessionExpired);
  void live.dispose();
});
const asset = computed(() =>
  metadata.value?.assets.find((a) => a.runtimeDeviceId === detail.value)
);
const projectName = computed(
  () => metadata.value?.projects.find((p) => p.id === asset.value?.projectId)?.name
);
const active = computed(() => viewer.value.state !== 'STOPPED');
const streamHeading = computed(() => {
  if (viewer.value.state === 'STOPPED') return 'Last Known State · 最近状态';
  if (viewer.value.state === 'RECONNECTING') return '实时流中断 · 正在重连';
  if (viewer.value.state === 'POLLING_FALLBACK') return '最近状态可用 · 实时流正在恢复';
  if (viewer.value.state === 'CONNECTING') return '正在连接实时流';
  return `LIVE 上游：${current.value?.upstreamStatus}`;
});
</script>
<template>
  <main class="cloud-devices">
    <p class="eyebrow">CLOUD SHADOW / MQTT 5 · TLS</p>
    <h1>{{ detail ? `设备 ${detail}` : '云端设备台账' }}</h1>
    <p role="status">
      {{
        error ||
        viewer.error ||
        (detail
          ? 'Cloud API · 运行身份与业务资产分开管理'
          : `已接收 ${devices.length} 个运行身份；运行源不等于现场设备，读取缓存不唤醒现场`)
      }}
    </p>
    <p class="silent-note">{{ silentExplanation }}</p>
    <template v-if="detail">
      <router-link to="/runtime">← 返回Runtime台账</router-link>
      <p v-if="asset">
        {{ projectName }} · {{ asset.displayName }} · {{ asset.craneModel ?? '型号待补充' }} ·
        数据来源：{{ assetSource(asset) }}
      </p>
      <p v-else>未关联业务资产 · 历史/测试运行源；不代表真实现场设备。</p>
      <div v-if="current" class="device-detail-layout">
        <div class="device-twin-column">
          <CraneTwin
            :key="detail"
            :axes="current.reported.position"
            :alarm="current.reported.alarmState === 'ACTIVE'"
            :label="'设备 ' + detail + ' 结构示意 · 最近状态'"
          />
          <div class="axis-gauge-grid">
            <AxisGauge
              axis="X"
              label="大车位置"
              :value="current.reported.position.x"
              :min="-12"
              :max="12"
            /><AxisGauge
              axis="Y"
              label="小车位置"
              :value="current.reported.position.y"
              :min="-5"
              :max="5"
            /><AxisGauge
              axis="Z"
              label="起升位置"
              :value="current.reported.position.z"
              :min="0"
              :max="10"
            /><AxisGauge axis="G" label="抓斗开度" :max="100" unit="%" />
          </div>
          <p class="inspector-copy">
            通用结构示意，非实际几何标定。坐标超出可视范围时模型限制显示范围，右侧保留原始读数。抓斗遥测与历史趋势尚未提供。
          </p>
          <section class="glass-panel">
            <div class="panel-heading">
              <h2>状态时间线</h2>
              <small>LAST KNOWN STATE</small>
            </div>
            <p class="inspector-copy">最近快照 · {{ formatCloudTime(current.updatedAt) }}</p>
            <p class="inspector-copy">
              最近心跳 · {{ formatCloudTime(current.presence.lastSeenAt) }}
            </p>
          </section>
        </div>
        <section
          v-if="current"
          class="shadow-card"
          data-testid="device-shadow"
          :data-shadow-version="current.version"
        >
          <h2 :data-stream-state="viewer.state">{{ streamHeading }}</h2>
          <p>
            最后更新时间：<time :title="current.updatedAt" :datetime="current.updatedAt">{{
              formatCloudTime(current.updatedAt)
            }}</time
            >（Asia/Shanghai） · 版本 {{ current.version }}
          </p>
          <p>
            {{ deviceState(current) }} / {{ current.presence.edgeHealth }} /
            {{ current.presence.controllerHealth }}
          </p>
          <p>
            最后心跳：<time
              :title="current.presence.lastSeenAt"
              :datetime="current.presence.lastSeenAt"
              >{{ formatCloudTime(current.presence.lastSeenAt) }}</time
            >
          </p>
          <p>
            采集状态 {{ current.reported.operatingState }} · 下线原因
            {{ current.presence.disconnectReason }}
          </p>
          <dl>
            <dt>X / Y / Z</dt>
            <dd>
              {{ current.reported.position.x.toFixed(2) }} /
              {{ current.reported.position.y.toFixed(2) }} /
              {{ current.reported.position.z.toFixed(2) }} m
            </dd>
            <dt>负载</dt>
            <dd>{{ current.reported.load }} t</dd>
          </dl>
          <button
            :disabled="!active && !canWatchProject(current.projectId)"
            @click="active ? live.stop() : live.start(detail)"
          >
            {{ active ? '停止实时查看' : '申请 LIVE · 1 Hz' }}
          </button>
          <p v-if="productionAuth && !canWatchProject(current.projectId)">当前项目为只读权限。</p>
          <p v-if="viewer.session">
            租约到期：<time
              :title="viewer.session.expiresAt"
              :datetime="viewer.session.expiresAt"
              >{{ formatCloudTime(viewer.session.expiresAt) }}</time
            >。上游仅在设备应用回执确认后显示 LIVE。
          </p>
          <p>
            离开/刷新会尽力关闭 Viewer；浏览器异常或断网时由云端 TTL + Grace
            兜底。刷新后回到最近状态。
          </p>
          <p>Point Cloud / Remote Local Web：Phase 2 未启用。</p>
          <router-link to="/alarms">Alarm · 告警记录 →</router-link>
        </section>
      </div>
    </template>
    <div v-else class="device-grid">
      <router-link
        v-for="device in devices"
        :key="device.deviceId"
        :to="`/devices/${device.deviceId}`"
        class="shadow-card"
        data-testid="cloud-device"
      >
        <h2>{{ device.deviceId }}</h2>
        <p>{{ device.projectId }}</p>
        <p :title="`${device.presence.connectivity} · ${device.reported.operatingState}`">
          {{ deviceState(device) }}
        </p>
        <small
          >最近状态
          <time :title="device.updatedAt" :datetime="device.updatedAt">{{
            formatCloudTime(device.updatedAt)
          }}</time>
          · 上海</small
        >
      </router-link>
    </div>
  </main>
</template>
<style scoped>
.cloud-devices {
  padding: 42px;
  color: #cce6ed;
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding-bottom: calc(160px + env(safe-area-inset-bottom, 0px));
  scroll-padding-bottom: 160px;
}
.eyebrow {
  font-size: 11px;
  letter-spacing: 3px;
  color: #6ecbd0;
}
h1 {
  font-size: 28px;
  margin: 14px 0;
}
h2 {
  font-size: 18px;
  margin-bottom: 12px;
}
p {
  margin: 12px 0;
  line-height: 1.6;
}
.device-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 18px;
  margin-top: 28px;
}
.shadow-card {
  display: block;
  margin-top: 18px;
  border: 1px solid #28505b;
  padding: 24px;
  background: #102730;
  border-radius: 6px;
  color: inherit;
  text-decoration: none;
}
a {
  color: #7bd8db;
}
dt {
  margin-top: 18px;
  color: #789da9;
}
dd {
  font-size: 24px;
  margin: 10px 0 22px;
}
</style>
