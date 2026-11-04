# DIRECT-D0 云服务器本地构建部署验收

启动 SHA 为 `55cbffe575bbe8396951d98db67d0ad530e4a5a6`。启动时产品分支本地与远端一致，工作区干净。[BB-B3 基线 CI](https://github.com/guolichen007/crane-operations-platform/actions/runs/41800267925) 八门通过。

交付包含本地构建和部署入口、生产 Compose 的本地拉取策略覆盖、审计清单及回滚流程。Web 和 API 使用完整提交 SHA 作为固定本地标签。清单以 Docker Engine 镜像 ID 记录构建结果。直接构建门禁核验 OCI 来源、精确 revision、架构和构建结果。

验证范围涵盖 Compose 渲染等价性、无宿主端口、缺失绑定源拒绝、失败保留上次生效版本及数据库契约变更阻止回滚。原八门 CI 保留真实 PostgreSQL 五身份、HTTP-01、34 项证书与 22 项浏览器回归。最终结果按交付提交核验。[产品分支 CI](https://github.com/guolichen007/crane-operations-platform/actions/workflows/baobei-ci.yml?query=branch%3Aproduct%2Fbaobei-v1)

本地已验证两张镜像的构建与清单、五身份数据库、HTTP-01 和 22 项 E2E。Windows Docker Desktop 的共享路径会创建缺失挂载源。直接部署入口仅在 Linux 启动，挂载预检先于 Compose，Linux CI 用真实 Docker 容器验证三处绑定挂载的拒绝和成功对照。

验收使用本地或 GitHub CI 的隔离环境。正式部署、迁移及生产 Keycloak 操作由后续获授权的部署任务执行。操作顺序及回滚条件见 [直接部署与回滚](直接部署与回滚.md)。
