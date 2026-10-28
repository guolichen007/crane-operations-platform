# Phase1-A1 生产部署手册

本阶段只交付代码、CI、镜像与模板，**没有授权服务器部署、真实 DNS/TLS 或 Firebot 变更**。以下为人工审查通过后的运行手册，不表示已执行。仓库按用户明确决定继续 Public：REPOSITORY_PRIVATE=NO_BY_REQUEST；只允许匿名代码/示例，真实 Inventory 和凭据仍必须留在忽略目录或受限服务器。

## 不可变交付

main 已封板至 d827891375cd3665041936890ec21b70f52b2b9c，annotated tag baseline/phase1-a0 的 peeled SHA 相同。A1 位于 feature/phase1-a1-production-web，不自动合并。Actions 按 commit SHA 固定；原有六 job 保留，新增 A1 安全及 release 门禁。

获取对应 CI run 的 `crane-production-<full SHA>` artifact，仅包含 release-manifest.json、cloud-api.cdx.json、web-console.cdx.json。清单给出 source_sha、image_name、registry image_digest、sbom_sha256、build_time、schema_version。镜像带 OCI revision/source 标签。Trivy HIGH/CRITICAL 检查不忽略 unfixed 漏洞。

```sh
node deploy/scripts/release-check.mjs /受限交付目录/release-manifest.json
# 校验 SBOM hash、source SHA、registry 名称、digest，同时执行磁盘门禁。
# 将输出的 CRANE_CLOUD_IMAGE/CRANE_WEB_IMAGE 显式写入本机 production.env。
docker pull ghcr.io/guolichen007/crane-operations-platform/cloud-api@sha256:实际清单值
docker pull ghcr.io/guolichen007/crane-operations-platform/web-console@sha256:实际清单值
```

服务器不得 build 正式 release，不使用 latest，不用本地测试镜像 ID 代替 OCI registry digest。升级前对 /var/lib/docker 和备份所在文件系统分别执行 disk-check（默认 >=10GiB 且 >=15%）；不够则停止。不得修改 Docker daemon 全局配置或清理 Firebot 数据来腾空间。

后续升级统一入口为 `node deploy/scripts/upgrade.mjs --check 清单路径 受限production.env路径`，先核对磁盘、SBOM、SHA、env中的digest及五服务集合；全部满足后，另行获准的服务器变更窗口才可用 --apply。脚本在任何 pull/up 前执行门禁，--check 不更改容器；本轮不执行 --apply。首次初始化须先完成下文分步的数据库、IdP、信任材料和 ingress 准备。

## Secret 与数据准备

由受控服务器管理员运行 `sh deploy/scripts/provision-secrets.sh /srv/u-dao/secrets/crane-ops`。目标目录绝对路径、0700；六份随机 secret 0600，已存在时不覆盖。PG admin 为 root；Keycloak 两份 secret UID1000；Cloud app DB/OIDC/Session secret UID10001。Compose file-backed secret 在非 Swarm 下保留宿主机文件 ownership，应核验而非依赖 YAML uid 声明。Windows 本地用匿名测试 named volume 模拟 Linux 权限，不是服务器权限豁免。

复制 production.env.example 为被忽略的 production.env，chmod0600，只填非敏感 origin/路径、已核验 digest 和显式可信代理 CIDR；不内联密码。真实 Inventory/Fleet/ACL 放受限目录且不进入 Git/Docker context。私有证书需由后续正式 PKI 流程交付，本阶段不签发生产设备证书；已知 Development CA 被拒绝。因此真实设备入口仍不开放。

数据库为单个 Crane PG server 内两个隔离 DB/user：crane_app、crane_keycloak。初始化脚本从文件读取密码，通过受控 stdin 创建 DB/role，禁止将包含密码的 SQL 写入日志。与 Firebot PostgreSQL 没有 volume/network 共用关系。初始化仅在空数据卷时执行，凭据轮换须单独流程，不能通过重新生成文件冒充数据库密码已同步。

## 启动与身份初始化（需后续授权）

先以精确 Compose project crane-ops 启动 postgres、keycloak、mosquitto。Keycloak 镜像为26.8.1，固定 digest 见 deploy/production/images.json；正式 `start`，strict hostname、受限 proxy headers、内部9000 health/metrics，8080只映射127.0.0.1。禁止 start-dev。

realm.template.json 不包含客户端 secret、用户或密码。一次性运行镜像中的 provision-keycloak：KEYCLOAK_ADMIN_URL 为本机 loopback 或隔离网络的 http://keycloak:8080，或受信 HTTPS；PUBLIC_URL 为精确业务 origin；以 KEYCLOAK_BOOTSTRAP_PASSWORD_FILE/OIDC_CLIENT_SECRET_FILE/KEYCLOAK_REALM_TEMPLATE 传入只读文件路径。管理员 CLI 的临时 token 只在内存；自动创建/更新 crane realm 与精确 Code+PKCE client，不创建用户/项目授权。仅此受控一次性进程可读 bootstrap 与 OIDC 两份 secret；Cloud 容器绝不挂载 bootstrap secret。操作后收回 bootstrap 使用权限并按 Keycloak 管理流程移除临时管理员。

完成业务域/auth 域在共享 ingress 的 HTTPS 后，Cloud 才能严格 HTTPS discovery；本轮仅提交 deploy/ingress/*.example。不得用关闭证书校验来绕过未部署的 TLS。共享 ingress 保持宿主80/443所有权，Crane Web仅127.0.0.1:18081、Keycloak仅127.0.0.1:18082。模板的 REVIEW_REQUIRED 证书路径必须人工替换；模板不自签真实域名证书。

管理员在 Keycloak 管理界面创建实际账号并获取不可变 sub，再以业务 DB secret 文件执行镜像内 identity：

```sh
identity bind --id 本地用户ID --issuer https://auth.crane.youdaozd.com/realms/crane --subject 已核验sub --display-name 显示名 --enable --actor 已授权管理员
identity grant --id 本地用户ID --project 明确项目ID --role platform_admin --actor 已授权管理员
identity list-grants --id 本地用户ID
# 必要时 identity revoke --id 本地用户ID --project 明确项目ID --actor 已授权管理员
```

APP_ENV=production、password-free DATABASE_URL、DATABASE_PASSWORD_FILE 必须显式设置。首次管理员只授予一个项目，不授全部九项目；email 不作为 identity key。实际身份/授权信息禁止粘贴到 Public 仓库。

## 网络、日志与回滚

默认五服务；device-simulator 只有额外 simulation overlay 加 --profile simulation 才存在。remote-gateway 与 pointcloud 不在生产启动集合；不公开 MQTT/Edge 管理端口。共享入口覆盖 Host/XFF/XFP/XFH/Forwarded；信任 CIDR 必须包含真实入口及 Web 代理的受控网段，禁止通配。应用固定 PUBLIC_URL，不能用客户端 Host 改写回调。

业务安全头包含 CSP、frame-ancestors、nosniff、no-referrer、Permissions-Policy；HSTS 只对当前子域，绝不 includeSubDomains/preload。Keycloak 保留自身登录 CSP，入口额外限制 framing。回调、认证/API 代理访问日志关闭，其他日志不含 query/cookie/Referer；五服务日志各10m×5。不要在诊断命令中输出 secrets、env、完整 Inventory 或 Keycloak token。

升级前完成双库备份与演练、记录原 image digests/source/schema。应用回滚只切换已审查的 A1 digest，并确认 DB schema 兼容；00004 为 forward-only，不能自动 down，也不能把 A0 development token 版当生产身份回滚目标。必要数据库恢复属于另行审批的事故流程；drill 工具只允许临时 DB，不能恢复覆盖正式库。

官方依据：[Keycloak reverse proxy](https://www.keycloak.org/server/reverseproxy)、[hostname](https://www.keycloak.org/server/hostname)、[Admin REST API](https://www.keycloak.org/docs-api/latest/rest-api/index.html)、[Trivy SBOM](https://trivy.dev/latest/docs/supply-chain/sbom/)。
