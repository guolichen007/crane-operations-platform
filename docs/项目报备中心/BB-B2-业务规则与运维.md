# BB-B2 业务规则与运维

START_HEAD=4c87266620f493bf8d04a2d2c17cc0118b4a5bd5，BB_B1_REVIEW=PASS；当前阶段 BB-B2，禁止进入下一批。

## 审核与重复
manager 仅同team，director全局，sales禁止审核；SUBMITTED/UNDER_REVIEW 可批准、退回或拒绝。重复未裁决不可普通批准。确认重复将新报备标记REJECTED，保留已有项目与审批/裁决依据。不是重复并批准要求有权核验全部待处理候选，并将它们整体标记 DISTINCT_APPROVED；跨团队候选对普通主管隐藏详情，须总监处理。
退回重提交会在事务中重建未裁决候选，已持久化的裁决与审批记录保留。全部写入 reason/actor/timestamp/version/audit，同事务提交；非法状态409 INVALID_STATE。
归属转移只允许有效sales；manager只能在本team转移，director可跨team并把项目team更新为新owner的team。created_by_subject永久不变；ownership_history保留原/新owner、原因、操作者、时间。

阶段：草稿可选择初始阶段；提交后仅允许 L0→L1→…→L8，L8→WON；任一非终局阶段可带原因进入HOLD/LOST；HOLD仅可带原因回到L1重新确认；WON/LOST为终局。全部经过 owner/OCC/审计。无拖拽前端伪状态。

## 保护期
reference_values.protection_policy 默认30/60/90天，基于max(created_at,last_activity_at)。ACTIVE/WARNING/EXPIRING/EXPIRED分别计算 next_review_at，protected_until为90天截止。EXPIRED仅需要主管复核保护资格，不修改owner、created_by或intake_status。实际跟进可恢复ACTIVE。
独立 Go CLI：baobei maintenance protection-sweep；同一输入可重复运行，状态/截止时间没有变化时不增加版本或审计。提交/跟进同事务刷新保护期。服务器后续可由systemd timer调用，本轮不创建timer。CLI不依赖页面、Redis/Kafka或新服务。

## 备份恢复
入口 node tools/task.mjs baobei backup；实际恢复演练 node tools/task.mjs baobei restore-test。仅允许 baobei/baobei_test 数据库；拒绝混入未知业务表。pg_export_snapshot维持同一只读快照，用于pg_dump与计数manifest；dump为custom格式、sha256校验，包含迁移校验和。
恢复仅写全新 baobei_restore_<随机ID> 隔离库，验证23张表行数、迁移版本/校验和、外键有效性与关键关联，之后仅删除本次随机测试库。绝不恢复覆盖源数据库；无Crane DB。dump与restore使用文件描述符流式传输，SHA-256分块计算，不把数据库备份全量读入内存。
输出默认local-private/baobei/backups，权限0700/0600；生产规划 /srv/u-dao/backups/baobei，可由 BAOBEI_BACKUP_DIR 显式指定。生产备份含Session等敏感数据，服务器任务线应实现访问控制、加密/异地保存和保留周期；本轮只对synthetic数据实际演练。

## 发布证据
最终干净产品分支运行 build，再 BAOBEI_SOURCE_SHA=<HEAD> docker-build，再 release。输出local-private/baobei/release/<SHA>/manifest.json、web.cdx.json、api.cdx.json。检查image revision等于source SHA；manifest包含schema version、契约hash、SBOM hash与镜像摘要。web由npm生成CycloneDX；API由实际编译二进制Go module buildinfo生成CycloneDX。
本轮镜像摘要为本地image ID，明确digest_kind=local_image_id、registry_published=false；未推镜像到任何注册表。未来发布须由负责人获取已发布registry digest，填入生产模板；不得把本地image config摘要误当registry manifest摘要。

## 依赖迁移依据
仅局部升级ESLint及其必要Vue/TypeScript插件，Node仍24.19.0。ESLint官方支持Node24：https://eslint.org/docs/latest/use/migrate-to-10.0.0 。Lucide官方迁移保留API并更名@lucide/vue：https://lucide.dev/guide/vue/migration 。第三方依赖表与锁文件同步更新；不影响Crane根依赖。
