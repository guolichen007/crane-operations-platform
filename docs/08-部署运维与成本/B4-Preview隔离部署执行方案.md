# B4 Preview 隔离部署执行方案（仅准备，未获授权不部署）

> 承接《Phase1-A1.1-R2-B4-Preview部署方案.md》，落实为可审查、可部署、可回滚的隔离环境清单。
> 本方案分两条基线记录：**R3 冻结基线** `cab1c20053596b9c85d43d90271c8c48bfc7e2f3` 与**当前候选 HEAD** `019415241584f70699becfcdad9ed94f99cf76c6`（长期分支，此后可能继续前移）。
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

部署（服务器 CC 复核后）：
```bash
docker compose --project-name crane-b4-preview --env-file deploy/compose/b4-preview.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml up -d
```

回滚 = **恢复上一套经审查的 Preview 镜像/配置及数据库快照**（先有快照才谈回滚），不把 `docker compose down` / `down -v` 当常规回滚方案；不做数据清除操作。不修改 main、不重启生产容器、不改正式数据库。

## 8. 待服务器 CC 只读复核项

- `10.240.40.0/24` 是否确为预留且不与生产冲突。
- `18083/18085` 端口占用与既有容器映射一致。
- 既有两张前端网络的归属与共享 Ingress 实际路由（DNS/TLS/回环/`X-Forwarded-*`/受信代理 IP）。

## 9. 不做

不真实部署、不改 DNS/TLS/Ingress/Keycloak/PG、不重启生产容器、不触碰正式库。
