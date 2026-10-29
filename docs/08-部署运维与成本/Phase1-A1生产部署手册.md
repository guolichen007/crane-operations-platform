# Phase1-A1 生产部署手册

本阶段只交付代码、CI、镜像与模板，**没有授权服务器部署、真实 DNS/TLS 或 Firebot 变更**。以下为人工审查通过后的运行手册，不表示已执行。仓库按用户明确决定继续 Public：REPOSITORY_PRIVATE=NO_BY_REQUEST；只允许匿名代码/示例，真实 Inventory 和凭据仍必须留在忽略目录或受限服务器。

## 不可变交付

历史 A0 封板为 d827891375cd3665041936890ec21b70f52b2b9c。A1.1 开始时 main 已为 3525a351aa3677b15754541ff13e9f27e5e0685f；本轮仅在 feature/phase1-a1-1-premium-ui 交付，不移动 baseline/phase1-a1、不自动合并。Actions 按 commit SHA 固定；原有六 job 保留，新增 A1 安全及 release 门禁。

获取对应 CI run 的 `crane-production-<full SHA>` artifact，仅包含 release-manifest.json、cloud-api.cdx.json、web-console.cdx.json。清单给出 source_sha、image_name、registry image_digest、sbom_sha256、build_time、schema_version。镜像带 OCI revision/source 标签。Trivy HIGH/CRITICAL 检查不忽略 unfixed 漏洞。

```sh
node deploy/scripts/release-check.mjs /受限交付目录/release-manifest.json /srv/u-dao/backups/crane-ops
# 必须先 checkout 到清单 source_sha 且 git status --porcelain 为空。
# 同时检查 SBOM、digest、实际 DockerRootDir 和明确传入的备份目录文件系统。
# 将输出的 CRANE_CLOUD_IMAGE/CRANE_WEB_IMAGE 显式写入本机 production.env。
docker pull ghcr.io/guolichen007/crane-operations-platform/cloud-api@sha256:实际清单值
docker pull ghcr.io/guolichen007/crane-operations-platform/web-console@sha256:实际清单值
```

服务器不得 build 正式 release，不使用 latest，不用本地测试镜像 ID 代替 OCI registry digest。Dockerfile 基础镜像同时保留可读版本并固定 digest，配置见 deploy/production/images.json。正式 R1 release 必须重新绑定新的 source SHA，不能沿用旧 b8bed8e 镜像。

升级门禁必须成功执行 `docker info --format '{{.DockerRootDir}}'`，对返回目录及 production.env 中 CRANE_BACKUP_ROOT 所在文件系统分别检查 >=10GiB 且 >=15% 空闲。服务器当前 DockerRootDir 为 /srv/u-dao/platform/docker，但代码不硬编码该路径。可选 CRANE_DOCKER_ROOT_DIR 仅作与 daemon 实际目录的一致性断言，不是覆盖值；错误路径、无法确认 daemon 目录、目录不存在或任一磁盘不足均拒绝。CRANE_DISK_PATH 已退役，不能用另一个空闲文件系统代替 Docker data-root。备份目录应事先创建为0700，备份命令的 --root/CRANE_BACKUP_ROOT 必须与门禁所选目录相同。不得修改 Docker daemon 全局配置或清理 Firebot 数据来腾空间。

后续升级统一入口为 `node deploy/scripts/upgrade.mjs --check 清单路径 受限production.env路径`。工具根据自身文件位置绑定实际 Git checkout，要求 `git rev-parse HEAD == manifest.source_sha` 且 `git status --porcelain` 为空；Compose/migrations 来自同一 clean checkout，而非调用者 cwd。核对磁盘、SBOM、env中的digest、五服务集合及 project 后，另行获准的服务器变更窗口才可用 --apply。pull 前和 pull 完成后的 up 前再次检查 HEAD/clean，期间 drift 也拒绝。--check 不更改容器；本轮不执行 --apply。

正式 Compose project 冻结为 **u-dao-crane-ops**。upgrade 和默认备份命令显式指定 --project-name，避免 COMPOSE_PROJECT_NAME 意外形成另一套 volume/network。/srv/u-dao/secrets/crane-ops 与 /srv/u-dao/backups/crane-ops 是既有目录路径，不是 Compose project 名，不做服务器目录迁移。

磁盘 preflight 只支持已确认的本机 Docker daemon（Unix socket/npipe），拒绝 SSH/TCP 远程 context，防止把本机同名路径的空闲空间误当成远程 Docker 文件系统。

## Secret 与数据准备

由受控服务器管理员运行 `bash deploy/scripts/provision-secrets.sh /srv/u-dao/secrets/crane-ops`。目标目录绝对路径、0700；六份随机 secret 0600，已存在时不覆盖。PG admin 为 root；Keycloak 两份 secret UID1000；Cloud app DB/OIDC/Session secret UID10001。Compose file-backed secret 在非 Swarm 下保留宿主机文件 ownership，应核验而非依赖 YAML uid 声明。Windows 本地用匿名测试 named volume 模拟 Linux 权限，不是服务器权限豁免。

复制 production.env.example 为被忽略的 production.env，chmod0600，只填非敏感 origin/路径、已核验 digest 和显式可信代理 CIDR；不内联密码。真实 Inventory/Fleet/ACL 放受限目录且不进入 Git/Docker context。私有证书需由后续正式 PKI 流程交付，本阶段不签发生产设备证书；已知 Development CA 被拒绝。因此真实设备入口仍不开放。

数据库为单个 Crane PG server 内两个隔离 DB/user：crane_app、crane_keycloak。初始化脚本从文件读取密码，通过受控 stdin 创建 DB/role，禁止将包含密码的 SQL 写入日志。与 Firebot PostgreSQL 没有 volume/network 共用关系。初始化仅在空数据卷时执行，凭据轮换须单独流程，不能通过重新生成文件冒充数据库密码已同步。

## 启动与身份初始化（需后续授权）

腾讯云 shared ingress 是独立容器 u-dao-ingress-nginx-1，其 localhost 属于容器自身。正式入口必须通过 external frontend 的 Docker DNS 访问 Crane，不得用宿主 loopback upstream。以下所有服务器操作均须另行授权，本轮没有执行：

1. 先检查现有 Docker network/IPAM、Firebot/ingress 路由及地址段；为 u-dao-crane-frontend 选择经审核且无冲突的 subnet。已有同名网络必须核验 driver、subnet、连接者与所有权，不覆盖重建；没有时由运维显式创建 bridge external network。禁止在文档中照抄任意固定 subnet。
2. 在受限 production.env 显式设置 CRANE_FRONTEND_NETWORK=u-dao-crane-frontend、该网络实际的可信代理 CIDR、固定镜像与私有路径。Crane Compose 的 frontend.external=true，backend.internal=true；Compose 不拥有 shared network，down 不删除它。
3. 仅启动 PostgreSQL、Keycloak、Mosquitto，并初始化 realm/client。不得整体 up Cloud/Web。Keycloak 可以先启动，BFF 此时尚未执行 HTTPS discovery。
4. 经批准把 shared ingress 加入该 network；若尚未连接，可运行 `docker network connect u-dao-crane-frontend u-dao-ingress-nginx-1`，并在 shared ingress 的受控 Compose 配置中声明此 external network，确保重建后仍连接。此步骤属于运维窗口，不是本仓库自动操作。
5. 在 shared ingress http context 包含 crane-http-common.conf.example，审核证书、DNS/TLS 前置条件及 Crane server 模板，再执行 `docker exec u-dao-ingress-nginx-1 nginx -t`；通过后按既有共享入口流程 reload，禁止 down/recreate 整套入口。变量 upstream 与 resolver 127.0.0.11 允许 Cloud/Web 缺席时校验配置；它不免除证书校验。
6. 验证 `https://crane.youdaozd.com/realms/crane/.well-known/openid-configuration`，正常校验证书链、hostname、精确 issuer=https://crane.youdaozd.com/realms/crane，并确认静态资源路由与公网管理/health/metrics 拒绝。失败则停止，不能通过关闭 TLS 校验启动 Cloud。
7. 执行 Firebot HTTP/health 回归，核对共享入口既有路由可用。失败则按入口受控配置回滚，不操作 Firebot 容器、网络或数据。
8. 再依次启动 Cloud API、Web Console；执行 OIDC/CSRF/项目授权/LIVE/WS 验收、匿名或受控双库备份与独立恢复演练，然后按私下批准计划创建业务身份和逐项目授权。

每个正式 Compose 命令显式使用 `--project-name u-dao-crane-ops --env-file deploy/compose/production.env -f deploy/compose/docker-compose.prod.yml`。第七步通过前只启动指定基础服务，不能无 service 范围整体 up。Keycloak 为26.8.1，固定 digest 见 deploy/production/images.json；正式 `start`，strict hostname、受限 proxy headers、内部9000 health/metrics。禁止 start-dev。

realm.template.json 不包含客户端 secret、用户或密码。一次性运行镜像中的 provision-keycloak：KEYCLOAK_ADMIN_URL 为本机 loopback 或隔离网络的 http://keycloak:8080，或受信 HTTPS；PUBLIC_URL 为精确业务 origin；以 KEYCLOAK_BOOTSTRAP_PASSWORD_FILE/OIDC_CLIENT_SECRET_FILE/KEYCLOAK_REALM_TEMPLATE 传入只读文件路径。管理员 CLI 的临时 token 只在内存；自动创建/更新 crane realm 与精确 Code+PKCE client，不创建用户/项目授权。仅此受控一次性进程可读 bootstrap 与 OIDC 两份 secret；Cloud 容器绝不挂载 bootstrap secret。操作后收回 bootstrap 使用权限并按 Keycloak 管理流程移除临时管理员。

完成单域名 crane.youdaozd.com 在共享 ingress 的 HTTPS，并将 /realms/ 与 /resources/ 经 keycloak:8080 转发，/auth/、/api/ 与 / 经 web-console:8080 转发，验证 discovery 后 Cloud 才能启动。正式模板使用变量 proxy_pass 与 resolver 127.0.0.11 valid=5s ipv6=off，容器 recreate 后按 DNS 更新，不在 nginx startup 固化服务 IP。不得使用 curl -k、InsecureSkipVerify、HTTP issuer 或 localhost fallback 绕过 strict HTTPS discovery。共享 ingress 永远拥有宿主80/443；Web 的127.0.0.1:18081、Keycloak 的127.0.0.1:18082仅作宿主运维 fallback，shared ingress 不使用它们。模板的 REVIEW_REQUIRED 证书路径必须人工替换；本轮不签发真实域名证书。

管理员在 Keycloak 管理界面创建实际账号并获取不可变 sub，再以业务 DB secret 文件执行镜像内 identity：

```sh
identity bind --id 本地用户ID --issuer https://crane.youdaozd.com/realms/crane --subject 已核验sub --display-name 显示名 --enable --actor 已授权管理员
identity grant --id 本地用户ID --project 明确项目ID --role platform_admin --actor 已授权管理员
identity list-grants --id 本地用户ID
# 必要时 identity revoke --id 本地用户ID --project 明确项目ID --actor 已授权管理员
```

APP_ENV=production、password-free DATABASE_URL、DATABASE_PASSWORD_FILE 必须显式设置。所有授权必须是逐项目显式 grant；首次授权范围来自负责人批准的私下部署计划，可以包含多个项目，必须逐项核对 list-grants 与 audit。email 不作为 identity key，用户名无权限特判，platform_admin 不能绕过 Project Scope。真实账号、初始密码和实际身份/授权清单禁止放进 README、docs、fixture、CI、Git history 或 artifact。

正式 realm 模板明确启用 UPDATE_PASSWORD required action。创建管理员业务身份时，通过受控 Keycloak 管理界面/API 将初始凭据设为 Temporary，并为该用户添加 UPDATE_PASSWORD；首次登录应先出现改密页，完成前业务 /auth/session 必须仍未认证。初始密码只经负责人批准的私下渠道交付，不写公共模板或命令示例。provision-keycloak 只配置 realm/client，不创建业务账号。CI 使用匿名身份与运行时随机密码验证该流程，未导入真实部署计划。

## 网络、日志与回滚

默认五服务；device-simulator 只有额外 simulation overlay 加 --profile simulation 才存在。remote-gateway 与 pointcloud 不在生产启动集合；不公开 MQTT/Edge 管理端口。共享入口覆盖 Host/XFF/XFP/XFH/Forwarded；信任 CIDR 必须包含真实入口及 Web 代理的受控网段，禁止通配。应用固定 PUBLIC_URL，不能用客户端 Host 改写回调。

PostgreSQL、Mosquitto 不连接 external frontend；Cloud API 无宿主端口发布。shared network 只允许明确审核的容器加入，防止服务别名碰撞或无关容器接触上游。禁止 `docker compose down` shared ingress、`docker network rm` shared network，禁止把 Crane 18081/18082 发布到公网。Crane down 只管理自己的容器、backend 和卷，不承担 shared network 生命周期；不修改 Firebot network/volume/logging。

交付报告使用 HISTORICAL_A0_BASE_SHA、A0_BASELINE_SHA、A1_BASELINE_SHA、CURRENT_MAIN_SHA、A1_1_DELIVERY_SHA 分别标识阶段与当前状态，不再以 A0_MAIN_SHA 表示当前 main。

业务安全头包含 CSP、frame-ancestors、nosniff、no-referrer、Permissions-Policy；HSTS 只对当前子域，绝不 includeSubDomains/preload。Keycloak 保留自身登录 CSP，入口额外限制 framing。回调、认证/API 代理访问日志关闭，其他日志不含 query/cookie/Referer；五服务日志各10m×5。不要在诊断命令中输出 secrets、env、完整 Inventory 或 Keycloak token。

升级前完成双库备份与演练、记录原 image digests/source/schema。应用回滚只切换已审查的 A1 digest，并确认 DB schema 兼容；00004 为 forward-only，不能自动 down，也不能把 A0 development token 版当生产身份回滚目标。必要数据库恢复属于另行审批的事故流程；drill 工具只允许临时 DB，不能恢复覆盖正式库。

官方依据：[Keycloak reverse proxy](https://www.keycloak.org/server/reverseproxy)、[hostname](https://www.keycloak.org/server/hostname)、[Admin REST API](https://www.keycloak.org/docs-api/latest/rest-api/index.html)、[Trivy SBOM](https://trivy.dev/latest/docs/supply-chain/sbom/)。
