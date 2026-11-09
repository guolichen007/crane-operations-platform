# 35 G4-S0 隔离 Preview 部署预检

基线 `886f75c7bbe94f96bc6884de24b8e62cced55650`（product/baobei-v1）。本文件只是静态审查和设计，没有实施任何内容。

## 0. 范围声明

- 本批没有拿到服务器只读访问授权，所以只审查了仓库内文件。**PREVIEW_ENVIRONMENT_VERIFIED=NO**。
- 未执行 `docker compose up/down`、`docker run`、`direct-deploy.py` 或任何 `tools/task.mjs baobei` 任务；未访问服务器、GitHub API、GHCR、Keycloak 或 DNS；未修改服务器网络、DNS、证书、Keycloak 或正式数据库（AGENTS.md:6）。
- 候选域名 `baobei-preview.youdaozd.com` 只是建议。DNS 记录、证书、公网可达性和访问控制都没有核实，本文不假定其中任何一项已经就绪。
- 以下“推断”指仓库文件里没有直接写明、由代码行为推出的结论。

## 1. 现状审查

### 1.1 生产 Compose（deploy/baobei/compose.production.yaml）

| 项 | 事实 | 引用 |
| --- | --- | --- |
| 项目名 | `name: u-dao-baobei`，写死在文件里 | compose.production.yaml:1 |
| 服务 | baobei-web、baobei-api、baobei-postgres，都没写 `container_name` | :3, :27, :47；production.mjs:16 |
| 网络 | web=frontend+internal，api=internal+egress，pg=internal | :6, :41, :55 |
| 网络名 | `u-dao-baobei-frontend`（external）、`u-dao-baobei-internal`（internal: true）、`u-dao-baobei-egress`（可出网） | :77-84 |
| 卷 | 命名卷 `baobei-production-data`，Compose 按项目名加前缀（推断：实际名为 `u-dao-baobei_baobei-production-data`）；web 只读 bind 宿主 `/srv/u-dao/platform/ingress/acme-webroot` | :57, :100；:8-13 |
| 宿主端口 | 三个服务都没有 `ports`；CI 门 production.mjs 断言没有端口，direct-deploy 也会拒绝带端口的配置 | production.mjs:15；direct-deploy.py:222-223 |
| 镜像 pin | web/api 由 `${BAOBEI_*_IMAGE:?}` 注入，CI 门要求 `ghcr.io/guolichen007/baobei-*@sha256:<64>`，只认 digest、不认 tag；PG 是 `postgres:18.4-alpine@sha256:9a8afca5…`（tag+digest） | :4, :28, :48；production.mjs:66-68 |
| secret | 四个外部文件，来自 `${BAOBEI_SECRETS_DIR}`，通过 `*_FILE` 读取；环境变量里没有明文 | :37-40, :53-54, :85-93；main.go:188-196；production.mjs:120-123 |
| 应用身份 | `APP_ENV=production`、`DEV_AUTH=0`、`PUBLIC_ORIGIN=https://baobei.youdaozd.com`、`OIDC_ISSUER=https://crane.youdaozd.com/realms/crane`、`OIDC_CLIENT_ID=baobei-web` | :31-36 |
| 初始化 SQL | initialize.sql、staff-role.sql 只读挂入 initdb 目录；角色 migrator/runtime/maintenance/staff_api，数据库名写死为 `baobei` | :58-69；db/initialize.sql:4-14；db/staff-role.sql:20-25 |
| 健康检查 | web 探测 `/health/ready`（API 侧会 ping 两个连接池）；pg 用 `pg_isready`；**API 自身没有 healthcheck** | :22-26, :70-74；api.go:20-33 |
| 加固 | read_only、tmpfs、no-new-privileges | :19-21, :42-44, :75 |
| 备份 | Compose 里没有备份服务。备份入口是 `tests/baobei/backup.mjs`：默认在 dev compose 中查找容器，只允许 `baobei`/`baobei_test` 库，输出目录取 `BAOBEI_BACKUP_DIR`。生产规划目录 `/srv/u-dao/backups/baobei`；保留周期、加密和异地存放仓库里都没有定义 | backup.mjs:28-42, :115；BB-B2-业务规则与运维.md:17-19 |

### 1.2 Nginx 与 Ingress 边界

| 层 | 监听 | 上游 / 证书 | 引用 |
| --- | --- | --- | --- |
| 共享 ingress（容器 `u-dao-ingress-nginx-1`，Baobei 仓库外） | 80/443 | 80 端口只转发 ACME 路径，其余 308 到 https；443 的证书是 `/etc/nginx/tls/baobei/{fullchain,privkey}.pem`，上游是 `baobei-web:8080`（Docker DNS 变量解析），Host 固定写成 `baobei.youdaozd.com` | ingress/baobei.youdaozd.com.conf.example:3-4, :9, :18, :29-30, :33-38, :44-46；cert-renew.sh:19；deploy/ingress/crane.youdaozd.com.conf.example:1 |
| ingress 限流区 | http 上下文定义 `baobei_ingress_login` / `baobei_ingress_callback` | ingress/baobei-http-common.conf.example:3-4 |
| baobei-web 内层 | 8080，`server_name baobei.youdaozd.com` | 上游 `http://baobei-api:8088`，经 resolver 127.0.0.11 解析；清空 XFF；access_log off | nginx.production.conf:5-11；nginx.proxy.production.conf:1-6 |
| 证书脚本 | 宿主机 systemd 运行 | `DOMAIN=baobei.youdaozd.com` 和 ACME_HOME/WEBROOT/TLS 路径都写死，路径不符就退出 | cert-renew.sh:6-9, :17；cert/README.md:5 |

### 1.3 direct-deploy.py

- 用途：从官方 origin 的已审 SHA 导出白名单文件，在本机构建 `baobei-local/<svc>:<SHA>`，生成 manifest，然后执行 `prepare|db|migrate|up|rollback`（direct-deploy.py:59-71, :171-172, :286-318, :344-380）。
- 目标主机：只用本机 Docker Engine，拒绝 tcp/ssh context（:179-183）；apply 只能在 Linux 上执行（:345）。
- **写死的生产标识**：`--project-name u-dao-baobei`（:200）、`compose.production.yaml`（:201）、migrate 用的网络 `u-dao-baobei-internal`（:372）、`APP_ENV=production`（:373）、`WEBROOT=/srv/u-dao/platform/ingress/acme-webroot`（:26）。状态目录和密钥目录由参数传入（:386-394）。
- 误指向风险：给 Preview 跑这个脚本，即使换了 `--state-dir/--secrets-dir`，也会在生产项目 `u-dao-baobei` 上执行 `up`，用 Preview 的 SHA 和 secret 替换生产容器；`migrate` 会接入生产 internal 网络。**结论：在不修改的前提下，Preview 禁止使用 direct-deploy.py。**

### 1.4 发布 workflow 与 CI compose 门

- publish：只在 `release/baobei-publish-<40位SHA>` 分支创建时触发，需要同 SHA 的八门 CI 通过和 Environment `baobei-ghcr-publish` 人工审批（baobei-publish.yml:4-5, :15, :39；publish.py:38-52, :68-102）。镜像 tag 为 `sha-<SHA>-run-<id>-<attempt>`，读出 registry digest 后按 `name@digest` 回拉并校验 revision/source，再做一次匿名拉取（publish.py:161, :177-191）。产物 `deployment-manifest.json` 包含 source_sha、web/api ref 与 digest、schema_version、ci_run_id，artifact 只保留 30 天（publish.py:201-210；baobei-publish.yml:62-67）。
- CI `baobei-compose` job：direct-check → cert-test → compose（dev compose config + production.mjs 静态门 + nginx -t）→ up（**dev** compose）→ http01-test（临时 internal 网络）→ restore-test → down（baobei-ci.yml:172-192；task.mjs:121-130；production.mjs:71-155；http01.mjs:128-129）。CI 从未启动过 `compose.production.yaml`。
- dev compose 同样是 `name: u-dao-baobei`，发布了宿主端口 `127.0.0.1:55439` / `127.0.0.1:5188`，并且 `DEV_AUTH=1`、PG 没有 digest（compose.yaml:1, :4, :9, :24, :42）。CI 的 PG service 也没有 digest，且使用 `trust`（baobei-ci.yml:68-73, :110-115）。

### 1.5 复制生产 Compose 给 Preview 会产生的冲突或泄漏点

| # | 冲突点 | 后果 | 引用 |
| --- | --- | --- | --- |
| C1 | 项目名 `u-dao-baobei` | 和生产同一个项目：`up` 会替换生产容器，`down` 会停掉生产 | compose.production.yaml:1；direct-deploy.py:200 |
| C2 | dev compose 也叫 `u-dao-baobei` | 在服务器上跑 `tools/task.mjs baobei up/down` 会作用到生产项目 | compose.yaml:1；task.mjs:24-31, :125-130 |
| C3 | 网络名写死 | Preview 的 api/pg 会加入生产的 `u-dao-baobei-internal`，Preview API 可以直接连到生产 PG | compose.production.yaml:78-84 |
| C4 | 服务 DNS 名 `baobei-web`/`baobei-api`/`baobei-postgres` | 同一 ingress 或容器接入两个都有同名服务的网络时，解析结果不确定（推断），可能把 Preview 流量送到生产，或反过来 | ingress conf:9, :44；nginx.production.conf:11 |
| C5 | 命名卷 `baobei-production-data` | 项目名相同时共用生产数据卷 | :57, :100 |
| C6 | 宿主 webroot bind | 和生产共用 ACME 目录 | :9；direct-deploy.py:26 |
| C7 | secret 目录变量 | 指错目录就会拿到生产 DSN 或 OIDC secret | :87-93 |
| C8 | `PUBLIC_ORIGIN` / `OIDC_ISSUER` / `OIDC_CLIENT_ID` | 回调地址会落到生产域名，并且使用生产 client | :34-36；auth.go:42 |
| C9 | 代码层硬约束 | `APP_ENV` 只接受 development 或 production；production 必须精确等于生产的 origin、issuer、client。**Preview 用 production 模式无法换域名或 client** | main.go:31-33, :40-42 |
| C10 | 退回 `APP_ENV=development` | cookie 不带 Secure、名字不是 `__Host-`；不校验 DB 角色、启动时以 runtime DSN 自动迁移；不读 STAFF DSN，staff 操作改用 runtime 连接池 | auth.go:54-61；auth_browser.go:48-52；database.go:114-116；main.go:80, :120；staff.go:703-713 |
| C11 | ingress Host 与证书路径 | 写死为 `baobei.youdaozd.com` 和 `/etc/nginx/tls/baobei/` | ingress conf:18, :29-30, :37-38, :46 |
| C12 | ingress 限流区名 | 复制 common 文件会在同一个 http 上下文重复定义 zone，`nginx -t` 失败；如果直接 reload，会影响生产 ingress | baobei-http-common.conf.example:3-4；http01.mjs:123-125 |
| C13 | 证书脚本与 systemd 单元 | 域名和路径写死，不能给 Preview 复用 | cert-renew.sh:6-9, :17；u-dao-baobei-cert-renew.service:19-20 |
| C14 | 备份 | 默认目录和容器定位都按 dev 项目；库名限定为 `baobei` | backup.mjs:28-42, :115 |
| C15 | Keycloak realm/client | 生产用的是 Crane 共享 realm `crane`；共享 realm 内的最小权限隔离还没验证 | compose.production.yaml:35；Keycloak生产配置契约.md:16, :44 |
| C16 | Cookie | 两边都是 `__Host-baobei_session`，但没有 Domain、只发给本主机，所以子域名之间天然隔离（静态已证） | auth.go:56, :61；生产安全与限流.md:5 |
| C17 | 本地镜像 tag | `baobei-local/<svc>:<SHA>` 两边共用；同一 SHA 的镜像内容相同，但清理镜像会影响生产回滚 | direct-deploy.py:171-172；直接部署与回滚.md:52 |

## 2. Preview 独立拓扑设计（可实施，本批不实施）

**(1) Compose 项目与文件。** 新建独立文件 `deploy/baobei/compose.preview.yaml`，`name: baobei-preview`。不采用 overlay，原因有两个：Compose 合并时 `name`、`networks.*.name` 和 environment 都按键覆盖，漏掉一项就会继承生产值（C1/C3/C8）；生产文件的静态门（production.mjs）也要求生产值保持精确。独立文件中：

- 服务名改为 `baobei-preview-web/api/postgres`，从根上避开 C4；
- 网络名 `baobei-preview-frontend`（external）、`baobei-preview-internal`（internal: true）、`baobei-preview-egress`；
- 卷名 `baobei-preview-data`；
- web 的 nginx 配置另写一份 `nginx.preview.conf`：`server_name` 与上游 `baobei-preview-api:8088` 都要改。

另外新增 `tests/baobei/preview.mjs` 静态门，断言上面所有名字不包含 `u-dao-baobei`，并且没有端口。direct-deploy.py 需要新增 `--target preview`，把项目名、compose 文件、网络名和 WEBROOT 都参数化，并按白名单校验；在这一改动完成前，Preview 只能手工 `docker compose -p baobei-preview -f compose.preview.yaml`。

**(2) PostgreSQL。** 用和生产相同的镜像 `postgres:18.4-alpine@sha256:9a8afca5…`（compose.production.yaml:48）。独立卷 `baobei-preview-data`，复用 initialize.sql 和 staff-role.sql 的五身份模型（生产部署契约.md:53-67）。各角色在 Preview 设置**独立的** SCRAM 密码，绝不复制生产密码。库名仍是 `baobei`，因为 SQL 里写死了（initialize.sql:8-10）。备份目录 `/srv/u-dao/backups/baobei-preview`（0700），通过 `BAOBEI_POSTGRES_CONTAINER`、`BAOBEI_BACKUP_USER=baobei_bootstrap` 和 `BAOBEI_BACKUP_DIR` 调用 backup.mjs（backup.mjs:28-43, :115）。建议只保留 7 天，因为里面只有合成数据；这一项需要负责人确认。

**(3) 认证。** `DEV_AUTH=0`。`APP_ENV` 不能用 production（C9），也不应用 development（C10）。**需要在 S1 先改代码**：新增 `APP_ENV=preview`，行为与 production 完全一致（Secure 和 `__Host-` cookie、角色校验、独立 staff DSN、`search_path`），只把 origin、issuer、client 换成另一组精确白名单，例如 `https://baobei-preview.youdaozd.com`、`…/realms/baobei-preview`、`baobei-preview-web`。同时规定 Preview 白名单和生产白名单不能互相匹配。Keycloak 优先建独立 realm `baobei-preview`；如果要求和 Crane 完全隔离，就用独立 Keycloak 实例。在 realm 中建四个 client role 和合成账号 `preview-sales-a/-b`、`preview-manager`、`preview-director`、`preview-admin`，邮箱用 `@example.test`；mapper 和 scope 配置按 Keycloak生产配置契约.md:18-34 照做。可参照 tests/baobei/keycloak-fgap.py:13-15 中的一次性 realm 做法。

**(4) OIDC。** client `baobei-preview-web`，Redirect URI 精确为 `https://baobei-preview.youdaozd.com/auth/callback`，Web Origin 精确为 `https://baobei-preview.youdaozd.com`。回调地址由 `PUBLIC_ORIGIN + /auth/callback` 拼出（auth.go:42）。client secret 单独签发，放在 `/srv/u-dao/secrets/baobei-preview/`。角色从 `resource_access[<ClientID>]` 读取（auth_browser.go:168），所以生产 client 的角色在 Preview 天然无效。

**(5) 网络与 Ingress。** 共享 ingress 已经占用 80/443（crane.youdaozd.com.conf.example:1），独立 ingress 需要另一个 IP 或端口，所以建议继续用共享 ingress，加一个独立 vhost：

- 新建 `baobei-preview.youdaozd.com.conf`，`server_name`、Host 头、308 目标都改为 preview；
- 上游 `baobei-preview-web:8080`，证书 `/etc/nginx/tls/baobei-preview/`；
- 限流区另起名字（如 `baobei_preview_login`），避免 C12；
- 建议加 `allow <办公出口>; deny all;`，Preview 不对公网全开。

ingress 只额外加入 `baobei-preview-frontend` 网络。加入前必须 `nginx -t`；回滚方法是删除这个 vhost，然后 reload。

**(6) 不发布端口的证明。** 静态：`docker compose -p baobei-preview -f compose.preview.yaml config --format json`，断言没有任何 `ports`、没有 `network_mode: host`、没有加入任何 `u-dao-*` 网络（仿照 production.mjs:11-29）。运行时（S1）：

- `docker ps --format '{{.Names}} {{.Ports}}'`：preview 容器的 Ports 列为空；
- `ss -ltnp`：没有新增监听；
- `docker network inspect baobei-preview-internal`：`Internal=true`，成员只有 preview 的 api 和 pg；
- 从外网探测 5432、8088、8080，均不可达。

**(7) 合成数据。** 现有的合成 seed 只在 `DEV_AUTH=1` 时运行（main.go:140-144, :169-185），Preview 用不上。身份数据用现有的 `maintenance identity-apply`：写一份合成 JSON 清单，subject 填 Preview realm 里合成账号的真实 sub（生产部署契约.md:94-107）。业务数据目前只有 Go 测试里的 `generate_series` 夹具（g32c_perf_test.go:358；g33a_stage_test.go:239），没有可以单独运行的工具。**S1 需新增**：一个通过 API 或 migrator 写入的 Preview 合成数据脚本，覆盖四角色、12 阶段、重复候选和金额已知/未知，并在每条记录上打 `合成` 标记。

**(8) 固定 SHA 与 digest。** 首选 publish 产物：取 `deployment-manifest.json` 的 `web_registry_ref` 和 `api_registry_ref`（`name@sha256`），连同 `source_sha` 和 `ci_run_id` 一起归档。artifact 只保留 30 天（baobei-publish.yml:67），所以必须由负责人另存到 `/srv/u-dao/baobei-preview/releases/<SHA>/`。启动后核对 `docker inspect` 的 RepoDigests 和 revision label 是否等于 source_sha（参照 publish.py:124-132）。第二种方式是本地构建，这要求 direct-deploy 先支持 `--target preview`（见 (1)）。

**(9) 健康、恢复、审计与日志。** 给 preview-api 补一个 healthcheck（`wget /health/ready`）。生产文件没有这一项，见 §1.1。恢复演练：用 backup.mjs 的 `restore-test`，它只恢复到 `baobei_restore_<随机>` 库，演练结束后删除（backup.mjs:205-207, :278）。审计数据在 `audit_events` / `staff_audit` 表；日志沿用生产做法（nginx.production.conf:7-8；main.go:164），不记录 query、code、token 或 cookie。Preview 合成数据也按同一规则处理。

**(10) 回滚，以及“Preview 不可能访问生产库”的反证。** 回滚：compose 切回上一组 digest 后执行 `up -d`。Preview 库是可丢弃的，数据库契约有变化时直接重建卷并重新 seed，不做向下兼容。反证分五层：

| 层 | 手段 | 现状 |
| --- | --- | --- |
| 网络 | 生产 PG 只在 `internal: true` 的 `u-dao-baobei-internal` 上，没有宿主端口；Preview 容器不加入该网络 | 生产侧静态已证（compose.production.yaml:55, :81-82；production.mjs:15, :25-26），Preview 侧需 S1 实测 |
| 名称 | DSN 主机名为 `baobei-preview-postgres`，生产网络中没有这个名字 | 设计 |
| 凭据 | Preview 角色密码独立；生产密码不进入 Preview secret 目录 | 需 S1 核对 |
| 启动校验 | 现在只校验角色属性（database.go:134-148）和 staff DSN 与 runtime DSN 同 host/port/db（main.go:121-126），**不校验数据库实例或环境标识** | **缺失，列为 S1 待实现**：migrator 写入 `baobei_environment(name)` 标记；API 在 preview/production 模式启动时核对标记与 `APP_ENV` 是否一致，不一致就 fatal。可选再加 DSN 主机白名单 |
| 镜像/项目 | Preview 静态门断言不含 `u-dao-baobei` | 需新增 |

## 3. 隔离矩阵

| 维度 | 生产 | Preview | 隔离手段 | 验证方法 | 当前状态 |
| --- | --- | --- | --- | --- | --- |
| Compose 项目 | u-dao-baobei（compose.production.yaml:1） | baobei-preview | 独立文件和项目名 | `docker compose ls` | 缺失（文件未建） |
| 服务 DNS 名 | baobei-web/api/postgres | baobei-preview-* | 改名 | `docker network inspect` | 缺失 |
| 内部网络 | u-dao-baobei-internal，internal（:81-82） | baobei-preview-internal | 独立 internal 网络 | inspect 成员与 Internal | 生产静态已证；Preview 需 S1 实测 |
| 前端网络 | u-dao-baobei-frontend（:78） | baobei-preview-frontend | ingress 分别接入 | inspect | 需 S1 实测 |
| 数据卷 | baobei-production-data（:100） | baobei-preview-data | 项目名前缀加独立卷名 | `docker volume ls` | 缺失 |
| PG 镜像 | 18.4@sha256:9a8a…（:48） | 同 digest | 同 pin | inspect RepoDigests | 静态已证（生产） |
| DB 凭据 | secrets 目录（:87-93） | `/srv/u-dao/secrets/baobei-preview` | 独立目录和密码 | 两边文件哈希不相等 | 需 S1 实测 |
| DB 标识校验 | 无（database.go:134-148） | 环境标记 | 新代码 | 负例：Preview API 指向生产库时 fatal | 缺失（S1 实现） |
| APP_ENV | production（:31） | preview（新增） | 代码白名单（main.go:31-42） | 单测与负例 | 缺失（S1 实现） |
| DEV_AUTH | 0（:32） | 0 | 配置 + main.go:34-38 | 静态门 | 静态已证（机制） |
| PUBLIC_ORIGIN | baobei.youdaozd.com（:34） | baobei-preview.youdaozd.com | 白名单 | 静态门 | 缺失 |
| Keycloak | realm crane / baobei-web（:35-36） | realm baobei-preview / baobei-preview-web | 独立 realm 或实例 | Evaluate 与 ID Token | 需 S1 实测 |
| Cookie | `__Host-` 无 Domain（auth.go:56-61） | 同名，主机不同 | host-only | 浏览器检查 | 静态已证 |
| 宿主端口 | 无（production.mjs:15） | 无 | 静态门与运行时检查 | `docker ps`、`ss` | 生产静态已证；Preview 需 S1 实测 |
| Ingress vhost | baobei.youdaozd.com（ingress conf:4, :34） | baobei-preview.youdaozd.com | 独立 vhost、zone、证书路径 | `nginx -t` 与 curl | 缺失 |
| 证书 | /etc/nginx/tls/baobei（:37-38） | /etc/nginx/tls/baobei-preview | 独立签发流程 | `openssl s_client` | 缺失（签发方式待定） |
| ACME webroot | /srv/u-dao/platform/ingress/acme-webroot（:9） | 独立目录或不用 HTTP-01 | 独立 bind | inspect mounts | 缺失 |
| 备份 | /srv/u-dao/backups/baobei（BB-B2:19） | /srv/u-dao/backups/baobei-preview | 环境变量 | ls 与权限 | 缺失 |
| 部署脚本 | direct-deploy 写死生产（:200, :372） | 手工或 `--target preview` | 参数化加白名单 | 单测 | 缺失 |
| 日志隐私 | access_log off（nginx.production.conf:7） | 同 | 同模板 | 配置 diff | 静态已证 |

## 4. 实施风险清单

| 级别 | 风险 | 依据 | 缓解 |
| --- | --- | --- | --- |
| P0 | 用 direct-deploy.py 部署 Preview，会覆盖生产项目 | direct-deploy.py:200, :372 | S1 前禁止使用；参数化后先补单测 |
| P0 | 复制生产 compose 后共用 internal 网络，Preview 能连到生产 PG | compose.production.yaml:81 | 独立文件、独立网络名，加静态门 |
| P0 | 服务同名，ingress 解析出错，Preview 与生产流量互串（推断） | ingress conf:9, :44 | 服务改名 |
| P0 | `APP_ENV` 无 preview 取值；用 development 会降级 cookie、角色校验和 staff 隔离 | main.go:31-42；C10 | S1 先改代码，新增 preview 模式 |
| P1 | 在服务器上执行 dev 任务 `baobei up/down`，误伤生产项目 | compose.yaml:1；task.mjs:125-130 | 服务器上禁止运行 tools/task.mjs 的 compose 类任务；将来把 dev 项目改名（另批） |
| P1 | 没有数据库标识校验，DSN 写错时无法在启动时拦截 | database.go:134-148 | 增加环境标记校验 |
| P1 | 共享 ingress reload 时，Preview vhost 写错会影响生产 | ingress 是单一进程 | `nginx -t`；zone 独立命名；保留回滚副本 |
| P1 | 共享 Keycloak 中新建 realm，或误改 crane realm | Keycloak生产配置契约.md:44 | 独立 realm，最好独立实例；管理员凭据只授权给 preview realm |
| P2 | publish artifact 30 天后过期，Preview 无法追溯 | baobei-publish.yml:67 | 部署时归档 manifest |
| P2 | API 没有 healthcheck，`--wait` 只覆盖 web 和 pg | compose.production.yaml:27-46 | Preview 补上 healthcheck |
| P2 | 没有可独立运行的业务合成数据工具 | main.go:140；只有测试夹具 | S1 新增 seed 工具 |
| P2 | 本地镜像 tag 共用，清理镜像会影响生产回滚 | direct-deploy.py:171 | Preview 只用 registry digest |
| P3 | backup.mjs 的表清单是 30 张，BB-B2 文档写 23 张，文档已过时 | backup.mjs:45-76；BB-B2:18 | 文档另批修正 |
| P3 | dev compose 和 CI 的 PG 没有 digest | compose.yaml:4；baobei-ci.yml:68, :110 | 不影响 Preview，只记录 |

## 5. 进入 G4-S1 所需的权限与环境

需要管理员提供：

| 项 | 内容 |
| --- | --- |
| 服务器账号 | 一个只读账号（S0 检查用）；一个操作账号，限于 docker 和 `/srv/u-dao/*/baobei-preview` |
| DNS | `baobei-preview.youdaozd.com` 的 A/AAAA 记录，或者决定只开放给内网/VPN |
| 证书 | 签发方式（人工 TrustAsia、独立 ACME、通配符证书），路径 `/srv/u-dao/platform/ingress/tls/baobei-preview` |
| Keycloak | 独立 realm 或实例、client `baobei-preview-web` 及 secret、五个合成账号，以及只限该 realm 的管理授权 |
| secret 存放 | `/srv/u-dao/secrets/baobei-preview/`（0700），由负责人写入 |
| 网络与防火墙 | 允许新建 `baobei-preview-*` 网络、允许 ingress 接入；确认不开放新端口；Preview 访问白名单 IP |
| 磁盘与备份 | 卷容量预算、`/srv/u-dao/backups/baobei-preview`、保留周期 |
| GHCR | 镜像 Package 已公开，或提供只读 pull token；指定 SHA 的 publish 运行授权 |
| 代码批次 | 批准 S1 代码改动：`APP_ENV=preview`、环境标记校验、compose.preview.yaml、preview 静态门、seed 工具、direct-deploy `--target` |

S0 只读检查命令清单（**只列，不执行**；全部在服务器只读账号下运行）：

```bash
# 系统
uname -a; cat /etc/os-release; uptime; df -h /srv /var/lib/docker; free -h
# 端口
ss -ltnp; ss -lunp
# Docker
docker version; docker info --format '{{.ServerVersion}} {{.Driver}}'; docker context ls
docker compose ls
docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Ports}}\t{{.Networks}}\t{{.Status}}'
docker volume ls; docker image ls --digests | grep -E 'baobei|postgres'
# 网络
docker network ls
docker network inspect u-dao-baobei-frontend u-dao-baobei-internal u-dao-baobei-egress --format '{{.Name}} internal={{.Internal}} {{range .Containers}}{{.Name}} {{end}}'
docker inspect u-dao-ingress-nginx-1 --format '{{json .NetworkSettings.Networks}} {{json .Mounts}}'
docker exec u-dao-ingress-nginx-1 nginx -T 2>/dev/null | grep -nE 'server_name|listen|limit_req_zone|ssl_certificate'
# 防火墙
sudo -n nft list ruleset 2>/dev/null || sudo -n iptables -S
# DNS
dig +short baobei-preview.youdaozd.com A; dig +short baobei-preview.youdaozd.com AAAA; dig +short baobei.youdaozd.com
# TLS
openssl s_client -connect baobei.youdaozd.com:443 -servername baobei.youdaozd.com </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates -ext subjectAltName
ls -l /srv/u-dao/platform/ingress/tls/ /srv/u-dao/backups/ 2>/dev/null
# Keycloak（只读）
curl -fsS https://crane.youdaozd.com/realms/crane/.well-known/openid-configuration | head -c 400
```

## 6. 结论

PREVIEW_S0_ISOLATION_DESIGN=PASS
PREVIEW_ENVIRONMENT_VERIFIED=NO
PREVIEW_DEPLOYED=NO

判 PASS 的理由：生产侧的网络、端口、secret 和 cookie 边界已经静态证实；所有冲突点（C1–C17）都可以用独立文件、改名、独立凭据和新代码消除，没有发现结构上无法隔离的问题。

有一个前提：P0 中的 `APP_ENV` 只接受两种取值（main.go:31-42），这一项必须在 S1 先改代码解决，之后才能部署任何 Preview。在 S1 完成之前，禁止用 direct-deploy.py 或复制生产 compose 的方式起 Preview。
