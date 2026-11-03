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
