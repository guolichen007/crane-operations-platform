# Phase1-A1.1-R2 技术模式与 REA 来源登记

> 目的：记录“为什么这样设计”，避免一年后只剩代码不知道来源。REA 只做公开客户端行为/交互结构研究，不绕过登录、不复制闭源/AGPL 源码。

## Evidence 分类

- `REA_OBSERVED`：REA/浏览器对公开页面/我们自己的平台真实观察；
- `PUBLIC_DOCUMENT`：官方公开文档；
- `OUR_ARCHITECTURE`：负责人基于项目约束做出的自有设计；
- `INFERENCE`：推断，不能冒充事实。

## TP-EAM-ASSET — Asset / Device / Equipment 分层

- SOURCE：ThingsBoard Assets/Devices、AWS SiteWise、OPC UA for Machinery、AAS。
- OBSERVED：ThingsBoard 明确 Asset 用于建筑/区域/车辆等抽象实体，Device 是真正连接并产生遥测的实体；OPC Machinery 提供 machine/component identification、state、operation mode、counter、monitoring building blocks。
- ADOPT：Asset→EquipmentInstallation→RuntimeBinding→Telemetry；type-instance；relations；nameplate/counter 语义。
- REJECT：引入上述平台/协议栈。
- BATCH：B1/B3。

## TP-FE-DATAGRID — 高密度台账

- SOURCE：TanStack Table/Virtual 官方文档 + OpenRemote/Maximo 台账交互。
- LICENSE：MIT。
- OBSERVED：TanStack 明确 server-side pagination 与 virtualization 是不同问题；virtualization 只减少 DOM。
- ADOPT：manual server-side paging/sort/filter + virtual rows 在必要页面使用。
- REJECT：10k 行一次拉全前端筛选。
- BATCH：B4。

## TP-FE-SERVER-STATE — 服务端状态管理

- SOURCE：TanStack Query Vue。
- LICENSE：MIT。
- OBSERVED：query key、cache、background refetch、mutation、targeted invalidation、optimistic rollback。
- ADOPT：B4 评估引入，管理 Asset/Equipment/Portfolio 等 HTTP server state；Pinia 不再承担 request cache。
- REJECT：将 high-frequency WebSocket telemetry 塞进 Query cache。

## TP-FE-PRIMITIVE — 可访问 headless UI

- SOURCE：Reka UI。
- LICENSE：MIT。
- ADOPT：Dialog/Popover/Dropdown/Tabs/Tooltip/Command/focus；U·DAO 自有样式。
- REJECT：大 UI Framework 重写视觉。

## TP-FE-MAP — Operations Map

- SOURCE：OpenRemote Map + MapLibre 官方 cluster/marker/popup/custom 3D 示例。
- LICENSE：MapLibre BSD-3-Clause。
- ADOPT：MapProvider 抽象、cluster/popup/quick entry；L1 national、L2 site、L3 asset。
- REJECT：生产直接依赖 demo tiles；行政区中心伪装设备坐标。

## TP-PROJECT-PORTFOLIO — Portfolio/Resource Planning

- SOURCE：Oracle Primavera Cloud Resource Planning。
- PUBLIC DOCUMENT：支持 time-phased resource demand/availability、allocation start 调整、scenario、project selection、over-allocation分析。
- ADOPT：Portfolio、资源日历/需求/预约、Heatmap、conflict、baseline；R2 手动/确定性解决冲突。
- REJECT：完整自动优化/CPM/leveling。
- BATCH：B2/B4。

## TP-SCHED-RANGE — 时间冲突查询

- SOURCE：PostgreSQL Range Types / Exclusion Constraints。
- ADOPT：tstzrange + GiST 支持重叠查询；独占且不可 override 的资源可用 exclusion constraint；其它冲突用 Go engine。
- REJECT：把所有 Warning 冲突硬编码成 DB constraint。

## TP-MAINTENANCE — PM

- SOURCE：IBM Maximo PM。
- PUBLIC DOCUMENT：支持 time-based、meter-based、time+meter whichever first、fixed/floating schedule、PM hierarchy。
- ADOPT：TIME/METER/HYBRID、fixed/floating、next due；R2 先单 Asset/Equipment。
- REJECT：完整 Maximo hierarchy/job plan 复杂度一次照搬。

## TP-MAINT-PHASE — 工作流状态机

- SOURCE：SAP S/4HANA Maintenance。
- PUBLIC DOCUMENT：Maintenance order 可经历 Planning/Approval/Preparation/Scheduling/Execution/Post Execution/Completion 等阶段。
- ADOPT：项目/工单用 explicit allowed transitions + audit。
- REJECT：自由 status dropdown。

## TP-CRANE-DATA — 行车数据口径

- SOURCE：Konecranes Portal/TRUCONNECT、Demag、卫华、微特。
- ADOPT：设备/维护/检查/文档集中；running hours/work cycles/motor starts/emergency/brake/inverter metrics；钢丝绳独立管理。
- REJECT：未验证 OEM 寿命预测公式、专有硬件算法。

## TP-3D-RUNTIME — Browser 3D HMI

- SOURCE：realvirtual-WEB。
- LICENSE：AGPL-3.0，只学模式。
- REA/PUBLIC：3D machine、KPI overlay、设备资料/文档、实时信号、GLB component context。
- ADOPT：semantic_slot→Equipment→Runtime→Telemetry；3D Inspector；KPI/Alarm overlay。
- REJECT：源码、其 scripting runtime。
- BATCH：B7。

## TP-3D-INTERACTION — Viewer 交互

- SOURCE：xeokit、Bentley iTwin、Autodesk Tandem。
- ADOPT：Select/Focus/Isolate/Hide/X-Ray/Tree/Property/Measurement。
- REJECT：AGPL xeokit SDK、商业 iTwin/Tandem 本体。

## TP-DIGITAL-TWIN-WORKSPACE — 3D + 数据并排

- SOURCE：Autodesk Tandem 公开资料/截图。
- OBSERVED：3D、资产/stream、charts、dashboard/数据完整性处在同一个操作上下文。
- ADOPT：Twin 右 Inspector + bottom history/timeline；Data Completeness。
- REJECT：复制 UI 皮肤。

## TP-ASSET-TREE — Tree + Detail + History

- SOURCE：OpenRemote Manager UI。
- OBSERVED：Asset tree 左、详情右；可写属性与 sensor/live 属性区分；Map 点击 Asset 可进入 Details；History panel。
- ADOPT：树/详情/位置/历史、可写/只读字段明确。
- REJECT：其 rules/agent platform。

## TP-HISTORY-TIME — Contextual Time Conductor

- SOURCE：NASA Open MCT。
- LICENSE：NASA/Other，只学交互。
- ADOPT：LIVE/Fixed range、时间窗统一、URL state、panel 联动。
- REJECT：框架本体；非时间页面全局常驻 Time Conductor。

## TP-HISTORY-DASH — Trend + Event Overlay

- SOURCE：Grafana/ThingsBoard/Tandem。
- ADOPT：多 metric、time range、event overlay、history/real-time drilldown。
- REJECT：完整 dashboard widget/rule engine。

## TP-GLTF — 3D 资产格式

- SOURCE：Three.js GLTFLoader。
- PUBLIC：支持 glTF2、Meshopt、KTX2、Draco 等扩展。
- ADOPT：GLB + Meshopt/KTX2，Draco only if benchmark。

## TP-RENDER-PERF — 路由/依赖按需

- SOURCE：Vue Router lazy loading、ECharts core tree-shaking、现有 REA bundle baseline。
- ADOPT：Operations 首屏不 Three；History 才 charts；route/component dynamic import。

## REA 后续批次

- B2：Primavera/SAP/Maximo 公开页面和流程；
- B4：OpenRemote/Tandem/Konecranes 信息架构；
- B7：realvirtual/xeokit/Tandem 3D；
- B8：Open MCT/Grafana/ThingsBoard history；
- B10：A1.1 vs R2 before/after。

每个 Evidence：TARGET/SCREEN/ROUTE/layout/actions/network/lazy/bundle/OBSERVED/INFERENCE/LIMITATION/ADOPT/REJECT。

