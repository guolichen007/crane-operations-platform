# Phase1-A1.1-R2 项目组合、排期与资源冲突数据模型

## 1. 目标

解决多个项目并行时的登记、计划、施工窗口、人员/团队/现场/测试设备争用、延期、调试、验收和移交。R2 实现确定性排期和冲突检测，不实现完整 Primavera/CPM 优化器。

## 2. Project Intake

新需求先成为 `project_intakes`，批准后才创建正式 Project。

字段建议：

```text
id
tenant_id
intake_code
project_name
customer_name*
project_type
priority
risk_level
scope_summary
site_reference*
registered_at      -- system generated
registered_by
requested_start*
requested_handover*
planned_start*
planned_end*
status
project_manager_user_id*
technical_lead_user_id*
site_lead_user_id*
formal_project_id*
version
created_at/updated_at
```

Intake 状态：REGISTERED → REVIEW → PLANNING → APPROVED/REJECTED/CANCELLED。正式 Project 生命周期在批准后继续。

## 3. Formal Project 生命周期

`REGISTERED / PLANNING / SURVEY / DESIGN / PREPARATION / READY_FOR_SITE / IN_EXECUTION / COMMISSIONING / TRIAL_RUN / ACCEPTANCE / HANDED_OVER / WARRANTY / CLOSED`，旁路 `ON_HOLD / CANCELLED`。

状态变更必须通过 transition service，记录 actor/time/reason/request_id；禁止任意 PATCH status。

## 4. Milestone / Activity

### project_milestones

`id, project_id, template_code*, name, planned_at, actual_at*, status, blocking, sort_order, version`

### project_activities

`id, project_id, milestone_id*, name, planned_start/end, actual_start/end*, status, percent_complete*, owner, schedule_item_id*, version`

模板：NEW_INSTALLATION、AUTOMATION_RETROFIT、ALGORITHM_UPGRADE、DEVICE_REPLACEMENT、SERVICE_PROJECT。

## 5. Scheduling Kernel

统一处理 Project Activity、Work Order、Inspection、Calibration、Commissioning、Site Window。

### schedule_items

```text
id
project_id*
source_type
source_id
name
start_at
end_at
timezone
status
priority
exclusive_location_id*
baseline_id*
version
```

### resources

```text
id
portfolio_scope_id
resource_type
reference_id*
name
capacity
exclusive
home_timezone
status
```

类型：PERSON、TEAM、SITE、WORKSHOP、BAY、RUNWAY、ASSET、TEST_EQUIPMENT、TOOL、VEHICLE、COMMISSIONING_SLOT、NETWORK_CUTOVER_SLOT、OTHER。

不保存敏感 HR 资料；人员资源引用 identity user。

### resource_calendars

工作日/休息日/不可用窗口/节假日/现场开放时间。所有时间 DB 存 UTC，Calendar 带 IANA timezone；前端按资源/项目时区显示。

### resource_reservations

```text
id
schedule_item_id
resource_id
start_at
end_at
quantity
status
created_at
```

## 6. PostgreSQL 时间范围技术

建议使用 `tstzrange`/range query + GiST 索引辅助重叠查询。

### 独占且绝对禁止重叠的资源

例如某些独占测试设备/Runway，可评估 PostgreSQL exclusion constraint：同一 resource_id 的时间范围不可 `&&` 重叠。

### 可超配/可 override 的资源

人员容量、团队 capacity、Warning 冲突不能用硬 DB constraint 直接拦死，应由 Go conflict engine 判断。审批前必须在 transaction 内重新检查。

### 并发正确性

不能采用“前端先查没冲突 → 再写”模式。Schedule Approval 流程：

1. 开事务；
2. 对涉及资源排序后获取 advisory/row locks，避免死锁；
3. 重读 reservations/calendar/dependencies；
4. 重新计算 conflict；
5. 有 BLOCKING 且无合法 override → rollback；
6. 写 reservation/conflict/override/audit；
7. commit。

这样避免两个用户同时排期都在各自视图中“检查通过”。

## 7. Dependency

`schedule_dependencies(id, predecessor_item_id, successor_item_id, dependency_type, lag_minutes)`。

R2 支持 FS 为主，可预留 SS/FF/SF；不实现完整关键路径算法。Dependency violation 进入 Conflict Center。

## 8. Conflict

`schedule_conflicts`：

```text
id
conflict_type
severity
left_item_id
right_item_id*
resource_id*
detected_at
details_json
status: OPEN|RESOLVED|OVERRIDDEN
resolved_at*
```

类型：RESOURCE_OVERLAP、RESOURCE_OVER_CAPACITY、SITE_OVERLAP、RUNWAY_OVERLAP、ASSET_DOWNTIME_OVERLAP、DEPENDENCY_VIOLATION、CALENDAR_UNAVAILABLE、SITE_ACCESS_WINDOW、COMMISSIONING_CONFLICT、NETWORK_CUTOVER_CONFLICT。

### Override

`schedule_conflict_overrides(conflict_id, reason, overridden_by, overridden_at)`；只允许有 `schedule_override` capability 的用户，且必须 audit。

## 9. Baseline 与变更

计划一旦批准，需要 baseline：`schedule_baselines` 保存 baseline version/approved_by/approved_at，不覆盖旧 baseline。实际计划调整通过 revision/change request 留痕。

## 10. Risk / Issue / Change

### project_risks

probability、impact、owner、mitigation、review_due、status。

### project_issues

severity、owner、due_at、resolution、status。

### change_requests

requested_by、reason、scope_impact、schedule_impact、affected_items、approval_status、approved_by/at。重大 change 进入 Timeline。

## 11. Commissioning / Acceptance / Punch / Handover

- `commissioning_records`：测试项、结果、执行人、时间、证据文档；
- `acceptance_records`：验收结论、范围、签署信息元数据；
- `punch_items`：严重度、owner、due、closed_at；
- `handover_checklists`：正式移交项。

Blocking Punch 未关闭默认不能 Handover；override 需 reason+audit。

## 12. Planned → AS-BUILT

项目 planned equipment 与正式 Equipment 分开。Handover 时明确确认：

- 实际 manufacturer/model/SN；
- 实际 Location / semantic slot；
- 固件/软件版本；
- Calibration；
- installed/commissioned time；
- 文档；
- Runtime binding 状态（如果已有受控绑定）。

Handover service 在同一事务内创建/确认正式 AS-BUILT 配置，并写 Timeline/Audit。

## 13. Portfolio Scope

Project 尚未创建时无法使用 ProjectGrant，因此设置窄 PortfolioGrant：只允许 Intake/Portfolio/Schedule/Resource/Risk 等组合级数据。没有具体 ProjectGrant 时不能读取该项目 PLC/LiDAR/Alarm/Telemetry。

