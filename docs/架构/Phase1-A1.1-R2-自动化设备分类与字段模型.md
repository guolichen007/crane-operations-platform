# Phase1-A1.1-R2 自动化设备分类与字段模型

> 定义 Equipment 的 taxonomy、通用安装字段、以及 LiDAR/Camera/PLC/VFD/Motor/Brake/Industrial PC/Switch/Sensor 的专项字段、标定、软件版本、备件。DESIGN_ONLY=YES。
> 关联：《行车数字资产图谱》《设备与运行数据边界》。

---

## 1. 设计决策：为什么用「typed common columns + validated specifications」而不为每个设备建 vendor 表

负责人要求"不要为所有字段单独创建 vendor table"，本方案采用：

- **`equipment_types`** 存 taxonomy（stable code + 中文名 + 所属大类 + 元数据），**不是数据库硬 ENUM**，未来加 RFID/毫米波雷达/AI 盒子/无线基站/安全激光扫描仪不用迁库。
- **`asset_equipment_installations`** 存**所有设备通用**字段（身份/安装/状态/快照）。
- **`validated specifications`（jsonb）** 存设备**专项技术参数**，由 `equipment_type` 声明的 schema 做校验（type 声明哪几个字段合法），既不散落成几十张 vendor 表，又不退化成自由文本。

理由：专项字段差异大（LiDAR 有点频/线数，Camera 有分辨率/FOV，PLC 有 DI/DO/AI/AO），若全平铺到一张表会几百列且大量空；若建 vendor 表则新增设备类型要迁库。typed common + validated jsonb 在"扩展性"与"可校验"间取平衡。

## 2. equipment_types taxonomy（大类 → 类型 code）

| 大类 | 类型 code（节选，可扩展） |
|------|--------------------------|
| MECHANICAL | HOIST, MOTOR, REDUCER, BRAKE, DRUM, HOOK, GRAB, WHEEL, RAIL, COUPLING |
| CONTROL | PLC, PLC_IO, SAFETY_PLC, RELAY, CONTACTOR, VFD, SERVO_DRIVE, REMOTE_CONTROLLER |
| PERCEPTION | LIDAR_3D, LIDAR_2D, CAMERA_RGB, CAMERA_DEPTH, CAMERA_STEREO, CAMERA_THERMAL, ENCODER, ROPE_ENCODER, LOAD_CELL, IMU, PROXIMITY_SENSOR, LIMIT_SWITCH, ANTI_COLLISION_SENSOR, WIND_SENSOR, TEMPERATURE_SENSOR, VIBRATION_SENSOR |
| COMPUTE | INDUSTRIAL_PC, EDGE_GATEWAY, GPU |
| NETWORK | INDUSTRIAL_SWITCH, POE_SWITCH, ROUTER, MEDIA_CONVERTER |
| POWER | POWER_SUPPLY, UPS, TRANSFORMER, CONDUCTOR_RAIL |
| SAFETY | OVERLOAD_LIMITER, EMERGENCY_STOP, UPPER_LIMIT, LOWER_LIMIT, ANTI_COLLISION, BUFFER |
| OTHER | OTHER |

> `equipment_types` 字段：`code(stable) / name / category / description / specification_schema(jsonb，声明该类型可用的专项字段) / status / version`。类型目录支持 TENANT / PROJECT scope；**R2 UI 只能写 PROJECT scoped catalog**，Tenant global 由受控工具维护（不制造 global admin）。

## 3. asset_equipment_installations（通用安装字段）

所有设备安装实例的最小字段集：

| 字段 | 说明 |
|------|------|
| id | 内部不可变 id（非人工编号） |
| asset_id | 所属行车（NOT NULL，scope 锚点） |
| equipment_type | 类型 code |
| catalog_item_id | 目录条目（可空） |
| manufacturer_snapshot / brand_snapshot / model_snapshot | 目录快照（目录改动不改历史） |
| serial_number / asset_tag | 序列号 / 资产标签 |
| position_label / semantic_slot | 位置标签 / 语义槽 |
| parent_installation_id / parent_semantic_slot | 父设备（层级） |
| installed_at / commissioned_at / removed_at | 安装/投运/移除 |
| administrative_state | IN_SERVICE/MAINTENANCE/OUT_OF_SERVICE/FAULT_REPORTED |
| criticality | 关键度 |
| warranty_until / supplier | 质保/供应商 |
| coordinate_frame / x / y / z / roll / pitch / yaw | 安装位姿 |
| pose_source | MANUAL/CAD/SURVEY/CALIBRATED |
| accuracy | 位姿精度 |
| specifications(jsonb) | 专项参数（按 type schema 校验） |
| notes / version | 备注 / 乐观锁 |

> **`ONLINE` 不写在这里**（Connectivity 只来自 Runtime/Telemetry）。lifecycle 由 removed_at + 状态表达（INSTALLED/REMOVED/RETIRED）。

## 4. 专项字段（specifications，按类型）

### 4.1 LiDAR（LIDAR_3D / LIDAR_2D）

`scan_type / channels / horizontal_fov_deg / vertical_fov_deg / minimum_range_m / maximum_range_m / scan_rate_hz / point_rate / wavelength / interface_type(Ethernet/CAN/…) / protocol / firmware_version / mount frame`

> 严格区分「INSTALLED（人工维护）」与「ONLINE（必须 Runtime/Telemetry）」。

### 4.2 Camera（CAMERA_RGB/DEPTH/STEREO/THERMAL）

`camera_type / resolution_width / resolution_height / fps / sensor_type / sensor_size / lens / focal_length / horizontal_fov / vertical_fov / interface(GigE/PoE/USB3/MIPI) / protocol(RTSP/ONVIF/Vendor SDK) / firmware / mount pose`

> 内参/外参**不进普通备注**，用 `equipment_calibrations`（CalibrationRecord）独立留历史。

### 4.3 PLC

`manufacturer / series / model / cpu_model / serial_number / firmware_version / rack / slot / di_count / do_count / ai_count / ao_count / protocol_capabilities(PROFINET/PROFIBUS/Modbus TCP/EtherNet/IP/EtherCAT/CAN/Serial)`

PLC Program 只存：`program_name / version / sha256 / deployed_at / document_ref`（见 §7 software）——**不存源码、不存密码**。Runtime（RUN/STOP/FAULT）必须 Telemetry sourced。

### 4.4 VFD / Motor / Brake（形成真实关系链）

- **VFD**：`rated_power_kw / input_voltage / rated_current / firmware`；关系 `VFD DRIVES MOTOR`。
- **Motor**：`rated_power / voltage / current / rated_speed / duty / insulation_class`。
- **Brake**：`type / rated_torque / service_state`。

关系链：`VFD-HOIST → DRIVES → MOTOR-HOIST → DRIVES → REDUCER-HOIST → DRUM → WIRE_ROPE_INSTALLATION`。真实运行（fault/temperature/current/service_life）走 Runtime/Condition，**不伪造 Business state**。

### 4.5 Industrial PC / Edge Gateway

`manufacturer / model / serial / cpu / gpu / ram / storage / os / os_version / edge_software_version`。行政资产数据可编辑；运行数据（uptime/CPU temperature/disk health/memory/process health）未来从 Telemetry。

### 4.6 Switch / Network

`model / serial / port_count / poe_capable / managed / firmware`。IP/MAC/VLAN/runtime address 归 DEPLOYMENT_OWNED；password 永不进入业务库。

### 4.7 Sensor 通用

`sensor_type / measuring_range / output / accuracy / interface / protocol`；具体（Load Cell 量程、Encoder 分辨率、IMU 轴数、Limit Switch 触点）落入 specifications。

## 5. equipment_calibrations（标定，一级能力）

自动行车大量使用 LiDAR/Camera/IMU/Load Cell/Encoder，标定历史必须留痕：

`id / equipment_installation_id / calibration_type / performed_at / performed_by / valid_from / valid_until / result / coordinate_frame / calibration_version / artifact_document_id / sha256 / notes`

类型：`CAMERA_INTRINSIC / CAMERA_EXTRINSIC / LIDAR_EXTRINSIC / LIDAR_ALIGNMENT / IMU_BIAS / IMU_ORIENTATION / LOAD_ZERO / LOAD_SPAN / OTHER`。

## 6. equipment_software_installations（软件/固件历史）

`id / equipment_installation_id / software_type / name / version / firmware_version / build_sha / configuration_sha / installed_at / installed_by / active`

类型：`PLC_PROGRAM / FIRMWARE / OS / DRIVER / EDGE_RUNTIME / ALGORITHM`。例：Industrial PC = Ubuntu 22.04.5 + ROS2 Humble + Crane Edge 1.2.4 + LiDAR Driver 2.3.0 + 算法 0.9.8；PLC = Program v2026.10 + SHA256。**不存 credential / source secret**。目的：故障时能回答"出事那天跑的是哪个版本"。

## 7. 备件闭环基础（非 ERP）

`spare_part_catalog`（name/model/规格/单位/参考价，可后置）、`equipment_spare_part_links`（设备 ↔ 备件）、`work_order_parts`（工单用件）。

目标：工单能明确"用了什么备件、替换了什么设备"。例：更换 HOIST_BRAKE → 用 "Brake Type XYZ ×1" → 形成 replacement event。采购/财务/供应商结算/库位等 ERP 与高级库存 **R2 后置**。
