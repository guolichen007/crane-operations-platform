# Phase1-A1 威胁模型

信任边界为 Browser → 共享 u-dao-ingress → Web proxy → 同一 Go BFF → PostgreSQL/Keycloak/MQTT。现场 Edge 继续独立自治；没有 PLC 接入、真实远控、Edge 公网管理端口或新消息中间件。

| 威胁 | 控制与验证 | 剩余风险/边界 |
| --- | --- | --- |
| OAuth code 截获/登录 CSRF | state+浏览器绑定、nonce、一次性5分钟事务、Code+PKCE S256、HTTPS endpoints、严格 callback/issuer/aud/azp/signature | IdP/宿主被控制仍可冒充；需后续运维管理 MFA |
| 会话窃取/fixation | 32字节随机 ID、hash-only、显式 Secure HttpOnly host Cookie、登录替换、绝对/空闲期、退出删除 | XSS 仍可代表在线用户操作，CSRF 不防 XSS；CSP 与依赖扫描只是降低风险 |
| 跨源写入 | session-bound header、常量时间比较、Origin/Referer、null拒绝、SameSite Lax | 同源恶意脚本可拿到 CSRF；不把 CSRF token 当登录凭据 |
| IDOR/跨项目管理员 | DB实际project→membership→role、列表/Alarm SQL过滤、直接对象/租约owner检查、事务内再检查 | 未授权对象404；授权变更对正在执行读取可能有最多当前请求窗口 |
| 长连接越权 | WS 建连前完整认证授权、每秒重查、撤权/过期/退出关闭1008、WS不刷新idle | 撤销可见性上界约一个轮询周期，不声称零延迟 |
| 伪造来源/代理头 | ingress覆盖 forwarded headers、应用生成request ID、只信任明确CIDR、固定PUBLIC_URL | 配错可信CIDR会破坏IP归因；不可把全网设为可信 |
| 开发身份泄漏 | production非空DEVtoken启动失败，无crane_dev/dev-session/开发CA/fallback；五组件默认集合 | 生产启动需要独立正确的私有Inventory/Fleet/信任材料，本阶段不提供生产PKI |
| Secret进入日志/制品 | secret文件0700/0600/最小UID、无浏览器OAuthtoken、回调access log关闭、DB错误SQL抑制、上游错误脱敏、artifact白名单 | root/Docker管理员可读取容器及DB；Public例外要求继续不引入真实资料 |
| 供应链替换 | Actions SHA、上游KC/PG/MQTT digest、source SHA OCI标签、TrivyHIGH/CRITICAL、SBOM checksum、registry digest、同digest运行生产演练 | digest完整性不代表软件无漏洞；定期重新扫描和重建，未声称签名/远程证明 |
| 数据丢失/磁盘满 | 双库逻辑快照、原子完成、checksum、临时DB演练、14天retention、每服务log rotation、磁盘门禁 | 当前本机备份非DR，尚需COS/off-host与监控安装；容量/RTO须持续复测 |

Audit 不保存密码、OAuth token、Session/CSRF secret、DB/client secret、私钥或整请求。授权与安全事务的审计失败会阻止成功提交；外部 IdP 操作和本地业务 DB 不宣称跨库原子。Keycloak 管理事件关闭 representation details，避免 client secret 写入事件 JSON。

没有新增 OIDC 自动注册、Active Source 状态机、Production PKI、REAL Adapter、Remote Web、PointCloud、Redis/Kafka/Kubernetes。宿主机 ingress、DNS/TLS、数据库备份调度和服务器切换均未在本轮执行。main 分支保护若当前连接不支持设置，作为 WARNING 交由人工处理，不申请额外权限阻塞验收。
