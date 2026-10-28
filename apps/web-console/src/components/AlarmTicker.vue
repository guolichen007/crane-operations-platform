<script setup lang="ts">
import type { CloudAlarm } from '../cloud/api';
import { formatCloudTime } from '../cloud/format';
import { ArrowUpRight } from 'lucide-vue-next';
defineProps<{ alarms: CloudAlarm[] }>();
</script>
<template><div class="alarm-ticker"><RouterLink v-for="a in alarms.slice(0, 4)" :key="a.messageId" :to="'/devices/' + encodeURIComponent(a.deviceId)"><span class="event-point"></span><div><b>{{ a.deviceId }}</b><p>{{ a.payload.text }}</p><small>{{ formatCloudTime(a.sentAt) }}</small></div><ArrowUpRight :size="13" /></RouterLink><p v-if="!alarms.length" class="no-data">暂无上报告警记录</p></div></template>
