// G4-S5-R0: list and pipeline share one filter vocabulary kept in the URL, so
// switching views, opening a project, going back, refreshing or sharing a link
// all keep the same filters. Pure functions, unit tested in view-state.test.ts.
import type { LocationQuery, LocationQueryRaw } from 'vue-router';

export interface ViewFilters {
  q: string;
  status: string;
  stage: string; // list only; the pipeline shows every stage as a column
  province: string;
  after: string; // yyyy-mm-dd
  sort: 'updated' | 'name';
  page: number;
}

const one = (v: LocationQuery[string]) => (Array.isArray(v) ? v[0] ?? '' : v ?? '');

export function readFilters(query: LocationQuery): ViewFilters {
  const page = Number.parseInt(one(query.page), 10);
  return {
    q: one(query.q).slice(0, 200),
    status: one(query.status),
    stage: one(query.stage),
    province: one(query.province).slice(0, 64),
    after: /^\d{4}-\d{2}-\d{2}$/.test(one(query.after)) ? one(query.after) : '',
    sort: one(query.sort) === 'name' ? 'name' : 'updated',
    page: Number.isSafeInteger(page) && page > 1 && page <= 10000 ? page : 1,
  };
}

// Only non-default values go into the URL.
export function writeFilters(f: Partial<ViewFilters>): LocationQueryRaw {
  const q: LocationQueryRaw = {};
  if (f.q) q.q = f.q;
  if (f.status) q.status = f.status;
  if (f.stage) q.stage = f.stage;
  if (f.province) q.province = f.province;
  if (f.after) q.after = f.after;
  if (f.sort === 'name') q.sort = 'name';
  if (f.page && f.page > 1) q.page = String(f.page);
  return q;
}

// Switching view keeps the shared filters. The pipeline has no single stage
// filter and no paging; the list restarts at page 1.
export function toPipeline(f: ViewFilters): LocationQueryRaw {
  return writeFilters({ q: f.q, status: f.status, province: f.province, after: f.after });
}
export function toList(f: ViewFilters): LocationQueryRaw {
  return writeFilters({ q: f.q, status: f.status, province: f.province, after: f.after, sort: f.sort });
}

// A detail page remembers where it was opened from (path + query), as a local
// path only. Anything else falls back to the list.
const sources = ['/projects', '/pipeline', '/followups', '/unassigned', '/recycle-bin', '/reviews', '/duplicates', '/statistics', '/'];
export function safeReturn(raw: unknown): string {
  if (typeof raw !== 'string' || !raw.startsWith('/') || raw.startsWith('//') || raw.length > 600) return '/projects';
  const path = raw.split('?')[0].split('#')[0];
  return sources.includes(path) ? raw : '/projects';
}
export function returnLabel(target: string, listLabel: string) {
  const path = target.split('?')[0];
  return ({
    '/pipeline': '返回项目管道', '/followups': '返回待跟进', '/unassigned': '返回待分配草稿', '/recycle-bin': '返回草稿回收站', '/reviews': '返回报备审核',
    '/duplicates': '返回重复处理', '/statistics': '返回统计', '/': '返回工作台',
  } as Record<string, string>)[path] ?? '返回' + listLabel;
}
