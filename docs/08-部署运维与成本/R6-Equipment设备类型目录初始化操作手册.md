# Equipment 设备类型目录初始化操作手册（R6-BIZ-R1，修订 R1）

适用版本：包含 `equipment-types` 工具的 cloud-api 镜像（`feature/crane-r6-biz-closure` 及之后）。数据库 Schema 仍为 12，本手册不涉及迁移。

本次初始化范围：**代码注册表全部 51 种类型**（R6 的 43 种 + R9 老河口首台资产新增 8 种：THERMAL_OVERLOAD_RELAY、MOTOR_PROTECTOR、SOLENOID_VALVE、RFID_READER、RADAR、LIDAR、BARRIER_GATE、WIRE_ROPE）（`internal/equipment/types.go`；`TestRegistryCountMatchesRunbook` 锁定该数量，注册表变化时本手册必须同步修订）。

## 1. 背景

`equipment_types` 是 Equipment 目录项和安装记录的外键目标。00001–00012 迁移只建表、不写入任何类型，因此全新部署后该表为空：`GET /api/v1/equipment/types` 返回 `[]`；资产详情「安装 Equipment」显示「尚未配置可安装的设备类型，请联系管理员初始化类型目录」且不可提交；直接调用安装 API 返回 422。没有任何 HTTP 接口可以写全局类型，本工具是唯一受控入口。

## 2. 工具与安全边界

| 操作 | 写库 | 说明 |
|---|---|---|
| `plan` | 否（只读事务） | 输出目标库身份 `target`（库名、连接角色、库 OID、Schema 版本，不含任何密钥）、范围内每个类型的状态（TO_CREATE / EXISTING / DISABLED / CONFLICT）、库中注册表不认识的 UNREGISTERED 行，以及 `digest` |
| `apply` | 是（单事务） | 必须 `--approve <digest>` 和 `--actor <管理员>`；事务内锁表后重算计划，**digest 必须与当前状态完全一致**；只插入 TO_CREATE；有 CONFLICT 拒绝整次执行 |
| `verify` | 否 | 范围内无缺失、无冲突、库中无 UNREGISTERED 时退出 0；带 `--expect-installable N` 时还要求可安装数量恰为 N，否则退出 3 |

digest 覆盖目标库身份和每一行的状态，所以：在另一个库上算出的 plan 不能用于本库；**同一份旧 plan 的 digest 只能成功使用一次**，第一次 apply 成功后状态已变化，再用它会被拒绝（`directory state changed since the approved plan`）。这不是幂等，而是防止误执行；要重复执行必须重新 plan。

永远不会发生：修改或重新启用已存在的行（含 DISABLED）、覆盖冲突行、触碰设备实例/安装历史/修订记录、在输出中打印数据库地址或密码。

## 3. 前置检查（全部满足才继续）

1. 已部署的 cloud-api 镜像就是本次批准的 R6 候选（`docker inspect` 的 revision 与批准 SHA 一致），数据库 Schema 为 12。
2. 已有本次窗口的新鲜数据库备份（D1 升级流程中的双库备份）。
3. 在部署主机上以私有方式工作：

```bash
set -euo pipefail          # 任何一步失败立即停止；管道中 tee 前的命令失败也会被发现
umask 077                  # 本次生成的计划/记录文件仅当前用户可读写（0600）
WORK=/srv/u-dao/releases/r6-equipment-types-$(date +%Y%m%dT%H%M%S)
mkdir -m 0700 "$WORK"
```

4. 数据库身份保护：只使用 compose 中 cloud-api 已有的应用库连接（`crane_app` 库、`crane_app` 角色），密码只通过容器内已挂载的私有 secret 文件 `/run/secrets/app_db_password` 读取。**不要**把密码写进命令行、环境变量值或 shell 历史；**不要**改用 postgres 超级用户；**不要**指向其他平台或其他库。plan 输出的 `target.database` 必须是 `crane_app`、`target.role` 必须是 `crane_app`、`target.schemaVersion` 必须是 `12`，否则停止。

```bash
TOOL=(docker compose --project-name u-dao-crane-ops --project-directory deploy/compose \
  --env-file /srv/u-dao/secrets/crane-ops/crane.env \
  -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.crane-dev.yml \
  run --rm --no-deps --entrypoint equipment-types \
  -e DATABASE_URL='postgres://crane_app@postgres:5432/crane_app?sslmode=disable' \
  -e DATABASE_PASSWORD_FILE=/run/secrets/app_db_password \
  cloud-api)
```

`DATABASE_URL` 不含密码；工具若发现 URL 自带密码、secret 文件权限不是私有或长度不合规，会直接拒绝。

## 4. 执行

```bash
# 4.1 只读计划（全部 51 种）
"${TOOL[@]}" plan --all > "$WORK/plan.json"
jq -e '.plan.target.database=="crane_app" and .plan.target.role=="crane_app" and .plan.target.schemaVersion==12' "$WORK/plan.json"
jq '{toCreate:(.plan.toCreate|length), existing:(.plan.existing|length), disabled:.plan.disabled, conflicts:.plan.conflicts, unregistered:.plan.unregistered, digest:.plan.digest}' "$WORK/plan.json"
```

审阅：首次初始化时期望 `toCreate=51`；已初始化过 43 种的环境（R6–R8）期望 `toCreate=8`、`existing=43`，`conflicts`、`unregistered` 为空。若有冲突或未注册行，**停止**并单独评审，不得缩小范围绕过。把 `digest` 交批准人。

```bash
# 4.2 执行（digest 必须来自 4.1 刚审阅的 plan.json）
DIGEST=$(jq -r .plan.digest "$WORK/plan.json")
"${TOOL[@]}" apply --all --approve "$DIGEST" --actor <管理员标识> > "$WORK/apply.json"
jq -e '.record.result=="success" and ((.record.created|length)==51 or (.record.created|length)==8)' "$WORK/apply.json"
```

区分两种成功：`result=="success"` 表示本次真正新增了 `created` 中的类型；`result=="noop"` 表示批准的计划里没有需要新增的类型（目录早已初始化），**只写了一条 noop 审计，并不代表本次完成了初始化**。首次初始化时出现 noop 必须停止核查（说明有人已提前执行过）。

```bash
# 4.3 验证：一致且 51 种全部可安装
"${TOOL[@]}" verify --all --expect-installable 51 > "$WORK/verify.json"
jq -e '.report.consistent==true and (.report.installable|length)==51' "$WORK/verify.json"
ls -l "$WORK"                      # 三个文件均应为 -rw-------
```

`set -e` 保证任一步退出码非 0（拒绝=1，漂移=3）都会中止。

4.4 审计核对（只读）：

```bash
docker compose ... exec -T --user postgres postgres psql -XqAt -d crane_app -c \
 "SELECT user_id,action,result,request_id FROM audit_logs WHERE action='equipment_types_initialized' ORDER BY observed_at DESC LIMIT 3"
```

最新一条的 `request_id` 应等于 `apply.json` 的 `.record.requestId`，`user_id` 为 `host:<管理员标识>`，`result=success`。

4.5 界面核对：打开任一测试资产 →「设备」→「安装 Equipment」，类型下拉含「电机（MOTOR）」等 51 项，不再显示初始化提示。

`$WORK` 中的 plan/apply/verify 三个 JSON 即本次完整操作记录，随运行记录一起归档。

## 5. 重复执行与异常

| 情况 | 结果 | 处理 |
|---|---|---|
| 重新 plan 后再 apply | plan 全为 EXISTING；apply `result=noop`，不新增行，写一条 noop 审计 | 正常（仅用于复核） |
| 用已成功执行过的旧 digest 再 apply | 拒绝：state changed | 这是预期保护；如确需复核，重新 plan |
| 在另一个库的 plan 上 apply | 拒绝：digest 含目标库身份 | 核对 compose 项目与库 |
| 范围内有 CONFLICT | 拒绝，无部分写入 | 单独评审冲突行 |
| verify 退出 3 | 缺失/冲突/未注册行，或可安装数≠51 | 查看 `report.plan` 定位 |
| secret 权限或 URL 含密码 | 拒绝，原因不回显连接细节 | 修正私有配置 |

## 6. 停用类型

本工具不提供停用/启用。类型停用属于业务决策；停用后不再可安装（直接指定或经 ACTIVE 目录项均 422），已安装设备照常可编辑、拆卸，历史保留。重新启用须单独审批，再次 apply 不会启用 DISABLED 行。

## 7. 回退

apply 只新增行；本次部署的镜像回退（无迁移）不影响已初始化的类型目录，旧镜像同样读取该表。删除已初始化类型属于数据库手工变更，须单独数据变更审批并先备份；一旦被目录项或安装记录引用，外键会阻止删除。
