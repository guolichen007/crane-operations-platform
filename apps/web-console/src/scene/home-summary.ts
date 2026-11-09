// API-mode homepage numbers (H2-1D-2): every value is one field of the authoritative operations
// summary (GET /api/v1/operations/summary). Nothing here is computed from the map's Inventory/Shadow
// view-models, and no two possibly-overlapping server counts are added together.
// Demo mode keeps scene/home-metrics.ts (local fixtures, always tagged DEMO).
import type { OperationsSummary } from '../../../../contracts/typescript/types.gen';
import type { AlarmWindow } from '../cloud/api';
import type { HomeKpi } from './home-metrics';

export const WINDOW_LABEL: Record<AlarmWindow, string> = { PT1H: '近 1 小时', PT6H: '近 6 小时', PT24H: '近 24 小时', P7D: '近 7 天' };

/**
 * Card → server field (H2-1D task table):
 *   registered     metrics.assets.registered        click → Inventory list, same authorization + region (exact)
 *   real-connected metrics.real.connected           click → Inventory list, marked "not the exact KPI set"
 *   alarm-real     metrics.alarm.devices.REAL       click → Inventory list, marked "not the exact KPI set"
 *   pending-review metrics.real.pendingReview       click → server-provided review asset ids (≤100 of N)
 *   no-runtime     metrics.assets.noRuntime         click → Inventory list, marked "not the exact KPI set"
 */
export function summaryKpis(s: OperationsSummary | null): HomeKpi[] {
  const m = s?.metrics;
  const v = (n: number | undefined) => (m ? (n as number) : null);
  const window = s ? WINDOW_LABEL[s.freshness.alarmWindow] : '';
  return [
    {
      id: 'registered',
      label: 'Inventory 登记设备',
      value: v(m?.assets.registered),
      unit: '台',
      detail: m ? `${m.projects} 个项目 · 草稿 ${m.assets.draft} · 停用 ${m.assets.outOfService}` : '—',
      filter: 'all',
      tone: 'neutral',
      tag: 'INVENTORY',
      hint: '服务端统计：授权范围内未退役的 Inventory 资产。点击打开同授权、同行政区的 Inventory 资产清单',
    },
    {
      id: 'real-connected',
      label: 'REAL 在线',
      value: v(m?.real.connected),
      unit: '台',
      detail: m ? `REAL 绑定 ${m.bound.REAL} · SIM 绑定 ${m.bound.SIMULATION}（SIM 不计入）` : '—',
      filter: 'all',
      tone: 'ok',
      hint: '服务端统计：ACTIVE 资产上至少一个 REAL 运行身份连接且观测时间在新鲜度内。SIM/TEST 在线不计入；不代表正在作业',
    },
    {
      id: 'alarm-real',
      label: '当前 REAL 告警',
      value: v(m?.alarm.devices.REAL),
      unit: '台',
      detail: m ? `${window}告警记录 ${m.alarm.records.total} 条（全部来源）` : '—',
      filter: 'all',
      tone: 'critical',
      hint: '卡片数字：REAL 在线且当前告警的 ACTIVE 资产数。下方为时间窗内的告警记录总数（服务端计数，不是最近 100 条明细的长度，也不是未处理数）',
    },
    {
      id: 'pending-review',
      label: '待复核设备',
      value: v(m?.real.pendingReview),
      unit: '台',
      detail: m ? `草稿/停用资产上的 REAL 在线 · 其中告警 ${m.real.pendingReviewAlarm}` : '—',
      filter: 'PENDING_REVIEW',
      tone: 'warning',
      hint: '服务端统计：DRAFT 或 OUT_OF_SERVICE 资产上出现新鲜的 REAL 连接，需要确认资产状态。点击列出服务端返回的资产（最多 100 条）',
    },
    {
      id: 'no-runtime',
      label: '无运行身份',
      value: v(m?.assets.noRuntime),
      unit: '台',
      detail: '未绑定任何 REAL / SIM / TEST 运行身份',
      filter: 'all',
      tone: 'muted',
      hint: '服务端统计：登记资产中没有任何运行身份绑定的数量',
    },
  ];
}

export type AttentionRow = { id: string; label: string; value: number | null; filter?: string; color: string; hint: string };
/**
 * "需要关注" rows. Each is ONE independent server field; REAL offline / stale / unknown time can overlap
 * on one asset (several REAL identities), so they are listed separately and never summed.
 */
export function summaryAttention(s: OperationsSummary | null): AttentionRow[] {
  const r = s?.metrics.real;
  const n = (x: number | undefined) => (s ? (x as number) : null);
  return [
    { id: 'pending-review-alarm', label: '待复核且告警', value: n(r?.pendingReviewAlarm), filter: 'PENDING_REVIEW', color: 'var(--critical)', hint: '草稿/停用资产上的 REAL 在线并告警' },
    { id: 'real-offline', label: 'REAL 离线', value: n(r?.offline), color: '#7b8ea3', hint: '有 REAL 身份报告离线的资产数' },
    { id: 'real-stale', label: 'REAL 数据过期', value: n(r?.stale), color: 'var(--warning)', hint: 'REAL 报告连接但最后观测时间超出新鲜度阈值' },
    { id: 'real-unknown-time', label: 'REAL 时间未知', value: n(r?.unknownTime), color: '#8a7fa8', hint: 'REAL 报告连接但没有可用的观测时间' },
    { id: 'unlocated', label: '无空间位置', value: n(s?.metrics.assets.unlocated), color: '#5c7791', hint: '所属项目没有可识别的行政区代码；只在全国范围计入' },
  ];
}

/** Region partition shown next to the region navigation: Σ children + scope level + unlocated = registered. */
export function regionPartition(s: OperationsSummary) {
  const children = s.regions.reduce((n, r) => n + r.registered, 0);
  return { children, atScopeLevel: s.metrics.assets.atScopeLevel, unlocated: s.metrics.assets.unlocated, registered: s.metrics.assets.registered };
}
