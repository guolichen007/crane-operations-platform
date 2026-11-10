# 38 G4-S1B1 Preview 部署前最终执行清单

本清单用于 G4-S1B1 实际部署，取代 37 号文档第 5 节的执行顺序，并已合并 Gate 1 的五项更正。架构与隔离设计不在此重复，见 35、36 号文档。**本批没有修改任何服务器、Keycloak、DNS、证书、Ingress 或发布设置。**

## 0. 冻结版本

| 项 | 值 |
| --- | --- |
| 冻结候选 SHA | `ca41c3ba090a7dc17120f6565d0a54a2f8d644fd`（product/baobei-v1 远端 HEAD） |
| CI | Run `42812452001`，Attempt 1，八项全部 success：contract、db-test、web-quality、security、build、api-test、compose、e2e（GitHub API 核对，head_sha 一致） |
| 冻结规则 | 冻结后只允许上线必需的修复。任何代码改动都要产生新的 SHA、重新通过八项 CI，并用该 SHA 重新走发布流程，冻结 SHA 也随之更新 |

## 1. 现状汇总

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 代码 | Preview 运行模式、环境标记、独立 Compose、静态门、备份区分、合成数据完整性都已在 CI 通过（`6e0bbda`、`ca41c3b` 八项 CI 均通过） | 36、37 号文档 |
| 服务器只读预检 | **NOT_RUN**：沙箱中没有任何授权的服务器访问方式（无 SSH 密钥、无账号、无跳板） | — |
| 生产隔离现状 | 仅从仓库静态推断（生产 Compose 无宿主端口，internal 网络 `internal: true`），**未经现场核实** | 35 号 §1 |
| GHCR 镜像 | **不存在**：没有 `release/baobei-publish-*` 分支，仓库 Environments 为空，GHCR 匿名令牌请求返回 403 | API 查询，2026-11-10 |
| Preview 域名 | `baobei-preview.youdaozd.com` 无 DNS 记录 | 公开 DNS |
| Keycloak | `realms/crane` 存在；`realms/baobei-preview` 返回 404，不存在 | 公开 OIDC metadata |
| 证书 | 签发方式未定 | — |

## 2. 上线前必需资源与责任人

| # | 资源 | 责任人 | 验收标准 | 状态 |
| --- | --- | --- | --- | --- |
| R1 | G4-S1B1 书面部署授权，注明冻结 SHA | 负责人 | 授权记录 | 未提供 |
| R2 | 服务器只读账号（供 S0 预检）和受限操作账号（docker、`/srv/u-dao/*/baobei-preview`） | 服务器管理员 | 只读账号执行 §3 命令成功；操作账号不能访问生产 secret 目录 | 未提供 |
| R3 | GHCR 发布：Environment、审批人、release ref、Package 设为 Public | 仓库管理员 + 审批人 | §4 全部通过，得到 deployment-manifest | 未开始 |
| R4 | DNS：`baobei-preview.youdaozd.com` 的 A 记录（以及 AAAA，若开放 IPv6），或决定仅内网访问 | 域名管理员 | `dig` 返回批准的地址 | 不存在 |
| R5 | 证书：签发方式（人工 TrustAsia、独立 ACME 或通配符证书）及存放路径 | 负责人 + 服务器管理员 | §5.3 | 未定 |
| R6 | 访问白名单：IPv4 CIDR 列表；IPv6 是否开放及对应 CIDR | 负责人 | 书面列表 | 未提供 |
| R7 | Keycloak：Preview realm、client、四个角色、七个合成账号、mapper、scope | Keycloak 管理员 | §5.1 Evaluate 结果 | 不存在 |
| R8 | secret 文件（§5.2） | 服务器管理员（写入）、负责人（核对权限） | `stat` 与 `sha256sum` 对照（不显示内容） | 未创建 |
| R9 | 合成身份清单（真实 sub 替换示例值） | Keycloak 管理员 + 负责人审批 | identity-apply dry-run 输出 | 未创建 |
| R10 | 真人 UAT 四角色执行人与签字人 | 负责人 | UAT 脚本签字 | 未指派 |

## 3. 服务器只读预检（执行人：持 R2 只读账号的管理员）

**执行前提：** R2 已提供。所有命令只读；不读取、不输出、不复制 secret 明文。

**禁止操作：** `up`/`down`/`restart`、`network create`、数据库写入、`nginx -s reload`。

```bash
# 系统与资源
uname -a; cat /etc/os-release; uptime; free -h; df -h / /srv /var/lib/docker
docker version --format '{{.Server.Version}}'; docker compose version
# 现有容器与健康（生产基线）
docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Networks}}'
docker compose ls
curl -fsS -o /dev/null -w '%{http_code}\n' https://baobei.youdaozd.com/health/ready
curl -fsS -o /dev/null -w '%{http_code}\n' https://crane.youdaozd.com/
# 端口
ss -ltnp | grep -E ':(80|443|5432|8080|8088)\b'
# 网络与成员
docker network ls
for n in $(docker network ls --format '{{.Name}}' | grep -E 'u-dao|baobei|crane|firebot|ingress'); do
  docker network inspect "$n" --format '{{.Name}} internal={{.Internal}} {{range .Containers}}{{.Name}} {{end}}'; done
# 冲突检查（应全部为空）
docker ps -a --format '{{.Names}}' | grep -i preview
docker network ls --format '{{.Name}}' | grep -i preview
docker volume ls --format '{{.Name}}' | grep -i preview
ls -ld /srv/u-dao/*/baobei-preview /srv/u-dao/platform/ingress/tls/baobei-preview 2>&1
# Ingress（只读）
docker inspect <ingress 容器> --format '{{json .NetworkSettings.Networks}}'
docker inspect <ingress 容器> --format '{{range .Mounts}}{{.Source}} -> {{.Destination}} {{.RW}}{{"\n"}}{{end}}'
docker exec <ingress 容器> nginx -T 2>/dev/null | grep -nE 'server_name|listen|limit_req_zone|ssl_certificate|allow|deny'
# 防火墙
sudo -n nft list ruleset 2>/dev/null | head -100 || sudo -n iptables -S | head -100
# 数据库网络可达性：确认没有任何 PostgreSQL 发布宿主端口，也没有加入非 internal 网络
docker ps --format '{{.Names}} {{.Ports}}' | grep -i postgres
# DNS 与 TLS
dig +short baobei-preview.youdaozd.com A; dig +short baobei-preview.youdaozd.com AAAA
openssl s_client -connect baobei.youdaozd.com:443 -servername baobei.youdaozd.com </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates
# GHCR 只读拉取（§4 发布完成后）
docker pull ghcr.io/guolichen007/baobei-api@<digest>
```

**成功标准（全部满足才可进入 §6）：**

1. 可用内存 ≥ 1 GiB，`/var/lib/docker` 可用空间 ≥ 10 GiB（建议值）。
2. 80/443 只由共享 ingress 监听；5432、8080、8088 没有宿主监听。
3. 不存在名称含 preview 的容器、网络、卷或目录。
4. 生产 Baobei 的 PostgreSQL 只在 `u-dao-baobei-internal` 网络中（`Internal=true`），没有宿主端口。
5. 生产 `/health/ready` 返回 200，记录为部署前健康基线。

**停止条件：** 任一成功标准不满足。将输出归档后上报，不进入后续步骤。

**证据存放：** `/srv/u-dao/baobei-preview/evidence/<日期>/precheck/`。该目录只能在获得写授权后创建；在此之前，证据由管理员保存在本地。

## 4. 镜像发布（执行人：仓库管理员与审批人，按原有保护流程）

**现状：** PREVIEW_IMAGES_READY=NO。本批没有创建 release ref、发布镜像或修改 Environment。`publish.py` 的门槛保持不变。

| 步 | 前提 | 操作 | 成功标准 | 停止条件 |
| --- | --- | --- | --- | --- |
| 4.1 | 冻结 SHA 的八项 CI 全部通过 | Settings → Environments 新建 `baobei-ghcr-publish`：Required reviewers 只设一组**直接用户**；开启 Prevent self-review；Deployment branches 选 Selected branches，只添加 `release/baobei-publish-<冻结 SHA>` | 设置截图；`publish.py --preflight` 能通过 Environment 校验 | 审批人只能是团队，或无法开启防自批 |
| 4.2 | 4.1 完成 | 由**非审批人**创建 `refs/heads/release/baobei-publish-<冻结 SHA>`，指向冻结 SHA；禁止 force push | Baobei GHCR Publish 工作流的 preflight 成功 | preflight 失败（ref、祖先关系、CI 或 Environment 任一项） |
| 4.3 | preflight 成功 | 审批人在 Actions 页面批准 publish job（发起人不能自批） | publish 成功：两个镜像的 registry digest 和 OCI revision 等于冻结 SHA，匿名回拉通过 | `PUBLIC_PULL_NOT_VERIFIED`：负责人把 `baobei-web`、`baobei-api` 两个 Package 设为 Public，再用**新的** release ref 重新走 4.2 |
| 4.4 | publish 成功 | 下载 artifact `baobei-registry-deployment-<SHA>` 中的 `deployment-manifest.json` 并核对 `source_sha`、`ci_run_id` 和两个 `*_registry_ref` | manifest 内容与冻结 SHA 一致 | 任何字段不一致 |
| 4.5 | 4.4 完成，R2 已提供 | 服务器上 `docker pull` 两个 digest，并 `docker inspect` 核对 RepoDigests 和 revision 标签 | 都等于 manifest 中的值 | 不一致：**禁止**使用其他镜像或临时 digest 替代 |

## 5. OIDC、secret、证书与访问边界准备

### 5.1 Keycloak（执行人：Keycloak 管理员；依据 Keycloak生产配置契约，在独立 realm 中执行）

| 项 | 值 |
| --- | --- |
| Realm | `baobei-preview`（issuer 必须精确为 `https://crane.youdaozd.com/realms/baobei-preview`，否则 API 拒绝启动；若改用其他主机，需先改代码白名单并重新通过 CI，冻结 SHA 也随之更新） |
| Client ID | `baobei-preview-web`，confidential |
| Standard flow | ON（Authorization Code + PKCE S256） |
| Direct access grants / Implicit | OFF / OFF |
| Valid Redirect URI | `https://baobei-preview.youdaozd.com/auth/callback`（精确，无通配符） |
| Web Origin | `https://baobei-preview.youdaozd.com`（精确） |
| Full Scope Allowed | OFF |
| Client roles | `baobei_sales`、`baobei_manager`、`baobei_director`、`baobei_admin` |
| Mapper | User Client Role，Client ID 为 `baobei-preview-web`，multivalued，claim `resource_access.${client_id}.roles`，加入 ID token 和 access token |
| Scope | dedicated scope 显式加入四个 client role 的 scope mapping |
| 账号属性 | email 使用 `@example.test`，并设 `email_verified=true` |

**七个合成账号**（sub 由 Keycloak 生成，管理员记录后填入身份清单 R9）：

| 账号 | Keycloak client role | Baobei 角色 | 团队 |
| --- | --- | --- | --- |
| preview-sales-a | baobei_sales | 销售 | 合成一组 |
| preview-sales-b | baobei_sales | 销售 | 合成一组 |
| preview-sales-c | baobei_sales | 销售 | 合成二组 |
| preview-manager | baobei_manager | 主管 | 合成一组 |
| preview-manager-2 | baobei_manager | 主管 | 合成二组 |
| preview-director | baobei_director | 总监 | 无（空字符串） |
| preview-admin | baobei_admin | 管理员 | 无（空字符串） |

**验收：** 用 Client Scopes → Evaluate 选中每个账号，生成的 ID Token 中 `resource_access.baobei-preview-web.roles` 只包含该账号对应的角色。不满足就 STOP。

### 5.2 secret 文件

目录 `/srv/u-dao/secrets/baobei-preview/`，root:root，0700。不得放进仓库；内容不得输出到终端或日志。

| 文件 | 内容 | 所有者与权限 | 挂载给 |
| --- | --- | --- | --- |
| `db_password` | bootstrap 密码 | root:root 0400 | 仅 PostgreSQL 容器 |
| `runtime_database_url` | `postgres://baobei_runtime:<密码>@baobei-preview-postgres:5432/baobei?sslmode=disable` | root:10001 0440 | 常驻 API |
| `staff_database_url` | `postgres://baobei_staff_api:<密码>@baobei-preview-postgres:5432/baobei?sslmode=disable` | root:10001 0440 | 常驻 API |
| `oidc_client_secret` | `baobei-preview-web` 的 client secret | root:10001 0440 | 常驻 API |
| `migrator_database_url` | `baobei_migrator` 的 DSN | root:10001 0440，**单独存放**于 `/srv/u-dao/secrets/baobei-preview-once/` | 仅一次性迁移容器 |
| `maintenance_database_url` | `baobei_maintenance` 的 DSN | root:root 0400，单独存放（同上） | 仅一次性 identity-apply 容器 |
| `identity.json` | R9 身份清单 | root:root 0400，单独存放（同上） | 仅一次性 identity-apply 容器 |
| `seed-accounts.json` | 七个合成账号的用户名和密码 | root:root 0400，单独存放（同上） | 仅 seed 脚本 |

**密码规则：** 每个密码由密码生成器独立生成，至少 32 字符；**不得与生产密码相同**，Preview 密码也不得复制到生产。

### 5.3 证书与访问边界

**证书：** 路径为 `/srv/u-dao/platform/ingress/tls/baobei-preview/{fullchain.pem,privkey.pem}`，ingress 容器内是 `/etc/nginx/tls/baobei-preview/`。签发方式由 R5 决定。生产使用的 `cert-renew.sh` 与 systemd 单元写死了生产域名，**不得复用**。证书必须先就绪，再执行 §6.10。

**白名单（Gate 1 第 4 项）：** 在 `deploy/baobei/ingress/baobei-preview.youdaozd.com.conf.example` 的 443 server 中，于 `deny all;` 之前逐行加入批准的 `allow <CIDR>;`，**`deny all;` 必须保留**。

- `deny all;` 同时覆盖 IPv4 和 IPv6。
- 若批准开放 IPv6，加入对应的 IPv6 `allow`；若不开放，就不创建 AAAA 记录，并确认 ingress 没有在 `[::]:443` 上为 Preview 提供服务。
- 80 端口 server 只做 308 跳转到 HTTPS，不返回应用内容。

**白名单验收：**

1. 从白名单外的 IPv4 地址访问，返回 403。
2. 如果 ingress 监听 IPv6，从白名单外的 IPv6 地址访问，同样返回 403。
3. 从白名单内访问，登录页正常。

## 6. 首次启动执行顺序（G4-S1B1，需 R1 授权）

**统一约定：**

```bash
cd /srv/u-dao/baobei-preview/releases/<冻结 SHA>/source   # 冻结 SHA 的干净检出，git rev-parse HEAD 必须等于冻结 SHA
export P=baobei-preview F=deploy/baobei/compose.preview.yaml
```

- 不使用 `direct-deploy.py`。
- 每一步的输出（不含 secret）都存入证据目录。

| 步 | 执行前提 | 操作 | 成功标准 | 停止条件 |
| --- | --- | --- | --- | --- |
| 6.1 准备变量与 secret（Gate 1 第 1、2 项） | §3 通过；§4 得到 manifest；§5.2 的文件已就位 | `export BAOBEI_PREVIEW_API_IMAGE=<manifest api_registry_ref>`、`BAOBEI_PREVIEW_WEB_IMAGE=<manifest web_registry_ref>`、`BAOBEI_PREVIEW_SECRETS_DIR=/srv/u-dao/secrets/baobei-preview`；`stat -c '%U:%G %a %n' $BAOBEI_PREVIEW_SECRETS_DIR/*` | 两个镜像变量逐字等于 manifest，格式为 `ghcr.io/guolichen007/baobei-*@sha256:<64 位>`；四个常驻 secret 文件存在且权限符合 §5.2；一次性凭据**不在**该目录 | 任何变量缺失或与 manifest 不符；任何人提议用本地构建、旧版本或临时 digest 替代：一律禁止 |
| 6.2 静态校验 | 6.1 完成 | `docker compose -p $P -f $F config --quiet`；`node tests/baobei/preview.mjs` | 两条命令都通过；`PREVIEW_COMPOSE_RENDERED_BY=docker compose config`，42/42 反例被拒绝，`Preview nginx syntax PASS` | 任一失败 |
| 6.3 创建前端网络 | 6.2 通过 | `docker network create baobei-preview-frontend` | `docker network inspect` 成功，名称完全一致 | 同名网络已存在（说明 §3 有遗漏） |
| 6.4 只启动 PostgreSQL（Gate 1 第 3 项） | 6.3 完成 | `docker compose -p $P -f $F up -d --no-deps --wait baobei-preview-postgres` | `docker compose -p $P ps` **只有** `baobei-preview-postgres` 一个容器且为 healthy；api、web 没有被创建 | 出现 api 或 web 容器：立即 `docker compose -p $P stop baobei-preview-api baobei-preview-web` 并上报 |
| 6.5 核对初始化与标记 | 6.4 healthy | `docker compose -p $P exec baobei-preview-postgres psql -U baobei_bootstrap -d baobei -tAc "SELECT name FROM baobei_environment; SELECT rolname FROM pg_roles WHERE rolname LIKE 'baobei_%' ORDER BY 1; SELECT extname FROM pg_extension"` | 标记只有一行 `preview`；五个 baobei 角色（bootstrap、maintenance、migrator、runtime、staff_api）；存在 pg_trgm | 任一项不符：删卷后从 6.4 重来（Preview 库可丢弃） |
| 6.6 设置角色密码 | 6.5 通过 | 在 PG 容器内 psql 中，对 migrator、runtime、maintenance、staff_api 分别执行 `\password <角色>`，值与 §5.2 中的 DSN 一致 | 用四个 DSN 分别执行 `SELECT current_user` 都能连接（只读） | 任一连接失败 |
| 6.7 一次性迁移 | 6.6 通过 | `docker run --rm --read-only --network baobei-preview-internal --security-opt no-new-privileges --env APP_ENV=preview --env DATABASE_URL_FILE=/run/secrets/db_url --mount type=bind,src=<migrator_database_url>,dst=/run/secrets/db_url,readonly "$BAOBEI_PREVIEW_API_IMAGE" maintenance migrate` | 输出 `database schema verified`；public 下有 31 张表 | 输出 `database role, migration or schema verification failed`：停止，核对角色和标记，**不要**换成其他角色重试 |
| 6.8 身份初始化 | 6.7 通过；R7、R9 完成 | 一次性容器（加 `--user 0:0`，maintenance DSN 和 identity.json 只读挂载）执行 `maintenance identity-apply --file /run/baobei/identity.json --operator <批准 ID> --dry-run`；负责人批准后去掉 `--dry-run` 再执行一次 | dry-run：`teams_changed=2`，`users_changed=7`，`dry_run=true`；正式执行的计数相同；第二次执行计数为 0 | 计数不符或报错 |
| 6.9 启动 API 与 Web | 6.8 完成；证书已就位 | `docker compose -p $P -f $F up -d --no-build --pull missing --wait` | 三个服务都为 healthy；`docker ps` 中 Ports 列为空；`baobei-preview-internal` 成员只有 api 和 pg；`u-dao-baobei-internal` 中没有 preview 容器；RepoDigests 等于 manifest | 任一不符：`docker compose -p $P -f $F stop baobei-preview-web baobei-preview-api` 并上报 |
| 6.10 Ingress | 6.9 通过；R4、R5、R6 完成 | 把 ingress 加入 `baobei-preview-frontend` 网络；安装 Preview vhost（§5.3 白名单）；先 `nginx -t`，通过后再 reload | 生产 `/health/ready` 仍返回 200（与 §3 基线对比）；Preview 的白名单验收通过 | `nginx -t` 失败（不执行 reload）；或生产健康状态有任何变化：立即执行 §7 的 Ingress 回滚 |
| 6.11 真实 OIDC 登录 | 6.10 通过 | 七个账号各登录一次 | 都能进入工作台；Cookie 为 `__Host-baobei_session` 且带 Secure；角色与 §5.1 一致；未开通账号得到 `LOGIN_NOT_PROVISIONED` | 任一账号的角色或范围不符 |
| 6.12 合成数据 | 6.11 通过 | 运行 `BAOBEI_SEED_ORIGIN=https://baobei-preview.youdaozd.com BAOBEI_SEED_ACCOUNTS_FILE=<seed-accounts.json> node tests/baobei/preview/seed.mjs`，然后再运行一次 | 第一次输出 `seeded`；第二次输出 `complete`，不写入 | 输出 `inconsistent`：停止，经负责人确认后从 6.4 重建隔离库 |
| 6.13 备份与恢复演练 | 6.12 通过 | 设置 `BAOBEI_BACKUP_ENV=preview`、`BAOBEI_POSTGRES_CONTAINER=<preview pg 容器 ID>`、`BAOBEI_BACKUP_USER=baobei_bootstrap`、`BAOBEI_BACKUP_DIR=/srv/u-dao/baobei-preview/backups`，执行 `node tests/baobei/backup.mjs backup`，再用 `BAOBEI_BACKUP_VERIFY=1` 运行一次 | `RESTORE_TEST=PASS; environment=preview; 31 table counts` | 失败，或容器 ID 不属于 `baobei-preview` 项目 |
| 6.14 四角色 UAT | 6.13 通过；R10 指派 | 按 `tests/baobei/uat/四角色人工UAT脚本.md` 执行 | 真人填写判定栏并签字 | 出现 P0 或 P1 问题时暂停 UAT 并上报 |

### 6.x 本地演练（PG 18.4 生产镜像二进制，非服务器）

在全新 PostgreSQL 上按 §6.5、§6.7、§6.8 演练（证据 `local-private/baobei/g4s1a/evidence/s1b1-rehearsal.txt`）：

- **6.5**：三个初始化脚本执行后，标记只有一行 `preview`；角色共 5 个：bootstrap、maintenance、migrator、runtime、staff_api；扩展为 pg_trgm。
- **6.7**：`APP_ENV=preview` 用 migrator 角色迁移，输出 `database schema verified`。
- **6.8**：对 `deploy/baobei/preview/identity.example.json` 用 maintenance 角色执行，结果为 dry-run 2/7，正式应用 2/7，重复执行 0/0。
  - CLI 要求清单文件属主为 root:root，沙箱中不是 root，无法从命令行走完整路径，所以改为直接调用同一个 `applyIdentity` 函数验证内容；服务器上必须以 `--user 0:0` 执行。

## 7. 回滚与证据保留

| 层 | 回滚操作 | 影响范围 |
| --- | --- | --- |
| Ingress | 删除 Preview vhost，`nginx -t` 通过后 reload；执行 `docker network disconnect baobei-preview-frontend <ingress>` | 只影响 Preview |
| 应用 | `docker compose -p baobei-preview -f deploy/baobei/compose.preview.yaml down`（不加 `-v`，保留卷以便排查） | 只影响 Preview |
| 数据库 | 负责人确认后执行 `docker volume rm baobei-preview-data`，然后从 6.4 重新开始 | 只影响 Preview（只有合成数据） |
| 镜像 | 不回退到其他 digest；问题修复走新的 SHA、新的 CI 和新的发布 | — |

**任何回滚都不涉及：** 生产项目 `u-dao-baobei`、生产网络、生产卷、生产 vhost、生产 secret。

**证据保留：** 每一步的命令和结果（不含 secret）、`docker inspect` 摘要、manifest、dry-run 输出、seed 输出、备份 manifest、生产健康基线与部署后对比、UAT 签字页。统一存入 `/srv/u-dao/baobei-preview/evidence/<日期>/`。

## 8. 仍需授权的写操作

| 操作 | 授权人 |
| --- | --- |
| 创建 GitHub Environment `baobei-ghcr-publish`，配置审批人 | 仓库管理员 |
| 创建 release ref，批准 publish，把 Package 设为 Public | 仓库管理员与审批人（不得自批） |
| 新建 Keycloak realm、client、角色、mapper、七个账号 | Keycloak 管理员 |
| 新建 DNS 记录，签发证书 | 域名管理员、负责人 |
| 在服务器上创建 secret 目录与证据目录，执行 `docker network create`、`compose up`、一次性迁移、identity-apply | 负责人（G4-S1B1 授权）与服务器管理员 |
| ingress 接入网络、安装 vhost、reload | 负责人与服务器管理员 |

## 9. 结论

| 门禁 | 结果 |
| --- | --- |
| 冻结 SHA CI | PASS（Run 42812452001 Attempt 1 8/8） |
| 服务器只读预检 | NOT_RUN（R2 缺失） |
| 镜像 | PREVIEW_IMAGES_READY=NO（R3 缺失） |
| DNS、证书、白名单 | 未就绪（R4、R5、R6 缺失） |
| Keycloak | 未就绪（R7 缺失） |

```
G4_S1B1_DEPLOY_READY=NO
PREVIEW_DEPLOYED=NO
PRODUCTION_DEPLOYED=NO
```

**阻断项：** R1–R9。代码层面已经就绪；外部资源和授权齐备、§3 与 §4 通过后，可按 §6 执行。
