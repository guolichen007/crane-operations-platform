# 契约说明
Mutation 必须携带 X-CSRF-Token 与同源 Origin。version 必须最新，409 后重新加载。

G1运行契约为1.1.0-g1：创建还必须携带Idempotency-Key（actor+key唯一）；相同内容重放原ID当前版本，不同内容409。admin可未分配创建或选择有效sales。Project.ownerSubject/teamId未分配时为JSON null，返回actions、归属、提交修订、证据等级和documentHash。三态knowledge保留旧零值语义，未标记的零不视为明确确认。重复裁决必须带comparison中全部可见候选的relatedVersions映射，缺项/多项/过期409；无权读取的关联项目仍拒绝，不通过版本映射扩大范围。新后端须配套本批销售客户端请求适配，旧未发送创建键的客户端会受控拒绝。
POST create 返回项目，含数据库编号；提交返回 SUBMITTED 或 UNDER_REVIEW（风险项需主管处理，无有效第二归属）。GET detail 含 activities/audit。现有项目列表返回 items/total，page/pageSize 为请求参数；人员列表另有 page/pageSize 响应，不能混用。
Transfer hash：移除 payloadHash 后，对象字段按 UTF-16 字典顺序递归排序、保留数组顺序，再 JSON.stringify UTF-8 SHA-256；validator 同时校验 schema 与 hash。selectedDocuments B1 固定空，防止不受控文件迁移。Preview 不调用目标系统。

validator.mjs 校验 JSON Schema 与规范化 hash，输出 connected=false 的 preview DTO；不会调用任何目标系统。

## 全角色G0契约设计（未启用）

正式openapi.yaml继续对应基线35个method/path。新裁决、现存端点和目标请求/响应见[权限ADR](../../docs/项目报备中心/15-业务权限与审批自审ADR.md)，nullable owner/未知数值/迁移兼容见[数据库ADR](../../docs/项目报备中心/16-待分配草稿与数据库迁移ADR.md)。本批仅设计，summary/actions/指派模式尚无实现；G1须将正式OpenAPI、Go/TS及反证同批更新。Transfer V1及hash不变，未分配/未知数量不得生成假销售引用或数量。
