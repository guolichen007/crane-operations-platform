# BB-STAFF-R1 主管授权设计与验收

启动基线为 `08c8af7ddc1999643ac7eb248ed1a59eff6e85f5`。[基线八门 CI](https://github.com/guolichen007/crane-operations-platform/actions/runs/41860828507)

## 授权路径

管理员在成员详情查看姓名、团队和当前角色，选择设为销售主管，填写原因。当前会话缺少近期认证证明时，进入企业重新认证。返回同一管理员、员工及版本后恢复本标签页草稿，仍需显式确认。草稿五分钟到期，记录版本变化时丢弃，服务端始终重查登录证明、角色、人员身份和版本。

既有 `PATCH /api/v1/staff/{id}` 为唯一写入路径。变更前须确认员工处于活动状态，绑定了精确的企业 UUID 和真实工作邮箱，且有受控回执后以签名企业凭证完成的激活记录。已有企业标识、尚未核验的活动员工，可由管理员选择完善企业身份，以原角色发起核验。缺少精确标识时交身份运维处理。管理员不能通过角色修改同时替换邮箱，现有项目移交门禁继续生效。

身份锁和版本号保证角色、旧会话撤销、同步任务及审计同事务更新。待同步与失败期间保持停用。待激活期间，只有精确企业身份、已验证且匹配的邮箱、唯一且正确的 Baobei Client Role 通过签名令牌校验，才激活并签发会话。旧、新角色同时存在时拒绝。角色变更后每次登录仍执行唯一角色检查。停用立即关闭本地授权并撤销会话。

## 运维回执

运维按任务版本、人员编号、精确 subject、核验邮箱与审批证据同步 Baobei Client Role。只处理审批指定的员工和 `baobei-web` Client。须核查直接和继承映射，移除旧的 Baobei 角色，确认唯一目标角色后再提交 `staff-provision --once` 的 `approve` 回执。本轮新增的角色变更及其后续开通回执须带 `clientRoles` 数组，例如主管授权用 `["baobei_manager"]`。其他回执字段及 root-only 运行方式沿用生产契约。旧邀请回执保持兼容。

Keycloak 同步失败时以 `fail` 回执记录可显示的原因，不得写入账号私密信息。管理员在成员详情查看失败原因和审计、重试同步任务。相同回执重放仅返回已处理。旧审批回执不能推进新版本任务。

## Keycloak 26.8.1 最小权限判定

官方 FGAP V2 文档显示，角色映射分别要求目标用户的 `map-roles` 和目标角色的 `map-role`。可以设计按已审核用户和四种指定角色授权的独立执行器。客户端级 `map-roles` 会覆盖角色级限制，用户侧的 `manage-users` 和其他内置管理角色也可能跳过细粒度判定。群组 `manage-members` 范围包含用户管理，不能用作只改角色的授权。配置还须排除继承的管理角色、复合角色和跨 Client 映射。[Keycloak 26.8.1 FGAP V2](https://www.keycloak.org/docs/26.8.1/server_admin/#fine-grained-admin-permissions)

生产共享 Realm 的实际策略、继承权限和用户范围尚未验证。本批使用受控人工同步，Web/API 只有原登录所需的 OIDC Client 凭据，角色流程不请求 Keycloak 管理接口。后续自动化门禁应使用同版本、同策略的脱敏隔离 Realm 验证：范围内精确用户可增加及移除允许的 Client Role；范围外用户、Crane、Firebot、Realm 角色、密码、组成员、复合角色、用户资料和 Realm 配置均拒绝。执行器还需将审批和 Outbox 版本与幂等回执绑定。官方文档说明配置机制，不能替代这些反证。

使用官方 Keycloak 26.8.1 镜像在回环端口运行 `python -B tests/baobei/keycloak-fgap.py`。临时 Realm 中，按单个用户和四个角色 UUID 配置 FGAP V2。四种角色的添加和移除均成功，17 项越权请求返回 403，包含仅有 `baobei_admin` Client Role 的令牌。实验同时验证 Keycloak 允许向同一用户映射两个 Baobei 角色。夹具证明了范围限制可以配置，未覆盖生产 Realm 的策略和继承关系。生产自动执行器仍未启用。

## 交付验证与风险

真实 PostgreSQL 和合成签名 OIDC 验证角色提升、失败、重试、双角色拒绝、降权及停用。生产模式浏览器 38/38 通过，覆盖重新认证返回、显式确认、旧会话撤销、同步失败、重试和激活。原业务 E2E 22/22、HTTP-01、五身份数据库与安全扫描通过。

受控角色变更提交时立即撤销本地会话。直接在 Keycloak 改动角色时，Baobei 在下次企业登录才重新检查令牌，已签发的本地会话继续按既有规则处理。回滚沿用上一已验收镜像及直接部署流程。恢复旧版服务前，身份运维须按审计核对本批角色变更和唯一有效 Client Role，无法核对的账号先停用。[产品分支 CI](https://github.com/guolichen007/crane-operations-platform/actions/workflows/baobei-ci.yml?query=branch%3Aproduct%2Fbaobei-v1)
