# 项目报备中心工程入口

仅适用于 product/baobei-v1 独立 worktree。当前阶段 BB-B2-R1 Production Closure；负责人 BB_B2_CODE_REVIEW=PASS，START_HEAD=06d911007b01fe3fda10a1bc3a6687984376fa21。仅生产收口，不开发新销售功能；完成本地验证后仅 push 产品分支、等待原八门 CI，然后 STOP。S1B_PUBLIC_DEPLOY=NOT_AUTHORIZED；禁止 workflow_dispatch 发布镜像、真实身份/证书/生产操作。
唯一事实源：docs/项目报备中心/00-项目总览与SSOT.md；UI 动作事实源：交互动作矩阵.md。
固定基线 29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18；永久产品分支 product/baobei-v1。
禁止合并 main / R2，禁止修改既有 Crane 代码、生产服务器、DNS、TLS、真实账号及现场数据。
允许独立产品路径及专用 CI；tools/task.mjs 仅可增加隔离 baobei 分派。不得修改 Node 基线。
独立数据库 migration 从 00001 开始，forward-only。协议优先；synthetic 测试；中文 Conventional Commits。
只允许最终 git push origin product/baobei-v1；禁止其他 push / PR / tag / force。
验证入口 node tools/task.mjs baobei <contract|lint|test|build|integration|e2e|security|compose>。
先 contract、lint、test、build 后 Compose 集成；未执行标 NOT_RUN，不得宣称 PASS。
REA evidence 仅 local-private/research/baobei-ui/，禁止提交。禁止提交凭据、日志、生成物。
