# B4 Preview 隔离部署执行方案（仅准备，未获授权不部署）

> 承接《Phase1-A1.1-R2-B4-Preview部署方案.md》，落实为可审查、可部署、可回滚的隔离环境清单。
> 本方案分两条基线记录：**R3 冻结基线** `cab1c20053596b9c85d43d90271c8c48bfc7e2f3` 与**当前候选 HEAD**（长期分支最新提交，以 `git rev-parse origin/feature/phase1-a1-1-r2-asset-operations` 为准；本文不写死，避免过时）。部署时镜像 digest 必须对应部署当刻核对过的候选 SHA。
> 未获服务器变更授权前，不执行部署、不改 DNS/TLS/Ingress/Keycloak/PG。

## 1. 环境边界（与既有基座一致，不假定全新环境）

| 项 | Preview | Production |
| --- | --- | --- |
| Compose 项目名 | `crane-b4-preview` | `u-dao-crane-ops` |
| 域名 | `b4.crane.youdaozd.com` | `crane.youdaozd.com` |
| 网络 | 既有 `u-dao-crane-b4-preview-frontend` + `u-dao-crane-b4-preview-api-frontend` + 内部 `backend` | `u-dao-crane-frontend` + 内部 `backend` |
| 预留子网 | `10.240.40.0/24`（部署前 CC 复核） | 既有子网 |
| 端口 | 18083（web）/ 18085（auth）；无自建 ingress、无 18443 | 18081 / 18082 / 443 |
| 数据卷 | `b4-preview-postgres` / `b4-preview-mqtt` | `crane-postgres` / `crane-mqtt` |
| Secrets | `/srv/u-dao/secrets/crane-b4-preview/` | `/srv/u-dao/secrets/crane-ops/` |
| 数据库 | 独立 `crane_app` / `crane_keycloak` | 正式库 |
| OIDC | 既有 realm `crane-preview` + 独立 client `crane-b4-preview`、redirect `.../auth/callback` | 正式 client/realm |
| 数据 | 仅匿名 Demo/seed（inventory.example + 匿名登记） | 真实 Inventory |

- 既有基座（项目名/网络 label/卷 label/owner）以服务器 CC 只读核实为准；本文不假定是空环境。部署前用 `docker compose config --format json` 复核项目名、容器、卷、网络、密钥路径、关键端口、服务依赖、issuer、镜像与预期一致。
- 不访问生产 PG/Keycloak/MQTT；不接真实行车；不发送设备控制命令。

## 2. 镜像与固定（独立 Preview candidate 制品链）

- `CRANE_CLOUD_IMAGE` / `CRANE_WEB_IMAGE` 固定到**当前候选 HEAD** 对应构建产物的 `@sha256` digest，禁止 `latest`/可变 tag。
- 分支 `production-release` 在非 main 分支为 SKIPPED，**不产生可复用的 release-manifest artifact**；因此 Preview 镜像 digest 须由 OWNER 按「source SHA → build → digest → SBOM → Trivy 扫描 → deploy manifest」独立产出并复核，不得借用旧生产镜像或 `latest`。未经 OWNER 授权不推送部署镜像。

## 3. 身份验证路径

- 复用已合规存在的 Preview realm（`crane-preview`）与 client（`crane-b4-preview`）。issuer、redirect URI、webOrigins、KC_HOSTNAME 与回调 URL 以只读 discovery 为准，不输出 secret。
- `tools/provision-keycloak` 硬编码 `crane`/`crane-bff`，**不得照原样执行于 Preview**。如需自动化，用独立 Preview-only 预检/初始化（见 G1.1-A），默认 dry-run、幂等、拒绝跨环境。
- Web Session 密钥独立随机生成，不复用 production。

## 4. 证书生命周期（与生产分开）

- `b4.crane.youdaozd.com` 使用独立 ACME 证书目录，与 `crane.youdaozd.com` 证书目录物理分离。
- 现有 `crane.youdaozd.com` 证书脚本不直接改写域名上线；Preview 证书独立 renew 配置。
- 前置条件：为 `b4.crane.youdaozd.com` 增加 DNS CNAME（待 OWNER/服务器 CC 执行）。
- TLS 终结由服务器 CC 既有前置代理负责（共享 Ingress 只读验证路由/`X-Forwarded-*`/受信代理 IP，不修改生产 Ingress）。

## 5. 资源预算（单实例，匿名 seed）

| 服务 | CPU | 内存 | 说明 |
| --- | --- | --- | --- |
| postgres | 0.5 vCPU | 512 MiB | 独立迁移全量建表 |
| keycloak | 1 vCPU | 1 GiB | 单 realm，local cache |
| cloud-api | 1 vCPU | 512 MiB | `--migrate` + demo 数据 |
| web-console | 0.25 vCPU | 128 MiB | 静态托管 |
| mosquitto | 0.1 vCPU | 64 MiB | 隔离 broker |

磁盘：PG 卷约 1–2 GiB；secrets/certs 约 10 MiB。总预算约 2.5 vCPU / 2.3 GiB。

## 6. 部署前置 Gate（全部通过才允许部署）

```
backend=PASS frontend=PASS integration=PASS edge=PASS
security=PASS a1-security=PASS r2-db-test=PASS repository-contract=PASS
b4-business-acceptance=PASS
```

## 7. 部署 / 回滚（未授权不执行）

部署前只读检查（服务器 CC 执行，均不写入）：
```bash
# 1) Compose 契约：项目名/镜像 digest/端口仅 127.0.0.1/issuer/哨兵/网络/卷/密钥路径/无生产标识
docker compose --project-name crane-b4-preview --env-file deploy/compose/b4-preview.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml \
  config --format json > /tmp/b4-preview.resolved.json
node tools/b4-preview-contract.mjs --resolved /tmp/b4-preview.resolved.json
# 2) Keycloak 只读预检：issuer、realm 存在、client 唯一且 redirect/webOrigins 精确等于 Preview
provision-keycloak --preview-precheck   # KEYCLOAK_ADMIN_URL 必须严格为 http://127.0.0.1:18085（其他一律拒绝，且在读取管理员密码前拒绝）；另需 PUBLIC_URL=https://b4.crane.youdaozd.com 与 KEYCLOAK_BOOTSTRAP_PASSWORD_FILE。还会复核 clientId 精确一致及安全开关（机密客户端、仅授权码流程、禁用 implicit/密码/服务账号、PKCE S256）
```
仓库内 `node tools/b4-preview-contract.mjs`（无参数）只检查源 overlay 文件，不能替代上面的 resolved 检查。

部署（以上全部通过后）：
```bash
docker compose --project-name crane-b4-preview --env-file deploy/compose/b4-preview.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml up -d
```

回滚 = **恢复上一套经审查的 Preview 镜像/配置及数据库快照**（先有快照才谈回滚），不把 `docker compose down` / `down -v` 当常规回滚方案；不做数据清除操作。不修改 main、不重启生产容器、不改正式数据库。

## 8. Preview 浏览器验收的写入保护

- `playwright.b4-preview.config.ts` 加载时双门禁，任一不满足即报错、不启动浏览器：`A1_WEB_ORIGIN` 必须严格等于 `https://b4.crane.youdaozd.com`（拒绝端口含 :443、路径、query、fragment、用户名密码、伪主机、大小写变体）；`B4_REQUIRED_ENVIRONMENT` 必须为 `b4-preview`。
- 每个写入用例登录后先读服务端 `GET /api/v1/environment`（由 cloud-api 的 `CRANE_DEPLOYMENT_ENVIRONMENT` 提供，生产不设置、返回空），与 `B4_REQUIRED_ENVIRONMENT` 不一致即失败，不发写请求；目标为 Preview 源而未声明哨兵时直接报错。
- 所有测试编码带每次运行唯一的 `RUN_ID`（`CRN-B4-<RUN_ID>-...`），持久化的 Preview 库重复运行不会冲突；清理仅限本次 `RUN_ID` 前缀的匿名数据，禁止对生产执行。

## 9. 镜像与 commit 的交叉核对（合法 sha256 字符串不等于对应提交）

`b4-preview-contract.mjs` 只能检查镜像引用「形如」`@sha256:<64hex>`，**不能证明该 digest 由候选 commit 构建**。部署前由 OWNER/CC 对每个镜像逐项核对：

1. 构建记录：候选 SHA 的 CI 构建日志/产物中记录的 `cloud-api` / `web-console` digest，与 env 文件中的 digest 字符完全一致。
2. 镜像标签：`docker buildx imagetools inspect <image@sha256:...>` 或 `docker image inspect` 读取 `org.opencontainers.image.revision`（`deploy/Dockerfile.backend` 由 `SOURCE_SHA` 写入），必须等于候选 SHA；值为 `development` 或缺失即拒绝。
3. SBOM：对同一 digest 生成/取回 SBOM（如 `cloud-api.cdx.json`），确认其主组件指向同一 digest，并随部署证据归档。
4. 漏洞扫描：对同一 digest 运行 Trivy（与 CI `security` job 同等级 HIGH/CRITICAL），结果归档。

任一项缺失或不一致，`PREVIEW_IMAGE_PROVENANCE` 不得记为 PASS。

## 10. 服务器 CC 只读核验清单（全部只读，不修改任何资源）

```bash
# 既有网络：归属、标签、子网、已连接容器
docker network inspect u-dao-crane-b4-preview-frontend u-dao-crane-b4-preview-api-frontend \
  --format '{{.Name}} driver={{.Driver}} internal={{.Internal}} labels={{json .Labels}} subnets={{json .IPAM.Config}} containers={{len .Containers}}'
# 既有卷：确认属于 Preview 项目、挂载点不是生产卷
docker volume ls --filter label=com.docker.compose.project=crane-b4-preview
docker volume inspect crane-b4-preview_b4-preview-postgres crane-b4-preview_b4-preview-mqtt \
  --format '{{.Name}} labels={{json .Labels}} mountpoint={{.Mountpoint}}'
# 生产卷不得被 Preview 容器挂载（逐个 Preview 容器检查 Mounts）
docker ps --filter label=com.docker.compose.project=crane-b4-preview --format '{{.Names}}' \
  | xargs -r docker inspect --format '{{.Name}} {{range .Mounts}}{{.Type}}:{{.Name}}{{.Source}}->{{.Destination}}(rw={{.RW}}) {{end}}'
# 数据库卷归属：Preview postgres 的数据目录来自 Preview 卷
docker inspect "$(docker ps -q --filter label=com.docker.compose.project=crane-b4-preview --filter label=com.docker.compose.service=postgres)" \
  --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql/data"}}{{.Name}}{{end}}{{end}}'
# 宿主机端口：18083/18085 仅绑定 127.0.0.1
ss -ltnp | grep -E ':(18083|18085)\b'
```

判定：网络标签/归属与既有 Preview 基座一致；卷标签 `com.docker.compose.project=crane-b4-preview`；任何 Preview 容器不挂载 `crane-postgres`/`crane-mqtt`/`/srv/u-dao/secrets/crane-ops`；Postgres 数据目录来自 `crane-b4-preview_b4-preview-postgres`。**未取得这些服务器实际证据，`RUNTIME_ISOLATION` 与 `PREVIEW_EXISTING_VOLUME_NETWORK_OWNERSHIP` 一律记为 NOT_RUN，不得用静态检查 PASS 推断实际隔离。**

## 11. 待服务器 CC 只读复核项

- `10.240.40.0/24` 是否确为预留且不与生产冲突。
- `18083/18085` 端口占用与既有容器映射一致。
- 既有两张前端网络的归属与共享 Ingress 实际路由（DNS/TLS/回环/`X-Forwarded-*`/受信代理 IP）。

## 12. 不做

不真实部署、不改 DNS/TLS/Ingress/Keycloak/PG、不重启生产容器、不触碰正式库。
