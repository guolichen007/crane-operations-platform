# Phase1-A1.1-R2 前端性能与 Bundle 预算

> 基于 rea 静态分析对当前 `apps/web-console/dist` 的 baseline 测量。DESIGN_ONLY=YES。

## 1. Baseline（rea 实测，dist/assets）

| chunk | raw 大小 | 说明 |
|-------|---------|------|
| three-*.js | ≈ 970 KB | three + camera-controls + postprocessing 整包 |
| charts-*.js | ≈ 1127 KB | **整个 echarts**（未按需） |
| index-*.js | ≈ 190 KB | 入口 |
| Overview-*.js | ≈ 105 KB | 已 lazy |
| Workspace-*.js | ≈ 30 KB | 已 lazy |
| CloudDevices-*.js | ≈ 12.6 KB | 已 lazy |
| CraneTwinFactory-*.js | ≈ 3.9 KB | 已 lazy |

> 说明：上为 minified raw（未 gzip）。minified JS 经 gzip 通常再压缩约 3×（估算，非精确）；brotli 略优。所有 gzip 数字均为估算基准，实施时以构建产物实测为准。

## 2. 问题

Three 与 ECharts 已付出近 2MB raw 成本，但当前业务深度不够。核心矛盾：首屏/常用路由被迫承担 three+echarts 大 chunk，且 charts 未按需注册。

## 3. 优化方案

### 3.1 ECharts 拆 core（charts 1127KB → 大幅下降）

```ts
import * as echarts from 'echarts/core';
import { LineChart, BarChart, GaugeChart } from 'echarts/charts';
import { GridComponent, TooltipComponent, LegendComponent, DataZoomComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
echarts.use([LineChart, BarChart, GaugeChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, CanvasRenderer]);
```

只注册真正使用的；History Explorer / Analytics 路由进入时才加载。

### 3.2 Three 不默认加载

- Operations Center 默认 Operations Map（MapProvider，轻量），**不加载 Three sandbox**。
- 「空间沙盘」用户主动点击才 dynamic import `three + MapSceneManager + postprocessing`。
- Asset 页面点「数字孪生」Tab 才 `import TwinWorkspace + GLTFLoader + postprocessing`。
- GLTFLoader 按需。

## 4. 预算目标

| 指标 | 目标（估算） | 依据 |
|------|------------|------|
| TARGET_INITIAL_JS | ≤ 220 KB raw / ≤ 70 KB gzip（首屏，不含 three/charts） | index + 首屏路由，去掉大 chunk |
| TARGET_ROUTE_CHUNK | 单路由 ≤ 120 KB raw（除 three/charts） | lazy 路由粒度 |
| TARGET_THREE_CHUNK | ≤ 970 KB raw 保持，但**只 opt-in 加载**（gzip ≈ 290KB） | 空间沙盘/孪生 Tab 才拉 |
| TARGET_CHART_CHUNK | ≤ 500 KB raw（gzip ≈ 150KB，echarts/core 按需） | 拆 core 后估算 |

> 数字为预算基准（基于 baseline + echarts/core 按需 + gzip 3×估算），实施时以真实 gzip/brotli 产物复核，不虚填。

## 5. Runtime Performance Gate（P1-5，冻结）

Bundle gate 保留，新增 Runtime Gate（阈值以自动测试/diagnostics 实测为准，不只写文档）：

| 指标 | Premium | Balanced | Reduced |
|------|---------|----------|---------|
| 目标 FPS | 50–60 | ≥40 | ≥30 |
| DPR max | 1.5 | 1.25 | 1 |
| Draw Calls | <350 | <300 | <220 |
| Triangles | <700k | <500k | <300k |
| click→selection highlight | <100ms | <120ms | <150ms |
| click→Inspector skeleton | <100ms | <120ms | <150ms |
| Long Task >50ms | 不得持续出现 | 同左 | 同左 |

模型复杂时逐级启用 LOD / InstancedMesh / Meshopt / KTX2 / frustum culling。

**前端遥测架构（防卡顿）**：
```
Realtime Stream → Runtime Frame Store(latest sample)
   ├── Three Adapter：requestAnimationFrame 直接更新 Object3D（不走 Vue watcher 链）
   ├── UI State：2~5Hz projection
   └── Chart：batch 1~2Hz
```
高频数据允许 **display frame decimation**（UI 可丢中间帧，服务器历史不丢）。Raycaster 只在 pointer move/click 且节流，禁每帧全场景 raycast；Label 默认只显示 selected/hover/alarm，不常驻大量 HTML label；GSAP 不在 render loop 新建 tween。

## 6. 其它性能纪律

- 路由已 lazy import Vue pages（保持）；Three/ECharts 大依赖均改为"功能内按需"。
- 资产列表 server-side 分页（不做全量拉取 + 前端 filter）；@tanstack/vue-virtual 只解 DOM 数量，不替代服务端分页。
- render loop 不做 GSAP tween / DOM measurement / 大量 reactive mutation。
- Reduced Motion 关闭 postprocessing/粒子；QualityManager 分档。
- 避免 N+1：设备台账子表计数用聚合；History API 服务端降采样。
