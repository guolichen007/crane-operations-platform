# Phase1-A1.1-R2 CI、服务器职责、Migration、发布与回滚策略

## 1. 环境职责

### CC Local

开发证据：Node、Go、REA、可安装的用户态 PostgreSQL/Chromium。工具缺失时先 DETECT→SAFE_USERSPACE_INSTALL→VERIFY→RUN；Docker daemon 是当前沙盒硬限制。

### GitHub CI

每 Batch 的权威证据：Go、Node、PostgreSQL、Compose integration、安全、浏览器测试、release contract。

### Production Server

只做 production validation，不做开发。B1~B9 默认不修改生产。

## 2. Migration 规划

现有 00004 是 web identity 安全 migration，禁止改。

建议按批次逐步增加，不预先创建未来空 migration：

- B1：00005 asset/location/equipment foundation；00006 runtime/history foundation；
- B2：后续顺序号 portfolio/scheduling；
- B5：reliability；
- B6：temporal history；
- B9：handover/governance。

文件名以实际评审通过内容为准，必须连续且 additive-first。

## 3. B10 服务器 rehearsal

1. exact GitHub/CI artifact；
2. production backup；
3. restore 到 isolated DB；
4. 在 isolated DB 跑 00004→R2；
5. 验证行数/关键 ID/role/grant/runtime/alarms；
6. 用旧 A1.1 app 连接副本，验证 additive compatibility；
7. 起 isolated R2 compose project/network，不映射公网；
8. 浏览器/RBAC/历史/CRUD/Three 关键验证；
9. backup restore drill；
10. 形成 GO/NO-GO。

## 4. 正式迁移

必须再次 backup。数据库 migration 与应用版本必须有明确顺序；任何不可逆数据转换不得与 UI 大切换混在一步。

建议部署：

`backup → migration precheck → additive migration → new services staged → health → ingress cutover/reload → browser smoke → acceptance → current symlink`。

## 5. 回滚

应用回滚优先：旧应用应能读取 additive R2 schema。若 migration 本身引起数据错误，使用正式 backup restore，而不是写未经验证的 down migration 回滚生产。

## 6. Firebot / shared ingress

继续保持邻接业务反证：不 restart shared ingress；任何 Crane ingress change 先 nginx -t，再 reload，再验证 Firebot 首页/health/restart count。

## 7. R2 不碰 ACME timer

证书 O1-1 与 R2 产品开发分离。生产 ACME adopt/timer 仍按单独运维 Gate，不把 R2 发布和证书接管耦合成一次高风险变更。

