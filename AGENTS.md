# 项目报备中心工程入口

仅适用于 product/baobei-v1 独立 worktree。当前阶段 FULL_ROLE_G3_4_A1_G4_S0（产品 UAT 准备、E2E 隔离与 Preview 只读预检），START_HEAD=886f75c7bbe94f96bc6884de24b8e62cced55650。G2 见20，G3-0 见24，G3-1 见25/26，G3-2A 见27，G3-2B 见28，G3-2C 见29，G3-3-P0 见30/30-R1，G3-3A 见31，G3-3B 见32，R1 见32-R1，G3-4-A0 见33，G3-4-A1 见34，G4-S0 见35；E2E 提交到审核队列的数据必须用 tests/baobei/e2e/support/isolation.ts 的 cleanup fixture 先登记后提交，审核卡用 findCard 翻页定位；工作台只用 workspace-summary 与 stage-summary，每个数字须能点击进入同口径列表且 total 相等，不显示收入/回款/同比/赢率；向导内未纠正的无效数字不得随步骤切换被丢弃。只正常提交/push产品分支，等待同SHA原八门CI后STOP；不得自动进入G3-4其余功能或G4-S1，不得部署Preview或生产，不授权release ref或GHCR。
唯一事实源：docs/项目报备中心/00-项目总览与SSOT.md；UI 动作事实源：交互动作矩阵.md。
固定基线 29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18；永久产品分支 product/baobei-v1。
禁止合并 main / R2，禁止修改既有 Crane 代码、生产服务器、DNS、TLS、真实账号及现场数据。
允许独立产品路径及专用 CI；tools/task.mjs 仅可增加隔离 baobei 分派。不得修改 Node 基线。
独立数据库 migration 从 00001 开始，forward-only。协议优先；synthetic 测试；中文 Conventional Commits。
只允许最终 git push origin product/baobei-v1；禁止其他 push / PR / tag / force。
验证入口 node tools/task.mjs baobei <contract|lint|test|build|integration|e2e|security|compose>。
先 contract、lint、test、build 后 Compose 集成；未执行标 NOT_RUN，不得宣称 PASS。
REA evidence 仅 local-private/research/baobei-ui/，禁止提交。禁止提交凭据、日志、生成物。
