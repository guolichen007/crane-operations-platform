# Phase1-A1.1-R2 设备与运行数据边界

> 本文档定义：什么能业务编辑、什么只能运行时产生、什么绝对不进入业务库。这是「成熟平台不误导用户」与「不破坏既有安全体系」的根基。
> DESIGN_ONLY=YES。关联：《行车数字资产图谱》《自动化设备分类与字段模型》。

---

## 1. 字段所有权三分（设备视角）

| 域 | 设备相关字段 | 谁可写 |
|----|------------|--------|
| **DEPLOYMENT_OWNED** | tenant_id、asset immutable id、equipment 不可变 id、runtimeDeviceId、sourceMode(REAL/SIMULATION/TEST)、asset-runtime provenance、MQTT identity、device certificate identity、MAC/接口/网段/管理地址（网络部署数据） | 部署/bootstrap/导入 |
| **BUSINESS_OWNED** | manufacturer/brand/model/serial、asset_tag、position_label、semantic_slot、安装/投运/移除日期、administrative_state、criticality、warranty_until、supplier、notes、固件/软件版本（业务记录）、标定记录、合规元数据 | 有权限业务用户 |
| **TELEMETRY_OWNED** | connectivity、heartbeat、operating_mode、running_hours、work_cycles、load、motor_starts、emergency_stop、brake_cycles、travel_distance、fault、condition、shadow、live state | 设备/Edge（**禁止 UI 伪造**） |

**绝对不进业务数据库**：password、SNMP secret、PLC password、camera credential、OIDC secret、token、session cookie、private key、真实设备 enrollment 凭据。

## 2. 四维状态（正式）

一个设备同时有四维状态，各自独立更新，绝不用一个 `status` 包打天下。

| 维度 | 枚举 | 维护方 | 语义 |
|------|------|--------|------|
| **Lifecycle** | INSTALLED / REMOVED / RETIRED | 业务动作（安装/退役） | 该设备在资产上的存在性 |
| **Administrative State** | IN_SERVICE / MAINTENANCE / OUT_OF_SERVICE / FAULT_REPORTED | 业务人工 | 行政/管理状态 |
| **Connectivity** | ONLINE / OFFLINE / UNKNOWN / UNBOUND | 仅 Runtime/Telemetry | 是否真正连着并产生遥测 |
| **Condition** | NORMAL / WARNING / ALARM / UNKNOWN | 仅带 `source` + `observed_at` 的判定 | 健康结论 |

**铁律**：
- `ONLINE` 不得由业务用户手工填写；无 runtime binding → `UNBOUND`，UI 显示「实时状态未接入」。
- Condition 无可信来源 → `UNKNOWN`；只有 raw inspection 时以信息方式展示，不模拟安全结论。
- 例：PLC「在役 · ONLINE · WARNING(来源:VFD告警 2026-11-02T10:00Z)」；未接入雷达「在役 · 未接入 · 未知」。

## 3. equipment_runtime_bindings（运行绑定）

这是「业务设备 ↔ 运行身份」的桥梁，**DEPLOYMENT_OWNED，业务 UI 只读**。

概念字段（R2 只建模，不创建真实 Edge identity、不进入 A2）：

| 字段 | 说明 |
|------|------|
| id | 绑定 id |
| equipment_installation_id | 业务设备 |
| asset_id | 冗余便于 scope 解析 |
| source_kind | runtime 来源类型（SIMULATION/REAL/TEST） |
| source_ref | 运行身份引用（runtimeDeviceId / EDGE-*） |
| property_alias / telemetry_namespace | 遥测命名空间（如 /perception/lidar/front、/drive/hoist） |
| binding_status | BOUND / UNBOUND / PENDING |
| created_at | 绑定时间 |

映射语义：`EQ-LIDAR-001 → EDGE-001 + topic /perception/lidar/front + driver=innovusion_xxx`。业务页面可编辑品牌/型号/序列号/安装/位置/固件/备注，但**不可编辑** MQTT credential、device certificate、REAL identity、runtime provenance、安全认证。

## 4. 运行数据模型（Telemetry Metrics）

对标 Konecranes TRUCONNECT Remote Monitoring（关键组件状态/预计寿命、running time、lifted loads、motor starts、work cycles、emergency stops、brake monitoring、inverter monitoring）与 OPC UA for Machinery（Operation Counter、Lifetime Counter、Machinery State）。

R2 把 **schema 与 UI 承载能力**做出来，无真实 Edge 数据 = `UNKNOWN / UNAVAILABLE`，**不造数据**：

```
operating_mode            （运行模式，Telemetry）
running_hours             （总运行时间）
hoist_running_hours       （起升运行时间）
work_cycles               （工作循环次数）
lift_count                （起吊次数）
current_load              （当前载荷）
maximum_load              （历史最大载荷）
overload_count            （过载次数）
motor_starts              （电机启动次数）
emergency_stop_count      （急停次数）
brake_cycles              （制动循环）
travel_distance_x         （大车行程）
travel_distance_y         （小车行程）
hoist_distance_z          （起升行程）
fault_count               （故障计数）
availability              （可用率，Metric 派生）
```

- `availability` 等派生值标为 Metric（由原始 Measurement 计算），来源含计算窗口与算法版本。
- 现有 `ReportedState`（X/Y/Z/Grab + mode + alarmState + load + runtimeTotal）继续保留，作为行车本体最小遥测；新 metrics 作为扩展，逐步叠加，不改现有运行时契约。

## 5. 现有数据边界不破坏（重申）

- 运行来源不可变（SIMULATION 不能直接改 REAL，需新建 EDGE 身份）。
- 身份/档案分离、Telemetry 不可 UI 伪造是既有安全边界。
- Inventory Import = deployment/bootstrap source；对已 BUSINESS_OWNED 且被 UI 编辑的字段不覆盖（反证测试）。
- 不接真实 PLC、不实现真实远控、不开公网 Edge 管理端口；A2 前无远程控制/设备 Enrollment/公网 MQTT 管理。
