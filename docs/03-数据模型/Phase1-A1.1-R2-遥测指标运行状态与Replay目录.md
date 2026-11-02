# Phase1-A1.1-R2 遥测指标、运行状态与 Replay 目录

## 1. Metric 定义原则

每个 metric 有 stable code、display name、unit、value type、source mapping、aggregation、interpolation、max gap、quality requirement。UI 不知道原始 JSON path。

## 2. Crane motion

| Code | Unit | Aggregation | Replay |
|---|---|---|---|
| crane.position.x | m | last/min/max | LINEAR within segment |
| crane.position.y | m | last/min/max | LINEAR |
| crane.position.z | m | last/min/max | LINEAR |
| crane.grab.opening | ratio/% | last | LINEAR/STEP by source |
| crane.load | t | avg/max/last | LINEAR |
| crane.operating_mode | enum | last | STEP |

## 3. Counters

- crane.runtime.hours；
- crane.hoist.runtime.hours；
- crane.work_cycles；
- crane.lift_count；
- crane.motor_starts；
- crane.emergency_stop_count；
- crane.brake_cycles；
- crane.travel_distance.x/y/z；
- crane.overload_count。

Counter Replay 使用 STEP/LAST，不线性插值。

## 4. Equipment common

- equipment.connectivity；
- equipment.condition；
- equipment.temperature；
- equipment.current；
- equipment.voltage；
- equipment.power；
- equipment.fault_code；
- equipment.running_state。

## 5. PLC

`plc.mode`、`plc.scan_time_ms`、`plc.diagnostic_code`、`plc.io_quality`。

## 6. VFD

`vfd.running`、`vfd.output_frequency_hz`、`vfd.output_current_a`、`vfd.dc_bus_v`、`vfd.temperature_c`、`vfd.fault_code`。

## 7. Motor / Brake

motor current/temp/vibration/start count；brake state/temp/cycle/wear_signal。

## 8. LiDAR / Camera

LiDAR packet/point rate/temp/error/time-sync；Camera frame rate/drop/temp/stream state。无实际驱动来源时保持 UNAVAILABLE，不创建假数据。

## 9. IPC / Network

IPC CPU/memory/storage/temp/process health；Switch port/link/PoE power/temp（未来数据源具备时启用）。

## 10. Quality

每 sample 可携带：GOOD/UNKNOWN/DEGRADED + flags。History UI 对 degraded/gap 使用视觉断点，不隐藏质量问题。

## 11. Derived metrics

availability/OEE 类派生值必须记录 formula/version/window，不能在 UI 临时算完当成事实。R2 先实现必要的简单 availability；复杂 KPI 后置。

## 12. Alarm correlation

Alarm 必须尽可能关联 asset/equipment/semantic_slot/metric/time。Incident Review 用该关联决定默认 metrics 和 camera focus。

