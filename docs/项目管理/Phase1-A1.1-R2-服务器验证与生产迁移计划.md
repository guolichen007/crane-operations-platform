# Phase1-A1.1-R2 服务器验证与生产迁移计划

## 1. 原则

生产服务器不是开发机。任何 R2 migration 必须先在 CI 和 isolated restore DB 证明。

## 2. B1~B9

默认：SERVER_CHANGED=NO。禁止把本地测试缺失转移到生产服务器补跑。

## 3. B10 Preflight

- 获取 exact main candidate SHA / release manifest / image digest / SBOM；
- 验证服务器 disk/docker/network/secrets/现有 Crane/Firebot；
- production DB backup；
- restore 到隔离 DB；
- migration；
- schema/data assertions；
- old app compatibility；
- isolated R2 services；
- browser/RBAC/CRUD/history/twin smoke；
- backup restore drill。

## 4. 数据核验

迁移前后至少核对：Project IDs、Asset IDs、Runtime bindings、users、memberships/grants、alarms、关键 row counts、foreign key integrity。

## 5. 正式发布

只有负责人 `PRODUCTION_DEPLOYMENT=GO` 后：backup → migration → staged new services → health → ingress reload → browser acceptance → current switch。

## 6. 回滚

优先应用回滚；旧 app 必须兼容 additive schema。若数据损坏使用备份恢复，不在生产临时写 down migration。

