# Homepage H2-1D：Premium 首页接入权威运营统计

> 基线：远端 `637be0b`（CI `42566812090`：8/9 SUCCESS，integration 25 分钟超时）→ 本地 `5651e5b`（H2-1C-R2 修复，见 H2-1C 报告 §11）。H2-1D 三笔提交建立在 `5651e5b` 之上。
> 未改生产数据库、未建 00013、未合并 main、未部署、未改 B4 资产 CRUD 或成员管理。所有数字截图来自浏览器测试中的合成数据（mock API），不是生产数据。

## 1. 结论

API 模式首页的五张 KPI、"需要关注"、行政区下级计数、历史告警总数全部来自 `GET /api/v1/operations/summary`，按「登录身份 × 行政区 × 告警时间窗」请求与缓存。地图仍用 Inventory + Shadow 做空间展示，二者职责分离、互不推算。演示模式不创建统计服务、不调用 summary 接口。

## 2. 数据层（D1）

| 文件 | 职责 |
|---|---|
| `src/cloud/api.ts` | `getOperationsSummary(region, alarmWindow, signal)`，生成的 `OperationsSummary` 类型；`ApiError` 携带 HTTP 状态；10 s 超时与调用方取消合并 |
| `src/scene/operations-summary-provider.ts` | 纯状态机 + 缓存（无 Vue 依赖，Vitest 直接测）|
| `src/scene/use-operations-summary.ts` | 绑定登录身份/区域/时间窗；60 s 轮询；离开页面取消；行政区版本校验 |
| `src/scene/geo-manifest.ts` | 同一次读取得到行政区清单与其 SHA-256（= 后端 `geoIndexVersion`）|

状态：`loading`（首次）/`ready`/`refreshing`（保留原值）/`stale`（同一范围刷新失败，保留该范围上次成功值）/`unavailable`（该范围从未成功，无数字）。

范围隔离（P0）：
- 请求键 = `[身份, region(全国=100000), alarmWindow]`；身份 = 用户 ID + 排序后的「项目:角色」，授权增减即新身份。
- 切换范围立即 `abort()` 旧请求并递增序号；迟到的应答因序号/键不符被丢弃；应答的 `scope.region`、`alarmWindow` 与请求不一致也拒收。
- 失败只能回退到**同一键**的缓存；广东失败时显示「不可用」，不会把浙江的数当作「广东（已过期）」。
- 身份变化（退出、换账号、授权变化）清空全部缓存；401 由既有逻辑清会话 → 身份为空 → 状态 `idle` → 登录页。
- 同键并发请求共享一个 Promise；轮询不会叠加。
- 结构不合法的 200（如 `{}`）按错误处理，从不显示 0。

## 3. KPI 与关注区（D2）

| 卡片 | 字段 | 点击 |
|---|---|---|
| Inventory 登记设备 | `metrics.assets.registered` | 同授权同行政区 Inventory 资产清单（口径一致）|
| REAL 在线 | `metrics.real.connected` | 同范围资产清单，页面明确标注「不是该指标的精确筛选结果」|
| 当前 REAL 告警 | `metrics.alarm.devices.REAL`；副行「近 N 时间窗告警记录 `records.total` 条（全部来源）」| 同上标注 |
| 待复核设备 | `metrics.real.pendingReview` | 地图/Inspector 过滤为服务端 `pendingReviewAssets`（≤100）；列表显示「前 100 条 / 共 N 台」，每条打开 `/assets/{id}`（同授权）；不提供「查看全部」|
| 无运行身份 | `metrics.assets.noRuntime` | 同范围清单 + 标注 |

- 「真实运行」改为「REAL 在线」；卡片提示写明不代表正在作业；作业状态仍为「未接入」。
- 需要关注：待复核且告警、REAL 离线、REAL 数据过期、REAL 时间未知、无空间位置——各为独立字段，标题注明「各项独立统计，不相加」。
- 历史告警：KPI 副行与事件条都用 `records.total` 并写明时间窗；`/alarms` 明细只作为「最近记录明细」列出，不再把其长度当总数。时间窗可选 1 h / 6 h / 24 h / 7 d（属于请求键）。

## 4. 行政区、钻取、时间（D3）

- 进入省/市/县即请求 `?region=<adcode>`，全国不带参数。行政区导航每个下级显示 `regions[]` 计数，下方显示分区等式「登记 N = 下级 a + 本级 b（+ 无位置 c，仅全国）」，不假设下级之和等于总数。
- `scope.geoIndexVersion` 与浏览器清单 SHA-256 比对，不一致时整组统计不可用并提示「行政区数据版本不一致」（浏览器无法计算版本时不阻断，记为未验证）。
- API 模式移除「数据来源：登记项目」筛选：API 中全部项目都是登记项目，该按钮不过滤任何东西却容易被读成 REAL 连接筛选。演示模式保留。
- 时间：页头显示 `sampledAt`（统计取样）；悬停说明同时给出 `generatedAt`（服务端生成完成）与浏览器接收时间，全部为上海时间完整日期时间。
- **H0-D2 修复**：事件时间由 `sentAt.slice(11,19)`（UTC 子串）改为上海时间；`homepage-h0-defects.spec.ts` 中的 `test.fail` 改为必过（断言未删）。**H0-D1 未修复**（`aggregate()` 未动、首页不显示该数），`it.fails` 保留。
- Cloud Runtime 仍独立 15 s 轮询，测试断言 30 s 内不触发首页统计请求、61 s 时恰好刷新一次。
- 地图数据（Inventory/Shadow）失败单独提示「地图标注可能不完整；指标不受影响」，不再与 KPI 状态混用。

## 5. 测试

Vitest：23 文件 198 通过 + 1 预期失败（H0-D1）。新增 `operations-summary.test.ts`（14：身份键、状态机、503、stale、无效 200、版本拒绝、去重、浙江→广东→浙江乱序、广东失败、时间窗、退出/换号/授权变化、取消、60 s 轮询）与 `home-summary.test.ts`（8）。

Playwright（本地 headless，mock API）：

| 套件 | 结果 |
|---|---|
| `premium/homepage-h2-1d.spec.ts`（新增）| 16/16 |
| `premium/homepage-h1-r1.spec.ts`（R1-1 改为 summary 驱动，新增「地图数据失败不影响指标」）| 23/23 |
| `premium/homepage-h1-acceptance.spec.ts`（五分辨率 + 2D/3D/WebGL/弱网/失败重试/抽屉）| 14/14 |
| `premium/homepage-h0-defects.spec.ts`（H0-D2 转必过）| 10/10 |
| premium 其余 3 个文件 | 21/21 |
| cloud | 14/14 |
| demo（web-console 7 个文件）| 全部通过；`完整巡演循环` 在与其它用例同批运行时出现一次计时超时，单独重跑与基线均通过（2 vCPU 沙箱负载，非本轮改动）|

任务书 14 项对应：1–3「五张 KPI 精确等于字段 / 不从 Shadow 推算 / REAL 离线 SIM 在线」；4「告警 0 历史 150」；5「首次 503」；6「同范围刷新失败过期」；7「浙江广东乱序」；8「换账号/撤销授权」及 8b「401」；9「无授权合法 0」；10「全国/省/市/县路径与分区」及 10b「版本不一致」；11「演示模式不调用」；12 五分辨率（h1-acceptance）；13 2D/3D/Inspector/WebGL/历史（h1-r1 R1-4/5/6、h0-defects D3/D4）；14 B4 新增真实 Keycloak + PostgreSQL 用例「首页 KPI 等于本账号 summary；同一浏览器退出换 `b4-noauth` 后全为 0 且出现空态」——本地无 Docker，**NOT_RUN**，以同 SHA CI `b4-business-acceptance` 为准（套件由 31 增至 32 项）。

五分辨率（地图净可见面积，阈值 1280/1366 ≥45 %，其余 ≥35 %；`evidence/homepage-h2-1d/layout-metrics.json`）：

| 视口 | 3D | 2D | 横向溢出 | 最小字号 | KPI |
|---|---|---|---|---|---|
| 1280×720 | 48.0 % | 50.1 % | 0 | 10 | 5 |
| 1366×768 | 51.0 % | 52.9 % | 0 | 10 | 5 |
| 1440×900 | 55.8 % | 57.3 % | 0 | 10 | 5 |
| 1920×1080 | 42.1 % | 43.1 % | 0 | 10 | 5 |
| 2560×1440 | 44.3 % | 44.9 % | 0 | 10 | 5 |

截图：`evidence/homepage-h2-1d/screens/`（1280×720、1440×900 的 2D/3D）。

## 6. 反证（每次只撤销一条规则，测试必须失败；均已恢复）

| # | 撤销 | 失败的测试 |
|---|---|---|
| 1 | 去掉迟到应答丢弃（序号/键检查）| Vitest：乱序、换号、取消 3 项 |
| 2 | 失败时回退到任意缓存范围 | Vitest「广东失败」；浏览器 #7 `Expected unavailable, Received stale` |
| 3 | 身份变化不清缓存 | Vitest「退出/换号/授权」|
| 4 | 首次失败当作 ready | Vitest 503、无效 200、版本拒绝、广东失败 4 项 |
| 5 | KPI/关注项缺数据时显示 0 | 浏览器 #5 `Received ["0","0","0","0","0"]` |
| 6 | 不按区域请求（始终全国）| 浏览器 #10 `Expected 3台, Received 4台` |
| 7 | 事件时间改回 UTC 子串 | H0-D2 `Expected 16:49:15, Received 08:49:15` |
| 8 | REAL 在线改用 `bound.REAL` | 浏览器 #1 `Expected 4台, Received 9台` |
| 9 | 关闭行政区版本校验 | 浏览器 #10b `Expected unavailable, Received ready` |
| 10 | 非精确 KPI 不带 `kpi=` 标注 | 浏览器「业务钻取」URL 断言失败 |
| 11 | REAL 在线改为本地 Shadow 推算 | 浏览器 #1 `Expected 4台, Received 0台` |

## 7. 产品限制（如实）

- REAL 在线 / 当前 REAL 告警 / 无运行身份没有同口径的明细接口，点击只能打开同范围的普通资产清单并标注非精确；待复核只有前 100 条明细，没有「查看全部」。
- 地图标注、Inspector 设备列表、二维色块仍基于 Inventory + Shadow（客户端口径），可能与 KPI 数字不同；KPI 以服务端为准。
- `/alarms` 明细仍为全部授权范围最近 ≤100 条，不随行政区或时间窗变化。
- 行政区版本在非安全上下文（无 SubtleCrypto）无法校验，此时不阻断。
- H0-D1 仍未修复。B4 新用例仅在 CI 运行。
- 性能/并发数字仍是 H2-1C 的合成数据结论，首页 60 s 刷新未在生产规格复测。
