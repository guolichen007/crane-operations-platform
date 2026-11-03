# Phase1-A1.1-R2 B4 视觉性能封板与验收证据

> 阶段：B4 封板。记录 B4 已交付能力、验证结果与诚实缺口。

## 1. 交付概览

| 提交 | 内容 |
|---|---|
| ae711f5 | B4 研究冻结：REA 模式研究 + 50 台性能基线 |
| 183b7d7 | 渲染/拾取性能架构：RenderScheduler、ScreenPickIndex、pointer 节流、drag 性能模式、PerfRecorder |
| d8302ec | 地图资产聚合：provinceFleetSummary、ProvinceFleetBeacon 状态环、CraneGlyphLayer(InstancedMesh)、CraneSelectionOverlay |
| bef9f14 | 旗舰 IOC：全局搜索 Ctrl+K、场景模式 dock(行政区/资产/告警)、locate 事件 |
| 90d98f8 | 导航+工作台基座：可折叠侧栏(64↔216)、TanStack Query/Table/Reka UI 依赖、统一 API client |
| 42f0c3d | 资产/位置/Equipment 工作区：AssetCenter、AssetDetail、LocationCenter、CatalogCenter + 后端结构化过滤 |
| db48d0b | 项目组合工作区：PortfolioWorkspace(登记/资源/排期/冲突) |

## 2. 性能架构（已实现）

- **RenderScheduler**：INTERACTING/CAMERA/TOUR=60FPS，LIVE=30FPS，IDLE=invalidate-only，HIDDEN=0。IDLE 只画脏帧，静止时 0 GPU 工作。
- **ScreenPickIndex**：投影拾取缓存，相机指纹 + 目标版本失效；pointermove rAF 节流一帧最多 1 次 pick。
- **Drag 性能模式**：拖拽时停 hover 拾取、标签碰撞重建暂停、密度下降；settle 后 80~120ms 重建。
- **CraneGlyphLayer**：50 台行车 glyph 走 2 个 InstancedMesh（body + beacon），状态色走 instanceColor；选中/悬停走 CraneSelectionOverlay（不整块重绘）。

## 3. 验证结果（本沙箱真实执行）

- 前端：`typecheck` ✓ / `lint` ✓ / `npm test` 62 tests ✓ / `build` ✓。
- 后端：`go build` ✓ / `go vet` ✓ / `gofmt` ✓ / 真实 PostgreSQL 下 `internal/asset` + portfolio + scheduling + identity + app 测试 ✓。
- 契约：`contracts.mjs --check` ✓ / `openapi.mjs --check` ✓。
- Playwright e2e：既有 overview.spec.ts（3 项关键用例）+ 新增 industrial-b4.spec.ts（4 项）✓。

## 4. 诚实缺口（未完成项）

1. **FPS 数值未以真实 GPU 验证**：本沙箱 Chromium 走 SwiftShader 软件渲染，FPS≈6 不代表真实 GPU。58FPS/1%low/帧 P95 是架构目标；验收需真实 GPU 机器跑 `window.__perfRecorder()`。
2. **Draw call 未压到 ≤120**：全国默认态约 235 draw calls，主要来自 34 个省级多边形的 surface/top/side/outline 未 batch。需 B4-8(后续) 对省域 mesh 做 merge/batch 才能达标；Fleet Layer 本身已 ≤8。
3. **结构化过滤仅 Asset 完整**：assetStatus/assetType/manufacturerId 已入后端+前端；location/equipment/catalog/manufacturer 的过滤字段已在 ListOptions 声明但未全部接入 list 函数。
4. **分页 envelope**：列表仍返回数组（后端 ListAssets 已算 total 但 HTTP 层未包 `{items,page}`）。前端用 `长度<limit` 判断是否还有下一页。
5. **Runtime Binding 只读**：无写路由（符合 B3 决定）；GET runtime-binding 因与 `/equipment/catalog/{id}` 的 Go ServeMux 路由冲突而省略（只读约束不受影响）。
6. **Soak/截图证据**：10 分钟 soak 循环未在真实 GPU 执行；截图由 overview.spec.ts 的 regression 目录产生（SwiftShader 渲染）。

## 5. 反模式自检

50 台 Full Twin 同时加载 → 否（InstancedMesh glyph）；省份与设备同一颜色语义 → 否（省本体 neutral，状态色只属 beacon/设备）；省份点击抢设备点击 → 否（LAYER 优先级 + 命中即消费）；无 Runtime 显示绿色 → 否（real 未投运计 PLANNED gold）；所有设备标签常显 → 否（分层密度）；mousemove 全量 project → 否（缓存 + 节流）；drag 持续 label collision → 否（drag 暂停）；sidebar hover 改 Canvas → 否（显式点击折叠，ResizeObserver 才 resize）；满屏 bloom → 否（selective）。

## 6. 验收对照

| Final Gate | 状态 |
|---|---|
| PROVINCE_COUNT / PROVINCE_STATUS_RING | ✓ ProvinceFleetBeacon |
| PROVINCE_DRILL / CITY_DRILL | ✓ 既有 DrilldownController |
| CRANE_SELECT / CRANE_SEARCH / CRANE_FOCUS | ✓（glyph 选中/全局搜索/focusDevice）|
| PROJECT_CLUSTER / PROJECT_SPIDERFY | ⚠ 聚类存在，spiderfy 未实现（B4-8）|
| ALARM_LOCATE | ⚠ 告警模式 dock 有，点到告警的逐级定位未做（B4-8）|
| MAP_LIST_LINKAGE | ⚠ 单向（列表→地图）；双向 hover 同步未做（B4-8）|
