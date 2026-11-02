# Phase1-A1.1-R2 成熟平台能力地图（Capability Map）

> 七大业务域 + 三技术底座的能力清单。字段定义：CAPABILITY_ID（能力编号）/ NAME（名称）/ BUSINESS_VALUE（业务价值）/ R2_SCOPE（MUST=本轮实现 / LATER=设计预留不深做 / A2_PLUS=A2 以后）/ OWNER_DOMAIN（所属域）/ DEPENDENCIES（依赖）/ PRIMARY_UI（主要界面）/ PRIMARY_API（主要接口）/ DATA_ENTITY（数据实体）/ SECURITY_SCOPE（权限域）/ REFERENCE_PATTERN（参考模式，见《技术模式与REA来源登记》）。
> R2_SCOPE 依据《范围控制》：预算/采购/完整库存/合同结算/自动CPM/自动leveling/AI助手/外部通知=LATER；真实Enrollment/证书生命周期/公网MQTT/远控/PLC命令=A2_PLUS。

## 一、项目交付与资源排期（Project Delivery & Portfolio）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-PJ-001 | 项目登记(Intake) | MUST | 新项目先登记不污染正式资产 | — | 项目中心·新项目登记 | POST /portfolio/intakes | project_intakes | Portfolio | Primavera/SAP |
| CP-PJ-002 | 项目组合(Portfolio) | MUST | 一眼看全在途项目 | intake | Portfolio Overview | GET /portfolio | project_intakes | Portfolio | Primavera Cloud |
| CP-PJ-003 | 项目状态(Lifecycle) | MUST | 阶段受控不随意改 | intake | 项目详情·状态 | PATCH /portfolio/intakes/{id} | project_intakes | Portfolio | SAP EAM |
| CP-PJ-004 | 项目里程碑 | MUST | 按模板生成节点 | 状态机 | 项目详情·里程碑 | /portfolio/{id}/milestones | project_milestones | Portfolio | SAP |
| CP-PJ-005 | 项目时间轴 | MUST | 看进场/延期/依赖 | milestone | Calendar/Gantt | /portfolio/schedule | schedule_items | Portfolio | Primavera |
| CP-PJ-006 | 资源排期 | MUST | 谁什么时间做什么 | calendar | Resource Heatmap | /schedule/reservations | resource_reservations | Portfolio | Primavera |
| CP-PJ-007 | 施工冲突 | MUST | 抢人/抢现场/抢设备 | 排期 | Conflict Center | /schedule/conflicts | schedule_conflicts | Portfolio | Primavera |
| CP-PJ-008 | 项目风险 | MUST | 早暴露延期/停机 | — | 项目详情·风险 | /portfolio/{id}/risks | project_risks | Portfolio | 通用PM |
| CP-PJ-009 | 项目问题 | MUST | 现场问题跟踪 | — | 项目详情·问题 | /portfolio/{id}/issues | project_issues | Portfolio | 通用PM |
| CP-PJ-010 | 变更申请 | MUST | 变更留痕可解释 | — | 项目详情·变更 | /portfolio/{id}/changes | change_requests | Portfolio | 通用PM |
| CP-PJ-011 | 调试/试运行 | MUST | 调试记录 | — | 项目详情 | /portfolio/{id}/commissioning | commissioning_records | Portfolio | SAP |
| CP-PJ-012 | 验收 | MUST | 验收留痕 | 调试 | 项目详情 | /portfolio/{id}/acceptance | acceptance_records | Portfolio | SAP |
| CP-PJ-013 | 整改项(Punch) | MUST | 未关阻塞移交 | 验收 | 项目详情 | /portfolio/{id}/punch-items | punch_items | Portfolio | SAP |
| CP-PJ-014 | 项目移交(Handover) | MUST | 正式闭环 | 整改 | 项目详情 | /portfolio/{id}/handover | handover_checklists | Portfolio | SAP |
| CP-PJ-015 | 质保(Warranty) | MUST | 质保到期跟踪 | 移交 | 项目/资产·质保 | /warranties | warranties | Portfolio/Project | Konecranes |

## 二、资产运营（Asset Operations）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-AS-001 | 资产(Asset) | MUST | 行车档案 | — | 行车台账/Asset 360 | /projects/{p}/assets | assets | Project | Maximo |
| CP-AS-002 | Equipment | MUST | 设备资产 | asset | 设备台账 | /assets/{id}/equipment | asset_equipment_installations | Project | Maximo |
| CP-AS-003 | Location | MUST | 真实位置 | — | 位置管理 | /projects/{p}/locations | asset_locations | Project | AAS/SiteWise |
| CP-AS-004 | Catalog | MUST | 型号/品牌目录 | — | 型号目录 | /catalog | equipment_types/catalog | Project | AAS |
| CP-AS-005 | Supplier | MUST | 供应商轻档案 | — | 供应商 | /partners | business_partners | Project | Konecranes |
| CP-AS-006 | Spare Part(轻) | MUST | 工单用件可追溯 | — | 备件 | /spare-parts | spare_part_catalog | Project | Maximo |

## 三、实时运行（Realtime Operations）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-RT-001 | 实时状态 | MUST | 在线/模式/位置 | runtime | 运行监控 | /devices/{id}/shadow | device_shadow | Project | Konecranes |
| CP-RT-002 | 告警 | MUST | 告警中心 | 实时 | 告警中心 | /alarms | alarms | Project | ThingsBoard |

## 四、数字孪生（Digital Twin）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-DT-001 | 数字孪生 | MUST | 3D=业务入口 | equipment | Twin Workspace | — | twin_models/semantic | Project | realvirtual |
| CP-DT-002 | 系统拓扑 | MUST | 控制/网络/供电视图 | relations | 拓扑视图 | /assets/{id}/topology | equipment_relations | Project | 工业拓扑 |

## 五、运维可靠性（Maintenance & Reliability）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-MT-001 | 点检(Inspection) | MUST | 检查记录+到期 | — | 点检台账 | /assets/{id}/inspections | inspections | Project | Konecranes CheckApp |
| CP-MT-002 | 维护计划(PM) | MUST | TIME/METER/HYBRID | — | 维护计划 | /assets/{id}/maintenance-plans | maintenance_plans | Project | Maximo |
| CP-MT-003 | 工单(Work Order) | MUST | 闭环状态机 | PM | 工单中心 | /assets/{id}/work-orders | work_orders | Project | Maximo/SAP |
| CP-MT-004 | 标定(Calibration) | MUST | 标定历史 | — | 标定 | /equipment/{id}/calibrations | equipment_calibrations | Project | 自有 |
| CP-MT-005 | 软件/固件 | MUST | 版本追溯 | — | 软件版本 | /equipment/{id}/software | equipment_software_installations | Project | 自有 |
| CP-MT-006 | 钢丝绳 | MUST | 专用四表+更换留痕 | — | 钢丝绳台账 | /assets/{id}/wire-ropes | wire_rope_* | Project | Konecranes/微特 |

## 六、历史取证（History & Forensics）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-HS-001 | 历史趋势 | MUST | 多metric时间窗 | metric catalog | History Explorer | /assets/{id}/history | telemetry_samples | Project | Open MCT/Grafana |
| CP-HS-002 | 回放(Replay) | MUST | 真实遥测驱动 | 趋势 | Replay | /assets/{id}/history | telemetry_samples | Project | Open MCT |
| CP-HS-003 | 事件审查(Incident) | MUST | T-30→T+10 | 配置@T | Incident Review | /assets/{id}/configuration?at= | revisions/timeline | Project | 自有 |
| CP-HS-004 | Revision | MUST | 数据当时是什么 | — | 变更历史 | /assets/{id}/revisions | entity_revisions | Project | EAM |
| CP-HS-005 | Timeline | MUST | 统一事件索引 | 各事件 | Asset Timeline | /assets/{id}/timeline | asset_timeline_events | Project | 自有 |

## 七、合规治理（Compliance & Governance）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-CG-001 | 合规档案 | MUST | 登记/检验/日周月 | — | 合规中心 | /assets/{id}/compliance | 合规字段 | Project | TSG/74号令 |
| CP-CG-002 | 审计(Audit) | MUST | 谁做了什么 | — | 审计 | /audit | audit_logs | Tenant | 自有 |
| CP-CG-003 | 到期中心 | MUST | 统一待办 | 各到期 | 首页待办 | /attention | notifications(站内) | Project/Portfolio | Konecranes |

## 八、三底座（Cross-cutting Bases）

| ID | 能力 | R2 | 业务价值 | 依赖 | 主要UI | 主要API | 数据实体 | 权限域 | 参考 |
|----|------|-----|---------|------|--------|---------|---------|--------|------|
| CP-BS-001 | 全局搜索 | MUST | 跨域 Ctrl+K | — | Command Palette | /search | 跨实体 | Portfolio+Project | 自有 |
| CP-BS-002 | 文档(Documents) | MUST | 元数据+storage接口 | — | 资料文档 | /documents | documents/document_links | Project | Konecranes |
| CP-BS-003 | 导入(Import) | MUST | 五步闭环+import_jobs | — | 数据治理 | /imports | import_jobs | Portfolio/Project | 自有 |
| CP-BS-004 | 导出(Export) | MUST | Project scoped XLSX | — | 数据治理 | /exports | — | Project | 微特/安监 |
| CP-BS-005 | Identity/RBAC/Audit | MUST | 三底座之一 | — | 身份权限 | /auth /capability | users/roles/grants | Tenant | 现状 |
| CP-BS-006 | Data/Search/History | MUST | 三底座之一 | — | — | /search /history | 历史/搜索 | Project | 现状 |

## 九、LATER（设计预留，不深做）

预算 / 采购 / 完整库存 / 合同结算 / 自动 CPM 优化器 / 资源自动 leveling / AI 生命周期助手 / 邮件·短信·微信通知 —— 均在设计文档保留接口，R2 不实现。

## 十、A2_PLUS

真实设备 Enrollment / 设备证书生命周期 / 真实 Edge provisioning / 公网设备 MQTT / 真实远程控制 / PLC command —— 严格守边界，R2 不进入。
