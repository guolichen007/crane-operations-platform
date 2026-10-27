# 工程入口

当前阶段：Phase1-A1 Production Web Foundation（用户任务书授权）。A0 已按任务书 fast-forward 封板至 d827891375cd3665041936890ec21b70f52b2b9c；A1 功能分支不得合并。代码、完整 CI、镜像交付与推送完成后停止；服务器部署、真实 DNS/TLS 与后续阶段须用户另行明确授权。用户明确要求本轮跳过仓库私有化，保留 Public 并记录例外，不引入真实凭据或客户资料。
不得改变现场边缘自治边界，不接真实 PLC，不实现真实远控，不公开 Edge 管理端口。
协议先更新 contracts，Cloud 与 Edge 共用契约；数据库变更必须有序 migration。
文档、提交、PR 优先中文，代码标识符英文。新功能应有有意义的测试。
禁止提交 Secret、证书、现场地址、客户资料、日志、生成物。local-private 为本地旧客户版归档，禁止上传。
真实 Inventory 仅存放忽略目录；公开示例仅含匿名数据。旧客户历史仅保留私有 bundle；仅推送显式核验的公共基线 tag，禁止 --all、--mirror 或批量推送所有 tag。
使用短期功能分支和中文 Conventional Commits；不得自动合并 main。不擅自选择根开源许可证。
验证入口 node tools/task.mjs；先运行 contract、lint、test、build，再运行 Compose 集成。报告实际结果，未执行不能标为通过。

按本轮 A1 任务书，如现有权限允许可创建 Draft PR 到 main；不能申请额外权限来阻塞验收，不自动合并。身份实现进入现有 Go 单体，Session 使用 PostgreSQL；不得引入 Redis/Kafka/Kubernetes 或新增微服务。
