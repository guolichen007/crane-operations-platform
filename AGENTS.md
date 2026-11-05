# 项目报备中心工程入口

仅适用于 product/baobei-v1 独立 worktree。当前阶段 FULL_ROLE_G1，START_HEAD=c580978b7056f95f1ac284f00b7d92f0b549f69b。负责人已批准G0并授权隔离后端、00004新增迁移及契约验证；根R2并行提交已明确要求跳过，保持原状。设计见SSOT的14—18入口，实施验收见19。只正常提交/push产品分支，等待同SHA原八门CI后STOP；不得自动进入G2，不授权生产、release ref或GHCR。
唯一事实源：docs/项目报备中心/00-项目总览与SSOT.md；UI 动作事实源：交互动作矩阵.md。
固定基线 29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18；永久产品分支 product/baobei-v1。
禁止合并 main / R2，禁止修改既有 Crane 代码、生产服务器、DNS、TLS、真实账号及现场数据。
允许独立产品路径及专用 CI；tools/task.mjs 仅可增加隔离 baobei 分派。不得修改 Node 基线。
独立数据库 migration 从 00001 开始，forward-only。协议优先；synthetic 测试；中文 Conventional Commits。
只允许最终 git push origin product/baobei-v1；禁止其他 push / PR / tag / force。
验证入口 node tools/task.mjs baobei <contract|lint|test|build|integration|e2e|security|compose>。
先 contract、lint、test、build 后 Compose 集成；未执行标 NOT_RUN，不得宣称 PASS。
REA evidence 仅 local-private/research/baobei-ui/，禁止提交。禁止提交凭据、日志、生成物。
