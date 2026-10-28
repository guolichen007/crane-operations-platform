# Phase1-A1 备份恢复手册

目标 RPO<=24h、RTO<=4h。双库逻辑备份覆盖 Crane application 与 Keycloak；两库分别使用 REPEATABLE READ 导出快照，pg_dump 与计数/列元数据在各自同一快照内读取，保证单库一致性。不是跨库分布式事务；当前应用的身份映射是显式配置，恢复后必须复核映射和授权。

需 PostgreSQL 官方客户端（在现有 PG 容器内）、Python3、Docker Compose。脚本通过本机 `docker compose --project-name u-dao-crane-ops exec --user postgres`，不把 DB 密码放 argv。正式 project 固定，不能因环境变量误用另一套 project；禁止指向 Firebot。只有匿名隔离 integration fixture 通过显式 --project-name 选择其临时项目。

```sh
python3 deploy/scripts/backup.py backup --source-sha 40位已部署提交SHA
python3 deploy/scripts/backup.py drill --backup /srv/u-dao/backups/crane-ops/crane-backup-实际时间与随机后缀
```

自定义 --compose/--env-file 用于明确测试或人工审查配置；默认 production.yml/production.env。默认 --root=/srv/u-dao/backups/crane-ops，--retention-days=14。根目录0700、文件0600；与 Git/runtime/artifact 分离。设置服务器受限存储访问权限，备份包含用户/会话/Keycloak 机密，不能上传 Public 仓库或普通 CI artifact。

R1 升级 preflight 必须独立检查 CRANE_BACKUP_ROOT 所在文件系统 >=10GiB 且 >=15% 空闲；不能用 DockerRootDir 的检查代替。备份脚本可由环境 CRANE_BACKUP_ROOT 或显式 --root 选择相同目录；若目录与升级门禁所检查的不一致，应停止并修正配置。

备份先生成 `.partial-*` 私有目录，两个 custom-format dump、metadata.json 和 metadata.sha256 完成并 fsync 后同文件系统原子 rename 为 `crane-backup-时间-随机值`。元数据含 UTC timestamp、source_sha、schema_version、各 dump SHA256、项目/资产/用户/membership/session/audit 计数、迁移版本及 session/audit 列名。Keycloak 记录 realm/user/client 计数。错误非0且不打印上游数据库错误，失败 partial 保留供受控本地诊断，不能当成功备份。

Retention 只在明确标记的 backup root 下删除超龄、固定命名格式、非 symlink 的完成目录；不删除其他路径或 partial，不跨文件系统边界。人工清理 partial 前须检查完成状态和绝对路径。checksum 用于完整性/意外损坏检测，不等于抵抗宿主机管理员篡改的签名。

恢复演练先验证 metadata/dump checksum，再为每个 dump 生成 `crane_drill_<随机32hex>` 临时 DB；调用方不能指定恢复目标。pg_restore 失败立即报错，恢复后与同快照计数及 schema metadata 精确比较；最后只 DROP 本工具创建的临时库。正式 crane_app/crane_keycloak 永不作为 drill 目标。独立 DB 需要本机 PG 管理权限，不能让公网调用此脚本。

验收至少包括 projects、asset_devices、users、project_memberships、web_sessions 列和 audit_logs 列；Keycloak 三类计数也要一致。脚本报告实际演练秒数，超过四小时失败。当前小规模测试数据达到 RTO 不保证未来增长后的耗时，需要定期真实容量演练。备份 cron 示例每12小时执行一次，为24小时目标留重试余量；本轮不安装服务器 cron。监控最新成功备份年龄，>24h 或脚本非0必须告警并重试。

当前只是本机备份，**不属于 Disaster Recovery**。服务器、磁盘或账号同时丢失仍会丢失备份。后续应另行设计腾讯 COS/off-host 加密副本、独立凭据、版本/删除保护和异机恢复演练。服务器真实恢复、凭据轮换和调度安装需用户后续授权。
