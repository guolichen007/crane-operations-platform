# R7 SIM-E2E 云边模拟联通与业务全流程闭环报告

- 基线：`feature/crane-r6-biz-closure` @ `a2a89ea711ae7425906185cd8421f1f046d82f6c`（Tag `dev/crane-r6-biz-closure`，CI 44012196803 9/9），Tag 未移动
- R7 分支：`feature/crane-r7-sim-e2e`
- 数据库 Schema：**12**（无迁移）
- 不接真实行车，不实现机械运动控制；WATCH 仅用于查看遥测；未升级现场 R6、未覆盖任何数据库

## 1. 三个现场缺陷（先复现旧失败，再验证修复）

| 缺陷 | 旧版本证据 | 修复 | 新版本证据 |
|---|---|---|---|
| F-09 资产编号重复误报 503 | `R7-F09F10-before.txt`：重复 CREATE → `503 database unavailable` | 约束 `asset_devices_project_code`（同项目唯一）。CREATE/PATCH 先查重，并发时唯一索引兜底，映射为 `ErrDuplicateAssetCode` → 409（`code: asset_code_duplicate`）；跨项目同编号仍合法；真实数据库故障仍 503；失败不写资产/修订/审计；幂等重放与幂等键冲突语义不变 | `TestR7AssetCodeDuplicateHTTP` PASS（含 6 路并发：恰 1×201、5×409、1 行；注入触发器故障 → 503） |
| F-10 assetType 未约束 | 同一文件：`MOTOR`、`PLC`、`crane`、`ANYTHING`、空白均 201；省略时存为空串 | 唯一权威 `internal/asset.AssetTypes = CRANE/GANTRY/BRIDGE/PORTAL/JIB`（即原台账表单选项），前端 `src/cloud/asset-types.ts` 与之一致，契约测试锁定并断言与 43 种 Equipment 类型不相交。CREATE 校验，省略时为 CRANE；PATCH **仅在类型变化时**校验，历史非标准值可读、可改其他字段、可原样回传，不自动清理现场数据。编辑表单改为下拉，历史值显示为「旧类型，保持不变」 | `TestR7AssetTypeConstraintHTTP` PASS |
| F-12 断网写入暂停后隐式提交 | `R7-F12-before.txt`：4 个浏览器用例全部失败（断网点保存后恢复网络自动发出 PATCH；切换用户后仍以 alice 的 CSRF 发出；响应丢失只显示"更新失败"） | mutation `networkMode: 'always'`、不重试；API 客户端离线时不发送写请求（`NetworkWriteError sent=false`：服务器确定未变，保留输入，需手动重提）；已发送但无应答（超时/连接重置/2xx 响应体丢失）为 `sent=true`「结果未知」，资产编辑自动回读服务器版本并说明是否已生效；身份变化清空待执行 mutation；文案明确"取消请求不会撤销服务器上已完成的操作"。覆盖资产新建/编辑/移动/退役、Equipment 安装/编辑/拆卸、位置、项目登记/流转/批准、成员授权、Inventory 登记 | `R7-F12-after.txt` 4 passed；`network-write.test.ts` 5 passed |

## 2. 云边调用链审查（复用现有实现）

真实链路：`simulators/device-simulator` → `edge/agent/runner.RunFleet/RunDevice`（mock 遥测适配器、boot 计数持久化、watch 代次围栏、关键事件 spool）→ `internal/platform.Connect`（MQTT5 over mTLS，LWT 带延迟，CleanStart=false）→ Mosquitto（`use_identity_as_username`，ACL 由 `internal/fleet.ACL` 按租户/项目/设备精确生成）→ cloud `startRuntime`（Fleet 过滤、`contract.Decode` 主题/身份/类型一致性、`shadow.Merge` 按 bootSeq/bootId/sequence 排序、`alarm.Record` 按 messageId 去重并回 `EVENT_STORED_ACK`、WATCH ACK 用设备当前 boot 围栏、LIVE 校验 lease+generation）→ PostgreSQL（`devices`、`device_shadow`、`telemetry_samples` 10 s 采样、`alarms/events`、`device_commands`、`device_live_state`、`live_sessions`）→ BFF（项目范围优先的 `/devices`、`/alarms`、`/live-sessions`、`/stream` WebSocket 每秒重验会话与授权）→ `LiveViewer.ts`。

身份关系：Tenant → Project → Asset（`asset_devices`）→ RuntimeDevice（`devices`，`asset_device_id` + `source_mode`，每资产每来源唯一）→ Edge Agent（证书 CN = RuntimeDeviceId）。Equipment 实例（`asset_equipment_installations`）与 RuntimeDevice 严格分离，二者只通过 `equipment_runtime_bindings` 关联。

缺口：线上没有受控的运行身份登记手段——`devices` 只能由启动时 Inventory 导入写入，而该导入会静默创建项目/资产、删除旧 `Project-A`、不看资产状态、不写审计。**新增最小部署侧能力** `internal/runtimeidentity` + `tools/runtime-identity`（不新增 API）：仅 SIMULATION/TEST，精确 ID，同项目同租户，资产非退役，身份不可变（SIM 不能变 REAL），可选 Equipment 绑定，plan 只读、apply 单事务与 Inventory 同锁、digest 绑定目标库且只能用一次、逐条审计、verify 一致性、fleet 输出。`TestRegisterPlanApplyVerifyAndBinding`、`TestRegisterRefusals` PASS。

为让联通测试驱动生产代码，`app.Run` 中的 MQTT 运行时段抽取为 `startRuntime`（逐行搬移，无行为变化，全部既有测试通过）。

## 3. 真实联通测试（无 REST/WebSocket 拦截或预制响应）

环境：沙箱内真实 Mosquitto 2.0.11（CI 使用固定镜像 2.0.22）、PostgreSQL 18.4（CI 17.8）、未改动的 Edge Agent runner、生产 webauth BFF（OIDC 夹具 IdP，与 a1 安全测试同一夹具）、`coder/websocket` 真实连接。故障注入按设备代理（断开/拒绝）或按进程（停止/重启 cloud 运行时），均记录在 `r7-sim-evidence.jsonl`。

| 测试 | 关键实测 |
|---|---|
| `TestR7Sim1DeviceFullLoop` | TLS 身份→BIRTH→影子入库；WATCH_START 由设备 ACK（generation 1）→LIVE；WebSocket 15 帧，位置 15 个不同值，负载>0；RENEW、关闭→宽限→WATCH_STOP→SILENT；GRACEFUL DEATH；绑定资产退役被拒（ErrActiveBinding） |
| `TestR7Sim3DevicesIndependent` | 正常/告警风暴/断网各一台；仅断网设备出现 UNEXPECTED LWT，其余保持在线；告警 5 条非空、0 重复、全部归属正确；按设备筛选为空；恢复后不换 boot 重连；汇总 SIMULATION=3、REAL=0 |
| `TestR7SimSpoolAckLossNoDuplicates` | cloud 停止期间 3 条 ALARM 留在 spool（PUBACK 不出队）；重启后应用 ACK 排空；重复 messageId 0 |
| `TestR7SimMQTTAbuseAndOrdering` | 非本 CA 证书无法连接；未在 ACL 的有效证书发布返回 135；跨设备影子/告警发布 135；主题/身份不一致与非法载荷被 cloud 忽略；旧 sequence、重放、旧 bootSeq、同 bootSeq 不同 bootId 均不生效 |
| `TestR7Sim5DevicesRolesIsolationRevocation` | 两项目 5 台（含 1 台 TEST）；P1 viewer 只见 3 台；跨项目读/WATCH/WebSocket 均 404；viewer WATCH 403；两名 operator 共用一次 WATCH_START；撤权后 WebSocket 1001 ms 内以 1008 关闭，续租 404；退出后无法建流；无人续租时按 TTL 回 SILENT；汇总 SIM=2、TEST=1、REAL=0、真实在线 0 |
| `TestR7Sim20DevicesSoak`（90 s） | 20 台 1235 ms 全部上线；每 30 s 两台断网、中途 cloud 重启一次，最低在线 20；告警 35 条 0 重复；影子 20；LIVE 采样延迟 p50 3.8 ms、p95 19.2 ms、p99 26.6 ms |
| `TestR7IsolatedRestoreDrill` | 43 类型、用户授权、资产、两次移动、Equipment、审计 → `pg_dump -Fc` → 新 `crane_drill_*` 库 → 指纹（Schema、计数、审计/修订/安装哈希）完全一致；用原用户真实 OIDC 登录、历史资产与位置、43 类型可读；新安装的审计在恢复库中继续追加；源库未变 |

`-race` 下 1 台、越权/乱序、spool 三项通过，无数据竞争。

接入：`tools/r7-sim-e2e.mjs` 放在必需的 `integration` 作业内（固定 Mosquitto/PostgreSQL 镜像、精确运行 7 个必需测试、SKIP/缺失/多余即失败，产出 `R7_SIM_E2E=PASS`、`R7_ISOLATED_RESTORE_DRILL=PASS`）。九个必需作业名称与数量不变。

**60 分钟持续运行未完成**：沙箱单次命令上限约 180 秒且不保留后台进程，无法本地跑 60 分钟。已提供手动工作流 `Crane R7 SIM Soak`（minutes=60），需 OWNER 在 GitHub 触发；本报告不将其记为通过。

真实浏览器：既有 `integration` 作业（`tests/integration/run.mjs`）已用真实浏览器 + 20 台模拟器验证 LIVE、多查看者、重载关闭租约、云/边/broker 重启、LWT 等；本轮新增的 SIM 场景在 Go 层用真实 WebSocket 客户端完成，未新增浏览器用例。

## 4. 发布工件

- Dev Candidate 额外构建 `dev/device-simulator`（同一 Dockerfile 固定 TARGET，SOURCE_SHA 标签、Trivy 门禁、CycloneDX SBOM、digest、platform），写入独立 `simulator-manifest.json`；双镜像 `dev-manifest.json` 结构不变（契约测试锁定）。
- `tools/oci-transport.mjs`：按原始 digest 用 skopeo `--preserve-digests` 生成 OCI Archive，校验 manifest 字节哈希、平台、归档 sha256、revision 标签。
- 手册：`docs/08-部署运维与成本/R7-模拟器发布离线运输与运行身份登记手册.md`。

## 5. 本地回归

Go：internal 全部、services 全部、tools（equipment-types/runtime-identity/uatdata/astguard）、migrations、contracts、edge/simulators（-race）均 ok；gofmt、go vet、astguard、actionlint、codegen check 通过。前端：vitest 31 文件 235 通过；typecheck、lint 通过；premium 回归 asset-guards 6、asset-route-switch 6、location-closure 5、portfolio-intake 8、inventory-registration 15、portfolio-grants 10、identity-isolation 5 全部通过。契约测试 72/72（不含需 nginx 的 web-nginx）。本地未运行：B4 真实 Keycloak 浏览器套件、integration Compose、security 扫描、staticcheck（磁盘不足），以 CI 为准。

## 6. 剩余缺口

- 60 分钟 20 台 soak 待手动工作流执行（P2，不阻断）。
- 模拟器 OCI 运输的目标主机导入路径未在服务器演练（首次使用只导入核对）。
- 资产时间线读取/展示仍为已知 P2（R6 遗留）。

## 7. 状态

```
BASE_R6_SHA=a2a89ea711ae7425906185cd8421f1f046d82f6c
R7_BRANCH=feature/crane-r7-sim-e2e
R7_HEAD_SHA=见提交后 git rev-parse HEAD（本报告所在提交）
F09_OLD_FAIL_NEW_PASS=YES
F10_OLD_FAIL_NEW_PASS=YES
F12_OLD_FAIL_NEW_PASS=YES
RUNTIME_BINDING_E2E=PASS
MQTT_REAL_TLS_ACL=PASS
MQTT_SHADOW_DB=PASS
MQTT_LIVE_WATCH_ACK=PASS
REAL_WEBSOCKET_E2E=PASS
ALARM_NONEMPTY_E2E=PASS
OFFLINE_SPOOL_ACK=PASS
DUPLICATE_EVENT_COUNT=0
CROSS_PROJECT_ISOLATION=PASS
REVOKED_STREAM_CLOSED=PASS（1008，约 1 s）
REAL_SOURCE_MISCOUNT=0
SIM_1_DEVICE=PASS
SIM_3_DEVICES=PASS
SIM_5_DEVICES=PASS
SIM_20_DEVICES_60MIN=NOT_RUN（90 s 版 PASS；60 min 待手动工作流）
LATENCY_P95=19.2 ms（20 台，Edge SentAt→Cloud 接收）
ISOLATED_RESTORE_DRILL=PASS
DATABASE_SCHEMA=12
CI_RUN_ID=待 OWNER 推送
CI_ATTEMPT=—
CI_RESULT=PENDING
OPEN_P0=0
OPEN_P1=0
WORKTREE_CLEAN=YES（仅既有未跟踪的私有文件，未提交）
PUSH_STATUS=NOT_PUSHED（沙箱无 GitHub 凭据，需 OWNER 推送）
```
