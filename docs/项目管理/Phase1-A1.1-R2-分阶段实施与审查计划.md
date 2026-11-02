# Phase1-A1.1-R2 分阶段实施与审查计划

> 十批实施（G0-R1 + B1~B10），每批含 TECHNOLOGY / REFERENCE / WHY / LICENSE / PERFORMANCE_GATE / DATA_GATE / SECURITY_GATE。每批 test + local commit，禁止 push。DESIGN_ONLY=YES（本批 G0-R1 只文档）。

## 0. 批次总览

| Batch | 目标 | 关键技术 |
|-------|------|---------|
| G0-R1 | 主计划/能力地图/技术来源/治理 | docs + REA pattern registry |
| B1 | 数据/权限/Contracts 地基 | PostgreSQL、Go、OpenAPI、JSON Schema、RBAC capability |
| B2 | 项目组合与排期 | Go 排期/冲突引擎、PG interval、ECharts timeline |
| B3 | Asset/Location/Equipment 后端 | Go REST、RBAC、乐观并发、幂等、审计 |
| B4 | 工业 Shell 与核心工作区 | Vue3、TanStack、Reka、MapProvider、ECharts |
| B5 | 生命周期与可靠性 | 钢丝绳/点检/PM/工单/标定/软件/供应商/质保/备件/合规 |
| B6 | 时态历史地基 | Revision、Timeline、时态关系、ConfigurationResolver、History API |
| B7 | 语义数字孪生 | Three.js、camera-controls、postprocessing、GSAP、GLTFLoader、Meshopt/KTX2、Vue Flow PoC |
| B8 | 历史 UX | ECharts core、Time Conductor、Replay、Incident Review |
| B9 | 移交与数据治理 | 验收、Punch List、Project→Asset、Bulk Import、XLSX Export、Documents |
| B10 | 加固 | 性能、Bundle、A11y、安全、REA、迁移演练、备份恢复、视觉回归 |

## 1. B1 — 数据/权限/Contracts 地基

- TECHNOLOGY：PostgreSQL + Go + OpenAPI + contracts codegen（Node）+ JSON Schema + RBAC capability
- REFERENCE：现有 contracts 生成器（tools/codegen/contracts.mjs）；TP-EAM-ASSET
- WHY：协议先更新 contracts，Cloud/Edge 共用契约；migration 00005 起 additive-first
- LICENSE：PostgreSQL License / Go BSD / 自产
- PERFORMANCE_GATE：contracts 生成 ≤ 秒级；migration fresh + 00004→R2 可回放
- DATA_GATE：ownership 三分落地；Inventory deployment-only 反证测试；equipment schema 版本化校验
- SECURITY_GATE：Project Scope First、无 global bypass、CSRF、乐观并发、幂等

## 2. B2 — 项目组合与排期

- TECHNOLOGY：Go scheduling/conflict engine + PostgreSQL interval 查询 + ECharts Custom Series + TanStack Table
- REFERENCE：Primavera Cloud（组合/资源/冲突）/ SAP EAM（阶段推进）
- WHY：第七域落地，解决多项目抢资源
- LICENSE：ECharts Apache-2.0 / 自产 Go engine
- PERFORMANCE_GATE：冲突检测（确定性）在秒级返回；Gantt 用 ECharts 不引重型框架
- DATA_GATE：project_intakes/milestones/activities/schedule_*/resource_*/conflict 表；状态机 allowed transitions
- SECURITY_GATE：Portfolio Scope 窄授权；portfolio_manager 无 ProjectGrant 不读项目遥测

## 3. B3 — Asset/Location/Equipment 后端

- TECHNOLOGY：Go REST + PostgreSQL + 乐观并发（version+If-Match）+ 幂等 + 审计
- REFERENCE：TP-EAM-ASSET（Maximo/SiteWise/ThingsBoard 概念）
- WHY：资产三层模型 CRUD，业务可编辑、运行只读
- LICENSE：自产
- PERFORMANCE_GATE：列表 server-side 分页；子表计数聚合避免 N+1
- DATA_GATE：asset_status 保留 lifecycle；installation 历史 valid_from/valid_to；目录快照
- SECURITY_GATE：maintainer/platform_admin 按 capability；写操作 CSRF+audit+version+idempotency；位置不进公开 fixture

## 4. B4 — 工业 Shell 与核心工作区

- TECHNOLOGY：Vue3 + @tanstack/vue-table + @tanstack/vue-virtual + Reka UI + MapProvider(+MapLibre 候选) + ECharts
- REFERENCE：OpenRemote / Tandem / Konecranes 高密度工作台；TP-FE-*
- WHY：从 Demo UI 到生产工具
- LICENSE：MIT（TanStack/Reka）、MapLibre BSD（候选）
- PERFORMANCE_GATE：首屏 ≤220KB raw/≤70KB gzip（估）；Three/ECharts 按需；DataGrid 10k 行流畅
- DATA_GATE：UX State 全定义（LOADING/EMPTY/ERROR/…/OFFLINE），禁空白卡片
- SECURITY_GATE：viewer 无 write CTA；OPERATIONS_SENSITIVE 按 capability 显隐

## 5. B5 — 生命周期与可靠性

- TECHNOLOGY：Go + PostgreSQL；wire_rope 专用四表、inspection、maintenance_plans、work_orders、calibration、software、supplier、warranty、spare、compliance
- REFERENCE：Maximo PM（TIME/METER/HYBRID）、Konecranes（检查/钢丝绳）、TP-EAM-CRANE、TP-EAM-COMPLIANCE
- WHY：资产全生命周期
- LICENSE：自产
- PERFORMANCE_GATE：台账分页；工单状态机后端校验
- DATA_GATE：四维状态（Connectivity/Condition 不可 UI 伪造）；替换留痕旧件不移删
- SECURITY_GATE：法定 vs 企业检查 vs 系统提醒三者区分；credential 永不入库

## 6. B6 — 时态历史地基

- TECHNOLOGY：PostgreSQL temporal + Go；entity_revisions、asset_timeline_events、telemetry_metric_definitions、ConfigurationResolver、History API
- REFERENCE：Open MCT/Grafana/ThingsBoard；TP-HISTORY-*、TP-REVISION
- WHY：能查、能回看、能聚合、能控制查询成本
- LICENSE：自产
- PERFORMANCE_GATE：History API max duration/max points/服务端降采样；不引入 TSDB
- DATA_GATE：六层历史；Timeline 只索引不复制 payload；遥测时间质量（source_sent_at/cloud_received_at/gap）
- SECURITY_GATE：Project Scope First；Configuration@T 不泄露无权限资产

## 7. B7 — 语义数字孪生

- TECHNOLOGY：Three.js + camera-controls + postprocessing + GSAP + GLTFLoader + Meshopt/KTX2 + semantic slots + Vue Flow PoC
- REFERENCE：realvirtual-WEB（AGPL 只学）/ xeokit（AGPL 只学交互）/ Tandem；TP-3D-*
- WHY：3D 从展示到业务入口
- LICENSE：Three.js MIT；GLTFLoader 生态 decoder 本地；Vue Flow MIT（PoC）
- PERFORMANCE_GATE：Runtime Gate（Premium 50–60 FPS/DPR≤1.5/DrawCalls<350/Triangles<700k；交互<100ms）；Raycaster 不每帧；display decimation
- DATA_GATE：semantic_slot→equipment→runtime→telemetry 映射；accuracy 不标 AS_BUILT
- SECURITY_GATE：传感器 pose_source（CALIBRATED/SURVEY=精确，MANUAL=示意）；不接远控

## 8. B8 — 历史 UX

- TECHNOLOGY：ECharts core + Contextual Time Conductor + Replay + Incident Review
- REFERENCE：Open MCT（Other 只学模式）/ Grafana；TP-HISTORY-*
- WHY：故障回放/历史取证
- LICENSE：ECharts Apache-2.0
- PERFORMANCE_GATE：趋势图 30d 流畅；Replay 禁 LIVE 覆盖
- DATA_GATE：GAP 禁跨大 gap 插值；Incident Review 用 configuration@T
- SECURITY_GATE：Replay read-only；禁接远程控制

## 9. B9 — 移交与数据治理

- TECHNOLOGY：Go + PostgreSQL；acceptance、punch list、Project→Asset Handover、Bulk Import（五步）、XLSX Export、Documents metadata
- REFERENCE：SAP（验收/移交）/ 微特（台账导出）；TP-EAM-COMPLIANCE
- WHY：项目与资产正式连接；导入导出闭环
- LICENSE：自产；XLSX 库（SheetJS 社区版或等价，需 license 核验）
- PERFORMANCE_GATE：Bulk Import 不直接写库（Parse→Preview→Validate→Conflict→Confirm）
- DATA_GATE：Handover 确认 AS-BUILT；未关 blocking punch 默认不 HANDOVER
- SECURITY_GATE：Export 严格 Project Scope；Runtime identity 禁止 business Excel 创建

## 10. B10 — 加固

- TECHNOLOGY：性能/Bundle/A11y/安全/REA/迁移演练/备份恢复/视觉回归
- REFERENCE：REA before/after（A1.1 vs R2）
- WHY：达到发布候选
- LICENSE：—
- PERFORMANCE_GATE：Bundle 预算 + Runtime Gate 实测；20 路由循环无重复 canvas/内存不持续增长
- DATA_GATE：migration fresh + 00004→R2 + old app 兼容 + backup/restore
- SECURITY_GATE：viewer 无 write CTA、无敏感泄漏、无 N+1、Asset 工作流闭环

## 11. 前端成熟度路线图

- Stage 0（当前）：Premium Demo-like，只读为主，Project-centric，Three showcase。
- Stage 1：可编辑工业工作区（Project+Asset+Equipment CRUD）。
- Stage 2：运营平台（工单/点检/排期/冲突）。
- Stage 3：语义孪生（3D 对象 = 真实 Equipment）。
- Stage 4：历史运营（Replay/Incident）。
- Stage 5：成熟企业平台（Portfolio/Assets/Maintenance/History/Compliance/Governance 闭环）。

## 12. REA Batch Plan（研究绑定批次）

G0-R1：设计/能力研究。B2：Primavera/SAP/Maximo 公开交互与流程。B4：OpenRemote/Tandem/Konecranes。B7：realvirtual/xeokit/Tandem。B8：OpenMCT/Grafana/ThingsBoard。B10：A1.1 vs R2 final comparison。每次：Research → Pattern → Implementation → Capture → Compare。

## 13. Server / CI 职责

- **CC Local = development evidence**（contracts 生成、Go 单测、migration 用户态、前端 lint/build）。
- **GitHub CI = authoritative batch evidence**（含 Compose 集成、Go test、e2e）。
- **Production Server = production validation only**（禁开发/随意 npm/build/实验 migration）。
- B1~B9 默认不修改生产服务器；B10 release rehearsal 才允许 isolated DB restore、migration rehearsal、isolated stack、backup/restore。
