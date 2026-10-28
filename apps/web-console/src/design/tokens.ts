export const palette = {
  brand900: '#06152D', brand800: '#072563', surface950: '#06111E', surface900: '#091725', surface850: '#0D2132',
  blue: '#2C8FFF', cyan: '#31D6E8', success: '#33D1A0', warning: '#F4B740', critical: '#FF5D6C', text: '#EAF4FF', muted: '#8FA8BE',
} as const;
export const statusMapping: Record<string, { label: string; tone: string }> = {
  RUNNING: { label: '运行中', tone: 'neutral' }, STANDBY: { label: '待机', tone: 'neutral' },
  ALARM: { label: '告警', tone: 'critical' }, OFFLINE: { label: '离线', tone: 'muted' },
  MAINTENANCE: { label: '维护中', tone: 'warning' }, NOT_COMMISSIONED: { label: '尚未投运', tone: 'muted' },
  MQTT_CONNECTED: { label: '已连接', tone: 'neutral' }, UNKNOWN: { label: '状态未知', tone: 'muted' },
};
export const severityMapping: Record<string, { label: string; tone: string }> = {
  CRITICAL: { label: '严重', tone: 'critical' }, HIGH: { label: '高', tone: 'critical' },
  WARNING: { label: '警告', tone: 'warning' }, MEDIUM: { label: '中', tone: 'warning' },
  INFO: { label: '提示', tone: 'neutral' }, LOW: { label: '低', tone: 'neutral' },
};
