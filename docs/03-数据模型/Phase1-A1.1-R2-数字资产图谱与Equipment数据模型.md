# Phase1-A1.1-R2 数字资产图谱与 Equipment 数据模型

## 1. 目标

建立能够支撑未来多年扩展的 Crane Digital Asset Graph。核心不是给 `asset_devices` 无限加列，而是把“业务资产、可维护设备、运行连接、遥测”解耦。

```text
Location Hierarchy
    │
    ▼
Asset (Crane)
    │
    ├── AssetInstallation
    │
    ▼
EquipmentInstallation
    ├── Relation ── EquipmentInstallation
    ├── Calibration
    ├── Software/Firmware
    ├── Inspection / WorkOrder / Documents
    └── RuntimeBinding → Shadow/Telemetry/Alarm
```

## 2. ID 与业务编号

所有实体使用 immutable internal ID；用户看到的编号单独保存：

- `asset_id`：系统不可变内部 ID；
- `asset_code`：例如 `QD-001`，项目内唯一，可按规则编辑但修改必须 Revision；
- `equipment_installation_id`：安装实例 ID；
- `equipment_code`：例如 `LIDAR-FRONT-01`；
- 法定 `special_equipment_code` 另列，平台不得自行生成法定编号。

## 3. Asset 主数据

建议在既有资产锚点上 additive 扩展，而非重建现有 ID。

关键字段：

| 字段 | 说明 | Ownership |
|---|---|---|
| id | immutable | DEPLOYMENT |
| project_id | immutable binding | DEPLOYMENT |
| asset_code | 人工业务编号 | BUSINESS |
| display_name | 名称 | BUSINESS |
| asset_type | CRANE 等 | BUSINESS |
| asset_status | DRAFT/ACTIVE/OUT_OF_SERVICE/RETIRED | BUSINESS |
| manufacturer_id/model_catalog_id | 目录引用 | BUSINESS |
| manufacturer_snapshot/brand_snapshot/model_snapshot | 建档时快照 | BUSINESS |
| serial_number | 出厂序列号 | BUSINESS |
| manufacture_date | 制造日期 | BUSINESS |
| installation_date | 初次安装日期（摘要） | BUSINESS |
| commissioning_date | 投运日期 | BUSINESS |
| rated_capacity_t | 额定起重量 | BUSINESS |
| span_m | 跨度 | BUSINESS |
| lifting_height_m | 起升高度 | BUSINESS |
| work_class | 工作级别 | BUSINESS |
| application_type | 抓斗/吊钩/冶金等业务分类 | BUSINESS |
| control_mode | 手动/半自动/自动等业务描述 | BUSINESS |
| special_equipment_code | 法定字段载体，需正式文本核验 | BUSINESS |
| use_registration_no | 登记字段载体 | BUSINESS |
| latest_inspection_date / inspection_valid_until | 检验摘要 | BUSINESS |
| version | 乐观并发 | SYSTEM |
| created_at/updated_at | 系统时间 | SYSTEM |

`asset_status` 与已有 lifecycle 不混用；旧 lifecycle 在 R2 继续兼容。

## 4. Manufacturer / Model Catalog

### manufacturers

`id, project_scope_id*, name, short_name, country_region*, website*, status, version, created_at, updated_at`

R2 UI 只允许 PROJECT scoped catalog；TENANT/global catalog 仅受控工具维护。

### asset_model_catalog / equipment_catalog_items

目录只是“型号模板”，正式 Asset/Equipment 必须保存关键 snapshot，避免目录后续改名导致历史设备资料变化。

禁止已被引用的目录 hard delete；只 `DISABLED`。

## 5. Location 层级

`asset_locations` 支持：`SITE / PLANT / WORKSHOP / BAY / RUNWAY / OTHER`。

字段：

```text
id
project_id
parent_id
kind
code
name
address / province / city / county
longitude / latitude
coordinate_system: WGS84 | GCJ02 | LOCAL_SITE | UNKNOWN
coordinate_source: MANUAL | SURVEY | IMPORTED | SITE_CENTER
accuracy_m
local_x_m / local_y_m / local_z_m
orientation_deg
status
version
```

### 位置语义

- Asset 精确坐标：允许 Asset marker；
- 只有 Site 坐标：显示 Site marker，不伪装成设备位置；
- 只有行政区：只做区域聚合/品牌沙盘；
- 无位置：进入“位置未配置”。

### asset_installations

资产移动不能直接覆盖 location：

`id, asset_id, location_id, valid_from, valid_to, local_x_m, local_y_m, local_z_m, orientation_deg, source, notes`

同一时刻只允许一个 active installation。历史查询按时间区间解析。

## 6. Equipment 类型模型

Equipment 是可维护、可更换、可配置的设备实例，而不只是机械 component。

稳定类别至少：

### MECHANICAL

MOTOR、REDUCER、BRAKE、DRUM、HOOK、GRAB、WHEEL、COUPLING。

### CONTROL

PLC、PLC_IO、SAFETY_PLC、VFD、SERVO_DRIVE、RELAY、CONTACTOR、REMOTE_CONTROLLER。

### PERCEPTION

LIDAR_3D、LIDAR_2D、CAMERA_RGB、CAMERA_DEPTH、CAMERA_STEREO、CAMERA_THERMAL、ENCODER、ROPE_ENCODER、LOAD_CELL、IMU、LIMIT_SWITCH、PROXIMITY_SENSOR、ANTI_COLLISION_SENSOR、WIND_SENSOR、TEMPERATURE_SENSOR、VIBRATION_SENSOR。

### COMPUTE / NETWORK / POWER

INDUSTRIAL_PC、EDGE_GATEWAY、GPU、INDUSTRIAL_SWITCH、POE_SWITCH、ROUTER、MEDIA_CONVERTER、POWER_SUPPLY、UPS、TRANSFORMER、CONDUCTOR_RAIL。

类别不要使用 PostgreSQL hard enum 锁死未来；使用 stable `type_code` + code-owned registry。

## 7. Equipment specification schema

禁止 uncontrolled JSONB。建议：

```text
equipment_types
  type_code
  display_name
  domain
  spec_schema_version
  status
```

真实 schema 由代码/契约仓库维护并版本化，DB 只记录版本；`equipment_catalog_items.specifications JSONB` 在 Go 写入前按相应 schema 验证。

好处：
- 允许不同类型专项字段；
- 不做 EAV；
- 不允许任意垃圾 key；
- schema 升级可审查；
- 前端可根据 schema 生成辅助表单，但关键字段仍显式 UI。

## 8. Equipment Catalog

`equipment_catalog_items`：

```text
id
project_id / scope_kind
name
type_code
manufacturer_id
brand
model
product_code
spec_schema_version
specifications
status
version
```

目录不保存现场序列号、安装位姿和运行状态。

## 9. Asset Equipment Installation

正式实例表建议：

```text
id
asset_id
equipment_code
type_code
catalog_item_id*
manufacturer_snapshot
brand_snapshot
model_snapshot
serial_number
batch_number*
asset_tag*
parent_semantic_slot*
semantic_slot*
position_label*
criticality: LOW|MEDIUM|HIGH|SAFETY_CRITICAL
administrative_state: IN_SERVICE|MAINTENANCE|OUT_OF_SERVICE|FAULT_REPORTED
valid_from
valid_to
installed_at
commissioned_at*
removed_at*
warranty_until*
supplier_id*
coordinate_frame*
x_m/y_m/z_m*
roll_deg/pitch_deg/yaw_deg*
pose_source: MANUAL|CAD|SURVEY|CALIBRATED
pose_accuracy_m*
notes*
version
```

### 四维状态

1. Lifecycle：由 valid interval/removed 表达 INSTALLED/REMOVED/RETIRED；
2. Administrative：业务可写；
3. Connectivity：Runtime/Telemetry 只读；
4. Condition：来源必须可追溯，包含 `source + observed_at`。

## 10. Equipment Relations

`equipment_relations`：

```text
id
asset_id
from_equipment_installation_id
to_equipment_installation_id
relation_type
valid_from
valid_to
source
notes
version
```

关系：`CONTAINS / MOUNTED_ON / DRIVES / CONTROLS / SENSES / MONITORS / POWERED_BY / NETWORKED_TO / PROTECTS / BACKS_UP`。

必须验证双方属于同一个 Asset（R2 不开放跨资产关系）。历史配置 resolver 按 `valid_from <= at < valid_to` 解析。

## 11. Runtime Binding

`equipment_runtime_bindings` 是 DEPLOYMENT_OWNED：

```text
id
asset_id
equipment_installation_id
source_kind
source_ref
telemetry_namespace*
property_aliases*
binding_status
valid_from
valid_to
created_by_deployment
created_at
```

业务 UI 只读。没有 binding 时：`Connectivity=UNBOUND`，不能显示 ONLINE。

## 12. Equipment 专项字段建议

### LiDAR

channels、scan_type、horizontal_fov_deg、vertical_fov_deg、minimum_range_m、maximum_range_m、scan_rate_hz、point_rate_hz、wavelength_nm、interface_type、protocol、firmware_version。

### Camera

camera_type、resolution_width/height、fps、sensor_type、lens、focal_length_mm、horizontal_fov_deg、vertical_fov_deg、interface_type（GigE/PoE/USB3/…）、protocol（RTSP/ONVIF/vendor SDK）、firmware_version。

### PLC

series、cpu_model、firmware_version、rack、slot、di_count、do_count、ai_count、ao_count、protocol_capabilities。PLC RUN/STOP/FAULT 不允许业务编辑。

### VFD

rated_power_kw、input_voltage_v、rated_current_a、firmware_version、supported_protocols。

### Motor

rated_power_kw、rated_voltage_v、rated_current_a、rated_speed_rpm、duty、insulation_class。

### Brake

brake_type、rated_torque_nm、manufacturer service metadata；实时开合/温度属于 telemetry。

### Industrial PC

cpu_model、gpu_model、ram_gb、storage_gb、os_name、os_version；uptime/温度/磁盘健康属于 telemetry。

### Network Equipment

port_count、poe_capable、managed、firmware_version。IP/MAC/VLAN 属 OPERATIONS_SENSITIVE/DEPLOYMENT，不进入公开 fixture；credential 永不进业务 DB。

## 13. Calibration

`equipment_calibrations`：

```text
id
equipment_installation_id
calibration_type
performed_at
performed_by
valid_from
valid_until*
result
coordinate_frame*
calibration_version*
artifact_document_id*
artifact_sha256*
notes*
created_at
```

类型：CAMERA_INTRINSIC、CAMERA_EXTRINSIC、LIDAR_EXTRINSIC、LIDAR_ALIGNMENT、IMU_BIAS、IMU_ORIENTATION、LOAD_ZERO、LOAD_SPAN、OTHER。

## 14. Software / Firmware History

`equipment_software_installations`：

```text
id
equipment_installation_id
software_type: PLC_PROGRAM|FIRMWARE|OS|DRIVER|EDGE_RUNTIME|ALGORITHM
name
version
build_sha*
configuration_sha*
valid_from
valid_to
installed_at
installed_by
notes*
```

不得保存源码密钥、PLC 密码、Camera credential。

## 15. Supplier / Warranty / Spare

- `business_partners`：供应商/服务商轻量档案；
- `warranties`：覆盖 Asset/Equipment，start/end/coverage/document link；
- `spare_part_catalog`：轻量备件型号；
- `equipment_spare_part_links`：某 Equipment 推荐备件；
- `work_order_parts`：某工单实际用了什么。

R2 不做采购、发票、财务和完整库存。

## 16. 删除策略

核心历史对象默认 `RESTRICT/NO ACTION`：

- Asset → RETIRED；
- Location/Catalog → DISABLED；
- Equipment Installation → valid_to/REMOVED；
- Work Order → COMPLETED/CANCELLED；
- final Inspection 不物理删除，只允许受控 correction；
- 仅从未使用的 DRAFT 且无任何引用可 hard delete。

## 17. Inventory ownership

首次创建时 deployment inventory 可 seed BUSINESS 字段；记录已存在后 inventory 只允许更新 DEPLOYMENT 字段。禁止 `ui_edited` 粗粒度标记。

反证：OLD import → UI 改 NEW → 重跑 OLD import → BUSINESS 仍 NEW，runtime/provenance immutable。

