# BB-AUTH-R1 登录可靠性与失败恢复

启动基线为 `0df1fe416d9706296be7ff62ada54c730dd58d73`，本地与远端一致，工作区干净。[基线 CI](https://github.com/guolichen007/crane-operations-platform/actions/runs/41812633201)

## 登录与会话契约

浏览器导航由 BFF 返回同源 `/login?error=<固定错误码>`。取消和 OAuth 拒绝、过期与无效请求、凭证与兑换、身份未开通或停用、角色、邮箱及会话服务失败分别提示。OAuth 描述、授权码、state 和凭证留在认证协议内。API 请求继续使用固定 JSON 错误结构。

回调先一次性消费 state，再校验期限与绑定 Cookie。有效授权码经过 PKCE S256 兑换，并校验签名、issuer、audience 和 nonce。客户端角色与本地角色仍需匹配。已绑定邮箱的受控账号保留邮箱验证要求。首次激活和会话写入同事务提交，生产 Cookie 继续使用 Secure、HttpOnly 和 SameSite=Lax。

`GET /api/v1/me` 区分普通未登录、失效会话和数据库故障。前端在会话过期时打开登录页。网络或会话服务故障可重试连接。密码、找回与修改入口继续由企业认证系统提供。浏览器若在抵达企业认证站点之前断网，可返回 Baobei 登录页重新发起。

## 验证与交接

`node tools/task.mjs baobei auth-e2e` 使用已构建的 Web、一次性 PostgreSQL schema 和本机独立 HTTPS 测试 OIDC。签名令牌、绑定 Cookie 和数据库会话均由测试环境生成。覆盖正常授权、取消、过期、Cookie 缺失、nonce 与凭证、账号开通状态、角色和邮箱、认证服务故障、会话库故障、刷新及回调重放。

本地桌面和 390px 专项 E2E 共 36/36 通过，原业务 E2E 22/22 通过。五身份 PostgreSQL 权限、人员启用与恢复、管理员重新认证、安全扫描、API 契约与 73 个动作绑定通过。

本批交付为产品分支代码及测试。回滚使用上一已验收提交构建的 Web/API 镜像，沿用现有直接部署回滚程序，无数据库迁移。远端结果按交付 SHA 核验。[产品分支 CI](https://github.com/guolichen007/crane-operations-platform/actions/workflows/baobei-ci.yml?query=branch%3Aproduct%2Fbaobei-v1)
