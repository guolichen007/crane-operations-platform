# crane-dev 源码直部署与模拟行车接入手册（R8）

OWNER 2026-11-17 决策：开发阶段不发布镜像、不构建 GHCR Candidate、不打开发 Tag、不做 OCI 运输。服务器直接检出已提交源码，本地构建并运行。只适用于 `crane-dev`（https://crane.youdaozd.com）。正式投产发布工具（release/upgrade/dev-upgrade）不变。

## 1. 部署一个已提交版本

```bash
set -euo pipefail; umask 077
cd /srv/u-dao/src/crane-operations-platform          # 部署用检出，只放已提交源码
git fetch origin && git checkout --detach <完整 40 位 SHA>
git status --porcelain                                 # 必须为空
ENV=/srv/u-dao/secrets/crane-ops/crane.env             # 0600，内容不变
node deploy/scripts/local-dev-deploy.mjs build "$ENV"  # 只构建；镜像名 crane-local/<服务>:<SHA>，不推送
node deploy/scripts/local-dev-deploy.mjs plan  "$ENV"  # 只读：哪些服务变化、schema、回退点
node deploy/scripts/local-dev-deploy.mjs apply "$ENV"  # 仅重建 cloud-api / web-console，--pull never
```

- build 每次都按 `SOURCE_SHA=<SHA>` 构建并核对 revision 标签；同名 `crane-local/<svc>:<SHA>` 已存在时，新构建内容必须相同，否则拒绝（同一 SHA 不能对应两份内容）。`--services cloud-api,web-console,device-simulator` 可只构建部分。
- plan/apply 只替换构建输入有变化的服务；未变化的服务继续用当前运行的镜像。
- apply 后逐个核对运行容器的 image ID 等于构建产物、revision 等于 SHA，然后跑冒烟（首页、OIDC issuer、API 401、/health 与 /admin 不公开）。APP_ENV=production、OIDC、CSRF、TLS、RBAC 与正式环境完全一致；生成的覆盖文件只含两服务的 `image` 和 `pull_policy: never`，且仅在 crane-dev 哨兵存在时允许。
- 记录：`/srv/u-dao/releases/crane-local/current.json`、`previous.json`（0600），含上一个可用版本与回退命令。
- 若有数据库迁移（source schema > 当前 schema），apply 要求 `--backup <6 小时内的 crane-backup-* 集>`。
- 回退（同 schema）：`git checkout <上一 SHA> && node deploy/scripts/local-dev-deploy.mjs rollback "$ENV"`。
- 状态：`node deploy/scripts/local-dev-deploy.mjs status "$ENV"`。
- 失败只影响 Crane 的 cloud-api/web-console 两个容器；postgres、keycloak、mosquitto、共享入口与其他平台不会被重建。

Crane 历史测试、演示数据允许清理或重建（OWNER 决策），但仍不得覆盖恢复数据库，不得动其他平台。

## 2. 一台模拟行车上线

前提：已部署包含 R7/R8 的版本；在网站上已有一个测试项目、一台资产（可选：一个已安装 Equipment）。

```bash
# 2.1 登记运行身份（SIMULATION，绑定已存在资产；只读 plan → 批准 apply → verify）
cat > /srv/u-dao/secrets/crane-ops/sim/req.json <<'JSON'
{ "tenantId": "demo", "entries": [ { "runtimeDeviceId": "SIM-UAT-0001", "projectId": "<测试项目ID>",
  "assetDeviceId": "<资产ID>", "sourceMode": "SIMULATION", "equipmentInstallationId": "<可选 Equipment ID>" } ] }
JSON
RI=(docker compose … run --rm --no-deps --entrypoint runtime-identity -e DATABASE_URL='postgres://crane_app@postgres:5432/crane_app?sslmode=disable' \
    -e DATABASE_PASSWORD_FILE=/run/secrets/app_db_password -v /srv/u-dao/secrets/crane-ops/sim:/sim:ro cloud-api)
"${RI[@]}" plan --request /sim/req.json > plan.json
"${RI[@]}" apply --request /sim/req.json --approve "$(jq -r .plan.digest plan.json)" --actor <管理员>
"${RI[@]}" verify --request /sim/req.json

# 2.2 更新 Fleet 与 ACL（保留现有条目；不可变冲突即拒绝）
"${RI[@]}" merge --request /sim/req.json --fleet /sim/fleet.current.json > fleet.new.json   # cloud-api FLEET_FILE 用
"${RI[@]}" merge --request /sim/req.json --fleet /sim/fleet.current.json --simulator > sim-fleet.json   # 模拟器只跑 SIMULATION
"${RI[@]}" acl --fleet /sim/fleet.new.json > acl.new                                         # broker ACL，逐主题精确授权
# 原子替换 CRANE_FLEET_FILE 与 CRANE_MQTT_ACL（0600，保留旧文件），然后：
#   docker compose … kill -s SIGHUP mosquitto     （重读 ACL，不断开其他连接）
#   node deploy/scripts/local-dev-deploy.mjs apply "$ENV"   （或 compose up --no-deps cloud-api，使新 Fleet 生效）

# 2.3 签发 SIM 客户端证书（仅宿主机 openssl；CA 私钥留在 CA 目录，不进容器）
deploy/scripts/sim-cert.sh issue --ca-dir /srv/u-dao/secrets/crane-ops/mqtt-ca --out /srv/u-dao/secrets/crane-ops/sim/certs --id SIM-UAT-0001 --days 30
sudo chown 10001:10001 /srv/u-dao/secrets/crane-ops/sim/certs/*.key

# 2.4 启动 / 停止模拟器（仅 device-simulator；本地镜像 crane-local/device-simulator:<SHA>）
node deploy/scripts/local-dev-deploy.mjs build "$ENV" --services device-simulator
deploy/scripts/sim-control.sh start "$ENV" --fleet sim-fleet.json --certs /srv/u-dao/secrets/crane-ops/sim/certs --scenario normal
deploy/scripts/sim-control.sh status "$ENV"
deploy/scripts/sim-control.sh stop "$ENV"          # 发送 GRACEFUL DEATH
```

CA 目录要求：`ca.crt` 与 `ca.key`（0600/0400），broker 证书由同一 CA 签发。cloud-api 在 APP_ENV=production 下拒绝名称含 "development" 的 CA；若现场 broker 仍用开发 CA，需先换用专用测试 CA 并重签 broker/cloud-api 证书（单独批准）。

场景：`normal`（BIRTH、心跳、WATCH 时 1 Hz LIVE）、`alarm-storm`（每 2 秒 ALARM，经 spool 与应用 ACK）、`intermittent`（每 20 秒进程级重启）、`offline`（登记但不连接）。

网站验收：运行状态页能看到 SIM-UAT-0001 在线且标注模拟；首页统计只计入「模拟运行」，真实在线保持 0；有 watch 权限的用户点开实时查看后位置与负载持续变化；alarm-storm 下告警页非空；其他项目的用户看不到该设备。

## 3. 安全不变量

- 只有登记过的 SIM-/TEST- 身份能拿到证书；REAL 不经此流程，SIM 不能改成 REAL（runtime-identity 拒绝）。
- 证书 CN = 身份；broker 以证书身份为用户名，ACL 逐设备逐主题，跨设备/跨项目发布返回 0x87。
- 模拟器容器只挂载 `--certs` 目录（不含 CA 私钥）与 SIMULATION Fleet，只读。
