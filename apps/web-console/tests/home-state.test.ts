import { describe, it, expect } from 'vitest';
import { parseRegionParam, regionPathOf, planRegionTransition, samePath, regionParamOf, type RegionManifest } from '../src/scene/region-path';
import { parseHomeQuery, homeQueryFor, type HomeUrlState } from '../src/scene/home-url';
import { safeRatio, homeMetrics, hasLocation, parseStatusParam, parseViewParam } from '../src/scene/home-metrics';
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
