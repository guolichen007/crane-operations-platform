# 契约说明
Mutation 必须携带 X-CSRF-Token 与同源 Origin。version 必须最新，409 后重新加载。
POST create 返回项目，含数据库编号；提交返回 SUBMITTED 或 UNDER_REVIEW（风险项需主管处理，无有效第二归属）。GET detail 含 activities/audit。列表返回 items/total/page/pageSize。
Transfer hash：移除 payloadHash 后，按 schema fixture 的字段插入顺序 JSON.stringify UTF-8 SHA-256；validator 同时校验 schema 与 hash。selectedDocuments B1 固定空，防止不受控文件迁移。Preview 不调用目标系统。
