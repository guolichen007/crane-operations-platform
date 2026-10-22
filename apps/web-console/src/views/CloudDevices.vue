<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { useRoute } from 'vue-router';
import type { Shadow, LiveSession } from '../../../../contracts/typescript/types.gen';
const route = useRoute();
const devices = ref<Shadow[]>([]), current = ref<Shadow>(), session = ref<LiveSession>(), error = ref(''), busy = ref(false);
const detail = computed(() => typeof route.params.id === 'string' ? route.params.id : '');
let poll: ReturnType<typeof setInterval>, renew: ReturnType<typeof setInterval> | undefined, socket: WebSocket | undefined;
async function api<T>(path: string, method = 'GET'): Promise<T> {
  const response = await fetch('/api/v1/' + path, { method, credentials: 'same-origin' });
  if (!response.ok) throw Error(response.status === 401 ? '请先使用底部开发凭据连接云端' : `云端请求失败 (${response.status})`);
  return response.status === 204 ? undefined as T : response.json();
}
async function refresh() {
  try { devices.value = (await api<Shadow[]>('devices')).sort((a,b) => a.deviceId.localeCompare(b.deviceId)); current.value = devices.value.find(d => d.deviceId === detail.value); error.value = ''; }
  catch (e) { error.value = (e as Error).message; }
}
async function stop() {
  socket?.close(); socket = undefined; if (renew) clearInterval(renew);
  const id = session.value?.sessionId; session.value = undefined;
  if (id) { try { await api(`live-sessions/${id}`, 'DELETE'); } catch { /* edge lease is bounded even after network loss */ } }
}
async function start() {
  busy.value = true;
  try {
    await stop();session.value = await api<LiveSession>(`devices/${detail.value}/live-sessions`, 'POST');
    socket = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/v1/devices/${detail.value}/stream`);
    socket.onmessage = (event) => { current.value = JSON.parse(event.data) as Shadow; };
    socket.onclose = () => { if (session.value) error.value = '连接中断，展示最近状态；租约会自动到期'; };
    renew = setInterval(async () => { if (!session.value) return; try { session.value = await api<LiveSession>(`live-sessions/${session.value.sessionId}/renew`, 'POST'); } catch { await stop();error.value = '续租失败，已停止实时查看'; } }, 60000);
  } catch (e) { error.value = (e as Error).message; } finally { busy.value = false; }
}
watch(detail, async () => { await stop();await refresh(); });
onMounted(() => { void refresh(); poll = setInterval(refresh, 15000);window.addEventListener('cloud-authenticated', refresh); });
onUnmounted(() => { clearInterval(poll);window.removeEventListener('cloud-authenticated', refresh);void stop(); });
</script>
<template>
  <main class="cloud-devices">
    <p class="eyebrow">CLOUD SHADOW / MQTT 5 · TLS</p><h1>{{ detail ? `设备 ${detail}` : '云端设备台账' }}</h1>
    <p role="status">{{ error || `已接收 ${devices.length} 台模拟设备；读取云端缓存，不唤醒现场` }}</p>
    <template v-if="detail">
      <router-link to="/devices">← 返回台账</router-link>
      <section v-if="current" class="shadow-card" data-testid="device-shadow">
        <h2>{{ session ? 'LIVE 租约已申请' : 'Last Known State · 最近状态' }}</h2>
        <p>最后更新时间：<time>{{ current.updatedAt }}</time> · 版本 {{ current.version }}</p>
        <p>{{ current.reported.connectivity }} / {{ current.reported.edgeHealth }} / {{ current.reported.controllerHealth }}</p>
        <p>采集状态 {{ current.reported.operatingState }} · 最近消息 {{ current.reported.kind }}</p>
        <dl><dt>X / Y / Z</dt><dd>{{ current.reported.x.toFixed(2) }} / {{ current.reported.y.toFixed(2) }} / {{ current.reported.z.toFixed(2) }} m</dd><dt>负载</dt><dd>{{ current.reported.load }} t</dd></dl>
        <button :disabled="busy" @click="session ? stop() : start()">{{ session ? '停止实时查看' : '申请 LIVE · 1 Hz' }}</button>
        <p v-if="session">租约到期：{{ session.expiresAt }}。只有近期 LIVE 消息才代表实时采样。</p>
        <p>Point Cloud / Remote Local Web：Phase 2 未启用。</p>
        <router-link to="/alarms">Alarm · 告警记录 →</router-link>
      </section>
    </template>
    <div v-else class="device-grid">
      <router-link v-for="device in devices" :key="device.deviceId" :to="`/devices/${device.deviceId}`" class="shadow-card" data-testid="cloud-device">
        <h2>{{ device.deviceId }}</h2><p>{{ device.projectId }}</p><p>{{ device.reported.connectivity }} · {{ device.reported.operatingState }}</p><small>最近状态 {{ device.updatedAt }}</small>
      </router-link>
    </div>
  </main>
</template>
<style scoped>
.cloud-devices { padding: 42px; color: #cce6ed; max-height: calc(100vh - 70px); overflow: auto; padding-bottom: 130px; }
.eyebrow { font-size: 11px; letter-spacing: 3px; color: #6ecbd0; } h1 { font-size: 28px; margin: 14px 0; } h2 { font-size: 18px; margin-bottom: 12px; } p { margin: 12px 0; line-height: 1.6; }
.device-grid { display: grid; grid-template-columns: repeat(auto-fill,minmax(260px,1fr)); gap: 18px; margin-top: 28px; }
.shadow-card { display: block; margin-top: 18px; border: 1px solid #28505b; padding: 24px; background: #102730; border-radius: 6px; color: inherit; text-decoration: none; } a { color: #7bd8db; } dt { margin-top: 18px; color: #789da9; } dd { font-size: 24px; margin: 10px 0 22px; }
</style>
