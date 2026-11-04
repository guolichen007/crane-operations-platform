# Keycloak 26.x 生产配置契约

本契约描述后续经批准的配置；本轮未访问或修改真实 Keycloak。BFF 验证 ID Token 的签名、issuer、audience、expiry、nonce，并使用 resource_access.baobei-web.roles。access token 或 Realm role 不可替代该授权来源。

| 项目 | 冻结值 |
| --- | --- |
| Client ID | baobei-web |
| Client type | OpenID Connect |
| Client authentication | ON（confidential，secret 仅通过外部文件） |
| Standard flow | ON（Authorization Code + PKCE S256） |
| Direct access grants | OFF |
| Implicit flow | OFF |
| Valid Redirect URI | https://baobei.youdaozd.com/auth/callback（精确，无 wildcard） |
| Web Origin | https://baobei.youdaozd.com（精确） |
| Full Scope Allowed | OFF |
| Issuer | https://crane.youdaozd.com/realms/crane |

创建四个 baobei-web Client Roles：baobei_sales、baobei_manager、baobei_director、baobei_admin。只能由负责人按审批分配用户角色，不授予其他 client/realm role 作为替代。

在 Clients → baobei-web → Client scopes → dedicated scope 的 Mappers 中显式建立：

| Mapper 项目 | 值 |
| --- | --- |
| Mapper type | User Client Role |
| Client ID | baobei-web |
| Multivalued | true |
| Token Claim Name | resource_access.${client_id}.roles |
| Claim JSON Type | String |
| Add to ID token | true |
| Add to access token | true |

角色前缀留空，不创建同 claim 的冲突 mapper。不可仅依赖默认 roles scope 中只向 access token 写角色的 mapper。保持 Full Scope Allowed=OFF，并在 dedicated scope 的 Scope 页显式添加 **这四个 baobei-web client roles** 的 role scope mappings；用户角色分配与 scope mappings 两者都要存在，不能通过把 Full Scope Allowed 改 ON 来排障。用 Evaluate 检查生成 ID Token 和 access token 的 resource_access.baobei-web.roles，再以专用 synthetic 用户验证；未满足则 STOP。

验收矩阵：Realm role only → DENY；other client role → DENY；baobei-web client role in ID Token 且 DB 匹配 active 用户 → ACCEPT。即使 client role 正确，DB 缺用户、角色不匹配、inactive 也均为 403；没有首次登录自动建用户。Identity Bootstrap 遵循生产部署契约.md，subject 使用 Keycloak UUID/sub。

真实结构的匿名 fixture：services/project-intake-api/testdata/keycloak-id-token.synthetic.json。包含 typ=ID、aud/azp、UUID sub、realm_access、resource_access.baobei-web 和 account；测试时临时签名并替换 issuer/time/nonce，fixture 不含真实 token/secret。

官方依据：[Keycloak 26.3.5 role scope mappings](https://www.keycloak.org/docs/26.3.5/server_admin/#_role_scope_mappings)。配置应以部署的 26.x 小版本 UI 与 Evaluate 结果核验，角色契约不变。

## BB-B3 企业身份开通与恢复

Baobei 请求 `openid profile email`。由负责人在专用 Client 的测试账号上检查 ID Token 中的 `email` 和布尔 `email_verified`。高级角色操作需要从 Baobei 显式发起 `prompt=login`、`max_age=0`，并收到与该次 nonce 绑定且在五分钟内的签名 `auth_time`。

自动开户在共享 Realm 的最小权限隔离尚待 Keycloak 26.8.x 验证。新增邀请或恢复申请显示待管理员开通。负责人通过已批准的身份流程，核对人员归属、精确 UUID `sub`、唯一工作邮箱和预期的 `baobei-web` Client Role。邮箱重复、归属不明或无法确认专属角色时暂停处理，使用失败回执记录原因。

通过独立维护 DSN，在 Linux 一次性容器以 root 运行以下命令。回执遵循生产部署契约中的文件属主和权限规则。先 dry-run，再用同一份获批文件正式执行。

```bash
baobei maintenance staff-provision --once --file /run/baobei/receipt.json \
  --operator "$APPROVED_OPERATOR_ID" --dry-run
```

```json
{
  "staffId": "66666666-6666-4666-8666-666666666666",
  "version": 1,
  "operation": "approve",
  "subject": "77777777-7777-4777-8777-777777777777",
  "email": "staff@example.test",
  "identityVerified": true,
  "uniqueEmail": true,
  "approvalReference": "SYNTHETIC-B3-APPROVAL",
  "reason": "合成示例，核对企业身份及 Baobei Client Role"
}
```

以上全为合成值。使用人员详情中的编号、当前版本和邮箱。开通回执通过后显示待企业账号激活，成员完成企业登录后生效。已绑定人员只允许同一 `sub` 恢复。停用即时关闭本地会话，负责人经批准移除该人员的 Baobei 专属 Client Role，再提交 `ack-suspend`。从未绑定的账号使用空 `subject` 确认停用。处理失败提交 `fail` 和空 `subject`，在人员详情发起重试。同一字节内容的已处理回执可安全重放，版本、账号或邮箱不符的回执拒绝。真实回执仅存受控目录。

部署前的隔离验证依据：[Keycloak 26.8 服务端管理文档](https://www.keycloak.org/docs/26.8.0/server_admin/)。
