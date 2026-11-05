# B4 Preview 业务验收与 GPU 性能

> 状态：验收用例已备（复用 `tests/e2e/b4/` 14 条），Preview 尚未部署，浏览器验收与 GPU 测量均为 NOT_RUN。

## 1. 浏览器业务验收（真实 HTTPS + Keycloak + PostgreSQL）

复用现有 14 条 B4 用例，指向已部署 Preview 执行：

```bash
A1_WEB_ORIGIN=https://b4.crane.youdaozd.com \
A1_BROWSER_PASSWORD_FILE=/srv/u-dao/secrets/crane-b4-preview/browser_test_password \
npx playwright test --config playwright.b4-preview.config.ts
```

覆盖（全部真实页面操作，非 Mock API / 非 `--list`）：

- 管理员登录 → 创建资产 → 编辑 → 移动 → 安装 Equipment → 拆卸 → 退役；
- viewer 只读；operator/maintainer 不能修改资产主数据；
- P001/P002 项目隔离（读 404、写 404）；
- 409 版本冲突；CSRF 缺失/错误；注销与会话失效；
- 修订与审计关联（Request ID ↔ entity_revisions ↔ audit_logs，`verify-b4-state`）；
- 项目范围切换；1280/1366/1920 页面视觉。

数据库备份恢复：沿用 `deploy/scripts/backup.py` 在 Preview 项目上 drill（`--project-name crane-b4-preview`），不触碰生产备份。

## 2. GPU 性能（真实 GPU 浏览器，非 SwiftShader）

SwiftShader 沙箱结果**不得冒充实机 GPU**。真实 GPU 测量由 OWNER 在真实浏览器执行：

```js
window.__perfRecorder().start();
// ... 交互：idle → orbit → province drill → asset mode → alarm mode → asset select → search locate ...
window.__perfRecorder().stop();   // { fps, fps1PercentLow, frameP95, frameP99, longestBlockingMs, forcedReflowMaxMs, samples }
window.__spatialDiagnostics();    // { drawCalls, triangles, labelLayoutMs, pickMs, renderMode, dpr, quality, labels, memory }
```

目标（B4 性能基线）：`FPS_MEDIAN >= 58`、`FPS_1_PERCENT_LOW >= 45`、`FRAME_P95 <= 20ms`、`FRAME_P99 <= 28ms`、`LABEL_LAYOUT_P95 <= 4ms`、`PICK_P95 <= 4ms`、静止后 `renderMode === 'IDLE'`、真实 GPU 的 `drawCalls` 实测 Premium 档并报告。

## 3. 本沙箱现状（如实）

- 无 Docker、无真实 Keycloak 部署、无真实 GPU：BROWSER_UAT=NOT_RUN、GPU_PERFORMANCE=NOT_RUN。
- 沙箱 SwiftShader 实测 drawCalls≈140（reduced 档），仅供参考，不冒充 GPU 结论。
