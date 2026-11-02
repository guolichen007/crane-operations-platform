# Phase1-A1.1-R2 安全、合规、数据敏感度、文档与审计规范

## 1. 安全继承

R2 不削弱既有：OIDC Authorization Code + PKCE、server-side PostgreSQL session、`__Host-crane_session`、CSRF、Origin/Referer、Project Scope、WS revoke、生产禁 DEV token。

## 2. 数据敏感度

### BUSINESS

品牌、型号、计划、状态、工单、维护记录等常规业务数据。

### OPERATIONS_SENSITIVE

现场精确坐标、设备 IP/MAC、网络拓扑、PLC 程序 hash、部署路径/版本、设备安装位姿。按 capability 限制，不进入公开 fixture/文档。

### SECRET_NEVER_STORE

密码、API token、MQTT secret、Camera/PLC credential、private key、OIDC client secret。不得进入业务 DB、审计、Revision、Timeline、前端 cache、日志。

## 3. 合规原则

平台是记录、提醒和归档工具，不自动宣称取得法定合规。

基于官方资料设计字段载体：TSG 08—2026、TSG 51—2023、市场监管总局第74号令。任何字段级法规要求在编码前应以正式文本核验；无法核验的字段标记 `verification_status`，不能用博客推导成法定义务。

第74号令已明确的日管控/周排查/月调度可以映射为 `DAILY_CHECK / WEEKLY_RISK_REVIEW / MONTHLY_SAFETY_REVIEW` 记录，但“系统提醒已完成”不等于负责人实际履职结论。

## 4. 文档模型

`documents` 只保存 metadata：file_name、content_type、byte_size、sha256、category、storage_provider、storage_key、uploaded_by/at。

`document_links` 关联 Asset/Equipment/Wire Rope/Inspection/Work Order/Project/Acceptance 等。

R2 生产文件上传 FEATURE OFF，直到对象存储方案审核。开发 local backend 必须放 ignore 目录，防 path traversal，限制 size/content-type/filename，预留 malware scanning hook。

## 5. 审计 vs Revision

Audit 回答“谁做了什么”；Revision 回答“数据当时是什么”。不要互相替代。

Audit 建议保存 safe diff/field names，不保存整份可能含敏感数据的 request body。

## 6. Import/Export 安全

- Import 先 parse/preview/validate/conflict/confirm；
- Excel/CSV 不能创建 REAL runtime identity；
- 公式注入：导出 XLSX/CSV 对 `= + - @` 等危险前缀做安全处理；
- 文件解析限制大小、行数、列数；
- Export 必须 Project Scope；
- 后台导出结果有过期时间和访问控制。

## 7. 位置隐私

公开仓库、测试 fixture 只使用 synthetic 坐标；禁止真实坐标简单随机偏移后提交。生产经纬度/LOCAL_SITE 位置属于 OPERATIONS_SENSITIVE。

## 8. 日志

允许：request_id、object id、safe status、duration、error code。

禁止：session cookie、token、secret、完整 OIDC query、private key、上传文件正文、真实客户敏感字段。

