# B4 Preview 隔离部署执行方案（仅准备，未获授权不部署）

> 承接《Phase1-A1.1-R2-B4-Preview部署方案.md》，将其落实为可审查、可部署、可回滚的隔离环境清单。
> 固定源码 SHA：`cab1c20053596b9c85d43d90271c8c48bfc7e2f3`（B4-R3 冻结 SHA，长期功能分支 HEAD）。

## 1. 环境边界

| 项 | Preview | Production |
| --- | --- | --- |
| Compose 项目名 | `crane-b4-preview` | `u-dao-crane-ops` |
| 域名 | `b4.crane.youdaozd.com` | `crane.youdaozd.com` |
| 网络 | 独立 `b4-preview-frontend` + 内部 `backend` | `u-dao-crane-frontend` + 内部 `backend` |
| 预留子网 | `10.240.40.0/24`（部署前 CC 复核） | 既有子网 |
| 端口 | 18083（web）/ 18084（auth）/ 18443（https 前置） | 18081 / 18082 / 443 |
| 数据卷 | `b4-preview-postgres` / `b4-preview-mqtt` | `crane-postgres` / `crane-mqtt` |
| Secrets | `/srv/u-dao/secrets/crane-b4-preview/` | `/srv/u-dao/secrets/crane-ops/` |
| 数据库 | 独立 `crane_app` / `crane_keycloak` | 正式库 |
| OIDC | 独立 `crane-b4-preview` client、独立 redirect `.../auth/callback`、独立 realm | 正式 client/realm |
| 数据 | 仅匿名 Demo/seed（inventory.example + 匿名登记） | 真实 Inventory |

不访问生产 PG/Keycloak/MQTT；不接真实行车；不发送设备控制命令。

## 2. 镜像与固定

- `CRANE_CLOUD_IMAGE` / `CRANE_WEB_IMAGE` 固定到对应冻结 SHA `cab1c20` 的**生产候选发布产物**（CI `production-release` 的 release-manifest.json artifact）中的 `@sha256` digest，禁止 `latest`/可变 tag。仓库不提交 release 产物，digest 由 OWNER 提供并以 trivy/SBOM 复核。
- SBOM（`cloud-api.cdx.json` / `web-console.cdx.json`）随清单归档为部署证据。

## 3. 身份验证路径

- Keycloak 仅托管 Preview realm（`crane-preview`，与既有基座一致），独立 bootstrap 密码、独立 client `crane-b4-preview`、独立 client secret。
- Preview redirect URI：`https://b4.crane.youdaozd.com/auth/callback`（需 DNS CNAME 与证书就绪后才可回调）。
- Web Session 密钥（`session_server_secret`）独立随机生成，不复用 production。
- 首次启动需运行 `provision-keycloak` 与匿名 seed（`tests/integration/a1fixture --operation seed`），仅写入 Preview 库。

## 4. 证书生命周期（与生产分开）

- `b4.crane.youdaozd.com` 使用独立 ACME 证书目录（`${CRANE_SECRET_DIR}/ingress`），与 `crane.youdaozd.com` 证书目录物理分离、互不覆盖。
- 现有 `crane.youdaozd.com` 证书脚本**不直接改写域名上线**；Preview 证书需独立 renew 配置。
- 前置条件：为 `b4.crane.youdaozd.com` 增加 DNS CNAME（待 OWNER/服务器 CC 执行）。

## 5. 资源预算（单实例，匿名 seed 50 台行车 / 22 演示项目）

| 服务 | CPU | 内存 | 说明 |
| --- | --- | --- | --- |
| postgres | 0.5 vCPU | 512 MiB | 独立迁移全量建表 |
| keycloak | 1 vCPU | 1 GiB | 单 realm，local cache |
| cloud-api | 1 vCPU | 512 MiB | `--migrate` + demo 数据 |
| web-console | 0.25 vCPU | 128 MiB | 静态托管 |
| mosquitto | 0.1 vCPU | 64 MiB | 隔离 broker |
| ingress | 0.1 vCPU | 64 MiB | TLS 终结 |

磁盘：PG 卷约 1–2 GiB；secrets/certs 约 10 MiB。总预算约 2.5 vCPU / 2.3 GiB。

## 6. 部署前置 Gate（全部通过才允许部署）

```
backend=PASS frontend=PASS integration=PASS edge=PASS
security=PASS a1-security=PASS r2-db-test=PASS repository-contract=PASS
b4-business-acceptance=PASS
```
（对应长期功能分支 CI Run #41958789287，已 9/9 SUCCESS。）

## 7. 部署 / 回滚（未授权不执行）

部署（服务器 CC 复核网络/端口/密钥后）：
```bash
docker compose --project-name crane-b4-preview --env-file deploy/compose/b4-preview.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml up -d
```

回滚（保持生产不受影响）：
```bash
docker compose --project-name crane-b4-preview --env-file deploy/compose/b4-preview.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml down
```
- 删除 Preview 不删除生产；Preview 卷/网络独立，可整体 `down -v`（仅限 Preview，需确认非生产卷名）。
- 不修改 main、不重启生产容器、不改正式数据库。

## 8. 待服务器 CC 复核项

- `10.240.40.0/24` 是否确为预留且不与生产冲突。
- `18083/18084/18443` 端口占用。
- Preview 前端网络 `b4-preview-frontend` 与生产前置代理的连接方式（是否由生产 ingress 分流到 Preview web 端口，或独立入口）。

## 9. 不做

不真实部署、不改 DNS、不访问真实 CA、不重启生产容器、不触碰正式库。
