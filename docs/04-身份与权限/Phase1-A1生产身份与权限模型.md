# Phase1-A1 生产身份与权限模型

A1 在现有 Go Cloud 模块化单体内实现 BFF；PostgreSQL 同时保存业务、身份授权与服务端会话。浏览器只使用同源 Cookie，不持有 OAuth access/refresh/ID token，不使用 Web Storage 保存认证材料。不增加 Redis、独立 Resource Server 或微服务。

## 登录与会话

`GET /auth/login` 生成 state、浏览器绑定值、nonce、PKCE S256。登录事务有效期五分钟、一次性消费，verifier 使用服务端密钥 AES-GCM 加密后保存。OIDC discovery/token/JWKS 必须 HTTPS，禁止重定向降级，校验 issuer、RS256 签名、audience、azp、nonce、到期时间及精确 callback。未知或 disabled 的 issuer+sub 直接拒绝，不创建授权。

`GET /auth/callback` 成功后生成新的密码学随机 opaque Session ID。数据库只保存域分离 HMAC-SHA256 hash 和 CSRF hash，不保存原始 Session ID。旧浏览器会话在登录成功时删除，避免 fixation。Cookie 固定为 `__Host-crane_session; Secure; HttpOnly; SameSite=Lax; Path=/`，无 Domain；Secure 与 Go 请求是否经过 TLS 无关。

`GET /auth/session` 返回显示身份、显式项目授权、CSRF token 和绝对/空闲到期时间。默认绝对八小时、空闲三十分钟；活动只能将 idle 延长到 absolute 之前。HEAD 不刷新空闲期。GET 登录、callback 和 session 的写入只涉及安全事务，不修改业务资源。

`POST /auth/logout` 校验 CSRF 后原子删除会话并写入审计，Cookie 立即过期。204 的 Location 可指向 Keycloak RP logout，只有 client_id 和精确 post_logout_redirect_uri，不向浏览器输出 ID token；Keycloak 可能要求用户确认退出。

## CSRF 与项目授权

所有 POST/PUT/PATCH/DELETE（包括 LIVE start/renew/stop、logout）要求 session-bound `X-CSRF-Token`，固定长度并常量时间比较。Origin 必须等于配置的业务 origin；没有 Origin 时检查 Referer。`Origin: null`、跨源、缺少两者均拒绝，Referer 不能覆盖错误 Origin。

| 项目内操作 | viewer | operator | maintainer | platform_admin |
| --- | --- | --- | --- | --- |
| 项目/资产/Runtime/Shadow/Alarm/WS 读取 | 是 | 是 | 是 | 是 |
| 自己的 LIVE start/renew/stop | 否 | 是 | 是 | 是 |
| 后续维护权限预留 | 否 | 否 | 预留 | 预留 |
| 当前真实远控/Remote Web/PointCloud | 否 | 否 | 否 | 否 |
| 自动读取未授权项目 | 否 | 否 | 否 | 否 |

每个用户对每个项目只有一个显式角色。先由 DB 实际设备或租约确定 project_id，再检查 membership，最后检查 role；不信任前端传入的 project。列表在返回前按 membership 过滤；Alarm 在 SQL LIMIT 之前过滤。跨项目和非本人 LIVE 租约返回 404，项目内 viewer 写入返回 403。LIVE repository 在事务内再次锁定并检查用户、会话和授权，防止中途撤权后写入。

WebSocket 使用相同 HttpOnly Cookie，连接前检查 Origin、session、DB 项目和读取权限；每秒重查会话、用户 enabled 与项目 membership。退出、过期、撤权、禁用用户后以 1008 关闭；WS 帧不延长空闲期。

## 映射与审计

`users` 的永久外部键为 issuer+sub，email 只可作为显示信息。`tools/identity` 提供 bind、grant、revoke、list-grants；这是宿主机管理员工具，不是公开授权 API。bind 不自动赋权，grant 必须指定一个项目。首次管理员只对一个验收项目赋 platform_admin。

审计事件包括 login_success、login_failure、logout、authorization_denied、live_start、live_renew、live_stop、project_role_changed；字段包括 actor/project/device/action/result/observed_at/source_ip/request_id。角色变更、会话创建/删除及 LIVE 成功写入与对应审计在同一事务中提交。预留 device_enrollment、certificate_revocation 事件名，本阶段不实现流程。

request_id 由应用生成。只有 RemoteAddr 属于显式 trusted proxy CIDR 时才解析 X-Forwarded-For，并从右向左剥离可信代理；不信任用户提供的 request ID、Forwarded、Host 或 Proto 作为认证事实。共享 ingress 覆盖 forwarded headers。若请求经过 Web 容器，trusted proxy 配置还必须包括该受控代理网络，不能配置 0.0.0.0/0。

迁移为 `00004_web_identity.sql`：扩展 users、增加 project_memberships/web_sessions/oidc_login_transactions、扩展 audit_logs。旧 development user_roles 保留给原有回归，production 完全不使用该全局角色表。迁移向前修复，禁止自动 destructive down。

## 生产关闭规则

APP_ENV=production 发现非空 DEV_API_TOKEN 即启动失败；不接受 crane_dev/Bearer 开发凭据，不注册 dev-session，不接受开发 CA，不允许 Inventory/Fleet 缺失或匿名 example fallback。默认不启动 simulator、remote-gateway、pointcloud-bridge。SIMULATION/REAL/TEST 来源语义保持 A0 模型不变。

Secret 从受限 file-backed secrets 读取；目录 0700、文件 0600，服务按 UID 最小授权。日志、审计、浏览器与 artifact 不保存密码、OAuth token、Session/CSRF secret、DB/client secret 或私钥。密钥变更会使既有 Session hash 失效，应安排重新登录。
