# Phase1-A1 安全反证矩阵

结果必须以待审查 HEAD 的完整 CI 为准，不能用旧 A0 CI 或未执行测试代替。DB 测试无 A1_TEST_DATABASE_URL 时会显式 SKIP；`node tools/task.mjs a1-test` 在隔离 Linux PostgreSQL 中要求关键用例实际 PASS，发现跳过即失败。测试只使用匿名数据、随机临时凭据与 24 小时测试证书，不是生产 PKI。

| 领域 | 自动反证与期望 | 位置/入口 |
| --- | --- | --- |
| OIDC | 错误 state/nonce/PKCE/issuer/audience/azp，过期事务，错误 callback/browser binding，未知 sub/disabled user 均拒绝 | internal/webauth/manager_test.go；a1-test |
| 会话 | 正常 Cookie、hash-only、绝对/空闲过期、随机/篡改 Cookie、fixation、logout 后不可复用 | 同上 |
| Cookie | Secure/HttpOnly/Lax/Path=/，无 Domain；TLS 在 ingress 终止时仍 Secure | 同上、真实浏览器认证用例 |
| CSRF | 缺失/错误/其他 session token、跨源、Origin:null、缺少 Origin/Referer 均拒绝；正确同源/Referer 放行 | 同上 |
| 四角色 | viewer 只读，其余三角色可 LIVE；所有角色包括 admin 均不能越项目；Remote 保持关闭 | internal/identity、app/api_security_test.go |
| 对象隔离 | 用户 A=P001 viewer、B=P002 operator；列表/detail/shadow/alarm/WS/LIVE start/renew/stop 直接猜 ID 无法越权 | app/api_security_test.go |
| 租约所有权 | 同项目其他用户不能续期或关闭别人的 LIVE；成功操作必须有审计 | 同上 |
| WS 生命周期 | 跨项目、跨源、null Origin 建连拒绝；logout/absolute/idle/revoke/disabled 后关闭 1008 | 同上 |
| Audit | grant/revoke 与审计原子；审计不可写则授权变更回滚；拒绝记录和应用 request ID；不接受不可信 XFF | identity/repository_test.go、webauth/metadata 测试 |
| Production | 非空 DEV_API_TOKEN 启动失败；开发 Cookie 不认证；无 dev-session 路由；开发 CA/不安全 origin/宽松 secret 权限拒绝 | internal/platform 与 app 测试 |
| Compose | 默认恰好五服务、无模拟器/Remote/PointCloud、无 PG/MQTT host port、Web 仅 loopback、镜像固定 digest、文件 secrets、日志轮转 | node tools/task.mjs production-contract |
| 真实 IdP/生产 Web | 实际 Keycloak 26.8.1 Code+PKCE、正式 Go BFF、非 root Web、HttpOnly Cookie、JS 不可读、无 Web Storage token、401/CSRF/越权/LIVE/退出 | node tools/task.mjs a1-production-test |
| Backup/Restore | 双库快照/dump/checksum；临时独立 DB 恢复；projects/assets/users/memberships/counts、Session/Audit 列与 Keycloak 计数一致；清理仅限生成的临时库 | 同上，deploy/scripts/backup.py |
| 回归 | 合同/冻结 MQTT、backend、edge、frontend 原有单元与 E2E、完整旧 Compose 集成、依赖/镜像安全检查 | 原有六 CI job 保留 |
| Release | 完整 SHA 标签、OCI revision、HIGH/CRITICAL Trivy、CycloneDX SBOM/checksum、registry digest、发布镜像实际运行生产演练 | production-release job |

原有六项为 repository-contract/backend/edge/frontend/integration/security；新增 a1-security 与 production-release。发布 job 在七项通过后执行，只有实际发布镜像通过生产认证与恢复演练后才上传三份明确选择的清单/SBOM 文件；不上传整个 runtime、dump、证书、测试报告或镜像 tar。

本地 Windows 的 portable packaging 仅用于验证，不冒充 CI release。正式交付仅认同 SHA CI 生成的 release-manifest.json 和其中 image@sha256。失败时输出 FAIL/未执行，不以“测试已编写”替代通过。
