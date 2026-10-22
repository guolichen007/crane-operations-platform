# 工程入口

当前阶段：T0.5 协议与运行时加固（用户已授权）。验收后停止，后续阶段须用户明确授权。
不得改变现场边缘自治边界，不接真实 PLC，不实现真实远控，不公开 Edge 管理端口。
协议先更新 contracts，Cloud 与 Edge 共用契约；数据库变更必须有序 migration。
文档、提交、PR 优先中文，代码标识符英文。新功能应有有意义的测试。
禁止提交 Secret、证书、现场地址、客户资料、日志、生成物。local-private 为本地旧客户版归档，禁止上传。
使用短期功能分支和中文 Conventional Commits；不得自动合并 main。不擅自选择根开源许可证。
验证入口 node tools/task.mjs；先运行 contract、lint、test、build，再运行 Compose 集成。报告实际结果，未执行不能标为通过。

本次首次正式提交按用户要求不创建 PR（PR_URL=NOT_CREATED_BY_REQUEST），保持功能分支，不合并 main。后续是否创建 PR 由用户决定。
