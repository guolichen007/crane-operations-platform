# Phase1-A1.1-R2 成熟资产运营平台设计蓝图

> **A2_STARTED=NO**。本阶段固定名称为 **Phase1-A1.1-R2 · 成熟资产运营平台升级**，不进入 A2。
>
> **本轮目标**：把平台从「Premium 展示 / 只读台账」升级为「成熟资产运营平台」——行车资产可完整建档（品牌/制造商/型号目录/序列号/合规档案/真实位置）、钢丝绳与机构部件按目录+安装历史管理、点检/维保工单闭环、服务端分页台账、全局搜索、语义孪生、运营地图快速直达。业务数据可编辑，运行身份与遥测仍由平台独占。
>
> **Git 边界（LOCAL_ONLY）**：基线 `origin/main = 29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18`；本地分支 `feature/phase1-a1-1-r2-asset-operations`；只本地 commit，**禁止 push / PR / merge main / tag / 改远端 ref**。负责人手动 push 后交独立审查。
>
> **工程门禁（全程）**：不进入 A2；不操作生产服务器；不签发真实证书；不修改生产 TLS/DNS/Firebot；不创建真实账号；不启用 production timer；不接真实 PLC；不开放真实远控；不开放公网 Edge 管理端口。新 migration 从 `00005` 起（`00004_web_identity.sql` 已存在），不改 00001~00004；全部 additive-first、forward-only。

---

## 0.0 第三轮架构升级（DESIGN_VERSION=3，本版核心）

第三轮审查把数据中心从「资产台账」升级为 **Crane Digital Asset Graph（行车数字资产图谱）**，本蓝图相应修订，要点如下（细节见 docs/架构/ 与 docs/产品设计/ 四份新文档）：

1. **三层模型**：`Asset（业务资产）→ Equipment（可维护/可更换/可配置设备）→ Runtime Binding（运行连接）→ Telemetry（遥测）`，三者不混为同一实体。
2. **Component → Equipment（全库统一，禁止 component_*）**：所有 `component_*` 命名已清除，最终统一为 `equipment_types / equipment_catalog_items / asset_equipment_installations / equipment_relations / equipment_runtime_bindings / equipment_calibrations / equipment_software_installations`；PLC/LiDAR/Camera/IPC/Switch/VFD/Motor/Brake 是"设备资产"，不是"机械零部件"。**新 migration 禁止创建 `component_catalog_items` / `asset_component_installations`**；`component` 仅允许出现在外部标准描述 / UI 文案的"组成部件"语境。
3. **钢丝绳仍专用四表**（specification/installation/inspection/replacement_event），不并入 generic Equipment，但可通过 equipment_relations 关联 DRUM/HOIST。
4. **四维状态**：Lifecycle / Administrative State / Connectivity / Condition（Condition 必带 source + observed_at）；ONLINE 不得人工填写，未绑定显示"未接入"。
5. **新增能力**：equipment_relations（关系图）、equipment_runtime_bindings、equipment_calibrations（标定）、equipment_software_installations（软件/固件历史）、spare_part_catalog/equipment_spare_part_links/work_order_parts（备件基础）、telemetry metrics 扩展、Physical Semantic Twin + 系统拓扑 2D graph + 传感器 FOV 可视化。
6. **对标**：概念对齐 OPC UA for Machinery / AAS / AWS SiteWise / ThingsBoard，借鉴语义不实现标准栈（见对标研究文档）。

**详细文档**：
- `docs/架构/Phase1-A1.1-R2-行车数字资产图谱.md`（三层模型 + ERD + 完整实例）
- `docs/架构/Phase1-A1.1-R2-设备与运行数据边界.md`（ownership + 四维状态 + runtime binding + metrics）
- `docs/架构/Phase1-A1.1-R2-自动化设备分类与字段模型.md`（taxonomy + 专项字段 + 标定 + 软件 + 备件）
- `docs/产品设计/Phase1-A1.1-R2-成熟平台对标研究.md`（借鉴/不实现/为什么）

**负责人 18 项决定已纳入**：见蓝图各章节与上述文档（法规载体先行、生产上传 FEATURE_OFF、空目录+虚构 fixture、MapProvider 不绑第三方、synthetic 坐标、internal id + asset_code、GLB pipeline+匿名示例、identity capability 扩展、站内提醒、Dark Premium+两档 density、PROJECT scoped catalog、ONLINE 不可手改、credential 不入库、Bulk import 五步、XLSX export 等）。

---

## 0.0b 第四轮设计冻结（DESIGN_VERSION=4，本版新增）

第四轮把两个更高层要求落到规范：**前端 = 成熟工业工作台与数字孪生**；**数据 = 长期保存、变化留痕、历史回看、故障追溯、版本追溯、运行回放**。要点：

1. **前端五 Workspace**：Operations Center / Asset Workspace / Twin Workspace / Maintenance Workspace / History Workspace；Global Shell 含 `LIVE|HISTORY` 模式、Ctrl+K 全局搜索、Project Scope、告警中心。
2. **Three.js 规范**：PBR/HDR/ACES/selective bloom（仅报警/指示灯/选择焦点，禁止整车发光）、Select/Focus/Isolate/Hide/X-Ray/Reset + Overview/Top/Side/Hoist 相机、语义树、传感器 FOV/range/frustum、pose_source 纪律（CALIBRATED/SURVEY=精确、MANUAL=示意）、系统拓扑 2D（Vue Flow MIT 候选，先 PoC）。
3. **工业历史六层**：Current / Business Revision（entity_revisions）/ Lifecycle / Event / Telemetry / Aggregated；统一 Timeline（asset_timeline_events 索引）；Telemetry Metric Catalog（前端禁止 JSON path）；History API（metric/from/to/resolution + 查询保护）；History Explorer；3D Replay（read-only，禁 LIVE 覆盖、禁接远控）；Incident Review（T-30min→T+10min）。
4. **性能预算**：baseline three≈970KB/charts≈1127KB；ECharts 拆 core、Three 按需 opt-in、首屏预算 ≤220KB raw/≤70KB gzip；license 红线（realvirtual/xeokit=AGPL 只学交互，Vue Flow/Reka/TanStack=MIT 可引入，Open MCT=Other 只学模式）。
5. **retention**：业务档案不自动删除；telemetry 可配置但默认不 purge；容量规划用可测量 trigger（rows/day、query P95、disk growth）决定何时 partition/TSDB，不提前重构。

**新增文档（docs/架构/ 与 docs/产品设计/）**：
- 《REA前端优秀平台取证报告》（license 核验 + Evidence Schema + 交互机制）
- 《前端工业工作台与数字孪生规范》（五 Workspace + Three spec + 3D 交互 + 传感器 + 拓扑 + 动画 + headless UI）
- 《工业历史数据与回放架构》（六层 + revisions + timeline + metric catalog + History API + Replay + Incident Review）
- 《前端性能与Bundle预算》（baseline + echarts core 拆包 + Three opt-in + 预算）
- 《历史数据保留与容量规划》（retention + rollup policy + partition/TSDB trigger）

---

## 0. 结论先行

| 维度 | 本轮结论 |
|------|---------|
| 资产数据 | 从 5 个技术字段扩展为**完整全生命周期档案**（制造/型号/合规/位置/部件/钢丝绳/点检/维保/文档） |
| 可编辑 | 业务字段（BUSINESS_OWNED）可编辑；运行身份/遥测（DEPLOYMENT_OWNED / TELEMETRY_OWNED）禁止 UI 伪造 |
| 数据一致性 | Inventory 导入不再覆盖 UI 已改字段（ownership 边界 + 反证测试） |
| 权限 | 显式 capability（12 项），无 role 大小比较；**GLOBAL_ADMIN_BYPASS=NO**；platform_admin 仍先要求 ProjectGrant |
| 台账 | 服务端分页/搜索/排序/筛选，不再全量拉取；Ctrl+K 全局搜索（Project Scope 过滤） |
| 地图 | MapProvider 抽象；Operations Map 与 Space View 语义分离；真实位置→Asset/Site marker，未配置→队列 |
| 孪生 | SemanticTwinManifest + GLB 可选 + procedural fallback；condition 结论必须带 source，不自动判报废 |
| 并发/幂等 | 乐观并发（version + If-Match，409）+ Idempotency-Key + 前端 saving 防重 |

---

## 1. 现状审查（挑刺，精简）

- **只读**：cloud-api 仅 `GET /projects`、`GET /assets/devices`、`GET /devices`、`GET /alarms`、shadow/stream 与 LIVE 会话 POST/DELETE；数据仅 CLI 导入 Inventory YAML。
- **字段缺失**：`asset_devices` 仅 `crane_model/rated_capacity_t/span_m/work_class/grab_type`；线上 `crane.youdaozd.com` bundle 反解 `钢丝绳/品牌/制造商/序列号/维护记录` 命中 0。
- **首页无快速直达**：地图只落 `adminCode` 行政区中心，无真实坐标、无点标记直达设备详情/LIVE。
- **孪生体是硬编码门吊**：`CraneTwinFactory` 固定几何，`ropes` 仅视觉吊索，与档案零关联。
- **导航单薄**：SideRail 仅总览/项目/设备/告警；monitor/analytics/maintenance/remote 为 DEMO 假数据。
- **无分页/并发/幂等**：`listAssets` 全量返回，无 version/If-Match，无幂等。

---

## 2. 对标研究（大企业 + 法规 + 前端 UI）

### 2.1 Konecranes（科尼）——数据口径标杆

- **yourKONECRANES 客户门户**：把设备信息、维护信息、数字化检查结果、数字工具信息集中一处；**检查结果数字化并归入一处**（对应"点检/检验记录 + 文档归档"）。
- **TRUCONNECT Wire Rope Monitoring**：连续掌握钢丝绳状态，含磁性探伤（断丝/磨损/锈蚀），对标 ISO 4309:2017 报废准则——**钢丝绳状态是独立监测对象**，而非资产的一个文本字段。
- **CheckApp**：日常检查数字化（对应"日管控"的数字化载体）。
- 可借鉴：钢丝绳=独立实体 + 安装历史 + 探伤记录；检查结果归档；维护信息与设备档案同门户。

### 2.2 卫华「起重管家 3.0」、微特 WTAU（中国市场）

- 卫华：全生命周期透明管理，"一张网覆盖全国"，设备档案 + 运维 + 备件。
- 微特：钢丝绳无损探伤（永久存档备查）、寿命测算、点检台账、维保工单闭环、档案库 Excel 导出（适配安监/内审）。
- 可借鉴：档案库/台账导出、维保工单闭环、钢丝绳探伤留痕。

### 2.3 中国特种设备法规（字段仅采自官方来源）

> 来源纪律：只引用 samr.gov.cn / gov.cn / 正式法规转载；TSG 08-2026 官方文本在政府网站为**图片扫描版**，字段级内容以正式文本核验为准，本文档凡标注「需核验」处不得在编码时当作已确证事实。

**第74号令《特种设备使用单位落实使用安全主体责任监督管理规定》**（2023 施行，gov.cn 官方原文）第七章「起重机械」明确：
- 使用单位依法配备**起重机械安全总监**与**安全员**，**逐台明确负责的安全员**；
- 制定《起重机械安全风险管控清单》，建立健全**日管控、周排查、月调度**制度与机制；
- 日管控：安全员每日按清单检查，未发现问题也记录（**零风险报告**）；
- 周排查：安全总监每周至少一次风险隐患排查，形成排查治理报告；
- 月调度：主要负责人每月听取汇报，形成调度会议纪要；
- 履职情况**记录并存档备查**。

→ 映射为合规模块：`DAILY_CHECK`（日管控）/ `WEEKLY_RISK_REVIEW`（周排查）/ `MONTHLY_SAFETY_REVIEW`（月调度），并**严格区分：法定要求 / 企业内部记录 / 系统提醒**。软件提醒≠法定检验结果。

**TSG 08-2026《特种设备使用管理规则》**（2026-05-01 施行）：承载使用登记、定期检验、安全技术档案。字段（使用登记证编号、设备代码、登记机关/日期、下次检验日期、安全技术档案完整性）**需核验**——参见 §14 讨论问题。平台只做字段载体，**不生成法定设备代码、不宣称自动取得法定合规**。

**TSG 51-2023《起重机械安全技术规程》**：技术安全要求（金属结构、安全保护装置、主要零部件报废等），作为 `standard_reference` 与合规字段来源，具体条款字段同样**需核验**。

### 2.4 前端 UI 技术对标（方向已定调）

负责人定调：**保留 Premium 视觉基调，但优先级从"更多 glow/glass/大卡片"转为 workflow / information density / editing / search / table / status / history**。参考工业 SaaS 高密度工作台：

- **数据表格**：headless 表格 + 虚拟化（Vue 3 用 TanStack Table + `@tanstack/vue-virtual`），列可见性、列排序、行内状态徽标、服务端分页。
- **表单**：受控 FormField + 校验（VeeValidate/Zod 或轻量自建）+ ValidationSummary + DirtyGuard（未保存离开警告）。
- **选择器**：EntitySelector / LocationSelector / ManufacturerSelector / ModelSelector（目录数据驱动，防自由文本泛滥）。
- **反馈**：Toast、ConfirmDialog、乐观更新失败回滚提示（"数据已被其他用户修改，请刷新后重新确认"）。
- **命令入口**：Ctrl+K 全局搜索（Command Palette）。
- 目标：1920×1080 / 1366×768 两档密度可用，reduced-motion 适配，20 次路由循环无画布泄漏。

---

## 3. 字段所有权模型（本轮核心约束）

### 3.1 三分所有权

| 域 | 含义 | 谁可写 | 谁不可写 |
|----|------|--------|---------|
| **DEPLOYMENT_OWNED** | tenant_id、project immutable identity、asset immutable identity、runtimeDeviceId、sourceMode（REAL/SIMULATION/TEST）、asset-runtime provenance、MQTT identity、未来设备证书身份 | 部署/bootstrap/导入 | 业务 UI 禁止修改 |
| **BUSINESS_OWNED** | asset 展示元数据、manufacturer/brand/model/serial、技术规格、location、equipment、wire rope、inspection、maintenance、document、regulatory metadata、notes | 有权限业务用户（capability） | — |
| **TELEMETRY_OWNED** | connectivity、heartbeat、position telemetry、load、runtime、work cycles、alarm、shadow、live state | 设备/Edge（不可 UI 伪造） | 业务 UI 禁止伪造 |

### 3.2 Inventory Import = deployment bootstrap（所有权边界）

现状 `internal/inventory.Import` 用 `ON CONFLICT DO UPDATE` 覆盖 project/asset 描述字段，与 UI 业务主数据冲突。本轮重新定义为：**Inventory Import = deployment/bootstrap source**。

**规则（最终，取消 `ui_edited` 方案）**：
- **首次创建**：Asset 首次通过 deployment Inventory 创建时，允许 seed BUSINESS_OWNED 初始值。
- **已存在后**：deployment Inventory Import **永远只允许更新 DEPLOYMENT_OWNED**（runtime identity / sourceMode / provenance）；**禁止修改任何 BUSINESS_OWNED 字段**。
- BUSINESS_OWNED 更新只能走：Asset API / UI / 受控 Business Bulk Import。
- project binding、asset identity、runtime identity、sourceMode 始终不可变。

**反证测试（必须落地）**：
1. Inventory import 导入 Asset A，`displayName=OLD`（seed）；
2. UI `PATCH` 该资产，`displayName=NEW`（写审计/版本）；
3. 再次执行**原 Inventory import**（含 `displayName=OLD`）；
4. 查询 Asset A：必须 `displayName=NEW`；同时 runtime identity、project binding、sourceMode 均保持不变。

实现手段：Import 事务对已存在资产仅写 DEPLOYMENT_OWNED 列，不触碰 BUSINESS_OWNED 列（**无需 `ui_edited` 标记**）；反证测试锁定该行为。

### 3.3 lifecycle 与 asset_status

- 保留现有 `lifecycle`（PLANNED/PRE_DEVELOPMENT/COMPLETED）与 project lifecycle 的既有耦合，**本轮不改其语义**。
- 新增 `asset_status`：`DRAFT / ACTIVE / OUT_OF_SERVICE / RETIRED`，作为资产在"运营"维度的状态；`RETIRED` 与软删协作（有历史不可物理删）。
- 未来再单独设计 lifecycle 迁移，本轮不删旧语义。

---

## 4. 目标数据模型（字段级）

> 约定：`PK`=主键、`FK`=外键、`*`=可空、`snapshot`=资产保存目录快照而非引用（目录变更不改历史资产参数）。

### 4.1 `asset_devices`（immutable identity anchor + 业务扩展）

保留现有十列不动，新增（全部 nullable，additive）：

| 字段 | 类型 | 归属 | 说明 |
|------|------|------|------|
| `asset_status` | enum | BUSINESS | DRAFT/ACTIVE/OUT_OF_SERVICE/RETIRED |
| `manufacturer_id` | text FK* | BUSINESS | 指向 manufacturers（snapshot 冗余见下） |
| `model_catalog_id` | text FK* | BUSINESS | 指向 asset_model_catalog |
| `brand` | text* | BUSINESS | 品牌（snapshot） |
| `model_code` | text* | BUSINESS | 型号代码（snapshot） |
| `serial_number` | text* | BUSINESS | 出厂/序列号 |
| `manufacture_date` | date* | BUSINESS | 出厂日期 |
| `installation_date` | date* | BUSINESS | 安装日期 |
| `commissioning_date` | date* | BUSINESS | 投运日期 |
| `rated_capacity` | numeric* | BUSINESS | 额定起重量（t，snapshot） |
| `span` | numeric* | BUSINESS | 跨度（m，snapshot） |
| `lifting_height` | numeric* | BUSINESS | 起升高度（m） |
| `work_class` | text* | BUSINESS | 工作级别（已有） |
| `application_type` | text* | BUSINESS | 应用类型 |
| `control_mode` | text* | BUSINESS | 控制方式 |
| `special_equipment_code` | text* | BUSINESS(合规) | 设备代码【需核验 TSG 08 字段】 |
| `use_registration_no` | text* | BUSINESS(合规) | 使用登记证编号【需核验】 |
| `registration_date` | date* | BUSINESS(合规) | 登记日期 |
| `registration_authority` | text* | BUSINESS(合规) | 登记机关 |
| `design_service_life` | int* | BUSINESS(合规) | 设计使用年限（年） |
| `latest_inspection_date` | date* | BUSINESS(合规) | 最近法定检验日期 |
| `inspection_valid_until` | date* | BUSINESS(合规) | 检验有效期至 |
| `technical_archive_status` | enum* | BUSINESS(合规) | 安全技术档案完整性状态 |
| `notes` | text* | BUSINESS | 备注 |
| `version` | bigint NOT NULL | 并发 | 乐观锁，默认 1 |

> 设备代码/使用登记号等**不在代码中生成**；`special_equipment_code` 仅作为录入载体。目录字段（manufacturer/model）采用 **snapshot 冗余**：保存时复制目录值到资产列，目录后续变更不静默改变历史资产参数。

### 4.2 目录表

**`manufacturers`**：`id PK / name / short_name / status(ACTIVE/DISABLED) / version`。被引用禁止 hard delete（DISABLED 代替）。

**`asset_model_catalog`**：`id PK / manufacturer_id FK / brand / model_code / asset_type / technical_defaults(jsonb) / status(ACTIVE/DISABLED) / version`。`technical_defaults` 存型号默认技术参数（额定起重量/跨度/起升高度等），新增资产选型号时预填。

### 4.3 位置层级与安装历史

**`asset_locations`**（层级 SITE→PLANT→WORKSHOP→BAY→RUNWAY，kind 枚举 SITE/PLANT/WORKSHOP/BAY/RUNWAY/OTHER）：

`id PK / project_id FK / parent_id FK* / kind / code / name / address / province / city / county / longitude / latitude / coordinate_system(WGS84/GCJ02/LOCAL_SITE/UNKNOWN) / coordinate_source(MANUAL/SURVEY/IMPORTED/SITE_CENTER) / accuracy_m / local_x_m / local_y_m / local_z_m / orientation_deg / status / version`。

> 不进行未经约定的静默坐标转换；`coordinate_source` 记录坐标来源，不直接 UPDATE 抹掉历史。

**`asset_installations`**（一资产可历史移动，同一时间仅一个 active）：

`id PK / asset_id FK / location_id FK / started_at / ended_at* / active / local_x_m / local_y_m / local_z_m / orientation_deg / source / notes / version`。

> 唯一约束 + 事务保护「同资产同时仅一个 active installation」；退役/移装用 `ended_at + active=false` 表达历史。

### 4.4 钢丝绳（四表，不承担混语义）

**`wire_rope_specifications`**：`id PK / manufacturer / brand / model / product_code / construction / nominal_diameter_mm / nominal_length_m / grade / lay_direction / lay_type / core_type / coating / minimum_breaking_force_kn / standard_reference / status / version`。

**`wire_rope_installations`**：`id PK / asset_id FK / equipment_installation_id FK* / specification_id FK* / serial_number / batch_number / supplier / position_label / installed_at / commissioned_at / removed_at* / runtime_at_install / cycles_at_install / status(ACTIVE/REMOVED/RETIRED) / notes / version`。

**`wire_rope_inspections`**：`id PK / installation_id FK / asset_id FK(NOT NULL) / inspection_type / inspected_at / inspector / measured_diameter_mm / broken_wire_observation / corrosion / deformation / lubrication / rope_seating / finding / conclusion / next_due_at / standard_reference / version`（附件用 document_links 关联，见 4.7）。

**`wire_rope_replacement_events`**：`id PK / asset_id FK / old_installation_id FK / new_installation_id FK / work_order_id FK* / replaced_at / reason / version`。

> **旧绳不 UPDATE 成新绳**：旧 installation 置 `REMOVED/RETIRED`，新 installation 置 `ACTIVE`，替换事件记录关联。**不实现** `broken_wire_count>X→必须报废` 之类的未验证自动算法；寿命估计若保存，必须含 `estimate_value / estimate_source / estimate_method / algorithm_version / observed_at / confidence / review_status`，无来源为 `UNAVAILABLE`。`standard_reference` 用 `ISO 4309:2017`（Edition 6 未正式发布，不把草案当正式执行标准）。

### 4.5 设备（Equipment，目录 + 安装分离）

**`equipment_catalog_items`**：`id PK / manufacturer / brand / model / category / specifications(jsonb) / status / version`。category 用**可扩展枚举**（存 varchar + 语义集），至少：HOIST/HOIST_MOTOR/HOIST_REDUCER/HOIST_BRAKE/DRUM/WIRE_ROPE/HOOK/GRAB/TROLLEY_MOTOR/TROLLEY_REDUCER/TROLLEY_BRAKE/TROLLEY_WHEEL/GANTRY_MOTOR/GANTRY_REDUCER/GANTRY_BRAKE/GANTRY_WHEEL/PLC/VFD/CONTACTOR/ENCODER/LIMIT_SWITCH/LOAD_SENSOR/ANTI_COLLISION/OTHER。不做大硬编码 enum，未来可扩展。

**`asset_equipment_installations`**：`id PK / asset_id FK / parent_installation_id FK* / catalog_item_id FK* / semantic_slot / position_label / serial_number / batch_number / installed_at / commissioned_at / removed_at* / status(ACTIVE/REMOVED/RETIRED) / notes / version`。

### 4.6 点检/维保/合规

**`inspections`**（合规档案 + 企业检查；`asset_id` 始终 NOT NULL，避免 scope 解析混乱）：

`id PK / asset_id FK(NOT NULL) / equipment_installation_id FK* / wire_rope_installation_id FK* / type(DAILY_CHECK/WEEKLY_RISK_REVIEW/MONTHLY_SAFETY_REVIEW/REGULAR_INSPECTION/SELF_CHECK/OTHER) / inspected_at / inspector / result / summary / next_due_at / standard_reference / final(immutable flag) / version`。

> 数据库校验子对象（component/wire_rope installation）属于同一 asset；Project Scope 恒从 `inspection → asset → project` 解析，不信任 body project_id。`type` 明确区分：法定定期检验 vs 企业自行检查 vs 系统提醒。

**`maintenance_plans`**：`id PK / asset_id FK / title / recurrence / next_due_at / status / version`。

**`work_orders`**：`id PK / project_id FK / asset_id FK / location_id FK* / equipment_installation_id FK* / wire_rope_installation_id FK* / type / priority / status(DRAFT/OPEN/IN_PROGRESS/WAITING/COMPLETED/CANCELLED) / title / description / created_by / assigned_to / created_at / due_at / started_at / completed_at / result / notes / version`。

**`work_order_events`**：`id PK / work_order_id FK / status_from / status_to / actor / at / note / version`。状态流转后端校验状态机；已完成工单禁止删除。

**`replacement_events`**：`id PK / asset_id FK / equipment_installation_id* / old_value / new_value / work_order_id FK* / replaced_at / reason / version`。

### 4.7 文档（不塞 jsonb，不放大二进制进 PG）

**`documents`**：`id PK / category / file_name / content_type / byte_size / sha256 / storage_provider / storage_key / uploaded_by / uploaded_at / version`。

**`document_links`**：`id PK / document_id FK / ref_type(asset/equipment/wire_rope/inspection/work_order) / ref_id / version`。

> 大型二进制不放 PostgreSQL；建立 storage interface，开发用受控 local backend；浏览器不持有长期对象存储 secret；生产对象存储未定前不绑定具体厂商。

### 4.8 通用约束

- **历史默认 RESTRICT/NO ACTION**，不 CASCADE；生命周期终点用状态而非物理删：Asset→RETIRE、Location→DISABLED、Component/Wire rope installation→REMOVED/RETIRED、Work order→CANCELLED/COMPLETED、Inspection→final 后 immutable 或审计更正。仅「从未使用的 DRAFT 且无任何引用」允许 platform_admin hard delete。
- **所有 mutable 业务实体加 `version bigint`**；更新用 `PATCH` + `If-Match` 或 `request.version`；`UPDATE ... WHERE id=? AND version=?` 成功则 version+1，0 rows 返回 409（前端提示"数据已被其他用户修改，请刷新后重新确认"），禁止 last-write-wins。
- **重要创建**（Asset/Component installation/Wire rope installation/Inspection/Work order）要求 `Idempotency-Key`，重复提交不产生两条记录；前端按钮 saving 禁重复点击。

---

## 5. 迁移规划（additive-first，forward-only）

| 文件 | 内容 |
|------|------|
| `00005_asset_master_location.sql` | asset_devices 扩展列 + asset_status + manufacturers + asset_model_catalog + asset_locations + asset_installations + 唯一 active installation 约束 |
| `00006_equipment_wire_rope.sql` | equipment_catalog_items + asset_equipment_installations + wire_rope_specifications + wire_rope_installations + wire_rope_inspections + wire_rope_replacement_events |
| `00007_maintenance_compliance.sql` | inspections + maintenance_plans + work_orders + work_order_events + replacement_events + 合规字段索引 |
| `00008_documents_search.sql` | documents + document_links + 全文/搜索辅助索引（必要时） |

原则：不把所有表塞进一个巨大 SQL；不动 00001~00004；不 DROP/RENAME 现有生产字段、不重写主键、不改 issuer/sub、不改 runtime provenance；生产 rollback 目标 = 旧 app 可继续运行 + 数据库 backup restore。

---

## 6. API 设计（resource-oriented）

### 6.1 端点清单（统一显式 Project Scope 上下文）

- `GET /api/v1/projects`（现状，保留）
- `GET /api/v1/projects/{projectId}/assets`（分页）· `POST /api/v1/projects/{projectId}/assets`（新建，走 Wizard/DRAFT）
- `GET/PATCH /api/v1/projects/{projectId}/assets/{assetId}`
- `POST /api/v1/projects/{projectId}/assets/{assetId}/retire`
- `GET/POST .../assets/{assetId}/equipment` · `GET/PATCH/DELETE .../equipment/{equipmentId}`（安装/移除/替换）
- `GET/POST .../assets/{assetId}/wire-rope` · `GET/PATCH .../wire-rope/{installationId}`（spec/install/inspection/replacement 子资源）
- `GET/POST .../assets/{assetId}/inspections` · `GET/PATCH .../inspections/{id}`（final 后 immutable）
- `GET/POST .../assets/{assetId}/work-orders` · `GET/PATCH .../work-orders/{id}` · `POST .../work-orders/{id}/events`（状态流转）
- `GET/POST .../assets/{assetId}/documents` · document_links 关联
- `GET /api/v1/search?q=`（全局搜索，Project Scope 过滤）
- `GET /api/v1/projects/{projectId}/locations`（层级）

**明确删除第一版设计中的** `POST /api/v1/projects`、`DELETE /api/v1/projects`：platform_admin 是 Project Scope 内管理员，非 tenant/global admin；不存在安全的「无 Project Scope 用户创建新 Project」授权语义。新 Project 继续用受控 bootstrap/admin 工具创建并授予 membership。**GLOBAL_ADMIN_BYPASS=NO**。

### 6.2 分页 / 搜索 / 排序（新台账 API 统一）

所有列表 API：请求 `page / pageSize / search / sort / filters`；响应 `{ items, page, pageSize, total }`。资产 1000+ 也不全量拉浏览器过滤；后端合并拉取，避免 N+1（子表计数用聚合，非逐条查询）。

### 6.3 并发 / 幂等 / 权限解析

- 写操作带 `If-Match`（或 `request.version`），version 不匹配 409。
- 创建带 `Idempotency-Key`，重复提交幂等。
- 子对象权限解析：后端从 DB `child → asset → project` 解析，**不信任 body project_id**；跨项目对象统一 404/403（不泄露存在性）。
- 所有 mutation 审计：`actor / project / asset / object_type / object_id / action / request_id / observed_at`，before/after 用安全 diff；**禁止**审计日志出现 password/token/session cookie/private key/OIDC secret；位置属敏感信息不进公开日志/fixture。

---

## 7. RBAC capability 模型

**不实现 `operator+`/`maintainer+` 隐式角色大小比较**；沿用 `identity.Allowed` 显式 action，扩展为显式 capability。

| capability | 含义 |
|-----------|------|
| `read` | 只读查看 |
| `watch` | 申请 LIVE 查看 |
| `maintenance_request_create` | 新建维保请求/工单 |
| `maintenance_write` | 维保工单编辑/流转 |
| `inspection_write` | 点检/检验记录写入 |
| `equipment_condition_write` | 部件状态写入 |
| `wire_rope_write` | 钢丝绳安装/探伤写入 |
| `asset_master_write` | 资产主数据/identity/spec 写入 |
| `location_write` | 位置写入 |
| `catalog_write` | 制造商/型号目录写入 |
| `asset_retire` | 资产退役 |
| `document_write` | 文档上传/关联 |

**角色映射**：

| 角色 | capabilities |
|------|-------------|
| viewer | read |
| operator | read, watch, maintenance_request_create |
| maintainer | read, watch, maintenance_request_create, maintenance_write, inspection_write, equipment_condition_write, wire_rope_write, document_write |
| platform_admin | 上述全部 + asset_master_write, location_write, catalog_write, asset_retire |

> Asset CREATE / RETIRE / 主数据 identity/spec 不交给普通 maintainer；platform_admin 仍首先要求显式 ProjectGrant，无 global bypass。viewer 无任何 write CTA；maintainer 只见 maintenance 域能力；platform_admin 仅在持有 Project Scope 时编辑 master。

---

## 8. 前端信息架构与交互

### 8.1 导航重构

侧栏按业务域分组（api 模式）：

- **运营中心**：全国运营态势（Space View / Operations Map）
- **资产中心**：行车台账、设备台账、钢丝绳、位置、型号目录
- **运行监控**：设备实时状态、数字孪生、告警
- **维保中心**：点检、工单、维护计划、更换记录、备件
- **合规中心**：使用登记、定期检验、日管控/周排查/月调度、技术档案
- **数据治理**：文档、导入、导出、审计

demo 的 monitor/analytics/maintenance/remote 在 api 模式移除或标注"规划中"；A2 前不出现远程控制、设备 Enrollment、公网 MQTT 管理入口。

### 8.2 行车资产台账（真正台账）

`InventoryDevices.vue` → 升级为服务端分页表格：server-side pagination、search、sort、filter、column visibility；操作按 capability 显隐：新增（Wizard，platform_admin）、编辑、查看、退役。**新增不用巨大 Modal，用 Wizard**：1 身份 → 2 制造与型号 → 3 技术参数 → 4 合规档案 → 5 部署位置 → 6 初始设备 → 7 文档 → 8 Review；支持 DRAFT 暂存。

### 8.3 资产详情 `/assets/{assetId}`（Asset 360）

Header：asset code / name / status / project / site / connectivity；Action：编辑档案、创建工单、新增点检（按 capability）。

Tab 结构：概览 / 实时运行 / 数字孪生 / 部署位置 / 设备 / 钢丝绳 / 告警 / 点检 / 维保工单 / 合规档案 / 资料文档 / 变更历史。

### 8.4 合规档案

展示：设备代码、使用登记、最近法定检验、有效期、档案完整性；**明确区分法定检验 vs 企业自行检查**。首页可加"即将到期/已到期/档案待补充"提示，但**不用模拟数据冒充正式结果**。承载 DAILY_CHECK/WEEKLY_RISK_REVIEW/MONTHLY_SAFETY_REVIEW 三类记录（区分法定要求/企业记录/系统提醒）。

### 8.5 全局搜索（Ctrl+K）

后端 authorized search endpoint；检索字段：Asset ID / Asset name / special equipment code / serial number / site / project / manufacturer / brand / model / wire rope serial/batch / work order；严格 Project Scope 过滤。前端 Command Palette 直达资产详情。

### 8.6 组件化

`DataGrid / FormField / DateField / EntitySelector / LocationSelector / ManufacturerSelector / ModelSelector / ValidationSummary / Toast / ConfirmDialog / DirtyGuard`（离开未保存表单警告）。saving 状态禁重复点击。

---

## 9. 地图抽象（Operations Map vs Space View）

- **MapProvider abstraction**：`marker / cluster / popup / fitBounds / select / asset quick entry` 接口；核心业务不耦合某供应商；本轮可研究 MapLibre，但**禁止生产依赖 demo tiles**、禁止未经许可绑定第三方地图 API（第三方底图密钥不落前端，走 BFF/受控配置）。
- **Space View（Three.js 全国沙盘）保留**：行政区示意，明确标注"非设备精确位置"。
- **Operations Map（运营地图）语义分离**：真实 Asset location → Asset marker；仅 Site location → Site marker；仅行政区 → 项目/区域聚合；完全未配置 → 进入"位置未配置"列表。
- 地图点点击立即出现 Asset/Site card；Asset card 含"进入资产 / 查看实时状态"，不再要求 首页→项目→资产列表→搜索→详情 的长路径。

---

## 10. three.js 语义孪生

- **保留** Three.js + CraneTwinFactory + QualityManager + postprocessing + dispose + 现有四轴 hierarchy；**不迁 TresJS**。
- 新增 **SemanticTwinManifest**：核心 `semantic_slot`（HOIST_MAIN/HOIST_MOTOR/HOIST_BRAKE/HOIST_REDUCER/DRUM/WIRE_ROPE_1/WIRE_ROPE_2/TROLLEY_MOTOR/GANTRY_MOTOR_FL/PLC/VFD_HOIST…）。
- Three node **不直接绑定 DB installation ID**；映射链：`GLB/procedural node → semantic_slot → 当前 equipment installation`。点击模型：Raycaster → semantic_slot → business equipment → Inspector。
- `spanM/liftingHeight` 驱动参数化示意，但显示 `PARAMETRIC_SCHEMATIC`，不标 `AS_BUILT`。模型准确度枚举：`GENERIC / PARAMETRIC / CATALOG / AS_BUILT`，仅真实 CAD/测量模型可标 AS_BUILT。
- **不做自动安全结论**：不因断丝数量自动红、不因直径磨损自动判报废色；Three.js 状态仅由**带 source 的 `condition_state`**（NORMAL/WARNING/ALARM/UNKNOWN）驱动；只有 raw inspection 时以信息方式展示，不模拟安全结论。
- GLB 支持：Three.js GLTFLoader；可选 Meshopt/Draco/KTX2，版本固定、local decoder、CSP 兼容、license 记录、**无第三方运行时 CDN**；无 GLB 继续 procedural fallback。

---

## 11. 分阶段路线图 + 测试 + 提交切分

### 11.1 分段提交（中文 Conventional Commits）

- **R2-0**：docs/rea 研究、架构冻结、AGENTS 阶段
- **R2-1**：contracts + migration + ownership + RBAC capability
- **R2-2**：Asset Master / Location / Catalog / API
- **R2-3**：Components / Wire Rope / Compliance
- **R2-4**：Inspection / Maintenance / Documents
- **R2-5**：Asset Center / Map / Global Search
- **R2-6**：Semantic Twin / GLB
- **R2-7**：UX / performance / accessibility
- **R2-8**：tests / counterproof / docs

示例：`feat(资产): 建立行车主数据与部署位置模型`。

### 11.2 测试覆盖（每 Stage 完成即测）

contracts generation；migration fresh DB + 从当前 00004 升级；旧 app 兼容新 additive DB；**Inventory import 非覆盖**；asset CRUD；location hierarchy；installation history；manufacturer/model 目录；component install/remove/replace；wire rope spec/install/inspection/replacement history；maintenance；documents metadata；**乐观并发 409**；**幂等**；CSRF；Project Scope；viewer 只读；operator/maintainer/platform_admin capability；**无 global bypass**；跨项目对象 404/403；全局搜索 scope；分页；地图快速进入；语义孪生 picking；WebGL fallback；20 次路由循环 dispose、无重复 canvas；reduced-motion；1920×1080 / 1366×768 响应式。

### 11.3 验证命令（按仓库真实 task）

`node tools/task.mjs contract` → `lint` → `test` → `build` → `integration-test` → `a1-test` → `production-contract` → `a1-production-test` → `security`。环境原因无法执行的步骤记为 **NOT_RUN**，不写 PASS。

### 11.4 开发前后对比取证（rea）

- 开发前 `capture A1.1`，开发后 `capture R2`；比较 routes/network/DOM/accessibility/bundle/screenshots。
- 重点检查：有无 N+1、有无巨大 bundle、是否真正 lazy load、有无敏感数据泄漏、viewer 是否出现 write UI、Asset Detail 是否有闭环操作。
- 每个结论建立 **REA Evidence Manifest**：`target / target_type / rea_version / rea_commit(或 package_version) / capture_id(evidence_id) / url / script_sha256 / timestamp / observed / limitations / conclusion`，分类 `REA_OBSERVED / PUBLIC_DOCUMENT / INFERENCE`；禁止把 INFERENCE 当事实。
- 公开竞品研究：不绕过登录、不破解、不绕过付费、不保存 session、不提交第三方 bundle；第三方原始 evidence 存 `local-private/research`（gitignore），仓库只放脱敏结论。

---

## 12. 风险与边界

- 法规字段（设备代码/登记号/检验有效期）只做载体，不生成、不宣称合规；TSG 08-2026 字段以正式文本核验为准。
- 真实坐标/位置属敏感业务信息，不进公开日志/fixture；公开仓库只放匿名示例（偏移坐标）。
- 运行来源不可变、身份/档案分离、Telemetry 不可 UI 伪造是既有安全边界，CRUD 不得绕过。
- 不接真实 PLC、不实现真实远控、不开 Edge 管理端口、远程会话/点云仍 Phase 2 禁用。
- 大二进制/对象存储用 storage interface + 受控 local backend，不绑定未定厂商，浏览器不持长期 secret。

---

## 13. 需与统筹方（gpt）讨论的开放问题

> 以下为进入编码前需敲定的决策点；每项给出我方建议，等待统筹方定夺。

1. **法规字段确证来源**：TSG 08-2026 官方文本在政府网站为图片扫描版，字段级内容无法程序化核验。是否按「载体字段 + 来源等级标注（需核验）+ 不生成不宣称合规」先行落地，正式文本到位后再补字段语义？（建议：先行，风险可控）
2. **对象存储本地 backend**：生产对象存储未定。文档模块本轮是否先做 metadata + storage interface + 本地磁盘 backend，还是暂缓上传只做 `documents/document_links` 骨架？（建议：先骨架 + 本地 backend）
3. **目录初始数据**：manufacturers / asset_model_catalog 是否预置常见品牌（卫华/科尼/法兰泰克/纽科伦/新乡起重 等）与型号，还是空目录由用户录？（建议：空目录 + 用户自录，避免硬编码品牌偏好）
4. **地图底图来源**：引入 MapLibre 后生产 tiles 用何底图（禁止 demo tiles、禁止未许可第三方 API）？还是先用现有 GeoJSON 行政区 + 本地平面坐标呈现 Operations Map？（建议：先本地坐标系，MapProvider 抽象留口）
5. **坐标精度与匿名**：`accuracy_m` 精度分级如何定义？公开仓库示例坐标的匿名偏移方案？（建议：公开示例偏移到行政区级，真实坐标仅 local-private）
6. **资产内部编号规则**：`asset_device_id`/`serial_number` 的编号是否由用户手工录入，平台不自动生成（法定设备代码已明确不生成）？（建议：手工录入 + 平台只做格式校验与唯一性）
7. **GLB 模型来源**：语义孪生 GLB 模型由谁提供？本轮是否只做 procedural + `semantic_slot` 映射骨架，GLB 管线留接口？（建议：只做骨架，GLB 后置）
8. **capability 落地方式**：12 capability 是新建独立授权层，还是直接在现有 `identity.Allowed` 上扩展 action 枚举？（建议：扩展现有 action 枚举，最小改动）
9. **合规模块提醒机制**：日管控/周排查/月调度的"系统提醒"是否只做到期列表提示（不做外部通知渠道），避免误当法定结果？（建议：仅平台内到期提示 + 记录，不接短信/邮件）
10. **前端视觉基调**：高密度工作台的密度档位（compact/comfortable）、状态色板、表格字号行高，是否延续现有深色 Premium 还是新增浅色高密度主题？（建议：延续深色 + 增加 density 切换）

---

## 14. 附录：参考来源

**法规（官方）**
- 市场监管总局令第74号《特种设备使用单位落实使用安全主体责任监督管理规定》：[gov.cn 原文](https://www.gov.cn/zhengce/202305/content_6857842.htm)、[samr.gov.cn](https://www.samr.gov.cn/zw/zfxxgk/fdzdgknr/fgs/art/2023/art_df3582cb24ae497ba18d9667ecbb63b7.html)
- TSG 08-2026《特种设备使用管理规则》（2026-05-01 施行，来源：国家市场监督管理总局；政府网站转载为图片版）：[锦屏县政府转载](https://www.jinping.gov.cn/zfbm/scjdglj/zcwj_5697858/202607/t20260720_90640731.html)
- TSG 51-2023《起重机械安全技术规程》

**大企业平台（数据口径对标）**
- Konecranes yourKONECRANES 维护信息：[Maintenance information](https://www.konecranes.com/en-ie/service/yourkonecranescom/maintenance-information-on-yourkonecranescom)
- Konecranes TRUCONNECT Wire Rope Monitoring：[brochure](https://www.konecranes.com/sites/default/files/2024-09/brochure_truconnect_wire_rope_monitoring_2024.pdf)
- Konecranes TRUCONNECT 远程服务：[中文](https://www.konecranes.com.cn/sites/china/files/2023-05/infographic_truconnect_remote_service_cn.pdf)
- 卫华「起重管家 3.0」：[河南卫华](https://www.henanweihua.com.cn/news/2166/)、[卫华集团全生命周期透明管理](https://articles.e-works.net.cn/erpoverview/article154808.htm)
- 微特技术·全生命周期运维管理系统：[wtauserver](https://www.wtauserver.com/full-lifecycle-operation-and-maintenance-management-system-for-lifting-equipment/)

**前端 UI 技术**
- TanStack Table + Virtual（Vue 3 headless 表格/虚拟化）；shadcn/ui 式 headless 组件设计语言（本项目 Vue 3 对应自建组件）；工业 IoT 高密度 dashboard 组件（Warren UI 等参考）

**逆向工具**
- rea（morluto/rea，v6.8.0，Node 22.23.4 兼容）：[GitHub](https://github.com/morluto/rea)


