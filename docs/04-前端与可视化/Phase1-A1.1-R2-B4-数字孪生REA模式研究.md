# Phase1-A1.1-R2 B4 数字孪生驾驶舱 REA 模式研究

> 阶段：B4 前置研究。本文只研究「成熟交互/聚合/LOD/GIS 设计」，不复制任何产品代码、不迁移 Cesium/Mars3D/Mapbox。
> 目标场景：50 台行车的中国数字沙盘 + 省市下钻 + 资产聚合 + 工业工作台。
> 证据来源：公开文档、示例、开源仓库与官方 Demo（见文末来源清单）。

## 1. 研究结论速览（先看这里）

| 模式 | 结论 | 我们怎么做 |
|---|---|---|
| 拾取优先级 | 点/标记 > 聚合/区域 > 底图 | 严格分层 Raycaster（LAYER_ASSET→ALARM→PROJECT→REGION），命中即消费 |
| 聚合 | 低缩放聚成 Beacon/Cluster，高缩放散开 | 全国→省 Beacon，省→市 Cluster，市→项目 Cluster，项目→Crane Glyph |
| 语义缩放 | 每级换视觉表达，不缩放同一模型 | COUNTRY/PROVINCE/CITY/PROJECT/ASSET 五级各自表达 |
| 标签 | 分层密度 + 碰撞 + 交互态才显示 | LabelEngine 分层：全国只省名+Beacon，省只市+项目 |
| 相机 | 短促、可中断、禁止点击即乱飞 | 650~900ms 钻取；选中 <100ms 反馈，不强制飞 |
| 悬停/选中 | 瞬时、低频率动效、不整机染色 | 悬停 lift 1.04~1.06 + halo；选中 cyan outline 覆盖层 |
| HUD/URL | 视觉态与列表/URL 双向同步 | sceneState ↔ Pinia ↔ URL query（?asset= / ?province=） |
| 实例化 | InstancedMesh + 脏实例稀疏更新 | 50 Crane Glyph 走 1~2 个 InstancedMesh |

## 2. 国内产品研究（重点）

### 2.1 阿里云 DataV（GeoAtlas 生态）
- **是什么**：阿里云一站式数据可视化大屏搭建平台，自带 GeoAtlas 行政区数据与「区域下钻层」组件。
- **关键模式**：
  - **双击下钻/上钻**：双击省/市下钻放大定位，双击空白上钻一级；拖拽平移、滚轮缩放、右键旋转。
  - **自定义区域下钻层**：省直辖市级、地市级子组件，可独立配置样式、数据、交互（下钻标注、边线、标签、双击聚焦）。
  - **蓝图编辑器联动**：组件间事件联动下钻，`area_id` 全局变量控制缩放恢复（回全国时恢复预设比例）。
  - **层级树**：用 DataV.GeoAtlas 制作层级树导入，实现自定义区域层级。
- **我们的借鉴**：行政区层级树 + 钻取恢复预设视角 + 地图与侧栏/列表联动。
- **风险**：大屏组件生态依赖其专有渲染，不能直接复用到自研 WebGL；我们只取交互语义。

### 2.2 UINO / ThingJS
- **是什么**：优锘科技的 WebGL 数字孪生引擎，底层 Three.js。
- **关键模式**：
  - **实例复制规范**：相同模型必须用「实例复制」，否则按单模型重新计算；批量改色/高亮走实例化渲染降低 Draw Call。
  - **模型轻量化 + LOD + 异步分块加载 + 内存池**，官方案例称加载优化 70%+、内存降 50%。
  - **单设备面数预算**：约 1 万三角面以内，纹理图集减少 Draw Call。
  - **InstancedMesh 更新预算**：实例矩阵不应每帧全量上传，应做脏实例稀疏更新，按帧预算控制上传字节。
  - **LOD + InstancedMesh**：每个 LOD 是独立 InstancedMesh，距离/屏幕尺寸切换 + 滞后（hysteresis）防闪烁。
- **我们的借鉴**：CraneGlyph 用 InstancedMesh；选中/告警只改 `instanceColor` 不重建整块；LOD 滞后切换；CraneTwinLite 控制三角面与材质数。

### 2.3 Hightopo（HT for Web）
- **是什么**：图扑软件自研 2D/3D 数据可视化中间件，WebGL + Canvas 双引擎。
- **关键模式**：
  - **批量聚合渲染（Batch）**：图元聚合成大模型一次绘制；海量连线合并路径。
  - **局部刷新/脏区域重绘**：避免全量重绘。
  - **自适应缩放渲染**：按缩放值动态控制 label/note/icons 显隐。
  - **LOD、视锥体剔除、多线程排序**。
- **我们的借鉴**：按缩放动态控制标签密度（正是 B4 Label Density 要求）；batch 化省域描边/Beacon。

### 2.4 51WORLD / 山海鲸 / EasyV / 图观（大屏数字孪生类）
- **共性**：GIS 底图/行政区 + 业务数据图层 + 两侧 HUD + 大屏叙事动效。
- **关键模式**：中心沙盘永远是最大视觉对象，HUD 悬浮两侧；指标卡 + 状态环 + 时间轴叙事。
- **风险**：大量装饰性动效、满屏 bloom、无业务意义的霓虹——B4 明确禁止，只保留 selected/alarm/重要路线 selective bloom。

### 2.5 AntV L7
- **是什么**：蚂蚁 AntV 的开源 WebGL 地理可视分析引擎。
- **关键模式**：
  - **基于像素拾取**：WebGL 离屏渲染实现海量点快速选中/高亮（1 像素线要素可精确拾取）。
  - **点图层聚合**：海量点开启聚合模式显示。
  - **active/select 图层交互**：`setActive(id)` / `setSelect(id)` 直接指定高亮/选中，事件返回要素信息。
- **我们的借鉴**：像素级拾取 + 显式 setActive/setSelect（不依赖每帧 raycast 全量）；这正好是 B4 ScreenPickIndex 的动机。

### 2.6 Mars3D
- **是什么**：基于 Cesium 封装的国产三维 GIS 平台，面向倾斜摄影/3DTiles 城市级场景。
- **研究结论**：Mars3D/Cesium 的 HLOD + 3D Tiles 面向「城市场景海量倾斜摄影」，与 50 台行车的单体设备场景不匹配；B4 明确不迁移。**仅吸收**其「几何误差 + 屏幕空间误差驱动的 LOD 选择」思想，映射到 CraneTwin 的 3 级 LOD。

## 3. 国外产品研究（重点）

### 3.1 deck.gl（+ Mapbox/Supercluster）
- **关键模式**：
  - **Aggregation Layers**：Hexagon/Grid/Heatmap 将点聚合为替代表达；GPU 聚合在 >10 万点显著快，但小数据集（<10 万）CPU 更省且有 upfront 开销。
  - **低缩放聚合、高缩放散开**：overview 用 HexagonLayer，z12+ 切 Scatterplot。
  - **二进制属性**：大 GeoJSON 解析冻结主线程，改喂 typed array（Float32 位置、Uint8 颜色）。
  - **视口窗口化**：按视口（含 margin）过滤数据，避免屏外工作。
  - **缩放自适应聚类半径**：低缩放大半径激进合并，高缩放小半径出细节。
- **我们的借鉴**：50 台属于「小数据集」，CPU 聚合即可（不做 GPU 聚合）；颜色走 instanceColor typed array；按视口窗口化标签/拾取候选。

### 3.2 Cesium / 3D Tiles
- **关键模式**：HLOD 层级 LOD，父 tile 是子 tile 简化；`maximumScreenSpaceError` 控制细分；`skipLevelOfDetail` 跳层。
- **我们的借鉴**：CraneTwin LOD0/LOD1/LOD2 用「屏幕空间尺寸」判定，选中资产优先最高 LOD；滞后切换防闪烁。
- **拒绝原因**：3D Tiles/BIM/倾斜摄影是 >500 资产场景的 SceneProvider 议题，B4 不做。

### 3.3 Siemens Industrial Experience (iX) + Grafana + Linear
- **iX**：西门子工业设计系统，框架无关 Web Components；数据网格直接采用 AG Grid；Card List 用于「有层级地展示大量信息」。
- **Grafana**：面板/仪表盘范式，时间序列 + 阈值着色 + 变量联动（URL state 驱动面板）。
- **Linear**：命令行（Cmd+K）+ 键盘优先 + 高密度列表 + 语义状态色收敛。
- **我们的借鉴**：工作台用 TanStack Table 做高密度可排序/过滤数据网格（对应 AG Grid 位）；Cmd+K 全局搜索（对应 Linear）；URL query 同步筛选（对应 Grafana 变量）；状态色只用于状态、图标/文字/形状三通道语义（对应 iX 可访问性）。

## 4. 地图场景模式清单（12 项）

针对地图产品逐项记录「怎么做 + 性能风险」：

| 场景 | 我们怎么做 | 性能风险与对策 |
|---|---|---|
| LOAD | 全国默认态：省轮廓+省名+Beacon，不加载 50 台 Twin | 只加载 Glyph 实例，TwinLite 按需 |
| HOVER REGION | outline + 极小 lift + tooltip；不触发相机 | 悬停节流 + 单拾取/帧 |
| SELECT REGION | 立即选中反馈 → HUD 切省 → 相机钻取 | 相机 650~900ms，可中断 |
| DRILL REGION | 全国 fade，省 dominant，市边界+市 label+市 cluster | 递归 dispose 旧 detail |
| HOVER MARKER | scale 1.04~1.06 + halo + tooltip；不飞 | 只改 instanceColor/overlay |
| SELECT MARKER | 选中 feedback <100ms，右侧 HUD 摘要 | 选中不改 InstancedMesh，用 overlay |
| SELECT CLUSTER | 点项目 cluster 进入/展开 | cluster 状态分布可视化 |
| EXPAND CLUSTER | spiderfy 展开 + leader 线到 anchor；点空白 collapse | spiderfy 只动少量 glyph |
| ALARM LOCATE | 点告警→选中资产→相机定位→右 HUD 告警详情 | 低频 pulse，禁止快速闪烁 |
| SEARCH LOCATE | Cmd+K→命中→逐级 Country→Province→City→Project→Asset | 逐级动画可被用户打断 |
| BACK | 面包屑/返回全国；恢复 COUNTRY_OVERVIEW 预设 | 清理 detail/marker/label |
| RESET | 重置视角+选中+筛选 | 无泄漏 dispose |

## 5. Pattern Review（结构化）

### P1 拾取优先级
- **SOURCE**：DataV 区域下钻、deck.gl/L7 图层拾取、Cesium pick。
- **EVIDENCE**：L7 像素拾取 + 图层 `setActive/setSelect`；deck.gl picking 按图层。
- **WHY_GOOD**：点 > 区域可避免「省点击抢设备点击」。
- **PERFORMANCE_RISK**：每帧全场景 raycast。
- **OUR_ADAPTATION**：固定优先级 HUD>Selected>Crane>Alarm>Project>City>Province>Background；Raycaster 分层 LAYER_*；ScreenPickIndex 缓存投影，相机/场景/目标变化才重建。
- **REJECT_REASON**：无（全量采纳）。

### P2 聚合与语义缩放
- **SOURCE**：deck.gl Aggregation、Mapbox cluster、ThingJS 实例复制、DataV 层级树。
- **EVIDENCE**：低缩放 Hexagon/高缩放 Scatterplot；zoom-adaptive cluster radius。
- **WHY_GOOD**：符合人眼信息密度，避免 50 个模型重叠。
- **PERFORMANCE_RISK**：聚合重算每帧触发。
- **OUR_ADAPTATION**：五级语义缩放；CPU 聚合；缩放变化才重建聚合，非每帧。
- **REJECT_REASON**：无。

### P3 标签行为
- **SOURCE**：Hightopo 自适应缩放渲染、deck.gl 标签、Cesium billboard。
- **EVIDENCE**：按缩放动态显隐 label/note/icons；Hightopo 局部刷新。
- **WHY_GOOD**：信息密度与可读性平衡。
- **PERFORMANCE_RISK**：per-frame DOM measure / 全量 label 常显。
- **OUR_ADAPTATION**：LabelEngine 拆 Projection/Layout/Render；分层密度；碰撞只在 settle 后重建；drag 时降密度。
- **REJECT_REASON**：无。

### P4 相机时长与中断
- **SOURCE**：DataV 双击下钻/上钻、GSAP tween、Cesium camera flyTo。
- **EVIDENCE**：DataV 双击下钻放大定位；Cesium flyTo 可中断。
- **WHY_GOOD**：短促 + 可中断让用户不晕、不被劫持。
- **PERFORMANCE_RISK**：render loop 每帧建 tween。
- **OUR_ADAPTATION**：650~900ms；新 Focus cancel/replace 旧 tween；GSAP 不每帧创建。
- **REJECT_REASON**：无。

### P5 悬停/选中/tooltip/popup
- **SOURCE**：deck.gl picking、L7 active/select、Linear 高密度列表。
- **EVIDENCE**：图层 active（hover 高亮）/ select（选中）；L7 setSelect 返回要素。
- **WHY_GOOD**：反馈即时、语义清晰。
- **PERFORMANCE_RISK**：整机染色、快速闪烁、mousemove 全量 project。
- **OUR_ADAPTATION**：hover lift+halo；selected cyan outline overlay（不盖真实状态色）；alarm 低频 pulse；每帧最多 1 pick。
- **REJECT_REASON**：无。

### P6 HUD 同步与 URL state
- **SOURCE**：DataV 蓝图联动、Grafana 变量、deck.gl viewState。
- **EVIDENCE**：DataV 组件事件联动下钻；Grafana URL 变量驱动面板。
- **WHY_GOOD**：刷新不丢态，可分享、可回溯。
- **PERFORMANCE_RISK**：无（状态量级小）。
- **OUR_ADAPTATION**：sceneState/选中/筛选 ↔ Pinia ↔ URL query（?asset=/?province=/?mode=）。
- **REJECT_REASON**：无。

### P7 满屏 bloom / 装饰动效
- **SOURCE**：部分国内大屏 Demo（51WORLD/EasyV 类）。
- **EVIDENCE**：大量霓虹、粒子、持续 bloom。
- **WHY_GOOD**：视觉冲击强。
- **PERFORMANCE_RISK**：GPU 过载、业务语义被淹没。
- **OUR_ADAPTATION**：selective bloom 仅 selected/alarm/重要路线；普通 Crane NO BLOOM。
- **REJECT_REASON**：违反 50 台性能预算与业务语义分离。

## 6. 来源清单

- [DataV 地图容器 v3.x](https://www.alibabacloud.com/help/zh/datav/datav-7-0/user-guide/map-containers-of-v3-x)
- [DataV 自定义区域下钻层 v3.x](https://www.alibabacloud.com/help/zh/datav/datav-7-0/user-guide/custom-section-drill-down-layers-of-v3-x)
- [deck.gl Aggregation Layers](https://deck.gl/next/docs/api-reference/aggregation-layers/overview)
- [deck.gl for Large Geospatial Layers](https://www.bathyl.com/en/blog/deck-gl-for-large-geospatial-layers)
- [Cesium 3D Tiles Essentials](https://cesium.com/why-cesium/3d-tiles/3d-tiles-essentials/)
- [Cesium3DTileset 参考](https://cesium.com/learn/cesiumjs/ref-doc/Cesium3DTileset.html)
- [ThingJS 模型加载性能优化实战](https://my.oschina.net/emacs_8000473/blog/19320583)
- [InstancedMesh 更新预算：实例矩阵不该每帧全量上传](https://blog.moyuta.com/posts/instanced-mesh-matrix-upload-budget)
- [HT for Web Batch 批量手册](https://www.hightopo.com/guide/guide/core/batch/ht-batch-guide.html)
- [HT 二三维拓扑图渲染优化](https://www.cechina.cn/Company/200484_232752/messagedetail.aspx)
- [AntV L7 交互高亮](https://l7.antv.antgroup.com/en/tutorial/interactive/hightlight.md)
- [AntV L7 2.0 发布](https://developer.aliyun.com/article/742355)
- [Siemens iX Components overview](https://ix.siemens.io/version-3.2/docs/components/overview)
- [Siemens iX Card list](https://ix.siemens.io/version-3.2/docs/components/card-list/guide)
