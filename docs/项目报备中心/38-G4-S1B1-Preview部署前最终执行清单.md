# 38 G4-S1B1 Preview 部署前最终执行清单（R1：已合入独立审查修订与实际预检结果）

本清单用于 G4-S1B1 实际部署，取代 37 号文档第 5 节的执行顺序。R1 版逐项复核并合入了独立审查的 9 项修订（§10），并加入 2026-11-11 的实际预检结果与资源就绪矩阵（§1、§9）。**本清单不代表任何部署已获授权。**架构与隔离设计不在此重复，见 35、36 号文档。**本批没有修改任何服务器、Keycloak、DNS、证书、Ingress 或发布设置。**

## 0. 版本对应关系

| 项 | 值 |
| --- | --- |
| RELEASE_SHA（唯一发布 SHA） | `572c6aa609e06419b832f7652e73bf29e8c65c37`。Run `42930430139`，Attempt 1，八项全部 success，head_sha 一致（GitHub API，2026-11-11）。release ref、镜像 OCI revision、deployment-manifest 的 `source_sha` 和部署检出都必须等于它 |
| CODE_SHA（最后一次代码变更） | `ca41c3ba090a7dc17120f6565d0a54a2f8d644fd`（Run `42812452001`，Attempt 1，8/8）。`ca41c3b..572c6aa` 只改了 `docs/` 与 `AGENTS.md`，应用、测试和部署文件完全相同，所以以 572c6aa 发布不会改变代码 |
| DOC_SHA（本清单 R1 所在提交） | 包含本次修订的那个提交（只改文档，SHA 见交付回执）。它是 RELEASE_SHA 的后代，**不是**发布 SHA；本次提交之后远端 HEAD 将是 DOC_SHA，572c6aa 成为它的祖先 |
| 冻结规则 | 发布前只允许上线必需的修复。任何代码改动都要产生新的 SHA、重新通过八项 CI，并以新的 RELEASE_SHA 重走 §4；仅文档提交不改变 RELEASE_SHA |

## 1. 现状汇总（2026-11-11 实际核对）

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 代码 | RELEASE_SHA 八项 CI 通过；Preview 运行模式、环境标记、独立 Compose、静态门、备份区分、合成数据完整性都在其中 | Run 42930430139；36、37 号文档 |
| 服务器只读预检 | **SERVER_PRECHECK=NOT_RUN**。原因：本执行环境没有任何经授权的服务器访问通道（无 SSH 密钥、账号或跳板），本轮也未收到管理员执行 §3 后回传的脱敏结果 | — |
| 正式 Baobei（生产） | **已在运行，不是 NOT_DEPLOYED**（公开只读核对）：`https://baobei.youdaozd.com/health/ready` 返回 200 `{"ok":true}`；`/auth/config` 返回 `development:false, oidc:true`；证书 CN=baobei.youdaozd.com（Let's Encrypt，2026-11-04 至 2027-02-02）。页面脚本 `index-eOgzRyfi.js` 与本地按 `a4f488a`（2026-11-04）构建的产物同名，说明现网 Web 来自该提交，比 RELEASE_SHA 落后 36 个提交；API 版本无法从外部确定。**因此 §3 的健康基线必须包含正式 Baobei，Preview 部署不得影响它。** 现网更新不属于本批范围 | `evidence/s1b1-r1-live-baobei.txt` |
| 主机 | `baobei.youdaozd.com` 与 `crane.youdaozd.com` 都只有 A 记录 124.223.220.182，无 AAAA；两者共用一台主机和共享 ingress（推断，需 §3 核实） | 公开 DNS |
| 生产隔离现状 | 仅从仓库静态推断（生产 Compose 无宿主端口，internal 网络 `internal: true`），**未经现场核实** | 35 号 §1 |
| GHCR 镜像 | **PREVIEW_IMAGES_READY=NO**：远端没有 `release/baobei-publish-*` 分支；仓库 Environments 为空；Actions 中从未运行过 Baobei GHCR Publish。GHCR 匿名令牌请求返回 403，**只能说明无法匿名拉取**，不能单凭它证明 Package 不存在；但没有发布运行，就没有与 RELEASE_SHA 匹配的 digest 和 manifest | `evidence/s1b1-r1-public-checks.txt` |
| Preview 域名 | `baobei-preview.youdaozd.com` 无 A、无 AAAA 记录 | 同上 |
| Keycloak | `realms/crane` 200；`realms/baobei-preview` 404（不存在） | 同上 |
| 证书 | 签发方式未定 | — |

## 2. 上线前必需资源与责任人

| # | 资源 | 责任人 | 验收标准 | 状态 |
| --- | --- | --- | --- | --- |
| R1 | G4-S1B1 书面部署授权，注明冻结 SHA | 负责人 | 授权记录 | 未提供 |
| R2 | 服务器只读预检通道与独立操作账号。**禁止为预检给普通账号授予完整 Docker 权限。**只读采集优先由可信管理员执行预设只读命令、导出脱敏结果；**不可把加入 docker 组的账号称为技术上的只读账号**。正式操作账号需单独授权 | 服务器管理员 | 预检命令、采集人、权限范围和证据已登记；无非授权写操作 | 未提供 |
| R3 | GHCR 发布：Environment、审批人、release ref、Package 设为 Public | 仓库管理员 + 审批人 | §4 全部通过，得到 deployment-manifest | 未开始 |
| R4 | DNS：`baobei-preview.youdaozd.com` 的 A 记录（以及 AAAA，若开放 IPv6），或决定仅内网访问 | 域名管理员 | `dig` 返回批准的地址 | 不存在 |
| R5 | 证书：签发方式（人工 TrustAsia、独立 ACME 或通配符证书）及存放路径 | 负责人 + 服务器管理员 | §5.3 | 未定 |
| R6 | 访问白名单：IPv4 CIDR 列表；IPv6 是否开放及对应 CIDR | 负责人 | 书面列表 | 未提供 |
| R7 | Keycloak：Preview realm、client、四个角色、七个合成账号、mapper、scope | Keycloak 管理员 | §5.1 Evaluate 结果 | 不存在 |
| R8 | secret 文件（§5.2） | 服务器管理员（写入）、负责人（核对权限） | `stat` 与 `sha256sum` 对照（不显示内容） | 未创建 |
| R9 | 合成身份清单（真实 sub 替换示例值） | Keycloak 管理员 + 负责人审批 | identity-apply dry-run 输出 | 未创建 |
| R10 | 真人 UAT 四角色执行人与签字人 | 负责人 | UAT 脚本签字 | 未指派 |

## 3. 服务器只读预检（执行人：持 R2 只读账号的管理员）

**执行前提：** R2 已提供。此阶段不执行写操作；不读取、不输出、不复制 secret 明文。Docker daemon/socket 通常具有 root 等价权限：通过有权限的可信管理员执行限定的只读采集命令，并回传脱敏报告，不把“可运行 docker 命令”错误等同于内核级只读权限。

**禁止操作：** `up`/`down`/`restart`、`network create`、数据库写入、`nginx -s reload`。

```bash
# 系统与资源
uname -a; cat /etc/os-release; uptime; free -h; df -h / /srv /var/lib/docker
docker version --format '{{.Server.Version}}'; docker compose version
# 现有容器与健康（生产基线）
docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Networks}}'
docker compose ls
docker info --format 'data-root={{.DockerRootDir}} driver={{.Driver}}'
# 健康基线：记录每个运行中容器的 health 状态，以及对外服务的结果。
# 2026-11-11 公开核对显示正式 Baobei 已在运行，必须纳入基线；若现场发现未部署，记 BAOBEI_PRODUCTION=NOT_DEPLOYED，不要求 200
docker ps --format '{{.Names}}\t{{.Status}}' | sort
curl -fsS -o /dev/null -w 'baobei %{http_code}\n' https://baobei.youdaozd.com/health/ready
curl -fsS -o /dev/null -w 'crane %{http_code}\n' https://crane.youdaozd.com/
curl -fsS -o /dev/null -w 'keycloak %{http_code}\n' https://crane.youdaozd.com/realms/crane/.well-known/openid-configuration
# 端口
ss -ltnp | grep -E ':(80|443|5432|8080|8088)\b'
# 网络与成员
docker network ls
for n in $(docker network ls --format '{{.Name}}' | grep -E 'u-dao|baobei|crane|firebot|ingress'); do
  docker network inspect "$n" --format '{{.Name}} internal={{.Internal}} {{range .Containers}}{{.Name}} {{end}}'; done
# 只查 Baobei Preview 资源，不得以其他项目（如 B4 Preview）的存在为理由拒绝部署
docker ps -a --format '{{.Names}}' | grep -E '(^|[-_])baobei-preview([-_]|$)' || true
docker network ls --format '{{.Name}}' | grep -E '^baobei-preview([-_]|$)' || true
docker volume ls --format '{{.Name}}' | grep -E '^baobei-preview([-_]|$)' || true
# Docker IPAM 子网与宿主路由、办公网、VPN/Tailscale 冲突检查
for n in $(docker network ls -q); do docker network inspect "$n" --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} gw={{.Gateway}} {{end}}'; done
cat /etc/docker/daemon.json 2>/dev/null | grep -E 'default-address-pools|bip' || echo 'daemon.json: no custom address pools'
ip -4 route; ip -6 route 2>/dev/null | head -20
ip -brief addr
command -v tailscale >/dev/null && tailscale status --peers=false 2>/dev/null | head -5
# 办公网与 VPN 网段由负责人书面提供（R6），与上面所有子网逐一比对
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
# GHCR 镜像的 docker pull 会修改本地镜像存储，不属于只读预检。
# 只读阶段只验证 manifest/registry 元数据；实际 docker pull 移至 §4.5 经授权的部署准备阶段。
```

**说明：** 宿主若有 `/srv/u-dao/*/baobei-preview` 之外的其他 Preview 项目（例如 B4），不构成冲突。

**成功标准（全部满足才可进入 §6）：**

1. 可用内存 ≥ 1 GiB，Docker data-root 所在文件系统可用空间 ≥ 10 GiB（建议值；先通过 `docker info` 核实真实 data-root）。
2. 80/443 的实际归属与共享 Ingress 拓扑一致；5432/8080/8088 若被其他已批准服务占用，应记录归属及暴露范围，而不是无条件判失败。**Preview 容器不得新增任何宿主端口映射**。
3. 不存在与本次 `baobei-preview-*` 精确冲突的容器、网络、卷或目录；其他产品的 Preview 资源不构成同名冲突。预先检查 Docker IPAM 子网与现有 Docker/宿主/办公网/Tailscale CIDR 不重叠。
4. 正式 Baobei 已在运行（§1 公开证据）：现场核对其 PostgreSQL 只接入批准的 internal 网络、不发布宿主端口，且将来 Preview 的任何容器都不在该网络中。若现场与公开证据相反、实为未部署，记录 `BAOBEI_PRODUCTION=NOT_DEPLOYED` 并说明公开 200 的来源，不得捏造现网容器。
5. 对当前**实际运行**的 Crane、Keycloak、正式 Baobei、Ingress 及其他受影响服务建立健康基线（容器 health 与对外状态码）。

**停止条件：** 任一成功标准不满足。将输出归档后上报，不进入后续步骤。

**证据存放：** `/srv/u-dao/baobei-preview/evidence/<日期>/precheck/`。该目录只能在获得写授权后创建；在此之前，证据由管理员保存在本地。

## 4. 镜像发布（执行人：仓库管理员与审批人，按原有保护流程）

**现状：** PREVIEW_IMAGES_READY=NO。本批没有创建 release ref、发布镜像或修改 Environment。`publish.py` 的门槛保持不变。

| 步 | 前提 | 操作 | 成功标准 | 停止条件 |
| --- | --- | --- | --- | --- |
| 4.1 | 冻结 SHA 的八项 CI 全部通过 | Settings → Environments 新建 `baobei-ghcr-publish`：Required reviewers 只设一组**直接用户**；开启 Prevent self-review；Deployment branches 选 Selected branches，只添加 `release/baobei-publish-<冻结 SHA>` | 设置截图；`publish.py --preflight` 能通过 Environment 校验 | 审批人只能是团队，或无法开启防自批 |
| 4.2 | 4.1 完成 | 由**非审批人**创建 `refs/heads/release/baobei-publish-<冻结 SHA>`，指向冻结 SHA；禁止 force push | Baobei GHCR Publish 工作流的 preflight 成功 | preflight 失败（ref、祖先关系、CI 或 Environment 任一项） |
| 4.3 | preflight 成功 | 审批人在 Actions 页面批准 publish job（发起人不能自批） | publish 成功：两个镜像的 registry digest 和 OCI revision 等于冻结 SHA，匿名回拉通过 | `PUBLIC_PULL_NOT_VERIFIED`：负责人把 Package 设为 Public；按原发布契约**另行授权删除先前同名 release ref，再重新创建完全相同的 SHA 命名 ref，以新的 create 事件及独立审批重试**。不可复用 rerun 代替新的事件 |
| 4.4 | publish 成功 | 下载 artifact `baobei-registry-deployment-<SHA>` 中的 `deployment-manifest.json` 并核对 `source_sha`、`ci_run_id` 和两个 `*_registry_ref` | manifest 内容与冻结 SHA 一致 | 任何字段不一致 |
| 4.5 | 4.4 完成，且**明确获得镜像拉取这一宿主写操作授权**、服务器只读预检通过 | 服务器上 `docker pull` 两个 digest（会写入本地镜像存储），并 `docker inspect` 核对 RepoDigests 和 OCI revision/source 标签 | 两镜像一致且 source_sha、digest 等于 manifest | 不一致：**禁止**使用其他镜像或临时 digest 替代 |

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
export P=baobei-preview F=deploy/baobei/compose.preview.yaml ENVF=/srv/u-dao/baobei-preview/releases/<RELEASE_SHA>/compose.env
```

- 不使用 `direct-deploy.py`。
- 每一步的输出（不含 secret）都存入证据目录。
- **每次执行 Compose（包括次日恢复、故障 rollback/down）都必须带上已批准的环境文件。** `compose.preview.yaml` 的两个镜像和 secret 目录是 `${VAR:?}` 必填变量，不能依赖当前 shell 里残留的 `export`，也不得在紧急回滚时用伪造的 digest 凑过解析。做法：6.1 完成后，由 manifest 生成 `/srv/u-dao/baobei-preview/releases/<RELEASE_SHA>/compose.env`（root:root 0400），只含三行 `BAOBEI_PREVIEW_API_IMAGE=…`、`BAOBEI_PREVIEW_WEB_IMAGE=…`、`BAOBEI_PREVIEW_SECRETS_DIR=…`，不含任何密码；此后所有 Compose 命令统一写作 `docker compose --env-file "$ENVF" --env-file "$ENVF" -p $P -f $F …`，执行前先与 manifest 比对。

| 步 | 执行前提 | 操作 | 成功标准 | 停止条件 |
| --- | --- | --- | --- | --- |
| 6.1 准备变量与 secret（Gate 1 第 1、2 项） | §3 通过；§4 得到 manifest；§5.2 的文件已就位 | `export BAOBEI_PREVIEW_API_IMAGE=<manifest api_registry_ref>`、`BAOBEI_PREVIEW_WEB_IMAGE=<manifest web_registry_ref>`、`BAOBEI_PREVIEW_SECRETS_DIR=/srv/u-dao/secrets/baobei-preview`；`stat -c '%U:%G %a %n' $BAOBEI_PREVIEW_SECRETS_DIR/*` | 两个镜像变量逐字等于 manifest，格式为 `ghcr.io/guolichen007/baobei-*@sha256:<64 位>`；四个常驻 secret 文件存在且权限符合 §5.2；一次性凭据**不在**该目录 | 任何变量缺失或与 manifest 不符；任何人提议用本地构建、旧版本或临时 digest 替代：一律禁止 |
| 6.2 静态校验 | 6.1 完成 | `docker compose --env-file "$ENVF" -p $P -f $F config --quiet`；`node tests/baobei/preview.mjs` | 两条命令都通过；`PREVIEW_COMPOSE_RENDERED_BY=docker compose config`，42/42 反例被拒绝，`Preview nginx syntax PASS` | 任一失败 |
| 6.3 创建前端网络 | 6.2 通过；IPAM 子网预检完成、无路由冲突 | 按批准的网络 CIDR 及网段规划创建 `baobei-preview-frontend`（不得临时猜测/复用现有子网） | `docker network inspect` 核验名称、子网、Gateway 与批准规划一致 | 同名网络已存在、子网重叠、IPAM 参数异常则停止 |
| 6.4 只启动 PostgreSQL（Gate 1 第 3 项） | 6.3 完成 | `docker compose --env-file "$ENVF" -p $P -f $F up -d --no-deps --wait baobei-preview-postgres` | `docker compose --env-file "$ENVF" -p $P -f $F ps` **只有** `baobei-preview-postgres` 一个容器且为 healthy；api、web 没有被创建 | 出现 api 或 web 容器：立即 `docker compose --env-file "$ENVF" -p $P -f $F stop baobei-preview-api baobei-preview-web` 并上报 |
| 6.5 核对初始化与标记 | 6.4 healthy | `docker compose --env-file "$ENVF" -p $P -f $F exec baobei-preview-postgres psql -U baobei_bootstrap -d baobei -tAc "SELECT name FROM baobei_environment; SELECT rolname FROM pg_roles WHERE rolname LIKE 'baobei_%' ORDER BY 1; SELECT extname FROM pg_extension"` | 标记只有一行 `preview`；五个 baobei 角色（bootstrap、maintenance、migrator、runtime、staff_api）；存在 pg_trgm | 任一项不符：删卷后从 6.4 重来（Preview 库可丢弃） |
| 6.6 设置角色密码 | 6.5 通过 | 在 PG 容器内 psql 中，对 migrator、runtime、maintenance、staff_api 分别执行 `\password <角色>`，值与 §5.2 中的 DSN 一致 | 用四个 DSN 分别执行 `SELECT current_user` 都能连接（只读） | 任一连接失败 |
| 6.7 一次性迁移 | 6.6 通过 | `docker run --rm --read-only --network baobei-preview-internal --security-opt no-new-privileges --env APP_ENV=preview --env DATABASE_URL_FILE=/run/secrets/db_url --mount type=bind,src=<migrator_database_url>,dst=/run/secrets/db_url,readonly "$BAOBEI_PREVIEW_API_IMAGE" maintenance migrate` | 输出 `database schema verified`；public 下有 31 张表 | 输出 `database role, migration or schema verification failed`：停止，核对角色和标记，**不要**换成其他角色重试 |
| 6.8 身份初始化 | 6.7 通过；R7、R9 完成 | 一次性容器（加 `--user 0:0`，maintenance DSN 和 identity.json 只读挂载）执行 `maintenance identity-apply --file /run/baobei/identity.json --operator <批准 ID> --dry-run`；负责人批准后去掉 `--dry-run` 再执行一次 | dry-run：`teams_changed=2`，`users_changed=7`，`dry_run=true`；正式执行的计数相同；第二次执行计数为 0 | 计数不符或报错 |
| 6.9 启动 API 与 Web | 6.8 完成；证书已就位 | `docker compose --env-file "$ENVF" -p $P -f $F up -d --no-build --pull missing --wait` | 三个服务都为 healthy；`docker ps` 中 Ports 列为空；`baobei-preview-internal` 成员只有 api 和 pg；`u-dao-baobei-internal` 中没有 preview 容器；RepoDigests 等于 manifest | 任一不符：`docker compose --env-file "$ENVF" -p $P -f $F stop baobei-preview-web baobei-preview-api` 并上报 |
| 6.10 Ingress | 6.9 通过；R4、R5、R6 完成 | 把 ingress 加入 `baobei-preview-frontend` 网络；安装 Preview vhost（§5.3 白名单）；先 `nginx -t`，通过后再 reload | §3 采集的所有**已存在服务**健康状态保持正常（Baobei 未部署则为 N/A）；Preview 白名单及 HTTPS 验收通过 | `nginx -t` 失败（不执行 reload）；或已有服务出现由变更导致的异常：按 §7 的 Ingress 回滚 |
| 6.11 真实 OIDC 登录 | 6.10 通过 | 七个账号各登录一次 | 都能进入工作台；Cookie 为 `__Host-baobei_session` 且带 Secure；角色与 §5.1 一致；未开通账号得到 `LOGIN_NOT_PROVISIONED` | 任一账号的角色或范围不符 |
| 6.12 合成数据 | 6.11 通过；已准备**可信的合成数据执行端**（优先批准的内网/VPN 运维工作站），在 RELEASE_SHA 的检出中执行 `npm ci --prefix apps/project-intake-web` 与 `npx --prefix apps/project-intake-web playwright install chromium`（seed.mjs 通过 `apps/project-intake-web` 的依赖加载 Playwright）；执行端出口地址在白名单内；合成账号凭据安全可读 | 在被批准的执行端运行 `BAOBEI_SEED_ORIGIN=https://baobei-preview.youdaozd.com BAOBEI_SEED_ACCOUNTS_FILE=<seed-accounts.json> node tests/baobei/preview/seed.mjs`，然后再运行一次。不得默认服务器已安装 Node/Chromium，更不能为 seed 随意修改共享服务器 | 第一次输出 `seeded`；第二次输出 `complete`，不写入；执行端不记录密码或 token | 依赖缺失、无法访问、或输出 `inconsistent`：停止；经负责人确认后才能重建隔离库 |
| 6.13 备份与恢复演练 | 6.12 通过；受控运维执行端具备 Node、Docker 客户端和 PostgreSQL 容器访问权限，且已完成专用备份目录与空间预检；执行目录为 RELEASE_SHA 的干净检出（manifest 中的 `source_sha` 取自 `git rev-parse HEAD`） | 设置 `BAOBEI_BACKUP_ENV=preview`、`BAOBEI_POSTGRES_CONTAINER=<preview pg 容器 ID>`、`BAOBEI_BACKUP_USER=baobei_bootstrap`、`BAOBEI_BACKUP_DIR=/srv/u-dao/baobei-preview/backups`，执行 `node tests/baobei/backup.mjs backup`，再用 `BAOBEI_BACKUP_VERIFY=1` 运行一次；备份程序与 Docker Engine 必须指向同一批准主机，禁止直接回退到默认 dev Compose 容器 | `RESTORE_TEST=PASS; environment=preview; 31 table counts` | 缺少 Node/容器权限、容器 ID 不属于批准的 `baobei-preview` 项目或恢复失败 |
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
| 应用 | 先从已验证的部署 manifest 恢复完整 `BAOBEI_PREVIEW_API_IMAGE`、`BAOBEI_PREVIEW_WEB_IMAGE`、`BAOBEI_PREVIEW_SECRETS_DIR` 环境变量，核对 `-p baobei-preview` 和确切 Compose 文件，再执行 `docker compose --env-file "$ENVF" -p baobei-preview -f deploy/baobei/compose.preview.yaml down`（不加 `-v`，保留卷以便排查）；不得删除 Secret 后才执行 down | 只影响 Preview |
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

## 9. 资源就绪矩阵与结论（2026-11-11）

| 条件（Gate 5） | 状态 | 证据 / 阻断原因 | 下一步由谁做什么 |
| --- | --- | --- | --- |
| RELEASE_SHA 八门 CI | **满足** | 572c6aa，Run 42930430139 Attempt 1 8/8 | — |
| 服务器只读预检 | **NOT_RUN** | 无授权访问通道，也未收到管理员回传结果 | 服务器管理员：以有 Docker 权限的可信身份执行 §3 命令，回传脱敏输出 |
| 正式 Baobei 健康基线 | 公开端点 200（不是 NOT_DEPLOYED） | `s1b1-r1-live-baobei.txt` | 服务器管理员：在 §3 中补齐容器级 health |
| 镜像可拉取，manifest 可验证 | **不满足** | 无发布 ref、无 Environment、无发布运行 | 仓库管理员：§4.1 建 Environment；非审批人：§4.2 建 ref；审批人：§4.3 批准；负责人：必要时设 Package 为 Public |
| 数据库、网络、凭据隔离与现场一致 | 未核实 | 依赖 §3 | 服务器管理员（§3 第 3、4 条及 IPAM 检查） |
| DNS | **不满足** | 无 A、无 AAAA | 域名管理员：添加 `baobei-preview.youdaozd.com` A 记录（124.223.220.182，或负责人指定地址）；负责人：决定是否开放 IPv6（不开放则不建 AAAA） |
| 证书 | **不满足** | 签发方式未定 | 负责人：决定签发方式；服务器管理员：存放到 `/srv/u-dao/platform/ingress/tls/baobei-preview/` |
| 白名单 | **不满足** | 未提供 CIDR | 负责人：书面给出 IPv4（如开放还要 IPv6）allow CIDR 及办公网、VPN 网段 |
| OIDC | **不满足** | realm 404 | Keycloak 管理员：按 §5.1 建 realm、client、四角色、mapper、七账号，回传 Evaluate 截图和七个 sub |
| 数据库与 OIDC secret | **不满足** | 未创建 | 服务器管理员：按 §5.2 生成文件并回传 `stat` 结果 |
| 身份清单与审批 | **不满足** | 依赖 Keycloak 的 sub | Keycloak 管理员与负责人：用真实 sub 替换示例，审批编号登记 |
| 部署与回滚授权 | **不满足** | 未授权 | 负责人：书面授权 G4-S1B1（注明 RELEASE_SHA） |

```
RELEASE_SHA=572c6aa609e06419b832f7652e73bf29e8c65c37
CODE_SHA=ca41c3ba090a7dc17120f6565d0a54a2f8d644fd
SERVER_PRECHECK=NOT_RUN
PREVIEW_IMAGES_READY=NO
G4_S1B1_DEPLOY_READY=NO
PREVIEW_DEPLOYED=NO
PRODUCTION_DEPLOYED=NO
```

## 10. R1 审查九项的复核结论

| # | 审查意见 | 复核结论 | 处理 |
| --- | --- | --- | --- |
| 1 | 代码、文档、发布 SHA 对应 | 成立：原稿把 ca41c3b 称为冻结候选，但远端 HEAD 已是 572c6aa | §0 定 RELEASE_SHA=572c6aa（已 8/8 且代码与 ca41c3b 相同），并分别记录 CODE_SHA 和 DOC_SHA |
| 2 | `docker pull` 移出只读阶段 | 成立：pull 会写入本地镜像存储 | §3 删除 pull；§4.5 需写授权 |
| 3 | 不能强制正式健康 200 | 成立；同时公开核对显示正式 Baobei 实际已在运行 | §3 改为按实际部署状态建立基线，现网已部署所以纳入 |
| 4 | 资源冲突只查 baobei-preview 名称 | 成立：原 `grep -i preview` 会误伤其他 Preview 项目 | §3 改为精确匹配 |
| 5 | 增加 IPAM 与路由冲突检查 | 成立：6.3 需手工创建 external 网络 | §3 增加 IPAM、daemon 地址池、路由、Tailscale 命令；6.3 按批准网段创建 |
| 6 | Docker socket 不是只读 | 成立：docker 组等价于 root | R2 与 §3 改写，并禁止为预检授予 docker 权限 |
| 7 | seed 执行端需要 Node、Playwright、Chromium | 成立：seed.mjs 通过 `apps/project-intake-web` 加载 Playwright | 6.12 写明 `npm ci` 与 `playwright install chromium`，以及执行端出口需在白名单内 |
| 8 | Compose 与回滚须恢复环境变量 | 成立：三个变量都是 `${VAR:?}` 必填 | 引入 root-only `compose.env`，所有 Compose 命令统一用 `--env-file` |
| 9 | Private Package 重试须删除并重建 ref | 成立：`publish.py` 要求 ref 为 created 事件、首次 attempt，ref 名固定为 SHA | 4.3 写明另行授权删除旧 ref、重新创建并重新独立审批，不可 rerun |

