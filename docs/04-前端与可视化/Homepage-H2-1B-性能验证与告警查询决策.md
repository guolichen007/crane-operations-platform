# Homepage H2-1B：运营统计 SQL 性能验证与告警查询决策

> 基线 `6badf6a`（CI `42552407036`）。本轮只改研究 SQL、探针、草案契约与契约测试；**没有**新增生产 summary 接口、**没有**创建 00013、没有改资产中心 CRUD、没有改首页 KPI 来源、没有部署、没有合并 main。
> **证据性质**：全部数据为合成数据，在本地沙箱（2 vCPU、约 4 GB 内存、本地磁盘）上的 PostgreSQL 17.11（PGDG 包；生产与 CI 镜像为 `postgres:17.8`，同一主版本、同一规划器）运行，`shared_buffers=128MB`（与官方镜像默认一致）。这不是生产负载认证，也不是生产数据实测。

## 0. 结论

1. **完整 summary SQL 正确性通过**：17 条 SQL 内断言全部 OK（`evidence/homepage-h2-0/summary-probe.out.txt`），5 组反证全部按预期失败。
2. **选定 SQL 形态 E**（告警在扫描时即按 `observed_at >= windowStart` 下推时间条件；`per_asset` 与 `scoped_alarms` 各只聚合一次）。与当前草案形态 A 相比，全部授权（200 项目）用户在 40 万告警下 warm 中位数 194.6 → 91.2 ms，200 万告警下 622 → 155 ms。
3. **`NOT MATERIALIZED` 不可用**：强制内联使计划膨胀为 608 个节点、告警表被扫描 4 次、约 0.9–1.0 s（形态 A 的 3–4 倍，形态 E 的 9 倍以上）。保持 PostgreSQL 默认（被多次引用的 CTE 物化）。
4. **00013 暂不建索引**。在不建新索引的情况下，形态 E 在全部合成分布中 warm 中位数 ≤ 156 ms（最差：200 万告警、全部 200 项目），1/20 项目用户 ≤ 50 ms，对 60 s 一次的首页刷新没有阻塞。`(device_id, observed_at DESC)` 对小授权范围效果最明显（200 万告警、1 项目：41.5 → 1.3 ms），对全范围用户收益有限（155 → 120 ms），且占用随告警量线性增长（40 万 12 MB / 200 万 60 MB）。在拿到真实数据分布前，**不创建 00013**；H2-1C 的只读接口不依赖它，索引作为单独性能优化发布（设计保留在契约 §6）。
5. **全范围用户的主要开销不在告警扫描**：自耗时分析显示 `params`/`per_asset` 链路（3,300 个运行身份 Shadow 的 JSON 字段提取与时间解析）约占 35–40 ms，与告警数量无关；任何告警索引都无法消除这部分。
6. **坏的观测时间按设备级 UNKNOWN 处理**，不中止整个 summary；一条坏数据也不会被统计为在线。
7. **OpenAPI 原始契约**：`evidence/homepage-h2-1b/operations-summary.openapi.json` 按原样通过 OpenAPI 3.1 校验（不在测试中补写任何字段），新增 `tests/contract/operations-summary-contract.test.mjs` 接入 repository-contract；3 组契约反证全部被捕获。

## 1. H2-1B-1：完整 SQL 执行计划对比

测试对象是仓库中的完整 `summary-draft.sql`（授权 CTE、资产与运行身份、`per_asset` 去重、`scoped_alarms` 多次引用、24 h 窗口、`recordsFutureSkew`、整份 JSON 聚合），不是单独的告警计数查询。

| 形态 | 含义 |
|---|---|
| A | H2-0-R3 草案：`scoped_alarms` 不带时间条件，窗口与未来记录在之后再过滤；约 20 个标量子查询 |
| B | A + 全部 CTE `NOT MATERIALIZED` |
| C | A + 时间条件下推到告警扫描（`al.observed_at >= windowStart`） |
| D | C + 全部 CTE `NOT MATERIALIZED` |
| **E** | C + `per_asset` / `scoped_alarms` 各自一次聚合（`agg_asset`、`agg_alarm`），去掉 `alarm_window` CTE（**选定**） |

数据集 D1（40 万告警，最近 24 h 约 3.3%，均匀分布），无新索引，`u-p200`（200 个项目），`EXPLAIN (ANALYZE, BUFFERS, VERBOSE)`：

| 形态 | 计划节点（`->`） | CTE Scan | 告警扫描 | 告警扫描返回行 | Rows Removed（告警） | Shared hit / read（顶层） | 临时盘 | Execution |
|---|---|---|---|---|---|---|---|---|
| A | 88 | 34 | Seq Scan ×1 | 400,000 | 0（在 CTE 外再过滤 387 k） | 38 / 3,088 | **temp read 1,495 / written 1,495 块**（40 万行告警 CTE 物化溢出到磁盘） | 267 ms |
| B | 608 | 0 | Seq Scan ×4 | 13,281 ×3 + 1 | 386,719 ×3 + 193,360 | 89,018 / 35 | 0 | 969 ms |
| C | 88 | 34 | Seq Scan ×1 | 13,281 | 386,719 | 12,872 / 0 | 0 | 135 ms |
| E | 44 | 9 | Seq Scan ×1 | 13,281 | 386,719 | 12,872 / 0 | 0 | 106 ms |

（数字取自已提交的计划文件 `evidence/homepage-h2-1b/plans/400k-noidx-{A,B,C,E}-u-p200.txt`。u-p20 的同类对比：A 53 ms、B 968 ms、C 18 ms、D 923 ms、E 17 ms。）


观察：

- **A 出现窗口外大量历史告警**：告警 CTE 物化了全部 40 万行，再由下游 CTE Scan 过滤；时间条件没有下推。
- **C/E 的时间条件确实下推**到 `Seq Scan on public.alarms` 的 Filter（或在有索引时成为 Index Cond），只把 1.3 万行送入后续连接。
- **B/D（NOT MATERIALIZED）**：没有 CTE Scan，但每个引用都内联了一份 `scoped_alarms` 与 `per_asset` 子树，计划从 88 个节点膨胀到 608 个，告警表被扫描 4 次，`assets` 等被重复计算，Shared hit 约为 C/E 的 7 倍。结论：`NOT MATERIALIZED` 不是优化，反而有害。
- **A 在全范围用户下使用临时磁盘**（物化的 40 万行告警 CTE 超过 `work_mem` 溢出）；C/E 时间条件下推后不再使用临时盘。
- E 的剩余开销（u-p200）：`params` 链路约 35 ms（Shadow JSON 与时间解析，跟随 `per_asset` 一起被计入）、`Hash Join`（运行身份 × Shadow）约 24 ms、`scoped_alarms` 扫描与连接约 30 ms。把时间解析换成直接转换（不安全，仅诊断）可从 91 降到 69 ms——说明防御性解析约占 20 ms，这是正确性的代价，保留。

## 2. H2-1B-2：索引必要性

数据集（`evidence/homepage-h2-1b/perf-dataset.sql`）：200 个 Inventory 项目 + 5 个遗留项目、2,000 资产（1% RETIRED、2% DRAFT、2% OUT_OF_SERVICE）、2,000 REAL + 1,000 SIM + 100 TEST 资产运行身份、200 个孤立运行身份、3,300 个 Shadow；告警分布在 `sampledAt` 之前 30 天。用户：`u-p1`（1 项目）、`u-p20`（20 项目）、`u-p200`（全部 200 项目，platform_admin）。

方法：每格先重启 PostgreSQL 后执行 1 次（PG 冷缓存；沙箱无法清空操作系统页缓存，所以「冷」只代表 shared_buffers 冷，并记录 shared read 块数），再连续执行 7 次取 warm 中位数（`\timing`，无 EXPLAIN 插桩）。原始数据 `bench-results.txt`。

形态 E 的 warm 中位数（ms）：

| 数据集 | 索引 | u-p1 | u-p20 | u-p200 | 索引大小 |
|---|---|---|---|---|---|
| D1 40 万 / 3.3% 近 24 h / 均匀 | 无 | 11.6 | 17.8 | 91.2 | — |
| | `(device_id, observed_at DESC)` | 1.5 | 6.2 | 79.0 | 12 MB |
| | `(observed_at DESC)` | 4.5 | 8.7 | 86.4 | 8.8 MB |
| D2 40 万 / 20% 近 24 h | 无 | 13.5 | 22.1 | 132.3 | — |
| | `(device_id, observed_at DESC)` | 1.4 | 9.3 | 119.3 | 12 MB |
| | `(observed_at DESC)` | 10.6 | 19.7 | 132.7 | 8.8 MB |
| D3 40 万 / 60% 集中在 10 台热点设备 | 无 | 11.9 | 16.2 | 91.3 | — |
| | `(device_id, observed_at DESC)` | 1.3 | 6.7 | 90.4（规划器改选 Seq Scan） | 12 MB |
| | `(observed_at DESC)` | 4.1 | 9.4 | 86.8 | 8.8 MB |
| D4 200 万 / 3.3% 近 24 h | 无 | 41.5 | 49.2 | 155.5 | — |
| | `(device_id, observed_at DESC)` | 1.3 | 11.0 | 120.0 | 60 MB |
| | `(observed_at DESC)` | 18.9 | 25.9 | 131.1 | 43 MB |

PG 冷缓存（重启后首次执行）最差值：D4 无索引 u-p200 167 ms（shared read 14,852 块）、D4 `(device_id, observed_at)` u-p200 187 ms。

结论：

1. 不建索引时，形态 E 的耗时随「近 24 h 告警量」与「授权项目数」增长，随告警总量近似线性增长（40 万 → 200 万：u-p1 11.6 → 41.5 ms）。在合成规模下没有达到 60 s 刷新的阻塞级别。
2. `(device_id, observed_at DESC)` 对 1/20 项目用户是数量级改善（Index Only Scan，只读取已授权设备），对全范围用户收益约 20%，热点分布下规划器会放弃它。
3. `(observed_at DESC)` 收益在两者之间，且在近 24 h 占比高（D2）时几乎无收益。
4. **决策：暂不建 00013**。H2-1C 先以无新索引上线只读接口；当真实数据表明「小授权范围用户 + 告警总量增长」成为瓶颈时，再以 `(device_id, observed_at DESC)`、`CREATE INDEX CONCURRENTLY` + goose 非事务 Go 迁移（契约 §6.2 的 INVALID 索引恢复方案）单独发布。
5. 限制：合成数据无法反映真实设备数、告警突发、`device_shadow` 真实 JSON 大小、并发请求与 autovacuum；2 vCPU 沙箱的绝对数字不可外推。

## 3. H2-1B-3：契约边界与正确性

探针（`evidence/homepage-h2-0/summary-probe.sql`，PostgreSQL 17，所有期望在 SQL 内断言）：

**未来时间偏差（契约范围 0–600 s）**

| 用例 | 断言 | 结果 |
|---|---|---|
| K0 skew = 0 | 未来记录从 `sampledAt` 起算（含恰好等于 `sampledAt` 的记录）：6 条 | OK |
| K120 skew = 120 s（默认） | 恰好 `sampledAt + 120 s` 的记录计为未来（`>=`）；`sampledAt` 当刻的记录既不在窗口也不在未来 | OK |
| K600 skew = 600 s | `+120 s` 不再算未来；恰好 `+600 s` 算未来；超前 1 h 的设备仍为过期 | OK |
| KB600 / KB599 | 观测时间恰好 `sampledAt + 600 s`：skew 600 时在线，599 时过期 | OK |
| KX | skew −1、601、7200，stale 299：SQL 自身拒绝（`out of range`），不产生任何数字 | REFUSED OK ×4 |

H2-0-R3 的 R7（7200 s）不再作为合法配置证明，已改为 KX 的拒绝用例。HTTP 层的 400 由 H2-1C handler 实现并测试（见 §5）；SQL 内的范围检查是第二道防线。

**Shadow 异常观测时间——策略：设备级 UNKNOWN**

| 资产 | `lastSeenAt` | 结果 |
|---|---|---|
| T1 | 空字符串 | realUnknownTime，不在线 |
| T2 | `"yesterday-ish"`（非法格式） | realUnknownTime，不在线 |
| T3 | `"2026-11-07T07:59:00"`（无时区） | realUnknownTime，不在线（不按会话时区猜测） |
| T4 | `"0001-01-01T00:00:00Z"`（Go 零值） | realUnknownTime，不在线 |
| T5 | 数字 `1730966340`（非字符串） | realUnknownTime，不在线 |
| T6 | `"2026-11-07T15:59:00+08:00"`（合法偏移时区） | 正常解析为 07:59Z，在线 |
| A6 | 超前 1 h | realStale，不在线 |
| A7 | 缺失 | realUnknownTime，不在线 |

接受条件：RFC 3339 且带显式时区（`Z` 或 `±hh:mm`）、`pg_input_is_valid` 通过、不早于 2000-01-01。选择设备级 UNKNOWN 而不是整份 503，理由：一台设备的坏数据不应让所有用户的首页不可用；同时它被单独计数（`real.unknownTime`）且绝不计为在线。反证：把解析改回直接转换 `::timestamptz` → 整个 summary 因 `"yesterday-ish"` 报错失败；去掉时区格式检查 → 无时区值被当作 UTC 解析并错误计入过期；去掉零值下限 → 零值被计入过期而不是 UNKNOWN。

**DRAFT / OUT_OF_SERVICE 现场异常**：A8（DRAFT + REAL 在线）、A9（OUT_OF_SERVICE + REAL 告警）、A13（DRAFT + REAL/SIM 双身份在线）、D1（DRAFT + REAL 告警）、D2（DRAFT + REAL/SIM 双身份同时告警）全部进入 `realPendingReview = 5`，其中带告警的 `pendingReviewAlarm = 3`（A9、D1、D2）；它们不进入 `realConnected` 与主告警 KPI，但通过新增的 `pendingReviewAssets` 明细（资产 id、状态、是否告警，最多 100 条，id 可直接打开资产中心详情）保持可见。反证：去掉待复核规则 → `realPendingReview: got 0, want 5`。

**告警去重与归属**（R1）：A10 同一资产 REAL 与 SIM 同时告警 → 来源设备数 REAL 2 / SIM 2，按资产去重 All 3；A10 窗口内 3 条记录（REAL 2 + SIM 1）只计 1 个资产；记录合计 9 = 6 个互斥单元格之和，去重资产 5（`recordsAssets`），无资产归属 2（REAL 1 + TEST 1）；退役资产、未授权项目、遗留项目的记录都不计；无位置项目 P3 的记录只在全国范围计入（R2 区域范围下 8 条、4 个资产）。

## 4. H2-1B-4：OpenAPI 原始契约

- 文件：`evidence/homepage-h2-1b/operations-summary.openapi.json`，完整 OpenAPI 3.1 文档（info、security、paths、components 齐全；200/400/401/404/503 均有 description；`webSession` 与生成的 `contracts/openapi/cloud-api.yaml` 逐字段一致，由测试断言）。
- 测试：`tests/contract/operations-summary-contract.test.mjs`（已加入 repository-contract），5 个测试：
  1. swagger-parser 按原样校验（并断言校验未修改文档）；
  2. 200 响应与安全方案；参数只有 `region`、`alarmWindow`；
  3. 每个对象 `additionalProperties: false`、全部属性 required、只有 `scope.region` 可为 null、不使用 3.0 的 `nullable`；
  4. ajv（JSON Schema 2020-12）校验：授权但为空的全 0 载荷合法、带数据载荷合法；
  5. 计数为 null、缺失、字符串、负数，`operation` 用数字代替 `NOT_AVAILABLE`，多余字段，skew 7200 或 −1，复核列表中出现 RETIRED——全部被拒绝。
- 反证：删除 200 description、把 `Count` 改成可空、把 `pendingEvents` 移出 required，各自导致对应测试失败（`openapi-contract-check.out.txt`）。
- 与 codegen 的关系：仓库的 `contracts/openapi/cloud-api.yaml` 由 `tools/codegen/openapi.mjs` 从 `model.json` 生成。本文件是 H2-1C 合入前的目标契约；H2-1C 把这些 schema 加入 `model.json` 并重新生成，届时这个契约测试改为校验生成结果中的同名 schema。

## 5. H2-1C 最小实施清单（不需新索引）

| 文件 | 内容 |
|---|---|
| `contracts/model.json` → codegen | 加入 `OperationsSummary` 及子 schema（以 `operations-summary.openapi.json` 为准）；`tools/codegen/openapi.mjs` 增加 `/api/v1/operations/summary` 的 `add(...)`；`--check` 通过 |
| `internal/operations/summary.go`（新） | `type Params{User string; WindowStart, SampledAt time.Time; StaleAfter, MaxSkew time.Duration; RegionCodes []int}`；`func Summary(ctx, db, Params) (contract.OperationsSummary, error)`：执行形态 E 的单条 SQL（`$1..$6`），在调用前取 `sampledAt`，查询后取 `generatedAt`；解析 JSON 时任一字段缺失即返回错误（不补 0） |
| `internal/operations/region.go`（新） | 从 geo-manifest 把区域展开为后代 `admin_code` 列表（复用前端同一份 `geo-manifest.json` 的服务端加载）；未知区域返回 `ErrRegionNotFound`；清单读取失败返回错误（→ 503） |
| `internal/platform` 配置 | `OPS_STALE_AFTER_SECONDS`（默认 2100，范围 300–7200）、`OPS_MAX_FUTURE_SKEW_SECONDS`（默认 120，范围 0–600）；启动时越界即拒绝启动 |
| `services/cloud-api/internal/app/operations_api.go`（新） | `GET /api/v1/operations/summary`：401 无会话；400 `region` 非 6 位数字或 `alarmWindow` 不在枚举；404 区域不存在；503 授权查询 / SQL / 清单失败（不返回部分数据）；200 带 `Cache-Control: no-store`；`statement_timeout`（建议 5 s）在单个查询上设置 |
| `internal/operations/summary_test.go`（新，真实 PG） | 移植 `summary-probe.sql` 的 R1–R6、K0/K120/K600/KB、异常时间 T1–T6、待复核与去重断言；并发快照反证（写者在两次扫描之间提交，`bound.REAL ≤ registered` 恒成立） |
| `services/cloud-api/internal/app/operations_http_test.go`（新，真实 PG + OIDC） | 401、400（region/alarmWindow）、404、503（注入 `statement_timeout`）、platform_admin 仅 P1 看不到 P2、禁用用户、无授权全 0、区域只含未授权项目返回 0；响应用契约 schema 校验 |
| `tools/r2-b2-test.mjs` | 把以上两组测试加入 H2-1A 同款强制门禁（精确 PASS，SKIP 即失败） |
| 前端 | **不改**。首页 KPI 来源切换与 60 s 刷新 / 5 min 过期 / `fetchedAt` 展示留到 H2-1D |

## 6. 未解决风险与影响判断

| 风险 | 是否阻塞 H2-1C |
|---|---|
| 性能数字全部来自合成数据与 2 vCPU 沙箱 | 不阻塞；H2-1C 上线后应在真实库上用同一脚本复测，再决定 00013 |
| 全范围用户约 90–155 ms，主要成本是 Shadow JSON 解析，随运行身份数线性增长 | 不阻塞 60 s 刷新；若运行身份达到数万，H2 后续可考虑把 `lastSeenAt` 物化为 `device_shadow` 的列（需单独迁移） |
| 冷缓存只测到 PostgreSQL 层，未清空操作系统页缓存 | 不阻塞；生产首次请求可能更慢 |
| 区域展开依赖服务端加载 geo-manifest | H2-1C 内实现，失败按 503 处理 |
| `inventory.Projects` 的三个运行计数口径未统一（契约 §2.7） | 不阻塞接口；H2-1C 决定改用 summary 口径或改名 |
| H0-D1 / H0-D2 仍为预期失败 | 属 H2-1D |
