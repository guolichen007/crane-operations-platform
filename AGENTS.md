# 工程入口

当前阶段：Phase1-A1.1-R2（成熟综合管理平台），基线 origin/main=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18，分支 feature/phase1-a1-1-r2-asset-operations。
实施路线统一为 G0-R1 → B1 → B2 → … → B10，每批固定流程：CC 本地开发/验证 → 本地 commit → STOP → OWNER 手动 push feature → GitHub CI → GPT 独立审查 → BATCH_REVIEW=PASS 才开放下一批。
当前事实源：根级 Phase1-A1.1-R2-成熟综合管理平台主蓝图.md + docs/Phase1-A1.1-R2-文档索引.md（唯一 SSOT 入口）；禁止把 DEPRECATED/旧草稿作为当前设计依据。
当前状态：G0_R1_REVIEW=PASS；R2_DESIGN_FROZEN=YES；CURRENT_BATCH=B1；B1_STARTED=YES；B2_STARTED=NO。
不进入 A2；不操作生产服务器；不签发真实证书；不修改生产 TLS/DNS/Firebot；不创建真实账号；不启用 production timer；不接真实 PLC；不开放真实远控；不开放公网 Edge 管理端口。
LOCAL_ONLY：只本地 commit，禁止 push / PR / merge main / tag / 改远端 ref；负责人手动 push。
保留 Public 例外，不引入真实凭据或客户资料。
不得改变现场边缘自治边界；协议先更新 contracts，Cloud 与 Edge 共用契约；数据库变更必须有序 migration（从 00005 起，禁改 00001~00004，additive-first、forward-only）。
文档、提交、PR 优先中文，代码标识符英文。新功能应有有意义的测试。
禁止提交 Secret、证书、现场地址、客户资料、日志、生成物、真实坐标。local-private 为本地归档，禁止上传；第三方 REA 原始 evidence 归 local-private/research/（gitignore）。
禁止新 migration 创建 component_*（统一 equipment_*）；禁引入 Redis/Kafka/Kubernetes/新增微服务/大 UI Framework/React/TresJS；AGPL（realvirtual/xeokit）只学交互不复制。
真实 Inventory 仅存放忽略目录；公开示例仅含匿名数据（synthetic 坐标）。旧客户历史仅保留私有 bundle；禁止 --all、--mirror、批量 push tag。
使用短期功能分支和中文 Conventional Commits；不得自动合并 main。不擅自选择根开源许可证。
验证入口 node tools/task.mjs；先运行 contract、lint、test、build，再运行 Compose 集成。报告实际结果，未执行不能标为通过（环境缺失标 NOT_RUN，不写 PASS）。
