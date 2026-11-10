# 36 G4-S1A Preview 离线安全实现与验收

本批只交付可部署到独立 Preview 的代码、配置和测试，没有部署任何环境。

| 项 | 值 |
| --- | --- |
| BASE_SHA | `df2c750e2c1004fca79a7171c6e1aab2ab7f5370`（Run `42755710072`，Attempt 1，8/8 success，经 GitHub API 核对） |
| HEAD_SHA / CI | `6e0bbda18457c2ff68aaed15853a99730e4be824`，Run `42797483906`，Attempt 1，8/8 success（compose job 实际执行 Docker 渲染与 Preview nginx -t） |
| PREVIEW_DEPLOYED | NO |
| PRODUCTION_DEPLOYED | NO |

**未触碰的范围：**

- main/R2、服务器、生产数据库、Keycloak、共享 Ingress、DNS、证书、发布流程；
- PR、tag、release 和 GHCR。

**环境中断。** 上一轮写完代码后，沙箱 VM 启动失败（"Download failed"），代码未编译、未提交。本轮（G4-S1A-R1）先在 Gate 0 保全了全部改动：

- 已跟踪文件的 diff；
- 13 个未跟踪的新文件（tar 包）；
- 文件清单与 SHA256，以及 BASE_SHA。

保全包位于 `local-private/baobei/g4s1a/preserve-20261110-1631/`（另存一份在会话输出目录），不含 secret。之后才在沙箱中编译、测试并修正。

## 1. 改动清单（相对 df2c750）

| 文件 | 内容 |
| --- | --- |
| `services/project-intake-api/environment.go`（新） | `deployedProfiles`：production 与 preview 各自的 origin/issuer/client 精确白名单；`deployed()` 和 `knownEnv()`；`verifyDatabaseEnvironment()`（环境标记）；`parseStaffConfig()`（独立 staff DSN，fail closed） |
| `main.go` | 环境白名单改为 development/production/preview。凡是 production 走的分支，preview 一律同走（`search_path=public`、staff 连接、角色校验）。服务模式在连接数据库和 OIDC 之前先要求独立 staff DSN。staff 连接还要核对环境标记 |
| `database.go` | `prepareDatabase`：未知环境直接拒绝。部署环境先核对角色，再核对环境标记，最后才迁移或校验 schema |
| `auth.go`、`auth_browser.go` | `__Host-` Cookie 名和 `Secure` 改由 `deployed()` 判断，preview 与 production 行为相同 |
| `staff.go` | `staffPool()` 只在 development 回退到 runtime 连接；production、preview 和任何未知值都返回 nil（fail closed） |
| `deploy/baobei/db/preview-environment.sql`（新） | Preview 专用初始化：`baobei_environment` 单行表，CHECK 约束只允许 `'preview'`。四个应用角色只有 SELECT 权限 |
| `deploy/baobei/compose.preview.yaml`（新） | 独立项目 `baobei-preview`，服务、网络、卷、config、secret 全部使用 preview 名字，没有宿主端口；镜像只接受 digest，PostgreSQL 与生产使用同一 digest；API 有 healthcheck |
| `deploy/baobei/nginx.preview.conf`、`nginx.proxy.preview.conf`（新） | Preview web 配置，限流区独立命名，加 `X-Robots-Tag: noindex`；代理头策略与生产逐字相同 |
| `deploy/baobei/ingress/baobei-preview.youdaozd.com.conf.example`（新） | 共享 ingress 的示例 vhost：独立 zone 和证书路径，默认 `deny all`（需填入批准的 CIDR）。**未安装** |
| `deploy/baobei/preview/identity.example.json`（新） | Preview 合成身份清单示例，供 `maintenance identity-apply` 使用；subject 需替换为 Preview realm 的真实 sub |
| `tests/baobei/preview.mjs`（新） | Preview 静态门：拓扑、隔离、secret 文件、digest、OIDC、healthcheck 和 Nginx 名称检查，42 个负例，加生产/Preview 交叉拒绝 |
| `tests/baobei/production-gate.mjs`（新）、`production.mjs` | `validateProduction` 原样搬出（diff 验证逐字相同），production.mjs 改为导入，规则和负例都没有变 |
| `tests/baobei/task.mjs` | CI 的 compose 任务增加运行 preview.mjs，新增 `preview-check` |
| `tests/baobei/db-privileges.mjs` | CI 的 db-test 在第二个全新 PG 实例上运行 `TestDatabasePreviewEnvironment`，并断言 Preview 与生产使用同一 PG digest |
| `tests/baobei/backup.mjs` | 按 `BAOBEI_BACKUP_ENV` 区分生产与 Preview 的表清单和环境标记（第 4 节）；新增仅限回环地址的本地工具模式，用于离线验证 |
| `tests/baobei/closure.mjs` | 生产 closure 门只读生产 ingress 文件，排除 Preview 示例（它由 preview.mjs 检查），规则不变 |
| `tests/baobei/preview/seed.mjs`（新） | Preview 合成数据脚本（第 6 节） |
| `apps/project-intake-web/src/Dashboard.vue` | 金额面板刷新失败时隐藏旧金额（第 7 节） |
| `tests/baobei/e2e/g4s1a-amount-refresh.spec.ts`（新） | 金额刷新的确定性回归测试 |
| `services/project-intake-api/environment_test.go`、`environment_db_test.go`（新） | 单元与 PG 负例 |
| `tests/baobei/uat/四角色人工UAT脚本.md` | 新增跨角色顺序和依赖表；S-15 改为先新建独立草稿；A-08 增加指派前后的可见范围对比；判定栏仍为空 |

**未改动**（相对 df2c750 的 diff 为空）：

- 生产部署与配置：`direct-deploy.py`、`compose.production.yaml`、`compose.direct.yaml`、`nginx.production.conf`、`nginx.proxy.production.conf`、生产 ingress 示例；
- 数据库：`initialize.sql`、`staff-role.sql`、`database-grants.sql`、全部迁移；
- CI：`.github/`；
- 业务代码：金额算法、聚合 SQL、权限策略。

完整 diff 见提交本身（`git show <HEAD>`）。

## 2. 环境矩阵

| 环境 | API 服务 | maintenance migrate | identity-apply | staff-provision | protection-sweep |
| --- | --- | --- | --- | --- | --- |
| development | 本地自动迁移；dev 登录只允许回环地址；staffPool 回退到 runtime（仅限一次性合成库） | 同左（自动迁移） | 同左 | 同左 | 同左 |
| production | origin/issuer/client 必须精确等于生产值；`DEV_AUTH=0`；必须提供独立 staff DSN（同一 host/port/db）；`__Host-` Secure HttpOnly；runtime 角色；拒绝带 Preview 标记的库 | 必须是 migrator 角色；拒绝带 Preview 标记的库（不迁移） | 必须是 maintenance 角色 | 必须是 maintenance 角色 | 必须是 runtime 角色 |
| preview | 与 production 同一路径，只换成 Preview 白名单；必须存在且只存在 `preview` 标记 | 必须是 migrator 角色，且标记正确后才迁移 | 必须是 maintenance 角色，且标记正确 | 同左 | 必须是 runtime 角色，且标记正确 |
| 其他值 | 拒绝启动 | 拒绝 | 拒绝 | 拒绝 | 拒绝 |

## 3. 安全负例（实际执行）

### 3.1 Go 单元（`environment_test.go`，race）

- **配置白名单**：`TestPreviewConfigGate` 断言两组白名单的三个字段互不相同。preview 拒绝以下 9 种配置：`DEV_AUTH=1`、生产 origin、生产 issuer、生产 client、http 协议的 origin、origin 末尾带 `/`、issuer 末尾带 `/`、空 client、空 secret。production 拒绝 preview 的 origin/issuer/client。`APP_ENV` 取 `""`、`Preview`、`staging`、`prod`、`preview `（尾随空格）时都被拒绝。
- **Cookie 与 staff 连接**：`TestPreviewHardenedBehaviour` 断言 production 和 preview 的 Cookie 都是 `__Host-baobei_session` / `__Host-baobei_login`，带 `Path=/`、`HttpOnly`、`Secure`、`SameSite=Lax`，且没有 `Domain`。production、preview 以及三个未知环境值下，`staffPool()` 都不会回退到 runtime 连接；development 行为不变。
- **staff DSN**：`TestPreviewStaffDSNRequired` 对以下 5 种情况全部拒绝：缺失、无法解析、主机不同（包括生产主机名）、端口不同、库名不同。
- 原有 `TestProductionConfig` 和 `TestProductionConfigGate` 不变，仍然通过。

### 3.2 PostgreSQL 18.4（`environment_db_test.go`，生产镜像二进制，用 initialize.sql 与 staff-role.sql 初始化的全新库）

| 场景 | 结果 |
| --- | --- |
| 没有标记，preview migrate | 返回 `errEnvironmentMismatch`；`baobei_migrations` **没有被创建**（没有发生迁移） |
| 没有标记，preview staff 连接 | 拒绝 |
| 没有标记，production | 接受（生产库无需写入标记） |
| 角色错误：runtime 执行 migrate、migrator 或 staff 或 bootstrap 跑 API、bootstrap 执行 migrate | 全部拒绝，并且都没有迁移 |
| 写入标记后：preview migrate、runtime 启动、staff 连接 | 通过 |
| 带 Preview 标记的库，production 的 runtime 启动、migrate、staff 连接 | 全部拒绝 |
| `APP_ENV=staging` | 拒绝 |
| runtime 与 staff 尝试 UPDATE、DELETE、DROP 标记表 | 全部拒绝 |
| bootstrap 尝试把标记改成 production、插入第二行、插入 singleton=false | 被 CHECK 和主键拒绝 |

**变异检验。** 为证明上述测试能发现回归，我逐个故意破坏代码后重跑：

| 故意破坏 | 结果 |
| --- | --- |
| M1：preview 不再强制要求标记 | DB 测试 FAIL |
| M2：`prepareDatabase` 去掉环境标记检查 | DB 测试 FAIL |
| M3：先迁移、后检查标记 | DB 测试 FAIL |
| M4：`staffPool` 对 preview 回退到 runtime | 单元测试 FAIL |

每项检验后都已恢复代码。原有的 `TestDatabaseProductionPrivileges` 在同一 fixture 上仍然通过。

### 3.3 实际二进制（`local-private/baobei/g4s1a/evidence/command-matrix.txt`）

用本批构建的 API 二进制直接启动，结果如下：

| 场景 | 结果 |
| --- | --- |
| A1：`APP_ENV=staging` | 拒绝 |
| A2：preview 且 `DEV_AUTH=1` | 拒绝 |
| A3–A6：preview 使用生产 origin、issuer、client，或缺少 secret | 拒绝 |
| A7：preview 缺少 staff DSN | 拒绝 |
| A8：preview 的 staff DSN 指向另一主机 | 拒绝 |
| A9：production 使用 Preview origin | 拒绝 |
| A10：production 缺少 staff DSN | 拒绝 |
| A11：缺少 DATABASE_URL | 拒绝 |

在两个全新的 PG18 库上执行 maintenance 命令（数字为执行后 public 下的表数）：

**未加标记的库：**

| 命令 | 结果 | 表数 |
| --- | --- | --- |
| C1 preview migrate | 拒绝 | 0 |
| C2 preview identity-apply | 拒绝 | 0 |
| C3 production migrate | 通过 | 30 |
| C4 preview migrate（已迁移的生产形态库） | 拒绝 | 30 |

**带 Preview 标记的库：**

| 命令 | 结果 | 表数 |
| --- | --- | --- |
| C5 production migrate | 拒绝 | 1（未迁移） |
| C6 preview migrate，使用 runtime 角色 | 拒绝 | 1 |
| C7 preview migrate，使用 migrator 角色 | 通过 | 31 |
| C8 再次执行 C7 | 通过（幂等） | 31 |
| C9 identity-apply，使用 runtime 角色 | 拒绝 | 31 |
| C10 production protection-sweep | 拒绝 | 31 |
| C11 preview protection-sweep | 通过 | 31 |
| C12 staff-provision，使用 bootstrap 角色 | 拒绝 | 31 |

**结论：** 所有错误配置都在迁移和业务修改之前被拒绝。生产形态库的迁移与命令行为保持不变。

## 4. 备份与恢复（P1）

`backup.mjs` 默认按 production 执行，原有严格清单（30 张表、仅 public schema）不变。设置 `BAOBEI_BACKUP_ENV=preview` 时：

- 表清单为 30 张 + `baobei_environment`，必须完全一致；
- 标记必须恰好是 `['preview']`；
- manifest 记录 `environment` 和 `environment_marker`；
- 恢复时要求备份所属环境与当前设置一致；
- 恢复到独立的临时库后，核对表清单、标记、每张表的行数、迁移记录、扩展、外键有效性和三组关键引用关系。

在 PG18.4 上实际执行（`evidence/backup-restore.txt`；本地用回环地址上的客户端工具代替 `docker exec`）：

| # | 场景 | 结果 |
| --- | --- | --- |
| B1 | 生产形态库：备份并恢复校验 | BACKUP=PASS；RESTORE_TEST=PASS，environment=production，30 张表 |
| B2 | 对生产库按 preview 备份 | 拒绝（表清单不符） |
| B3 | 把生产备份按 preview 恢复 | 拒绝（环境不一致） |
| B4 | 生产库多一张未知表 | 拒绝 |
| B5 | 生产库多一个 schema | 拒绝 |
| B6 | Preview 库：备份并恢复校验 | BACKUP=PASS；RESTORE_TEST=PASS，environment=preview，31 张表；manifest 含标记 `['preview']` |
| B7 | 对 Preview 库按 production 备份 | 拒绝（表清单不符） |
| B8 | 把 Preview 备份按 production 恢复 | 拒绝 |
| B9 | Preview 库多一张未知表 | 拒绝 |
| B10 | 标记被改为 production（先删除 CHECK 约束） | 拒绝（“不是 Preview 库”） |
| B11 | `BAOBEI_BACKUP_ENV=staging` | 拒绝 |

CI 的 compose job 照旧跑 `restore-test`（生产默认路径）。

## 5. Preview 配置静态门（`tests/baobei/preview.mjs`）

| 检查 | 本地 | CI |
| --- | --- | --- |
| 拓扑与隔离规则（项目名、服务名、网络、卷、secret、镜像、healthcheck、init 脚本、Nginx config） | PASS（基于对原文件做本地变量插值的结果） | 用 `docker compose config` 渲染后再验，CI 中渲染失败就判失败 |
| 缺少必需变量（web 镜像、api 镜像、secret 目录）时拒绝 | PASS（3/3） | 同 |
| 负例 | **42/42 被拒绝**（每项的具体断言信息见 `evidence/preview-negatives.txt`） | 同 |
| 生产门拒绝 Preview 配置，Preview 门拒绝生产配置；生产 compose 不含 preview 字样 | PASS | 同 |
| Nginx 名称隔离，代理头与生产一致，ingress 示例独立 zone、证书和 deny all | PASS | 同 |
| `direct-deploy.py` 不含 preview（生产部署脚本不变） | PASS | 同 |
| Nginx 容器语法 `nginx -t` | **NOT_RUN**（本地无 Docker） | 在 compose job 中执行，CI 中失败就判失败 |
| 生产门 `production.mjs`（compose 渲染与 nginx 语法） | 本地无 Docker，未运行；validateProduction 已确认与 df2c750 逐字相同 | compose job 执行 |

42 个负例覆盖：

- **端口与命名空间**：api、pg、web 发布宿主端口，host 网络模式；
- **项目、服务、网络、卷**：生产项目名、生产服务名、加入生产 internal 网络、internal 网络改名为生产名、internal 设为 external、internal 不再 internal、pg 接入 egress、api 接入 frontend、frontend 改名为生产名、生产卷、external 卷、卷改名为生产名；
- **挂载**：web 挂载 acme 目录，api 挂载生产 secret 目录；
- **应用环境**：APP_ENV 为 production 或 development、DEV_AUTH=1、生产 origin、issuer、client；
- **secret 注入**：明文 client secret、明文 DATABASE_URL、缺少 staff DSN、删除 staff secret、runtime 改用 migrator DSN、使用生产 secret 目录、external secret；
- **镜像**：tag 镜像、其他仓库的镜像、未固定 digest 的 PG；
- **初始化与健康检查**：缺少标记初始化脚本、初始化脚本可写、删除 API healthcheck、healthcheck 端口错误；
- **加固**：使用生产 Nginx 配置、privileged、根文件系统可写、container_name。

**API healthcheck 与镜像一致。** `api.Dockerfile` 的运行镜像是 `alpine:3.23`。我检查了该镜像的 amd64 层（从 Docker Hub 下载），`/usr/bin/wget` 是指向 busybox 的软链接，所以 `wget -qO- http://127.0.0.1:8088/health/ready` 在运行镜像中可执行。`/health/ready` 会检查两个连接池（api.go:24）。

## 6. 合成数据（`tests/baobei/preview/seed.mjs`）

| 要求 | 证据 |
| --- | --- |
| 只能用于明确指定的 Preview | OIDC 模式只接受 `https://baobei-preview.youdaozd.com`，并要求目标的 `/auth/config` 为非 development 且启用 OIDC；dev 模式只接受回环地址的 http 并要求 dev 登录。实测拒绝：生产 origin、内网 IP 加 dev、Preview origin 加 dev、回环地址加 oidc、未知模式 |
| 使用真实 OIDC | Preview 使用 Keycloak 登录表单和 Preview realm 的合成账号（`BAOBEI_SEED_ACCOUNTS_FILE`，存放在 secret 目录）；不使用 DEV_AUTH |
| 不硬编码凭据 | 脚本中没有密码、Cookie 或 Token，凭据只从外部文件读取 |
| 只经真实 API | 所有写入都走 HTTP API，受权限、审计和审核独立性约束；没有直接 SQL |
| 覆盖范围 | 四角色；12 个阶段；已知、未知、零金额；批准一条、退回一条；跨组重复一条（UNDER_REVIEW，等待总监）；待分配草稿一条；一次阶段推进 |
| 第二次运行不重复 | 本地合成栈实测：第 1 次创建 18 个项目，第 2、3 次输出“tag already present … nothing written”。前后项目数（18）、待审数（3）、`max(updated_at)`、`audit_events` 数（31）完全相同（`evidence/seed-idempotent.txt`） |
| 不修改非本次生成的数据 | 只创建带 `合成UAT·` 前缀的新项目；审核、退回和阶段推进只作用于本次创建的 ID；库中非本标记项目数为 0 |
| 本地运行的范围视图 | 销售 A 15 项、主管 16 项（可审 2）、总监 17 项（可审 3）、管理员 18 项（含待分配 1） |

**真实 OIDC 运行：NOT_RUN。** Preview realm 和 Preview 环境都还不存在。

## 7. 金额面板刷新失败

**修正内容。** 只有在当前快照加载成功（`stageState === 'ready'`）时才显示金额和“阶段与金额统计时间”。刷新失败时显示“本次刷新失败”，并写明“上次成功统计（时间）的金额已隐藏，避免被误认为当前结果”；阶段条也不显示。重试成功后显示新的金额和新的统计时间。金额算法、聚合 SQL 和权限都没有改动。

**确定性回归** `g4s1a-amount-refresh.spec.ts`：第一次成功显示 ¥111,111.11 和 05:01 → 刷新时接口返回 503，旧金额和旧时间都不显示 → 重试成功显示 ¥222,222.22 和 05:03 → 共请求 3 次。

| 版本 | 结果 | 证据 |
| --- | --- | --- |
| df2c750 前端 | 失败（找不到失败提示，旧金额仍在） | `evidence/amount-before.txt` |
| 本批 | 通过 | `evidence/amount-after.txt`；g34a0-dashboard 与 g34a1-consistency 同时通过 |

## 8. 回归（本机，PG 18.4 生产镜像二进制）

| 项 | 结果 |
| --- | --- |
| `go vet`、gofmt | PASS |
| Go `-race`，PG18 | 44 个顶层测试 PASS；9 个按开关或 Docker 跳过。其中两个 fixture 测试在全新的分权库上单独运行，`TestDatabaseProductionPrivileges` 和 `TestDatabasePreviewEnvironment` 都 PASS |
| 契约 | 125 个动作绑定，18 个负面契约，生产 closure 门；publish 与 direct 的 unittest 均 OK |
| 前端 | vue-tsc、ESLint（0 warning）、Vitest 44/44 |
| Playwright，CI 顺序（全新库 desktop → 同库 mobile-390） | desktop 69 passed / 3 skipped；mobile-390 45 passed / 27 skipped（证据专用用例与自带视口、只在 desktop 运行的用例） |
| 认证浏览器 E2E | 38 passed |
| gitleaks 8.24.2 | no leaks found |
| Docker 类项目（compose 渲染、nginx -t、生产 compose up、restore-test、TestDatabaseG1BackupAndOldSchemaGate） | **本地 NOT_RUN**，由 CI 的 compose、db-test 和 build job 执行 |

## 9. 已验证与未验证

| 项 | 状态 |
| --- | --- |
| preview 运行模式与 production 等价；白名单互斥；staff fail closed；标记先于迁移 | 已验证（单元、PG18、实际二进制） |
| 生产启动、维护命令和数据库规则不变 | 已验证（未加标记的生产形态库 C3；原有生产权限测试；生产相关文件 diff 为空） |
| Preview 备份与恢复 | 已验证（PG18，本地工具模式） |
| Preview compose 与 Nginx 规则 | 已验证（本地插值）；compose 渲染和 nginx -t 待 CI |
| 镜像中存在 healthcheck 命令 | 已验证（alpine 3.23 层） |
| 合成数据脚本（本地 dev 栈） | 已验证；G4-S1A-R2 补充了中途失败的完整性校验（37 号第 2 节） |
| 合成数据脚本（真实 Preview OIDC） | **NOT_RUN** |
| 真实 Preview 部署、DNS、证书、Keycloak realm/client、ingress 接入、端口与网络实测 | **NOT_RUN**（本批禁止） |
| 真人 UAT | **NOT_RUN**（脚本已修订，判定栏为空） |

## 10. 下一次真实部署（G4-S1B）的最小条件

1. 负责人书面授权 G4-S1B。
2. 管理员提供以下资源：
   - 服务器只读账号与操作账号；
   - `baobei-preview.youdaozd.com` 的 DNS 解析（或决定仅内网访问）；
   - 证书签发方式和存放路径 `/etc/nginx/tls/baobei-preview/`；
   - 访问白名单 CIDR。
3. Keycloak 配置：
   - 独立 realm `baobei-preview`，issuer 精确为 `https://crane.youdaozd.com/realms/baobei-preview`；如果 issuer 主机不同，需要另批修改白名单；
   - client `baobei-preview-web` 及其 secret，Redirect URI 精确为 `https://baobei-preview.youdaozd.com/auth/callback`；
   - 七个合成账号，以及对应 sub 的身份清单。
4. `/srv/u-dao/secrets/baobei-preview/`（权限 0700）下的 secret 文件：
   - `oidc_client_secret`
   - `runtime_database_url`
   - `staff_database_url`
   - `db_password`
   - `seed-accounts.json`

   各角色密码均为 Preview 独立签发。
5. 固定的发布产物：同一 SHA 的 publish 产物，记录 web/api 镜像 digest 并归档 manifest。
6. 手工执行 `docker compose -p baobei-preview -f deploy/baobei/compose.preview.yaml`。`direct-deploy.py` 仍然只用于生产，不得用于 Preview。
7. 部署后实测：
   - `docker ps` 无端口；
   - `network inspect` 确认 internal 网络的成员只有 preview 的 api 和 pg；
   - 生产网络中查不到 preview 容器；
   - `/health/ready` 正常；
   - `restore-test`（`BAOBEI_BACKUP_ENV=preview`）通过；
   - seed 运行两次，第二次不写入；
   - 真人 UAT 按脚本执行并签字。

## 11. 结论

```
BASE_SHA=df2c750e2c1004fca79a7171c6e1aab2ab7f5370
PREVIEW_RUN_MODE=PASS
DB_ENVIRONMENT_MARKER=PASS
PREVIEW_BACKUP_RESTORE=PASS
PREVIEW_STATIC_GATE=PASS (42/42 negatives; compose render + nginx -t in CI)
SYNTHETIC_DATA_LOCAL=PASS
SYNTHETIC_DATA_REAL_OIDC=NOT_RUN
AMOUNT_REFRESH_FIX=PASS
PREVIEW_CODE_READY=YES (6e0bbda, Run 42797483906 Attempt 1 8/8; 见 37 号第 1 节)
PREVIEW_DEPLOYED=NO
PRODUCTION_DEPLOYED=NO
```
