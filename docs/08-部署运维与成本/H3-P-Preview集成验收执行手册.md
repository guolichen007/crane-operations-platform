# H3-P Preview 集成验收与发布候选：执行手册（G0–G5）

> **已被取代（D1，2026-11-10）**：OWNER 取消独立 b4 公网 Preview，开发验收改在 `https://crane.youdaozd.com` 进行。现行流程见《D1-单域名开发发布实施与验收报告.md》。下文的 `preview-candidate` 工作流已删除，由 `dev-candidate` 取代；`uatdata` 守卫和清理逻辑已改写。本文只保留 G0 证据与历史记录。

> 分支：`feature/h3-preview-candidate`（基于 `d0f309e`，只做 Preview 准备，不改业务代码）。
> 边界：不合并 main，不替换 `crane.youdaozd.com`，不连接生产库、生产 Keycloak 或生产 MQTT，不建 00013，不做 R4，不重做 Three.js。
> 角色：CC 负责本地提交，OWNER 负责推送、打标签和批准服务器变更。服务器只读预检由 OWNER 或服务器 CC 执行。独立审查方负责封板。

## G0 H2-1D 集成封板证据（2026-11-09，只读 GitHub API）

| 项 | 结果 |
| --- | --- |
| 远端分支 HEAD | `d0f309e7278417570a8b779ea32451fc77cae756`，与本地一致，普通快进推送 |
| CI | Run `42584696398`（push，同一 SHA），9/9 必需作业 SUCCESS，`production-release` 为 SKIPPED（功能分支不发布） |
| integration | 08:16:08–08:21:11，其中 integration-test 步骤 2m23s（上一轮 637be0b 在 25 分钟处超时），不再长期挂起 |
| r2-db-test | `node tools/r2-b2-test.mjs` 步骤 SUCCESS（1m14s）。门禁对 16 项必过测试做精确 PASS 判定，SKIP 或缺失均判失败。原始日志返回 403，未逐行核对 |
| b4-business-acceptance | 本地构建生产候选镜像，跑真实 Keycloak + PostgreSQL。注解为 `32 passed (4.0m)` 和 `3 passed (20.0s)`，较上一轮 31 项多 1 项，即首页账号隔离用例 |
| frontend | typecheck/lint/unit/build + e2e 全 SUCCESS（含 H2-1D、H1 回归、五分辨率、H0-D2） |
| main | `29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18` 未变；仓库 deployments 为 0 |
| 证据 artifact | `premium-ui-visual-d0f309e…`（31.9 MB）、`b4-business-evidence-d0f309e…`（3.3 MB），保留至 2027-02-07 |

结论：`H2-1D_REMOTE_CI=PASS (9/9)`。H2-1C 封板和 H2-1D 封板由独立审查方裁定。

## G1 服务器只读预检

脚本：`deploy/scripts/h3-preview-precheck.sh`。脚本里没有 up、down、rm、restart、写配置或连数据库的命令，由 `tests/contract/preview-candidate.test.mjs` 做静态守卫。执行命令：

```bash
git fetch origin && git checkout <本分支 SHA> -- deploy/scripts/h3-preview-precheck.sh   # 或直接复制该文件
bash deploy/scripts/h3-preview-precheck.sh > h3-precheck-$(date +%Y%m%dT%H%M%S).txt 2>&1
# 可选：已准备 Preview env 文件时，额外做只读 compose config（不启动容器）
PREVIEW_ENV_FILE=/srv/u-dao/secrets/crane-b4-preview/b4-preview.env bash deploy/scripts/h3-preview-precheck.sh
```

输出贴回后，CC 按下表给出 G1 结论。任意一项 BLOCK 都不进入 G2 部署。

| 检查 | 通过 | 阻塞 |
| --- | --- | --- |
| 资源 | 可用内存 ≥ 2.6 GiB，根分区可用空间 ≥ 10 GiB | 低于 Preview 预算（约 2.5 vCPU / 2.3 GiB） |
| 生产健康 | `u-dao-crane-ops` 容器 healthy，镜像为 digest 而非 latest | 生产不健康（先处理生产，不做 Preview） |
| 端口 | 18083/18085 空闲，或只被 `crane-b4-preview` 占用并绑定 127.0.0.1 | 被其他项目占用，或绑定到 0.0.0.0 |
| 既有 Preview | 无 `crane-b4-preview` 容器（另建），或现有实例归属可证明（复用前由 OWNER 确认没有其他任务在用） | 来源不明、正被他人使用：不覆盖、不停止、不删除 |
| DNS/TLS | `b4.crane.youdaozd.com` 有解析，证书 SAN 覆盖该域名且未过期 | 无解析或无证书，需 OWNER 先加 CNAME 并签发独立证书 |
| 入口代理 | `server_name b4.crane.youdaozd.com` 只反代到 127.0.0.1:18083/18085，`X-Forwarded-*` 与受信 CIDR 正确 | 反代到生产端口，或与生产共用 upstream |
| 卷 | Preview PG 数据目录来自 `crane-b4-preview_b4-preview-postgres` | 挂载 `crane-postgres`、`crane-mqtt` 或 `/srv/u-dao/secrets/crane-ops` |
| 网络 | 只有入口代理同时连 Preview 和生产前端网络；10.240.40.0/24 未冲突 | 非入口容器跨网，或子网冲突 |
| Keycloak | `https://b4…/realms/crane-preview` 的 issuer 精确匹配；随后用 `provision-keycloak --preview-precheck` 核对 client | issuer 指向 `crane` realm，或 client 漂移 |
| 密钥 | Preview 的 session、OIDC、DB 密钥哈希前缀与生产不同 | 任一密钥与生产相同 |

G1 还需记录 Preview PostgreSQL 的 `system_identifier`，供 G3 的 `uatdata --expect-system-identifier` 使用。读取方式只有 `SELECT system_identifier FROM pg_control_system()`，在 Preview 的 postgres 容器内执行，只读。

**G1 完成前禁止**：compose up/down，修改 Nginx、证书、网络、数据库或生产环境。

## G2 Preview Candidate（可追溯镜像）

工作流：`.github/workflows/preview-candidate.yml`，脚本：`tools/preview-candidate.mjs`。

1. 触发方式：OWNER 对一个**已经九项全绿**的 SHA 打标签 `preview/h3-<名称>` 并推送，例如 `git tag -a preview/h3-rc1 d0f309e -m "H3 Preview RC1" && git push origin preview/h3-rc1`。标签推送不会触发 `ci.yml`，也不会运行 production-release。
2. 工作流第一步用只读 API 核对同一 SHA 最新的绿色 `Crane Platform CI` 中九项必需作业全为 SUCCESS，不满足就失败。
3. 对两个镜像依次执行：构建（`SOURCE_SHA` 标签）→ 校验 `org.opencontainers.image.revision` 等于该 SHA → Trivy 扫描，HIGH/CRITICAL 非 0 即失败 → 生成 CycloneDX SBOM → 推送 `ghcr.io/<repo>/preview-cloud-api:<完整SHA>` 和 `preview-web-console:<完整SHA>` → 读取仓库返回的 digest。
4. 产物（artifact `h3-preview-candidate-<sha>`，保留 90 天）：`preview-manifest.json`（源码 SHA、CI run、两个 digest、SBOM 哈希、Go/Node/PG/Keycloak/Mosquitto 版本）、`preview-images.env`（`CRANE_CLOUD_IMAGE=…@sha256:…`）、SBOM、Trivy JSON，以及 `uatdata-linux-amd64` 及其哈希。
5. 生产镜像名 `cloud-api`、`web-console` 和 `tools/release.mjs` 都不改动，验收不使用 `latest`。

**分发与回滚**：服务器按 digest `docker pull` 拉取镜像（只拉 Preview 名下的镜像）。env 文件每次部署前另存为 `b4-preview.env.<上一SHA>`。回滚步骤为：先确认有 PG 快照，再把 env 恢复成上一组 digest，然后 `up -d` 只针对 `cloud-api web-console`。这一步属于服务器变更，需 OWNER 批准。不用 `down -v`。

**环境配置清单（不含密钥）**：项目 `crane-b4-preview`；卷 `b4-preview-postgres`、`b4-preview-mqtt`；realm `crane-preview`，client `crane-b4-preview`；端口 127.0.0.1:18083/18085；网络 `u-dao-crane-b4-preview-frontend`、`-api-frontend` 以及内部 backend；密钥目录 `/srv/u-dao/secrets/crane-b4-preview/`；`CRANE_DEPLOYMENT_ENVIRONMENT=b4-preview`。隔离要以 G1 中 `docker compose config --format json` 加 `node tools/b4-preview-contract.mjs --resolved` 的结果，以及容器挂载和网络的实际证据为准。

## G3 合成验收数据（`tools/uatdata`）

只写入 UAT 命名空间：项目、资产、运行身份以 `UAT-` 开头，用户以 `uat-` 开头。不导入任何生产信息。

**三道 Preview 守卫，任一不满足即拒绝写入**：`--issuer` 必须精确等于 `https://b4.crane.youdaozd.com/realms/crane-preview`；库中已绑定的用户不得有非 Preview issuer（生产库或混合库会被拒绝）；`--expect-system-identifier` 必须等于所连集群的 `pg_control_system()`，取 G1 记录的值。

```bash
# 在 Preview 主机的 compose 网络内执行（G2 artifact 中的二进制，哈希见 manifest）
uatdata plan
uatdata seed    --issuer https://b4.crane.youdaozd.com/realms/crane-preview --expect-system-identifier <G1> --subjects-file subjects.json
uatdata refresh --issuer … --expect-system-identifier <G1>     # 长时间 UAT 时把相对时间平移到"现在"，不重绑用户
uatdata verify  --issuer … --expect-system-identifier <G1>     # 与真实 operations.Summary 比对 14 组期望
uatdata cleanup --issuer … --expect-system-identifier <G1> --confirm-cleanup
```

`subjects.json` 是 `uat-admin` 等用户 ID 到 Preview Keycloak 中**已存在**用户的 `sub` 的映射。Keycloak 用户由 OWNER 在 Preview realm 中创建，工具不读管理员密码。seed 每次都会先清理再重建，可重复执行，结果确定；cleanup 只删 UAT 命名空间，遇到外键未知引用时整体回滚，audit_logs 保留。

**身份**：`uat-admin` 只在 UAT-P001 上是 platform_admin；`uat-biz` 只在 UAT-P002 上是 operator；`uat-zero` 零授权；`uat-geo` 是多层级查看者（P001/P003/P004/P005）；`uat-bulk` 只看 130 条待复核；`uat-revoke` 是撤销测试对象（初始为 P001 viewer）。

**覆盖矩阵与期望值（默认 stale 2100s、skew 120s，24h 窗口）**：

| 情形 | 数据 | 期望（服务端 summary） |
| --- | --- | --- |
| ACTIVE / DRAFT / OUT_OF_SERVICE / RETIRED | A01…A12、A06 DRAFT、A07 OOS、A08 RETIRED | uat-admin：registered 11（不含 RETIRED）、draft 1、outOfService 1 |
| REAL 在线 / REAL 离线 / SIM 在线 / 同时 REAL+SIM | A01 在线、A03 REAL 离线 + SIM 在线、A04 仅 SIM | real.connected = 2（A01、A02），A03 不计入；offline 1；bound REAL 8 / SIM 2 / TEST 1 |
| 无运行身份 | A05、B03 | noRuntime：admin 1、biz 1 |
| 无效观测时间 / 过期 / 未来时间 | A09 `not-a-timestamp`、A10 3h 前、A11 +1h | unknownTime 1，stale 2（偏移对所有合法配置都成立） |
| 待复核 | A06（DRAFT 在线）、A07（OOS 在线告警）；P006 有 130 条 | pendingReview 2（A07 alarm=true）；uat-bulk pendingReview 130，列表上限 100（R001…R100） |
| 当前 REAL 告警 vs 历史记录 | A02 当前告警，共 150 条历史 | alarm.devices.REAL 1，records：1h 17 / 6h 37 / 24h 77 / 7d 137；futureSkew 2；退役 A08 的记录不计入 |
| 孤儿运行身份 | UAT-ORPH-P001 | REAL unattributed 2 |
| 省 / 市 / 区县 / 无位置 | P003 浙江省本级、P005 杭州、P001 海宁、P004 无编码 | uat-geo 全国：unlocated 1；浙江省：atScopeLevel 1，regions 杭州 1 + 嘉兴 11；嘉兴：海宁 11 |
| 跨账号隔离 | P002 资产与 P001 同名（"1号行车"） | uat-admin 看广东为 0，uat-biz 看浙江为 0；零授权为合法 0 |

本地真实 PG17 验证（`go test ./tools/uatdata`，已纳入 r2-db-test 门禁 `h3-uatdata`）：seed→verify PASS、重复 seed 不产生重复数据、refresh 后仍 PASS、cleanup 只清 UAT。反证一：把 A03 改为 REAL 在线，verify 必须报 `real.connected got 3 want 2`。反证二：给 uat-admin 授予 P002，verify 必须报广东 `projects got 1 want 0`。反证三：生产 issuer、带尾斜杠、缺 system id、其他集群、混合 issuer 库都会被守卫拒绝。另做过变异：把期望值改错一处，verify 会精确报出该项。

## G4 UAT 执行清单

每条记录写明执行环境（服务器、浏览器、GPU）、时间、截图或 HAR、API 原始响应与结论。没跑的标 NOT_RUN。

**UAT-A 账号与权限（P0）**：A1 uat-admin 看不到 UAT-P002 的项目、设备及同名"1号行车"；A2 platform_admin 不绕过授权（请求广东时 projects=0）；A3 uat-zero 的 KPI 显示合法 0，不是错误；A4 同一浏览器先登录 uat-admin、退出后再登录 uat-zero，逐项检查 KPI、地图、Inspector、选中态、事件列表和 URL 历史（后退）不残留；A5 uat-revoke 登录中由宿主机执行 `identity revoke --id uat-revoke --project UAT-P001 --actor <owner>`，记录下次刷新和重新登录后的表现；A6 撤销前发出的请求或缓存不得在撤销后恢复数据；A7 直接打开 `/#/assets/UAT-B01` 应被拒绝。任一跨账号残留归 P0，修复后整条 A 链路重跑。

**UAT-B 运营统计（P0）**：浏览器 KPI 必须逐项等于 `GET /api/v1/operations/summary`（同用户、同区域、同窗口），API 又等于 G3 期望表（用 `uatdata verify` 交叉验证）。逐项验证：登记数与资产中心一致；A03 不增加 REAL 在线；当前告警与历史记录分开显示；1h/6h/24h/7d 四个窗口；无运行身份与离线分开；待复核；失败时不显示为 0（临时让 API 返回 503 或断网）；首次加载失败与同范围刷新失败的表现不同；浙江→广东→浙江快速切换不串数据；sampledAt 与浏览器更新时间的展示。

**UAT-C 地图与业务路径（P1）**：默认三维和二维备用视图；全国→省→市→区县钻取；Inspector、抽屉、前进后退；KPI 跳转资产中心；非精确列表的说明；待复核前 100 条可以打开有授权的详情；无位置资产和本级资产不被错误分配；索引版本不一致的提示；WebGL 失效时转入二维。KPI 与地图标记数量不同时，只用数据来源、授权范围和时间差解释，不改后端数字。

**UAT-D 视觉与性能（P1）**：五个分辨率 1280×720、1366×768、1440×900、1920×1080、2560×1440；1280 和 1366 下地图净可见面积 ≥45%；无横向溢出；真实 GPU 的 FPS、帧耗时、Draw Call（`window.__perfRecorder()`）；2D 和 3D 首屏加载时间；summary 在 10 和 30 并发下的 p50/p95、CPU、内存、DB 连接、503 情况（`tools/perf/operations-summary`，只指向 Preview）；断网恢复；DB 短暂不可用时的提示。所有结果标注具体服务器和浏览器环境。

**UAT-E 备份、故障与恢复（P0）**：Preview PG 备份并恢复到隔离库后 `uatdata verify` 仍 PASS；镜像回滚到上一组 digest；旧镜像与当前 schema（v12）兼容；Keycloak 会话恢复；回滚过程中所有连接串只指向 Preview；Preview 停止、重建、恢复期间生产 `/healthz` 持续 200。以上每一步都是服务器变更，需 OWNER 逐项批准。

## G5 发布候选评审输出

HTTPS 地址；源码 SHA 与两个 digest；构建、Trivy 与 SBOM；隔离记录（G1 加 resolved compose）；Keycloak + PG 验收报告；五分辨率截图与 GPU 性能；KPI 与 API 对照；账号切换、撤销与越权反证；备份恢复与回滚记录；P0/P1/P2 清单（P0 必须为 0）；Go/No-Go 结论。正式发布由 OWNER 在 H3 验收后另行批准。

## 当前状态

| 阶段 | 状态 |
| --- | --- |
| G0 | 远端 CI PASS（9/9）。封板待独立审查 |
| G1 | 脚本已就绪，服务器执行 NOT_RUN（沙箱没有服务器访问权限） |
| G2 | 工作流已就绪，NOT_RUN（需 OWNER 推送本分支并打 `preview/h3-*` 标签；本分支的新提交须先通过九项 CI） |
| G3 | 工具和本地真实 PG 测试已就绪；Preview 上 NOT_RUN |
| G4/G5 | NOT_RUN（依赖 G1 批准和部署） |
