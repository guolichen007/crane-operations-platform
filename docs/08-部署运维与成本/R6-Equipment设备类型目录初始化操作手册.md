# Equipment 设备类型目录初始化操作手册（R6-BIZ-R1）

适用版本：`feature/crane-r6-biz-closure` 及之后包含 `equipment-types` 工具的 cloud-api 镜像。数据库 Schema 仍为 12，本手册不涉及任何迁移。

## 1. 为什么需要这一步

`equipment_types` 是所有 Equipment 目录项和安装记录的外键目标。00001–00012 迁移只建表、不写入任何类型，因此全新部署的数据库里该表为空。表为空时：

- `GET /api/v1/equipment/types` 返回 `[]`；
- 资产详情「安装 Equipment」表单显示「尚未配置可安装的设备类型，请联系管理员初始化类型目录」，安装按钮不可用；
- 直接调用 `POST /api/v1/assets/{id}/equipment` 返回 422。

类型代码只能来自代码注册表（`internal/equipment/types.go`，当前 43 个）。没有任何 HTTP 接口可以新增或修改全局类型，本工具是唯一受控入口。

## 2. 工具与安全边界

工具名 `equipment-types`，已打包进 cloud-api 镜像 `/usr/local/bin/equipment-types`，与 `identity` 工具同一种宿主机管理员用法。

| 操作 | 是否写库 | 说明 |
|---|---|---|
| `plan` | 否（只读事务） | 列出范围内每个类型的状态：TO_CREATE / EXISTING / DISABLED / CONFLICT，并列出数据库中注册表不认识的 UNREGISTERED 行；输出 `digest` |
| `apply` | 是（单事务） | 必须带 `--approve <plan 的 digest>` 和 `--actor <管理员标识>`；只插入 TO_CREATE；任何 CONFLICT 拒绝整次执行；digest 与当前状态不一致拒绝执行 |
| `verify` | 否 | 范围内无缺失、无冲突、库中无 UNREGISTERED 时退出码 0，否则退出码 3 |

不会发生的事情：已存在的行（包括 DISABLED）不会被修改或重新启用；冲突行不会被覆盖；设备实例、安装历史、修订记录不会被触碰；数据库地址和密码不会出现在任何输出中（密码只从私有文件读取）。

每次 apply 写一条 `audit_logs`：`user_id = host:<actor>`，`action = equipment_types_initialized`，`result = success | noop`，`request_id` 与输出记录中的 `requestId` 一致。apply 的 JSON 输出就是完整操作记录，请原样保存。

## 3. 操作步骤（生产 / 开发服务器）

前提：已获得本次初始化范围的书面批准（全部注册类型，或明确的类型清单）。以下示例在 compose 项目目录执行，`<project>` 与 `<compose 文件>` 按实际部署替换。

```bash
TOOL="docker compose -p <project> -f <compose 文件> run --rm --no-deps \
  --entrypoint equipment-types \
  -e DATABASE_URL=postgres://crane_app@postgres:5432/crane_app?sslmode=disable \
  -e DATABASE_PASSWORD_FILE=/run/secrets/app_db_password \
  cloud-api"

# 1) 只读计划。范围二选一：--all（全部 43 个注册类型）或 --types MOTOR,PLC,...
$TOOL plan --all | tee equipment-types-plan-$(date +%Y%m%dT%H%M%S).json
```

审阅 plan 输出：

- `toCreate`：本次将新增的类型；
- `existing` / `disabled`：已存在，不会改动（DISABLED 保持停用）；
- `conflicts`：库中同名类型的名称、领域或规格版本与注册表不一致。存在冲突时 apply 会拒绝，需要先单独评审该行如何处理，或缩小范围；
- `unregistered`：库里有、代码注册表没有的类型，不可安装，仅作为漂移报告。

确认后把 `plan.digest` 交给批准人，批准后执行：

```bash
# 2) 执行（digest 必须来自刚才审阅的 plan）
$TOOL apply --all --approve <plan.digest> --actor <管理员标识> | tee equipment-types-apply-$(date +%Y%m%dT%H%M%S).json

# 3) 验证
$TOOL verify --all; echo "exit=$?"
```

`verify` 退出码 0 即完成。随后在浏览器打开任一资产详情 →「设备」→「安装 Equipment」，类型下拉应出现「电机（MOTOR）」等选项。

## 4. 重复执行与异常

| 情况 | 结果 | 处理 |
|---|---|---|
| 再次执行 plan → apply | plan 全为 EXISTING；apply 输出 `result: "noop"`，不新增任何行，写一条 noop 审计 | 正常 |
| 用旧的 digest 执行 apply | 拒绝：`directory state changed since the approved plan` | 重新 plan、重新批准 |
| 范围内有 CONFLICT | 拒绝：`conflicting types in scope`，不做任何部分写入 | 评审冲突行或缩小范围 |
| 类型代码不在注册表 | 拒绝：`type code is not in the code registry`，不连接数据库 | 核对拼写；新增类型需要代码变更 |
| 未给 `--approve` 或 `--actor` | 拒绝 | 补全参数 |
| 数据库不可达或密码文件权限不是 0600 | 拒绝，原因不回显连接细节 | 检查私有配置 |

## 5. 停用类型

停用某个类型属于业务决策，本工具不提供停用或启用命令。类型被停用后：不再出现在可安装列表；新安装（无论直接指定还是通过 ACTIVE 目录项）返回 422；已安装的设备仍可编辑、拆卸，历史完整保留。重新启用需要单独的变更审批，不能通过再次 apply 实现。

## 6. 回退

apply 只新增行。如果需要撤销一次刚完成且尚无任何目录项或安装引用的初始化，属于数据库手工变更，须走数据变更审批，并先做备份；本手册不提供自动回退。一旦有目录项或安装记录引用该类型，外键会阻止删除，这是预期行为。
