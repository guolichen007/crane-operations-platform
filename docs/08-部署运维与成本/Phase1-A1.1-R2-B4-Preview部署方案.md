# Phase1-A1.1-R2 B4 Preview 部署方案（仅准备，不执行）

> 阶段：B4-R1 收口。本轮只准备方案，不真实部署、不访问真实 CA、不修改 DNS。等待 OWNER 明确批准后才部署。

## 1. 目标

- 独立 Preview 域名：`b4.crane.youdaozd.com`。
- 完全独立于 production：独立 container/project name、独立 port/network、独立 volume，不修改正式容器、不切正式 DNS、不覆盖正式 volume。

## 2. 组件与隔离

| 项 | Preview | Production |
|---|---|---|
| Compose project | `crane-b4-preview` | 现有 production project |
| 网络 | 独立 preview network | 现有网络 |
| Web 端口 | 独立（如 18081/443 由前置代理分流） | 不变 |
| 数据库 | 独立 Preview 库（seed/demo 数据） | 不变 |
| Volume | `preview-*` 前缀 | 不变 |
| 镜像 | 同一 immutable 镜像 + digest | 同一镜像 |

## 3. Auth（如复用真实 Keycloak）

- 必须新增合法 Preview redirect URI：`https://b4.crane.youdaozd.com/auth/callback`。
- 不关闭 OIDC 来省事。
- 如需 Preview Client，独立 client_id/secret，不复用 production client。
- Web Session 密钥独立生成，不复制 production 密钥。

## 4. TLS

- 复用现有自动证书体系（ACME）。
- 本轮不访问真实 CA、不修改 DNS；仅记录待办：
  - 为 `b4.crane.youdaozd.com` 增加 DNS CNAME。
  - 证书签发后由现有 renew 脚本管理。
  - Preview 证书与 production 证书分目录，互不覆盖。

## 5. 数据

- 视觉验收：优先 Demo/seed 数据集（50 台行车、22 个演示项目 + 匿名登记项目）。
- API 验收：隔离 Preview 数据；禁止把 Production DB 当实验数据源。
- Preview 库用独立 migration 全量建表，不引用 production volume。

## 6. 前置 Gate（全部通过才部署）

```
backend=PASS
frontend=PASS
integration=PASS
edge=PASS
security=PASS
a1-security=PASS
r2-db-test=PASS
repository-contract=PASS
```

## 7. 真实 GPU 验收步骤（OWNER 执行）

在真实浏览器（非 SwiftShader 沙箱）打开 Preview，逐项执行：

```
idle → orbit → province drill → asset mode → alarm mode → asset select → search locate
```

每项后读取性能采样：

```js
// 控制台执行
window.__perfRecorder().start();      // 开始采样
// ... 执行交互 ...
window.__perfRecorder().stop();       // 返回 { fps, fps1PercentLow, frameP95, frameP99, longestBlockingMs, forcedReflowMaxMs, samples }
window.__spatialDiagnostics();        // { drawCalls, triangles, labelLayoutMs, pickMs, renderMode, dpr, quality, labels, memory }
```

验收目标（见 B4 性能基线文档）：

- FPS_MEDIAN >= 58；FPS_1_PERCENT_LOW >= 45。
- FRAME_P95 <= 20ms；FRAME_P99 <= 28ms。
- LABEL_LAYOUT_P95 <= 4ms；PICK_P95 <= 4ms。
- 全国默认态静止后 `renderMode === 'IDLE'`。
- DRAW_CALLS：本沙箱 SwiftShader 实测 140（reduced 档）；真实 GPU 需实测 Premium 档并报告。

## 8. 视觉证据页

需在 2560×1440 / 1920×1080 / 1440×900 / 1280×720 截取：

```
Country（全国默认） / Fleet（资产） / Alarm（告警）
Province（省）
Asset selected（选中设备）
Search（全局搜索）
Sidebar（侧栏） / Asset workspace（资产中心） / Portfolio（项目组合）
```

本沙箱已产出 SwiftShader 截图（见 overview.spec.ts 的 regression 目录）；真实 GPU 截图待 OWNER 在 Preview 环境补充。

## 9. 不做

- 不真实部署；不修改生产容器/DNS/volume；不访问真实 CA。
- 不进入 B5/B6/B7/A2。
