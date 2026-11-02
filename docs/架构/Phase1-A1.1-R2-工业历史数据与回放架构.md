# Phase1-A1.1-R2 工业历史数据与回放架构

> 补齐当前设计最大缺口：回答「2026年5月装的是什么雷达？故障前软件版本？钢丝绳何时装？PLC 程序何时升级？近一年何时离线？故障前 30 分钟 X/Y/Z/载荷发生了什么？」。DESIGN_ONLY=YES。

---

## 1. Industrial History Model（六层）

| 层 | 数据 | 载体 |
|----|------|------|
| **Current State** | 现在是什么 | 业务表（normalized）+ `device_shadow`（runtime） |
| **Business Revision** | 主数据以前是什么 | `entity_revisions`（新增） |
| **Lifecycle History** | 安装/拆除/替换/标定/软件升级 | 专用表（installation/calibration/software/replacement 已是 authoritative） |
| **Event History** | 告警、工单、检查、操作 | `alarms`/`work_orders`/`inspections`/`audit_logs` |
| **Telemetry History** | X/Y/Z、载荷、温度、状态 | `telemetry_samples` |
| **Aggregated History** | 1m/15m/1h 摘要 | 聚合视图（R2 可后置，先留 policy model） |

> Current 查询不用 History 表替代；历史不复制 Current。

## 2. Business Revision（entity_revisions）

只存 BUSINESS_OWNED 的历史版本，**不存** password/token/private key/MQTT secret。

| 字段 | 说明 |
|------|------|
| id | 内部 id |
| project_id / asset_id | scope 锚点 |
| entity_type / entity_id | 哪个实体（asset/equipment/…） |
| revision | 版本号（单调） |
| recorded_at / changed_by | 系统录入时间 / 操作者 |
| effective_at | 业务有效时间（区别于录入时间；例："10-20 已换"于"11-05 录入" → effective_at=10-20, recorded_at=11-05） |
| reason | 变更原因 |
| request_id | 幂等/审计关联 |
| snapshot_json | 变更后快照（BUSINESS_OWNED 字段） |
| snapshot_sha256 | 快照哈希（防篡改/去重） |

UI：变更历史 Tab 支持「查看 Revision N」「compare N / N-1」。

## 3. Lifecycle History（专用表是 authoritative，不靠 generic audit）

安装/拆除/替换/标定/软件升级以专用表为权威：
`asset_installations / asset_equipment_installations / wire_rope_installations / replacement_events / equipment_calibrations / equipment_software_installations / inspections / work_orders`。这些表已经表达"什么时候装的、什么时候拆的、从哪个换成哪个"。

## 4. Unified Timeline（asset_timeline_events）

仅作**统一索引**，**不复制 canonical payload**。

| 字段 | 说明 |
|------|------|
| id / project_id / asset_id | 锚点 |
| category | ALARM / WORK_ORDER / INSPECTION / CALIBRATION / SOFTWARE / REPLACEMENT / … |
| occurred_at | 发生时间 |
| source | 来源系统 |
| reference_type / reference_id | 指向 canonical 表 |
| summary | 一行摘要 |
| severity | 等级 |

canonical 仍在各自表；mutation 与 timeline 写入**同事务**。

## 5. Telemetry Metric Catalog（telemetry_metric_definitions）

前端**禁止**直接知道 `payload_json#>>'{reported,load}'`；永远查询 `metric=crane.load`。

| 字段 | 说明 |
|------|------|
| metric_code | crane.position.x / equipment.vfd.current / crane.load / crane.runtime |
| display_name | 大车位置 / 主起升电流 / 载荷 / 运行时间 |
| value_type | number/boolean/string |
| unit | m/t/A/h/次 |
| source_path | reported.position.x（后端映射，前端无感） |
| aggregation | avg/min/max/sum/last |
| category | CRANE / EQUIPMENT / SAFETY |
| status | ACTIVE/DISABLED |

**Metrics 至少**：crane.position.x/y/z、crane.grab、crane.load、crane.runtime、crane.work_cycles、crane.motor_starts、crane.emergency_stops、equipment.temperature、equipment.current、equipment.voltage、equipment.health、equipment.connectivity。无来源 = `UNAVAILABLE`。

## 6. History API（Project Scope First）

```
GET /assets/{id}/history?metric=&from=&to=&resolution=
GET /assets/{id}/timeline
GET /assets/{id}/revisions
GET /equipment/{id}/history?metric=&from=&to=&resolution=
GET /equipment/{id}/timeline
```

- `resolution`：`raw / 1m / 15m / 1h / auto`。
- **查询保护**：max query duration、max points、服务端聚合/降采样，防一次查询拖死 PG。

## 7. History Explorer（前端）

- **LIVE | HISTORY** 模式 + 时间范围：1H / 6H / 24H / 7D / 30D / CUSTOM。
- 多 metric 趋势图（ECharts core）。
- 趋势图叠加事件：`08:13:22 Alarm / 08:14:08 Emergency Stop / 08:16:41 Operator Login / 08:18:22 Work Order`。
- 学习 Open MCT Time Conductor 与 Grafana 时间窗交互，不复制其代码。

## 8. 3D Historical Replay

- 用真实历史 telemetry（X/Y/Z/grab/load/state/alarm）驱动 Twin。
- 明显显示 `HISTORY REPLAY` + 精确时间戳。
- Replay 时 **LIVE subscription 不得覆盖画面**；**绝不接远程控制**；Replay = read-only historical visualization。
- UX：Play / Pause / speed(0.25x/0.5x/1x/2x/4x) / timeline scrubber / event markers / jump alarm / jump inspection；选事件 → camera focus 相关 semantic equipment。

## 9. Incident Review Mode（数据审查/取证）

用户选 Alarm A → 自动打开 `T-30min → T+10min`，展示：position/load/state/equipment health + timeline(alarm/event) + software version + equipment configuration。回答「事故发生时：什么配置、什么版本、怎么运动、什么状态、发生了什么」。

## 10. Audit vs Revision vs Timeline vs Telemetry（四者不混表）

| 概念 | 回答 | 表 |
|------|------|-----|
| Audit | 谁执行了什么 | audit_logs |
| Revision | 数据当时是什么 | entity_revisions |
| Timeline | 业务上发生了什么 | asset_timeline_events（索引） |
| Telemetry | 设备当时报告什么 | telemetry_samples |

## 11. Data Completeness

Asset 360 展示档案/位置/Equipment/合规/标定/文档的完整度：COMPLETE / PARTIAL / MISSING；规则明确后才显示 percentage，不虚构百分比。

---

## 12. 遥测时间质量（P1-3，冻结）

**不重做 Edge wire contract**（现有 envelope 已有 `sentAt / bootId / bootSeq / sequence`）。Cloud 持久化增强，区分 **Event Time（设备产生时刻）** 与 **Ingest Time（云端收到时刻）**：

| 字段 | 说明 |
|------|------|
| `source_sent_at` | 设备上报的 sentAt（Event Time） |
| `cloud_received_at` | 云端入库时间（Ingest Time） |
| `boot_id` / `boot_seq` / `sequence` | 现网 envelope 字段保留，用于顺序/重启/重复判定 |
| `quality_flags` | 时间质量标志（clock_anomaly / late_arrival / unknown / …） |
| `late_arrival` | 迟到标记（source_sent_at 明显早于 received_at） |
| `clock_skew_ms` | 设备时钟与云端时钟偏差（可计算则记，否则 NULL） |

旧 telemetry 无这些字段时允许 `UNKNOWN`。History API 响应必须包含 time quality / gap metadata。

**GAP 检测与插值纪律**：`sequence gap / time gap / clock anomaly / unknown quality` 出现时标记 **GAP**，前端**禁止跨大 gap 插值**（不得把 08:14→08:20 的点插成虚假运动轨迹），timeline 显示数据缺口 `────── GAP ──────`。

---

## 13. Historical Configuration Resolver（P1-4，冻结）

**AssetConfigurationResolver**：`resolve(assetId, at)` 返回指定历史时刻 **T 有效** 的配置快照：

- Asset master revision（当时生效）
- Asset installation / location
- Equipment installations（valid at T）
- Equipment relations（valid at T）
- Runtime bindings（valid at T）
- Wire rope installations（valid at T）
- Software/Firmware installations（valid at T）
- Calibration records（valid at T）
- Twin model/version（valid at T）

**时态化要求**：所有 installation / relation / binding 增加 `valid_from / valid_to`（或等价 interval）；`NULL valid_to` = 当前有效。

**semantic_slot 双解析**：
```
HOIST_VFD
   ├── LIVE        → 当前 equipment installation
   └── HISTORY@T   → equipment installation valid at T
```

**API**：`GET /assets/{id}/configuration?at=`（或等价内部服务/API），供 Incident Review / Replay / Revision Compare 使用，必须返回 configuration valid at T。

**用途闭环**：回放 2026-05-12 事故时，Twin Inspector 显示**当时的**雷达型号/软件/标定，而非今天的；Incident Review 真正回答"故障发生时设备到底是什么配置"。
