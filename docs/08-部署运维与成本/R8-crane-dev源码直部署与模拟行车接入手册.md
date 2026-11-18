# crane-dev 源码直部署与模拟行车接入手册（R8）

OWNER 2026-11-17 决策：开发阶段不发布镜像、不构建 GHCR Candidate、不打开发 Tag、不做 OCI 运输。服务器直接检出已提交源码，本地构建并运行。只适用于 `crane-dev`（https://crane.youdaozd.com）。正式投产发布工具（release/upgrade/dev-upgrade）不变。

所有业务修复必须先进入 Git；服务器上的临时改动不算交付。

## 1. 部署一个已提交版本

```bash
set -euo pipefail; umask 077
cd /srv/u-dao/src/crane-operations-platform          # 部署用检出，只放已提交源码
git fetch origin && git checkout --detach <完整 40 位 SHA>
git status --porcelain                                 # 必须为空
ENV=/srv/u-dao/secrets/crane-ops/crane.env             # 0600，内容不变
D=deploy/scripts/local-dev-deploy.mjs
node $D build "$ENV" --goproxy https://goproxy.cn,direct   # 只构建；镜像 crane-local/<服务>:<SHA>，不推送
node $D plan  "$ENV"                                       # 哪些服务变化、schema、回退点
node $D apply "$ENV"                                       # 仅 cloud-api / web-console，--pull never
node $D status "$ENV"
```

- **GOPROXY**：`--goproxy`（或 env 文件里的 `CRANE_BUILD_GOPROXY`）只作为构建参数传给 Go 构建阶段（cloud-api、device-simulator），只接受 `https://…`、`direct`、`off` 组成的列表，不能带凭据。go.sum 与 Go checksum 数据库校验不变，也不改全局 Docker 或系统代理。web-console 的构建不受影响。
- **同一 SHA 不会对应两份内容**：`crane-local/<svc>:<SHA>` 已存在时，新构建必须得到相同的 image ID，否则拒绝。只有构建输入发生变化的服务才会被替换。
- **首次本地部署**：apply 会先捕获当前运行的 R6 状态，写入 `baseline.json`，内容包括各镜像 ID、Registry 引用、revision、compose 工作目录与配置文件、env 文件摘要、解析后配置摘要以及 schema。每个运行中的镜像会另外打一个本地标签 `crane-local/<svc>:baseline-<revision>`，image ID 不变，不需要拉取。即使首次部署失败，也能直接回到本地 R6 镜像，不依赖 GHCR。
- **只有确认后才写 current**：新镜像启动后，依次核对运行容器的 image ID、revision、数据库 schema，再跑冒烟（首页、OIDC issuer、API 401、/health 与 /admin 不公开）。四项全部通过，才写入 `current.json`、`previous.json` 和 `active-override.yml`。任何一步失败：`attempt.json` 记为 `FAILED` 并写明原因；在 schema 未变化时，工具会自动用 `restore` 恢复到上一个已确认版本（首次部署即 R6 baseline），并把恢复结果一并写入 attempt 记录。退出码含义：3 = 冒烟失败但已恢复，2 = 需要人工决定（例如迁移后不允许镜像回退）。
- **单实例锁**：build、apply、rollback、fleet 共用 `deploy.lock`。第二个并发操作会被拒绝（退出码 2），并显示持锁进程。只有确认该进程已不存在时，才可以手工删除锁目录。
- **回退**：`node $D rollback "$ENV"`。回到 `previous.json` 记录的版本（首次部署后即 R6 baseline），要求 schema 与该版本相同。
- **迁移**：source schema 高于当前 schema 时，apply 必须带 `--backup <6 小时内的 crane-backup-* 集>`。
- 失败只影响 Crane 的 cloud-api 和 web-console 两个容器；postgres、keycloak、mosquitto、共享入口和其他平台都不会被重建。

### 1.1 之后所有维护都必须使用实际生效的 override

部署确认后，`/srv/u-dao/releases/crane-local/active-override.yml` 指向当前生效的本地镜像。如果不带这个文件执行 `docker compose … up`，cloud-api 和 web-console 会被重建回 env 文件里的旧 Registry 镜像。所以维护一律通过工具执行：

```bash
node $D compose "$ENV" -- ps
node $D compose "$ENV" -- logs --tail 200 cloud-api
node $D compose "$ENV" -- restart cloud-api
node $D compose "$ENV" -- up -d --no-deps cloud-api      # 工具自动加 --no-build --pull never
```

该模式拒绝以下操作：`down`、`pull`、`push`、`build`、`rm`、`kill`、`create`、删除卷，以及不带 `--no-deps` 的 `up`。`sim-control.sh` 同样会读取这个文件，文件不存在时直接拒绝。

Crane 历史测试和演示数据允许清理或重建（OWNER 决策），但仍不得覆盖恢复数据库，不得动其他平台。

## 2. 第一台模拟行车上线

前提：已用第 1 节部署到包含本手册的版本；网站上已有一个测试项目和一台资产（Equipment 可选）。现场已有 10 个**没有客户端证书**的历史 SIM 身份，它们留在云端 Fleet 中，但**不进入**模拟器的运行 Fleet。

```bash
S=/srv/u-dao/secrets/crane-ops/sim; install -d -m 0711 $S   # 容器用户 10001 可按文件名进入；certs/ 子目录另为 0700
# 2.1 登记运行身份（SIMULATION，绑定已存在资产；plan 只读 → 批准后 apply → verify）
cat > $S/req.json <<'JSON'
{ "tenantId": "demo", "entries": [ { "runtimeDeviceId": "SIM-UAT-0001", "projectId": "<测试项目ID>",
  "assetDeviceId": "<资产ID>", "sourceMode": "SIMULATION", "equipmentInstallationId": "<可选 Equipment ID>" } ] }
JSON
chmod 0644 $S/req.json                                 # 不含机密；runtime-identity 容器以 10001 运行
RI=(node $D compose "$ENV" -- run --rm --no-deps --entrypoint runtime-identity \
    -e DATABASE_URL='postgres://crane_app@postgres:5432/crane_app?sslmode=disable' \
    -e DATABASE_PASSWORD_FILE=/run/secrets/app_db_password -v $S:/sim:ro cloud-api)
"${RI[@]}" plan --request /sim/req.json > $S/plan.json
"${RI[@]}" apply --request /sim/req.json --approve "$(jq -r .plan.digest $S/plan.json)" --actor <管理员>
"${RI[@]}" verify --request /sim/req.json

# 2.2 两份 Fleet 分开管理（输入文件只读挂载，输出写到宿主机）
FLEET=$(sed -n 's/^CRANE_FLEET_FILE=//p' "$ENV"); install -m 0644 "$FLEET" $S/fleet.current.json
"${RI[@]}" merge --request /sim/req.json --fleet /sim/fleet.current.json             > $S/fleet.new.json      # 云端完整 Fleet（含历史 10 个 SIM）
"${RI[@]}" merge --request /sim/req.json --fleet /sim/fleet.current.json --simulator > $S/sim-run-fleet.json  # 只含本次请求的 SIM-UAT-0001
"${RI[@]}" acl --fleet /sim/fleet.new.json > $S/acl.new                                                       # 逐设备、逐主题精确授权
jq -r '.devices[].runtimeDeviceId' $S/sim-run-fleet.json                             # 必须只输出 SIM-UAT-0001
chmod 0644 $S/sim-run-fleet.json $S/fleet.new.json                                  # 不含机密；容器用户 10001 需可读

# 2.3 更新云端 Fleet 与 ACL（就地写入，保持 bind mount 的 inode；旧文件保留为 *.bak-<时间>）
node $D fleet "$ENV" --fleet $S/fleet.new.json --acl $S/acl.new
#   检查项：现有身份一个都不能删、不能改；ACL 必须与新 Fleet 一一对应且无通配符。
#   执行动作：mosquitto 收到 SIGHUP 重读 ACL（不断开连接）；cloud-api 用 active override 强制重建
#   （镜像不变）以加载新 Fleet；随后比对容器内 /mosquitto/acl 与 /inventory/fleet.json 是否与宿主机
#   文件一致，并跑冒烟。

# 2.4 签发 SIM 客户端证书（宿主机 openssl；沿用现有 RSA CA，不重建；CA 私钥不进容器）
sudo deploy/scripts/sim-cert.sh issue --ca-dir /srv/u-dao/secrets/crane-ops/mqtt-ca --out $S/certs \
     --id SIM-UAT-0001 --days 30 --owner 10001:10001
sudo deploy/scripts/sim-cert.sh verify --ca-dir /srv/u-dao/secrets/crane-ops/mqtt-ca --out $S/certs \
     --id SIM-UAT-0001 --owner 10001:10001

# 2.5 启动 / 停止模拟器（仅 device-simulator；本地镜像 crane-local/device-simulator:<SHA>）
node $D build "$ENV" --services device-simulator --goproxy https://goproxy.cn,direct
deploy/scripts/sim-control.sh start "$ENV" --fleet $S/sim-run-fleet.json --certs $S/certs --scenario normal
deploy/scripts/sim-control.sh status "$ENV"
deploy/scripts/sim-control.sh stop "$ENV"          # Agent 收到 SIGTERM 后发送正常 DEATH
```

**sim-cert.sh 的检查项**：

- CA 可以是 RSA 或 EC，直接使用现场现有 CA，不重建。
- `ca.crt` 必须是 CA 证书，`ca.key` 必须是它的私钥。
- CA 有效期必须覆盖叶子证书（否则请减少 `--days`；CA 续期需另行决定）。
- 叶子证书必须能按 clientAuth 用途链到 CA，私钥与证书匹配，CN 等于身份 ID。
- 已存在的证书一律不覆盖。如果输出目录里已有 `ca.crt` 但与 CA 不一致，直接拒绝。
- 使用 `--owner 10001:10001`（需要 root）后，输出目录为 0700、属主 10001，私钥为 0600、属主 10001，证书为 0644。这样容器内可读，宿主机上其他用户读不到。
- CA 名称中含 "development" 时只给出警告：cloud-api 在 APP_ENV=production 下会拒绝这样的信任链，需要另行批准换用测试 CA。

**sim-control.sh 的检查项**：

- 运行 Fleet 只能包含 SIMULATION 的 SIM-* 身份，数量 1–20，且每个身份必须与生效中的云端 Fleet 中的记录完全一致。
- 证书目录属主为 10001、权限 0700；私钥属主为 10001、权限 0600；证书对 10001 可读。
- 每个证书都能按 clientAuth 链到 CA，CN 与身份一致，私钥与证书匹配；目录中不能出现 `ca.key`。
- Fleet 文件对 10001 可读。
- spool 卷属主必须是 10001。镜像中的 `/spool` 已预建为 10001 所有，所以新建的卷会继承这个属主。如果遗留的是 root 所有的旧卷，需要在模拟器停止后执行 `docker volume rm u-dao-crane-ops_crane-simulation-spool`。
- 镜像 revision 必须等于当前 SHA。启动 5 秒后，如果容器已退出，直接判定失败。

**场景**：

- `normal`：BIRTH、心跳；被 WATCH 时以 1 Hz 发送 LIVE。
- `alarm-storm`：每 2 秒一条 ALARM，经 spool 和应用层 ACK。
- `intermittent`：每 20 秒进程级重启一次。
- `offline`：已登记但不连接。

**网站验收**：

- 运行状态页能看到 SIM-UAT-0001 在线，并标注为模拟。
- 首页统计只计入「模拟运行」，真实在线保持 0。
- 有 watch 权限的用户打开实时查看后，位置和负载持续变化。
- alarm-storm 场景下，告警页非空。
- 其他项目的用户看不到该设备。

## 3A. 最终零业务初始化（一次性，OWNER 已批准删除全部 Crane 历史测试业务数据）

### 3A.1 维护窗口准备（全部完成后才能执行 apply）

1. 公告维护窗口。窗口期间不允许任何人使用网站做业务操作。
2. 停止专用 SIM：`deploy/scripts/sim-control.sh stop "$ENV"`，再用 `deploy/scripts/sim-control.sh status "$ENV"` 确认它不在运行。apply 自己也会先停模拟器，然后停 cloud-api，cloud-api 停下后才执行数据库删除，因此窗口内不会有 MQTT 入库或网页写入与重置并发。
3. 核对目标实例：在部署检出目录确认 `node $D status "$ENV"` 显示 `current.kind = crane-local-deploy`，并且 `git rev-parse HEAD` 是本次要部署的 SHA。plan 会再自动核对 compose 项目 `u-dao-crane-ops`、哨兵 `crane-dev`、域名、`APP_ENV=production` 和数据库 `crane_app`。
4. 准备**新鲜备份**：在窗口开始时执行一次 `crane-backup-*` 备份（应用库和 Keycloak 库都要包含），并核对 `metadata.sha256`。这份备份只用于审计和应急，**不授权覆盖恢复**数据库；如需恢复，必须另行批准。
5. 确认 REAL=0：plan 输出中的 `realRuntimes` 必须是 0。只要不为 0，工具就拒绝执行；不要试图绕过这一检查。
6. 生成**最新** plan：必须在第 1–5 步完成之后执行。digest 绑定当时的数据库和行数，因此窗口外生成的 plan 不能使用。

```bash
node $D reset-business "$ENV" plan
#   只读。输出每张表的删除行数、保留行数和 digest，并写入 reset-plan.json。由批准人核对后执行：
node $D reset-business "$ENV" apply --approve <plan digest> --actor <管理员> --confirm crane-dev
```

### 3A.2 apply 的执行顺序与状态（reset-state.json）

| 步骤 | 内容 | 完成后的状态 |
|---|---|---|
| 1 | 停止模拟器容器，停止 cloud-api | `WRITES_STOPPED` |
| 2 | 在**单个事务**内删除全部业务数据：项目、登记、正式项目成员、位置、资产、Equipment、运行身份、影子、告警、事件、遥测、资源、日历、排期、预约、冲突、基线、幂等键。同时写入审计 `business_data_reset`。只要 digest 不一致、出现 REAL 或环境不符，事务就整体回滚，数据库不变 | `DB_RESET_FILES_PENDING` |
| 3 | 把 Inventory、Fleet、ACL 就地改写为显式空文档（inode 不变，旧文件另存为 `*.bak-<时间>`）；ACL 只保留 `user cloud-api` | 同上 |
| 4 | 删除模拟器 spool 卷 | 同上 |
| 5 | 向 mosquitto 发送 SIGHUP；用当前生效的 override 重建并启动 cloud-api（镜像不变） | 同上 |
| 6 | 核对容器内三个文件与写入内容一致，确认 cloud-api 运行的是已确认的镜像、业务行数为 0，冒烟通过 | `COMPLETE`，并生成 `reset-<时间>.json` |

**只有 `reset-state.json` 的 status 为 `COMPLETE`，才算完整清理成功。** 数据库删除成功（`DB_RESET_FILES_PENDING`）不等于清理完成。此时 Inventory/Fleet/ACL 可能仍是旧内容，cloud-api 也可能处于停止状态，不能对外宣布环境已就绪。

### 3A.3 数据库已提交但后续步骤失败时的恢复

现象：apply 报错退出，且 `reset-state.json` 的 status 为 `DB_RESET_FILES_PENDING`。

1. 不要再次执行 apply。digest 只能使用一次，数据库已变化，再执行也会被拒绝。也不要从备份覆盖恢复。
2. 按报错信息找到原因并处理，例如文件属主或权限、磁盘空间、mosquitto 或 Docker 状态。三个配置文件的旧版本保存在同目录的 `*.bak-<时间>`，**不要**把它们拷回去：数据库已经清空，旧 Fleet 和 Inventory 与之不再一致，而旧 Inventory 在重置之后会被启动规则直接拒绝。
3. 执行收尾。它只重复第 3–6 步，这些步骤都是幂等的；执行前会先确认数据库中确实已没有业务行，否则拒绝：

   ```bash
   node $D reset-business "$ENV" finish --actor <管理员> --confirm crane-dev
   ```

4. 确认 status 变为 `COMPLETE`，再执行 `node $D compose "$ENV" -- ps` 确认 cloud-api 是 healthy 状态。
5. 如果 finish 仍然失败，cloud-api 会保持停止，网站登录返回 502，这是安全的停机状态。此时保留现场并上报，不要手工改数据库。

### 3A.4 授权清理（重置之后、窗口结束之前）

- **项目授权**（project_memberships）随项目一起被删除，不会残留。
- **项目组合成员**（portfolio_memberships）默认保留。窗口结束前，按 OWNER 确认的正式名单清理 UAT 临时成员（例如 `b4-*`、`uat-*` 测试账号），只保留正式用户和必要的组合角色。先查看：

  ```bash
  node $D compose "$ENV" -- exec -T --user postgres postgres psql -d crane_app -c \
    "SELECT m.portfolio_scope_id, m.user_id, u.display_name, m.role FROM portfolio_memberships m JOIN users u ON u.id=m.user_id ORDER BY 1,2"
  ```

  再按名单删除。删除时用精确的 user_id 列表，必须在一个事务内完成，并同时写入审计：

  ```sql
  BEGIN;
  DELETE FROM portfolio_memberships WHERE user_id IN ('<临时账号1>','<临时账号2>');
  INSERT INTO audit_logs(user_id,action,result,request_id) VALUES('<管理员>','uat_portfolio_grants_removed','success','<窗口编号>');
  COMMIT;
  ```

- Keycloak 中的 UAT 临时账号在 Keycloak 管理台单独停用。这一步不影响 Crane 数据库。

### 3A.5 窗口结束前的验收

1. 用一名正式的项目组合经理账号登录。确认首页统计为 0，资产、运行状态、位置和项目组合各页都显示空态，没有错误提示。
2. 在「项目组合 → 项目登记」中确认「新建项目登记」按钮可用，打开表单即可，不必提交。确认之后才结束维护窗口。
3. 确认 cloud-api 日志中有 `inventory_startup imported=false`，并且没有导入错误。

重置之后的启动规则：Cloud 只接受显式空 Inventory，数据库是唯一的业务事实来源。如果把旧的示例 Inventory 放回去，Cloud 会拒绝启动（`startup Inventory import refused`）。普通 `fleet` 增量更新仍然只允许新增身份。

从零开始的业务流程（全部在网页上完成）：

1. 项目登记：评审 → 规划 → 经理批准，生成正式项目。
2. 经理为正式项目授权项目管理员（不能给自己授权）。
3. 运营登记（行政区）。
4. 新建资源和排期并完成预约（规划员或经理均可），由**经理**批准排期。
5. 新建位置、资产和 Equipment。资产的名称和编号至少填写一项。
6. 按第 2 节登记第一台 SIM，`merge --fleet` 以空 Fleet 为基础。

以上整个链路已在真实 PostgreSQL、Mosquitto 和 BFF 上验证，对应测试 `TestR8ZeroBusinessStartToFirstSimulatedCrane`。

## 3. 60 分钟容量测试（隔离运行，不碰现场）

```bash
cd /srv/u-dao/src/crane-operations-platform     # 固定 SHA、干净工作树
R7_SIM_BUILD=docker R7_SIM_GOPROXY=https://goproxy.cn,direct \
R7_SIM_SOAK_MINUTES=60 R7_SIM_ONLY=TestR7Sim20DevicesSoak \
  node tools/r7-sim-e2e.mjs 2>&1 | tee /srv/u-dao/releases/crane-local/soak-$(git rev-parse HEAD).log
```

- 测试二进制在 `deploy/Dockerfile.backend` 固定的 Go 镜像里编译，服务器不需要安装 Go。运行使用独立的临时 PostgreSQL 容器（tmpfs）和每个测试单独启动的 Mosquitto 容器（随机端口 127.0.0.1，带标签，结束后删除），**不使用 Crane 数据库和共享 Broker**。
- 硬断言（任何一项失败即整体失败）：
  - SQL 查询出错。
  - 实际持续时长不足。
  - 两台被注入断网的设备，每次断开后必须在 60 秒内重新连上；云端运行时必须真的重启过一次。
  - LIVE 样本只能来自被 watch 的那台设备，数量必须落在采样策略（每 10 秒最多 1 条）对应的区间内。
  - P95（Edge sentAt 到 Cloud 接收）不超过 1000 ms，即一个 1 Hz 采样周期。
  - 告警数量达到下限，且按 messageId 无重复。
  - 结束时 20 台全部在线。
- 证据：`test-results-r7/sim-evidence.jsonl`。其中包括每分钟一条 `SOAK_RESOURCES`（goroutine、堆、PG 连接、告警数、样本数、在线数），以及汇总 `SIM_20_DEVICES`。
- 每次运行写入独立目录 `test-results-r7/run-<时间>-<pid>/`，其中 `test.log` 是测试进程 stdout/stderr 直接落盘的完整日志（数据库密码已脱敏），`meta.json` 记录 SHA、起止时间、退出码、信号和是否超时，`sim-evidence.jsonl` 为证据记录。
- 测试会话有效期为测试时长加 30 分钟，60 分钟测试结尾续租不会因会话过期返回 401。
- 输出 `R7_SIM_E2E_SUBSET=PASS tests=TestR7Sim20DevicesSoak sha=<SHA>` 才算通过。也可以在 GitHub 上手动触发 `Crane R7 SIM Soak` 工作流（minutes=60）。

## 4. 安全不变量

- 只有已登记的 SIM-/TEST- 身份才能拿到证书；REAL 身份不走这个流程，SIM 身份也不能改成 REAL（runtime-identity 会拒绝）。
- 证书 CN 等于身份 ID。Broker 以证书身份作为用户名，ACL 逐设备、逐主题授权，跨设备或跨项目发布会返回 0x87。
- 模拟器容器只读挂载两样东西：不含 CA 私钥的 `--certs` 目录，和只含本次身份的运行 Fleet。
- 不控制任何真实硬件。
