# Phase1-A1 实施与验收追踪

任务来源：用户 Phase1-A1 Production Web Foundation 全文。完整范围按下表执行；任何缺失不能用其他绿色检查替代。本表为工作追踪，结果须由实际命令和最终同 SHA CI 证明。

| 任务 | 交付与验收证据 | 当前状态 |
| --- | --- | --- |
| 0 A0 封板 | main/tag peel=d827891375cd3665041936890ec21b70f52b2b9c；原 CI 39925506488 六项成功；A1 从封板 main 建分支 | 已重新核验并执行 |
| 0 仓库权限 | 用户本轮明确要求保留 Public、跳过私有化；当前连接未提供 main 规则写入能力，不申请额外权限 | REPOSITORY_PRIVATE=NO_BY_REQUEST；main 保护留人工核验 |
| 1–2 OIDC/BFF | Code+PKCE S256、state/nonce/issuer/audience、严格 callback；issuer+sub 映射；opaque Cookie、hash-only PostgreSQL session、absolute/idle/logout | 已实现；核心 Linux PG/TLS OIDC 反证通过 |
| 3 CSRF | session-bound header、常量时间比较、Origin/Referer 第二层、null 拒绝 | 已实现；缺失/错误/跨源/null 等反证通过 |
| 4–5 项目 RBAC | scope first、四角色、逐对象/WS/Viewer owner 授权；两个用户/项目反证 | 已实现；直接对象/owner/事务内撤权检查通过 |
| 6 Bootstrap | identity bind/grant/revoke/list，单项目初始管理员、无 email 权限事实 | 已实现；身份绑定/授权/撤权与审计回滚通过 |
| 7 Audit | 登录/失败/退出/拒绝/LIVE/角色变更，可信代理来源与应用 request ID，禁止 secret 字段 | 已实现；原子审计与可信代理反证通过 |
| 8 fail-closed | production 拒绝开发 token/cookie/route/CA/fallback；旧 development 回归保留 | 已实现；生产配置与路由反证通过，完整旧回归以最终 CI 为准 |
| 9–10 Web/WS | 正式登录退出、用户/授权项目、401/403；Cookie-only WS、过期关闭与跨项目反证 | 已实现；WS 五类撤销通过；真实 Keycloak 浏览器用例纳入 release 门禁 |
| 11–14 生产配置 | 单 PG 两库/用户、Keycloak 26.8.1 digest、五组件、file secrets、可参数化域名、共享 ingress 模板、安全头 | 已实现；Compose 契约通过；未操作服务器/DNS/TLS |
| 15 Release | 保留六 CI、source SHA→digest、HIGH/CRITICAL Trivy、SBOM、release manifest、服务器仅 pull digest | 已实现；最终交付以相同 HEAD 的完整 CI 和 release artifact 为准 |
| 16–17 Ops | 两库原子备份/checksum/metadata/retention、独立 DB restore drill、服务日志轮转、磁盘升级门禁；不改 Firebot/Docker daemon | 已实现；失败/完成边界单元通过；双库恢复纳入真实生产演练门禁 |
| 18 全矩阵 | OIDC、Session、CSRF、4 roles、对象隔离、production startup/Compose、restore、全部旧回归 | 测试矩阵与隔离 runner 已实现；最终结果以同 SHA CI 为准 |
| 19 文档 | 身份权限模型、安全反证矩阵、生产部署/备份恢复手册、威胁模型 | 五份文档与索引已完成 |
| 20–21 交付 | 分逻辑中文提交、指定分支 push、完整 CI；有权限可 Draft PR；精确 SHA/digest、实际修改/迁移/警告与停止审查 | 最终提交、CI/镜像证据和停止审查在交付回复中给出 |

任何开发/集成测试仅使用匿名样例和临时测试凭据。真实 Inventory、备份、secret、日志、SBOM 生成物继续在忽略路径或经筛选的 CI artifact；不部署、不改真实 DNS/TLS、不改 Firebot、不公开开发版。
