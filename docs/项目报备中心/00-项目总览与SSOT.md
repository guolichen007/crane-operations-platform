# 项目报备中心 / Project Intake

BAOBEI_DOMAIN=baobei.youdaozd.com
BAOBEI_MAIN_MERGE=FORBIDDEN
BAOBEI_R2_MERGE=FORBIDDEN
BASE_SHA=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18
BRANCH=product/baobei-v1
CURRENT_BATCH=BB-B1
STOP_AFTER_BB_B1=YES

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
