# Phase1-A1.1-R2 技术模式与 REA 来源登记（Technology Pattern Register）

> 每一个重要技术模式的可追溯来源登记，回答"我们为什么这样设计"。字段：PATTERN_ID / DOMAIN / SOURCE / SOURCE_TYPE（REA_OBSERVED·PUBLIC_DOCUMENT·OUR_ARCHITECTURE）/ LICENSE / OBSERVED（观察到的做法）/ ADOPT（采纳）/ REJECT（拒绝）/ OUR_IMPLEMENTATION（我们的落地）/ BATCH。
> 原则：AGPL 只学模式不复制代码；PUBLIC_DOCUMENT 与 INFERENCE 区分标注；不把 INFERENCE 当事实。

## TP-FE-DATAGRID — 高密度数据台账

- SOURCE：TanStack Table/Virtual + Maximo/OpenRemote 台账思想
- SOURCE_TYPE：PUBLIC_DOCUMENT + REA_OBSERVED
- LICENSE：MIT（TanStack）
- OBSERVED：server-side 分页/筛选/排序 + 虚拟化只减 DOM；列可见性/密度切换
- ADOPT：TanStack Table（状态）+ Virtual（大行渲染）+ server-side 分页为主逻辑
- REJECT：全量拉取前端过滤；虚拟化替代分页的误用
- OUR_IMPLEMENTATION：DataGrid 组件，Compact/Comfortable 两档
- BATCH：B4

## TP-FE-PRIMITIVE — 无样式可访问原语

- SOURCE：Reka UI（前身 Radix Vue）
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：MIT
- OBSERVED：Dialog/Popover/Dropdown/Tabs/Tooltip/Command + WAI-ARIA/键盘导航/focus management
- ADOPT：仅 primitive，样式全 U·DAO
- REJECT：整套视觉、大而全 UI Framework
- OUR_IMPLEMENTATION：Dialog/Popover/Dropdown/Tabs/Tooltip/Command 原语
- BATCH：B4

## TP-FE-MAP — 地图抽象

- SOURCE：MapLibre + OpenRemote/Tandem 地图交互
- SOURCE_TYPE：PUBLIC_DOCUMENT + REA_OBSERVED
- LICENSE：MapLibre BSD-3-Clause（候选）
- OBSERVED：marker/cluster/popup/fitBounds/select 抽象；资产地图与沙盘分离
- ADOPT：MapProvider interface（marker/cluster/popup/fitBounds/select/asset quick entry）
- REJECT：生产依赖 demo tiles、未许可绑第三方 API、公网地图默认加载
- OUR_IMPLEMENTATION：Operations Map 轻量默认 + Three Space View opt-in
- BATCH：B4

## TP-FE-SCHEDULE — 排期可视化

- SOURCE：ECharts 6 Custom Series + Primavera schedule 思想
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：Apache-2.0（ECharts）
- OBSERVED：区间条/里程碑/基线/实际/依赖/冲突叠加；资源热力图
- ADOPT：ECharts Custom Series + Range Bar 自建 Gantt/热力图
- REJECT：大型 Gantt 框架、闭源排程库
- OUR_IMPLEMENTATION：Calendar/Gantt + Resource Heatmap
- BATCH：B2/B4

## TP-3D-RUNTIME — 浏览器 3D HMI

- SOURCE：realvirtual-WEB
- SOURCE_TYPE：REA_OBSERVED + PUBLIC_DOCUMENT
- LICENSE：AGPL-3.0（**只学模式，禁复制代码**）
- OBSERVED：GLB 节点、信号绑定、runtime lifecycle、文档关联、KPI/报警 overlay、点击部件进设备信息系统
- ADOPT：semantic_slot → equipment installation → runtime binding → telemetry 映射链；3D 里 KPI/报警/Inspector
- REJECT：AGPL 源码、嵌入式脚本模型
- OUR_IMPLEMENTATION：Semantic Twin（自研 CraneTwinFactory 扩展）
- BATCH：B7

## TP-3D-INTERACTION — 3D 选择交互

- SOURCE：xeokit / Bentley iTwin / Tandem
- SOURCE_TYPE：REA_OBSERVED + PUBLIC_DOCUMENT
- LICENSE：xeokit AGPL-3.0（**只学交互，禁引入**）；iTwin 商业
- OBSERVED：Select/Focus/Isolate/Hide/X-Ray/Reset + 相机预设 + 树 + property grid + measurement
- ADOPT：Select/Focus/Isolate/Hide/X-Ray/Reset、四相机预设、语义树、场景搜索、Measurement
- REJECT：其引擎/SDK 本体
- OUR_IMPLEMENTATION：自研 Three.js 交互层
- BATCH：B7

## TP-HISTORY-TIME — 统一时间轴

- SOURCE：Open MCT Time Conductor
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：Open MCT "Other"（NASA，**只学模式，禁复制代码**）
- OBSERVED：统一 realtime/fixed timespan；时间范围编码进可分享状态；多 panel 叠加
- ADOPT：Contextual Time Conductor（LIVE/1H/6H/24H/7D/30D/CUSTOM），URL 表达 time state
- REJECT：Open MCT 框架本体；全局永久 LIVE/HISTORY（EAM 无时间语义的页面不放）
- OUR_IMPLEMENTATION：Realtime/Twin/History/Incident 内嵌 Time Conductor
- BATCH：B8

## TP-HISTORY-DASH — 趋势仪表

- SOURCE：Grafana Scenes / ThingsBoard dashboard
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：Grafana AGPL（**只学模式**）；ThingsBoard Apache-2.0
- OBSERVED：多 metric 趋势 + 事件叠加 + 时间窗联动 + URL state
- ADOPT：多 metric 趋势图 + 事件叠加到时间轴
- REJECT：其 widget/规则链引擎、dashboard 框架本体
- OUR_IMPLEMENTATION：History Explorer（ECharts core）
- BATCH：B8

## TP-EAM-ASSET — 资产模型

- SOURCE：IBM Maximo / AWS SiteWise / ThingsBoard
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：商业（只学概念）
- OBSERVED：Asset/Device 分离、Asset Model vs 实例、层级、Relations、PM hierarchy
- ADOPT：Asset→Equipment→Runtime Binding 三层、type-instance 分离、relations、TIME/METER/HYBRID PM
- REJECT：其平台本体
- OUR_IMPLEMENTATION：Crane Digital Asset Graph
- BATCH：B1/B3

## TP-EAM-CRANE — 行车域数据口径

- SOURCE：Konecranes Portal/TRUCONNECT + Demag + 卫华 + 微特
- SOURCE_TYPE：PUBLIC_DOCUMENT + REA_OBSERVED
- LICENSE：商业（只学数据口径）
- OBSERVED：检查结果数字化归档、钢丝绳连续监测、running/motor starts/work cycles/brake、维护文档统一、台账导出
- ADOPT：钢丝绳专用四表、检查归档、运行 metrics 口径、文档统一
- REJECT：OEM 专有采集硬件/云、未验证寿命算法
- OUR_IMPLEMENTATION：wire_rope_* + inspections + documents + telemetry metrics
- BATCH：B5

## TP-PROJECT-SCHEDULE — 项目组合排程

- SOURCE：Primavera Cloud
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：商业（只学思想）
- OBSERVED：组合/资源需求/时间分配/资源过载/场景比较/调整解决冲突；活动依赖/约束/基线
- ADOPT：Portfolio、资源日历/预约、确定性冲突检测、Override 留痕
- REJECT：完整 CPM 优化器、自动 leveling（R2）
- OUR_IMPLEMENTATION：Go Scheduling Engine + schedule_* 表 + ECharts 时间轴
- BATCH：B2

## TP-MAINTENANCE-PHASE — 维护阶段推进

- SOURCE：SAP S/4HANA 维护业务
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：商业（只学状态机）
- OBSERVED：initiation/planning/approval/preparation/scheduling/execution/completion 明确阶段推进
- ADOPT：项目生命周期状态机 + allowed transitions + transition 审计
- REJECT：随意 status 下拉
- OUR_IMPLEMENTATION：project_intakes 状态机
- BATCH：B2

## TP-SEARCH — 搜索克制

- SOURCE：PostgreSQL（非 Elasticsearch）
- SOURCE_TYPE：OUR_ARCHITECTURE
- LICENSE：PostgreSQL License
- OBSERVED：GIN/trgm 全文/模糊
- ADOPT：PG + GIN/trgm（需 EXPLAIN/benchmark 后建）
- REJECT：R2 引入 Elasticsearch
- OUR_IMPLEMENTATION：全局搜索（Portfolio/Project 双 scope 过滤）
- BATCH：B4/B6

## TP-REVISION — 时态主数据

- SOURCE：EAM/config mgmt 时态模型
- SOURCE_TYPE：OUR_ARCHITECTURE
- LICENSE：—（自有）
- OBSERVED：entity_revisions + effective_at/recorded_at + valid_from/valid_to
- ADOPT：六层历史 + Configuration@T resolver
- REJECT：generic history 大表、last-write-wins
- OUR_IMPLEMENTATION：entity_revisions + temporal relations/bindings
- BATCH：B6

## TP-SCHED-ENGINE — 冲突检测内核

- SOURCE：Primavera/SAP 冲突思想
- SOURCE_TYPE：OUR_ARCHITECTURE
- LICENSE：—（自有）
- OBSERVED：确定性冲突检测（overlap/over-capacity/dependency/site/runway）
- ADOPT：Go conflict engine（BLOCKING/WARNING/INFO）+ Override 留痕
- REJECT：R2 复杂自动优化器
- OUR_IMPLEMENTATION：schedule_conflicts + overrides
- BATCH：B2

## TP-EAM-COMPLIANCE — 合规字段载体

- SOURCE：TSG 08-2026 / TSG 51-2023 / 第74号令（gov.cn/samr.gov.cn）
- SOURCE_TYPE：PUBLIC_DOCUMENT
- LICENSE：法规（公有）
- OBSERVED：使用登记/定期检验/日管控周排查月调度/履职存档
- ADOPT：字段载体 + 来源等级 + 待核验 + 不生成不宣称合规
- REJECT：自行生成法定设备代码、软件提醒冒充法定结果
- OUR_IMPLEMENTATION：合规字段 + 日周月记录（DAILY_CHECK/WEEKLY_RISK_REVIEW/MONTHLY_SAFETY_REVIEW）
- BATCH：B5
