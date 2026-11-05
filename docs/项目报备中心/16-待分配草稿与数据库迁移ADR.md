# 待分配草稿与数据库迁移 ADR（G0）

状态：设计基线待审。只读审计 `8221cdab7bc6017aa0936f866478bdb607eb9f02` 的迁移、Go 读写、授权及部署入口；未连接生产数据库，未创建迁移文件，未执行任何升级。生产实际数据规模、历史完整性和恢复能力均为 UNKNOWN，不能用本地源码推断。

## 1. 现有数据库完整盘点

三份业务迁移共创建 26 张表，迁移器另外创建 `baobei_migrations`。以下约束来自源码，不是生产 catalog 实测。全部主键/唯一/FK/检查通过逐份 SQL 阅读核对，默认 pgx 事务隔离下的应用授权不能冒称数据库 RLS。

| 表 | 现有约束/用途 | 目标影响 |
| --- | --- | --- |
| teams | UUID PK；name 非空；00003 增 staff_version，未有 name 唯一约束 | 不改人员团队契约；存在性和名称冲突继续服务端验证 |
| app_users | subject PK；四角色 CHECK；team FK 可空；active 默认 true | owner 生效性从此表读取，不伪造身份 |
| app_sessions | token_hash PK；subject FK；csrf/expires 非空 | 保留撤销和过期规则 |
| organizations | UUID PK；normalized_name UNIQUE；credit_code UNIQUE 可空 | 草稿无客户不生成虚构组织 |
| organization_aliases | normalized_alias PK；organization FK | 提交查重保留 alias |
| contacts | UUID PK；organization FK 可空；display_name 非空 | 预留，当前不采集私人联系方式 |
| registration_counters | year PK；value 1–999999 CHECK | 编号函数及年度并发保留，幂等重试不得另占业务记录 |
| intake_projects | UUID PK、registration_no UNIQUE；owner/creator 非空 FK；team 可空 FK；9 状态 CHECK；version>0；document JSONB 非空；estimated_amount 可空 | 允许真实 NULL owner；增加保管、提交、保护锚点与已知性设计 |
| project_locations | project PK/FK；各位置 text 非空 | 未知使用空字段/说明，非假省市；结构行仍可存在 |
| project_parties | UUID PK；project/org FK；role 非空 | 正文和结构化关系同事务 |
| project_contacts | project/contact 复合 PK，二者 FK | 保留预留，不新增隐私采集 |
| planned_equipment | UUID PK；project FK；data JSONB 非空 | 数据已知性与 document 一致，无正式设备生成 |
| project_requirements | project/category 复合 PK；project FK | 正式提交仍需真实需求，草稿允许空列表 |
| project_activities | UUID PK；project/subject FK；类型/摘要/时间等非空；followup 可空 | actor 记录真实 admin/sales；无 owner 冒名 |
| duplicate_cases | UUID PK；project/candidate FK；score/risk；00002 decision 三枚举 CHECK；matched_signals 非空 JSON | 保留已有裁决；新增提交关联，不能重试删掉已确认重复证据 |
| approvals | UUID PK；project FK 可空；subject FK；decision/reason/时间非空 | 新增 submission 关联，旧事件保留且标历史证据等级 |
| ownership_history | UUID PK；project FK 可空；from 可空、to 非空；无用户 FK；00002 actor 可空 | 保留 to 非空；首次未指派不写假归属，首次指派 from=NULL；不要误称已有 user FK |
| conversion_jobs | UUID PK；project FK；UNIQUE(project,source_revision) | 仅预留；未分配草稿不能转换 |
| conversion_links | job PK/FK；UNIQUE(target_system,target_project_id) | 预留、未连接 Crane |
| audit_events | UUID PK；project FK 可空；actor/action/revision；00002 details 非空 JSON | 增有效提交关联/精确审计数据，不存凭据或全文请求 |
| reference_values | key PK、value JSONB；查重阈值及保护策略 | 沿用现有配置，校验阈值范围 |
| oidc_pending_logins | state_hash PK；nonce/verifier/binding/expiry；过期索引 | 一次 DELETE RETURNING，5 分钟有效，保留容量锁 |
| staff_sessions | token PK/FK 到 session 且级联删除；subject FK；近期认证时间 | 不让业务连接签发人员管理证明 |
| staff_audit | UUID PK；target/actor/action/reason/version/时间非空；target 时间索引 | 保留角色/激活证据及追溯 |
| staff_outbox | UUID PK；staff_id FK；operation/status CHECK；UNIQUE(staff_id,version,operation) | 保留受控回执、幂等与旧版本拒绝 |
| staff_handoffs | 复合 PK(staff,project)，两 FK | 人员交接与业务参与记录衔接 |
| baobei_migrations | name PK、checksum 非空 | 完整数量+逐文件哈希严格校验，不容忍额外或缺失迁移 |

00003 还增加：staff_id UNIQUE、canonical work_email CHECK 与非空邮箱部分 UNIQUE；staff_status 六状态及 `active=(status='ACTIVE')` CHECK；staff_version>0；capability 0–7；legacy 触发器协调旧身份入口；最后一名 active admin 触发器（727105 锁）保护。`staff_outbox` pending 索引、team/role/status 索引保持。00001 的 owner/team/trgm 及关系表索引、00002 的 pending/session 到期和 duplicate 索引不删减。

固定迁移 SHA-256（按基线与本批提交的 Git 对象逐字节比较，二者相同；以下 LF 字节是 Linux 构建源码核对基准，不是生产数据库实测）：

| 文件 | SHA-256 |
| --- | --- |
| 00001_init.sql | `30ae47d5d305cac9fbfb2dcd36fd73ffcf324d6f1d02ec699c64417d74b93db7` |
| 00002_auth_and_governance.sql | `f50dbf5570c5b010b58faf2cc5883e78c6ae6dfb63363e660728004bbe591e8a` |
| 00003_staff_governance.sql | `00ffd4f93cc2be16d269bfdd3741fe4fe1307b84d0bc7153b6d5bee36de17ed1` |

另外，本机历史工作树的00001/00002分别为42/15行CRLF，尽管当前Git属性为 `text=auto,eol=lf`，文件字节尚未重新检出；00003为LF。两份CRLF工作树SHA分别是 `f7b14b52d80b68c8ddc40aaba27211e19e353d5aa605f5521a375693f31c3335`、`6080a101b4a7938341fac8707c640f1a3e12451118cc699c302b08f587ab355b`。Git对象无变更，本批不改SQL或重检出它们。迁移器对嵌入原始字节校验，不能用本地CRLF测试清单替代Linux LF清单，更不能为通过校验改数据库checksum。后续发布预检须比较实际待运行镜像的嵌入清单、核准Git对象及生产baobei_migrations；不同即STOP并调查来源。生产目前属于哪一组仍为UNKNOWN。

## ADR-004：真实未分配草稿

选择在现有 intake_projects 上做有序增量迁移，不另建平行项目表，不用 admin 或合成销售充当 owner。未来新增 `00004+`，旧三份文件保持上述字节。G1 首先验证 NULL 扫描/API/TS，再允许新数据产生；本批没有可执行 SQL。

目标字段与不变量：

| 目标字段/证据 | 设计 |
| --- | --- |
| owner_subject | 可空、保留 app_users FK；NULL 只允许 DRAFT，且 team_id 必须 NULL |
| draft_custodian_subject | 非空 FK；本次新建取真实 creator；历史优先使用可靠 created_by_subject（当前已有非空 FK） |
| submitted_by_subject | 可空 FK，当前轮次真实 submitter 的便捷投影，不能代替完整历史 |
| submission_revision | 非负计数，0 表示没有可证明的新提交快照；历史 UNKNOWN 不伪造 1 |
| project_submissions（拟议） | `(project_id,submission_revision)` 唯一；正文版本/hash、creator、submitter、owner/team 快照、提交时间、证据等级；不 UPDATE/DELETE 已冻结事实 |
| project_participations（拟议） | 项目/修订轮次/actor/类型/依据版本/事件标识；唯一事件键去重；冻结成员与提交后追加指派冲突可追溯 |
| project_create_requests（拟议） | `(actor_subject,idempotency_key)` 唯一，规范请求 hash、project FK、创建回执版本、时间；不保存密钥/完整敏感请求 |
| protection_started_at/basis（拟议） | 新业务从首次正式提交开始；历史依据显式 LEGACY_CREATED_AT 或可验证事件，不能凭空补提交日期 |
| next_action（拟议）/现有 next_followup_at | 保存真实计划；next_action 从现有最新 created_at 活动的 next_action 可靠回填，无活动为空；新活动同事务更新两列，草稿计划不插入伪造活动 |

目标 CHECK 逻辑：`owner IS NOT NULL OR (status='DRAFT' AND custodian IS NOT NULL AND team_id IS NULL)`。有 owner 的业务写入必须在同一事务验证当前 owner 为 ACTIVE、active=true 的 sales，并有存在的非空 team。普通 SQL CHECK 不查询人员表，动态角色与启停一致性由身份锁+事务保证。历史人员被停用不使旧项目失去阅读/审计，但新的提交或指派必须拒绝失效 owner 并引导交接。

未分配创建只记录 project.create_unassigned/审计，不写 ownership_history。指派后保留 custodian/creator，写 from=NULL、有效 to、真实 actor/reason/version；已有转移继续保留原 owner。不能默认 manager/director 可见内部 ∅ 草稿。无归属草稿不参与正式保护计时和已提交重复候选库。

custodian 停用后记录仍由其他有效 admin 管理，不丢数据；不可删除其历史外键。后续归档保管人前需明确交接未分配草稿，记录新保管者和原因，不能把它统计为销售归属移交。最后 admin 保护保持；G1 须覆盖此检查，不能只按 owner 判断其全部责任已清。

### 提交历史与旧数据处理

新提交将参与记录、submitted_by、snapshot、version、重复 case、审计和状态一次提交。退回及再提交新建轮次，原提交不覆写；审批引用确切 submission。历史 audit 的 actor/action/revision 能提供部分事实，但没有完整正文快照及 owner-at-submit，不据此假造全量证明。回填将历史分为 COMPLETE/INCOMPLETE，并输出逐条依据摘要；只有可证实的事实写入新证据表。INCOMPLETE 的待审维持原状态且拒绝新决定，已批准历史不被静默重判。

恢复 INCOMPLETE 的操作尚未有现成接口，不能借 identity-apply 或 admin 按钮跳过。本期 G1 必须先以合成旧数据验证数量与处置清单；若需生产核验/新报送流程，由负责人另行批准，未收口不得发布迁移。数据仍可按原 scope 阅读，不能宣称所有旧待审可无缝继续审批。

## ADR-005：未知数值与保护期兼容

当前 Input 的金额、载荷、跨度、起升高度为 float64，省略/null 经旧模型可能落零；DB estimated_amount 虽可空，但业务创建总传数字。目标请求/Go 指针/TS `number|null` 区分未知与真实 0，数量也须明确未知：草稿可 null，正式设备列表每台数量必须 >=1；整项设备尚不清楚时可先不建该项。载荷/跨度等未知不作为正式提交必填，但已填值按范围校验，不能把 0 吨自动解释成“未知”。

环境条件和设备自动化目标为文档/设备 JSONB 的显式可选字段，不新增微服务或控制配置表。计划 nextAction 从“最新活动子查询”迁到单一项目列以支持尚无跟进的草稿；创建/PATCH/活动更新同步 document、列及审计，旧活动的 occurredAt 顺序不用于覆盖最新录入计划。遇到旧活动 created_at 相同且 nextAction 不同的歧义样本先记录待核对，不任取一个声称可靠回填。

旧值保留：不批量把历史 0 改成 NULL；新增已知性元数据对旧零标记 LEGACY_UNCONFIRMED，原值保留在原快照/迁移证据。新客户端显示“旧记录待确认”，操作者显式确认未知才写 null，显式确认真实零才写 KNOWN+0；正数保留原值，非法数量/非有限数值进入兼容报告。省略与显式 null 的处理需 G1 请求模型同时落地，PATCH 仍为带 version 的完整表单，不引入隐式局部清空。金额继续单一 estimatedAmount，本期不扩为 CPQ 或复杂金额区间。

当前保护仅在提交/跟进/sweep 刷新，sweep 只选 SUBMITTED/UNDER_REVIEW/APPROVED；但 anchor 取 created_at 与 last_activity_at 较晚者。很早建立的草稿第一次提交可能立即到期，不能只隐藏草稿保护标签掩盖这个问题。

新建记录首次正式提交写 protection_started_at=提交时间；后续 followup 取其与有效最近活动较晚者，再用原 30/60/90 天策略；重新提交不无限重置首次保护起点。未正式提交的新草稿 protectionStatus=NOT_STARTED，protectedUntil/nextReviewAt=null；契约、标签和 filter 必须一起更新。

历史已生效保护保持当前时间点与原 anchor 语义，不重新赠送保护期；basis 显式 LEGACY_CREATED_AT，在有充分证据时才转换。历史 DRAFT/RETURNED 可能有已提交记录，不能一概判为从未提交；仅可证明从未提交且未建立保护的旧草稿在首次新提交时设新锚点。不完整历史保守沿用旧依据并标注需要核验。G1 演练保护边界、退回再提交、旧日期和 sweep 幂等，禁止变动当前阈值来掩盖差异。

Transfer V1 仍要求非空 salesOwnerReference 和 quantity>=1；未指派或未知设备数量不能输出假值，预览应明确拒绝/要求完善。原 schema/hash、selectedDocuments 为空和未连接 adapter 保留；不因为内部 nullable 改造擅自改外部转换版本。

## ADR-006：事务、锁和两个 DSN

核对当前顺序：

| 路径 | 已有锁顺序/连接 | 设计约束 |
| --- | --- | --- |
| 创建/编辑/跟进 | runtime beginBusiness：identityLock 727105 共享→重查 Session/角色/team/capability→项目行（创建为 INSERT） | 保留，admin 按策略判定而非销售 mask |
| 正式提交 | identity 共享→全局 submit 727102→主项目行→fingerprint hashtextextended 锁→查重/写入 | **现状不是“行锁后全局锁”**，保留顺序；不能和其他提交反向等待 |
| 审核/转移 | identity 共享→主项目 FOR UPDATE | 如为重复决定锁多项目，按 UUID 排序；若需要全局提交锁，必须在任一项目行之前取得 |
| 人员改动/批量交接 | staff_api identity 排它→重查 staff_sessions→人员/项目行；批量按输入序 | 拟议多项目按 UUID 排序；当前独占身份锁串行化该入口，新增路径不可反向获取 |
| 人员 GET/登录 | identity 共享；激活有写入时排它；维护 CLI 排它 | 保留近期认证、受控激活、会话撤销原子性 |
| protection-sweep | runtime 727104→按 UUID 项目行，不修改身份 | 不在持有项目行后补拿 identity/submit 锁；不能由其产生业务参与 |
| 迁移 | migrator 727101→DDL/哈希/grants 同事务 | 停旧 API 后做，不能和正常业务混跑 |

identityLock 的实际常量以 `identity.go` 为准；任何新锁号分配须先复核此清单。创建幂等拟议顺序为 identity→有界幂等键锁/唯一回执→INSERT；其他路径不获取此键锁，避免反向链路。候选比对若需锁全局，先拿 727102 再按 UUID 升序锁主项目和候选，不边发现边逆序锁。提交扫描其他候选不加互相反向的行锁；按当前全局锁覆盖近似/alias 不同指纹，保留吞吐限制。

main.go 生产强制两个 DSN 同 host/port/database、search_path=public，且 current_user/session_user 与角色固定。identity 共享锁能在同一库跨连接协调，但不是跨 DSN 分布式事务。新业务读取目标销售所需字段走 runtime；指派、业务证据、审计都在一个 runtime tx。人员批量交接在原 staff_api tx 中增加最小 INSERT 参与证据权限，不能先提交身份再由 runtime 补证据。若真实 SQL 最小授权不能完成原子性，应阻断并重新审定设计，不授全表/DDL。

并发启停/指派允许两种合法串行结果：指派先完成、随后停用形成待交接；停用先完成、指派拒绝。绝不声称锁能让已经停用的目标永久保持有效。业务提交也要重查现存 owner，不能只检查执行动作的 admin。

## 2. 五身份授权与升级顺序

| 身份 | 现有范围 | 拟议最小变化/负例 |
| --- | --- | --- |
| baobei_bootstrap | 专用 DB 初始化、角色与扩展准备 | 仅受控初始化，不进入 API |
| baobei_migrator | public CREATE，表/函数拥有者；维护迁移清单和 grants；非 superuser/createrole | 新表/列/索引与授权同事务；不能登录业务替代 runtime |
| baobei_runtime | 业务表指定 DML；身份仅有限列 SELECT 和 last_login_at UPDATE；无 staff_sessions 证明、无 DDL | 新提交/参与/幂等表最小 SELECT/INSERT；已冻结证据不授 UPDATE/DELETE；是否增身份列必须逐列证明 |
| baobei_staff_api | 人员表/列和 session 证明；业务只读摘要、owner/team/version/updated_at 更新及历史审计 INSERT | 批量移交追加参与证据 INSERT；无正文、审批、重复裁决或 DDL 写权限 |
| baobei_maintenance | 受控 identity/staff-provision；人员及回执指定权限 | 不因新业务扩大 Realm 或业务正文权限；历史核验未审定前不增万能回填能力 |

表拥有者仍可修改自己拥有的表，INSERT-only grant 是 API DB 边界，不是能抵御 migrator/主机管理员的不可篡改存储保证。审计回执与部署清单需受控保管。不能宣称“整个 API 失陷也无法更改人员”，同进程确实持有 staff_api 凭据。

升级步骤设计：核准新代码/完整迁移与权限清单→隔离 PG18.4 先装旧三份迁移并造旧数据→备份/恢复校验→运行新 migrator→核对旧字段/行数/FK/hash及 NULL 新记录→五身份正负例→新 API/Web 配套验证→生产另行授权。迁移分阶段添加/回填/验证约束；数据不满足时整事务失败，不丢行、不改旧文件。DDL 锁等待/体量预算在有生产脱敏统计后确定，本批 UNKNOWN。

## 3. 不兼容影响与回滚裁决

允许 NULL owner 和 nullable JSON 数值是明确的兼容边界：旧 Go string 扫描和旧 TS 不能消费，旧版本也没有新审批证据规则。更重要的是 `database.go:77 verifySchema` 要求已应用文件数量与嵌入迁移完全一致；**即使只新增字段，旧 API 在多一份迁移后也会拒绝启动**。不能承诺“additive 就可直接退镜像”。

`direct-deploy.py` 的 database_contract 包含迁移、grants 和初始化 SQL，回滚要求新旧 hash 相同；`00004+` 必然改变 hash。G1 不能静默绕过这一门禁，也不在 G0 改部署工具。备选顺序：优先新代码 forward-fix；确需旧版本时，单独授权从升级前已实证恢复的备份恢复到匹配旧 schema 的独立 Baobei DB/卷，再恢复旧镜像及受控指针，并处置升级后新增业务。不做未经设计的 down migration，不修改 migration inventory 骗过启动。

因此 G0 可完成设计，但 `PRODUCTION_MIGRATION=BLOCKED`，直到完成真实脱敏规模评估、旧数据兼容/缺失证据处置、隔离升级演练、备份恢复及新旧部署契约核对。生产当前 SHA、数据、共享 Realm 和备份均未由本轮实时确认。G1 只能在隔离夹具实现和验证，不获得生产迁移授权。
