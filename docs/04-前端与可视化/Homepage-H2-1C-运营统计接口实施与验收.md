# Homepage H2-1C：运营统计接口实施与验收

> 基线 `650a0aa`（CI `42555975224`）。本轮新增只读接口 `GET /api/v1/operations/summary`。没有切换首页 KPI 数据源，没有开始 H2-1D，没有创建 00013 或任何迁移，没有改生产数据库、部署或 main。
> 环境：本地沙箱（2 vCPU、约 4 GB）PostgreSQL 17.11（PGDG；CI/生产镜像 17.8）。所有性能数字来自合成数据，是容量证据，**不是生产 SLA**。

## 1. 交付内容

| 批次 | 内容 |
|---|---|
| H2-1C-1 契约与行政区 | `contracts/model.json` 新增 14 个命名类型（`OperationsSummary` 及其子对象），codegen 生成 Go 结构体、TS 接口、JSON Schema、OpenAPI 路径；`tools/codegen/geoindex.mjs` 从浏览器同一份 `geo-manifest.json` 生成 `internal/geo/index.gen.json` 并 `go:embed`，CI repository-contract 增加 `--check` |
| H2-1C-2 Repository | `internal/operations/summary.sql` 单条语句（H2-1B 形态 E + `unlocated`、`atScopeLevel`、`regions[]`、受保护的嵌套 CASE 时间解析、有界复核列表）；`internal/operations/summary.go` 绑定参数、强制字段完整性、快照内不变量、生成契约校验 |
| H2-1C-3 Handler 与验收 | `services/cloud-api/internal/app/operations_api.go`；`OPS_*` 启动校验；r2-db-test 门禁新增 10 个必过 PG 测试 + 1 个 HTTP 测试；B4 真实 Keycloak 只读用例；`tools/perf/operations-summary/` 可复跑基准与并发工具 |

## 2. 契约要点（生成物与 H2-1B 目标契约同构）

- 查询参数只允许 `region`（6 位）与 `alarmWindow`（PT1H/PT6H/PT24H/P7D，默认 PT24H）；其它参数一律 400。
- 所有计数 required、非负整数；只有 `scope.region` 可为 null；`operation`、`pendingEvents` 恒为 `{"status":"NOT_AVAILABLE"}`。
- 相对 H2-1B 目标契约的两处增补：`scope.geoIndexVersion`（行政区索引 SHA-256，前后端一致性凭据）与 `metrics.assets.atScopeLevel`（行政区代码恰好等于当前范围、无法下钻到子区的资产数，保证 `Σregions.registered + unlocated + atScopeLevel = registered`）。
- `tests/contract/operations-summary-contract.test.mjs`（8 测试）：校验生成的 OpenAPI 3.1、路径参数与安全方案、所有对象封闭且全 required、与 H2-1B 目标契约**结构同构**（规范化后逐字段比较类型/枚举/边界/可空）、生成 JSON Schema 的正反载荷、Go/TS 字段集合一致，以及**真实 API 响应**（Go HTTP 测试写出的 11 个响应）通过生成的 JSON Schema。反证：把 `realConnected` 改成 number、把 `unlocated` 移出 required，同构与封闭性测试各自失败。

## 3. 行政区（服务端）

- 统一源：`apps/web-console/public/geo/geo-manifest.json`（3,238 个区划：1 全国、34 省、363 地市、2,840 区县，其中 112 个县级直属省）。
- 生成：`node tools/codegen/geoindex.mjs`（索引版本 = 源文件 SHA-256）；`--check` 漂移即失败；`internal/geo` 测试再逐条比对源文件。反证：改动一个地市名称 → `--check` 失败、`TestEmbeddedIndexMatchesBrowserManifest` 失败。
- 解析时做结构校验（唯一代码、唯一全国根、父节点存在、层级严格加深），损坏/缺失 → `ErrUnavailable`；cloud-api **启动时**加载，损坏即拒绝启动；请求期不可用 → 503。
- 区域语义：格式错 400；已加载但不存在 404；索引不可用 503；存在但无授权项目 200 且全 0（不返回任何项目名或指标）。

## 4. 单条 SQL 的口径

授权先行（`users.enabled` ∧ 读能力角色的 membership）→ Inventory 项目 → 区域只收窄（服务端把区域展开为后代代码与「所属直接子区」两组数组）→ 资产排除 RETIRED → 全部运行身份 → 以同一 `sampledAt` 判新鲜 → 每资产一行去重 → 告警按运行源 × 资产归属互斥计数。

- 时间解析：`lastSeenAt` 先用正则确认是带时区的 RFC 3339，**在 CASE 内部**再判 `pg_input_is_valid`，最后才转换；不依赖 `AND` 求值顺序。空串、非法、无时区、零值、非字符串 → 设备级 UNKNOWN，不在线、不中止。反证：改回直接转换 → 整个 summary 因 `"yesterday-ish"` 失败。
- 位置：`adminCode` 缺失或不在索引中的项目算「无位置」（`unlocated`），只在全国范围计入；省/市/县范围不计入。
- 复核明细：先在 `per_asset` 上 `ORDER BY id COLLATE "C" LIMIT 100`，再聚合 JSON；`pendingReview` 是完整总数。反证：去掉 LIMIT → 不变量检查拒绝响应（503）。
- JIT：PostgreSQL 17 对该语句估算成本超过 `jit_above_cost`，JIT 编译单次约 45 ms 而执行本身几毫秒。Repository 在**只读事务**内 `SET LOCAL jit = off` 后执行该语句：设置随事务结束，测试证明连接池复用连接后 `SHOW jit` 不变。

## 5. HTTP

| 状态 | 条件 |
|---|---|
| 200 | 完整 summary；`Cache-Control: no-store` |
| 400 | `region` 非 6 位、`alarmWindow` 不在枚举、重复参数、任何其它参数（`project`/`status`/`source` 等） |
| 401 | 无会话、伪造会话、禁用用户（会话层拒绝） |
| 404 | 区域代码不在索引 |
| 503 | 行政区索引、授权查询、统计语句任一失败；超时（5 s，经 context 取消后端查询，不用会话级 `statement_timeout`） |

日志：`event=operations_summary`，含 requestId、状态、耗时、范围、错误类型、用户 id；不记录会话令牌、设备明细。启动：`OPS_STALE_AFTER_SECONDS`（默认 2100，300–7200）、`OPS_MAX_FUTURE_SKEW_SECONDS`（默认 120，0–600），非整数或越界拒绝启动（6 个子用例）。

示例请求与响应（来自真实 HTTP 测试，`evidence/homepage-h2-1c/responses/`）：

```http
GET /api/v1/operations/summary
Cookie: __Host-crane_session=…
```

```json
{"schemaVersion":1,"sampledAt":"2026-11-09T04:05:51.29023Z","generatedAt":"2026-11-09T04:05:51.294031408Z",
 "scope":{"region":null,"regionName":"全部授权范围","authorizedProjects":1,"geoIndexVersion":"2c3397c7…a634"},
 "freshness":{"staleAfterSeconds":2100,"maxFutureSkewSeconds":120,"alarmWindow":"PT24H","windowStart":"2026-11-08T04:05:51.29023Z","windowEnd":"2026-11-09T04:05:51.29023Z"},
 "metrics":{"projects":1,"assets":{"registered":1,"draft":0,"outOfService":0,"noRuntime":0,"unlocated":0,"atScopeLevel":0},
  "bound":{"REAL":0,"SIMULATION":1,"TEST":0},
  "real":{"connected":0,"pendingReview":0,"pendingReviewAlarm":0,"pendingReviewAssets":[],"stale":0,"unknownTime":0,"offline":0,"live":0},
  "operation":{"status":"NOT_AVAILABLE"},
  "alarm":{"devices":{"REAL":0,"SIMULATION":0,"TEST":0},"devicesAll":0,
   "records":{"cells":{"REAL":{"attributed":0,"unattributed":0},"SIMULATION":{"attributed":1,"unattributed":0},"TEST":{"attributed":0,"unattributed":0}},"total":1,"assets":1,"futureSkew":0}},
  "pendingEvents":{"status":"NOT_AVAILABLE"}},
 "regions":[{"adcode":330000,"name":"浙江省","registered":1,"realConnected":0,"realAlarmDevices":0}]}
```

（区域请求如 `?region=330000&alarmWindow=P7D` 的响应结构相同，`scope.region` 为请求代码、`scope.regionName` 为区域名称、`regions[]` 为其直接下级。）

## 6. 测试与反证

真实 PostgreSQL 必过（r2-db-test 门禁，精确 PASS，SKIP/漏选/失败即失败；本地等价运行 `evidence/homepage-h2-1c/r2-db-gate-local.out.txt` 13 项全部 `R2_REQUIRED_PASS`）：

| 测试 | 覆盖 |
|---|---|
| `TestSummaryIdentityRuntimeAndAlarms` | REAL+SIM 只算一台；REAL 离线 SIM 在线不计在线；LIVE 只是采集；DRAFT/OOS 待复核及确定性明细；退役不计；异常时间 UNKNOWN；告警 6 格互斥、去重资产、未来记录；时间字段 |
| `TestSummaryAuthorizationScopes` | viewer 仅 P1、platform_admin 仅 P1/P3 看不到 P2、无授权与禁用用户全 0、只含未授权项目的区域全 0 |
| `TestSummaryGeographyCountryProvincePrefectureCounty` | 全国/省/市/县/直属县、项目挂在省或市代码本级（atScopeLevel）、无位置与未知代码只在全国、全国分区之和等于总数、未知区域 404 |
| `TestSummaryFutureSkewBoundariesAndConfig` | skew 0/120/600 及恰在边界、599/600 观测边界、越界配置被 Config 与 SQL 双重拒绝、非法时间窗 |
| `TestSummaryEmptyDatabaseIsLegalZero` | 无项目无资产无告警：合法 0、空数组、NOT_AVAILABLE |
| `TestSummaryMoreThan100RecordsAndReviewAssets` | 158 条记录总数正确；135 个待复核只返回 100 条且顺序确定 |
| `TestSummaryQueryFailureIsUnavailableNotPartial` | 取消、缺表、索引不可用 → `ErrUnavailable`，无数据 |
| `TestSummaryConcurrentWritersSeeOneSnapshot` | 写者持续提交「资产+REAL 身份」时，40 次统计都满足 `bound.REAL = registered − 2` |
| `TestSummaryLeavesNoSessionSettingOnPooledConnection` | 单连接池重复统计与取消后 `SHOW jit` 不变 |
| `TestOperationsSummaryHTTP` | 200/400（9 种）/404/401（无会话、伪造、禁用用户）/503（索引、统计语句、授权查询）及恢复；区域钻取不放大授权；复核资产 id 可在资产中心同授权打开；全部响应按生成契约严格解码 |

反证（`evidence/homepage-h2-1c/counter-proofs.out.txt`，每组只撤销一条规则）：去掉 RETIRED 过滤、用 SIM 代替 REAL 在线、直接转换时间、取消授权过滤、取消区域收窄、取消复核 LIMIT、DRAFT 计入在线、取消时间下推、不检查用户启用；handler 接受未知参数、未知区域返回 200、索引失败继续计算、错误退化为 200 零值、按开发模式统计全部项目——14 组全部使对应测试失败。

B4（CI `b4-business-acceptance`，生产候选镜像 + 真实 Keycloak + PostgreSQL）：新增 `tests/e2e/b4/operations-summary.spec.ts` 2 个只读用例（`b4-admin` 仅 P001、`b4-p002` 仅 P002、`b4-noauth` 空结果、无会话 401；区域 330000 解析为「浙江省」证明镜像内置行政区索引；400/404 语义）。本地无 Docker：**NOT_RUN**，以 CI 为准。本地以与 Dockerfile 相同的 `CGO_ENABLED=0 go build -trimpath` 构建 cloud-api 二进制，确认内含索引版本与统计 SQL，`OPS_MAX_FUTURE_SKEW_SECONDS=601` 时启动失败。

## 7. 性能（合成数据，容量证据）

`tools/perf/operations-summary/bench.sh`（仓库相对路径，环境变量配置，全新环境可复跑）对**正式** `internal/operations/summary.sql` 按 Go 相同方式绑定参数；200 万告警（近 24 h 65,824 条）、jit off：

| 用户 | 范围 | 无新索引 warm 中位数 | `(device_id, observed_at DESC)` |
|---|---|---|---|
| 1 项目 | 全国 / 浙江 | 39.4 / 38.1 ms | 2.4 / 1.7 ms |
| 20 项目 | 全国 / 浙江 | 44.8 / 39.9 ms | 10.3 / 3.7 ms |
| 200 项目 | 全国 / 浙江 | 110.6 / 55.5 ms | 84.5 / 20.3 ms |

与 H2-1B 研究 SQL（同数据 155.5 ms / 全范围）相比，加入 `regions`、`unlocated`、`atScopeLevel` 与 jit off 后没有退化（110.6 ms）。只有「200 项目 × 全国」一格写临时盘 625 块（约 5 MB，`recordsAssets` 的 `count(DISTINCT)` 在 6.5 万行窗口记录上超过默认 `work_mem` 4 MB），其余均无临时盘。

并发（`tools/perf/operations-summary/concurrency`，调用正式 Repository，连接池 10 = cloud-api 配置，每级 90 请求，`evidence/homepage-h2-1c/concurrency.out.txt`）：

| 用户 | 单调用端到端 p50 | 10 并发 p50 / p95 / 吞吐 | 30 并发 p50 / p95 / 错误 |
|---|---|---|---|
| 1 项目 | 42 ms | 325 / 587 ms / 25.6 次/s | 799 ms / 2.2 s / 0 |
| 20 项目 | 47 ms | 360 / 557 ms / 25.2 次/s | 862 ms / 2.1 s / 0 |
| 200 项目 | 112 ms | 771 ms / 1.0 s / 12.4 次/s | 1.8 s / 3.5 s / 2（5 s 超时 → 503） |

在 2 vCPU 沙箱上吞吐受 CPU 饱和限制（并发增加只排队）；30 并发时请求在连接池排队，全范围用户少数超过 5 s 超时，按设计返回 503 而不是部分数据。连接池耗尽：300 ms 截止的调用得到 `ErrUnavailable`；查询中途取消：后端语句被取消、无残留活动语句；之后连接池恢复正常。首页按 60 s 刷新，单用户请求频率远低于上述排队阈值；真实容量需在生产规格硬件与真实数据上复测。

## 8. 回归

- Go：`go vet` 全部包；`-race` 通过 geo、operations、inventory、identity、contracts、migrations、asset、cloud-api 全部包；gofmt 无输出。
- 契约：`contracts.mjs`/`openapi.mjs`/`geoindex.mjs --check` 通过；`tests/contract` 49 项通过；仓库安全预检通过。
- 前端：typecheck、lint、build 通过；vitest 176 passed + 1 expected fail（H0-D1 保留）；首页 H1-R1 用例 11/11、H0 缺陷 10 passed（D2 预期失败保留、D3/D4 必过）、cloud 14/14、1280/1366 布局面积 2/2。首页仍使用原数据源（未切换 KPI）。
- B4 资产 CRUD/RBAC：本地 NOT_RUN（需 Docker），由同 SHA CI 验证。

## 9. 剩余风险

| 风险 | 影响 |
|---|---|
| 性能与并发数字来自合成数据与 2 vCPU 沙箱 | 不阻塞；上线前在生产规格与真实数据上用同一工具复测 |
| 全范围用户在高并发下排队，5 s 超时返回 503 | 首页 60 s 刷新下风险低；若出现，可先加 `(device_id, observed_at DESC)`（独立迁移，需审批） |
| `inventory.Projects` 三个运行计数口径仍未与 summary 统一（契约 §2.7） | 首页不使用它们；H2-1D 前需决定改名或改口径 |
| 前端尚未接入（H2-1D） | 本轮按要求不切换 |
| B4 新用例只在 CI 运行 | 以同 SHA CI 结果为准 |
