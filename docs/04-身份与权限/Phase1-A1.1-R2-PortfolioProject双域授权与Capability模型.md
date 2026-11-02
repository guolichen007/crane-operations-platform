# Phase1-A1.1-R2 Portfolio / Project 双域授权与 Capability 模型

## 1. 不变安全原则

- Identity = issuer + sub；
- Project Scope First；
- `platform_admin != global super admin`；
- 浏览器按钮隐藏不是授权；所有 mutation 后端重新解析 scope；
- Business API 不得修改 DEPLOYMENT_OWNED runtime identity；
- viewer/test 继续是只读反证账号语义。

## 2. 为什么需要 Portfolio Scope

新项目还没有 ProjectGrant，因此无法用 Project Scope 表达“谁可以登记/规划尚未创建的项目”。解决方式不是给 platform_admin 全局 bypass，而是增加窄 Portfolio Scope。

## 3. Portfolio capabilities

建议：

```text
portfolio_read
project_intake_read
project_intake_write
project_approve
project_schedule_read
project_schedule_write
resource_calendar_read
resource_calendar_write
schedule_override
portfolio_risk_write
```

PortfolioGrant 只允许组合/登记/排期相关数据，不自动授予项目内部资产数据权限。

## 4. Project capabilities

```text
read
watch
maintenance_request_create
inspection_write
maintenance_write
wire_rope_write
document_write
asset_master_write
location_write
catalog_write
asset_retire
compliance_write
handover_write
```

Equipment Condition（NORMAL/WARNING/ALARM/UNKNOWN）是 TELEMETRY/DERIVED 状态，业务 UI 不允许直接覆盖，故 capability 中不设 condition 写权限。人工健康判断经 `inspection_write` 或未来独立 assessment record（必须含 source、observed_at、actor、evidence）表达，且与 TELEMETRY_OWNED current condition 分离。禁止任何业务 API 直接 PATCH 当前 telemetry condition。

角色只是 capability bundle，代码禁止 `role >= maintainer` 这种隐式比较。

## 5. 建议角色映射

### viewer

`read`

### operator

`read, watch, maintenance_request_create`

### maintainer

`read, watch, maintenance_request_create, inspection_write, maintenance_write, wire_rope_write, document_write`

### platform_admin

上述全部 + `asset_master_write, location_write, catalog_write, asset_retire, compliance_write, handover_write`

### portfolio_planner / portfolio_manager

只绑定 Portfolio capability；没有具体 ProjectGrant 时不读该项目设备/遥测。

## 6. Scope 解析规则

任何 child mutation：

`child id → DB load child → asset → project → authorize`。

禁止相信 body 中的 `project_id`。跨项目对象按现有 404/403 policy 处理，不泄露目标是否存在。

## 7. Operations Sensitive

字段分三类：

- BUSINESS：普通项目业务数据；
- OPERATIONS_SENSITIVE：IP/MAC、网络标签、现场精确坐标、部署信息；
- SECRET_NEVER_STORE：密码、token、MQTT Secret、PLC credential、private key。

`read` 不默认代表读取全部 OPERATIONS_SENSITIVE；如确有需求，增加独立 capability（例如 `deployment_metadata_read`），不要扩大 viewer。

## 8. Capability 与 UI

前端路由/按钮可以基于 session capability 做 UX 控制：
- 不显示无权 CTA；
- 表单字段 read-only；
- 错误明确显示 NO_PERMISSION。

但后端仍是唯一安全边界。

## 9. 审计

以下必须审计：资产/位置/Equipment/目录/钢丝绳/工单/合规/项目排期/冲突 override/项目批准/Handover/权限变更。

审计字段：actor、issuer/sub、project/portfolio、action、object type/id、request_id、timestamp、result、safe diff。禁止写 Secret。

