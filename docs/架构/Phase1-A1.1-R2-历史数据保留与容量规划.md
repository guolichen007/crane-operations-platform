# Phase1-A1.1-R2 历史数据保留与容量规划

> 回答「数据保存多久、telemetry 何时需要换存储、何时需要 partition/TSDB」。DESIGN_ONLY=YES。R2 不引入 InfluxDB/TimescaleDB/ClickHouse/Kafka。

## 1. Retention 模型

| 数据 | 保留策略 |
|------|---------|
| 业务档案（asset/equipment/wire rope/inspection/work order/replacement/calibration/software history） | **本轮禁止自动删除** |
| Telemetry（raw） | 可配置 retention，但 **R2 默认 auto purge disabled** |
| 聚合（minute/hour rollup） | policy model 先设计，后置 |

> 法定保存期限不在 R2 自行发明（合规档案保存期限由正式法规文本核验，见蓝图 OPEN 项）。

## 2. 未来 rollup policy model（仅设计，不启用）

```
raw      → 保留 N 天（默认不启用）
1m  rollup → 保留 M 天
1h  rollup → 保留更久
```

真正启用前须以真实数据量/查询 P95/磁盘增长为输入；默认全关。

## 3. telemetry 容量规划 trigger（可测量，达到才动）

| 触发条件 | 阈值（示例，待真实数据校准） | 动作 |
|---------|------------------------------|------|
| rows/day | telemetry_samples 日增 > 500 万行（≈100 设备 × 1Hz × 全天 + LIVE 突发） | 评估 rollup/retention |
| query P95 | 历史查询 P95 > 500ms（持续） | 评估索引/降采样 |
| disk growth | 磁盘月增 > 50GB | 评估 partition/归档 |
| retention window | 需查询窗口 > 现有索引可支撑范围 | 评估 TSDB |

> 触发条件必须**可测量**；未达标不提前重构。

## 4. 何时需要 telemetry v2 / partition / TSDB（决策框架）

- **现在（R2）**：保持 `telemetry_samples`（`device_id, sampled_at DESC` 索引）+ `device_shadow`。不强行 partition（现有普通表不能简单原地变 partitioned table，贸然重构风险更高）。
- **何时 partition**：单表行数进入数亿、按 `sampled_at` 范围删除成为常态、查询 P95 因全表扫描上升时，才以**新表 + 迁移**方式引入 PostgreSQL declarative partitioning。
- **何时 TSDB**：出现高基数海量传感器、需要列式压缩、需要自动下采样、需要远超 PG 的写入吞吐时，才评估 InfluxDB/TimescaleDB/ClickHouse；**Kafka 不在本项目引入清单**（身份/安全体系已固化，不新增基础设施）。
- 结论：以「真实设备量与采样频率」为唯一依据，不提前过度设计。

## 5. 与历史架构的关系

- 业务历史（revision/lifecycle/event）走业务库，不依赖 telemetry 表；不受 retention 影响。
- telemetry 只承载遥测历史；retention 只作用于遥测层，不触碰业务档案。
