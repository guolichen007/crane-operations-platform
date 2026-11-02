# Phase1-A1.1-R2 可靠性、钢丝绳、点检与维保数据模型

## 1. 钢丝绳必须是一等实体

钢丝绳不是资产的一列文本字段。必须记录产品规格、某根实际绳的安装、检查和替换历史。

### wire_rope_specifications

manufacturer、brand、model、product_code、construction、nominal_diameter_mm、nominal_length_m、grade、lay_direction、lay_type、core_type、coating、minimum_breaking_force_kn、standard_reference、status、version。

### wire_rope_installations

asset_id、equipment/drum relation、specification_id、serial_number、batch_number、supplier_id、position_label、installed_at、commissioned_at、removed_at、runtime_at_install、cycles_at_install、status、version。

### wire_rope_inspections

inspection_type（DAILY/VISUAL/PERIODIC/MRT/OTHER）、inspected_at、inspector、measured_diameter_mm、broken_wire_observation、corrosion、deformation、lubrication、rope_seating、finding、conclusion、next_due_at、standard_reference、documents。

### wire_rope_replacement_events

old_installation_id、new_installation_id、work_order_id、replaced_at、reason。

**禁止**：简单 `broken_wire_count > X` 自动宣告报废；没有经过标准/现场/安全责任审查的寿命算法不得上线。

## 2. Inspection 模型

inspection 的 `asset_id` 始终 NOT NULL；可以额外指向 equipment installation 或 wire rope installation。数据库/服务层必须校验子对象属于同一 asset。

Inspection Template 建议支持：

- PASS/FAIL/NA；
- Number + unit；
- Text；
- Selection；
- Photo/Document reference；
- required/optional；
- threshold 仅作提示，不能自动替代法定结论。

finalized inspection 默认不可物理修改；需要 correction 时创建 correction revision/audit。

## 3. Maintenance Plan

借鉴 Maximo：

- `TIME_BASED`；
- `METER_BASED`；
- `HYBRID`（哪个先到）；
- fixed schedule（基于目标日期）；
- floating schedule（基于上次实际完成）。

建议字段：

```text
id
asset_id
equipment_installation_id*
name
plan_type
schedule_mode: FIXED|FLOATING
time_interval/value/unit*
meter_code*
meter_interval*
lead_time*
next_due_at*
next_due_meter*
job_template_id*
status
version
```

R2 不实现复杂 PM hierarchy 自动生成器，但 schema 可保留 parent_plan_id；优先完成单 Asset/Equipment。

## 4. Meter

可以使用 metric catalog 的累计量作为 meter source：`crane.runtime`、`crane.work_cycles`、`motor.starts` 等。Meter reading 必须来自 Telemetry/可信输入；业务用户不能随意把运行小时改小来推迟维护。

## 5. Work Order

字段：

```text
id/project_id/asset_id
location_id*
equipment_installation_id*
wire_rope_installation_id*
source_type/source_id*
type
priority
status
title/description
created_by
assigned_to*
created_at
due_at*
started_at*
completed_at*
result*
notes*
version
```

状态：DRAFT → OPEN → IN_PROGRESS → WAITING → COMPLETED，旁路 CANCELLED。状态机后端校验。

工单完成时可产生：inspection、replacement、software install、calibration、parts consumption、timeline events。

## 6. Alarm → Work Order

成熟告警工作流建议：

- Alarm 可 ACK；
- 添加 comment/assignee；
- “创建工单”保留 alarm reference；
- 关闭工单不自动关闭设备 alarm，除非运行系统确认恢复；
- Alarm Incident Review 可跳到关联 Equipment。

## 7. Supplier / Warranty / Spare Part

### business_partners

R2 仅轻量：供应商/服务商名称、联系人业务元数据、状态、备注。敏感账号/支付信息不在平台。

### warranties

Asset/Equipment scope、provider、start/end、coverage_summary、document_link、status。

### spare_part_catalog

part_code、manufacturer、brand、model、description、applicable_equipment_types、status。

### work_order_parts

work_order_id、part_id、quantity、note。R2 不做库存扣减和财务。

## 8. Attention Center

统一生成：INSPECTION_DUE、MAINTENANCE_DUE、CALIBRATION_DUE、WARRANTY_EXPIRING、DOCUMENT_EXPIRING、ALARM_OPEN、PROJECT_CONFLICT 等。

Attention 是计算/索引视图，不成为 canonical 业务数据。用户可以 ACK/resolve 仅当对应业务允许；不能通过“关闭提醒”绕过真实过期状态。

