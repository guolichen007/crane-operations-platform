# Phase1-A1.1-R2 项目交付与资源排期设计

> 解决"公司同时 8 个项目时，知不知道谁什么时候进场、谁抢同一个工程师/施工队/调试人员/测试设备、哪个项目延期会影响后面"。对标 Primavera Cloud（项目组合/资源/场景比较/冲突解决）、SAP S/4HANA 维护阶段推进、IBM Maximo PM（时间/运行量/混合排期 + PM hierarchy）。
> DESIGN_ONLY=YES，本轮只设计，不实现。

---

## 1. 产品定位：第七业务域

在六能力（资产运营/实时/孪生/运维/历史/合规）之上，新增一级域 **项目交付与资源排期**，回答：

| 域 | 平台回答的问题 |
|----|--------------|
| 项目交付 | 新项目什么时候做？谁做？有没有施工冲突？ |
| 资产运营 | 公司有多少行车、装在哪、什么配置？ |
| 实时运行 | 现在谁在线、怎么运行、哪里告警？ |
| 数字孪生 | 真实 Equipment 在哪、状态、相互关系？ |
| 运维可靠性 | 什么时候检查/保养/维修/更换？ |
| 历史取证 | 故障时什么配置、什么版本、怎样运动？ |
| 合规治理 | 登记/检验/档案/日周月是否完整？ |

三个技术底座：**Identity/RBAC/Audit** · **Data/Search/History** · **Documents/Import/Export**。

## 2. Project Intake（项目登记，不是直接建正式项目）

关键架构点：新项目登记**不是**立即创建 Runtime Project。因为现有权限模型只有 Project Scope，一个还不存在的项目自然没有 Project Scope。**不能**为登记给 platform_admin 加 global bypass。

流程：`Project Intake → Review → Planning → Approval → Formal Project → Execution → Asset Handover`。

**`project_intakes` 概念模型**：

| 字段 | 说明 |
|------|------|
| id / tenant_id | 内部 id |
| intake_code / project_name | 登记编号 / 名称 |
| customer | 客户 |
| site/location reference | 现场/位置 |
| project_type | NEW_INSTALLATION/AUTOMATION_RETROFIT/ALGORITHM_UPGRADE/DEVICE_REPLACEMENT/SERVICE_PROJECT |
| priority / risk_level | 优先级 / 风险等级 |
| scope_summary | 范围摘要 |
| **registered_at / registered_by** | 登记时间**系统自动生成**（registered_at 不可手工改）/ 登记人 |
| requested_start / requested_handover | 要求开始/交付 |
| planned_start / planned_end | 计划开始/结束 |
| status | REGISTERED/REVIEW/PLANNING/APPROVED/REJECTED/CANCELLED |
| project_manager / technical_lead / site_lead | 团队 |
| notes / version | 备注 / 乐观锁 |
| formal_project_id | 批准后关联正式项目 |

## 3. Portfolio Scope 授权（窄，非全局超级管理员）

现有 Project Scope 无法授权"创建还不存在的项目"。新增一层非常窄的 **Portfolio Scope**：

- capabilities：`project_intake_read / project_intake_write / project_schedule_read / project_schedule_write / project_approve / resource_calendar_read / resource_calendar_write / schedule_override`
- 角色：`portfolio_planner / portfolio_manager`

**授权矩阵（关键）**：

| 能力域 | portfolio_manager | platform_admin（有 ProjectGrant） | platform_admin（无 ProjectGrant） |
|--------|-------------------|-----------------------------------|-----------------------------------|
| 登记/排期/组合/资源日历 | ✅ | ✅（若被授 Portfolio） | ❌ |
| 项目组合数据 | ✅ | ✅ | ❌ |
| 某项目 PLC/LiDAR/告警/遥测 | ❌（无该 ProjectGrant 则 403） | ✅ | ❌ |

> **platform_admin != global admin**。Portfolio Manager 可登记/排期/看组合/查冲突，但若没有 P003 的 ProjectGrant，**不能**读 P003 的 PLC/LiDAR/告警/遥测。两个权限域必须分开。

## 4. Project Lifecycle（状态机，非自由 status 下拉）

正式状态：`REGISTERED / PLANNING / SURVEY / DESIGN / PREPARATION / READY_FOR_SITE / IN_EXECUTION / COMMISSIONING / TRIAL_RUN / ACCEPTANCE / HANDED_OVER / WARRANTY / CLOSED`；旁路 `ON_HOLD / CANCELLED`。

- 定义 **allowed transitions**（状态机，非任意改）；关键 transition 记录 `actor / time / reason / request_id`。
- 采购/FAT/运输不一定适用所有项目 → 作为 **Milestone/Activity**，不硬塞状态机。

## 5. Milestones / Activities

`project_milestones` / `project_activities`。典型模板：项目登记 / 现场勘察 / 设计冻结 / 设备选型 / 设备到货 / FAT / 进场准备 / 现场进场 / 硬件安装 / 环境部署 / 单机调试 / 系统联调 / 试运行 / 验收 / 整改项关闭 / 正式移交 / 质保结束。

项目类型模板：NEW_INSTALLATION / AUTOMATION_RETROFIT / ALGORITHM_UPGRADE / DEVICE_REPLACEMENT / SERVICE_PROJECT。登记后按类型自动生成里程碑（模板本轮设计，实现阶段按优先级）。

## 6. Unified Scheduling Engine（通用排期内核）

不止项目排期——安装/维护工单/定期检验/标定/PLC升级/停机窗口都在争抢时间/人员/现场/设备。设计**通用 Scheduling Kernel**：

核心对象：`schedule_items / resource_calendars / resource_reservations / schedule_dependencies / schedule_conflicts / schedule_conflict_overrides / schedule_baselines(或 revisions)`。

Schedule Item 来源：`PROJECT_ACTIVITY / WORK_ORDER / INSPECTION / CALIBRATION / COMMISSIONING / SITE_WINDOW`（后续扩展）。

## 7. Resource Types

`PERSON / TEAM / SITE / WORKSHOP / BAY / RUNWAY / ASSET / TEST_EQUIPMENT / TOOL / VEHICLE / COMMISSIONING_SLOT / NETWORK_CUTOVER_SLOT / OTHER`。

Resource 属性：`capacity / exclusive(独占) / calendar / timezone / status`。**不保存敏感 HR 数据**；用户资源尽量引用现有 identity user id。

## 8. Schedule Conflict Model

冲突类型：`RESOURCE_OVERLAP / RESOURCE_OVER_CAPACITY / SITE_OVERLAP / RUNWAY_OVERLAP / ASSET_DOWNTIME_OVERLAP / DEPENDENCY_VIOLATION / CALENDAR_UNAVAILABLE / SITE_ACCESS_WINDOW / COMMISSIONING_CONFLICT / NETWORK_CUTOVER_CONFLICT`。

严重度：`BLOCKING（默认阻止审批）/ WARNING（可继续但需理由）/ INFO（提示）`。

- BLOCKING 例：同一人同时间两现场、同一施工队同时两城市、同一独占 Runway 两项目、同一测试设备被两项目预约。
- WARNING 例：两高优项目紧挨仅半天转场、施工结束当天另一项目进场。
- INFO 例：利用率偏高、缓冲很短。

**Override**：必须 `reason + actor + time`，audit，不能静默忽略。第一版**确定性冲突检测**，禁止 R2 复杂自动优化器。

## 9. Portfolio UI（四种视图 + 详情）

1. **Portfolio Overview** KPI：全部项目 / 规划中 / 施工中 / 待验收 / 延期 / 资源冲突 / 本月进场。
2. **Project Register**（登记列表 + 状态/筛选）。
3. **Calendar / Gantt**（项目/活动区间、milestone、baseline、actual、dependency、conflict overlay）。
4. **Resource Heatmap**（周 × 资源 利用率，红=OVERALLOCATED）。
5. **Conflict Center**（只列 Blocking/Warning/Overdue/Dependency violations）。
6. **Project Detail** Tabs：Overview / Schedule / Resources / Risk·Issues / Change / Site / Assets / Documents / Commissioning / Acceptance / Handover / History。

**排期可视化技术**：不引入大型 Gantt 库；优先 ECharts 6 **Custom Series + Range Bar**（区间条/里程碑/基线/实际/依赖/冲突叠加）+ 资源热力图。借鉴 Primavera/SAP/Maximo 排程思想，不复制闭源。

## 10. Risk / Issue / Change

- `project_risks`：probability / impact / owner / mitigation / status（例：现场无法停机、设备供货延期、网络未就绪、图纸缺失、客户未确认）。
- `project_issues`：severity / owner / due_at / resolution（例：相机支架不匹配、PLC 点位不足）。
- `change_requests`：requested_by / reason / scope_impact / schedule_impact / approval_status / approved_by / approved_at（例：增加相机、换雷达型号、改控制范围、延期）。重大变更必须进 Project Timeline。

## 11. Commissioning / Acceptance / Handover

`commissioning_records / acceptance_records / punch_items / handover_checklists`。

Handover Checklist 至少：PLC I/O 确认、LiDAR 安装确认、Camera 安装确认、标定完成、设备版本确认、Edge 软件版本确认、联锁/安全验证、告警验证、资产档案完整、文档移交、培训记录。

未关闭 blocking punch item → 默认不能 HANDED_OVER；允许受控 override（reason + audit）。

## 12. Project → Asset Handover（正式连接）

Project planned equipment ≠ formal Asset Equipment。只有经过 Commissioning → Acceptance → Handover 后，**实际安装数据**才成为 **AS-BUILT asset configuration**。Handover 必须确认：manufacturer / model / serial / location / firmware-software / calibration / installation time / semantic slot。

## 13. Maintenance Planning 升级

参照 IBM Maximo PM：`TIME_BASED / METER_BASED / HYBRID`（如"每 30 天 或 500 工作循环，谁先到就提醒"）。PM hierarchy / asset hierarchy 作为长期方向。R2 至少完成单资产/Equipment 的 maintenance plan。

## 14. Supplier / Warranty / light Spare Part

- `business_partners`（供应商/服务商，轻量，非 ERP）。
- `warranties`：provider / start / end / coverage summary / document link（例：LiDAR 质保至 2028-10-01）。
- `spare_part_catalog` / `work_order_parts`（工单"更换 PLC 模块 → 用 SM1231 ×1"）。

**不做**：采购订单、发票、财务、完整仓储 ERP。

## 15. Attention Center（到期中心，统一）

类型：`PROJECT_SITE_ENTRY / PROJECT_DELAY / PROJECT_CONFLICT / PROJECT_ACCEPTANCE_DUE / INSPECTION_DUE / MAINTENANCE_DUE / CALIBRATION_DUE / WARRANTY_EXPIRING / DOCUMENT_EXPIRING / ALARM_OPEN`。首页"待办与到期"。R2 只站内，不接短信/微信/邮件。

## 16. Global Search 扩展

Ctrl+K 搜索：Project / Site / Asset / Equipment / Serial / Wire Rope / Work Order / Inspection / Alarm / Document / Risk / Issue / Change。返回带 type/status/location/project；严格按 **Portfolio scope** 与 **Project scope** 分别过滤。

## 17. 范围控制（防膨胀）

- **R2 MUST**：项目登记、排期、冲突、资产、Equipment、位置、钢丝绳、维护、点检、工单、标定、软件、供应商/质保、轻量备件、孪生、历史、回放、合规、导入导出、文档元数据、搜索、审计/Revision/Timeline、站内提醒。
- **R2 LATER（仅设计预留）**：预算、采购、完整库存、合同结算、自动 CPM 优化器、资源自动 leveling、AI 助手、邮件/短信/微信。
- **A2+**：真实设备 Enrollment、证书生命周期、真实 Edge provisioning、公网设备 MQTT、真实远程控制、PLC command。
