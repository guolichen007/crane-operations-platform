# Phase1-A1.1-R2 API 约定、分页、并发、幂等、搜索与后台任务规范

## 1. 路径原则

明确项目上下文：

```text
/api/v1/projects/{projectId}/assets
/api/v1/projects/{projectId}/assets/{assetId}/equipment
/api/v1/projects/{projectId}/locations
/api/v1/portfolio/intakes
/api/v1/portfolio/schedule
```

Child mutation 后端必须通过 DB relation 重算 scope，不能只信 path/body projectId。

## 2. HTTP Semantics

- GET：读；
- POST：创建/显式 transition action；
- PATCH：部分更新；
- DELETE：只允许真正可 hard-delete 的 DRAFT 临时对象；核心业务使用 `/retire`, `/remove`, `/cancel` action。

## 3. 乐观并发

Mutable entity 有 `version bigint`。GET 返回 version/ETag；PATCH 带 If-Match 或 request.version。

SQL：`UPDATE ... SET version=version+1 WHERE id=? AND version=?`；0 rows → `409 CONFLICT`。

响应应返回 `error.code=VERSION_CONFLICT`、currentVersion，必要时允许获取 current snapshot。前端不能静默覆盖。

## 4. Idempotency

重要创建/transition 支持 `Idempotency-Key`：Asset、Equipment installation、Inspection、Work Order、Wire Rope install、Handover、Import confirm。

服务端保存 key + actor/scope + request hash + response reference；相同 key 不同 payload → 409/422；过期策略明确。

## 5. Pagination

统一：

```json
{
  "items": [],
  "page": 1,
  "pageSize": 50,
  "total": 1234
}
```

或在大历史/搜索采用 cursor，但同一 endpoint 不混乱。

pageSize 有上限；filter/sort 使用 allowlist，禁止用户直接传 SQL column/expression。

## 6. Filter / Sort

资产示例：`search, status, siteId, manufacturerId, equipmentType, connectivity, sort=-updatedAt`。

后端将外部字段映射到内部列；禁止拼接任意 SQL。

## 7. Error Model

```json
{
  "error": {
    "code": "VERSION_CONFLICT",
    "message": "数据已被其他用户修改",
    "requestId": "...",
    "details": {}
  }
}
```

至少统一：VALIDATION_FAILED、NOT_FOUND、FORBIDDEN、VERSION_CONFLICT、IDEMPOTENCY_CONFLICT、STATE_TRANSITION_INVALID、SCHEDULE_CONFLICT、RATE_LIMITED/QUERY_LIMIT、INTERNAL。

## 8. Search

R2 使用 PostgreSQL。先普通索引/ILIKE；数据真实增长后用 EXPLAIN 决定 pg_trgm/GIN。Search service 必须先计算允许 Project IDs/Portfolio scope，再查询实体。

禁止 Elasticsearch 预优化。

## 9. History query

限制 metric 数、时间窗、max points；`resolution=auto` 根据窗口自动选择聚合。响应返回 time quality/gap metadata。

## 10. Async Jobs

导入/导出等返回 `202 Accepted + jobId`，通过 `/jobs/{id}` 查询。DB-backed queue，worker 使用 `SKIP LOCKED`。Job 结果下载也需要授权，URL/文件引用有期限。

## 11. Audit / Request ID

每个 mutation 有 request_id；audit/revision/timeline/job 可关联。不得在 error.details 返回敏感内部信息。

