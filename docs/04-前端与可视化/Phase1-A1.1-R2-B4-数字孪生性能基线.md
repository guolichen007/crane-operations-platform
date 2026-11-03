# Phase1-A1.1-R2 B4 数字孪生 50 台资产性能基线

> 阶段：B4-0 冻结。本基线是 B4 的性能验收目标与测量口径，50 台行车（CRANE_COUNT=50），架构不得阻塞未来 >500 扩展，但本阶段不为 500+ 过度设计。

## 1. 目标与口径

| 指标 | 目标 | 口径 |
|---|---|---|
| FPS_MEDIAN | >= 58 | 1920×1080，Premium 档，soak 循环采样中位数 |
| FPS_1_PERCENT_LOW | >= 45 | 采样序列 1% 低分位 |
| FRAME_P95 | <= 20ms | 单帧耗时 95 分位 |
| FRAME_P99 | <= 28ms | 单帧耗时 99 分位 |
| HOVER_FEEDBACK_P95 | <= 50ms | pointermove 到 hover 视觉反馈 |
| CLICK_FEEDBACK_P95 | <= 100ms | pointerdown/click 到选中视觉反馈 |
| LABEL_LAYOUT_P95 | <= 4ms | settle 后一次 label 布局重建 |
| PICK_P95 | <= 4ms | 一次屏幕空间拾取 |
| DRAW_CALLS（Fleet Layer） | <= 8 | Country Scene 中 Fleet/资产层 |
| DRAW_CALLS（Country Scene） | Premium<=120 / Balanced<=90 / Reduced<=70 | 全国默认态 |
| LoAF blocking >100ms | = 0 | 关键交互 |
| Forced Reflow（单次） | <= 30ms | 关键交互 |

> 说明：FPS 是「架构目标」；在无 GPU 的 CI/沙箱环境，SwiftShader 软件渲染的 FPS 不具代表性，验收以真实 GPU 机器 + 性能证据脚本为准。本仓库提供测量脚本与埋点，不伪造数值。

## 2. 渲染预算拆解（Country 默认态）

- **省域底图**：34 个省级多边形，每省 1 个 surface mesh + 1 个 outline，约 68~90 draw calls（premium 含 side/bevel）。可选 batch 省轮廓为合并 path 进一步压 draw call。
- **Fleet Beacon**：仅「有资产」的省，一个状态环（RingGeometry）+ 中心数字标签（DOM），每省 1~2 draw call。
- **Crane Glyph**：50 台用 1~2 个 InstancedMesh（bridge/legs/trolley/beacon 各一个 instanced mesh，合计 <=4 draw calls），状态色走 instanceColor，选中走 SelectedCraneOverlay（1 个 ring/outline），告警走低频 pulse overlay。
- **网络/氛围层**：network arcs + 地面 floor + atmosphere，预算 ~10 draw calls。
- **合计**：Premium <=120 可达成。

## 3. 五级 LOD 与资产

| 层级 | 视觉表达 | 资产 |
|---|---|---|
| COUNTRY | 省轮廓 + 省名 + Fleet Beacon | 34 省多边形 + 少量 beacon |
| PROVINCE | 市边界 + 市 label + 市 fleet cluster | 该省市多边形 + cluster |
| CITY | 项目 cluster | 项目 cluster |
| PROJECT | 项目下 Crane Glyph（instanced） | 若干 glyph |
| ASSET | CraneTwinLite（按需加载，1 台） | GLB 15k~30k tri, <=3 materials |

CraneTwinLite 不一次加载 50 台；选中/聚焦才加载，LRU 缓存。

## 4. 帧调度（RenderScheduler）

| 模式 | 帧率 | 触发 |
|---|---|---|
| INTERACTING | 60FPS | pointer down / 拖拽 |
| CAMERA | 60FPS | 相机过渡 |
| TOUR | 60FPS | 自动巡演 |
| LIVE | 30FPS | 静止但需要实时态更新 |
| IDLE | invalidate-only | 完全静止，无动画脏标记则 0 帧 |
| HIDDEN | 0 | document hidden |

IDLE 模式下只有状态/数据变化才 invalidate 重绘，杜绝「静止空转」。

## 5. 拾取（ScreenPickIndex）

- 不每帧全场景 raycast；相机/场景/目标变化 → 重建「投影拾取缓存」。
- pointermove 经 rAF 节流，一帧最多 1 次 pick。
- Raycaster 分层：LAYER_ASSET > LAYER_ALARM > LAYER_PROJECT > LAYER_REGION。

## 6. 标签（LabelEngine）

- 拆 Projection / Layout / Render 三阶段；禁止 per-frame DOM measure。
- 分层密度：全国只省名+Beacon；省只市+项目；市只项目+Crane。
- DRAGGING：降标签密度 + 暂停碰撞重建 + 停 hover 拾取；camera settle 后 80~120ms 再重建。

## 7. 数据与状态语义

- Business 状态（asset_status）与 Runtime 状态（RUNNING/STANDBY/ALARM/OFFLINE/MAINTENANCE）严格分离；无 Runtime Binding 显示 PLANNED/NO_RUNTIME，禁止默认绿色。
- 类型（Crane Type）用 shape/silhouette 表达，运行状态用 color 表达；禁止同一颜色同时代表类型与状态。

## 8. Soak 泄漏检查

10 分钟循环（Country→Province→Project→Asset→Back + Search/Alarm/Sidebar），检查 heap / textures / geometries / listeners 不持续增长；每次 dispose 用 disposeObject 释放 geometry/material/texture，ResizeObserver/事件监听在 onUnmounted 清理。

## 9. 测量脚本与埋点

- `window.__spatialDiagnostics()`：draw calls / triangles / textures / labels / markers / picks 快照（已有，扩展 FPS/帧时间）。
- 新增 `window.__perfRecorder()`：采样 FPS 序列、1% low、帧 P95/P99、LoAF、forced reflow。
- Playwright e2e：载入 → 循环交互 → 读取 recorder 输出（仅在真实 GPU 环境有参考意义；CI 只跑「交互正确性」断言，不把 SwiftShader FPS 当验收）。

## 10. 反模式（出现即返工）

50 台 Full Twin 同时加载；省份和设备同一颜色语义；省份点击抢设备点击；无 Runtime 却显示绿色；所有设备标签常显；mousemove 全量 project；drag 时持续 label collision；sidebar hover 改变 Canvas 尺寸；大量 backdrop-blur；全屏 bloom；点击设备镜头乱飞；设备重叠无法区分；截图漂亮但拖动掉帧。
