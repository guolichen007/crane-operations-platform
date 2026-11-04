# 项目报备中心 / Project Intake

BAOBEI_DOMAIN=baobei.youdaozd.com
BAOBEI_MAIN_MERGE=FORBIDDEN
BAOBEI_R2_MERGE=FORBIDDEN
BASE_SHA=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18
BRANCH=product/baobei-v1
CURRENT_BATCH=BB-B3
STOP_AFTER_BB_B3=YES

目标：快速报项目、识别重复、明确归属并跟进。独立产品，永不合并 main/R2。

## BB-G0 与 BB-B1 历史交付边界
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

## BB-B2 交付
负责人批准 START_HEAD=4c87266620f493bf8d04a2d2c17cc0118b4a5bd5。B1边界说明保留为历史记录，B2已新增：客户端角色隔离/DB pending、主管审核与重复裁决、归属转移、保护期CLI、受控阶段流转、生产错误模型、隔离部署模板、备份恢复和发布证据。
规则与证据入口：BB-B2-业务规则与运维.md、生产安全与限流.md、06-BB-B2验收报告.md。BB_B2_CODE_REVIEW=PASS；本轮后STOP，不开启下一批。生产模板不在本轮部署。

## BB-B2-R1 Production Closure

START_HEAD=06d911007b01fe3fda10a1bc3a6687984376fa21；仅生产收口，无新增销售业务功能。
生产 SSOT：[生产部署契约](生产部署契约.md)、[Keycloak生产配置契约](Keycloak生产配置契约.md)。独立 ACME 入口 deploy/baobei/cert/README.md。
S1B_PUBLIC_DEPLOY=NOT_AUTHORIZED；GHCR_PUBLISHED=NO；ACME_SERVER_APPLIED=NO。
完成本地验证并 push product/baobei-v1、等待原八门后 STOP_AFTER_BB_B2_R1=YES；发布工作流只交付机制，不触发。main/R2/真实生产保持禁止修改。

本地验证证据：[07-BB-B2-R1验收报告](07-BB-B2-R1验收报告.md)。

## BB-B2-R2 首次发布机制与数据库权限

START_HEAD=89000c3ee1508c64f44369d97767e19f9b97cf09，基线 CI Run 41611873351 八门通过。
生产部署契约已记录一次性 release ref、人工审批及 PostgreSQL 角色。此次交付范围为产品分支和验证，release ref、发布和生产操作等待负责人独立授权。

验证记录见 [08-BB-B2-R2验收报告](08-BB-B2-R2验收报告.md)。

## BB-B2-R3 无重建 Ingress 的 HTTP-01 收口

START_HEAD=0f153931e18d37eb73b752b8a86bfd3498e01023，基线 CI Run 41764142151 八门通过。
宿主机 ACME 写入 challenge，baobei-web 通过只读 webroot 提供文件，共享 ingress 代理至 baobei-web:8080 并复用现有 TLS 根挂载。Nginx resolver 放在 Baobei vhost 内。生产步骤以[生产部署契约](生产部署契约.md)为准。

验证记录见 [09-BB-B2-R3验收报告](09-BB-B2-R3验收报告.md)。

## BB-B3 销售人员、团队与权限治理

以 `1abd8b346a3672f3faf1510cea518ab0ef015f36` 为启动基线，CI `41779203800` 八门通过。销售主管管理本组销售，总监处理跨组交接，管理员管理团队与高级角色。人员生命周期、能力变更和交接均由 API 在事务内校验会话和权限。

首次启用及恢复采用受控开通回执。签名企业凭证通过精确身份、已验证邮箱、客户端角色与 Baobei 人员记录的复核后，账号才会激活。完成八门 CI 后停在 BB-B3，等待负责人验收。

入口：[人员治理设计](BB-B3人员治理设计.md) · [交互动作矩阵](交互动作矩阵.md) · [BB-B3 验收报告](10-BB-B3验收报告.md)。
