# Phase1-A1.1-R2 Equipment 类型字段与铭牌字典

> 本文定义 R2 首批 Equipment 的“应该能存什么”。不是所有字段都必须变成 SQL 列；通用高频字段显式列，类型特有字段进入受版本化 JSON Schema 验证的 `specifications`。

## 1. 所有 Equipment 通用字段

### Identity / Nameplate

`equipment_code, type_code, manufacturer, brand, model, product_code, serial_number, batch_number, asset_tag`

### Installation

`asset_id, semantic_slot, position_label, installed_at, commissioned_at, removed_at, supplier_id, warranty_until`

### Mount Pose

`coordinate_frame, x/y/z, roll/pitch/yaw, pose_source, pose_accuracy`

### Business State

`administrative_state, criticality, notes`

### Runtime derived（只读）

`connectivity, last_seen, condition, condition_source, condition_observed_at`

## 2. LIDAR_3D / LIDAR_2D

### 铭牌/规格

- channels / beam count；
- scan_type（rotating/solid-state/hybrid）；
- horizontal_fov_deg；
- vertical_fov_deg；
- min/max_range_m；
- range_accuracy_cm（有厂商可靠资料时）；
- point_rate_hz；
- scan_rate_hz；
- wavelength_nm；
- interface_type；
- supported_protocols；
- time_sync_capability（PTP/NTP/GPS/unknown）；
- ingress_protection（可选）；
- operating_temperature_min/max（可选）；
- firmware_version（业务当前版本摘要，历史另表）。

### 安装/标定

- mount frame；
- extrinsic calibration record；
- alignment record；
- calibration artifact sha256；
- last calibration/valid until。

### Runtime metrics

`equipment.connectivity, lidar.packet_rate, lidar.point_rate, lidar.temperature, lidar.error_state, lidar.time_sync_state`（只有实际驱动有来源才启用）。

## 3. Camera

类型：RGB / DEPTH / STEREO / THERMAL / INDUSTRIAL。

### 规格

- resolution_width/height；
- max_fps；
- sensor_model/type；
- pixel_size_um（可选）；
- lens_model；
- focal_length_mm；
- horizontal/vertical_fov_deg；
- interface_type：GigE/PoE/USB3/MIPI/OTHER；
- protocol：RTSP/ONVIF/VENDOR_SDK；
- poe_required；
- firmware_version。

### Calibration

Intrinsic：fx/fy/cx/cy/distortion 作为 artifact/structured record，不塞普通 note；Extrinsic 与 mount frame 关联。

### Runtime

connectivity、frame_rate、dropped_frames、temperature、stream_state（有来源才启用）。不存 Camera password。

## 4. PLC / Safety PLC / IO

### PLC

manufacturer、series、model、cpu_model、serial、firmware_version、rack/slot、protocol_capabilities、di/do/ai/ao capacity、program_name/version/sha256 摘要。

PLC source program 不进数据库；密码不进数据库。

### Runtime

RUN/STOP/FAULT、scan time、diagnostic code、connection quality 只来自 runtime/telemetry。

### PLC_IO

module_type、rack、slot、channel_count、signal_class。具体客户真实 I/O 点表属于 OPERATIONS_SENSITIVE，公开 fixture 仅 synthetic。

## 5. VFD / Servo Drive

rated_power_kw、rated_voltage_v、rated_current_a、input/output frequency range、control_mode、supported_protocols、firmware_version。

Runtime：output_frequency/current/voltage/DC bus/temperature/fault code/ready/running。

## 6. Motor

rated_power_kw、rated_voltage_v、rated_current_a、rated_speed_rpm、frequency_hz、duty、insulation_class、protection_class、mounting_type、brake_integrated。

Runtime：current/temperature/vibration/start_count/running_hours。

## 7. Reducer

ratio、rated_torque、lubricant_spec、oil_capacity、mounting、manufacturer/model/SN。

Runtime 可选：temperature/vibration（如有传感来源）。

## 8. Brake

brake_type、rated_torque_nm、coil_voltage、lining_spec、air_gap_nominal、manual_release、manufacturer/model/SN。

Runtime：open/closed feedback、temperature、cycle_count、wear signal（有来源才启用）。

## 9. Drum / Hook / Grab / Wheel

Drum：diameter、groove_type、rope_capacity、material。

Hook：rated_capacity、hook_type、material_grade、inspection mark。

Grab：grab_type、volume、rated load、manufacturer/model。

Wheel：diameter、material、flange type、position。

## 10. Industrial PC / Edge Gateway

CPU、GPU、RAM、storage、NIC count、OS/name/version、hostname label（非 secret）、Edge Runtime version 摘要。

运行指标：uptime、CPU/memory、disk usage/health、temperature、process health。

不要保存 SSH password/private key。

## 11. Industrial / PoE Switch

port_count、speed_profile、managed、poe_capable、poe_budget_w、uplink_type、firmware_version。

部署字段（IP/MAC/VLAN）属于 OPERATIONS_SENSITIVE；credentials NEVER STORE。

Runtime（未来）：port up/down、PoE power、device temp、link errors。

## 12. Encoder / Rope Encoder

measurement_type、resolution、range、interface、output_type、mounting、direction convention。

Runtime：position/velocity/quality。

## 13. Load Cell / Weighing Sensor

rated_capacity、sensitivity、accuracy_class、signal_type、excitation、amplifier/model。

Calibration：ZERO/SPAN；Runtime：load/raw/quality/overload flag。

## 14. IMU

axes、gyro_range、accel_range、output_rate、interface、time_sync。

Calibration：BIAS/ORIENTATION；Runtime：orientation/angular velocity/acceleration/quality。

## 15. Limit / Proximity / Anti-collision

类型、sensing principle、range、output type、normally open/closed、safety category（有可靠资料时）、mount position。

Runtime：triggered/healthy；不能人工伪造。

## 16. Power Supply / UPS

input/output voltage/current/power、battery type/capacity（UPS）、runtime estimate capability。

Runtime：input/output state、battery %, alarm。

## 17. Safety Criticality

`criticality=SAFETY_CRITICAL` 不等于系统自行认证安全等级，只用于平台管理优先级。任何 SIL/PL/安全等级字段只有来自可靠技术文件时记录，并关联 source document。

