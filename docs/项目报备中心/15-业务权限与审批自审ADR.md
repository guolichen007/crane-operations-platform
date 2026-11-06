# 业务权限与审批自审 ADR（G0）

状态：目标设计待负责人审查。源码审计基线 `8221cdab7bc6017aa0936f866478bdb607eb9f02`。本文的“目标”不是现存授权声明；本批不修改 Go、Vue 或正式 OpenAPI。

## ADR-001：集中资源策略

采用单个 `ProjectPolicy`（逻辑名称）联合判定：有效 Session/员工状态、应用角色、原销售 capability、资源范围、项目状态、版本、独立性证据。SQL scope 与对象判定由同一策略定义派生；所有 COUNT、列表、详情、搜索、查重展示、候选、统计、队列和写入复用。仅在前端增按钮或仅修改 canRead 都不能完成此决策。

当前 `model.go:136` 的 canRead 是 owner 或 director 或同团队 manager，并未要求 owner 身份当前仍为 sales；因此不能把当前实现简单概括为“admin 永远读不到任何项目”。正常 admin 没有所属项目时没有业务范围，历史 owner 特例仍可能通过。`canWrite` 则明确 sales owner。`store.go:138` 独立 SQL 范围与其对应。新策略应消除历史身份导致的非预期读取，同时保留审计事实。

目标范围：sales=当前本人 owner；manager=当前非空 team 的已分配项目；director=全部已分配项目；admin=全部合法项目及内部未分配草稿。未分配草稿对 manager/director 不可见，custodian 是保管者而非销售 owner。联系人等预留敏感数据没有现存采集入口，未来不能仅因 admin 全局读取就自动扩大字段；每个新敏感字段另有投影与测试。

原 capability 1/2/4 分别为 project.create/project.submit/activity.create，只约束原销售行为，不改 mask 语义，也不通过给 admin 设置 7 获得业务权限。admin 能力由业务策略计算，不能授予任何 Realm 权限。

### 四角色 × 功能 × 状态裁决表

约定：D=DRAFT，R=RETURNED，Q=SUBMITTED/UNDER_REVIEW，A=APPROVED，T=REJECTED/EXPIRED/CANCELLED/CONVERTED；`∅`=未分配 D。状态 EXPIRED 与 protection EXPIRED 是不同轴。所有允许项还需有效身份、资源范围、版本和字段校验。

| 功能/状态 | sales | manager | director | admin |
| --- | --- | --- | --- | --- |
| 新建 D | 本人，create 位 | 拒绝 | 拒绝 | 可 ∅ 或指派有效 sales |
| 读取 ∅ | 拒绝 | 拒绝 | 拒绝 | 可读 |
| 读取已分配 D/R/Q/A/T | 本人 | 本组 | 全局 | 全局 |
| 编辑 ∅/D/R | 本人 D/R | 拒绝 | 拒绝 | 可，实质编辑记参与 |
| 编辑 Q/A/T 正文 | 拒绝 | 拒绝 | 拒绝 | 拒绝，不能用 admin 绕过状态机 |
| 指派 ∅ | 拒绝 | 拒绝 | 拒绝 | 有效 sales，team 同源 |
| 转移 D/R/Q/A | 拒绝 | 源/目标均本组 | 已分配跨组 | 全局；留参与证据 |
| 转移 T | 拒绝 | 拒绝 | 拒绝 | 拒绝 |
| 提交 D/R | 本人+submit 位 | 拒绝 | 拒绝 | 已分配且有效 owner；∅ 拒绝 |
| 跟进 Q/A | 本人+activity 位 | 本期不代录 | 本期不代录 | 可代录，真实 actor |
| 跟进 D/R/T | 拒绝 | 拒绝 | 拒绝 | 拒绝 |
| 阶段 D/R/Q/A | 本人，既有合法转移 | 拒绝 | 拒绝 | 同状态约束且有原因/审计 |
| 阶段 T | 拒绝 | 拒绝 | 拒绝 | 拒绝 |
| 批准/拒绝/退回 Q | 拒绝 | 本组且独立 | 全局且独立 | 全局且独立 |
| 审核 D/R/A/T | 拒绝 | 拒绝 | 拒绝 | 拒绝 |
| 重复裁决 Q | 拒绝 | 主项目及候选均本组且独立 | 主项目/全部候选可见且独立 | 全部相关项目可见且独立 |
| 列表/统计/管道 | 本人 | 本组 | 全局已分配 | 全局，∅ 单列 |
| 人员/角色/团队 | 拒绝 | 原本组销售治理 | 原跨组销售治理 | 原 BB-STAFF-R1，近期认证等照旧 |

manager/director 不新增创建、销售代提交或跟进写权限。人员路由的 admin 项目摘要当前为空（staffProjects），本期新业务读取从项目策略获得，不把人员 DSN 扩为项目正文连接。批量交接可供 admin 使用的目标扩展，应在 G1 显式统一策略与测试，不能顺手把旧 manager helper 改成“所有高级角色”。

## ADR-002：提交版本与反自审

当前 `governance.go` 审核仅检查 manager/director、canRead、version、状态和重复裁决，没有创建人、提交人或实质编辑人冲突判断。`approvals` 没有 submission revision；`audit()` 普通编辑仅写 actor/action/revision。此为新要求的已知差距，不能声称现状已满足反自审。

每次正式提交建立不可修改的 submission 快照，含真实 submitter、提交 owner/team、creator、提交所依据版本、正文 hash、参与者证据和关联事件。当前业务 version 每次变动，submission revision 只在新一轮提交增加，不能互相替代。

所有批准、拒绝、退回、CONFIRMED_DUPLICATE 和 DISTINCT_APPROVED 判定冲突集合：永久 creator；本轮 submitter；当前 owner；本轮提交时 owner；该提交依据草稿/退回修订中的实质编辑者和指派者；提交后对当前审核归属有实质作用的指派者。跨组重复决议会实质影响主项目及相关候选，须对相关提交证据分别校验，任一不可见、冲突或不完整即拒绝。只读查看、登录及定时保护 sweep 不产生业务参与；普通跟进不改变提交正文，若将跟进作为新审核依据则必须先形成新的受控提交证据。

一轮修订从上次被独立退回之后开始，到本次提交冻结；首次从创建开始。creator 永久排除；上一轮旧编辑者不自动成为新轮次编辑者，但不能删掉其历史。重新提交不能清除创建人和既有裁决。转移 owner 不删除任何已冻结参与者，提交后的新指派立即扩充当前审核冲突，旧 owner 不能因离开而获得本轮审核资格。先审后移交也不能让同一人决定尚未完成的重复裁决。

审核事务必须从数据库当前 Session 和当前有效角色重新判定；身份锁后锁项目行，校验 version+submission revision、scope、独立性及候选，再写审批、状态、审计，一次提交。任何失败均无部分决定。`DISTINCT_APPROVED` 当前会直接批准且批量处理全部 pending 候选，目标必须核对全部候选，不能只校验当前选中的 case。

**G2-0 修订（负责人 2026-11-05 批准）：相关候选规则。** G1 实现要求每个相关候选都有 COMPLETE 提交快照；而 00004 迁移后全部历史项目为 INCOMPLETE，导致任何与历史项目相似的新报备无法确认重复、无法排除重复，也无法批准（只能退回）。修订后：被审核的**主项目**仍须本轮提交证据 COMPLETE 且审核人独立；**候选项目**只要求审核人可读，且审核人从未是该候选的创建人、负责人、提交人、提交快照参与者、参与记录 actor/owner 或归属历史中的操作人/前后负责人。缺失历史按已知事实保守排除，不补造 submitter，不提供 override。实现见 `review_eligibility.go`，反证见 `g2_contract_test.go`。

**G2-R1 补充（独立审查 P1-02）：历史候选的证据等级。** 上述修订只能排除“已知”参与人；对 INCOMPLETE 候选，未发现参与记录不等于已证明未参与，因此本系统不宣称这类裁决具备完整历史独立性。实现：重复对比对可读候选返回 `evidenceGrade=COMPLETE_VERIFIED|LEGACY_PARTIAL_EVIDENCE`，项目审核资格返回 `legacyCandidates` 数；`DISTINCT_APPROVED`（会直接批准主项目）在任一相关候选为 `LEGACY_PARTIAL_EVIDENCE` 时必须携带 `acknowledgeLegacyEvidence=true`，否则 409 `LEGACY_EVIDENCE_ACK_REQUIRED`；审计事件记录每个候选的证据等级与是否确认。`CONFIRMED_DUPLICATE` 只拒绝主项目、不扩大归属，不要求确认但同样记录证据等级。这是责任可追溯措施，不是 override，也不放宽任何已知冲突。若业务需要对历史候选强制“双人核验”，属于新的业务裁决，须负责人另行批准，G2-R1 未实施。

历史未能证明独立性的待审记录返回 `REVIEW_EVIDENCE_INCOMPLETE`，保持原状态；可读取的人员看到“需要独立核验”，不可读取的人只得 404。G1 可从可靠事件生成“已证实/不完整”的回填报告，不得把当前 owner 当成历史 submitter。缺失证据的恢复需要另行审定受控核验/重新报送流程，不设置管理员 override。此类记录不在 G0 被修复或自动批准。

## ADR-003：响应与失败约束

`/api/v1/me` 保留 subject/name/role/teamId/csrfToken/permissions，目标增 `businessActions`（全局动作）与 scope 标签；permissions 仍只含原三个能力。Project 增 `actions`：每项 `{allowed:boolean, reasonCode:固定枚举|null}`；禁用原因只针对已授权可读资源，不包含其他人员敏感身份。前端不提交这些服务端字段。

目标固定原因码：`OWNER_REQUIRED`、`OWNER_INACTIVE`、`BUSINESS_PERMISSION_DENIED`、`INVALID_STATE`、`REVIEW_CONFLICT`、`REVIEW_EVIDENCE_INCOMPLETE`、`DUPLICATE_DECISION_REQUIRED`。未建立统一实现前不能把这些称为现有 API。401 会话缺失/过期；403 有效身份但动作被禁止/CSRF；404 资源不存在或范围外；409 版本、业务状态或证据不满足；400 输入；415 非 JSON；503 服务故障。范围外先返回 404，不能通过 reasonCode/计数/目标候选泄露存在性。

原请求限制 JSON 64 KiB、禁止未知字段、同源 Origin+CSRF、API 15 秒及 OIDC 10 秒等保持。`/auth/login?reauth=1` 与角色草稿恢复沿用，不能让业务确认替代近期认证。错误不得拼装客户、邮箱、Token、Cookie、授权码、state 或内部堆栈。

## 现存端点完整盘点

正式 OpenAPI 当前为 35 个 method/path；另 5 个认证 path 在实际 auth 路由中。下表均按实现核对，不以 OpenAPI 中历史 `501` 响应说明认定接口未实现。缩写 P=`/api/v1/projects`，S=`/api/v1/staff`；每个 `{id}` 以实际资源授权为准。

| 现存接口 | 当前实现/权限及证据 | G1 契约动作 |
| --- | --- | --- |
| GET `/api/v1/me` | auth.go user，当前 DB 有效会话，返回原三位能力 | 添加服务端 businessActions/scope |
| GET P | store.go list，owner/同组 manager/director；items/total | 统一 scope 和允许过滤 |
| POST P | api.go，sales+create，owner=creator=session，200 | admin 新模式，幂等，保留 200 |
| GET P/{id} | api.go canRead；含活动/审计/治理历史 | nullable owner、安全字段投影、actions |
| PATCH P/{id} | sales owner，D/R，version | 增 admin；参与证据；销售原规则保留 |
| POST P/{id}/submit | sales owner+submit，D/R；事务重查 | owner 生效、提交快照、admin、证据 |
| GET P/{id}/duplicates | 可读主项目；候选按角色脱敏 | scope 与脱敏一致 |
| POST `/api/v1/duplicate-checks` | sales；缺客户/省市/名称返回空候选 | admin 可用；未知输入说明不足，不称无重复 |
| POST P/{id}/activities | sales owner+activity，Q/A，version | admin 真实代录 actor |
| GET `/api/v1/reviews` | manager/director；pending、duplicates、protection | admin；队列数与可审数分开 |
| POST P/{id}/approve | manager/director，Q，无 pending case | 独立性和 submission 原子复核 |
| POST P/{id}/return | manager/director，Q，必填原因 | 同上，保存本轮历史 |
| POST P/{id}/reject | manager/director，Q，必填原因 | 同上 |
| POST P/{id}/ownership-transfer | manager/director，D/R/Q/A，有效 sales | admin 首次指派/跨组，参与证据 |
| GET `/api/v1/reference-values` | 登录身份；阶段、来源等五类字典 | 原字典保留，必要时增有界业务字典 |
| GET `/health/live` | 无认证，handler 不限制 HTTP method | 不扩大状态信息；无需本批改行为 |
| GET `/health/ready` | 无认证，两连接池 ping，不执行迁移 | 保留，schema gate 仍在启动 |
| POST P/{id}/stage-change | sales owner，合法阶段+原因+version | admin，同一状态规则 |
| POST P/{id}/duplicate-decisions | manager/director；跨组需 director；DISTINCT 直接批准 | 全候选可见+独立性；admin 显式授权 |
| GET P/{id}/duplicate-comparison | manager/director，主项目 scope；候选可见才有正文 | admin；跨组响应投影不扩散 |
| GET `/api/v1/ownership-candidates` | manager/director，projectId 必填；有效 sales | admin；最小目标信息，不枚举所有人员私密字段 |
| GET S | manager 同组 sales，director/admin 全局，分页 | 保留原人员 scope |
| POST S/invitations | staffWritable/needsApproval；高级角色需 admin 近期认证 | 保留 |
| GET S/{id} | staffVisible；admin 不返回项目正文 | 保留，项目能力走业务端点 |
| PATCH S/{id} | 非本人、版本、原因、精确已核验身份；角色变更有移交/近期认证门禁 | 保留 |
| PATCH S/{id}/permissions | 原 sales 三能力；版本/原因/事务 | 不添加 admin 业务假 capability |
| POST S/{id}/suspend | 立即停用撤会话、Outbox、handoffs | 增草稿保管责任的兼容检查设计 |
| POST S/{id}/reactivate | 挂起→回执→企业登录激活 | 保留 |
| POST S/{id}/retry | 失败状态才可重排，同版本证据 | 保留 |
| POST S/{id}/archive | admin，已停用且无未结束所属项目 | 增未分配草稿保管交接检查 |
| POST S/{id}/ownership-transfer-batch | 仅 manager/director，最多 100，逐项版本/结果 | 不遗漏参与记录；未来 admin 须显式扩展 |
| GET S/{id}/audit | staffVisible，历史事件 | 保留，错误脱敏 |
| GET `/api/v1/teams` | manager 仅本组；director/admin 全部 | 保留 |
| POST `/api/v1/teams` | admin，原因，重名检查 | 保留 |
| PATCH `/api/v1/teams/{id}` | admin，version/原因 | 保留 |
| `/auth/config`（规范 GET） | 当前路由不限制 method，仅配置布尔值 | 不泄露密钥 |
| POST `/auth/dev` | 显式开发+回环+Origin+固定 synthetic；生产 404 | 保留生产禁用 |
| GET `/auth/login` | 有 OIDC provider，state/nonce/PKCE | 保留 |
| GET `/auth/callback` | 一次 state、绑定 Cookie、签名、nonce、精确身份/角色 | 保留所有失败恢复 |
| POST `/auth/logout` | 有会话+Origin+CSRF，撤会话清 Cookie | 保留 |

没有运行时转换/Crane 提交端点。不存在独立 ownership-history API，归属历史由 detail 的 governanceHistory 返回；新设计优先继续使用这个路径。没有全量 summary 或 activity-feed API，前端不得提前调用拟议路径冒充已交付。

## G1–G3 契约设计（本批不启用）

| 目标契约 | 请求/响应收口 | 安全与兼容 |
| --- | --- | --- |
| POST P | 原 Input 加可选 assignmentMode=`UNASSIGNED_DRAFT/ASSIGN_ACTIVE_SALES` 和 targetSubject；admin 默认未分配；sales 请求这两个字段拒绝；`Idempotency-Key` 有界随机标识 | creator/custodian/team/提交人不可输入，沿用 200；目标 subject 仅候选接口返回 |
| PATCH P/{id} | 原完整 Input+version；归属只能走 ownership-transfer；修改 nullable 数值按数据库 ADR；nextAction/nextFollowupAt 为计划，不是假跟进 | 不接受权限/creator/owner 注入，不能“少字段 PATCH”静默清空旧文档 |
| POST P/{id}/ownership-transfer | `{version,targetSubject,reason}`，复用旧字段 | 首次 from=NULL，to 有效销售；保持 audit/history |
| POST P/{id}/submit | `{version}`；返回 Project+submissionRevision，服务端记录提交人 | 缺 owner/无效 owner/证据冲突不生效；已有提交/旧版本拒绝 |
| Project 响应 | ownerSubject 为 string/null，teamId 维持旧已分配 string 表示；未分配 teamId=null；增 assignmentStatus、submissionRevision、actions、reviewEligibility；不返回冲突他人的私密信息 | nullable 是需要配套客户端升级的语义变化，不称完全向后兼容 |
| GET P | 现有参数保留；增加 city、ownerSubject、teamId、assignment、updatedBefore、followupFrom/To | scope 只取服务端，过滤不能扩大 scope；新增 page/pageSize/scope/asOf 元信息 |
| GET `/api/v1/dashboard/summary` | 原列表过滤共用；返回 scope/filters/asOf/counts/overdue/dueNext7d/reviewableCount；未分配仅 admin | G3 新增；SQL 聚合，不扫描当前页 |
| GET `/api/v1/projects/stage-summary` | 同过滤；12 阶段 `{stage,total,knownAmount,unknownAmountCount}` | G3 新增；路由应优先于 UUID，stage 列表每栏独立分页 |

G1 将上述目标及请求示例转成正式 OpenAPI，与 Go/TS 同批实现、测试和哈希更新；本批保留运行 OpenAPI 原样，避免设计被误认成可调用接口。`activity-feed` 本期不单独新增，最近动态先来自授权项目事件，不伪造。

### 目标字段与幂等回执的确定语义

`ownerSubject` 在 OpenAPI 3.0.3 中仍为 required 属性，使用 `type:string, nullable:true`，不能因未知就省略。`teamId` 未分配为 null，已分配为 UUID 字符串；creator 不变且不可输入。`submissionRevision=0` 不代表旧数据从未提交，必须同时看 reviewEligibility 的证据等级。`actions` 不回传 CSRF、身份管理证明或其他人的私密信息。

创建键要求 UUID 或同等随机的 16–128 字符有界标识，按当前 actor+键隔离，存规范化业务输入（含指派模式/目标）的 hash。相同键不同内容返回409 `IDEMPOTENCY_CONFLICT`；相同键同内容只得到原 project ID，不重新创建。回执重放仍重验当前会话及项目读取权，返回该项目当前版本并标注 `replayed=true`，不能把首次旧快照覆盖到已编辑的表单。当前无清理实现；G1先保留键到项目生命周期结束，不靠短TTL重新允许重复创建。重试不能跨账号复用回执。

目标 Input 增加可选 `nextAction`（最长3000字节）、`nextFollowupAt`（RFC3339/null）记录计划；设备增 `environmentFlags`（null=未知，数组取 DUST/HIGH_TEMPERATURE/EXPLOSIVE/OUTDOOR，空数组=已确认无上述条件）及按原 requirements 枚举的 `automationGoals`。均由数据库文档快照与结构关系保存，不接 PLC，不把工业参数当可执行指令。正式必填不因新增可选项扩大；所有设备数值均须有限、非负并受既有大小限制，数量正式取1–10000。

以下为设计示例，当前代码会拒绝新增字段，不应对现有线上调用：

```json
{
  "projectName": "合成项目",
  "customer": "合成终端",
  "location": {"province": "", "city": ""},
  "assignmentMode": "UNASSIGNED_DRAFT",
  "estimatedAmount": null,
  "nextAction": "补充需求",
  "nextFollowupAt": null
}
```

聚合响应的 knownAmount 是人民币元的精确十进制定点字符串（两位小数），unknownAmountCount 是整数；避免将 numeric(18,2) 聚合经浮点格式化丢精度。明细旧 estimatedAmount 数字兼容和 nullable 变更仍按数据库 ADR 配套。`asOf` 为UTC，所有counts非负；包含本次有效scope及规范化filters，前端只能筛选，不能指定权限范围。

## 安全与否决方案

否决：前端角色数组放行；admin 复用 sales 的 mask；只查当前 owner 防自审；给人员 DSN 全部项目权限；在两 DSN 上各提交一半事务；持 Realm 管理密钥同步业务；给缺失历史补假 submitter。权限负例及五身份验证进入 [测试矩阵](18-测试追溯矩阵与部署风险.md)。
