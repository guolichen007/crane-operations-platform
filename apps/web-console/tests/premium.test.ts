import { describe, expect, it } from 'vitest';
import { authState, isViewerOnly } from '../src/cloud/auth-state';
import { palette, statusMapping, severityMapping } from '../src/design/tokens';
import { QualityManager } from '../src/three/core/QualityManager';
import { CraneTwinFactory } from '../src/three/twin/CraneTwinFactory';
import type { WebSession } from '../../../contracts/typescript/types.gen';
const viewer: WebSession = {
  userId: 'anonymous-viewer',
  displayName: '观察员',
  projects: [{ projectId: 'P001', projectName: '示例项目', role: 'viewer' }],
  csrfToken: 'synthetic-test-only',
  expiresAt: '2099-01-01T00:00:00Z',
  idleExpiresAt: '2099-01-01T00:00:00Z',
};
describe('认证挂载边界与权限推导', () => {
  it('loading / 401 / session / error 均有明确状态', () => {
    expect(authState(true, null)).toBe('AUTH_LOADING');
    expect(authState(false, null)).toBe('UNAUTHENTICATED');
    expect(authState(false, viewer)).toBe('AUTHENTICATED');
    expect(authState(false, null, '503')).toBe('AUTH_ERROR');
  });
  it('仅全部项目为 viewer 显示只读，不按用户名判断', () => {
    expect(isViewerOnly(viewer)).toBe(true);
    expect(isViewerOnly({ ...viewer, userId: 'admin' })).toBe(true);
    expect(isViewerOnly({ ...viewer, projects: [] })).toBe(false);
    expect(
      isViewerOnly({
        ...viewer,
        projects: [
          ...viewer.projects,
          { projectId: 'P002', projectName: '示例二', role: 'operator' },
        ],
      })
    ).toBe(false);
    expect(isViewerOnly(null)).toBe(false);
  });
});
describe('工业视觉语义', () => {
  it('品牌蓝固定，正常态克制，异常显著且有文本', () => {
    expect(palette.brand800).toBe('#072563');
    expect(statusMapping.RUNNING?.tone).toBe('neutral');
    expect(statusMapping.ALARM?.tone).toBe('critical');
    expect(severityMapping.CRITICAL?.label).toBe('严重');
    expect(statusMapping.UNKNOWN?.label).toBe('状态未知');
  });
});
describe('连续帧率降级', () => {
  it('软件 WebGL 不等待数秒慢帧采样即进入 reduced', () => {
    expect(new QualityManager(2, false, 16384, 'ANGLE SwiftShader Device').tier).toBe('reduced');
    expect(new QualityManager(2, false, 16384, 'llvmpipe').pixelRatio).toBe(1);
    expect(new QualityManager(1.5, false, 16384, 'hardware GPU').tier).toBe('premium');
  });
  it('硬件与 reduced motion 控制初始档位及 DPR', () => {
    expect(new QualityManager(3).tier).toBe('balanced');
    expect(new QualityManager(2, true).pixelRatio).toBe(1);
    expect(new QualityManager(1, false, 2048).tier).toBe('reduced');
    expect(new QualityManager(2).pixelRatio).toBe(1.5);
  });
  it('连续三窗口慢帧才降级，单次停顿不触发，最低档不反复分配', () => {
    const q = new QualityManager(2);
    q.sample(10);
    expect(q.tier).toBe('premium');
    for (let i = 0; i < 100; i++) q.sample(0.04);
    expect(q.tier).toBe('premium');
    for (let i = 0; i < 100; i++) q.sample(0.04);
    expect(q.tier).toBe('balanced');
    for (let i = 0; i < 200; i++) q.sample(0.05);
    expect(q.tier).toBe('reduced');
    expect(q.pixelRatio).toBe(1);
  });
});
describe('孪生轴层级与释放', () => {
  it('大车带小车，小车带起升，绳长随起升更新且输入限制范围', () => {
    const twin = new CraneTwinFactory();
    expect(twin.trolleyYGroup.parent).toBe(twin.gantryXGroup);
    expect(twin.hoistZGroup.parent).toBe(twin.trolleyYGroup);
    expect(twin.grabGGroup.parent).toBe(twin.hoistZGroup);
    twin.setAxes({ x: 99, y: -99, z: 6, grab: 1 });
    expect(twin.gantryXGroup.position.x).toBe(12);
    expect(twin.trolleyYGroup.position.z).toBe(-5);
    expect(twin.hoistZGroup.position.y).toBe(6);
    expect(twin.root.getObjectByName('ropes')?.children[0]?.scale.y).toBe(3);
    twin.dispose();
  });
});
