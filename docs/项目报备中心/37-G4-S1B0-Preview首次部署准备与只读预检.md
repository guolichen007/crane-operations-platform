# 37 G4-S1B0 Preview 首次部署准备与只读预检

本文是 G4-S1B1 首次 Preview 部署的执行顺序与前置条件。**本批没有执行任何部署、服务器操作、Keycloak 或 DNS 变更。** 依据：[生产部署契约](生产部署契约.md)、[36 号](36-G4-S1A-Preview离线安全实现与验收.md)、`deploy/baobei/compose.preview.yaml`。

| 项 | 值 |
| --- | --- |
| BASE_SHA | `6e0bbda18457c2ff68aaed15853a99730e4be824`（Run `42797483906`，Attempt 1，8/8 success） |
| HEAD_SHA / CI | 推送后按同一 SHA 核对原八门，回填于 STOP 回执 |
| PREVIEW_IMAGES_READY | NO（第 3 节） |
| SERVER_PRECHECK | NOT_RUN（无授权只读访问方式，第 4 节） |
| PREVIEW_DEPLOYED / PRODUCTION_DEPLOYED | NO / NO |

## 1. G4-S1A 原版本工程门禁（Gate 0）

**核对方法：** GitHub API。Run `42797483906` 的 head_sha 与远端 `product/baobei-v1` 都是 `6e0bbda`，Attempt 1。

**八个 job 全部 success：** contract、api-test、security、web-quality、db-test、build、compose、e2e。

**Compose job 执行了 Preview 门。** `tests/baobei/task.mjs` 中的 `compose()` 依次运行 `compose config`、`production.mjs`、`preview.mjs`。preview.mjs 在 `process.env.CI` 下有两条硬约束：

- `docker compose config` 渲染失败即抛错；
- `nginx -t` 无法运行或语法失败即抛错。

步骤 “Run node tools/task.mjs baobei compose” 结论为 success，耗时 2 s（上一基线为 1 s）。说明以下内容在 CI 中实际执行并通过：Docker Compose 渲染后的校验、42 个反例、生产与 Preview 交叉拒绝，以及 Preview Nginx 语法检查。

**E2E 包含 `g4s1a-amount-refresh.spec.ts`。** 该 spec 位于 `tests/baobei/e2e/`，Playwright 配置 `testDir: './e2e'` 未设排除规则，“Run node tools/task.mjs baobei e2e” 步骤 success。

**db-test 执行了 Preview DB 测试。** “db-privileges” 步骤由 3 s 增至 6 s，对应新增的第二个 PostgreSQL fixture（`TestDatabasePreviewEnvironment`）。

**限制：** CI 日志接口对匿名访问返回 403。以上结论来自步骤结论、步骤耗时与代码路径，没有逐行读取日志。

**结论：G4-S1A 原版本（6e0bbda）工程门禁 PASS。**

## 2. 合成数据完整性修复（Gate 1，P1）

**问题。** 旧版 `seed.mjs` 只要销售 A 能看到同标记项目就整体跳过。首次运行中途失败后，剩余数据永远不会补齐，脚本却会报告成功。

**修复方式：**

- 预期数据清单 `plan()` 固定为 18 个项目，逐项列出项目名、创建人、负责人（待分配为空）、最终状态和最终阶段，其中跨组重复项单独列出。
- 写入前，用**管理员**账号读取全部同标记项目。管理员是全局范围，包含待分配草稿，不再只依赖销售 A 能看到的部分。读到的结果按三种情况处理：

| 情况 | 处理 |
| --- | --- |
| 不存在任何同标记项目 | 正常初始化 |
| 与清单完全一致 | 不写入，报告 `complete` |
| 其他任何情况：缺失、多余、重复、状态或阶段不符、负责人或创建人不符 | 列出全部差异后报错，**不写入、不删除、不归还、不覆盖** |

- 初始化完成后再比对一次，不一致即判失败。
- 首次 Preview 数据库若被部分写入，经负责人确认后重建隔离合成库再重跑，不做自动修复。

**纯逻辑测试** `tests/baobei/preview/seed-check.mjs`：在 CI compose job 中由 preview.mjs 调用。14 个场景加 9 个目标保护断言，全部 PASS：

- 空库、完整、顺序无关；
- 只创建 1、2、3 条后中断；
- 跨组重复缺失；
- 批准未生效、退回未生效；
- 待分配草稿被指派、负责人被移交；
- 同一项目重复创建、出现非清单项目、同名项目但创建人不同。

**真实 API 场景**（本地 PG18 合成栈，`BAOBEI_SEED_FAIL_AFTER` 仅在回环 dev 模式下生效）。证据：`local-private/baobei/g4s1a/evidence/seed-r2-full.txt`、`seed-r2-partial.txt`。

| 场景 | 中断后的库 | 重跑结果 | 重跑后的库 |
| --- | --- | --- | --- |
| 空库完整运行 | — | 第一次：`seeded`，26 次写入，18 个项目、3 个待审、审计 31 条；第二次：`complete`，不写入 | 不变：项目数、待审数、`max(updated_at)`、审计数完全相同 |
| 第 1 次写入后中断 | 1 个项目 | 报错，列出缺失的 17 项 | 不变 |
| 第 3 次写入后中断 | 3 个项目 | 报错，列出缺失项 | 不变 |
| 第 14 次写入后中断（批准前） | 13 个项目、1 个待审 | 报错：`status approved: SUBMITTED ≠ APPROVED`、`stage approved` 不符、缺少 returned、original、crossTeamDuplicate 等 | 不变 |
| 第 21 次写入后中断（跨组重复已创建、未提交） | 16 个项目 | 报错：`status crossTeamDuplicate: DRAFT ≠ UNDER_REVIEW`、缺少 teamQueue 和 unassigned | 不变 |
| 完整数据被改状态（退回项被改为 SUBMITTED） | — | 报错：`status returned: SUBMITTED ≠ RETURNED` | 不变 |

## 3. 发布产物来源（Gate 3）

**核对结果（只读）：**

- **发布分支：** 远端只有 `product/baobei-v1`，没有 `release/baobei-publish-<SHA>` 分支，因此从未触发过 Baobei 发布工作流。
- **Environment：** 仓库的 Environments 列表为空，`baobei-ghcr-publish` 不存在。而 `publish.py` 要求该 Environment 恰好有一组 required reviewers，且为直接 User、`prevent_self_review=true`、只允许一个对应的发布分支，否则 `RELEASE_ENV_NOT_VERIFIED`。
- **GHCR：** 匿名拉取令牌对 `guolichen007/baobei-web` 返回 403，Package 页面 404，即镜像不存在或不可公开拉取。
- **结论：** 当前源码 SHA 没有可验证的 registry digest，也没有部署 manifest。`local-private/.../release/*/manifest.json` 只是开发证据（`digest_kind=local_image_id`），不得使用。

**PREVIEW_IMAGES_READY=NO。** 本批没有创建 release ref、发布 GHCR 或修改 Environment，也没有拿旧镜像冒充当前版本。

**正式发布需要负责人完成**（按生产部署契约，不绕过发布保护）：

1. 在仓库 Settings → Environments 新建 `baobei-ghcr-publish`：
   - Required reviewers 只设一组直接用户（不能是团队），开启 “Prevent self-review”；
   - Deployment branches 选 “Selected branches”，只添加 `release/baobei-publish-<待发布 SHA>` 一条。
2. 由负责人以外的人员创建 release ref：`refs/heads/release/baobei-publish-<SHA>`，指向已通过八门 CI 的 `product/baobei-v1` 提交，不能是 force push。
3. 工作流 preflight 核对：ref 格式、SHA 是 `product/baobei-v1` 的祖先、同 SHA 八门 CI success、Environment 规则。之后由审批人在 Actions 页面批准 publish job（发起人不能自批）。
4. publish 推送 `ghcr.io/guolichen007/baobei-web`、`baobei-api`，校验 registry digest 和 OCI 标签（revision 等于 SHA），然后匿名回拉两次。
   - 首次创建的 Package 若为 Private，会失败并输出 `PUBLIC_PULL_NOT_VERIFIED`；
   - 负责人把两个 Package 设为 Public 后，用新的 release ref 重试。
5. 下载 artifact `baobei-registry-deployment-<SHA>` 中的 `deployment-manifest.json`，归档到 `/srv/u-dao/baobei-preview/releases/<SHA>/`（artifact 只保留 30 天）。
6. 拉取验证：在服务器上 `docker pull ghcr.io/guolichen007/baobei-api@<digest>`（web 同理），再用 `docker inspect` 确认 RepoDigests 包含该 digest，且 `org.opencontainers.image.revision` 等于 SHA。

## 4. 服务器只读预检（Gate 4）

**状态：NOT_RUN。** 本批没有获得授权的服务器只读访问方式，不推测服务器状态。

**已完成的公开只读检查**（不登录服务器；`evidence/s1b0-public-checks.txt`，2026-11-10T11:01Z）：

| 检查 | 结果 |
| --- | --- |
| `baobei-preview.youdaozd.com` DNS | 不存在（Name or service not known） |
| `baobei.youdaozd.com`、`crane.youdaozd.com` DNS | 都解析到 124.223.220.182（同一台主机） |
| Keycloak `realms/crane` OIDC metadata | 200，issuer 为 `https://crane.youdaozd.com/realms/crane` |
| Keycloak `realms/baobei-preview` | 404 “Realm does not exist” |
| GHCR 匿名拉取 `baobei-web` | 403 |

获得授权后，由负责人提供的只读账号执行 35 号文档第 5 节的命令清单（系统、端口、Docker、网络、ingress `nginx -T`、防火墙、DNS、TLS）。重点核对以下几项：

- 内存和磁盘余量；
- 80/443 只被共享 ingress 占用；
- 不存在 `baobei-preview-*` 容器、网络、卷；
- ingress 容器的网络与挂载；
- `/etc/nginx/tls/` 下是否有可用于 Preview 的证书路径；
- GHCR 只读拉取。

## 5. 首次部署严格执行顺序（G4-S1B1，需另行授权）

> 已被 [38 号部署前最终执行清单](38-G4-S1B1-Preview部署前最终执行清单.md) 第 6 节取代（合并了镜像与 secret 先行校验、只启动 PG、白名单 allow+deny、每步前提与停止条件等更正）。执行时以 38 号为准。

所有命令在服务器上由负责人指定的操作员执行。每一步完成并留证后才进入下一步，任何一步失败都按第 6 节处理。

**统一约定：**

- `P=baobei-preview`，`F=deploy/baobei/compose.preview.yaml`；
- 只用 `docker compose -p $P -f $F`，**不使用 `direct-deploy.py`**（它只用于生产）；
- 镜像变量只接受 `ghcr.io/guolichen007/baobei-*@sha256:…`。

### 5.1 资源与现状只读检查

执行第 4 节清单，输出归档到 `/srv/u-dao/baobei-preview/evidence/<日期>/precheck/`。

**停止条件（满足任一即停）：**

- 磁盘少于 10 GiB 或可用内存少于 1 GiB（建议值，负责人可调整）；
- 已存在任何 `baobei-preview` 名称的容器、网络或卷；
- 80/443 被其他进程占用；
- `nginx -t` 当前就不通过。

### 5.2 待创建清单（先审批，后创建）

| 资源 | 名称 | 创建方式 |
| --- | --- | --- |
| 前端网络 | `baobei-preview-frontend` | `docker network create baobei-preview-frontend`（compose 中为 external，必须预先建好） |
| 内部网络 | `baobei-preview-internal`（internal） | compose 首次启动时创建 |
| 出网网络 | `baobei-preview-egress` | compose 首次启动时创建 |
| 数据卷 | `baobei-preview-data` | compose 首次启动时创建 |
| secret 目录 | `/srv/u-dao/secrets/baobei-preview/`，root:root，0700 | 手工创建 |
| 证据目录 | `/srv/u-dao/baobei-preview/{releases,evidence,backups}`，0700 | 手工创建 |
| 证书目录 | `/srv/u-dao/platform/ingress/tls/baobei-preview/` | 按负责人选定的签发方式 |

### 5.3 PostgreSQL 首次启动与环境标记

1. 只启动数据库：`docker compose -p $P -f $F up -d --wait baobei-preview-postgres`。这时还不能启动 API 或 Web：API 不会自动迁移，在未迁移的库上会拒绝启动。
2. 在空卷上，初始化目录按顺序执行三个脚本：
   - `10-baobei-roles.sql`（initialize.sql）
   - `20-baobei-staff-role.sql`
   - `30-baobei-preview-environment.sql`
3. 核对：
   - `SELECT name FROM baobei_environment` 返回唯一一行 `preview`；
   - 存在 `baobei_migrator`、`baobei_runtime`、`baobei_maintenance`、`baobei_staff_api` 四个角色；
   - 存在 pg_trgm 扩展。

### 5.4 五种数据库身份与独立 SCRAM 密码

| 身份 | 用途 | 凭据文件 | 是否挂入常驻 API |
| --- | --- | --- | --- |
| `baobei_bootstrap` | PostgreSQL 初始化管理员 | `db_password`（仅 PG 容器读取） | 否 |
| `baobei_migrator` | 一次性 `maintenance migrate` | `migrator_database_url`，**单独保管**，UID 10001 可读 | **否** |
| `baobei_maintenance` | 一次性 `identity-apply`、`staff-provision --once` | `maintenance_database_url`，root 0400，**单独保管** | **否** |
| `baobei_runtime` | 常驻 API、`protection-sweep` | `runtime_database_url` | 是 |
| `baobei_staff_api` | 常驻 API 的人员与会话连接 | `staff_database_url` | 是 |

- 在 PG 容器内用 psql，对四个登录角色分别执行 `\password <角色>`，每个密码由密码生成器独立产生。
- **不得复用生产密码**，Preview 密码也**不得复制到生产**。
- DSN 主机统一为 `baobei-preview-postgres:5432/baobei`。

### 5.5 一次性 migrator 与 maintenance DSN

- 两份文件只在一次性容器运行期间挂载，**不挂入 compose 的常驻 API**。compose.preview.yaml 的 API 只引用 runtime、staff、oidc 三个 secret，preview.mjs 的反例“runtime 改用 migrator DSN”会拒绝其他写法。
- 用完后移出 secret 目录，另行保管。

### 5.6 一次性迁移与 schema 验证

```bash
docker run --rm --read-only --network baobei-preview-internal \
  --security-opt no-new-privileges --env APP_ENV=preview \
  --env DATABASE_URL_FILE=/run/secrets/db_url \
  --mount "type=bind,src=$MIGRATOR_DSN_FILE,dst=/run/secrets/db_url,readonly" \
  "$BAOBEI_PREVIEW_API_IMAGE" maintenance migrate
```

**预期：** 输出 `database schema verified`。

**拒绝情况：**

- 环境标记缺失或不是 `preview`：迁移前拒绝，36 号 C1 已验证；
- 用错角色：拒绝，C6 已验证。

迁移完成后，public 下应有 31 张表（30 张产品表 + `baobei_environment`）。

### 5.7 Keycloak（Preview realm）

依据 [Keycloak生产配置契约](Keycloak生产配置契约.md) 的 mapper/scope 做法，但在**独立 realm** 中进行：

- realm：`baobei-preview`，issuer 必须精确等于 `https://crane.youdaozd.com/realms/baobei-preview`。若改用其他主机或独立实例，需先修改 `environment.go` 白名单，另行提交并通过 CI。
- client：`baobei-preview-web`（confidential），Redirect URI 精确为 `https://baobei-preview.youdaozd.com/auth/callback`，Web Origin 为 `https://baobei-preview.youdaozd.com`。client secret 写入 `oidc_client_secret`。
- client roles：`baobei_sales`、`baobei_manager`、`baobei_director`、`baobei_admin`。
- 七个合成账号：preview-sales-a、preview-sales-b、preview-sales-c、preview-manager、preview-manager-2、preview-director、preview-admin。邮箱用 `@example.test`，密码独立生成，写入 `seed-accounts.json`（0400）。
- 记录每个账号的 sub，替换 `deploy/baobei/preview/identity.example.json` 中的 subject。两个团队的 ID 保持示例值或另行生成。

### 5.8 identity-apply

```bash
docker run --rm --read-only --user 0:0 --network baobei-preview-internal \
  --security-opt no-new-privileges --env APP_ENV=preview \
  --env DATABASE_URL_FILE=/run/secrets/db_url \
  --mount "type=bind,src=$MAINTENANCE_DSN_FILE,dst=/run/secrets/db_url,readonly" \
  --mount "type=bind,src=$APPROVED_IDENTITY_FILE,dst=/run/baobei/identity.json,readonly" \
  "$BAOBEI_PREVIEW_API_IMAGE" maintenance identity-apply --file /run/baobei/identity.json \
  --operator "$APPROVED_OPERATOR_ID" --dry-run
```

- 清单文件为 root:root、0400。
- 先 dry-run，核对输出：teams_changed=2，users_changed=7，并附 manifest_sha256。
- 负责人批准后去掉 `--dry-run` 正式应用。

### 5.9 API 与 Web 固定 digest 启动

```bash
export BAOBEI_PREVIEW_API_IMAGE=ghcr.io/guolichen007/baobei-api@sha256:<manifest 中的值>
export BAOBEI_PREVIEW_WEB_IMAGE=ghcr.io/guolichen007/baobei-web@sha256:<manifest 中的值>
export BAOBEI_PREVIEW_SECRETS_DIR=/srv/u-dao/secrets/baobei-preview
docker compose -p $P -f $F config --quiet
node tests/baobei/preview.mjs   # 在同一 SHA 的检出上执行
docker compose -p $P -f $F up -d --no-build --pull missing --wait
```

**核对清单：**

- `docker ps` 中 preview 容器的 Ports 列为空；
- `docker network inspect baobei-preview-internal` 显示 Internal=true，成员只有 preview 的 api 和 pg；
- `docker network inspect u-dao-baobei-internal` 中没有 preview 容器；
- 容器内 `wget -qO- http://127.0.0.1:8088/health/ready` 正常；
- `RepoDigests` 和 revision 标签等于 manifest。

### 5.10 HTTPS、Ingress、白名单与真实 OIDC 登录

1. 负责人创建 DNS 记录，或决定仅限内网访问；按选定方式签发证书。
2. ingress 只额外加入 `baobei-preview-frontend` 网络。
3. 安装 `deploy/baobei/ingress/baobei-preview.youdaozd.com.conf.example`，填入批准的 CIDR，替换其中的 `deny all` 前置规则。
4. 先执行 `nginx -t`，通过后再 reload。生产 vhost 不改动。
5. 验证：
   - 白名单外访问被拒绝；
   - 白名单内的浏览器用七个账号各登录一次，确认 Cookie 名为 `__Host-baobei_session` 且带 Secure；
   - 未开通的账号得到 `LOGIN_NOT_PROVISIONED`。

### 5.11 合成数据、备份恢复与四角色 UAT

1. 合成数据：执行 `BAOBEI_SEED_ORIGIN=https://baobei-preview.youdaozd.com BAOBEI_SEED_ACCOUNTS_FILE=… node tests/baobei/preview/seed.mjs`。
   - 第一次应输出 `seeded`；
   - 第二次应输出 `complete`，不写入；
   - 若输出不一致并报错，按第 6 节停止。
2. 备份恢复：用 `BAOBEI_BACKUP_ENV=preview`、`BAOBEI_POSTGRES_CONTAINER=<preview pg 容器>`、`BAOBEI_BACKUP_USER=baobei_bootstrap`、`BAOBEI_BACKUP_DIR=/srv/u-dao/baobei-preview/backups` 执行 `node tests/baobei/backup.mjs backup`，然后执行 `restore-test`。
   - 预期 `environment=preview`，31 张表；
   - 不得对生产容器执行。
3. 四角色 UAT：按 `tests/baobei/uat/四角色人工UAT脚本.md` 执行，判定由真人填写并签字。

### 5.12 停止条件、回滚与证据保留

**停止条件（出现任一即停止后续步骤，记录并上报）：**

- 健康检查失败，或任一容器反复重启；
- 出现宿主端口；
- preview 容器出现在任何 `u-dao-*` 网络中；
- 环境标记或迁移校验失败；
- OIDC issuer、client 与白名单不符；
- 合成数据不一致；
- 恢复校验失败；
- 生产 vhost 或生产容器出现任何变化。

**回滚：**

- ingress：删除 Preview vhost，`nginx -t` 通过后 reload，并把 ingress 从 `baobei-preview-frontend` 断开。
- 应用：`docker compose -p $P -f $F down`。不加 `-v` 时卷保留，可以排查。
- 数据库：Preview 库可以丢弃。确认后 `docker volume rm baobei-preview-data`，从 5.3 重新开始。

任何回滚步骤都不涉及生产项目 `u-dao-baobei`、生产网络、生产卷或生产 vhost。

**证据保留：** 每一步的命令输出（不含密钥）、`docker inspect` 摘要、manifest、dry-run 输出、seed 输出、备份 manifest 和 UAT 记录，都存入 `/srv/u-dao/baobei-preview/evidence/<日期>/`。

## 6. 进入 G4-S1B1 前仍需负责人提供

| 项 | 状态 |
| --- | --- |
| G4-S1B1 书面授权 | 未提供 |
| 服务器只读账号与操作账号 | 未提供 |
| `baobei-preview.youdaozd.com` DNS（或仅内网的决定） | 不存在 |
| 证书签发方式 | 未定 |
| 访问白名单 CIDR | 未提供 |
| Keycloak realm `baobei-preview`、client、七个账号 | 不存在 |
| `baobei-ghcr-publish` Environment 与审批人 | 不存在 |
| 当前 SHA 的 GHCR 镜像与 deployment manifest | 不存在（PREVIEW_IMAGES_READY=NO） |

```
G4_S1A_ORIGINAL_GATES=PASS (6e0bbda, Run 42797483906 a1 8/8)
SEED_INTEGRITY=PASS (14 pure cases + 9 target guards; 6 real-API scenarios)
PREVIEW_IMAGES_READY=NO
SERVER_PRECHECK=NOT_RUN
REAL_OIDC=NOT_RUN
PREVIEW_DEPLOYED=NO
PRODUCTION_DEPLOYED=NO
```
