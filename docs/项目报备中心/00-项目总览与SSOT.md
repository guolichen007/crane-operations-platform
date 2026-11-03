# 项目报备中心 / Project Intake

BAOBEI_DOMAIN=baobei.youdaozd.com
BAOBEI_MAIN_MERGE=FORBIDDEN
BAOBEI_R2_MERGE=FORBIDDEN
BASE_SHA=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18
BRANCH=product/baobei-v1
CURRENT_BATCH=BB-B2
STOP_AFTER_BB_B2=YES

目标：快速报项目、识别重复、明确归属并跟进。独立产品，永不合并 main/R2。

## 当前交付边界
BB-G0：治理、架构、模型、交互矩阵、契约、研究及 CI。
BB-B1：开发认证/生产 OIDC BFF 结构、工作台、我的报备、三步创建、服务器草稿自动保存、查重、提交、详情、跟进。
审批、归属转移、团队治理与正式转换仅冻结契约，BB-B1 不显示可点击业务入口；不得进入 BB-B2。管道只读。
生产域名仅作为配置约束；本轮不部署、不创建真实账号。开发身份仅 loopback 显式开启，synthetic-a/b/manager。

## 入口
- [架构](01-架构.md)
- [数据模型](02-数据模型.md)
- [交互矩阵](交互动作矩阵.md)
- [UI 研究](03-UI研究.md)
- [运行与CI](04-运行与CI.md)
- contracts/project-intake/openapi.yaml
- contracts/project-intake/ProjectIntakeTransferV1.schema.json

## 不变约束
独立 PostgreSQL、独立 Cookie、独立服务、契约先行。没有运行时 Crane 集成。
Planned Equipment 永远不能直接生成 Equipment / Serial Number / Runtime Binding。
不扩展 ERP/财务/完整 CRM/邮件/LLM/爬虫/三维/PLC/远控。没有根许可证变更。

BB_G0_LOCAL=PASS
BB_B1_LOCAL_VALIDATION=PASS
BB_B1_REVIEW=PASS
BB_B2_STARTED=YES

本轮本地交付证据见 05-BB-G0-B1验收报告.md。全部 production 操作未执行；后续批次须负责人架构/代码/UI/安全审查。

## BB-B2 当前交付
负责人批准 START_HEAD=4c87266620f493bf8d04a2d2c17cc0118b4a5bd5。B1边界说明保留为历史记录，B2已新增：客户端角色隔离/DB pending、主管审核与重复裁决、归属转移、保护期CLI、受控阶段流转、生产错误模型、隔离部署模板、备份恢复和发布证据。
规则与证据入口：BB-B2-业务规则与运维.md、生产安全与限流.md、06-BB-B2验收报告.md。BB_B2_OWNER_REVIEW=PENDING；本轮后STOP，不开启下一批。生产模板不在本轮部署。
