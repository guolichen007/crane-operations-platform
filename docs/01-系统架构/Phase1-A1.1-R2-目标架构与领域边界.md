# Phase1-A1.1-R2 目标架构与领域边界

## 1. 架构原则

保持现有 Go 模块化单体 + Vue BFF 同源架构，不为了“成熟”拆微服务。R2 的复杂度来自业务域增加，优先通过清晰模块边界、数据库事务、capability、契约和测试治理，而不是增加基础设施。

## 2. 目标逻辑架构

```text
Browser
  │ OIDC session / CSRF / same-origin
  ▼
Web Console (Vue)
  ├─ Portfolio Workspace
  ├─ Operations Center
  ├─ Asset / Equipment / Maintenance
  ├─ Twin
  └─ History / Incident
  │
  ▼
Cloud API modular monolith (Go)
  ├─ identity
  ├─ portfolio
  ├─ project
  ├─ location
  ├─ asset
  ├─ equipment
  ├─ reliability
  ├─ maintenance
  ├─ compliance
  ├─ documents
  ├─ history
  ├─ search
  ├─ attention
  ├─ importexport/jobs
  ├─ runtime (existing)
  ├─ audit
  └─ twin-metadata
  │
  ├─ PostgreSQL
  └─ existing MQTT/runtime path
```

## 3. 领域模块职责

### identity

OIDC issuer/sub、Session、CSRF、ProjectGrant、PortfolioGrant、Capability resolution。禁止业务模块自行判断 `role == ...`。

### portfolio

Project Intake、组合视图、项目状态、活动/里程碑、资源、排期、冲突、风险/问题/变更、Commissioning/Acceptance/Handover。

### project

正式项目的业务资料与 Project Scope；不负责创建未批准的 Intake。

### location

Site/Plant/Workshop/Bay/Runway/Other 层级、坐标系、LOCAL_SITE、位置历史。

### asset

Crane 等业务资产主数据、asset_code、status、manufacturer/model snapshot、合规摘要。

### equipment

Equipment type/catalog/installation/relations、runtime binding、标定、软件/固件、语义 slot、设备专项规格验证。

### reliability / maintenance

钢丝绳、inspection、PM、work order、replacement、spares、warranty。

### history

Revision、Timeline、metric catalog、History query、ConfigurationResolver、gap/time quality、Incident query assembly。

### search

Portfolio/Project 双 scope 搜索，R2 使用 PostgreSQL；不得绕过授权做全局索引。

### documents

文件元数据、document_links、storage interface；生产上传 FEATURE_OFF 直到对象存储方案批准。

### attention

统一到期/待办计算和列表。R2 只站内提醒。

### importexport/jobs

CSV/XLSX 五步导入、导出、后台任务状态；优先 PostgreSQL-backed job queue，不引入 Redis/Kafka。

## 4. 模块依赖规则

- identity 是横切基础模块，可被其它模块调用；业务模块不能反向修改 identity 数据。
- runtime 只通过稳定接口向 asset/equipment/history 提供运行状态或绑定信息。
- portfolio 的 planned equipment 不能直接写正式 equipment installation；Handover 服务负责转换。
- history 读取各域 authoritative 表，但 generic timeline 不成为 canonical source。
- documents 只保存元数据/引用，不允许业务表存任意文件路径。
- search 只读聚合，不拥有业务实体。

## 5. 一致性策略

### 强事务场景

以下必须单 PostgreSQL transaction：

- 资产/Equipment mutation + audit + revision；
- 工单状态变更 + timeline；
- replacement old remove + new install + replacement event；
- Handover 生成 AS-BUILT + project transition；
- schedule approval + conflict recheck + reservation write。

### 异步任务场景

导入解析、XLSX 导出、长报表可走 `job_runs`。R2 建议：

```text
job_runs(id, kind, status, requested_by, project_id, input_ref, progress, result_ref,
         attempts, available_at, locked_at, locked_by, created_at, finished_at, error_code)
```

Worker 使用 `SELECT ... FOR UPDATE SKIP LOCKED` 领取任务。任务必须幂等；进程重启后可重新领取超时任务。不得把 Secret 放入 job payload。

## 6. 事件策略

R2 不引入消息总线。需要 timeline/attention 的同步业务事件采用域服务显式写入；后台任务由 DB queue 承担。未来真实规模需要事件总线时再独立 ADR。

## 7. 错误隔离

- Twin/History/Map 页面失败不能影响资产 CRUD；
- 3D/WebGL 不可用时仍可查看业务档案；
- runtime offline 不影响 BUSINESS_OWNED 编辑；
- storage backend 未启用时文档 metadata 可查看，上传 CTA 显示 disabled reason；
- 搜索故障不阻止直接 URL 访问授权资源。

## 8. 生产兼容

所有 R2 migration additive-first。旧 A1.1 应能忽略新增表/列，作为紧急 rollback 应用版本读取数据库。R2 内不删除旧列、不重写 asset/runtime identity、不修改 issuer/sub 语义。

