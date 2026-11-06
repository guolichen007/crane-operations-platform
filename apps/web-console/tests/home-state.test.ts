import { describe, it, expect } from 'vitest';
import { parseRegionParam, regionPathOf, planRegionTransition, samePath, regionParamOf, type RegionManifest } from '../src/scene/region-path';
import { parseHomeQuery, homeQueryFor, type HomeUrlState } from '../src/scene/home-url';
import { safeRatio, homeMetrics, hasLocation, parseStatusParam, parseViewParam, homeDataState, hasNumbers, homeKpis, scopeProjects, scopeLabel, inRegion } from '../src/scene/home-metrics';
import type { Device, Project } from '../src/data/types';

const manifest: RegionManifest = {
  100000: { adcode: 100000, parent: null, level: 'country' },
  330000: { adcode: 330000, parent: 100000, level: 'province' },
  330400: { adcode: 330400, parent: 330000, level: 'prefecture' },
  330481: { adcode: 330481, parent: 330400, level: 'county' },
  440000: { adcode: 440000, parent: 100000, level: 'province' },
  469023: { adcode: 469023, parent: 460000, level: 'county' }, // parent missing from manifest
  111111: { adcode: 111111, parent: 222222, level: 'county' },
  222222: { adcode: 222222, parent: 111111, level: 'prefecture' }, // cycle
};

describe('region-path', () => {
  it('parses only 6-digit adcodes; country is the empty path', () => {
    expect(parseRegionParam('330481')).toBe(330481);
    expect(parseRegionParam('100000')).toBeNull();
    for (const bad of ['', '33048', '3304811', 'abc', '330481x', undefined, 330481, null]) expect(parseRegionParam(bad)).toBeNull();
  });
  it('resolves the full province→prefecture→county path', () => {
    expect(regionPathOf(330481, manifest)).toEqual([330000, 330400, 330481]);
    expect(regionPathOf(330000, manifest)).toEqual([330000]);
  });
  it('unknown code, broken parent chain or cycle → null (never a partial path)', () => {
    expect(regionPathOf(999999, manifest)).toBeNull();
    expect(regionPathOf(469023, manifest)).toBeNull();
    expect(regionPathOf(111111, manifest)).toBeNull();
  });
  it('plans the minimal back/enter steps', () => {
    expect(planRegionTransition([], [330000, 330400, 330481])).toEqual({ backTo: null, enter: [330000, 330400, 330481] });
    expect(planRegionTransition([330000, 330400, 330481], [330000])).toEqual({ backTo: 1, enter: [] });
    expect(planRegionTransition([330000, 330400], [440000])).toEqual({ backTo: 0, enter: [440000] });
    expect(planRegionTransition([330000], [330000, 330400])).toEqual({ backTo: null, enter: [330400] });
    expect(planRegionTransition([330000, 330400], [330000, 330400])).toEqual({ backTo: null, enter: [] });
    expect(planRegionTransition([330000], [])).toEqual({ backTo: 0, enter: [] });
  });
  it('samePath / regionParamOf', () => {
    expect(samePath([1, 2], [1, 2])).toBe(true);
    expect(samePath([1, 2], [1])).toBe(false);
    expect(regionParamOf([])).toBeUndefined();
    expect(regionParamOf([330000, 330481])).toBe('330481');
  });
});

const state = (s: Partial<HomeUrlState> = {}): HomeUrlState => ({
  regionPath: [], regionTarget: [], selectedId: null, selectedProjectId: null, status: 'all', view: '3d', ...s,
});

describe('home-url', () => {
  it('parses and flags malformed values', () => {
    expect(parseHomeQuery({ region: '330481', status: 'ALARM', view: '2d', asset: ' A1 ' })).toMatchObject({
      region: 330481, regionMalformed: false, status: 'ALARM', statusMalformed: false, view: '2d', asset: 'A1', project: null,
    });
    expect(parseHomeQuery({ region: 'zz', status: 'NOPE' })).toMatchObject({ region: null, regionMalformed: true, status: null, statusMalformed: true, view: '3d' });
    expect(parseHomeQuery({ region: '100000' })).toMatchObject({ region: null, regionMalformed: false });
  });
  it('no write when the URL already matches the state (no loop)', () => {
    expect(homeQueryFor(state({ regionPath: [330000, 330400, 330481], regionTarget: [330000, 330400, 330481] }), { region: '330481' })).toBeNull();
    expect(homeQueryFor(state(), {})).toBeNull();
  });
  it('drill → push region; preserves unrelated keys', () => {
    const w = homeQueryFor(state({ regionPath: [330000], regionTarget: [330000] }), { other: 'x' });
    expect(w).toEqual({ query: { other: 'x', region: '330000' }, history: 'push' });
  });
  it('reset to country while the scene is still deep: region is removed (no stale value to re-apply)', () => {
    expect(homeQueryFor(state({ regionPath: [330000, 330400, 330481], regionTarget: [] }), { region: '330481', asset: 'D1' })).toEqual({ query: {}, history: 'push' });
  });
  it('does not write intermediate levels while converging to a deep link', () => {
    // URL asks for 330481; scene has only reached 330000 so far.
    expect(homeQueryFor(state({ regionPath: [330000], regionTarget: [330000, 330400, 330481] }), { region: '330481' })).toBeNull();
  });
  it('selection: device wins over project; status/view replace', () => {
    expect(homeQueryFor(state({ selectedId: 'D1', selectedProjectId: 'P1' }), {})).toEqual({ query: { asset: 'D1' }, history: 'push' });
    expect(homeQueryFor(state({ selectedProjectId: 'P1' }), { asset: 'D1' })).toEqual({ query: { project: 'P1' }, history: 'push' });
    expect(homeQueryFor(state({ status: 'ALARM' }), {})).toEqual({ query: { status: 'ALARM' }, history: 'replace' });
    expect(homeQueryFor(state({ view: '2d' }), { status: 'ALARM' })).toEqual({ query: { view: '2d' }, history: 'replace' });
  });
});

const project = (p: Partial<Project>): Project => ({
  id: 'P', projectName: 'P', dataSource: 'real', province: '', city: null, county: null, locationLabel: '', approximateLng: 0, approximateLat: 0,
  craneModel: '', ratedCapacityT: 0, spanM: 0, workClass: '', grabType: '', quantity: 1, lifecycle: 'PLANNED', plannedImplementation: '',
  scheduleText: '', coordinateAccuracy: 'city_center_demo', adminPath: [], ...p,
} as Project);
const device = (d: Partial<Device>): Device => ({ ...project({}), telemetrySource: 'cloud', runtimeSource: null, commissioningStatus: 'NOT_COMMISSIONED', grab: null, mode: null, fault: '', deviceId: 'D', deviceName: 'D', status: 'STANDBY', todayHours: NaN, totalHours: NaN, load: NaN, x: NaN, y: NaN, z: NaN, heartbeat: '', ...d } as Device);

describe('home-metrics', () => {
  it('safeRatio never returns NaN/Infinity', () => {
    expect(safeRatio(1, 0)).toBeNull();
    expect(safeRatio(0, 0)).toBeNull();
    expect(safeRatio(NaN, 2)).toBeNull();
    expect(safeRatio(1, Infinity)).toBeNull();
    expect(safeRatio(1, 4)).toBe(0.25);
  });
  it('empty data → all zero, no NaN', () => {
    const m = homeMetrics([], []);
    expect(Object.values(m).every((v) => v === 0)).toBe(true);
  });
  it('counts by the frozen status rules and REAL/SIM isolation', () => {
    const m = homeMetrics([project({})], [
      device({ runtimeSource: 'REAL', status: 'RUNNING', adminPath: [330000] }),
      device({ runtimeSource: 'SIMULATION', status: 'RUNNING' }),
      device({ runtimeSource: 'REAL', status: 'OFFLINE' }),
      device({ status: 'ALARM', runtimeSource: 'TEST' }),
      device({ status: 'NOT_COMMISSIONED' }),
    ]);
    expect(m).toMatchObject({ devices: 5, realRunning: 1, realOnline: 1, simulationBound: 1, testBound: 1, alarmDevices: 1, offline: 1, notCommissioned: 1, unlocated: 4 });
  });
  it('hasLocation / param parsers', () => {
    expect(hasLocation(project({ adminPath: [330000] }))).toBe(true);
    expect(hasLocation(project({ approximateLng: 120 }))).toBe(true);
    expect(hasLocation(project({}))).toBe(false);
    expect(parseStatusParam('REAL_RUNNING')).toBe('REAL_RUNNING');
    expect(parseStatusParam('bogus')).toBeNull();
    expect(parseViewParam('2d')).toBe('2d');
    expect(parseViewParam('x')).toBe('3d');
  });
});

describe('H1-R1 data state (no fabricated 0)', () => {
  const st = (loading: boolean, error: string, fetchedAt: string) => homeDataState({ mode: 'api', loading, error, fetchedAt });
  it('maps load lifecycle to a state; numbers only after a success', () => {
    expect(st(true, '', '')).toBe('loading');
    expect(st(false, '503', '')).toBe('unavailable');
    expect(st(false, '', 'T')).toBe('ready');
    expect(st(true, '', 'T')).toBe('refreshing');
    expect(st(false, '503', 'T')).toBe('stale');
    expect(homeDataState({ mode: 'demo', loading: false, error: '', fetchedAt: '' })).toBe('ready');
    expect(['loading', 'unavailable'].map((s) => hasNumbers(s as never))).toEqual([false, false]);
    expect(['ready', 'refreshing', 'stale'].map((s) => hasNumbers(s as never))).toEqual([true, true, true]);
  });
  it('no metrics → every KPI value is null (never 0)', () => {
    for (const mode of ['api', 'demo'] as const)
      for (const k of homeKpis(mode, null, null, 0).filter((k) => k.id !== 'planned')) expect(k.value).toBeNull();
    expect(homeKpis('api', null, 5, 0).find((k) => k.id === 'alarm')!.detail).toBe('告警记录未加载');
  });
});

describe('H1-R1 statistics scope contract', () => {
  const zj = project({ id: 'ZJ', adminPath: [330000, 330400, 330481] });
  const gd = project({ id: 'GD', adminPath: [440000, 440300] });
  const demoGd = project({ id: 'DG', dataSource: 'demo', adminPath: [440000, 440100] });
  const nowhere = project({ id: 'NO' });
  it('region prefix match on the administrative path', () => {
    expect(inRegion([330000, 330400], [])).toBe(true);
    expect(inRegion([330000, 330400], [330000])).toBe(true);
    expect(inRegion([330000], [330000, 330400])).toBe(false);
    expect(inRegion([], [330000])).toBe(false);
  });
  it('base set = source ∩ region; unlocated projects only count at the country level', () => {
    const all = [zj, gd, demoGd, nowhere];
    expect(scopeProjects(all, { source: 'all', regionPath: [] }).map((p) => p.id)).toEqual(['ZJ', 'GD', 'DG', 'NO']);
    expect(scopeProjects(all, { source: 'all', regionPath: [440000] }).map((p) => p.id)).toEqual(['GD', 'DG']);
    expect(scopeProjects(all, { source: 'real', regionPath: [440000] }).map((p) => p.id)).toEqual(['GD']);
    expect(scopeProjects(all, { source: 'demo', regionPath: [330000] })).toEqual([]);
  });
  it('one alarm in this province, more elsewhere → the scoped KPI counts only this province', () => {
    const devices = [
      device({ deviceId: 'z1', status: 'ALARM', adminPath: zj.adminPath }),
      device({ deviceId: 'g1', status: 'ALARM', adminPath: gd.adminPath }),
      device({ deviceId: 'g2', status: 'ALARM', adminPath: gd.adminPath }),
    ];
    const scope = { source: 'all' as const, regionPath: [330000] };
    expect(homeMetrics(scopeProjects([zj, gd], scope), scopeProjects(devices, scope)).alarmDevices).toBe(1);
    expect(homeMetrics([zj, gd], devices).alarmDevices).toBe(3);
  });
  it('scope label names the source and the region', () => {
    expect(scopeLabel('api', { source: 'all', regionPath: [] }, '全国')).toBe('全部授权项目 · 全国');
    expect(scopeLabel('demo', { source: 'real', regionPath: [330000] }, '浙江省')).toBe('匿名登记项目 · 浙江省');
    expect(scopeLabel('demo', { source: 'all', regionPath: [] }, '全国')).toBe('全部展示（含演示） · 全国');
  });
});
