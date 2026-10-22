import { describe, it, expect } from 'vitest';
import {
  aggregate,
  expandProjects,
  realProjects,
  demoProjects,
  devices,
  filterProjects,
} from '../src/data';
import { lifecycleText } from '../src/data/types';
import { DrilldownController } from '../src/three/map/DrilldownController';
describe('真实资产与演示隔离', () => {
  it('固定 6 项目、10 台设备、5 省', () =>
    expect(aggregate(realProjects)).toEqual({ projects: 6, cranes: 10, provinces: 5 }));
  it('按数量展开且编号唯一', () => {
    const real = expandProjects(realProjects);
    expect(real.length).toBe(10);
    expect(new Set(real.map((d) => d.deviceId)).size).toBe(10);
    expect(real.filter((d) => d.projectName === 'Project-D').map((d) => d.deviceId)).toEqual([
      'SAMPLE-004-A',
      'SAMPLE-004-B',
    ]);
    expect(real.filter((d) => d.projectName === 'Project-F')).toHaveLength(3);
  });
  it('Project-C按实际地址归属四川马尔康', () => {
    const p = realProjects.find((p) => p.projectName === 'Project-C')!;
    expect(p.province).toBe('四川省');
    expect(p.adminPath).toEqual([510000, 513200, 513201]);
  });
  it('省级聚合数量正确', () =>
    expect(
      Object.fromEntries(
        [...new Set(realProjects.map((p) => p.province))].map((province) => [
          province,
          aggregate(realProjects.filter((p) => p.province === province)).cranes,
        ])
      )
    ).toEqual({ 浙江省: 2, 四川省: 1, 海南省: 2, 广东省: 2, 云南省: 3 }));
  it('真实档案未投运，模拟遥测具有独立来源与固定值', () => {
    for (const d of devices.filter((d) => d.dataSource === 'real')) {
      expect(d.commissioningStatus).toBe('NOT_COMMISSIONED');
      expect(d.telemetrySource).toBe('demo');
      expect(d.status).not.toBe('NOT_COMMISSIONED');
      expect(d.totalHours).toBeGreaterThan(0);
      expect(d.todayHours).toBeGreaterThan(0);
    }
    expect(lifecycleText.PRE_DEVELOPMENT).toBe('软件预开发');
  });
  it('真实演示筛选与固定状态分布', () => {
    expect(filterProjects('real')).toHaveLength(6);
    expect(filterProjects('demo')).toHaveLength(22);
    expect(filterProjects('all')).toHaveLength(28);
    expect(aggregate(demoProjects).cranes).toBe(44);
    expect(devices.filter((d) => d.dataSource === 'demo' && d.status === 'ALARM')).toHaveLength(4);
    expect(devices.filter((d) => d.dataSource === 'real' && d.status === 'ALARM')).toHaveLength(1);
  });
});
describe('行政区层级栈', () => {
  it('支持省市县并按面包屑回退', () => {
    const c = new DrilldownController();
    c.push({ adcode: 330000, name: '浙江省', level: 'province' });
    c.push({ adcode: 330400, name: '嘉兴市', level: 'prefecture' });
    c.push({ adcode: 330481, name: '海宁市', level: 'county' });
    expect(c.current.name).toBe('海宁市');
    c.back(1);
    expect(c.current.adcode).toBe(330000);
    expect(c.stack).toHaveLength(2);
  });
  it('支持海南直管县，拒绝重复栈项', () => {
    const c = new DrilldownController();
    c.push({ adcode: 460000, name: '海南省', level: 'province' });
    c.push({ adcode: 469023, name: '澄迈县', level: 'county' });
    c.push({ adcode: 469023, name: '澄迈县', level: 'county' });
    expect(c.stack.map((l) => l.adcode)).toEqual([100000, 460000, 469023]);
  });
  it('转场互斥状态正确', () => {
    const c = new DrilldownController();
    c.state = 'DRILLING_IN';
    expect(c.busy).toBe(true);
    c.state = 'REGION_IDLE';
    expect(c.busy).toBe(false);
  });
});

it('云南三机状态与固定遥测在子集展开时不变', () => {
  const y = devices.filter((d) => d.projectName === 'Project-F');
  expect(y.map((d) => d.status)).toEqual(['RUNNING', 'MAINTENANCE', 'RUNNING']);
  for (const p of [...realProjects, ...demoProjects])
    expect(expandProjects([p])).toEqual(devices.filter((d) => d.id === p.id));
});
