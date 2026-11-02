# Phase1-A1.1-R2 工业历史、时态与事故取证数据模型

## 1. 目标

平台必须回答：

- 某次故障发生时装的是哪一台 LiDAR/PLC/VFD？
- 当时的软件/固件/标定版本是什么？
- 故障前 30 分钟 X/Y/Z/载荷怎样变化？
- 数据是否断流、补传或设备时钟异常？
- 谁在什么时候修改了资产资料？修改在业务上何时生效？

## 2. 六层历史

1. **Current State**：normalized 业务表 + `device_shadow`；
2. **Business Revision**：业务主数据历史；
3. **Lifecycle History**：安装/拆除/替换/标定/软件升级；
4. **Event History**：alarm/work order/inspection/audit；
5. **Telemetry History**：设备时序数据；
6. **Aggregated History**：按分钟/15min/小时聚合，R2 可按需即时计算，后续再物化。

## 3. entity_revisions

建议：

```text
id
project_id
asset_id*
entity_type
entity_id
revision
recorded_at       -- 系统记录时间
effective_at      -- 业务实际生效时间
changed_by
reason*
request_id
changed_fields*
snapshot_json
snapshot_sha256
```

只保存 BUSINESS_OWNED 安全字段快照；禁止 Secret、token、private key、credential。

`recorded_at != effective_at` 是重要能力。例如 11 月 5 日补录“设备在 10 月 20 日实际已经更换”。

## 4. Timeline

`asset_timeline_events` 只做统一事件索引：

```text
id/project_id/asset_id
category
occurred_at
recorded_at
source
reference_type/reference_id
summary
severity
```

Canonical payload 继续留在 alarm/work_order/inspection/calibration/software/replacement 等原表。Timeline 不能演变为 generic history 大表。

## 5. Telemetry 时间模型

现有 wire envelope 已有 sentAt/boot/sequence 类信息，应在 Cloud 落库时保留，而不是只保存 receive time。

建议 telemetry 增量字段：

```text
source_sent_at
cloud_received_at
boot_id
boot_seq
sequence
late_arrival
clock_skew_ms
quality_flags[]
```

老数据字段允许 UNKNOWN，不回填虚构质量。

### 质量 flags 示例

- SOURCE_TIME_MISSING
- CLOCK_SKEW
- LATE_ARRIVAL
- OUT_OF_ORDER
- SEQUENCE_GAP
- DUPLICATE
- BOOT_CHANGED
- PARSE_PARTIAL

## 6. GAP 检测

GAP 不能只按时间差判断：

- 同 boot_id 内 sequence 不连续 → sequence gap；
- boot 变化 → session boundary，不应误报普通 gap；
- source_sent_at 大间隔超过 metric 的允许阈值 → time gap；
- clock skew 超阈值 → quality warning。

History API 返回 `segments` 与 `gaps`。前端不得跨 GAP 做平滑插值。

## 7. Interpolation Policy

Metric definition 增加 replay interpolation：

- POSITION_X/Y/Z：LINEAR，但只在连续 segment；
- DIGITAL_STATE / MODE / ALARM：STEP；
- COUNTER：STEP/LAST；
- UNKNOWN/quality bad：NONE。

禁止把 08:14 到 08:20 的断流数据“漂亮地补成连续轨迹”。

## 8. Telemetry Metric Catalog

```text
metric_code
display_name
value_type
unit
source_path
aggregation
interpolation
max_gap_seconds
category
status
```

前端只能按 metric code 请求，不能硬编码 JSON path。

首批建议：crane.position.x/y/z、crane.grab、crane.load、crane.operating_mode、crane.runtime、crane.work_cycles、crane.motor_starts、crane.emergency_stops、equipment.temperature/current/voltage/connectivity/condition。

## 9. ConfigurationResolver

`resolve(assetId, at)` 是 R2 历史可信度核心。

必须解析该历史时刻有效的：

- Asset Revision；
- Asset Installation/Location；
- Equipment Installations；
- Equipment Relations；
- Runtime Bindings；
- Wire Rope Installations；
- Software/Firmware；
- Calibration；
- Twin Model/semantic manifest。

所有 installation/relation/binding 都必须有 valid_from/valid_to 或等价 interval。

## 10. History API

```http
GET /api/v1/projects/{projectId}/assets/{assetId}/history
GET /api/v1/projects/{projectId}/assets/{assetId}/timeline
GET /api/v1/projects/{projectId}/assets/{assetId}/revisions
GET /api/v1/projects/{projectId}/assets/{assetId}/configuration?at=...
GET /api/v1/projects/{projectId}/equipment/{equipmentId}/history
```

参数：metric、from、to、resolution(raw/1m/15m/1h/auto)。

保护：
- 最大时间窗口；
- 最大 metric 数；
- 最大点数；
- server-side aggregation/downsampling；
- request cancellation；
- Project Scope First。

推荐前端单图默认最大 2k～5k points/series；超过自动提高 resolution，具体阈值在 B8 benchmark 后冻结。

## 11. Incident Review

选择 Alarm 后默认窗口 `T-30min → T+10min`，同屏显示：

- Position/Load/Operating State；
- Equipment Condition；
- Alarm/WorkOrder/Inspection/Change Timeline；
- Configuration@T；
- Software/Calibration@T；
- Gap/clock quality；
- 3D Replay。

Incident Review 是只读证据工作区，不连接 Remote Control。

## 12. Retention

业务档案、Revision、Lifecycle、Inspection、Work Order、Calibration、Software、Replacement 在 R2 不自动 purge。

Telemetry retention 可配置，但 R2 默认 purge OFF。真实数据量达到可测量 trigger 后，再决定 PostgreSQL partition/rollup/TSDB；不得提前引入额外基础设施。

