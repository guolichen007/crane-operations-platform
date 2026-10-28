<script setup lang="ts">
import { computed, ref } from 'vue';
import { listAlarms } from '../cloud/api';
import { formatCloudTime } from '../cloud/format';
import { useCloudQuery } from '../cloud/useCloudQuery';
import SeverityBadge from './SeverityBadge.vue';
import StatusPill from './StatusPill.vue';
const { data, error, loading, refresh } = useCloudQuery(listAlarms);
const search = ref('');
const device = ref(''), date = ref('');
const deviceIds = computed(() => [...new Set((data.value ?? []).map(a => a.deviceId))]);
const filtered = computed(() =>
  (data.value ?? []).filter((a) =>
    (!device.value || a.deviceId === device.value) && (!date.value || a.sentAt.startsWith(date.value)) &&
    `${a.deviceId} ${a.payload.code} ${a.payload.text}`
      .toLowerCase()
      .includes(search.value.toLowerCase())
  )
);
</script>
<template>
  <section data-testid="cloud-alarms">
    <div class="alarm-summary-strip"><div><span>最近上报记录</span><b>{{ data?.length ?? '—' }}</b></div><div><span>关联设备</span><b>{{ deviceIds.length }}</b></div><div><span>处理状态</span><strong>API 尚未提供</strong></div><div><span>告警分级</span><strong>源协议尚未提供</strong></div></div>
    <p class="cloud-alarm-note">
      数据来源：Cloud API · 最近最多100条已上报告警记录；API
      未提供处理状态或云端确认能力。时间：Asia/Shanghai。
    </p>
    <div class="filter-toolbar">
      <label class="search-box"
        ><input
          v-model="search"
          aria-label="搜索云端告警"
          placeholder="设备、告警码或描述" /></label
      ><button @click="refresh">刷新告警</button>
      <select v-model="device" aria-label="告警设备筛选"><option value="">全部设备</option><option v-for="id in deviceIds" :key="id">{{ id }}</option></select>
      <input v-model="date" type="date" aria-label="告警日期" />
    </div>
    <p v-if="error" role="status">{{ error }}{{ data ? '；以下保留上次读取的记录' : '' }}</p>
    <p v-else-if="loading" role="status">正在读取 Cloud Alarm…</p>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>设备编号</th>
            <th>告警码</th>
            <th>等级</th>
            <th>描述</th>
            <th>上报时间 · 上海</th>
            <th>来源</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="alarm in filtered" :key="alarm.messageId" data-testid="cloud-alarm">
            <td>
              <router-link :to="`/devices/${encodeURIComponent(alarm.deviceId)}`">{{
                alarm.deviceId
              }}</router-link>
            </td>
            <td>{{ alarm.payload.code }}</td>
            <td><SeverityBadge severity="未分级" /></td>
            <td>{{ alarm.payload.text }}</td>
            <td>
              <time :title="alarm.sentAt" :datetime="alarm.sentAt">{{
                formatCloudTime(alarm.sentAt)
              }}</time>
            </td>
            <td><StatusPill label="Cloud API" /></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="!loading && !error && !filtered.length" class="empty-state">无匹配的云端告警记录</p>
  </section>
</template>
<style scoped>
.cloud-alarm-note {
  color: #92a4af;
  margin-bottom: 18px;
  line-height: 1.7;
}
</style>
