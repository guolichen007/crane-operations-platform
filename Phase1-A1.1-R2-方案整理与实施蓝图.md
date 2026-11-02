# Phase1-A1.1-R2 方案整理（实施前最终版）

> 本文档是四轮设计 + R2-0 收口后的**方案总览**，供最终确认。DESIGN 已冻结（R2_DESIGN_FROZEN=YES_AFTER_R2_0_PATCH），R2-0 已作为纯文档提交（`d82e88c`）并自检通过。
> 代码实现暂缓，待确认后按 R2-1 → R2-9 连续推进。LOCAL_ONLY，不 push。

---

## 1. 产品定位（七大业务域 + 三个技术底座，不再漂）

从「Premium 展示 + 只读台账 + 通用沙盘」升级为**全国行车综合管理平台**。

七大业务域：

| 域 | 平台回答的问题 |
|----|--------------|
| 1 项目交付与资源排期 | 新项目什么时候做？谁做？有没有施工冲突？ |
| 2 资产运营 | 公司有多少行车、装在哪、什么配置？ |
| 3 实时运行 | 现在谁在线、怎么运行、哪里告警？ |
| 4 数字孪生 | 真实 Equipment 在哪、状态、相互关系？ |
| 5 运维可靠性 | 什么时候检查/保养/维修/更换？ |
| 6 历史取证 | 故障时什么配置、什么版本、怎样运动？ |
| 7 合规治理 | 登记/检验/档案/日周月是否完整？ |

三个技术底座：**Identity/RBAC/Audit** · **Data/Search/History** · **Documents/Import/Export**。

## 2. 核心架构（已冻结）

### 2.1 三层资产模型（Crane Digital Asset Graph）

```
Asset（业务资产 CRANE/SITE/RUNWAY）
  → Equipment Installation（可维护/可更换/可配置设备 PLC/LiDAR/Camera/VFD…）
    → Runtime Binding（真实运行连接，DEPLOYMENT_OWNED，只读）
      → Telemetry（遥测，TELEMETRY_OWNED，不可 UI 伪造）
```

命名已统一 `equipment_*`（types / catalog_items / asset_equipment_installations / relations / runtime_bindings / calibrations / software_installations），**禁止 `component_*`**。钢丝绳保留专用四表（specification/installation/inspection/replacement_event）。

### 2.2 数据所有权三分

| 域 | 谁可写 |
|----|--------|
| DEPLOYMENT_OWNED（identity/sourceMode/MQTT/cert） | 仅部署/bootstrap；业务 UI 只读 |
| BUSINESS_OWNED（制造/型号/位置/设备/钢丝绳/维保/文档/合规） | 有权限业务用户 |
| TELEMETRY_OWNED（connectivity/遥测/condition） | 仅设备/Edge |

**Inventory 所有权边界（P1-2 收口）**：首次创建可 seed BUSINESS_OWNED；已存在后 Import 只允许更新 DEPLOYMENT_OWNED，禁止改 BUSINESS_OWNED。**取消 `ui_edited` 方案**。

### 2.3 四维状态（不误导用户）

Lifecycle（INSTALLED/REMOVED/RETIRED）· Administrative State（IN_SERVICE/MAINTENANCE/OUT_OF_SERVICE/FAULT_REPORTED）· Connectivity（ONLINE/OFFLINE/UNKNOWN/UNBOUND，仅 Telemetry）· Condition（NORMAL/WARNING/ALARM/UNKNOWN，必带 source+observed_at）。ONLINE 不可人工填写，未绑定显示"未接入"。

### 2.4 历史六层（可追溯证据）

Current State / Business Revision（entity_revisions，含 effective_at+recorded_at）/ Lifecycle History（installation 专用表）/ Event History（alarm/work_order/inspection/audit）/ Telemetry History（时间质量字段）/ Aggregated History。统一 Timeline（asset_timeline_events 只做索引）。**Timeline/Revision/Audit/Telemetry 四者不合并**。

### 2.5 前端五 Workspace

Operations Center（默认轻量 Operations Map，Three 变 opt-in）/ Asset Workspace（Asset 360 六域 IA）/ Twin Workspace（Physical Twin + 传感器 FOV + Inspector）/ Maintenance Workspace / History Workspace（LIVE|HISTORY Contextual Time Conductor）。

### 2.6 Three.js 规范

WebGL（不追 WebGPU）；PBR/HDR/ACES/soft shadow/outline/selective bloom（仅报警/指示灯/焦点）；Select/Focus/Isolate/Hide/X-Ray/Reset + 四相机预设 + 语义树 + 场景搜索；传感器 FOV/range/frustum（pose_source：CALIBRATED/SURVEY=精确、MANUAL=示意）；系统拓扑 2D；GLB 管线（GLTFLoader + Meshopt/KTX2 + semantic metadata，accuracy GENERIC/PARAMETRIC/CATALOG/AS_BUILT）。

## 3. R2-0 收口结论（5 P1，已解决）

| P1 | 结论 |
|----|------|
| P1-1 Component→Equipment | 全库统一 equipment_*，API /equipment，capability equipment_condition_write，Three EquipmentInstallation；component 仅限外部标准描述/UI 文案 |
| P1-2 Inventory ownership | 取消 ui_edited；deployment-only update + 反证测试 |
| P1-3 遥测时间质量 | source_sent_at/cloud_received_at/boot_id/boot_seq/sequence/quality_flags/late_arrival/clock_skew_ms；GAP 检测禁跨大 gap 插值 |
| P1-4 Historical Configuration Resolver | resolve(assetId, at)；installation/relation/binding 加 valid_from/valid_to；revision 加 effective_at/recorded_at；semantic_slot 双解析 |
| P1-5 Runtime Performance Gate | FPS/DPR/DrawCalls/Triangles/交互延迟/Long Task 阈值 + RuntimeFrameAdapter（display decimation，UI 可丢帧服务器历史不丢） |

## 4. 实施路线（G0-R1 + B1 → B10，十批）

| Batch | 交付 |
|-------|------|
| G0-R1 | 主计划/能力地图/技术来源/治理（本批，只文档） |
| B1 | contracts + migrations(00005起) + ownership + capability + equipment schema + 时间质量持久化 |
| B2 | 项目组合与排期（Project Intake/生命周期/排期引擎/冲突/风险·问题·变更） |
| B3 | Asset/Location/Equipment/Catalog 后端 CRUD |
| B4 | 工业 Shell/DataGrid/表单/全局搜索/Operations Map/排期可视化 |
| B5 | 钢丝绳/点检/PM/工单/标定/软件/供应商/质保/备件/合规 |
| B6 | Revision + Timeline + 时态关系 + ConfigurationResolver + History API + 遥测时间质量 |
| B7 | Semantic Twin + GLB + 传感器可视化 + 系统拓扑 |
| B8 | History Explorer + Replay + Incident Review |
| B9 | 验收/Punch List/Project→Asset Handover/导入导出/文档 |
| B10 | 性能/bundle/无障碍/安全/视觉回归/REA 前后对比/迁移演练/备份恢复 |

## 5. 四条验收链（负责人最终盯的重点）

1. 新增行车 → 配置真实位置 → 新增 PLC/LiDAR/Camera/VFD → 首页找到 → 直接进 Asset 360
2. 点击 Three.js LiDAR → 真实 Equipment → 型号/安装/标定 → 实时状态 → 历史 → 工单
3. 安装钢丝绳 → 检查 → 异常 → 工单 → 更换 → 旧绳历史永久保留
4. 选择历史 Alarm → Incident Review → T-30min → 配置 at T → X/Y/Z/Load 回放 → Equipment/软件/标定 at T → Timeline → 看清故障全过程

四条链全部成立，同时 UI 不卡、Three 流畅、权限正确、历史可信，才算跨进成熟平台。

## 6. 技术决策冻结清单

- 保留 Vue 3 + Three.js + GSAP + ECharts + Pinia，不迁 React/TresJS/大 UI Framework。
- 引入 @tanstack/vue-table + @tanstack/vue-virtual + Reka UI（MIT，仅 primitive，样式全 U·DAO）。
- Vue Flow（MIT）候选，先 30-node/100-edge PoC，失败自研 SVG。
- ECharts 改 echarts/core 按需注册；Three 按需 opt-in；首屏预算 ≤220KB raw/≤70KB gzip（估算）。
- 遥测不换 TSDB（不引入 InfluxDB/Timescale/ClickHouse/Kafka），保留 telemetry_samples + device_shadow + metric catalog。
- 搜索继续 PostgreSQL（GIN/trgm 需 EXPLAIN/benchmark 后建），不引入 Elasticsearch。
- 业务档案不自动 purge；telemetry 默认不 purge；容量 trigger（rows/day、P95、disk）需真实数据校准。
- AGPL（realvirtual/xeokit）只学交互不复制；Open MCT（Other）只学模式。

## 7. 验证与环境约束（修正版）

| 工具 | 状态 |
|------|------|
| Node | 22.23.4 可用 |
| Go | 1.27.3 可用（用户态已装） |
| REA | 6.8.0 可用 |
| Docker daemon | 硬限制不可用（NoNewPrivs=1 非特权容器） |
| PostgreSQL | 用户态可装（未冻结，项目用 postgres:17.8） |
| 浏览器 | 待探测（Playwright e2e/视觉回归需装 chromium） |

- **CC 本地可跑**：contracts 生成（Node）、Go 编译/单测（Go 已装）、migration（装 PG 用户态后）、前端 lint/test/build。
- **归 GitHub CI（权威证据）**：Docker Compose 集成（mosquitto/keycloak/postgres）、完整 e2e、视觉回归。
- **生产服务器**：只做 B10 release rehearsal（isolated DB restore/migration rehearsal/backup restore），禁开发/实验。
- 未执行一律标 NOT_RUN，不写 PASS。

## 8. 已交付文档清单

- `Phase1-A1.1-R2-成熟资产运营平台设计蓝图.md`（主蓝图，四轮 + 5 P1 收口）
- `Phase1-A1.1-R2-方案整理与实施蓝图.md`（本文件，七域方案总览）
- `docs/架构/`：行车数字资产图谱、设备与运行数据边界、自动化设备分类与字段模型、前端工业工作台与数字孪生规范、工业历史数据与回放架构、前端性能与Bundle预算、历史数据保留与容量规划、技术模式与REA来源登记
- `docs/产品设计/`：成熟平台对标研究、REA前端优秀平台取证报告、成熟平台能力地图、项目交付与资源排期设计
- `docs/项目管理/`：分阶段实施与审查计划、当前状态与交接
- `AGENTS.md`：阶段=R2，LOCAL_ONLY 边界
- `docs/会话上下文.md`：本地，DO NOT COMMIT
